// package dashboard - provides a web dashboard for monitoring GoAgentX runtime.
package dashboard

import (
	"time"
)

// SystemOverview holds the top-level dashboard summary.
type SystemOverview struct {
	Uptime       string           `json:"uptime"`
	AgentCount   int              `json:"agent_count"`
	ActiveTasks  int              `json:"active_tasks"`
	TotalEvents  int64            `json:"total_events"`
	MemoryStats  MemoryStats      `json:"memory_stats"`
	MCPStatus    *MCPOverview     `json:"mcp_status,omitempty"`
	RuntimeStats RuntimeStatsView `json:"runtime_stats"`
}

// RuntimeStatsView is the dashboard's view of runtime stats.
type RuntimeStatsView struct {
	ActiveAgents  int   `json:"active_agents"`
	TotalRestarts int   `json:"total_restarts"`
	UptimeSeconds int64 `json:"uptime_seconds"`
}

// MemoryStats holds memory subsystem summary.
type MemoryStats struct {
	ActiveSessions int `json:"active_sessions"`
	DistilledCount int `json:"distilled_count"`
	TotalMessages  int `json:"total_messages"`
}

// MCPOverview holds MCP subsystem summary.
type MCPOverview struct {
	ServerCount    int `json:"server_count"`
	ConnectedCount int `json:"connected_count"`
	TotalTools     int `json:"total_tools"`
}

// AgentView represents an agent in the dashboard.
type AgentView struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Status    string         `json:"status"`
	Restarts  int            `json:"restarts"`
	Uptime    string         `json:"uptime"`
	LastEvent *EventView     `json:"last_event,omitempty"`
	Heartbeat *HeartbeatView `json:"heartbeat,omitempty"`
}

// HeartbeatView represents agent heartbeat status.
type HeartbeatView struct {
	LastSeen    time.Time `json:"last_seen"`
	MissedCount int       `json:"missed_count"`
	IsAlive     bool      `json:"is_alive"`
}

// DAGView represents a workflow DAG for visualization.
type DAGView struct {
	Nodes   []DAGNodeView `json:"nodes"`
	Edges   []DAGEdgeView `json:"edges"`
	Version uint64        `json:"version"`
}

// DAGNodeView represents a single node in the DAG.
type DAGNodeView struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	AgentType string            `json:"agent_type"`
	Status    string            `json:"status"`
	InDegree  int               `json:"in_degree"`
	OutDegree int               `json:"out_degree"`
	Duration  string            `json:"duration,omitempty"`
	Error     string            `json:"error,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// DAGEdgeView represents an edge in the DAG.
type DAGEdgeView struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// WorkflowExecutionView represents a workflow execution.
type WorkflowExecutionView struct {
	ID         string          `json:"id"`
	WorkflowID string          `json:"workflow_id"`
	Status     string          `json:"status"`
	Steps      []StepStateView `json:"steps"`
	StartedAt  time.Time       `json:"started_at"`
	Duration   string          `json:"duration,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// StepStateView represents a single step in a workflow execution.
type StepStateView struct {
	StepID   string `json:"step_id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Duration string `json:"duration,omitempty"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
	Attempts int    `json:"attempts"`
}

// SessionView represents a memory session.
type SessionView struct {
	SessionID    string        `json:"session_id"`
	UserID       string        `json:"user_id"`
	MessageCount int           `json:"message_count"`
	CreatedAt    time.Time     `json:"created_at"`
	Messages     []MessageView `json:"messages,omitempty"`
}

// MessageView represents a single message in a session.
type MessageView struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}

// DistilledMemoryView represents a distilled memory entry.
type DistilledMemoryView struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Content    string    `json:"content"`
	Importance float64   `json:"importance"`
	Source     string    `json:"source"`
	CreatedAt  time.Time `json:"created_at"`
}

// EventView represents an event for the dashboard.
type EventView struct {
	ID        string         `json:"id"`
	StreamID  string         `json:"stream_id"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	Version   int64          `json:"version"`
	Timestamp time.Time      `json:"timestamp"`
}

// EventQueryParams holds query parameters for event filtering.
type EventQueryParams struct {
	StreamID  string
	Types     []string
	Since     time.Time
	Limit     int
	Direction string
}

// MCPServerView represents an MCP server in the dashboard.
type MCPServerView struct {
	Name      string        `json:"name"`
	Connected bool          `json:"connected"`
	Version   string        `json:"version"`
	Tools     []MCPToolView `json:"tools"`
	Error     string        `json:"error,omitempty"`
	ConnAt    time.Time     `json:"connected_at,omitempty"`
}

// MCPToolView represents a single MCP tool.
type MCPToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ServerName  string `json:"server_name"`
}

// MemorySearchRequest holds parameters for memory search.
type MemorySearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}
