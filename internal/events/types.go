// Package events defines event sourcing types and the event store interface.
package events

import (
	"errors"
	"time"
)

// Event represents something that happened in the system.
type Event struct {
	ID        string         `json:"id"`
	StreamID  string         `json:"stream_id"`
	Type      EventType      `json:"type"`
	Payload   map[string]any `json:"payload"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Version   int64          `json:"version"`
	Timestamp time.Time      `json:"timestamp"`
}

// EventType classifies events.
type EventType string

const (
	EventAgentStarted      EventType = "agent.started"
	EventAgentStopped      EventType = "agent.stopped"
	EventAgentFailed       EventType = "agent.failed"
	EventAgentRecovered    EventType = "agent.recovered"
	EventTaskCreated       EventType = "task.created"
	EventTaskDispatched    EventType = "task.dispatched"
	EventTaskCompleted     EventType = "task.completed"
	EventTaskFailed        EventType = "task.failed"
	EventSessionCreated    EventType = "session.created"
	EventMessageAdded      EventType = "message.added"
	EventMemoryDistilled   EventType = "memory.distilled"
	EventWorkflowStarted   EventType = "workflow.started"
	EventStepCompleted     EventType = "step.completed"
	EventStepFailed        EventType = "step.failed"
	EventStepSkipped       EventType = "step.skipped"
	EventFailoverTriggered EventType = "failover.triggered"
	EventFailoverCompleted EventType = "failover.completed"
)

// ReadDirection controls the order in which events are returned.
type ReadDirection int

const (
	// ReadAscending returns events from oldest to newest.
	ReadAscending ReadDirection = iota + 1
	// ReadDescending returns events from newest to oldest.
	ReadDescending
)

// ReadOptions configures event read operations.
type ReadOptions struct {
	// FromVersion specifies the starting version (inclusive).
	FromVersion int64
	// Limit caps the number of events returned. Zero means no limit.
	Limit int
	// Direction controls sort order. Defaults to ReadAscending.
	Direction ReadDirection
	// Since filters to events created at or after this time.
	Since time.Time
}

// EventFilter constrains event subscription queries.
type EventFilter struct {
	// StreamIDs, if non-empty, restricts events to these streams.
	StreamIDs []string
	// Types, if non-empty, restricts events to these types.
	Types []EventType
	// Since, if non-zero, restricts events created at or after this time.
	Since time.Time
}

// Sentinel errors for the events package.
var (
	// ErrVersionConflict indicates an optimistic concurrency violation on append.
	ErrVersionConflict = errors.New("version conflict")
	// ErrStreamNotFound indicates the requested stream does not exist.
	ErrStreamNotFound = errors.New("stream not found")
	// ErrEventStoreClosed indicates the store has been closed and cannot accept operations.
	ErrEventStoreClosed = errors.New("event store closed")
)
