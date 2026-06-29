// Package mcp provides the public API for MCP (Model Context Protocol) integration.
//
// External projects can use this package to:
//   - Connect to MCP servers (stdio or SSE transport)
//   - List and call tools exposed by MCP servers
//   - Register MCP tools into an api/tools.Registry
//
// Usage:
//
//	import "github.com/Timwood0x10/ares/api/mcp"
//
//	// Connect to an MCP server via stdio
//	client, err := mcp.ConnectStdio(ctx, "my-server", "codegraph", []string{"serve", "--mcp"})
//
//	// Or from config
//	client, err := mcp.ConnectFromConfig(ctx, mcp.ServerConfig{
//	    Name:    "codegraph",
//	    Command: "codegraph",
//	    Args:    []string{"serve", "--mcp"},
//	})
//
//	// List tools
//	tools, _ := client.ListTools(ctx)
//
//	// Call a tool
//	result, _ := client.CallTool(ctx, "codegraph_files", map[string]any{"query": "*.go"})
//
//	// Register all MCP tools into an api/tools.Registry
//	client.RegisterTools(ctx, registry)
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
)

// ServerConfig holds MCP server connection configuration.
type ServerConfig struct {
	Name    string   `json:"name"`              // Server name (used as prefix in tool names)
	Command string   `json:"command,omitempty"` // For stdio: command to run
	Args    []string `json:"args,omitempty"`    // For stdio: command arguments
	URL     string   `json:"url,omitempty"`     // For SSE: server URL
	Timeout int      `json:"timeout,omitempty"` // Connection timeout in seconds (default: 30)
}

// Client wraps an MCP server connection.
type Client struct {
	inner      *ares_mcp.MCPClient
	serverName string
}

// ConnectStdio connects to an MCP server via stdio transport.
func ConnectStdio(ctx context.Context, name, command string, args []string) (*Client, error) {
	return ConnectFromConfig(ctx, ServerConfig{
		Name:    name,
		Command: command,
		Args:    args,
	})
}

// ConnectSSE connects to an MCP server via SSE transport.
func ConnectSSE(ctx context.Context, name, url string) (*Client, error) {
	return ConnectFromConfig(ctx, ServerConfig{
		Name: name,
		URL:  url,
	})
}

// ConnectFromConfig connects to an MCP server from a ServerConfig.
func ConnectFromConfig(ctx context.Context, cfg ServerConfig) (*Client, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("server name is required")
	}

	timeout := 30 * time.Second
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}

	client := ares_mcp.NewMCPClient(ares_mcp.MCPClientConfig{
		ServerName: cfg.Name,
		Timeout:    timeout,
	})

	transportCfg := ares_mcp.TransportConfig{}
	if cfg.Command != "" {
		transportCfg.Type = "stdio"
		transportCfg.Stdio = &ares_mcp.StdioConfig{
			Command: cfg.Command,
			Args:    cfg.Args,
		}
	} else if cfg.URL != "" {
		transportCfg.Type = "sse"
		transportCfg.SSE = &ares_mcp.SSEConfig{
			URL: cfg.URL,
		}
	} else {
		return nil, fmt.Errorf("either command (stdio) or url (sse) is required")
	}

	transport, err := ares_mcp.NewTransportFromConfig(transportCfg)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	if err := client.Connect(ctx, transport); err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	return &Client{inner: client, serverName: cfg.Name}, nil
}

// ToolInfo describes an MCP tool.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListTools returns all tools exposed by the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	defs, err := c.inner.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]ToolInfo, len(defs))
	for i, d := range defs {
		infos[i] = ToolInfo{Name: d.Name, Description: d.Description}
	}
	return infos, nil
}

// ContentBlock represents a content block returned by an MCP tool.
type ContentBlock struct {
	Type     string `json:"type"`               // "text", "image", etc.
	Text     string `json:"text,omitempty"`     // Text content
	MimeType string `json:"mimeType,omitempty"` // MIME type (for images, etc.)
	Data     string `json:"data,omitempty"`     // Base64-encoded data (for images, etc.)
	URI      string `json:"uri,omitempty"`      // Resource URI
}

// CallToolResult is the result of calling an MCP tool.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"is_error"`
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	result, err := c.inner.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	blocks := make([]ContentBlock, len(result.Content))
	for i, b := range result.Content {
		blocks[i] = ContentBlock{Type: b.Type, Text: b.Text}
	}
	return &CallToolResult{
		Content: blocks,
		IsError: result.IsError,
	}, nil
}

// RegisterTools registers all MCP tools into an api/tools.Registry.
// Tool names are prefixed with "mcp.<server_name>." to avoid conflicts.
func (c *Client) RegisterTools(ctx context.Context, registry *api_tools.Registry) error {
	defs, err := c.inner.ListTools(ctx)
	if err != nil {
		return err
	}
	for _, def := range defs {
		d := def // capture
		toolName := fmt.Sprintf("mcp.%s.%s", c.serverName, d.Name)
		_ = registry.Register(api_tools.ToolFunc{
			ToolName: toolName,
			ToolDesc: d.Description,
			Fn: func(ctx context.Context, params map[string]any) (any, error) {
				result, err := c.inner.CallTool(ctx, d.Name, params)
				if err != nil {
					return nil, err
				}
				blocks := make([]ContentBlock, len(result.Content))
				for i, b := range result.Content {
					blocks[i] = ContentBlock{Type: b.Type, Text: b.Text}
				}
				return CallToolResult{Content: blocks, IsError: result.IsError}, nil
			},
		})
	}
	return nil
}

// ServerName returns the MCP server name.
func (c *Client) ServerName() string {
	return c.serverName
}

// Close closes the MCP connection.
func (c *Client) Close() error {
	return c.inner.Close()
}

// ── MCP Server Discovery ─────────────────────────────────

// DiscoverServers scans all known config sources for MCP server definitions.
// Discovery sources (in order):
//  1. ~/.ares/mcp-registry.json — ARES's own registration center
//  2. ~/.claude.json — Claude Code global config
//  3. .claude/settings.json — Claude Code project config
//  4. ~/.cursor/mcp.json — Cursor IDE config
//  5. .vscode/mcp.json — VS Code project config
//
// Returns a list of ServerConfig that can be passed to ConnectFromConfig.
func DiscoverServers(projectDir string) []ServerConfig {
	servers := make([]ServerConfig, 0)
	seen := make(map[string]bool)

	home, _ := os.UserHomeDir()

	// 1. ARES registry (~/.ares/mcp-registry.json)
	if home != "" {
		servers = append(servers, scanARESRegistry(filepath.Join(home, ".ares", "mcp-registry.json"), seen)...)
	}

	// 2. Claude Code global (~/.claude.json)
	if home != "" {
		servers = append(servers, scanClaudeConfig(filepath.Join(home, ".claude.json"), seen)...)
	}

	// 3. Claude Code project (.claude/settings.json)
	if projectDir != "" {
		servers = append(servers, scanClaudeConfig(filepath.Join(projectDir, ".claude", "settings.json"), seen)...)
	}

	// 4. Cursor IDE (~/.cursor/mcp.json)
	if home != "" {
		servers = append(servers, scanCursorConfig(filepath.Join(home, ".cursor", "mcp.json"), seen)...)
	}

	// 5. VS Code project (.vscode/mcp.json)
	if projectDir != "" {
		servers = append(servers, scanVSCodeConfig(filepath.Join(projectDir, ".vscode", "mcp.json"), seen)...)
	}

	return servers
}

// DiscoverHTTP probes a URL for .well-known/mcp.json and returns server info.
func DiscoverHTTP(ctx context.Context, baseURL string) (*ServerConfig, error) {
	url := strings.TrimRight(baseURL, "/") + "/.well-known/mcp.json"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	var info struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Version     string   `json:"version"`
		Tools       []string `json:"tools"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &ServerConfig{
		Name: info.Name,
		URL:  baseURL,
	}, nil
}

// scanARESRegistry reads ARES's own MCP registry file.
// Format: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "..."}]}
func scanARESRegistry(path string, seen map[string]bool) []ServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		Servers []struct {
			Name    string   `json:"name"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	servers := make([]ServerConfig, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		servers = append(servers, ServerConfig{
			Name: s.Name, Command: s.Command, Args: s.Args, URL: s.URL,
		})
	}
	return servers
}

// scanClaudeConfig reads Claude Code config (mcpServers key).
func scanClaudeConfig(path string, seen map[string]bool) []ServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	servers := make([]ServerConfig, 0, len(cfg.MCPServers))
	for name, sc := range cfg.MCPServers {
		if seen[name] {
			continue
		}
		seen[name] = true
		servers = append(servers, ServerConfig{
			Name: name, Command: sc.Command, Args: sc.Args, URL: sc.URL,
		})
	}
	return servers
}

// scanCursorConfig reads Cursor IDE config.
func scanCursorConfig(path string, seen map[string]bool) []ServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// Cursor uses same format as Claude
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	servers := make([]ServerConfig, 0, len(cfg.MCPServers))
	for name, sc := range cfg.MCPServers {
		if seen[name] {
			continue
		}
		seen[name] = true
		servers = append(servers, ServerConfig{
			Name: name, Command: sc.Command, Args: sc.Args, URL: sc.URL,
		})
	}
	return servers
}

// scanVSCodeConfig reads VS Code mcp.json config.
func scanVSCodeConfig(path string, seen map[string]bool) []ServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// VS Code uses {"servers": {"name": {"command": "...", "args": [...]}}}
	var cfg struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	servers := make([]ServerConfig, 0, len(cfg.Servers))
	for name, sc := range cfg.Servers {
		if seen[name] {
			continue
		}
		seen[name] = true
		servers = append(servers, ServerConfig{
			Name: name, Command: sc.Command, Args: sc.Args, URL: sc.URL,
		})
	}
	return servers
}

