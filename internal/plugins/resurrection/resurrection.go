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

	"goagentx/internal/agents/base"
	"goagentx/internal/core/models"
	"goagentx/internal/errors"
	"goagentx/internal/events"

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
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		CheckInterval:     10 * time.Second,
		ResurrectTimeout:  60 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 5 * time.Second,
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
	mu           sync.RWMutex
	agents       map[string]*watched
	health       HealthChecker
	config       Config
	eventStore   events.EventStore
	cancel       context.CancelFunc
	g            *errgroup.Group
	gctx         context.Context
	isStarted    bool
	isStopped    bool
	resurrecting map[string]bool // prevents concurrent resurrection for same agent
	resurrects   int
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
		_ = s.g.Wait()
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

// replayEvents reads all events for the given agent stream and reconstructs
// state that can be restored via StatefulAgent.RestoreState.
// Returns nil if eventStore is nil or no events are found.
func (s *Supervisor) replayEvents(ctx context.Context, agentID string) map[string]any {
	if s.eventStore == nil {
		return nil
	}
	evts, err := s.eventStore.Read(ctx, agentID, events.ReadOptions{})
	if err != nil || len(evts) == 0 {
		return nil
	}
	state := make(map[string]any)
	for _, ev := range evts {
		switch ev.Type {
		case events.EventSessionCreated:
			state["session_id"] = ev.Payload["session_id"]
		default:
			// Other event types are not used for state reconstruction yet.
		}
	}
	return state
}

// resurrect creates and starts a new agent instance.
func (s *Supervisor) resurrect(agentID string) {
	// Ensure we clear the resurrecting flag when done.
	defer func() {
		s.mu.Lock()
		delete(s.resurrecting, agentID)
		s.mu.Unlock()
	}()

	s.mu.RLock()
	w, exists := s.agents[agentID]
	s.mu.RUnlock()
	if !exists || w == nil {
		return
	}

	var newAgent base.Agent
	var lastErr error

	for attempt := 1; attempt <= s.config.MaxAttempts; attempt++ {
		// Check context cancellation between attempts.
		if s.gctx.Err() != nil {
			return
		}

		resCtx, cancel := context.WithTimeout(s.gctx, s.config.ResurrectTimeout)

		newAgent = w.factory()
		if newAgent == nil {
			lastErr = errors.New("resurrection: factory returned nil")
			cancel()
			continue
		}

		// Replay events and restore state before starting the new agent.
		state := s.replayEvents(resCtx, agentID)
		if state != nil {
			if sa, ok := newAgent.(base.StatefulAgent); ok {
				if restoreErr := sa.RestoreState(state); restoreErr != nil {
					slog.Warn("failed to restore state", "agent_id", agentID, "error", restoreErr)
				}
			}
		}

		// Start with timeout-bounded context.
		if err := newAgent.Start(resCtx); err != nil {
			lastErr = errors.Wrap(err, "resurrection: start failed")
			cancel()
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
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	slog.Info("resurrection: agent resurrected",
		"agent_id", agentID,
		"type", newAgent.Type(),
		"total_resurrects", total,
	)
}
