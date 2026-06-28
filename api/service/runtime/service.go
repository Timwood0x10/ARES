// Package ares_runtime provides a high-level API for agent lifecycle management.
// It wraps internal/ares_runtime, internal/ares_events, and internal/plugins/resurrection
// into a single entry point for external users.
//
// Usage:
//
//	svc, _ := ares_runtime.NewService(ares_runtime.Config{...})
//	svc.RegisterAgent("worker-1", func() base.Agent { return NewWorker() })
//	svc.Start(ctx)
//	// Agent crashes → automatically resurrected with event replay
package ares_runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	ares_runtime "github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/plugins/resurrection"
)

// Config holds configuration for the ares_runtime service.
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
	rt         *ares_runtime.Manager
	supervisor *resurrection.Supervisor
	eventStore ares_events.EventStore
	hbMon      *ahp.HeartbeatMonitor
}

// NewService creates a new ares_runtime service with all components wired together.
//
// Args:
//
//	config - service configuration. Uses defaults if zero value.
//	eventStore - event store for operational recovery. If nil and config.UseMemoryStore
//	            is true, creates an in-memory store.
//
// Returns:
//
//	service - the ares_runtime service.
//	err - if configuration is invalid.
func NewService(config Config, eventStore ares_events.EventStore) (*Service, error) {
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
			eventStore = ares_events.NewMemoryEventStore()
		} else {
			return nil, fmt.Errorf("ares_runtime: event store required when UseMemoryStore is false")
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
		return nil, fmt.Errorf("ares_runtime: create resurrection supervisor: %w", err)
	}

	// Create ares_runtime manager.
	rtConfig := &ares_runtime.Config{
		HealthCheckInterval: config.HeartbeatInterval,
		MaxRestartsPerAgent: config.MaxRestartsPerAgent,
		MaxReplayEvents:     10000,
		AgentStopTimeout:    10 * time.Second,
		OverallStopTimeout:  30 * time.Second,
		RestoreTimeout:      config.ResurrectTimeout,
	}
	rt := ares_runtime.New(rtConfig, eventStore, nil)

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
	slog.Info("ares_runtime: agent registered", "agent_id", agent.ID(), "type", agent.Type())
}

// Start begins monitoring all registered agents.
func (s *Service) Start(ctx context.Context) error {
	if err := s.rt.Start(ctx); err != nil {
		return fmt.Errorf("ares_runtime: start manager: %w", err)
	}
	if err := s.supervisor.Start(ctx); err != nil {
		return fmt.Errorf("ares_runtime: start supervisor: %w", err)
	}
	slog.Info("ares_runtime: service started")
	return nil
}

// Stop gracefully shuts down all agents and monitoring.
func (s *Service) Stop() error {
	if err := s.supervisor.Stop(); err != nil {
		slog.Warn("ares_runtime: supervisor stop error", "error", err)
	}
	if err := s.rt.Stop(); err != nil {
		return fmt.Errorf("ares_runtime: stop manager: %w", err)
	}
	if c, ok := s.eventStore.(io.Closer); ok {
		if err := c.Close(); err != nil {
			slog.Warn("ares_runtime: event store close error", "error", err)
		}
	}
	slog.Info("ares_runtime: service stopped")
	return nil
}

// GetAgent returns the current instance of a managed agent.
func (s *Service) GetAgent(agentID string) base.Agent {
	return s.rt.GetAgent(agentID)
}

// Stats returns service statistics.
func (s *Service) Stats() ares_runtime.RuntimeStats {
	return s.rt.Stats()
}

// EventStore returns the underlying event store for external use.
func (s *Service) EventStore() ares_events.EventStore {
	return s.eventStore
}
