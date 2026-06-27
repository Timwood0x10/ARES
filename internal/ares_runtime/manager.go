package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agents/base"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/ares_ctxutil"
	"github.com/Timwood0x10/ares/internal/ares_events"
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
	// resurrecting is set to true when NotifyAgentDead triggers RestoreAgent.
	// Prevents duplicate resurrection attempts for the same agent.
	resurrecting bool
}

// Manager implements the Runtime interface.
// It owns agent lifecycle: registration, start, stop, restart, and resurrection.
type Manager struct {
	mu            sync.RWMutex
	agents        map[string]*managedAgent
	factories     map[string]AgentFactory
	eventStore    ares_events.EventStore
	memManager    memory.MemoryManager
	snapshotStore base.SnapshotStore
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
func New(config *Config, eventStore ares_events.EventStore, memManager memory.MemoryManager) *Manager {
	if config == nil {
		config = DefaultConfig()
	}
	// Initialize errgroup with a labeled detached context so that m.g.Go() never
	// panics even if called before Start(). Start() will re-initialize with
	// the caller's context.
	g, gctx := errgroup.WithContext(ares_ctxutil.WithDetachedLabel("runtime:pre-start"))
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

// WithSnapshotStore sets the snapshot store used for agent state recovery.
// Must be called before Start(). Snapshots provide a richer state recovery
// path than event replay alone and should be used when a resurrection plugin
// periodically captures snapshots. When set, recoverAgentState will attempt
// to load a snapshot first, then supplement with event replay for any state
// the snapshot may lack.
func (m *Manager) WithSnapshotStore(store base.SnapshotStore) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotStore = store
	return m
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

	m.launchAgentGoroutine(agentCtx, id, agent)

	m.emitEvent(ctx, id, ares_events.EventAgentStarted, map[string]any{
		"agent_id": id,
		"type":     string(agent.Type()),
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

	m.emitEvent(ctx, agentID, ares_events.EventAgentStopped, map[string]any{
		"agent_id": agentID,
		"reason":   "explicit_stop",
	})

	slog.Info("runtime: agent stopped", "agent_id", agentID)
	return nil
}

// emitEvent appends a lifecycle event to the EventStore using the canonical
// ares_events.Emit helper. No-op if eventStore is nil.
func (m *Manager) emitEvent(ctx context.Context, streamID string, eventType ares_events.EventType, payload map[string]any) {
	if !ares_events.Emit(ctx, m.eventStore, streamID, eventType, payload) {
		slog.Warn("failed to emit event", "event_type", eventType, "stream_id", streamID)
	}
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

	m.emitEvent(ctx, agentID, ares_events.EventAgentStopped, map[string]any{
		"agent_id": agentID,
		"reason":   "restart",
	})

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

	m.launchAgentGoroutine(agentCtx, agentID, newAgent)

	m.emitEvent(ctx, agentID, ares_events.EventAgentStarted, map[string]any{
		"agent_id": agentID,
		"type":     "restart",
	})

	slog.Info("runtime: agent restarted", "agent_id", agentID)
	return nil
}

// RestoreAgent creates a new agent from factory, replays ares_events, restores memory, and starts it.
func (m *Manager) RestoreAgent(ctx context.Context, agentID string, factory AgentFactory) error {
	if factory == nil {
		return ErrNilFactory
	}

	m.emitEvent(ctx, agentID, ares_events.EventFailoverTriggered, map[string]any{
		"agent_id": agentID,
	})

	oldMA, oldExists := m.stopOldRestoredAgent(ctx, agentID)

	newAgent, err := m.recoverAgentState(ctx, agentID, factory)
	if err != nil {
		return err
	}
	if newAgent == nil {
		return fmt.Errorf("recover returned nil agent for %s", agentID)
	}

	prevRestarts := 0
	m.mu.Lock()
	if oldExists && oldMA != nil {
		prevRestarts = oldMA.restarts
	}
	agentCtx, agentCancel := context.WithCancel(m.gctx)
	m.agents[agentID] = &managedAgent{
		agent:    newAgent,
		factory:  factory,
		cancel:   agentCancel,
		restarts: prevRestarts,
	}
	m.mu.Unlock()

	m.launchAgentGoroutine(agentCtx, agentID, newAgent)

	m.emitEvent(ctx, agentID, ares_events.EventFailoverCompleted, map[string]any{
		"agent_id": agentID,
		"type":     newAgent.Type(),
	})

	slog.Info("runtime: agent restored",
		"agent_id", agentID, "type", newAgent.Type(),
		"restarts", prevRestarts,
	)
	return nil
}

// stopOldRestoredAgent marks the old agent as stopped and gracefully shuts it down.
func (m *Manager) stopOldRestoredAgent(ctx context.Context, agentID string) (*managedAgent, bool) {
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
		defer stopCancel()
		if err := oldMA.agent.Stop(stopCtx); err != nil {
			slog.Warn("runtime: restore stop old agent failed",
				"agent_id", agentID, "error", err,
			)
		}
	}
	return oldMA, oldExists
}

// recoverAgentState creates a new agent from factory, replays ares_events for operational
// recovery, and enriches state with memory context for cognitive recovery.
func (m *Manager) recoverAgentState(ctx context.Context, agentID string, factory AgentFactory) (base.Agent, error) {
	newAgent := factory()
	if newAgent == nil {
		return nil, fmt.Errorf("runtime: factory returned nil for agent %s", agentID)
	}

	evts := m.replayEvents(ctx, agentID)
	if sa, ok := newAgent.(base.StatefulAgent); ok {
		m.mu.RLock()
		store := m.snapshotStore
		m.mu.RUnlock()

		state := RecoverSnapshotOrEvents(ctx, store, agentID, func() map[string]any {
			state := buildStateFromEvents(evts)
			if m.memManager != nil {
				cognitiveState := m.buildCognitiveState(ctx, agentID, state)
				for k, v := range cognitiveState {
					state[k] = v
				}
			}
			return state
		})

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
	return newAgent, nil
}

// launchAgentGoroutine starts the agent in a managed goroutine with panic recovery.
func (m *Manager) launchAgentGoroutine(ctx context.Context, agentID string, agent base.Agent) {
	m.g.Go(func() error {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("runtime: agent panicked",
					"agent_id", agentID, "panic", r,
				)
				m.NotifyAgentDead(agentID, fmt.Sprintf("panic: %v", r))
			}
		}()

		if err := agent.Start(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("runtime: agent start failed",
				"agent_id", agentID, "error", err,
			)
			m.NotifyAgentDead(agentID, fmt.Sprintf("start failed: %v", err))
			return nil
		}
		return nil
	})
}

// NotifyAgentDead is called when an agent dies. It triggers asynchronous restoration
// via errgroup if a factory is registered for the agent.
func (m *Manager) NotifyAgentDead(agentID string, reason string) {
	factory, shouldRestore := func() (AgentFactory, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()

		factory, hasFactory := m.factories[agentID]
		ma, hasAgent := m.agents[agentID]

		if m.isStopped || (hasAgent && (ma.stopped || ma.resurrecting)) {
			return nil, false
		}
		if !hasFactory {
			slog.Warn("runtime: agent dead but no factory registered, skipping restore",
				"agent_id", agentID, "reason", reason,
			)
			return nil, false
		}
		if hasAgent && m.config.MaxRestartsPerAgent > 0 && ma.restarts >= m.config.MaxRestartsPerAgent {
			slog.Error("runtime: max restarts exceeded, not restoring",
				"agent_id", agentID, "restarts", ma.restarts,
				"max", m.config.MaxRestartsPerAgent, "reason", reason,
			)
			return nil, false
		}
		if hasAgent {
			ma.restarts++
			ma.resurrecting = true
		}
		m.totalRestarts++
		return factory, true
	}()
	if !shouldRestore {
		return
	}

	m.scheduleResurrection(agentID, factory)

	slog.Warn("runtime: agent dead, scheduling restore",
		"agent_id", agentID, "reason", reason,
	)

	m.emitEvent(ares_ctxutil.WithDetachedLabel("runtime:notify-agent-dead"), agentID, ares_events.EventAgentStopped, map[string]any{
		"agent_id":     agentID,
		"reason":       reason,
		"auto_restore": true,
	})
}

func (m *Manager) scheduleResurrection(agentID string, factory AgentFactory) {
	m.g.Go(func() error {
		// Exponential backoff: 1s, 2s, 4s, capped at 30s.
		backoff := time.Second
		const maxBackoff = 30 * time.Second
		const maxAttempts = 5
		timer := time.NewTimer(backoff)
		defer timer.Stop()
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			restoreCtx, restoreCancel := context.WithTimeout(m.gctx, m.config.RestoreTimeout)
			err := m.RestoreAgent(restoreCtx, agentID, factory)
			restoreCancel()
			if err == nil {
				m.mu.Lock()
				if entry, exists := m.agents[agentID]; exists {
					entry.resurrecting = false
				}
				m.mu.Unlock()
				return nil
			}
			slog.Error("runtime: restore failed",
				"agent_id", agentID, "attempt", attempt, "error", err,
			)
			if attempt < maxAttempts {
				timer.Reset(backoff)
				select {
				case <-m.gctx.Done():
					m.mu.Lock()
					if entry, exists := m.agents[agentID]; exists {
						entry.resurrecting = false
					}
					m.mu.Unlock()
					return nil
				case <-timer.C:
				}
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
		m.mu.Lock()
		if entry, exists := m.agents[agentID]; exists {
			entry.resurrecting = false
		}
		m.mu.Unlock()
		slog.Error("runtime: restore exhausted all retries",
			"agent_id", agentID, "max_attempts", maxAttempts,
		)
		return nil
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

	// Wire EventStore into MemoryManager if both are available.
	if m.memManager != nil && m.eventStore != nil {
		m.memManager.SetEventStore(m.eventStore, "memory-manager")
	}

	// Collect agent launch info under read lock, then launch outside lock
	// to avoid blocking concurrent agent registration during goroutine creation.
	type agentLaunch struct {
		id    string
		agent base.Agent
		ctx   context.Context
	}
	m.mu.RLock()
	launches := make([]agentLaunch, 0, len(m.agents))
	for id, ma := range m.agents {
		if ma.agent != nil {
			agentCtx, agentCancel := context.WithCancel(m.gctx)
			ma.cancel = agentCancel
			launches = append(launches, agentLaunch{id: id, agent: ma.agent, ctx: agentCtx})
		}
	}
	m.mu.RUnlock()

	for _, l := range launches {
		m.launchAgentGoroutine(l.ctx, l.id, l.agent)
	}

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

	slog.Info("runtime: started", "agents", len(launches))
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
	// Use a detached context because m.gctx is already cancelled by m.cancel() above.
	stopCtx, stopCancel := ares_ctxutil.WithDetachedTimeout("runtime:stop", m.config.OverallStopTimeout)
	defer stopCancel()

	// Capture final snapshots for stateful agents, then mark all as stopped.
	type agentStopInfo struct {
		id     string
		agent  base.Agent
		cancel context.CancelFunc
	}
	var toStop []agentStopInfo
	m.mu.Lock()
	store := m.snapshotStore
	for id, ma := range m.agents {
		if ma.stopped {
			continue
		}
		// Capture a final snapshot for stateful agents before shutdown.
		if store != nil {
			if sa, ok := ma.agent.(base.StatefulAgent); ok {
				snap, err := sa.Snapshot()
				if err != nil {
					slog.Warn("runtime: final snapshot failed",
						"agent_id", id, "error", err,
					)
				} else if snap != nil {
					if err := store.Save(stopCtx, id, snap); err != nil {
						slog.Warn("runtime: final snapshot save failed",
							"agent_id", id, "error", err,
						)
					}
				}
			}
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
		ActiveAgents:    active,
		TotalRestarts:   m.totalRestarts,
		Uptime:          uptime,
		BackgroundTasks: ares_ctxutil.BackgroundStats(),
	}
}

// replayEvents reads ares_events for the given agent stream from EventStore.
// Limits the number of ares_events to prevent unbounded memory usage.
// Returns nil if eventStore is nil or an error occurs.
// Verifies event stream integrity; logs warnings on gaps or corruption.
func (m *Manager) replayEvents(ctx context.Context, agentID string) []*ares_events.Event {
	if m.eventStore == nil {
		return nil
	}
	// Use agentID directly as stream ID — matches how agents emit ares_events
	// via emitEvent(ctx, eventType, payload) which uses a.id as stream ID.
	streamID := agentID
	limit := m.config.MaxReplayEvents
	if limit <= 0 {
		limit = 10000
	}
	evts, err := m.eventStore.Read(ctx, streamID, ares_events.ReadOptions{
		Direction: ares_events.ReadAscending,
		Limit:     limit,
	})
	if err != nil {
		slog.Warn("runtime: failed to read ares_events for replay",
			"agent_id", agentID, "error", err,
		)
		return nil
	}

	if len(evts) > 1 {
		if err := ares_events.VerifyStreamIntegrity(evts); err != nil {
			slog.Error("runtime: event stream integrity check failed",
				"agent_id", agentID,
				"event_count", len(evts),
				"error", err,
				"hash", ares_events.StreamHash(evts),
			)
		}

		// Semantic completeness: detect truncation by comparing last replayed
		// version against the store's stream version.
		if streamVersion, svErr := m.eventStore.StreamVersion(ctx, streamID); svErr == nil {
			lastVersion := evts[len(evts)-1].Version
			if lastVersion != streamVersion {
				slog.Error("runtime: event stream truncated",
					"agent_id", agentID,
					"last_replayed", lastVersion,
					"stream_version", streamVersion,
					"missing_events", streamVersion-lastVersion,
					"hash", ares_events.StreamHash(evts),
				)
			}
		} else if svErr != ares_events.ErrStreamNotFound {
			slog.Warn("runtime: failed to check stream version",
				"agent_id", agentID, "error", svErr,
			)
		}
	}

	return evts
}

// buildStateFromEvents constructs a state map from ares_events for RestoreState.
// Currently extracts session_id from EventSessionCreated ares_events.
func buildStateFromEvents(evts []*ares_events.Event) map[string]any {
	state := make(map[string]any)
	for _, ev := range evts {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case ares_events.EventSessionCreated:
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
//	operationalState - state built from ares_events; used to find session_id if present.
//
//	Returns:
//
//	state - map with cognitive recovery data (session_id, conversation_history).
func (m *Manager) buildCognitiveState(ctx context.Context, agentID string, operationalState map[string]any) map[string]any {
	state := make(map[string]any)

	// Try to find session_id from operational state first, then from memory manager.
	sessionID, _ := operationalState["session_id"].(string)
	if sessionID == "" {
		// No session from ares_events; try the memory manager checkpoint.
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
