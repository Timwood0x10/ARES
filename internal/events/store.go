package events

import (
	"context"

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

// NewEventID generates a new unique event identifier.
func NewEventID() string {
	return uuid.New().String()
}
