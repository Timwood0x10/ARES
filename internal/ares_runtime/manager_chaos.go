package ares_runtime

import (
	"context"
	"time"
)

// AgentInfo holds agent metadata for external consumers like the dashboard.
type AgentInfo struct {
	ID       string
	Type     string
	Status   string
	Restarts int
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
	}, true
}

// ── Arena Chaos Engineering Fault Injection ───────────────────────────

// PauseAgent stops an agent without triggering resurrection.
func (m *Manager) PauseAgent(ctx context.Context, agentID string) error {
	log.Info("[arena] PauseAgent", "agent", agentID)
	return m.StopAgent(ctx, agentID)
}

// ResumeAgent restarts a previously paused agent.
func (m *Manager) ResumeAgent(ctx context.Context, agentID string) error {
	log.Info("[arena] ResumeAgent", "agent", agentID)
	return m.RestartAgent(ctx, agentID)
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
	log.Warn("[arena] PartitionNetwork — SIMULATION: no actual network partition applied (R-02)", "agent", agentID)
	return nil
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
	log.Warn("[arena] CorruptMemory — SIMULATION: no actual memory corruption applied (R-02)", "agent", agentID)
	return nil
}

// DisconnectMCP simulates an MCP server disconnection for an agent.
func (m *Manager) DisconnectMCP(_ context.Context, agentID string) error {
	log.Warn("[arena] DisconnectMCP — SIMULATION: no actual MCP disconnection applied (R-02)", "agent", agentID)
	return nil
}

// InjectLLMFailure simulates an LLM failure for an agent.
func (m *Manager) InjectLLMFailure(_ context.Context, agentID string, errType string) error {
	log.Warn("[arena] InjectLLMFailure — SIMULATION: no actual LLM failure injected (R-02)", "agent", agentID, "errType", errType)
	return nil
}
