// Package resurrection provides a pluggable agent resurrection plugin.
// Any agent type can be monitored and automatically recreated on failure.
//
// Usage:
//
//	supervisor, _ := resurrection.New(health, resurrection.Config{...}, nil)
//	supervisor.Watch(myAgent, func() base.Agent { return NewWorker() })
//	supervisor.Start(ctx)
//	// ... agent runs ...
//	// On failure: supervisor detects via HealthChecker, calls factory, starts new instance
package resurrection

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	runtime "github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/ctxutil"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/events"

	"golang.org/x/sync/errgroup"
)

// Sentinel errors for resurrection operations.
var (
	ErrNilHealthChecker = errors.New("resurrection: health checker is required")
	ErrAlreadyStarted   = errors.New("resurrection: already started")
	ErrAlreadyStopped   = errors.New("resurrection: already stopped")
	ErrNilContext       = errors.New("resurrection: context is required")
)

// HealthChecker abstracts health detection. Implementations include
// heartbeat monitors, HTTP probes, or process watchers.
type HealthChecker interface {
	// RegisterAgent registers an agent for health monitoring.
	RegisterAgent(agentID string)

	// UnregisterAgent removes an agent from monitoring.
	UnregisterAgent(agentID string)

	// RecordAlive signals that an agent is alive.
	RecordAlive(agentID string)

	// CheckHealth returns IDs of agents that have failed.
	CheckHealth() []string

	// OnFailure registers a callback invoked when an agent fails.
	OnFailure(fn func(agentID string))
}

// AgentFactory creates a fresh agent instance. Must return a new instance
// each time — reusing old instances may carry stale state.
type AgentFactory func() base.Agent

// Config holds resurrection plugin configuration.
type Config struct {
	// CheckInterval is how often to probe agent health.
	CheckInterval time.Duration `yaml:"check_interval"`

	// ResurrectTimeout is the max time for a single resurrection.
	ResurrectTimeout time.Duration `yaml:"resurrect_timeout"`

	// MaxAttempts is the max retries per resurrection.
	MaxAttempts int `yaml:"max_attempts"`

	// HeartbeatInterval is how often to send heartbeats for alive agents.
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`

	// MaxBackoff is the maximum backoff between resurrection attempts.
	// Defaults to 30s.
	MaxBackoff time.Duration `yaml:"max_backoff"`

	// InitialBackoff is the initial backoff before the first retry.
	// Defaults to 1s. Subsequent retries double until MaxBackoff.
	InitialBackoff time.Duration `yaml:"initial_backoff"`

	// SnapshotInterval is how often to capture snapshots of stateful agents.
	// If zero, periodic snapshots are disabled. Defaults to 0 (disabled).
	SnapshotInterval time.Duration `yaml:"snapshot_interval"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		CheckInterval:     10 * time.Second,
		ResurrectTimeout:  60 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 5 * time.Second,
		MaxBackoff:        30 * time.Second,
		InitialBackoff:    time.Second,
	}
}

// watched holds a watched agent and its factory.
type watched struct {
	agent   base.Agent
	factory AgentFactory
}

// Supervisor monitors agents and resurrects them on failure.
// It depends only on the HealthChecker interface — not on any
// specific heartbeat implementation.
type Supervisor struct {
	mu            sync.RWMutex
	agents        map[string]*watched
	health        HealthChecker
	config        Config
	eventStore    events.EventStore
	snapshotStore base.SnapshotStore
	cancel        context.CancelFunc
	g             *errgroup.Group
	gctx          context.Context
	isStarted     bool
	isStopped     bool
	resurrecting  map[string]bool // prevents concurrent resurrection for same agent
	resurrects    int
}

// New creates a new resurrection Supervisor.
//
// Args:
//
//	health - health checker implementation (e.g., ahp.HeartbeatMonitor adapter).
//	config - plugin config. Uses defaults for zero-value fields.
//	eventStore - event store for replaying agent state on resurrection (may be nil).
//
// Returns:
//
//	supervisor - the resurrection supervisor.
//	err - if health is nil.
func New(health HealthChecker, config Config, eventStore events.EventStore) (*Supervisor, error) {
	if health == nil {
		return nil, ErrNilHealthChecker
	}
	defaults := DefaultConfig()
	if config.CheckInterval == 0 {
		config.CheckInterval = defaults.CheckInterval
	}
	if config.ResurrectTimeout == 0 {
		config.ResurrectTimeout = defaults.ResurrectTimeout
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = defaults.MaxAttempts
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = defaults.HeartbeatInterval
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = defaults.MaxBackoff
	}
	if config.InitialBackoff == 0 {
		config.InitialBackoff = defaults.InitialBackoff
	}
	return &Supervisor{
		agents:       make(map[string]*watched),
		resurrecting: make(map[string]bool),
		health:       health,
		config:       config,
		eventStore:   eventStore,
	}, nil
}

// Watch registers an agent for monitoring with a factory for resurrection.
//
// Args:
//
//	agent - the agent to monitor. Must have a valid non-empty ID().
//	factory - creates a fresh agent instance on resurrection.
func (s *Supervisor) Watch(agent base.Agent, factory AgentFactory) {
	if agent == nil || factory == nil {
		return
	}
	id := agent.ID()
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.agents[id] = &watched{
		agent:   agent,
		factory: factory,
	}
	s.health.RegisterAgent(id)
	slog.Info("resurrection: agent watched", "agent_id", id, "type", agent.Type())
}

// Unwatch stops monitoring an agent.
func (s *Supervisor) Unwatch(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, agentID)
	s.health.UnregisterAgent(agentID)
	slog.Info("resurrection: agent unwatched", "agent_id", agentID)
}

// Start begins the monitoring loop. The context controls the supervisor lifecycle.
func (s *Supervisor) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	s.mu.Lock()
	if s.isStarted {
		s.mu.Unlock()
		return ErrAlreadyStarted
	}
	if s.isStopped {
		s.mu.Unlock()
		return ErrAlreadyStopped
	}
	s.isStarted = true
	childCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.g, s.gctx = errgroup.WithContext(childCtx)
	s.mu.Unlock()

	// Register failure callback.
	s.health.OnFailure(func(agentID string) {
		s.onFailure(agentID)
	})

	// Background: periodic health check + heartbeat.
	s.g.Go(func() error {
		heartbeatTicker := time.NewTicker(s.config.HeartbeatInterval)
		defer heartbeatTicker.Stop()
		for {
			select {
			case <-s.gctx.Done():
				return nil
			case <-heartbeatTicker.C:
				s.sendHeartbeats()
				s.health.CheckHealth()
			}
		}
	})

	// Background: periodic snapshot persistence for stateful agents.
	if s.snapshotStore != nil && s.config.SnapshotInterval > 0 {
		s.g.Go(func() error {
			return s.snapshotLoop()
		})
	}

	slog.Info("resurrection: supervisor started", "check_interval", s.config.CheckInterval)
	return nil
}

// Stop gracefully stops the supervisor.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	if !s.isStarted || s.isStopped {
		s.mu.Unlock()
		return nil
	}
	s.isStopped = true
	s.cancel()
	s.mu.Unlock()

	if s.g != nil {
		if err := s.g.Wait(); err != nil {
			slog.Error("resurrection: background task failed", "error", err)
		}
	}

	s.mu.RLock()
	total := s.resurrects
	s.mu.RUnlock()
	slog.Info("resurrection: supervisor stopped", "total_resurrects", total)
	return nil
}

// Agent returns the current instance of a watched agent.
func (s *Supervisor) Agent(agentID string) base.Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if w, ok := s.agents[agentID]; ok {
		return w.agent
	}
	return nil
}

// Stats returns supervisor statistics.
func (s *Supervisor) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	alive := 0
	statuses := make(map[string]string, len(s.agents))
	for id, w := range s.agents {
		st := w.agent.Status()
		statuses[id] = string(st)
		if st != models.AgentStatusOffline {
			alive++
		}
	}
	return Stats{
		Watched:    len(s.agents),
		Alive:      alive,
		Resurrects: s.resurrects,
		Statuses:   statuses,
	}
}

// Stats holds supervisor statistics.
type Stats struct {
	Watched    int               `json:"watched"`
	Alive      int               `json:"alive"`
	Resurrects int               `json:"resurrects"`
	Statuses   map[string]string `json:"statuses"`
}

// WithSnapshotStore sets the snapshot store for periodic state persistence.
// When set, the supervisor periodically calls Snapshot() on stateful agents
// and persists the result. On resurrection, the latest snapshot is loaded
// and restored before replaying events.
//
// Args:
//
//	store - the snapshot store implementation (e.g., MemorySnapshotStore).
func (s *Supervisor) WithSnapshotStore(store base.SnapshotStore) *Supervisor {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotStore = store
	return s
}

// sendHeartbeats signals liveness for all non-offline agents.
func (s *Supervisor) sendHeartbeats() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, w := range s.agents {
		if w.agent.Status() != models.AgentStatusOffline {
			s.health.RecordAlive(id)
		}
	}
}

// onFailure handles a failed agent. Skips if already being resurrected.
func (s *Supervisor) onFailure(agentID string) {
	s.mu.Lock()
	if s.isStopped {
		s.mu.Unlock()
		return
	}
	_, exists := s.agents[agentID]
	if !exists {
		s.mu.Unlock()
		return
	}
	// Deduplicate: skip if already resurrecting this agent.
	if s.resurrecting[agentID] {
		s.mu.Unlock()
		return
	}
	s.resurrecting[agentID] = true
	s.mu.Unlock()

	slog.Warn("resurrection: failure detected, starting resurrection", "agent_id", agentID)

	s.g.Go(func() error {
		s.resurrect(agentID)
		return nil
	})
}

// backoffWait sleeps with exponential backoff between resurrection attempts.
// backoff is mutated (doubled) on each call, capped at config.MaxBackoff.
func (s *Supervisor) backoffWait(attempt int, backoff *time.Duration) {
	if attempt >= s.config.MaxAttempts {
		return
	}
	timer := time.NewTimer(*backoff)
	select {
	case <-s.gctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}
	*backoff *= 2
	if *backoff > s.config.MaxBackoff {
		*backoff = s.config.MaxBackoff
	}
}

// replayEvents reads all events for the given agent stream and reconstructs
// state that can be restored via StatefulAgent.RestoreState.
// Returns nil if eventStore is nil or no events are found.
// Verifies event stream integrity; logs warnings on gaps or corruption.
//
// Supported event types for state reconstruction:
//   - EventSessionCreated: extracts session_id and user_id
//   - EventTaskCreated: extracts last_task_id
//   - EventAgentStarted/Stopped: tracks status transitions
func (s *Supervisor) replayEvents(ctx context.Context, agentID string) map[string]any {
	if s.eventStore == nil {
		return nil
	}
	evts, err := s.eventStore.Read(ctx, agentID, events.ReadOptions{})
	if err != nil || len(evts) == 0 {
		return nil
	}

	if err := events.VerifyStreamIntegrity(evts); err != nil {
		slog.Error("resurrection: event stream integrity check failed",
			"agent_id", agentID,
			"event_count", len(evts),
			"error", err,
			"hash", events.StreamHash(evts),
		)
	}

	// Semantic completeness: check if the stream was truncated.
	if streamVersion, svErr := s.eventStore.StreamVersion(ctx, agentID); svErr == nil {
		lastVersion := evts[len(evts)-1].Version
		if lastVersion != streamVersion {
			slog.Error("resurrection: event stream truncated",
				"agent_id", agentID,
				"last_replayed", lastVersion,
				"stream_version", streamVersion,
				"missing_events", streamVersion-lastVersion,
				"hash", events.StreamHash(evts),
			)
		}
	} else if svErr != events.ErrStreamNotFound {
		slog.Warn("resurrection: failed to check stream version",
			"agent_id", agentID, "error", svErr,
		)
	}

	state := make(map[string]any)

	for _, ev := range evts {
		switch ev.Type {
		case events.EventSessionCreated:
			state["session_id"] = ev.Payload["session_id"]
			state["user_id"] = ev.Payload["user_id"]

		case events.EventTaskCreated:
			if tid, ok := ev.Payload["task_id"].(string); ok && tid != "" {
				state["last_task_id"] = tid
			}

		case events.EventAgentStarted:
			state["agent_status"] = string(models.AgentStatusReady)

		case events.EventAgentStopped:
			state["agent_status"] = string(models.AgentStatusOffline)

		default:
			// Other event types are not used for state reconstruction yet.
		}
	}

	return state
}

// resurrect creates and starts a new agent instance.
func (s *Supervisor) resurrect(agentID string) {
	s.mu.RLock()
	w, exists := s.agents[agentID]
	s.mu.RUnlock()
	if !exists || w == nil {
		return
	}

	// Clean up resurrecting flag when done.
	// Note: cleared explicitly before verifyResurrection below;
	// this defer covers early-return paths.
	defer func() {
		s.mu.Lock()
		delete(s.resurrecting, agentID)
		s.mu.Unlock()
	}()

	var newAgent base.Agent
	var lastErr error
	var state map[string]any

	backoff := s.config.InitialBackoff
	for attempt := 1; attempt <= s.config.MaxAttempts; attempt++ {
		if s.gctx.Err() != nil {
			return
		}

		resCtx, cancel := context.WithTimeout(s.gctx, s.config.ResurrectTimeout)

		newAgent = w.factory()
		if newAgent == nil {
			lastErr = errors.New("resurrection: factory returned nil")
			cancel()
			s.backoffWait(attempt, &backoff)
			continue
		}

		// Try snapshot-based recovery first for full state restoration.
		// If no snapshot exists, fall back to event-based state reconstruction.
		s.mu.RLock()
		store := s.snapshotStore
		s.mu.RUnlock()
		state = runtime.RecoverSnapshotOrEvents(resCtx, store, agentID, func() map[string]any {
			return s.replayEvents(resCtx, agentID)
		})
		if state != nil {
			if sa, ok := newAgent.(base.StatefulAgent); ok {
				if restoreErr := sa.RestoreState(state); restoreErr != nil {
					slog.Warn("failed to restore state", "agent_id", agentID, "error", restoreErr)
				}
			}
		}

		if err := newAgent.Start(resCtx); err != nil {
			lastErr = errors.Wrap(err, "resurrection: start failed")
			cancel()
			s.backoffWait(attempt, &backoff)
			continue
		}

		cancel()
		lastErr = nil
		break
	}

	if lastErr != nil {
		slog.Error("resurrection: all attempts exhausted",
			"agent_id", agentID,
			"max_attempts", s.config.MaxAttempts,
			"last_error", lastErr,
		)
		return
	}

	// Stop old agent to release resources.
	s.mu.RLock()
	oldEntry := s.agents[agentID]
	s.mu.RUnlock()
	if oldEntry == nil {
		return
	}
	oldAgent := oldEntry.agent
	if oldAgent != nil {
		stopCtx, stopCancel := ctxutil.WithDetachedTimeout("resurrection:stop-old", 10*time.Second)
		if err := oldAgent.Stop(stopCtx); err != nil {
			slog.Warn("resurrection: failed to stop old agent", "agent_id", agentID, "error", err)
		}
		stopCancel()
	}

	// Replace old agent with new one.
	s.mu.Lock()
	s.agents[agentID] = &watched{
		agent:   newAgent,
		factory: w.factory,
	}
	s.resurrects++
	total := s.resurrects
	s.mu.Unlock()

	s.health.RecordAlive(agentID)

	// Clear the resurrecting flag so verifyResurrection can re-trigger
	// onFailure if the revived agent is unhealthy.
	s.mu.Lock()
	delete(s.resurrecting, agentID)
	s.mu.Unlock()

	healthy := s.verifyResurrection(agentID, newAgent)

	if healthy {
		slog.Info("resurrection: agent resurrected",
			"agent_id", agentID,
			"type", newAgent.Type(),
			"total_resurrects", total,
		)
	} else {
		slog.Error("resurrection: revived agent unhealthy, re-triggering",
			"agent_id", agentID,
			"total_resurrects", total,
		)
	}
}

// verifyResurrection does a quick health check after resurrection.
// If the agent is unhealthy, immediately re-triggers failure detection
// so the supervisor cycles back into resurrection rather than waiting
// for the next periodic health check tick.
func (s *Supervisor) verifyResurrection(agentID string, agent base.Agent) bool {
	healthy := true
	if hb, ok := agent.(base.Heartbeater); ok {
		if !hb.IsAlive() {
			slog.Error("resurrection: revived agent reports not alive",
				"agent_id", agentID,
			)
			healthy = false
		}
	}
	if agent.Status() == models.AgentStatusOffline {
		slog.Error("resurrection: revived agent is offline",
			"agent_id", agentID,
		)
		healthy = false
	}

	if !healthy {
		slog.Warn("resurrection: re-triggering failure for unhealthy revived agent",
			"agent_id", agentID,
		)
		s.health.RecordAlive(agentID)
		s.onFailure(agentID)
	}
	return healthy
}

// snapshotLoop periodically captures snapshots of all watched stateful
// agents and persists them to the snapshot store. Runs until the
// supervisor context is cancelled.
func (s *Supervisor) snapshotLoop() error {
	ticker := time.NewTicker(s.config.SnapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.gctx.Done():
			return nil
		case <-ticker.C:
			s.takeSnapshots()
		}
	}
}

// takeSnapshots iterates all watched agents and persists a snapshot for
// each that implements StatefulAgent. Non-stateful agents are skipped.
func (s *Supervisor) takeSnapshots() {
	s.mu.RLock()
	agents := make(map[string]base.Agent, len(s.agents))
	for id, w := range s.agents {
		agents[id] = w.agent
	}
	store := s.snapshotStore
	s.mu.RUnlock()

	if store == nil {
		return
	}
	for id, agent := range agents {
		sa, ok := agent.(base.StatefulAgent)
		if !ok {
			continue
		}
		snap, err := sa.Snapshot()
		if err != nil {
			slog.Warn("resurrection: snapshot capture failed",
				"agent_id", id, "error", err,
			)
			continue
		}
		if snap == nil {
			continue
		}
		if err := store.Save(s.gctx, id, snap); err != nil {
			slog.Warn("resurrection: snapshot save failed",
				"agent_id", id, "error", err,
			)
		}
	}
}
