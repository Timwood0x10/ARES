package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// stdioMaxFrameBytes bounds one MCP frame on the stdio transport.
//
// MCP frames are single-line JSON, so bufio.Scanner's default
// MaxScanTokenSize (64KB) silently truncated any larger response: Scan()
// failed and roundTrip reported "connection closed" while the child process
// was perfectly healthy, killing every subsequent call on that client. 64MB
// matches what the SSE transport's documentation already claimed this guard
// was — the claim was previously false.
const stdioMaxFrameBytes = 64 << 20

// newStdioScanner builds the frame scanner for a stdio connection with the
// transport's real size bound applied. Every construction site must use it:
// a bare bufio.NewScanner reintroduces the 64KB truncation.
func newStdioScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), stdioMaxFrameBytes)
	return sc
}

// stdioTransport implements JSON-RPC over stdin/stdout.
//
// Frames are read by ONE goroutine that dispatches responses to the waiting caller by id. The
// earlier design started a scanner per call and relied on the client mutex for serialization —
// which broke the moment a call timed out: the caller returned and released the mutex while its
// scanner was still reading. To unblock that scanner the timeout path closed stdin, and closing it
// is fatal (it is the child's input pipe): every later call failed to write, and there was no
// reconnect, so one slow response retired that MCP server for the life of the process.
//
// A timeout now only removes the waiter. The frame arrives later, finds no waiter, and is dropped.
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	name   string
	stdout *bufio.Scanner

	mu      sync.Mutex
	pending map[int]chan rpcResult
	closed  bool

	// writeMu serializes frame writes: roundTrip and notify can interleave, and a JSON-RPC line
	// split across two writers would corrupt the stream.
	writeMu      sync.Mutex
	readerDone   chan struct{}
	readerActive bool
}

// rpcResult is one response (or read error) delivered to a waiting call.
type rpcResult struct {
	resp *jsonrpcResponse
	err  error
}

// errConnectionClosed is returned when the child's stdout reached EOF while a call was waiting.
var errConnectionClosed = errors.New("connection closed")

// ConnectStdio connects to an MCP server via stdio transport.
//
// ctx bounds the connection handshake ONLY (the initialize round-trip). The
// subprocess's lifetime is owned by Client.Close, deliberately NOT by ctx:
// exec.CommandContext ties the process to the context it was built with, so
// binding it to the caller's connect-timeout context meant the cancel that
// follows a successful connect (the SDK's wireMCPClients cancels its 30s
// connect context immediately) KILLED the server — every later CallTool died
// with "broken pipe" and WithMCP was unusable (F-02). A handshake failure
// still kills the child here; after a successful connect, only Close does.
func ConnectStdio(ctx context.Context, name, command string, args []string) (*Client, error) {
	resolvedCommand, err := ResolveStdioCommand(command)
	if err != nil {
		return nil, err
	}
	command = resolvedCommand

	// Detached lifetime, bounded handshake: the process runs until Close,
	// while initialize below still runs under the caller's ctx (deadline,
	// cancellation) and kills the child on failure.
	//nolint:noctx,gosec // F-02: deliberately NOT CommandContext — binding the
	// process to the caller's connect-timeout ctx made the post-connect
	// cancel KILL the server (broken pipe on every later call). The
	// handshake stays bounded via initialize(ctx); the process is owned by
	// Client.Close. IsAbs check above guards the binary path.
	cmd := exec.Command(command, args...)
	// Attributed and bounded: several MCP servers run at once, and a child that logs in a loop must
	// not be able to bury the parent's structured log (see stderrDrain).
	cmd.Stderr = newStderrDrain(name, os.Stderr)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	tr := &stdioTransport{
		cmd:        cmd,
		stdin:      stdin,
		name:       name,
		stdout:     newStdioScanner(stdout),
		pending:    make(map[int]chan rpcResult),
		readerDone: make(chan struct{}),
	}
	// start the reader BEFORE the handshake: initialize is a round-trip like any other.
	tr.ensureReader()

	c := &Client{
		name:      name,
		transport: tr,
		idCounter: 1,
	}

	if err := c.initialize(ctx); err != nil {
		_ = cmd.Process.Kill() //nolint: errcheck
		return nil, fmt.Errorf("initialize: %w", err)
	}

	c.connected = true
	return c, nil
}

// frameID extracts the JSON-RPC id of a raw frame. Notifications carry no id
// (nil); requests/responses carry one. The id is compared as raw JSON so
// string-typed server ids cannot be confused with our integer ids.
func frameID(raw []byte) (json.RawMessage, bool) {
	var env struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false
	}
	return env.ID, len(env.ID) > 0
}

func (tr *stdioTransport) roundTrip(ctx context.Context, req jsonrpcRequest) (*jsonrpcResponse, error) {
	tr.ensureReader()
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan rpcResult, 1)
	tr.mu.Lock()
	if tr.closed {
		tr.mu.Unlock()
		return nil, errConnectionClosed
	}
	tr.pending[req.ID] = ch
	tr.mu.Unlock()
	// The waiter leaves on every exit path, including a timeout: a late response then finds no
	// entry and is discarded by the reader instead of blocking it.
	defer func() {
		tr.mu.Lock()
		delete(tr.pending, req.ID)
		tr.mu.Unlock()
	}()

	if err := tr.writeFrame(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-ctx.Done():
		// The caller gave up. The CONNECTION does not: the reader keeps running, so the next call
		// works and the responses of both are still matched by id.
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for response")
	case <-tr.readerDone:
		return nil, errConnectionClosed
	}
}

// requestTimeout bounds one round-trip when the caller's context has no deadline of its own.
const requestTimeout = 30 * time.Second

// writeFrame writes one JSON-RPC frame, bounded.
//
// The pipe write is a syscall on an *os.File, so it carries a deadline when the platform supports
// one; that bounds the write without abandoning a goroutine and, crucially, without destroying the
// connection. A wedged child is reported to the caller and the transport stays usable — the bytes
// may be lost, but a server that stops draining its stdin is already misbehaving.
func (tr *stdioTransport) writeFrame(frame []byte) error {
	tr.writeMu.Lock()
	defer tr.writeMu.Unlock()

	frame = append(frame, '\n')
	if deadline, ok := tr.stdin.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = deadline.SetWriteDeadline(time.Now().Add(requestTimeout))
		defer func() { _ = deadline.SetWriteDeadline(time.Time{}) }()
	}
	if _, err := tr.stdin.Write(frame); err != nil {
		return err
	}
	return nil
}

// ensureReader starts the single reader the first time it is needed.
//
// Idempotent so a transport built directly (tests) works the same as one built by ConnectStdio —
// the reader is what matches responses to waiters, so a transport without it can only time out.
func (tr *stdioTransport) ensureReader() {
	tr.mu.Lock()
	if tr.pending == nil {
		tr.pending = make(map[int]chan rpcResult)
	}
	if tr.readerActive {
		tr.mu.Unlock()
		return
	}
	tr.readerActive = true
	if tr.readerDone == nil {
		tr.readerDone = make(chan struct{})
	}
	tr.mu.Unlock()
	go tr.readLoop()
}

// readLoop owns stdout for the life of the transport: one reader, responses dispatched by id.
func (tr *stdioTransport) readLoop() {
	defer close(tr.readerDone)
	for tr.stdout.Scan() {
		raw := append([]byte(nil), tr.stdout.Bytes()...)
		id, hasID := frameID(raw)
		if !hasID {
			continue // notification or server-initiated request: nothing to dispatch
		}
		var requestedID int
		if err := json.Unmarshal(id, &requestedID); err != nil {
			continue
		}

		tr.mu.Lock()
		ch := tr.pending[requestedID]
		tr.mu.Unlock()
		if ch == nil {
			// A response nobody is waiting for: the call timed out, and dropping the frame is the
			// whole point of keeping the connection alive.
			continue
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			ch <- rpcResult{nil, err}
			continue
		}
		ch <- rpcResult{&resp, nil}
	}
	// EOF or a scan failure: every waiter learns the connection is gone.
	tr.mu.Lock()
	tr.closed = true
	for id, ch := range tr.pending {
		ch <- rpcResult{nil, errConnectionClosed}
		delete(tr.pending, id)
	}
	tr.mu.Unlock()
}

func (tr *stdioTransport) notify(ctx context.Context, notif jsonrpcNotification) error {
	data, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := tr.writeFrame(data); err != nil {
		return fmt.Errorf("write notification: %w", err)
	}
	return nil
}

func (tr *stdioTransport) close() error {
	if tr.cmd == nil || tr.cmd.Process == nil {
		return nil
	}
	tr.mu.Lock()
	tr.closed = true
	tr.mu.Unlock()
	// Close stdin to signal the child to exit, then kill to force-terminate.
	_ = tr.stdin.Close()
	if err := tr.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill process: %w", err)
	}
	// Wait reaps the child process, preventing zombies.
	_ = tr.cmd.Wait()
	return nil
}

// ResolveStdioCommand turns a configured command into the absolute path to execute, applying the
// single rule both stdio call sites share.
//
// The command comes from configuration, not from a model, so this is not an injection boundary — it
// is a consistency boundary, and the two halves of it are different:
//
//   - A path RELATIVE TO THE WORKING DIRECTORY ("./helper", "../bin/x", "bin/x") is refused. It
//     silently means a different binary depending on where the process was started, which is the
//     case that turns a configuration mistake into "we ran something else".
//   - A BARE NAME ("codegraph") is resolved through PATH and the resolved absolute path is what
//     gets executed. Refusing it would break real deployments: this repository's own example
//     configuration and documentation use bare names, and Config.Validate only requires non-empty.
//     Resolving pins the binary at start, so a later PATH change cannot swap it.
//
// The SDK path and the production transport both go through here, so the same ares.yaml behaves the
// same way in both.
func ResolveStdioCommand(command string) (string, error) {
	if command == "" {
		return "", errors.New("command is required")
	}
	if filepath.IsAbs(command) {
		return command, nil
	}
	// Anything with a separator is a path, not a name: only names may be looked up, because a
	// lookup of "./x" would resolve against the working directory.
	if strings.ContainsRune(command, filepath.Separator) || strings.ContainsRune(command, '/') {
		return "", fmt.Errorf("command must be an absolute path, got: %s", command)
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("command %q not found on PATH: %w", command, err)
	}
	if !filepath.IsAbs(resolved) {
		// LookPath returns a path relative to the current directory when the match came from a
		// relative PATH entry; make it absolute so the pin is real.
		abs, absErr := filepath.Abs(resolved)
		if absErr != nil {
			return "", fmt.Errorf("command %q resolved to a relative path %q: %w", command, resolved, absErr)
		}
		resolved = abs
	}
	return resolved, nil
}
