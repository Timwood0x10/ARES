// Package agents provides the public API for multi-agent orchestration.
package agents

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// ---------------------------------------------------------------------------
// Agent interface
// ---------------------------------------------------------------------------

// EventType represents the type of agent event.
type EventType = base.EventType

const (
	EventPlanning     = base.EventPlanning
	EventTaskStart    = base.EventTaskStart
	EventTaskProgress = base.EventTaskProgress
	EventTaskComplete = base.EventTaskComplete
	EventAggregating  = base.EventAggregating
	EventComplete     = base.EventComplete
	EventError        = base.EventError
)

// AgentEvent represents an event emitted during agent processing.
type AgentEvent = base.AgentEvent

// Agent is the base interface for all agents.
type Agent interface {
	ID() string
	Type() models.AgentType
	Status() models.AgentStatus
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Process(ctx context.Context, input any) (any, error)
	ProcessStream(ctx context.Context, input any) (<-chan AgentEvent, error)
}

// Messenger defines message passing between agents.
type Messenger interface {
	SendMessage(ctx context.Context, msg *ahp.AHPMessage) error
	ReceiveMessage(ctx context.Context) (*ahp.AHPMessage, error)
}

// Heartbeater defines heartbeat capabilities.
type Heartbeater interface {
	Heartbeat(ctx context.Context) error
	IsAlive() bool
}

// StatefulAgent supports restoration from snapshots.
type StatefulAgent interface {
	Agent
	RestoreState(state map[string]any) error
	ReplayEvents(events []*AgentEvent) error
	Snapshot() (map[string]any, error)
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Config holds common agent configuration.
type Config struct {
	ID                string
	Type              models.AgentType
	HeartbeatInterval time.Duration
	MaxRetries        int
	Timeout           time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig(agentType models.AgentType) *Config {
	cfg := base.DefaultConfig(agentType)
	return &Config{
		ID:                cfg.ID,
		Type:              cfg.Type,
		HeartbeatInterval: cfg.HeartbeatInterval,
		MaxRetries:        cfg.MaxRetries,
		Timeout:           cfg.Timeout,
	}
}

// ---------------------------------------------------------------------------
// Message (AHP protocol)
// ---------------------------------------------------------------------------

// AHPMessage represents a message between agents.
type AHPMessage = ahp.AHPMessage

// ---------------------------------------------------------------------------
// Leader agent
// ---------------------------------------------------------------------------

// LeaderAgent manages task decomposition and delegation.
type LeaderAgent = Agent

// NewLeader creates a new leader agent.
// The caller provides implementations for the internal interfaces.
func NewLeader(
	id string,
	profileParser any,
	taskPlanner any,
	taskDispatcher any,
	resultAggregator any,
	memMgr any,
	cfg *Config,
) (Agent, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Sub agent
// ---------------------------------------------------------------------------

// SubAgent executes tasks assigned by the leader.
type SubAgent = Agent

// NewSub creates a new sub agent.
func NewSub(
	id string,
	agentType models.AgentType,
	executor any,
	handler any,
	cfg *Config,
) (Agent, error) {
	return nil, nil
}
