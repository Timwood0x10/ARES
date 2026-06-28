// Package monitoring provides types and APIs for the ARES Console monitoring plugin.
package monitoring

import (
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

// UnifiedAgent represents an aggregated view of an agent across the system.
type UnifiedAgent struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Status    dag.NodeStatus `json:"status"`
	Role      string         `json:"role"`
	ModelName string         `json:"model_name"`
	TaskID    string         `json:"task_id"`
	ParentID  string         `json:"parent_id,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	StartedAt time.Time      `json:"started_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// AgentCost tracks cost information for a single agent.
type AgentCost struct {
	AgentID       string  `json:"agent_id"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	TotalTokens   int64   `json:"total_tokens"`
	EstimatedCost float64 `json:"estimated_cost"`
	Currency      string  `json:"currency"`
	CallCount     int     `json:"call_count"`
}

// CostBreakdown provides a hierarchical cost breakdown.
type CostBreakdown struct {
	ByAgent  map[string]AgentCost `json:"by_agent"`
	ByModel  map[string]float64   `json:"by_model"`
	ByTask   map[string]float64   `json:"by_task"`
	Total    float64              `json:"total"`
	Currency string               `json:"currency"`
}

// CostAlert represents a threshold-based cost alert.
type CostAlert struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	Threshold   float64   `json:"threshold"`
	Actual      float64   `json:"actual"`
	Message     string    `json:"message"`
	Severity    string    `json:"severity"`
	TriggeredAt time.Time `json:"triggered_at"`
}

// NodeAction describes an action that can be performed on a node.
type NodeAction struct {
	ID      string `json:"id"`
	NodeID  string `json:"node_id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// ActionResult is the outcome of executing a NodeAction.
type ActionResult struct {
	ActionID string `json:"action_id"`
	Success  bool   `json:"success"`
	Message  string `json:"message"`
}

// TraceSpan represents a single span in a distributed trace.
type TraceSpan struct {
	TraceID    string         `json:"trace_id"`
	SpanID     string         `json:"span_id"`
	ParentID   string         `json:"parent_id,omitempty"`
	Name       string         `json:"name"`
	AgentID    string         `json:"agent_id"`
	Status     string         `json:"status"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time,omitempty"`
	Duration   time.Duration  `json:"duration"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// TaskView is a console-friendly representation of a task.
type TaskView struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Status      dag.NodeStatus `json:"status"`
	AgentID     string         `json:"agent_id"`
	Progress    float64        `json:"progress"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

// Interaction represents a user-system or agent-agent interaction.
type Interaction struct {
	ID        string    `json:"id"`
	FromID    string    `json:"from_id"`
	ToID      string    `json:"to_id"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// TabAction defines an action available in the console tab UI.
type TabAction struct {
	ID      string `json:"id"`
	TabName string `json:"tab_name"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// AgentMemory stores memory state for a single agent.
type AgentMemory struct {
	AgentID   string         `json:"agent_id"`
	ShortTerm []MemoryRecord `json:"short_term"`
	LongTerm  []MemoryRecord `json:"long_term"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// AgentEvolution tracks evolutionary changes of an agent.
type AgentEvolution struct {
	AgentID    string           `json:"agent_id"`
	Mutations  []MutationRecord `json:"mutations"`
	Generation int              `json:"generation"`
	ParentID   string           `json:"parent_id,omitempty"`
	StartedAt  time.Time        `json:"started_at"`
}

// MCPToolCall records a Model Context Protocol tool invocation.
type MCPToolCall struct {
	ID        string         `json:"id"`
	AgentID   string         `json:"agent_id"`
	ToolName  string         `json:"tool_name"`
	Input     map[string]any `json:"input,omitempty"`
	Output    map[string]any `json:"output,omitempty"`
	Status    string         `json:"status"`
	Duration  time.Duration  `json:"duration"`
	Timestamp time.Time      `json:"timestamp"`
}

// LLMCallRecord logs a single LLM API call.
type LLMCallRecord struct {
	ID           string        `json:"id"`
	AgentID      string        `json:"agent_id"`
	ModelName    string        `json:"model_name"`
	InputTokens  int64         `json:"input_tokens"`
	OutputTokens int64         `json:"output_tokens"`
	Duration     time.Duration `json:"duration"`
	Timestamp    time.Time     `json:"timestamp"`
}

// MemoryRecord is a single memory entry.
type MemoryRecord struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Category  string    `json:"category"`
	Content   string    `json:"content"`
	Relevance float64   `json:"relevance"`
	CreatedAt time.Time `json:"created_at"`
}

// MutationRecord tracks a single mutation in agent evolution.
type MutationRecord struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	Description string    `json:"description"`
	Before      string    `json:"before"`
	After       string    `json:"after"`
	Timestamp   time.Time `json:"timestamp"`
}

// Recommendation is an AI-generated suggestion for the operator.
type Recommendation struct {
	ID       string `json:"id"`
	AgentID  string `json:"agent_id,omitempty"`
	Category string `json:"category"`
	Text     string `json:"text"`
	Priority string `json:"priority"`
}

// ConsoleSnapshot is the full console state at a point in time.
type ConsoleSnapshot struct {
	Agents     []UnifiedAgent       `json:"agents"`
	Tasks      []TaskView           `json:"tasks"`
	Events     []*ares_events.Event `json:"events"`
	Cost       CostBreakdown        `json:"cost"`
	Alerts     []CostAlert          `json:"alerts,omitempty"`
	UpdateTime time.Time            `json:"update_time"`
}

// ConsoleStats provides aggregate statistics for the console view.
type ConsoleStats struct {
	ActiveAgents int           `json:"active_agents"`
	TotalTasks   int           `json:"total_tasks"`
	RunningTasks int           `json:"running_tasks"`
	TotalCost    float64       `json:"total_cost"`
	Uptime       time.Duration `json:"uptime"`
}

// CostBarSnapshot renders a cost bar in the console UI.
type CostBarSnapshot struct {
	Current  float64 `json:"current"`
	Budget   float64 `json:"budget"`
	Percent  float64 `json:"percent"`
	Currency string  `json:"currency"`
}

// DetailView shows detailed information for a selected entity.
type DetailView struct {
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Tabs       []string       `json:"tabs"`
	Data       map[string]any `json:"data"`
}

// ConsoleUpdate is a delta pushed to the console client.
type ConsoleUpdate struct {
	Type      string    `json:"type"`
	Payload   any       `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

// CostBarEntry is a single agent's cost entry in the cost bar.
type CostBarEntry struct {
	AgentID       string  `json:"agent_id"`
	EstimatedCost float64 `json:"estimated_cost"`
	Currency      string  `json:"currency"`
	CallCount     int     `json:"call_count"`
}

// CostBarBreakdown is the full cost bar snapshot at a point in time.
type CostBarBreakdown struct {
	Total    float64        `json:"total"`
	Entries  []CostBarEntry `json:"entries"`
	Currency string         `json:"currency"`
}
