package dashboard

import (
	"time"
)

// AgentInfo holds basic agent information for the dashboard.
type AgentInfo struct {
	ID       string
	Type     string
	Status   string
	Restarts int
}

// AgentProvider abstracts agent listing for the dashboard.
// The runtime.Manager will implement this.
type AgentProvider interface {
	ListAgents() []AgentInfo
	GetAgent(id string) (*AgentInfo, bool)
}

// MCPStatusProvider abstracts MCP status for the dashboard.
// The mcp.MCPManager will implement this via MCPServerLister.
type MCPStatusProvider interface {
	ListServers() []MCPServerStatusView
}

// MCPServerStatusView is a dashboard-safe view of MCP server status.
type MCPServerStatusView struct {
	Name      string        `json:"name"`
	Connected bool          `json:"connected"`
	ToolCount int           `json:"tool_count"`
	Version   string        `json:"version"`
	Error     string        `json:"error,omitempty"`
	ConnAt    time.Time     `json:"connected_at,omitempty"`
	Tools     []MCPToolView `json:"tools"`
}

// DashboardConfig holds configuration for the dashboard.
type DashboardConfig struct {
	Addr           string        `yaml:"addr" json:"addr"`
	EnableAuth     bool          `yaml:"enable_auth" json:"enable_auth"`
	StaticDir      string        `yaml:"static_dir" json:"static_dir"`
	WSPingInterval time.Duration `yaml:"ws_ping_interval" json:"ws_ping_interval"`
}
