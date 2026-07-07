// Package core provides core interfaces for the ARES system.
package core

import "context"

// EventStore defines the interface for event sourcing operations.
type EventStore interface {
	// Append appends events to a stream.
	// Args:
	// ctx - operation context.
	// streamID - the stream to append to.
	// events - events to append.
	// expectedVersion - expected current version for optimistic concurrency (-1 for no check).
	// Returns error if append fails.
	Append(ctx context.Context, streamID string, events []interface{}, expectedVersion int64) error

	// Read retrieves events from a stream.
	// Args:
	// ctx - operation context.
	// streamID - the stream to read from.
	// afterVersion - only return events after this version (0 for all).
	// limit - max events to return.
	// Returns events or error.
	Read(ctx context.Context, streamID string, afterVersion int64, limit int) ([]interface{}, error)

	// Close closes the event store and releases resources.
	Close() error
}
