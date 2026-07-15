package ares_runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_ctxutil"
	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
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
//
// Lifecycle methods are split across files for readability:
//   - manager_lifecycle.go — Start, Stop, Stats, replay, health check
//   - manager_chaos.go     — Arena fault injection, ListAgents, GetAgentInfo
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
	// chaosConfig stores per-agent fault injection settings for the arena.
	chaosConfig map[string]chaosEntry
	// dagStore maps agent IDs to their workflow DAGs.
	// Used by the evolution system to apply workflow patches to the live DAG.
	// The DAG type is any (engine.MutableDAG) to avoid importing workflow/engine.
	dagStore map[string]any
}

// chaosSlowKey is the context key for SlowAgent delay duration.
type chaosSlowKey struct{}

// chaosEntry holds fault injection settings for a single agent.
type chaosEntry struct {
	slowDelay   time.Duration // zero = no slow
	toolTimeout time.Duration // zero = no timeout
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
		agents:      make(map[string]*managedAgent),
		factories:   make(map[string]AgentFactory),
		eventStore:  eventStore,
		memManager:  memManager,
		config:      config,
		chaosConfig: make(map[string]chaosEntry),
		dagStore:    make(map[string]any),
		g:           g,
		gctx:        gctx,
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
		log.Error("runtime: RegisterAgent called with nil agent")
		return
	}
	if factory == nil {
		log.Error("runtime: RegisterAgent called with nil factory", "agent_id", agent.ID())
		return
	}
	id := agent.ID()
	if id == "" {
		log.Error("runtime: RegisterAgent called with empty agent ID")
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

	log.Info("runtime: agent registered", "agent_id", id, "type", agent.Type())
}

// RegisterAgentDAG associates a workflow DAG with an agent.
// The evolution system uses this to apply workflow patches to the live DAG.
// dag is typically an *engine.MutableDAG, stored as any to avoid importing
// workflow/engine at this layer.
func (m *Manager) RegisterAgentDAG(agentID string, dag any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dagStore == nil {
		m.dagStore = make(map[string]any)
	}
	m.dagStore[agentID] = dag
	log.Info("runtime: DAG registered for agent", "agent_id", agentID)
}

// GetAgentDAG returns the workflow DAG associated with an agent, if any.
func (m *Manager) GetAgentDAG(agentID string) (any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dag, ok := m.dagStore[agentID]
	return dag, ok
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

	// Apply chaos engineering injections if configured for this agent.
	// Note: We already hold the write lock (m.mu.Lock() above),
	// so we can directly read chaosConfig without acquiring a read lock.
	chaos := m.chaosConfig[id]
	if chaos.slowDelay > 0 {
		agentCtx = context.WithValue(agentCtx, chaosSlowKey{}, chaos.slowDelay)
	}
	if chaos.toolTimeout > 0 {
		// Derive a timeout context from the cancellable parent. Keep both
		// cancel functions: the timeout cancel stops the timer, and the parent
		// cancel frees the WithCancel resources. Overwriting agentCancel with
		// only the timeout cancel leaks the parent context.
		timeoutCtx, timeoutCancel := context.WithTimeout(agentCtx, chaos.toolTimeout)
		agentCtx = timeoutCtx
		parentCancel := agentCancel
		agentCancel = func() {
			timeoutCancel()
			parentCancel()
		}
	}

	ma := &managedAgent{
		agent:  agent,
		cancel: agentCancel,
	}
	// Preserve factory if already registered via RegisterAgent.
	if f, ok := m.factories[id]; ok {
		ma.factory = f
	}
	m.agents[id] = ma

	// If runtime hasn't started yet, skip launching — Start() will re-launch
	// all agents with the real errgroup context (m.gctx). Launching now would
	// attach the goroutine to the pre-start errgroup which gets discarded,
	// creating an orphan agent whose context is never cancelled (R-01).
	if !m.isStarted {
		m.mu.Unlock()
		return nil
	}
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
			log.Warn("runtime: agent stop returned error",
				"agent_id", agentID, "error", err,
			)
		}
	}

	m.emitEvent(ctx, agentID, ares_events.EventAgentStopped, map[string]any{
		"agent_id": agentID,
		"reason":   "explicit_stop",
	})

	log.Info("runtime: agent stopped", "agent_id", agentID)
	return nil
}

// emitEvent appends a lifecycle event to the EventStore using the canonical
// ares_events.Emit helper. No-op if eventStore is nil.
func (m *Manager) emitEvent(ctx context.Context, streamID string, eventType ares_events.EventType, payload map[string]any) {
	if !ares_events.Emit(ctx, m.eventStore, streamID, eventType, "runtime", payload) {
		log.Warn("failed to emit event", "event_type", eventType, "stream_id", streamID)
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
			log.Warn("runtime: restart stop failed", "agent_id", agentID, "error", err)
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

	log.Info("runtime: agent restarted", "agent_id", agentID)
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

	log.Info("runtime: agent restored",
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
			log.Warn("runtime: restore stop old agent failed",
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
				log.Warn("runtime: RestoreState failed",
					"agent_id", agentID, "error", err,
				)
			}
		}
		if len(evts) > 0 {
			if err := sa.ReplayEvents(evts); err != nil {
				log.Warn("runtime: ReplayEvents failed",
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
				log.Error("runtime: agent panicked",
					"agent_id", agentID, "panic", r,
				)
				m.NotifyAgentDead(agentID, fmt.Sprintf("panic: %v", r))
			}
		}()

		if err := agent.Start(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Error("runtime: agent start failed",
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
			log.Warn("runtime: agent dead but no factory registered, skipping restore",
				"agent_id", agentID, "reason", reason,
			)
			return nil, false
		}
		if hasAgent && m.config.MaxRestartsPerAgent > 0 && ma.restarts >= m.config.MaxRestartsPerAgent {
			log.Error("runtime: max restarts exceeded, not restoring",
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

	log.Warn("runtime: agent dead, scheduling restore",
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
			log.Error("runtime: restore failed",
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
		log.Error("runtime: restore exhausted all retries",
			"agent_id", agentID, "max_attempts", maxAttempts,
		)
		return nil
	})
}
