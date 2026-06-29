// Package mcp provides the public API for MCP (Model Context Protocol) integration.
//
// This package is self-contained with no dependency on internal/.
// External projects can use it to connect to MCP servers and call tools.
//
// Usage:
//
//	import "github.com/Timwood0x10/ares/api/mcp"
//
//	client, err := mcp.ConnectStdio(ctx, "my-server", "codegraph", []string{"serve", "--mcp"})
//	tools, _ := client.ListTools(ctx)
//	result, _ := client.CallTool(ctx, "tool_name", map[string]any{"key": "value"})
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Client connects to an MCP server and provides tool access.
type Client struct {
	name      string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Scanner
	mu        sync.Mutex
	idCounter int
	tools     []ToolInfo
	connected bool
}

// ToolInfo describes a tool exposed by an MCP server.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CallResult is the result of calling an MCP tool.
type CallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"is_error"`
}

// ContentBlock represents a content block in a tool result.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
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

	c := &Client{
		name:      name,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    bufio.NewScanner(stdout),
		idCounter: 1,
	}

	// Initialize handshake.
	if err := c.initialize(ctx); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	c.connected = true
	return c, nil
}

func (c *Client) initialize(ctx context.Context) error {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "ares-mcp-client",
				"version": "1.0.0",
			},
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	// Send initialized notification.
	notif := jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	_ = c.sendNotification(notif)

	return nil
}

// ListTools returns all tools exposed by the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/list",
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("list tools error: %s", resp.Error.Message)
	}

	var result struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	c.tools = result.Tools
	return c.tools, nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallResult, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/call",
		Params: map[string]any{
			"name":      name,
			"arguments": args,
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return &CallResult{IsError: true}, fmt.Errorf("call tool error: %s", resp.Error.Message)
	}

	var result CallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	return &result, nil
}

// Name returns the server name.
func (c *Client) Name() string {
	return c.name
}

// Close closes the MCP connection.
func (c *Client) Close() error {
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}

// ── JSON-RPC Protocol ────────────────────────────────────

type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) nextID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.idCounter
	c.idCounter++
	return id
}

func (c *Client) sendRequest(ctx context.Context, req jsonrpcRequest) (*jsonrpcResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := fmt.Fprintf(c.stdin, "%s\n", data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read response with timeout.
	type result struct {
		resp *jsonrpcResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		if c.stdout.Scan() {
			var resp jsonrpcResponse
			if err := json.Unmarshal(c.stdout.Bytes(), &resp); err != nil {
				ch <- result{nil, err}
				return
			}
			ch <- result{&resp, nil}
		} else {
			ch <- result{nil, fmt.Errorf("connection closed")}
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.resp, r.err
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

func (c *Client) sendNotification(notif jsonrpcNotification) error {
	data, err := json.Marshal(notif)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.stdin, "%s\n", data)
	return err
}

// ServerConfig holds MCP server connection configuration.
type ServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// ConnectFromConfig connects to an MCP server from a ServerConfig.
func ConnectFromConfig(ctx context.Context, cfg ServerConfig) (*Client, error) {
	if cfg.Command != "" {
		return ConnectStdio(ctx, cfg.Name, cfg.Command, cfg.Args)
	}
	if cfg.URL != "" {
		return nil, fmt.Errorf("SSE transport not yet supported in public API")
	}
	return nil, fmt.Errorf("either command or url is required")
}

// DiscoverServers scans ~/.claude.json for MCP server definitions.
func DiscoverServers(projectDir string) []ServerConfig {
	home, _ := os.UserHomeDir()
	var servers []ServerConfig
	seen := make(map[string]bool)

	// ~/.claude.json
	if home != "" {
		servers = append(servers, scanClaudeConfig(home+"/.claude.json", seen)...)
	}
	// Project .claude/settings.json
	if projectDir != "" {
		servers = append(servers, scanClaudeConfig(projectDir+"/.claude/settings.json", seen)...)
	}
	return servers
}

func scanClaudeConfig(path string, seen map[string]bool) []ServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	var servers []ServerConfig
	for name, sc := range cfg.MCPServers {
		if seen[name] {
			continue
		}
		seen[name] = true
		servers = append(servers, ServerConfig{
			Name:    name,
			Command: sc.Command,
			Args:    sc.Args,
		})
	}
	return servers
}
