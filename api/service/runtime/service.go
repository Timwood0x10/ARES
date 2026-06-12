// Package runtime provides a high-level API for agent lifecycle management.
// It wraps internal/runtime, internal/events, and internal/plugins/resurrection
// into a single entry point for external users.
//
// Usage:
//
//	svc, _ := runtime.NewService(runtime.Config{...})
//	svc.RegisterAgent("worker-1", func() base.Agent { return NewWorker() })
//	svc.Start(ctx)
//	// Agent crashes → automatically resurrected with event replay
package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"goagentx/internal/agents/base"
	"goagentx/internal/events"
	"goagentx/internal/plugins/resurrection"
	"goagentx/internal/protocol/ahp"
	"goagentx/internal/runtime"
)

// Config holds configuration for the runtime service.
type Config struct {
	// HeartbeatInterval is how often agents send heartbeats.
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`

	// HeartbeatTimeout is how long before an agent is considered dead.
	HeartbeatTimeout time.Duration `yaml:"heartbeat_timeout"`

	// MaxMissedHeartbeats is the number of missed heartbeats before failure.
	MaxMissedHeartbeats int `yaml:"max_missed_heartbeats"`

	// MaxRestartsPerAgent is the max times an agent can be resurrected.
	MaxRestartsPerAgent int `yaml:"max_restarts_per_agent"`

	// ResurrectTimeout is the max time for a single resurrection.
	ResurrectTimeout time.Duration `yaml:"resurrect_timeout"`

	// UseMemoryStore uses in-memory event store (for dev/test).
	// If false, requires a PostgreSQL-backed EventStore.
	UseMemoryStore bool `yaml:"use_memory_store"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		HeartbeatInterval:   5 * time.Second,
		HeartbeatTimeout:    30 * time.Second,
		MaxMissedHeartbeats: 3,
		MaxRestartsPerAgent: 10,
		ResurrectTimeout:    60 * time.Second,
		UseMemoryStore:      true,
	}
}

// Service is the unified entry point for agent lifecycle management.
type Service struct {
	config     Config
	rt         *runtime.Manager
	supervisor *resurrection.Supervisor
	eventStore events.EventStore
	hbMon      *ahp.HeartbeatMonitor
}

// NewService creates a new runtime service with all components wired together.
//
// Args:
//
//	config - service configuration. Uses defaults if zero value.
//	eventStore - event store for operational recovery. If nil and config.UseMemoryStore
//	            is true, creates an in-memory store.
//
// Returns:
//
//	service - the runtime service.
//	err - if configuration is invalid.
func NewService(config Config, eventStore events.EventStore) (*Service, error) {
	// Apply defaults for zero values.
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = 5 * time.Second
	}
	if config.HeartbeatTimeout == 0 {
		config.HeartbeatTimeout = 30 * time.Second
	}
	if config.MaxMissedHeartbeats == 0 {
		config.MaxMissedHeartbeats = 3
	}
	if config.MaxRestartsPerAgent == 0 {
		config.MaxRestartsPerAgent = 10
	}
	if config.ResurrectTimeout == 0 {
		config.ResurrectTimeout = 60 * time.Second
	}

	// Create event store if not provided.
	if eventStore == nil {
		if config.UseMemoryStore {
			eventStore = events.NewMemoryEventStore()
		} else {
			return nil, fmt.Errorf("runtime: event store required when UseMemoryStore is false")
		}
	}

	// Create heartbeat monitor.
	hbMon := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  config.HeartbeatInterval,
		Timeout:   config.HeartbeatTimeout,
		MaxMissed: config.MaxMissedHeartbeats,
	})

	// Create resurrection plugin.
	health := resurrection.NewHeartbeatAdapter(hbMon)
	sup, err := resurrection.New(health, resurrection.Config{
		CheckInterval:     config.HeartbeatInterval,
		ResurrectTimeout:  config.ResurrectTimeout,
		MaxAttempts:       config.MaxRestartsPerAgent,
		HeartbeatInterval: config.HeartbeatInterval,
	}, eventStore)
	if err != nil {
		return nil, fmt.Errorf("runtime: create resurrection supervisor: %w", err)
	}

	// Create runtime manager.
	rtConfig := &runtime.Config{
		HealthCheckInterval: config.HeartbeatInterval,
		MaxRestartsPerAgent: config.MaxRestartsPerAgent,
		MaxReplayEvents:     10000,
		AgentStopTimeout:    10 * time.Second,
		OverallStopTimeout:  30 * time.Second,
		RestoreTimeout:      config.ResurrectTimeout,
	}
	rt := runtime.New(rtConfig, eventStore, nil)

	return &Service{
		config:     config,
		rt:         rt,
		supervisor: sup,
		eventStore: eventStore,
		hbMon:      hbMon,
	}, nil
}

// RegisterAgent registers an agent with a factory for lifecycle management.
// The factory creates a fresh agent instance for resurrection.
//
// Args:
//
//	agent - the agent to monitor.
//	factory - creates a fresh agent instance on resurrection.
func (s *Service) RegisterAgent(agent base.Agent, factory func() base.Agent) {
	if agent == nil || factory == nil {
		return
	}
	s.rt.RegisterAgent(agent, factory)
	s.supervisor.Watch(agent, factory)
	slog.Info("runtime: agent registered", "agent_id", agent.ID(), "type", agent.Type())
}

// Start begins monitoring all registered agents.
func (s *Service) Start(ctx context.Context) error {
	if err := s.rt.Start(ctx); err != nil {
		return fmt.Errorf("runtime: start manager: %w", err)
	}
	if err := s.supervisor.Start(ctx); err != nil {
		return fmt.Errorf("runtime: start supervisor: %w", err)
	}
	slog.Info("runtime: service started")
	return nil
}

// Stop gracefully shuts down all agents and monitoring.
func (s *Service) Stop() error {
	if err := s.supervisor.Stop(); err != nil {
		slog.Warn("runtime: supervisor stop error", "error", err)
	}
	if err := s.rt.Stop(); err != nil {
		return fmt.Errorf("runtime: stop manager: %w", err)
	}
	slog.Info("runtime: service stopped")
	return nil
}

// GetAgent returns the current instance of a managed agent.
func (s *Service) GetAgent(agentID string) base.Agent {
	return s.rt.GetAgent(agentID)
}

// Stats returns service statistics.
func (s *Service) Stats() runtime.RuntimeStats {
	return s.rt.Stats()
}

// EventStore returns the underlying event store for external use.
func (s *Service) EventStore() events.EventStore {
	return s.eventStore
}
