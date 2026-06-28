package ares_events

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// EventStore defines the interface for appending, reading, and subscribing to events.
type EventStore interface {
	// Append persists events to the given stream.
	// expectedVersion is used for optimistic concurrency control:
	//   - 0 means the stream must be empty or not exist.
	//   - Any positive value must match the stream's current version.
	// Returns ErrVersionConflict on mismatch.
	Append(ctx context.Context, streamID string, events []*Event, expectedVersion int64) error

	// Read returns events for a single stream, ordered by version.
	Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error)

	// ReadAll returns events across all streams, ordered by timestamp.
	ReadAll(ctx context.Context, opts ReadOptions) ([]*Event, error)

	// Subscribe returns a channel that receives events matching the filter.
	// The channel is closed when ctx is cancelled.
	Subscribe(ctx context.Context, filter EventFilter) (<-chan *Event, error)

	// StreamVersion returns the current version of a stream, or ErrStreamNotFound.
	StreamVersion(ctx context.Context, streamID string) (int64, error)
}

// EventAppender is the minimal subset of EventStore required by Emit.
// Accepting this interface lets callers with narrower store types
// (e.g. arena.EventStore) use the canonical helper without adapting.
type EventAppender interface {
	Append(ctx context.Context, streamID string, events []*Event, expectedVersion int64) error
}

// Ensure EventStore satisfies EventAppender at compile time.
var _ EventAppender = (EventStore)(nil)

// NewEventID generates a new unique event identifier.
func NewEventID() string {
	return uuid.New().String()
}

// Emit appends a single event to the store with a consistent format.
// This is the canonical emit helper — prefer it over inline Event construction.
// moduleName identifies the emitting module for traceability (e.g., "runtime", "workflow", "memory").
// Returns false on failure (logs a warning internally).
func Emit(ctx context.Context, store EventAppender, streamID string, eventType EventType, moduleName string, payload map[string]any) bool {
	if store == nil {
		return false
	}
	event := &Event{
		ID:         NewEventID(),
		StreamID:   streamID,
		Type:       eventType,
		ModuleName: moduleName,
		Payload:    payload,
		Timestamp:  time.Now(),
	}
	if err := store.Append(ctx, streamID, []*Event{event}, 0); err != nil {
		log.Warn("events: emit failed",
			"module", moduleName,
			"stream_id", streamID,
			"type", eventType,
			"error", err,
		)
		return false
	}
	return true
}
