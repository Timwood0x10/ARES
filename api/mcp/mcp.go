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
	"fmt"
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
