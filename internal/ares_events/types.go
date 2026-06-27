// Package events defines event sourcing types and the event store interface.
package ares_events

import (
	"crypto/sha256"
	"errors"
	"fmt"
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
	EventAgentStarted          EventType = "agent.started"
	EventAgentStopped          EventType = "agent.stopped"
	EventTaskCreated           EventType = "task.created"
	EventTaskDispatched        EventType = "task.dispatched"
	EventTaskCompleted         EventType = "task.completed"
	EventTaskFailed            EventType = "task.failed"
	EventSessionCreated        EventType = "session.created"
	EventMessageAdded          EventType = "message.added"
	EventMemoryDistilled       EventType = "memory.distilled"
	EventFailoverTriggered     EventType = "failover.triggered"
	EventFailoverCompleted     EventType = "failover.completed"
	EventLLMCall               EventType = "llm.call"
	EventToolCallStarted       EventType = "tool.call.started"
	EventToolCallCompleted     EventType = "tool.call.completed"
	EventStepFailed            EventType = "step.failed"
	EventStepRecoveryStarted   EventType = "step.recovery.started"
	EventStepRecoveryCompleted EventType = "step.recovery.completed"
	EventStepRecoveryFailed    EventType = "step.recovery.failed"
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
	// ErrEventIntegrity indicates event stream has gaps or version anomalies.
	ErrEventIntegrity = errors.New("event stream integrity violation")
)

// VerifyStreamIntegrity checks that a sequence of events has contiguous versions
// with no gaps or duplicates. Returns nil for empty or single-event streams.
// Legacy events with Version == 0 skip the check for backward compatibility.
func VerifyStreamIntegrity(evts []*Event) error {
	if len(evts) <= 1 {
		return nil
	}
	// Skip check if first event is version 0 (legacy events).
	if evts[0].Version == 0 {
		return nil
	}
	expected := evts[0].Version
	for i, ev := range evts {
		if ev.Version == 0 {
			return nil // mixed legacy/modern — skip further checks
		}
		if ev.Version != expected {
			return fmt.Errorf("%w: at index %d: expected version %d, got %d",
				ErrEventIntegrity, i, expected, ev.Version)
		}
		expected++
	}
	return nil
}

// StreamHash computes a deterministic hash for an event stream,
// useful for detecting silent corruption or partial writes.
func StreamHash(evts []*Event) string {
	if len(evts) == 0 {
		return ""
	}
	h := sha256.New()
	for _, ev := range evts {
		h.Write([]byte(ev.ID))
		h.Write([]byte(ev.Type))
		_, _ = fmt.Fprintf(h, "%d", ev.Version)
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:8])
}
