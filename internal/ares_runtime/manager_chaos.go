package ares_runtime

import (
	"context"
	"fmt"
	"time"
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

// PauseAgent stops an agent without triggering resurrection.
func (m *Manager) PauseAgent(ctx context.Context, agentID string) error {
	log.Info("[arena] PauseAgent", "agent", agentID)
	m.mu.Lock()
	if ma, ok := m.agents[agentID]; ok {
		ma.paused = true
	}
	m.mu.Unlock()
	return m.StopAgent(ctx, agentID)
}

// ResumeAgent restarts a previously paused agent.
func (m *Manager) ResumeAgent(ctx context.Context, agentID string) error {
	log.Info("[arena] ResumeAgent", "agent", agentID)
	m.mu.Lock()
	if ma, ok := m.agents[agentID]; ok {
		ma.paused = false
	}
	m.mu.Unlock()
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
