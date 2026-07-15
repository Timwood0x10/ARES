// Package agent exposes the Agent public API — the contract an AI assistant
// uses to create, run, and stream from an agent without importing internal
// packages.
//
// Architecture:
//
//	api/agent              (this package: Agent interface + type aliases)
//	     ↑
//	internal/agents/base   (real implementation)
//
// The Agent interface is re-exported via type aliases so external callers
// can construct, start, stop, and stream from agents. AgentEvent and
// EventType are also re-exported for stream consumers.
package agent

import (
	"context"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// AgentType identifies the kind of agent (leader, sub-agent, destination, etc).
type AgentType = models.AgentType

// AgentStatus identifies the current lifecycle status of an agent.
type AgentStatus = models.AgentStatus

// Built-in agent type constants.
const (
	AgentTypeLeader      = models.AgentTypeLeader
	AgentTypeTop         = models.AgentTypeTop
	AgentTypeBottom      = models.AgentTypeBottom
	AgentTypeDestination = models.AgentTypeDestination
	AgentTypeFood        = models.AgentTypeFood
	AgentTypeHotel       = models.AgentTypeHotel
	AgentTypeItinerary   = models.AgentTypeItinerary
)

// Built-in agent status constants.
const (
	AgentStatusStarting = models.AgentStatusStarting
	AgentStatusReady    = models.AgentStatusReady
	AgentStatusBusy     = models.AgentStatusBusy
)

// EventType classifies an agent event emitted during streaming.
type EventType = base.EventType

// AgentEvent is the payload of a single streamed event from an agent.
type AgentEvent = base.AgentEvent

// Built-in EventType constants.
const (
	EventPlanning     = base.EventPlanning
	EventTaskStart    = base.EventTaskStart
	EventTaskProgress = base.EventTaskProgress
	EventTaskComplete = base.EventTaskComplete
	EventAggregating  = base.EventAggregating
	EventComplete     = base.EventComplete
	EventError        = base.EventError
)

// Agent is the public contract for an AI agent. External modules (including
// AI assistants) interact with agents through this interface — they never
// need to import internal/agents/base.
//
// Lifecycle:
//
//	agent.Start(ctx) → agent.Process(ctx, input) → agent.Stop(ctx)
//
// Streaming:
//
//	ch, _ := agent.ProcessStream(ctx, input)
//	for ev := range ch { /* handle AgentEvent */ }
type Agent interface {
	// ID returns the unique identifier of the agent.
	ID() string

	// Type returns the type of the agent.
	Type() AgentType

	// Status returns the current status of the agent.
	Status() AgentStatus

	// Start starts the agent.
	Start(ctx context.Context) error

	// Stop stops the agent.
	Stop(ctx context.Context) error

	// Process handles input and returns result.
	Process(ctx context.Context, input any) (any, error)

	// ProcessStream handles input and returns a stream of events.
	// The returned channel is closed when processing completes.
	ProcessStream(ctx context.Context, input any) (<-chan AgentEvent, error)
}

// Ensure internal/agents/base.Agent is compatible with the public Agent
// interface. This is a compile-time check — if the internal interface
// drifts, this will fail to build.
var _ Agent = (base.Agent)(nil)
