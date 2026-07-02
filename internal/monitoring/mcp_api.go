package monitoring

import "context"

// MCPToolInfo describes a tool exposed by an MCP server.
type MCPToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// MCPToolResult is the outcome of calling an MCP tool.
type MCPToolResult struct {
	ToolName string         `json:"tool_name"`
	Output   map[string]any `json:"output,omitempty"`
	IsError  bool           `json:"is_error"`
	Error    string         `json:"error,omitempty"`
}

// MCPManager abstracts MCP operations for the monitoring plugin.
// Implementations bridge to a real MCP server or a local tool registry.
type MCPManager interface {
	// ListTools returns all available MCP tools.
	ListTools(ctx context.Context) ([]MCPToolInfo, error)
	// CallTool invokes a tool by name with the given arguments.
	CallTool(ctx context.Context, name string, args map[string]any) (*MCPToolResult, error)
}
