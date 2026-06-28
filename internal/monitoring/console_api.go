package monitoring

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

// ConsoleAPI is the top-level public interface for the ARES Console monitoring plugin.
// Implementations aggregate data from internal subsystems and expose a unified
// view for dashboards, CLI tooling, and real-time consumers.
type ConsoleAPI interface {
	// Snapshot returns the full console state including agents, tasks, and cost.
	Snapshot(ctx context.Context) (*ConsoleSnapshot, error)

	// DAG returns the current DAG snapshot for graph visualization.
	DAG(ctx context.Context) (*dag.DAGSnapshot, error)

	// Events returns recent events.
	Events(ctx context.Context, limit int) ([]*ares_events.Event, error)

	// Agent returns details for a single agent by ID.
	Agent(ctx context.Context, agentID string) (*UnifiedAgent, error)

	// AgentCost returns the cost breakdown for a specific agent.
	AgentCost(ctx context.Context, agentID string) (*AgentCost, error)

	// CostBreakdown returns the full cost breakdown across agents, models, and tasks.
	CostBreakdown(ctx context.Context) (*CostBreakdown, error)

	// CostAlerts returns any active cost alerts.
	CostAlerts(ctx context.Context) ([]CostAlert, error)

	// Tasks returns all task views, optionally filtered by status.
	Tasks(ctx context.Context, status *dag.NodeStatus) ([]TaskView, error)

	// Traces returns trace spans for a given trace ID.
	Traces(ctx context.Context, traceID string) ([]TraceSpan, error)

	// Timeline returns timeline events for a specific node.
	Timeline(ctx context.Context, nodeID string) ([]dag.TimelineEvent, error)

	// Actions returns available actions for a specific node.
	Actions(ctx context.Context, nodeID string) ([]NodeAction, error)

	// ExecuteAction performs an action on a node and returns the result.
	ExecuteAction(ctx context.Context, actionID string) (*ActionResult, error)

	// Interactions returns recent interactions between agents.
	Interactions(ctx context.Context, limit int) ([]Interaction, error)

	// Detail returns a detailed view for a selected entity.
	Detail(ctx context.Context, entityType, entityID string) (*DetailView, error)

	// AgentMemory returns the memory state of a specific agent.
	AgentMemory(ctx context.Context, agentID string) (*AgentMemory, error)

	// AgentEvolution returns the evolutionary history of an agent.
	AgentEvolution(ctx context.Context, agentID string) (*AgentEvolution, error)

	// MCPToolCalls returns MCP tool call records for an agent.
	MCPToolCalls(ctx context.Context, agentID string, limit int) ([]MCPToolCall, error)

	// LLMCalls returns LLM call records for an agent.
	LLMCalls(ctx context.Context, agentID string, limit int) ([]LLMCallRecord, error)

	// Recommendations returns current AI-generated recommendations.
	Recommendations(ctx context.Context) ([]Recommendation, error)
}
