package mcp

import "encoding/json"

// ProtocolVersion is the MCP protocol version supported.
const ProtocolVersion = "2024-11-05"

// ClientName identifies this MCP client implementation.
const ClientName = "GoAgentX"

// --- Initialize handshake types ---

// Implementation identifies an MCP client or server.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities declares what the client supports.
type ClientCapabilities struct {
	Tools *ToolClientCapabilities `json:"tools,omitempty"`
}

// ToolClientCapabilities declares tool-related client capabilities.
type ToolClientCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// InitializeParams holds parameters for the initialize request.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ClientInfo      Implementation     `json:"clientInfo"`
	Capabilities    ClientCapabilities `json:"capabilities"`
}

// ServerCapabilities declares what the server supports.
type ServerCapabilities struct {
	Tools     *ToolServerCapabilities     `json:"tools,omitempty"`
	Resources *ResourceServerCapabilities `json:"resources,omitempty"`
	Prompts   *PromptServerCapabilities   `json:"prompts,omitempty"`
}

// ToolServerCapabilities declares tool-related server capabilities.
type ToolServerCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourceServerCapabilities declares resource-related server capabilities.
type ResourceServerCapabilities struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptServerCapabilities declares prompt-related server capabilities.
type PromptServerCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// InitializeResult holds the server's response to initialize.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
}

// --- Tool types ---

// MCPToolDef represents a tool definition returned by the MCP server.
type MCPToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolsListResult holds the result of tools/list.
type ToolsListResult struct {
	Tools []MCPToolDef `json:"tools"`
}

// ToolCallParams holds parameters for tools/call.
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolCallResult holds the result of tools/call.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock represents a piece of content in a tool call result.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// --- Notification types ---

// NotificationMethod constants for MCP notifications.
const (
	NotificationInitialized      = "notifications/initialized"
	NotificationToolsListChanged = "notifications/tools/list_changed"
)

// --- Method constants ---

// Method constants for MCP JSON-RPC methods.
const (
	MethodInitialize = "initialize"
	MethodToolsList  = "tools/list"
	MethodToolsCall  = "tools/call"
	MethodPing       = "ping"
)

// PingResult is an empty response to a ping.
type PingResult struct{}
