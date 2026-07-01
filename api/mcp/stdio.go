package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// stdioTransport implements JSON-RPC over stdin/stdout.
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

// ConnectStdio connects to an MCP server via stdio transport.
func ConnectStdio(ctx context.Context, name, command string, args []string) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stderr = os.Stderr

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
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewScanner(stdout),
	}

	c := &Client{
		name:      name,
		transport: tr,
		idCounter: 1,
	}

	if err := c.initialize(ctx); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	c.connected = true
	return c, nil
}

func (tr *stdioTransport) roundTrip(_ context.Context, req jsonrpcRequest) (*jsonrpcResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := fmt.Fprintf(tr.stdin, "%s\n", data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	type result struct {
		resp *jsonrpcResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		if tr.stdout.Scan() {
			var resp jsonrpcResponse
			if err := json.Unmarshal(tr.stdout.Bytes(), &resp); err != nil {
				ch <- result{nil, err}
				return
			}
			ch <- result{&resp, nil}
		} else {
			ch <- result{nil, fmt.Errorf("connection closed")}
		}
	}()

	select {
	case r := <-ch:
		return r.resp, r.err
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

func (tr *stdioTransport) close() error {
	if tr.cmd != nil && tr.cmd.Process != nil {
		return tr.cmd.Process.Kill()
	}
	return nil
}
