// package dashboard - provides a web dashboard for monitoring ares runtime.
package dashboard

import (
	"time"
)

// EventView represents an event for the dashboard.
type EventView struct {
	ID        string         `json:"id"`
	StreamID  string         `json:"stream_id"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	Version   int64          `json:"version"`
	Timestamp time.Time      `json:"timestamp"`
}

// MCPToolView represents a single MCP tool.
type MCPToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ServerName  string `json:"server_name"`
}
