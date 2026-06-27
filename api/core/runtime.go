// Package core provides core abstractions for ARES modules.
package core

import (
	"context"
	"time"
)

// RuntimeConfig holds runtime configuration.
type RuntimeConfig struct {
	// HealthCheckInterval is the interval between agent liveness checks.
	HealthCheckInterval time.Duration
	// MaxRestartsPerAgent is the maximum number of restarts allowed per agent.
	MaxRestartsPerAgent int
	// MaxReplayEvents caps the number of events loaded during replay.
	MaxReplayEvents int
	// AgentStopTimeout is the timeout for stopping a single agent.
	AgentStopTimeout time.Duration
	// OverallStopTimeout is the timeout for stopping all agents during shutdown.
	OverallStopTimeout time.Duration
	// RestoreTimeout is the timeout for a single agent restoration attempt.
	RestoreTimeout time.Duration
}

// Runtime defines the interface for agent lifecycle management.
type Runtime interface {
	// RegisterAgent registers an agent with its factory for resurrection.
	RegisterAgent(agent Agent, factory AgentFactory)

	// StartAgent starts an agent.
	StartAgent(ctx context.Context, agent Agent) error

	// StopAgent stops an agent by ID.
	StopAgent(ctx context.Context, agentID string) error

	// GetAgent returns an agent by ID.
	GetAgent(agentID string) Agent

	// RestartAgent restarts an agent.
	RestartAgent(ctx context.Context, agentID string) error

	// RestoreAgent restores an agent from checkpoint.
	RestoreAgent(ctx context.Context, agentID string, factory AgentFactory) error

	// NotifyAgentDead notifies the runtime that an agent has died.
	NotifyAgentDead(agentID string, reason string)

	// Start starts the runtime.
	Start(ctx context.Context) error

	// Stop stops the runtime.
	Stop() error

	// Stats returns runtime statistics.
	Stats() RuntimeStats
}

// AgentFactory creates new agent instances.
type AgentFactory func() Agent

// RuntimeStats holds runtime statistics.
type RuntimeStats struct {
	ActiveAgents    int
	TotalRestarts   int
	Uptime          time.Duration
	BackgroundTasks map[string]int64
}
