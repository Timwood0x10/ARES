// Package events provides the public API for event sourcing.
package events

import (
	"context"
	"time"

	internal "github.com/Timwood0x10/ares/internal/ares_events"
)

// Event represents something that happened in the system.
type Event struct {
	ID         string
	Type       string
	ModuleName string
	Payload    map[string]any
	Metadata   map[string]any
	Version    int64
}

// ReadDirection controls the sort order of read events.
type ReadDirection int

const (
	// ReadAscending returns events from oldest to newest.
	ReadAscending ReadDirection = iota + 1
	// ReadDescending returns events from newest to oldest.
	ReadDescending
)

// ReadOptions specifies options for reading events.
type ReadOptions struct {
	FromVersion int64
	Limit       int
	Direction   ReadDirection
	Since       time.Time
}

// EventFilter specifies criteria for subscribing to events.
type EventFilter struct {
	StreamIDs []string
	Types     []string
	Since     time.Time
}

// toInternalEvent converts a public Event to the internal Event type.
func toInternalEvent(e *Event) *internal.Event {
	if e == nil {
		return nil
	}
	return &internal.Event{
		ID:         e.ID,
		Type:       internal.EventType(e.Type),
		ModuleName: e.ModuleName,
		Payload:    e.Payload,
		Metadata:   e.Metadata,
		Version:    e.Version,
	}
}

// toPublicEvent converts an internal Event to the public Event type.
func toPublicEvent(e *internal.Event) *Event {
	if e == nil {
		return nil
	}
	return &Event{
		ID:         e.ID,
		Type:       string(e.Type),
		ModuleName: e.ModuleName,
		Payload:    e.Payload,
		Metadata:   e.Metadata,
		Version:    e.Version,
	}
}

// toPublicEvents converts a slice of internal Events to public Events.
func toPublicEvents(events []*internal.Event) []*Event {
	if events == nil {
		return nil
	}
	out := make([]*Event, len(events))
	for i, e := range events {
		out[i] = toPublicEvent(e)
	}
	return out
}

// toInternalEvents converts a slice of public Events to internal Events.
func toInternalEvents(events []*Event) []*internal.Event {
	if events == nil {
		return nil
	}
	out := make([]*internal.Event, len(events))
	for i, e := range events {
		out[i] = toInternalEvent(e)
	}
	return out
}

// toInternalReadOptions converts public ReadOptions to internal ReadOptions.
func toInternalReadOptions(opts ReadOptions) internal.ReadOptions {
	return internal.ReadOptions{
		FromVersion: opts.FromVersion,
		Limit:       opts.Limit,
		Direction:   internal.ReadDirection(opts.Direction),
		Since:       opts.Since,
	}
}

// toInternalEventFilter converts public EventFilter to internal EventFilter.
func toInternalEventFilter(filter EventFilter) internal.EventFilter {
	types := make([]internal.EventType, len(filter.Types))
	for i, t := range filter.Types {
		types[i] = internal.EventType(t)
	}
	return internal.EventFilter{
		StreamIDs: filter.StreamIDs,
		Types:     types,
		Since:     filter.Since,
	}
}

// Store wraps internal/ares_events.EventStore for public consumption.
type Store struct {
	inner internal.EventStore
}

// NewInMemory creates an in-memory event store.
func NewInMemory() *Store {
	return &Store{inner: internal.NewMemoryEventStore()}
}

// Append appends events to a stream.
func (s *Store) Append(ctx context.Context, streamID string, events []*Event, expectedVersion int64) error {
	return s.inner.Append(ctx, streamID, toInternalEvents(events), expectedVersion)
}

// Read returns events for a single stream.
func (s *Store) Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error) {
	events, err := s.inner.Read(ctx, streamID, toInternalReadOptions(opts))
	if err != nil {
		return nil, err
	}
	return toPublicEvents(events), nil
}

// ReadAll returns events across all streams.
func (s *Store) ReadAll(ctx context.Context, opts ReadOptions) ([]*Event, error) {
	events, err := s.inner.ReadAll(ctx, toInternalReadOptions(opts))
	if err != nil {
		return nil, err
	}
	return toPublicEvents(events), nil
}

// Subscribe returns a channel that receives events matching the filter.
func (s *Store) Subscribe(ctx context.Context, filter EventFilter) (<-chan *Event, error) {
	internalCh, err := s.inner.Subscribe(ctx, toInternalEventFilter(filter))
	if err != nil {
		return nil, err
	}

	ch := make(chan *Event, 1)
	go func() {
		defer close(ch)
		for e := range internalCh {
			select {
			case ch <- toPublicEvent(e):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// StreamVersion returns the current version of a stream.
func (s *Store) StreamVersion(ctx context.Context, streamID string) (int64, error) {
	return s.inner.StreamVersion(ctx, streamID)
}
