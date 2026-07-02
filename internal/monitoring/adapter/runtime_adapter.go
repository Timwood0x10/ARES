// Package adapter provides adapters between the monitoring plugin and
// external subsystems such as the runtime manager and orchestrator.
package adapter

import (
	"context"

	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

// RuntimeManager is the interface that the actual runtime manager must
// implement. It is deliberately kept narrow so the adapter can translate
// between the runtime's native types and the DAG layer's types.
type RuntimeManager interface {
	// NotifyAgentDead signals that an agent should be marked dead.
	NotifyAgentDead(agentID string, reason string)
	// RestartAgent restarts a stopped or dead agent.
	RestartAgent(ctx context.Context, agentID string) error
	// GetAgentInfo returns runtime metadata for the given agent.
	GetAgentInfo(agentID string) (*AgentInfo, bool)
}

// AgentInfo holds runtime metadata about a single agent, as seen by
// the runtime manager layer.
type AgentInfo struct {
	ID       string
	Type     string
	Status   string
	Restarts int
}

// RuntimeAdapter wraps a RuntimeManager and implements dag.RuntimeController.
// This allows the monitoring plugin to use any runtime manager that satisfies
// the RuntimeManager interface without creating a hard dependency.
type RuntimeAdapter struct {
	mgr RuntimeManager
}

// NewRuntimeAdapter creates a new RuntimeAdapter that delegates to the
// given RuntimeManager.
func NewRuntimeAdapter(mgr RuntimeManager) *RuntimeAdapter {
	if mgr == nil {
		return nil
	}
	return &RuntimeAdapter{mgr: mgr}
}

// NotifyAgentDead delegates to the wrapped RuntimeManager.
func (a *RuntimeAdapter) NotifyAgentDead(agentID, reason string) {
	a.mgr.NotifyAgentDead(agentID, reason)
}

// GetAgentInfo retrieves agent info from the RuntimeManager and converts
// it to the DAG layer's AgentInfo type.
func (a *RuntimeAdapter) GetAgentInfo(agentID string) (*dag.AgentInfo, bool) {
	info, ok := a.mgr.GetAgentInfo(agentID)
	if !ok {
		return nil, false
	}
	return &dag.AgentInfo{
		ID:     info.ID,
		Name:   info.Type,
		Status: info.Status,
		Source: "runtime",
	}, true
}

// RestartAgent delegates to the wrapped RuntimeManager.
func (a *RuntimeAdapter) RestartAgent(ctx context.Context, agentID string) error {
	return a.mgr.RestartAgent(ctx, agentID)
}
