package ares_runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// AgentInfo holds agent metadata for external consumers like the dashboard.
type AgentInfo struct {
	ID       string
	Type     string
	Status   string
	Restarts int
	Paused   bool
}

// ListAgents returns metadata for all managed agents.
func (m *Manager) ListAgents() []AgentInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]AgentInfo, 0, len(m.agents))
	for id, ma := range m.agents {
		if ma.agent == nil {
			continue
		}
		infos = append(infos, AgentInfo{
			ID:       id,
			Type:     string(ma.agent.Type()),
			Status:   string(ma.agent.Status()),
			Restarts: ma.restarts,
			Paused:   ma.paused,
		})
	}

	return infos
}

// GetAgentInfo returns metadata for a specific agent.
func (m *Manager) GetAgentInfo(agentID string) (*AgentInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ma, ok := m.agents[agentID]
	if !ok || ma.agent == nil {
		return nil, false
	}

	return &AgentInfo{
		ID:       agentID,
		Type:     string(ma.agent.Type()),
		Status:   string(ma.agent.Status()),
		Restarts: ma.restarts,
		Paused:   ma.paused,
	}, true
}

// ── Arena Chaos Engineering Fault Injection ───────────────────────────

// PauseAgent suspends an agent's goroutine without destroying its state, so
// ResumeAgent can relaunch the SAME in-memory instance. Unlike StopAgent it
// does NOT set the permanent `stopped` flag: the managedAgent entry stays in
// m.agents and the agent object is preserved. Paused agents are skipped by
// healthCheck and NotifyAgentDead (no resurrection while paused).
func (m *Manager) PauseAgent(ctx context.Context, agentID string) error {
	log.Info("[arena] PauseAgent", "agent", agentID)
	m.mu.Lock()
	ma, exists := m.agents[agentID]
	if !exists {
		m.mu.Unlock()
		return ErrAgentNotFound
	}
	ma.paused = true
	cancel := ma.cancel
	agent := ma.agent
	m.mu.Unlock()

	// Cancel the managed goroutine context first, then stop the agent
	// gracefully. The agent instance is intentionally NOT replaced, so the
	// in-memory state survives for ResumeAgent.
	if cancel != nil {
		cancel()
	}
	if agent != nil {
		stopCtx, stopCancel := context.WithTimeout(ctx, m.config.AgentStopTimeout)
		defer stopCancel()
		if err := agent.Stop(stopCtx); err != nil {
			log.Warn("runtime: agent pause stop failed", "agent_id", agentID, "error", err)
		}
	}

	m.emitEvent(ctx, agentID, ares_events.EventAgentStopped, map[string]any{
		FieldAgentID: agentID,
		FieldReason:  "pause",
	})

	log.Info("runtime: agent paused", "agent_id", agentID)
	return nil
}

// ResumeAgent relaunches a previously paused agent using its SAME in-memory
// instance: no factory rebuild, no restart counter increment, and any state
// accumulated before the pause is preserved. It is a no-op for agents that
// are not paused. A fresh cancellable context is stored on the managedAgent
// so a later StopAgent/PauseAgent can cancel the new goroutine.
func (m *Manager) ResumeAgent(ctx context.Context, agentID string) error {
	log.Info("[arena] ResumeAgent", "agent", agentID)
	m.mu.Lock()
	ma, exists := m.agents[agentID]
	if !exists {
		m.mu.Unlock()
		return ErrAgentNotFound
	}
	if !ma.paused {
		m.mu.Unlock()
		return nil // not paused: nothing to resume
	}
	agentCtx, agentCancel := context.WithCancel(m.gctx)
	ma.paused = false
	ma.cancel = agentCancel
	agent := ma.agent
	m.mu.Unlock()

	if agent != nil {
		m.launchAgentGoroutine(agentCtx, agentID, agent)
	}

	m.emitEvent(ctx, agentID, ares_events.EventAgentStarted, map[string]any{
		FieldAgentID: agentID,
		FieldReason:  "resume",
	})

	log.Info("runtime: agent resumed", "agent_id", agentID)
	return nil
}

// SlowAgent adds an artificial latency for an agent's operations.
func (m *Manager) SlowAgent(_ context.Context, agentID string, delay time.Duration) error {
	log.Info("[arena] SlowAgent", "agent", agentID, "delay", delay.String())
	m.mu.Lock()
	if m.chaosConfig == nil {
		m.chaosConfig = make(map[string]chaosEntry)
	}
	entry := m.chaosConfig[agentID]
	entry.slowDelay = delay
	m.chaosConfig[agentID] = entry
	m.mu.Unlock()
	return nil
}

// PartitionNetwork simulates a network partition for an agent.
func (m *Manager) PartitionNetwork(_ context.Context, agentID string) error {
	return fmt.Errorf("partition network: %w", ErrNotImplemented)
}

// ToolTimeout sets a short execution deadline for an agent's tools.
func (m *Manager) ToolTimeout(_ context.Context, agentID string, timeout time.Duration) error {
	log.Info("[arena] ToolTimeout", "agent", agentID, "timeout", timeout.String())
	m.mu.Lock()
	if m.chaosConfig == nil {
		m.chaosConfig = make(map[string]chaosEntry)
	}
	entry := m.chaosConfig[agentID]
	entry.toolTimeout = timeout
	m.chaosConfig[agentID] = entry
	m.mu.Unlock()
	return nil
}

// CorruptMemory simulates memory corruption for an agent.
func (m *Manager) CorruptMemory(_ context.Context, agentID string) error {
	return fmt.Errorf("corrupt memory: %w", ErrNotImplemented)
}

// DisconnectMCP simulates an MCP server disconnection for an agent.
func (m *Manager) DisconnectMCP(_ context.Context, agentID string) error {
	return fmt.Errorf("disconnect MCP: %w", ErrNotImplemented)
}

// InjectLLMFailure simulates an LLM failure for an agent.
func (m *Manager) InjectLLMFailure(_ context.Context, agentID string, errType string) error {
	return fmt.Errorf("inject LLM failure: %w", ErrNotImplemented)
}
