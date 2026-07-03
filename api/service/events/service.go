// Package events provides the public API for event sourcing.
package events

import (
	"context"

	internal "github.com/Timwood0x10/ares/internal/ares_events"
)

// Store wraps internal/ares_events.EventStore for public consumption.
type Store struct {
	inner internal.EventStore
}

// NewInMemory creates an in-memory event store.
func NewInMemory() *Store {
	return &Store{inner: internal.NewMemoryEventStore()}
}

// Append appends events to a stream.
func (s *Store) Append(ctx context.Context, streamID string, events []*internal.Event, expectedVersion int64) error {
	return s.inner.Append(ctx, streamID, events, expectedVersion)
}

// Read returns events for a single stream.
func (s *Store) Read(ctx context.Context, streamID string, opts internal.ReadOptions) ([]*internal.Event, error) {
	return s.inner.Read(ctx, streamID, opts)
}

// ReadAll returns events across all streams.
func (s *Store) ReadAll(ctx context.Context, opts internal.ReadOptions) ([]*internal.Event, error) {
	return s.inner.ReadAll(ctx, opts)
}

// Subscribe returns a channel that receives events matching the filter.
func (s *Store) Subscribe(ctx context.Context, filter internal.EventFilter) (<-chan *internal.Event, error) {
	return s.inner.Subscribe(ctx, filter)
}

// StreamVersion returns the current version of a stream.
func (s *Store) StreamVersion(ctx context.Context, streamID string) (int64, error) {
	return s.inner.StreamVersion(ctx, streamID)
}
