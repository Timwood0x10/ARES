// Package core provides core interfaces for the ARES system.
package core

import "context"

// MCPManager defines the interface for managing MCP server connections.
type MCPManager interface {
	// Start connects to all configured MCP servers.
	// Args:
	// ctx - operation context.
	// Returns error if any server fails to connect.
	Start(ctx context.Context) error

	// Stop disconnects from all MCP servers.
	// Args:
	// ctx - operation context.
	// Returns error if any disconnect fails.
	Stop(ctx context.Context) error

	// ListServers returns the status of all managed servers.
	// Returns list of server statuses.
	ListServers() []MCPStatus
}

// MCPStatus represents the current status of an MCP server.
type MCPStatus struct {
	// Name is the server name.
	Name string
	// Connected indicates whether the server is connected.
	Connected bool
	// ToolCount is the number of tools provided by the server.
	ToolCount int
	// Version is the server protocol version.
	Version string
}
