package ares_mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Default buffer size for reading subprocess stdout lines (1 MB).
const stdoutBufferSize = 1024 * 1024

// StdioConfig holds configuration for a stdio-based MCP transport.
type StdioConfig struct {
	Command string            `yaml:"command" json:"command"`
	Args    []string          `yaml:"args" json:"args"`
	Env     map[string]string `yaml:"env" json:"env"`
	WorkDir string            `yaml:"work_dir" json:"work_dir"`
}

// StdioTransport implements Transport by communicating with an MCP server
// over stdin/stdout of a subprocess.
type StdioTransport struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Scanner
	stdoutPipe io.ReadCloser
	stderr     io.ReadCloser
	config     StdioConfig
	mu         sync.Mutex
	started    atomic.Bool
	receiveMu  sync.Mutex
	stderrWg   sync.WaitGroup
}

// NewStdioTransport creates a new stdio transport with the given config.
func NewStdioTransport(config StdioConfig) *StdioTransport {
	return &StdioTransport{
		config: config,
	}
}

// Start launches the subprocess and prepares stdin/stdout for communication.
func (t *StdioTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started.Load() {
		return fmt.Errorf("transport already started")
	}

	if t.config.Command == "" {
		return fmt.Errorf("command is required")
	}

	t.cmd = exec.CommandContext(ctx, t.config.Command, t.config.Args...) // #nosec G204

	if t.config.WorkDir != "" {
		t.cmd.Dir = t.config.WorkDir
	}

	if len(t.config.Env) > 0 {
		// Preserve parent process environment and append custom variables.
		// This ensures the subprocess has PATH and other essential variables.
		t.cmd.Env = append(os.Environ(), convertEnvVars(t.config.Env)...)
	}

	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := t.cmd.StdoutPipe()
	if err != nil {
		_ = t.stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	t.stdoutPipe = stdoutPipe
	t.stdout = bufio.NewScanner(stdoutPipe)
	t.stdout.Buffer(make([]byte, 0, stdoutBufferSize), stdoutBufferSize)

	t.stderr, err = t.cmd.StderrPipe()
	if err != nil {
		_ = t.stdin.Close()
		_ = t.stdoutPipe.Close()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := t.cmd.Start(); err != nil {
		_ = t.stdin.Close()
		_ = t.stdoutPipe.Close()
		_ = t.stderr.Close()
		return fmt.Errorf("start process: %w", err)
	}

	t.started.Store(true)

	// Drain stderr in background to prevent blocking, logging output for diagnostics.
	t.stderrWg.Add(1)
	go func() {
		defer t.stderrWg.Done()
		scanner := bufio.NewScanner(t.stderr)
		for scanner.Scan() {
			log.Debug("mcp: subprocess stderr", "command", t.config.Command, "line", scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			log.Warn("mcp: error reading subprocess stderr", "command", t.config.Command, "error", err)
		}
	}()

	return nil
}

// Send encodes and writes a JSON-RPC message to the subprocess stdin.
func (t *StdioTransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started.Load() {
		return fmt.Errorf("transport not started")
	}

	data, err := Encode(msg)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	// Write message followed by newline as delimiter.
	data = append(data, '\n')
	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}

	return nil
}

// Receive reads the next JSON-RPC message from the subprocess stdout.
func (t *StdioTransport) Receive(ctx context.Context) (*JSONRPCMessage, error) {
	// Check context before acquiring lock to avoid deadlock with Close.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	t.receiveMu.Lock()
	defer t.receiveMu.Unlock()

	// Re-check after acquiring lock; state may have changed.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !t.started.Load() {
		return nil, fmt.Errorf("transport not started")
	}

	// Use a channel to make the blocking scan interruptible.
	type scanResult struct {
		data []byte
		err  error
	}

	ch := make(chan scanResult, 1)
	var scanWg sync.WaitGroup
	scanWg.Add(1)
	go func() {
		defer scanWg.Done()
		if t.stdout.Scan() {
			ch <- scanResult{data: t.stdout.Bytes()}
		} else {
			ch <- scanResult{err: t.stdout.Err()}
		}
	}()

	select {
	case <-ctx.Done():
		// Close stdout pipe to unblock the scanning goroutine so it can exit.
		// Without this, t.stdout.Scan() blocks forever on pipe read,
		// causing scanWg.Wait() to hang and leak the goroutine.
		t.closeStdoutPipe()
		scanWg.Wait()
		return nil, ctx.Err()
	case result := <-ch:
		if result.err != nil {
			return nil, fmt.Errorf("read stdout: %w", result.err)
		}
		msg, err := Decode(result.data)
		if err != nil {
			return nil, fmt.Errorf("decode message: %w", err)
		}
		return msg, nil
	}
}

// closeStdoutPipe closes the stdout pipe to unblock the scanner goroutine.
// The pipe's Close method is safe for concurrent calls on *os.File —
// double-close returns os.ErrClosed which is discarded here.
// This is used from both Receive() (ctx cancellation) and Close() (shutdown).
func (t *StdioTransport) closeStdoutPipe() {
	if t.stdoutPipe != nil {
		_ = t.stdoutPipe.Close()
	}
}

// Close terminates the subprocess and cleans up resources.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started.Load() {
		return nil
	}

	t.started.Store(false)

	t.closeStdoutPipe()

	if t.stdin != nil {
		_ = t.stdin.Close()
	}

	if t.stderr != nil {
		_ = t.stderr.Close()
	}

	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
	}

	t.stderrWg.Wait()

	return nil
}

// convertEnvVars converts map[string]string to []string format for exec.Cmd.Env.
func convertEnvVars(env map[string]string) []string {
	vars := make([]string, 0, len(env))
	for k, v := range env {
		vars = append(vars, k+"="+v)
	}
	return vars
}

// Ensure StdioTransport implements Transport at compile time.
var _ Transport = (*StdioTransport)(nil)
