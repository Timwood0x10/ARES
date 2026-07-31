package ares_runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
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

// chaosFault returns the active fault error for the agent, or nil when no
// fault is configured. It is read at the Process boundary by
// chaosWrappedAgent so injections take effect on the next execution without
// restarting the agent. Read under RLock; config is written by the arena
// injectors under Lock.
func (m *Manager) chaosFault(agentID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := m.chaosConfig[agentID]
	switch {
	case c.networkPartitioned:
		return fmt.Errorf("chaos: network partition injected for agent %s", agentID)
	case c.memoryCorrupt:
		return fmt.Errorf("chaos: memory corruption injected for agent %s", agentID)
	case c.mcpDisconnected:
		return fmt.Errorf("chaos: MCP disconnected injected for agent %s", agentID)
	case c.llmFailureType != "":
		return fmt.Errorf("chaos: LLM failure (%s) injected for agent %s", c.llmFailureType, agentID)
	default:
		return nil
	}
}

// chaosWrappedAgent decorates a base.Agent so fault injections take effect at
// the Process/ProcessStream boundary. All other methods are promoted from the
// embedded base.Agent unchanged. Only agents registered via StartAgent (or
// rebuilt by RestartAgent) are wrapped; the wrap is transparent to callers.
type chaosWrappedAgent struct {
	base.Agent
	m  *Manager
	id string
}

// Process injects the configured fault (if any) before delegating to the
// wrapped agent, so a partitioned/corrupted/disconnected/failing agent fails
// fast instead of silently succeeding.
func (w *chaosWrappedAgent) Process(ctx context.Context, input any) (any, error) {
	if err := w.m.chaosFault(w.id); err != nil {
		return nil, err
	}
	return w.Agent.Process(ctx, input)
}

// ProcessStream injects the configured fault (if any) before delegating.
// On injection the returned channel is closed immediately and the fault error
// is returned, matching the ProcessStream contract.
func (w *chaosWrappedAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	if err := w.m.chaosFault(w.id); err != nil {
		ch := make(chan base.AgentEvent)
		close(ch)
		return ch, err
	}
	return w.Agent.ProcessStream(ctx, input)
}

// chaosUnwrap returns the underlying agent when wrapped by chaosWrappedAgent,
// so optional-interface assertions (StatefulAgent, Heartbeater, Messenger)
// keep working on the raw instance. Returns the input unchanged otherwise.
func chaosUnwrap(a base.Agent) base.Agent {
	if w, ok := a.(*chaosWrappedAgent); ok {
		return w.Agent
	}
	return a
}

// setChaosConfig writes a per-agent chaos entry under the manager write lock,
// initializing the map on first use.
func (m *Manager) setChaosConfig(agentID string, mutate func(*chaosEntry)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chaosConfig == nil {
		m.chaosConfig = make(map[string]chaosEntry)
	}
	entry := m.chaosConfig[agentID]
	mutate(&entry)
	m.chaosConfig[agentID] = entry
}

// PartitionNetwork marks the agent as network-partitioned: its next
// Process/ProcessStream call fails with an injected network fault.
func (m *Manager) PartitionNetwork(_ context.Context, agentID string) error {
	log.Info("[arena] PartitionNetwork", "agent", agentID)
	m.setChaosConfig(agentID, func(e *chaosEntry) { e.networkPartitioned = true })
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

// CorruptMemory marks the agent's memory as corrupted: its next
// Process/ProcessStream call fails with an injected memory fault.
func (m *Manager) CorruptMemory(_ context.Context, agentID string) error {
	log.Info("[arena] CorruptMemory", "agent", agentID)
	m.setChaosConfig(agentID, func(e *chaosEntry) { e.memoryCorrupt = true })
	return nil
}

// DisconnectMCP marks the agent's MCP connection as disconnected: its next
// Process/ProcessStream call fails with an injected MCP fault.
func (m *Manager) DisconnectMCP(_ context.Context, agentID string) error {
	log.Info("[arena] DisconnectMCP", "agent", agentID)
	m.setChaosConfig(agentID, func(e *chaosEntry) { e.mcpDisconnected = true })
	return nil
}

// InjectLLMFailure marks the agent's LLM as failing with the given error
// type: its next Process/ProcessStream call fails with an injected LLM fault.
func (m *Manager) InjectLLMFailure(_ context.Context, agentID string, errType string) error {
	log.Info("[arena] InjectLLMFailure", "agent", agentID, "error_type", errType)
	m.setChaosConfig(agentID, func(e *chaosEntry) { e.llmFailureType = errType })
	return nil
}
