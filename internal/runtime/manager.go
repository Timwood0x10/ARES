package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"goagentx/internal/agents/base"
	"goagentx/internal/core/models"
	"goagentx/internal/events"
	"goagentx/internal/memory"
)

// managedAgent holds an agent and its lifecycle metadata.
type managedAgent struct {
	agent    base.Agent
	factory  AgentFactory
	cancel   context.CancelFunc
	restarts int
	// stopped is set to true when the agent is intentionally stopped
	// (via StopAgent or RestartAgent). Prevents NotifyAgentDead from
	// triggering resurrection of an intentionally stopped agent.
	stopped bool
}

// Manager implements the Runtime interface.
// It owns agent lifecycle: registration, start, stop, restart, and resurrection.
type Manager struct {
	mu            sync.RWMutex
	agents        map[string]*managedAgent
	factories     map[string]AgentFactory
	eventStore    events.EventStore
	memManager    memory.MemoryManager
	g             *errgroup.Group
	gctx          context.Context
	cancel        context.CancelFunc
	config        *Config
	totalRestarts int
	startTime     time.Time
	isStarted     bool
	isStopped     bool
}

// New creates a new Manager.
//
// Args:
//
//	config - runtime configuration. Uses defaults if nil.
//	eventStore - event store for operational recovery (may be nil).
//	memManager - memory manager for cognitive recovery (may be nil).
//
// Returns:
//
//	manager - the runtime manager.
func New(config *Config, eventStore events.EventStore, memManager memory.MemoryManager) *Manager {
	if config == nil {
		config = DefaultConfig()
	}
	// Initialize errgroup with a background context so that m.g.Go() never
	// panics even if called before Start(). Start() will re-initialize with
	// the caller's context.
	g, gctx := errgroup.WithContext(context.Background())
	return &Manager{
		agents:     make(map[string]*managedAgent),
		factories:  make(map[string]AgentFactory),
		eventStore: eventStore,
		memManager: memManager,
		config:     config,
		g:          g,
		gctx:       gctx,
	}
}

// RegisterAgent registers an agent and its factory for lifecycle management.
func (m *Manager) RegisterAgent(agent base.Agent, factory AgentFactory) {
	if agent == nil {
		slog.Error("runtime: RegisterAgent called with nil agent")
		return
	}
	if factory == nil {
		slog.Error("runtime: RegisterAgent called with nil factory", "agent_id", agent.ID())
		return
	}
	id := agent.ID()
	if id == "" {
		slog.Error("runtime: RegisterAgent called with empty agent ID")
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.factories[id] = factory

	// Store agent entry if not already present.
	if _, exists := m.agents[id]; !exists {
		m.agents[id] = &managedAgent{
			agent:   agent,
			factory: factory,
		}
	}

	slog.Info("runtime: agent registered", "agent_id", id, "type", agent.Type())
}

// StartAgent launches an agent in a managed goroutine with panic recovery.
func (m *Manager) StartAgent(ctx context.Context, agent base.Agent) error {
	if agent == nil {
		return ErrNilAgent
	}

	id := agent.ID()
	if id == "" {
		return fmt.Errorf("runtime: agent ID must not be empty")
	}

	m.mu.Lock()
	if m.isStopped {
		m.mu.Unlock()
		return ErrRuntimeStopped
	}

	// If agent already exists and is running (has cancel), reject.
	if existing, exists := m.agents[id]; exists && existing.cancel != nil {
		m.mu.Unlock()
		return ErrAgentAlreadyRegistered
	}

	agentCtx, agentCancel := context.WithCancel(m.gctx)
	ma := &managedAgent{
		agent:  agent,
		cancel: agentCancel,
	}
	// Preserve factory if already registered via RegisterAgent.
	if f, ok := m.factories[id]; ok {
		ma.factory = f
	}
	m.agents[id] = ma
	m.mu.Unlock()

	// Launch agent in a managed goroutine with panic recovery.
	m.g.Go(func() error {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("runtime: agent panicked in Start",
					"agent_id", id, "panic", r,
				)
				m.NotifyAgentDead(id, fmt.Sprintf("panic: %v", r))
			}
		}()

		if err := agent.Start(agentCtx); err != nil {
			// Context cancellation means intentional stop; do not resurrect.
			if agentCtx.Err() != nil {
				return nil
			}
			slog.Error("runtime: agent start failed",
				"agent_id", id, "error", err,
			)
			m.NotifyAgentDead(id, fmt.Sprintf("start failed: %v", err))
			return nil // Do not propagate; runtime must keep running.
		}
		return nil
	})

	return nil
}

// StopAgent gracefully stops an agent by ID.
func (m *Manager) StopAgent(ctx context.Context, agentID string) error {
	// Mark as intentionally stopped before cancelling context.
	// This prevents NotifyAgentDead from triggering resurrection.
	m.mu.Lock()
	ma, exists := m.agents[agentID]
	if !exists {
		m.mu.Unlock()
		return ErrAgentNotFound
	}
	ma.stopped = true
	cancel := ma.cancel
	agent := ma.agent
	m.mu.Unlock()

	// Cancel the agent's managed goroutine context.
	if cancel != nil {
		cancel()
	}

	// Gracefully stop the agent.
	if agent != nil {
		stopCtx, stopCancel := context.WithTimeout(ctx, m.config.AgentStopTimeout)
		defer stopCancel()
		if err := agent.Stop(stopCtx); err != nil {
			slog.Warn("runtime: agent stop returned error",
				"agent_id", agentID, "error", err,
			)
		}
	}

	slog.Info("runtime: agent stopped", "agent_id", agentID)
	return nil
}

// GetAgent returns the current instance of a managed agent, or nil if not found.
func (m *Manager) GetAgent(agentID string) base.Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ma, ok := m.agents[agentID]; ok {
		return ma.agent
	}
	return nil
}

// RestartAgent stops and restarts an agent with fresh state.
func (m *Manager) RestartAgent(ctx context.Context, agentID string) error {
	// Use write lock for entire check-and-mutate to prevent NotifyAgentDead race.
	m.mu.Lock()
	ma, exists := m.agents[agentID]
	if !exists {
		m.mu.Unlock()
		return ErrAgentNotFound
	}
	factory := m.factories[agentID]
	if factory == nil {
		m.mu.Unlock()
		return fmt.Errorf("runtime: no factory registered for agent %s", agentID)
	}

	// Mark as intentionally stopped to prevent NotifyAgentDead race.
	ma.stopped = true
	prevRestarts := ma.restarts
	m.mu.Unlock()

	// Stop the old agent.
	if ma.cancel != nil {
		ma.cancel()
	}
	if ma.agent != nil {
		stopCtx, stopCancel := context.WithTimeout(ctx, m.config.AgentStopTimeout)
		if err := ma.agent.Stop(stopCtx); err != nil {
			slog.Warn("runtime: restart stop failed", "agent_id", agentID, "error", err)
		}
		stopCancel()
	}

	// Create a fresh instance from factory.
	newAgent := factory()
	if newAgent == nil {
		return fmt.Errorf("runtime: factory returned nil for agent %s", agentID)
	}

	// Re-register and start.
	m.mu.Lock()
	agentCtx, agentCancel := context.WithCancel(m.gctx)
	m.agents[agentID] = &managedAgent{
		agent:    newAgent,
		factory:  factory,
		cancel:   agentCancel,
		restarts: prevRestarts + 1,
	}
	m.totalRestarts++
	m.mu.Unlock()

	m.g.Go(func() error {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("runtime: restarted agent panicked",
					"agent_id", agentID, "panic", r,
				)
				m.NotifyAgentDead(agentID, fmt.Sprintf("panic: %v", r))
			}
		}()

		if err := newAgent.Start(agentCtx); err != nil {
			if agentCtx.Err() != nil {
				return nil
			}
			slog.Error("runtime: restarted agent start failed",
				"agent_id", agentID, "error", err,
			)
			m.NotifyAgentDead(agentID, fmt.Sprintf("start failed: %v", err))
			return nil
		}
		return nil
	})

	slog.Info("runtime: agent restarted", "agent_id", agentID)
	return nil
}

// RestoreAgent creates a new agent from factory, replays events, restores memory, and starts it.
//
// Recovery flow:
//  1. Create new agent instance from factory.
//  2. Replay events from EventStore (operational recovery).
//  3. Enrich state with MemoryManager context (cognitive recovery).
//  4. RestoreState + ReplayEvents on the new agent if it implements StatefulAgent.
//  5. Start the new agent.
func (m *Manager) RestoreAgent(ctx context.Context, agentID string, factory AgentFactory) error {
	if factory == nil {
		return ErrNilFactory
	}

	// Stop old agent if present. Use write lock to prevent race with concurrent
	// RestoreAgent or NotifyAgentDead calls replacing the same agentID.
	m.mu.Lock()
	oldMA, oldExists := m.agents[agentID]
	if oldExists && oldMA != nil {
		oldMA.stopped = true
	}
	m.mu.Unlock()

	if oldExists && oldMA != nil {
		if oldMA.cancel != nil {
			oldMA.cancel()
		}
		stopCtx, stopCancel := context.WithTimeout(ctx, m.config.AgentStopTimeout)
		if err := oldMA.agent.Stop(stopCtx); err != nil {
			slog.Warn("runtime: restore stop old agent failed",
				"agent_id", agentID, "error", err,
			)
		}
		stopCancel()
	}

	// Step 1: Create new agent from factory.
	newAgent := factory()
	if newAgent == nil {
		return fmt.Errorf("runtime: factory returned nil for agent %s", agentID)
	}

	// Step 2: Replay events from EventStore (operational recovery).
	evts := m.replayEvents(ctx, agentID)

	// Step 3: Build combined recovery state if agent is StatefulAgent.
	if sa, ok := newAgent.(base.StatefulAgent); ok {
		state := buildStateFromEvents(evts)

		// Enrich state with memory context (cognitive recovery).
		if m.memManager != nil {
			cognitiveState := m.buildCognitiveState(ctx, agentID, state)
			for k, v := range cognitiveState {
				state[k] = v
			}
		}

		if len(state) > 0 {
			if err := sa.RestoreState(state); err != nil {
				slog.Warn("runtime: RestoreState failed",
					"agent_id", agentID, "error", err,
				)
			}
		}
		if len(evts) > 0 {
			if err := sa.ReplayEvents(evts); err != nil {
				slog.Warn("runtime: ReplayEvents failed",
					"agent_id", agentID, "error", err,
				)
			}
		}
	}

	// Step 4: Register and start the new agent.
	m.mu.Lock()
	agentCtx, agentCancel := context.WithCancel(m.gctx)
	prevRestarts := 0
	if oldExists && oldMA != nil {
		prevRestarts = oldMA.restarts
	}
	// Do NOT increment restarts here; NotifyAgentDead already did so
	// under write lock before scheduling this call.
	m.agents[agentID] = &managedAgent{
		agent:    newAgent,
		factory:  factory,
		cancel:   agentCancel,
		restarts: prevRestarts,
	}
	m.mu.Unlock()

	m.g.Go(func() error {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("runtime: restored agent panicked",
					"agent_id", agentID, "panic", r,
				)
				m.NotifyAgentDead(agentID, fmt.Sprintf("panic: %v", r))
			}
		}()

		if err := newAgent.Start(agentCtx); err != nil {
			if agentCtx.Err() != nil {
				return nil
			}
			slog.Error("runtime: restored agent start failed",
				"agent_id", agentID, "error", err,
			)
			m.NotifyAgentDead(agentID, fmt.Sprintf("start failed: %v", err))
			return nil
		}
		return nil
	})

	slog.Info("runtime: agent restored",
		"agent_id", agentID, "type", newAgent.Type(),
		"restarts", prevRestarts,
	)
	return nil
}

// NotifyAgentDead is called when an agent dies. It triggers asynchronous restoration
// via errgroup if a factory is registered for the agent.
func (m *Manager) NotifyAgentDead(agentID string, reason string) {
	// Use write lock for check-and-increment to prevent TOCTOU race.
	// Two concurrent calls could both pass the restart limit check under RLock,
	// then both schedule RestoreAgent, exceeding the intended restart count.
	m.mu.Lock()
	factory, hasFactory := m.factories[agentID]
	ma, hasAgent := m.agents[agentID]
	isStopped := m.isStopped
	// Check if the agent was intentionally stopped (via StopAgent or RestartAgent).
	// If so, do not trigger resurrection.
	intentionallyStopped := hasAgent && ma.stopped

	if isStopped || intentionallyStopped {
		m.mu.Unlock()
		return
	}
	if !hasFactory {
		m.mu.Unlock()
		slog.Warn("runtime: agent dead but no factory registered, skipping restore",
			"agent_id", agentID, "reason", reason,
		)
		return
	}

	// Check restart limit and increment under the same write lock.
	if hasAgent && m.config.MaxRestartsPerAgent > 0 && ma.restarts >= m.config.MaxRestartsPerAgent {
		m.mu.Unlock()
		slog.Error("runtime: max restarts exceeded, not restoring",
			"agent_id", agentID, "restarts", ma.restarts,
			"max", m.config.MaxRestartsPerAgent, "reason", reason,
		)
		return
	}

	// Increment restarts under lock BEFORE launching the goroutine.
	if hasAgent {
		ma.restarts++
	}
	m.totalRestarts++
	m.mu.Unlock()

	slog.Warn("runtime: agent dead, scheduling restore",
		"agent_id", agentID, "reason", reason,
	)

	// Trigger RestoreAgent asynchronously via errgroup.
	m.g.Go(func() error {
		restoreCtx, restoreCancel := context.WithTimeout(m.gctx, m.config.RestoreTimeout)
		defer restoreCancel()

		if err := m.RestoreAgent(restoreCtx, agentID, factory); err != nil {
			slog.Error("runtime: restore failed",
				"agent_id", agentID, "error", err,
			)
		}
		return nil // Never propagate; runtime must keep running.
	})
}

// Start begins the runtime's monitoring loop and launches all registered agents.
func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("runtime: context must not be nil")
	}

	m.mu.Lock()
	if m.isStarted {
		m.mu.Unlock()
		return fmt.Errorf("runtime: already started")
	}
	if m.isStopped {
		m.mu.Unlock()
		return ErrRuntimeStopped
	}
	m.isStarted = true
	m.startTime = time.Now()

	childCtx, childCancel := context.WithCancel(ctx)
	m.cancel = childCancel
	m.g, m.gctx = errgroup.WithContext(childCtx)
	m.mu.Unlock()

	// Launch all registered agents in managed goroutines.
	m.mu.RLock()
	agentIDs := make([]string, 0, len(m.agents))
	for id, ma := range m.agents {
		if ma.agent != nil {
			agentCtx, agentCancel := context.WithCancel(m.gctx)
			ma.cancel = agentCancel
			agentIDs = append(agentIDs, id)

			currentAgent := ma.agent
			currentID := id
			m.g.Go(func() error {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("runtime: agent panicked",
							"agent_id", currentID, "panic", r,
						)
						m.NotifyAgentDead(currentID, fmt.Sprintf("panic: %v", r))
					}
				}()

				if err := currentAgent.Start(agentCtx); err != nil {
					if agentCtx.Err() != nil {
						return nil
					}
					slog.Error("runtime: agent start failed",
						"agent_id", currentID, "error", err,
					)
					m.NotifyAgentDead(currentID, fmt.Sprintf("start failed: %v", err))
					return nil
				}
				return nil
			})
		}
	}
	m.mu.RUnlock()

	// Background health check loop.
	m.g.Go(func() error {
		ticker := time.NewTicker(m.config.HealthCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-m.gctx.Done():
				return nil
			case <-ticker.C:
				m.healthCheck()
			}
		}
	})

	slog.Info("runtime: started", "agents", len(agentIDs))
	return nil
}

// Stop gracefully shuts down all agents and the runtime.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.isStarted || m.isStopped {
		m.mu.Unlock()
		return nil
	}
	m.isStopped = true
	m.cancel()
	m.mu.Unlock()

	// Stop all agents concurrently with an overall timeout.
	// Use Background because m.gctx is already cancelled by m.cancel() above.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), m.config.OverallStopTimeout)
	defer stopCancel()

	// Snapshot agents under write lock and mark all as stopped before launching goroutines.
	type agentStopInfo struct {
		id     string
		agent  base.Agent
		cancel context.CancelFunc
	}
	var toStop []agentStopInfo
	m.mu.Lock()
	for id, ma := range m.agents {
		if ma.stopped {
			continue
		}
		ma.stopped = true
		toStop = append(toStop, agentStopInfo{id: id, agent: ma.agent, cancel: ma.cancel})
	}
	m.mu.Unlock()

	g, _ := errgroup.WithContext(stopCtx)
	for _, info := range toStop {
		info := info
		g.Go(func() error {
			if info.cancel != nil {
				info.cancel()
			}
			if info.agent != nil {
				agentStopCtx, agentStopCancel := context.WithTimeout(stopCtx, m.config.AgentStopTimeout)
				defer agentStopCancel()
				if err := info.agent.Stop(agentStopCtx); err != nil {
					slog.Warn("runtime: failed to stop agent",
						"agent_id", info.id, "error", err,
					)
				}
			}
			return nil
		})
	}

	_ = g.Wait()

	// Wait for all errgroup goroutines.
	if m.g != nil {
		_ = m.g.Wait()
	}

	slog.Info("runtime: stopped", "total_restarts", m.totalRestarts)
	return nil
}

// Stats returns runtime statistics.
func (m *Manager) Stats() RuntimeStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Count agents that are managed (registered and not intentionally stopped).
	active := 0
	for _, ma := range m.agents {
		if ma.agent != nil && !ma.stopped {
			active++
		}
	}

	uptime := time.Duration(0)
	if !m.startTime.IsZero() {
		uptime = time.Since(m.startTime)
	}

	return RuntimeStats{
		ActiveAgents:  active,
		TotalRestarts: m.totalRestarts,
		Uptime:        uptime,
	}
}

// replayEvents reads events for the given agent stream from EventStore.
// Limits the number of events to prevent unbounded memory usage.
// Returns nil if eventStore is nil or an error occurs.
func (m *Manager) replayEvents(ctx context.Context, agentID string) []*events.Event {
	if m.eventStore == nil {
		return nil
	}
	// Use agentID directly as stream ID — matches how agents emit events
	// via emitEvent(ctx, eventType, payload) which uses a.id as stream ID.
	streamID := agentID
	limit := m.config.MaxReplayEvents
	if limit <= 0 {
		limit = 10000
	}
	evts, err := m.eventStore.Read(ctx, streamID, events.ReadOptions{
		Direction: events.ReadAscending,
		Limit:     limit,
	})
	if err != nil {
		slog.Warn("runtime: failed to read events for replay",
			"agent_id", agentID, "error", err,
		)
		return nil
	}
	return evts
}

// buildStateFromEvents constructs a state map from events for RestoreState.
// Currently extracts session_id from EventSessionCreated events.
func buildStateFromEvents(evts []*events.Event) map[string]any {
	state := make(map[string]any)
	for _, ev := range evts {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case events.EventSessionCreated:
			if sid, ok := ev.Payload["session_id"].(string); ok && sid != "" {
				state["session_id"] = sid
			}
		}
	}
	return state
}

// buildCognitiveState loads conversation history from MemoryManager for cognitive recovery.
// It enriches the operational state with session messages so the restored agent
// has context about prior conversations.
//
// Design note: The conversation_history is loaded here for completeness, but
// agent implementations (RestoreState) typically only consume session_id.
// The agent's initMemoryContext() loads conversation history on-demand via
// the MemoryManager using the restored session_id. This avoids duplicating
// the full conversation in the state map.
//
// Args:
//
//	ctx - context for memory operations.
//	agentID - the agent being restored.
//	operationalState - state built from events; used to find session_id if present.
//
//	Returns:
//
//	state - map with cognitive recovery data (session_id, conversation_history).
func (m *Manager) buildCognitiveState(ctx context.Context, agentID string, operationalState map[string]any) map[string]any {
	state := make(map[string]any)

	// Try to find session_id from operational state first, then from memory manager.
	sessionID, _ := operationalState["session_id"].(string)
	if sessionID == "" {
		// No session from events; try the memory manager checkpoint.
		// Use a bounded timeout to prevent hanging on slow DB.
		sessionCtx, sessionCancel := context.WithTimeout(ctx, 5*time.Second)
		sid, err := m.memManager.GetLatestSessionForLeader(sessionCtx, agentID)
		sessionCancel()
		if err != nil {
			slog.Warn("runtime: cognitive recovery: failed to get latest session",
				"agent_id", agentID, "error", err,
			)
			return state
		}
		sessionID = sid
	}

	if sessionID == "" {
		return state
	}

	// Load conversation history for the session with bounded timeout.
	msgCtx, msgCancel := context.WithTimeout(ctx, 5*time.Second)
	defer msgCancel()
	messages, err := m.memManager.GetMessages(msgCtx, sessionID)
	if err != nil {
		slog.Warn("runtime: cognitive recovery: failed to get messages",
			"agent_id", agentID, "session_id", sessionID, "error", err,
		)
		return state
	}

	if len(messages) > 0 {
		state["session_id"] = sessionID
		state["conversation_history"] = messages
		slog.Debug("runtime: cognitive recovery loaded",
			"agent_id", agentID,
			"session_id", sessionID,
			"messages", len(messages),
		)
	}

	return state
}

// healthCheck checks all agents for liveness. If an agent is offline
// unexpectedly, NotifyAgentDead is triggered.
func (m *Manager) healthCheck() {
	type agentCheck struct {
		id      string
		agent   base.Agent
		factory AgentFactory
	}

	m.mu.RLock()
	checks := make([]agentCheck, 0, len(m.agents))
	for id, ma := range m.agents {
		if ma.stopped {
			continue
		}
		checks = append(checks, agentCheck{
			id:      id,
			agent:   ma.agent,
			factory: ma.factory,
		})
	}
	m.mu.RUnlock()

	for _, c := range checks {
		if c.agent == nil {
			continue
		}
		// Prefer Heartbeater interface for liveness check if available.
		if h, ok := c.agent.(base.Heartbeater); ok {
			if !h.IsAlive() {
				if c.factory != nil {
					slog.Warn("runtime: health check: agent heartbeat failed",
						"agent_id", c.id,
					)
					m.NotifyAgentDead(c.id, "health check: heartbeat failed")
				}
			}
			continue
		}
		// Fall back to status-based check.
		status := c.agent.Status()
		if status == models.AgentStatusOffline || status == models.AgentStatusStopping {
			if c.factory != nil {
				slog.Warn("runtime: health check: agent status abnormal",
					"agent_id", c.id, "status", status,
				)
				m.NotifyAgentDead(c.id, "health check: status="+string(status))
			}
		}
	}
}

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
