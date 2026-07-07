// Package data provides state aggregation for the ARES Console monitoring plugin.
package data

import (
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
	"github.com/Timwood0x10/ares/internal/monitoring/eventutil"
)

// AgentTracker aggregates agent state from event streams.
// All methods are safe for concurrent use.
type AgentTracker struct {
	mu     sync.RWMutex
	agents map[string]*monitoring.UnifiedAgent
	costs  map[string]*monitoring.AgentCost
}

// NewAgentTracker creates a new AgentTracker with empty agent and cost maps.
func NewAgentTracker() *AgentTracker {
	return &AgentTracker{
		agents: make(map[string]*monitoring.UnifiedAgent),
		costs:  make(map[string]*monitoring.AgentCost),
	}
}

// HandleEvent processes an event and updates the tracked agent state.
func (at *AgentTracker) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}

	switch evt.Type {
	case ares_events.EventAgentStarted:
		at.handleAgentStarted(evt)
	case ares_events.EventAgentStopped:
		at.handleAgentStopped(evt)
	case ares_events.EventFailoverTriggered:
		at.handleFailoverTriggered(evt)
	case ares_events.EventFailoverCompleted:
		at.handleFailoverCompleted(evt)
	case ares_events.EventLLMCall:
		at.handleLLMCall(evt)
	}
}

// GetAgent returns a copy of the tracked agent by ID.
func (at *AgentTracker) GetAgent(id string) (*monitoring.UnifiedAgent, bool) {
	at.mu.RLock()
	defer at.mu.RUnlock()
	agent, ok := at.agents[id]
	if !ok {
		return nil, false
	}
	cp := *agent
	return &cp, true
}

// ListAgents returns copies of all tracked agents.
func (at *AgentTracker) ListAgents() []monitoring.UnifiedAgent {
	at.mu.RLock()
	defer at.mu.RUnlock()
	result := make([]monitoring.UnifiedAgent, 0, len(at.agents))
	for _, a := range at.agents {
		cp := *a
		result = append(result, cp)
	}
	return result
}

// Snapshot returns aggregate console statistics.
func (at *AgentTracker) Snapshot() monitoring.ConsoleStats {
	at.mu.RLock()
	defer at.mu.RUnlock()

	stats := monitoring.ConsoleStats{}
	stats.TotalTasks = len(at.agents)

	var totalCost float64
	for _, a := range at.agents {
		if a.Status == dag.StatusRunning {
			stats.ActiveAgents++
			stats.RunningTasks++
		}
		if c, ok := at.costs[a.ID]; ok {
			totalCost += c.EstimatedCost
		}
	}
	stats.TotalCost = totalCost
	return stats
}

// GetCost returns the cost record for an agent, if tracked.
func (at *AgentTracker) GetCost(id string) (*monitoring.AgentCost, bool) {
	at.mu.RLock()
	defer at.mu.RUnlock()
	c, ok := at.costs[id]
	if !ok {
		return nil, false
	}
	cp := *c
	return &cp, true
}

// handleAgentStarted creates or updates an agent on start.
func (at *AgentTracker) handleAgentStarted(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	if agentID == "" {
		return
	}

	at.mu.Lock()
	defer at.mu.Unlock()

	now := evt.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	if existing, ok := at.agents[agentID]; ok {
		existing.Status = dag.StatusRunning
		existing.UpdatedAt = now
		if name := eventutil.ExtractString(evt, "name"); name != "" {
			existing.Name = name
		}
		return
	}

	at.agents[agentID] = &monitoring.UnifiedAgent{
		ID:        agentID,
		Name:      eventutil.ExtractString(evt, "name"),
		Status:    dag.StatusRunning,
		Role:      eventutil.ExtractString(evt, "role"),
		ModelName: eventutil.ExtractString(evt, "model_name"),
		TaskID:    eventutil.ExtractString(evt, "task_id"),
		ParentID:  eventutil.ExtractString(evt, "parent_id"),
		StartedAt: now,
		UpdatedAt: now,
	}
}

// handleAgentStopped transitions an agent to completed.
func (at *AgentTracker) handleAgentStopped(evt *ares_events.Event) {
	at.updateAgentStatus(evt, dag.StatusCompleted)
}

// handleFailoverTriggered transitions an agent to dead.
func (at *AgentTracker) handleFailoverTriggered(evt *ares_events.Event) {
	at.updateAgentStatus(evt, dag.StatusDead)
}

// handleFailoverCompleted transitions an agent through resurrecting to running.
func (at *AgentTracker) handleFailoverCompleted(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	if agentID == "" {
		return
	}

	at.mu.Lock()
	defer at.mu.Unlock()

	agent, ok := at.agents[agentID]
	if !ok {
		return
	}
	now := evt.Timestamp
	if now.IsZero() {
		now = time.Now()
	}
	agent.Status = dag.StatusRunning
	agent.UpdatedAt = now
}

// handleLLMCall accumulates token cost from LLM call events.
func (at *AgentTracker) handleLLMCall(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	if agentID == "" {
		return
	}

	inputTokens := eventutil.ExtractInt64(evt, "input_tokens")
	outputTokens := eventutil.ExtractInt64(evt, "output_tokens")
	estimatedCost := eventutil.ExtractFloat64(evt, "estimated_cost")
	modelName := eventutil.ExtractString(evt, "model_name")

	at.mu.Lock()
	defer at.mu.Unlock()

	cost, ok := at.costs[agentID]
	if !ok {
		cost = &monitoring.AgentCost{
			AgentID:  agentID,
			Currency: "USD",
		}
		at.costs[agentID] = cost
	}
	cost.InputTokens += inputTokens
	cost.OutputTokens += outputTokens
	cost.TotalTokens += inputTokens + outputTokens
	cost.EstimatedCost += estimatedCost
	cost.CallCount++

	// Update agent model name if provided.
	if modelName != "" {
		if agent, ok := at.agents[agentID]; ok {
			agent.ModelName = modelName
		}
	}
}

// updateAgentStatus transitions an agent to the given status.
func (at *AgentTracker) updateAgentStatus(evt *ares_events.Event, status dag.NodeStatus) {
	agentID := eventutil.ExtractAgentID(evt)
	if agentID == "" {
		return
	}

	at.mu.Lock()
	defer at.mu.Unlock()

	agent, ok := at.agents[agentID]
	if !ok {
		return
	}
	now := evt.Timestamp
	if now.IsZero() {
		now = time.Now()
	}
	agent.Status = status
	agent.UpdatedAt = now
}
