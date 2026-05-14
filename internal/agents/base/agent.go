package base

import (
	"context"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/protocol/ahp"
)

// Agent represents the base interface for all agents.
type Agent interface {
	// ID returns the unique identifier of the agent.
	ID() string
	// Type returns the type of the agent.
	Type() models.AgentType
	// Status returns the current status of the agent.
	Status() models.AgentStatus
	// Start starts the agent.
	Start(ctx context.Context) error
	// Stop stops the agent.
	Stop(ctx context.Context) error
	// Process handles input and returns result.
	Process(ctx context.Context, input any) (any, error)
}

// Messenger defines message passing capabilities.
type Messenger interface {
	// SendMessage sends a message to another agent.
	SendMessage(ctx context.Context, msg *ahp.AHPMessage) error
	// ReceiveMessage receives a message from the message queue.
	ReceiveMessage(ctx context.Context) (*ahp.AHPMessage, error)
}

// Heartbeater defines heartbeat capabilities.
type Heartbeater interface {
	// Heartbeat sends a heartbeat signal.
	Heartbeat(ctx context.Context) error
	// IsAlive checks if the agent is alive.
	IsAlive() bool
}

// Config holds common agent configuration.
type Config struct {
	ID                string
	Type              models.AgentType
	HeartbeatInterval time.Duration
	MaxRetries        int
	Timeout           time.Duration
}

// DefaultConfig returns default agent configuration.
func DefaultConfig(agentType models.AgentType) *Config {
	return &Config{
		Type:              agentType,
		HeartbeatInterval: 30 * time.Second,
		MaxRetries:        3,
		Timeout:           5 * time.Minute,
	}
}
