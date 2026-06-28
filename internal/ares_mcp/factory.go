package ares_mcp

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// MCPToolFactory implements core.ToolFactory for PluginRegistry integration.
type MCPToolFactory struct {
	manager *MCPManager
}

// NewMCPToolFactory creates a new factory wrapping the given manager.
func NewMCPToolFactory(manager *MCPManager) *MCPToolFactory {
	return &MCPToolFactory{
		manager: manager,
	}
}

// Name returns the factory identifier.
func (f *MCPToolFactory) Name() string {
	return "mcp"
}

// Description returns a human-readable description.
func (f *MCPToolFactory) Description() string {
	return "MCP (Model Context Protocol) tool factory for connecting to external MCP servers"
}

// Create connects to an MCP server and returns its tools.
// The config map must contain "name", "transport.type", and transport-specific fields.
func (f *MCPToolFactory) Create(config map[string]interface{}) (core.Tool, error) {
	name, _ := config["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("mcp server name is required")
	}

	transportType, _ := config["transport_type"].(string)
	if transportType == "" {
		transportType = "stdio"
	}

	// Build server config from the map.
	sc := MCPServerConfig{
		Name:    name,
		Enabled: true,
	}

	switch transportType {
	case "stdio":
		command, _ := config["command"].(string)
		if command == "" {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		args := toStringSlice(config["args"])
		env := toStringMap(config["env"])
		workDir, _ := config["work_dir"].(string)

		sc.Transport = TransportConfig{
			Type: "stdio",
			Stdio: &StdioConfig{
				Command: command,
				Args:    args,
				Env:     env,
				WorkDir: workDir,
			},
		}
	case "sse":
		url, _ := config["url"].(string)
		if url == "" {
			return nil, fmt.Errorf("url is required for sse transport")
		}
		headers := toStringMap(config["headers"])

		sc.Transport = TransportConfig{
			Type: "sse",
			SSE: &SSEConfig{
				URL:     url,
				Headers: headers,
			},
		}
	default:
		return nil, fmt.Errorf("unsupported transport type: %s", transportType)
	}

	// Create a temporary client to discover tools.
	transport, err := NewTransportFromConfig(sc.Transport)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	client := NewMCPClient(MCPClientConfig{
		ServerName: name,
		Transport:  sc.Transport,
	})

	ctx := context.Background()
	if err := client.Connect(ctx, transport); err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	// Return the first tool (factory creates one tool per call).
	client.mu.RLock()
	var firstDef *MCPToolDef
	for _, def := range client.tools {
		firstDef = def
		break
	}
	client.mu.RUnlock()

	if firstDef == nil {
		_ = client.Close()
		return nil, fmt.Errorf("no tools found on server %s", name)
	}

	tool, err := NewMCPTool(client, firstDef)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("create tool: %w", err)
	}

	return tool, nil
}

// ValidateConfig checks if the config map is valid for creating an MCP tool.
func (f *MCPToolFactory) ValidateConfig(config map[string]interface{}) error {
	name, _ := config["name"].(string)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}

	transportType, _ := config["transport_type"].(string)
	if transportType == "" {
		transportType = "stdio"
	}

	switch transportType {
	case "stdio":
		command, _ := config["command"].(string)
		if command == "" {
			return fmt.Errorf("command is required for stdio transport")
		}
	case "sse":
		url, _ := config["url"].(string)
		if url == "" {
			return fmt.Errorf("url is required for sse transport")
		}
	default:
		return fmt.Errorf("unsupported transport type: %s", transportType)
	}

	return nil
}

// toStringSlice converts an interface{} to []string.
func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}

	switch s := v.(type) {
	case []string:
		return s
	case []interface{}:
		result := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}

// toStringMap converts an interface{} to map[string]string.
func toStringMap(v interface{}) map[string]string {
	if v == nil {
		return nil
	}

	switch m := v.(type) {
	case map[string]string:
		return m
	case map[string]interface{}:
		result := make(map[string]string, len(m))
		for k, val := range m {
			if str, ok := val.(string); ok {
				result[k] = str
			}
		}
		return result
	default:
		return nil
	}
}

// Ensure MCPToolFactory implements core.ToolFactory at compile time.
var _ core.ToolFactory = (*MCPToolFactory)(nil)
