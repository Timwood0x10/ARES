package events

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryEventStore is an in-memory implementation of EventStore.
// Use for development, testing, and prototyping. Not for production.
type MemoryEventStore struct {
	mu          sync.RWMutex
	events      []*Event
	streams     map[string][]*Event
	versions    map[string]int64
	subscribers []subscription
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
}

type subscription struct {
	id        string
	filter    EventFilter
	ch        chan *Event
	closeOnce *sync.Once
}

// NewMemoryEventStore creates a new in-memory EventStore.
func NewMemoryEventStore() *MemoryEventStore {
	ctx, cancel := context.WithCancel(context.Background())
	return &MemoryEventStore{
		streams:  make(map[string][]*Event),
		versions: make(map[string]int64),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Append writes events to a stream with optimistic concurrency control.
func (s *MemoryEventStore) Append(_ context.Context, streamID string, events []*Event, expectedVersion int64) error {
	if streamID == "" {
		return ErrStreamNotFound
	}
	if len(events) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrEventStoreClosed
	}

	currentVersion := s.versions[streamID]

	// Optimistic concurrency: if expectedVersion > 0, check it matches.
	if expectedVersion > 0 && currentVersion != expectedVersion {
		return ErrVersionConflict
	}

	// If expectedVersion == 0, append after current version (auto-detect).
	startVersion := expectedVersion
	if startVersion == 0 {
		startVersion = currentVersion
	}

	for i, event := range events {
		if event == nil {
			continue
		}
		event.Version = startVersion + int64(i+1)
		if event.Version <= 0 {
			return fmt.Errorf("version overflow: computed %d", event.Version)
		}
		event.StreamID = streamID
		if event.Timestamp.IsZero() {
			event.Timestamp = timeNow()
		}
		if event.ID == "" {
			event.ID = NewEventID()
		}

		s.events = append(s.events, event)
		s.streams[streamID] = append(s.streams[streamID], event)
		s.versions[streamID] = event.Version

		// Notify matching subscribers (non-blocking).
		s.notifySubscribers(event)
	}

	return nil
}

// Read returns events from a stream with optional filtering.
func (s *MemoryEventStore) Read(_ context.Context, streamID string, opts ReadOptions) ([]*Event, error) {
	if streamID == "" {
		return nil, ErrStreamNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrEventStoreClosed
	}

	stream := s.streams[streamID]
	if len(stream) == 0 {
		return nil, nil
	}

	// Filter by FromVersion (inclusive per ReadOptions contract).
	var filtered []*Event
	for _, event := range stream {
		if event.Version >= opts.FromVersion {
			filtered = append(filtered, event)
		}
	}

	// Filter by Since.
	if !opts.Since.IsZero() {
		var byTime []*Event
		for _, event := range filtered {
			if event.Timestamp.After(opts.Since) || event.Timestamp.Equal(opts.Since) {
				byTime = append(byTime, event)
			}
		}
		filtered = byTime
	}

	// Sort by direction.
	if opts.Direction == ReadDescending {
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Version > filtered[j].Version
		})
	}

	// Apply limit.
	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}

	if filtered == nil {
		return nil, nil
	}
	return filtered, nil
}

// ReadAll returns events across all streams.
func (s *MemoryEventStore) ReadAll(_ context.Context, opts ReadOptions) ([]*Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrEventStoreClosed
	}

	// FromVersion is per-stream and meaningless across streams,
	// so ReadAll ignores it (consistent with pg_store behavior).
	filtered := make([]*Event, len(s.events))
	copy(filtered, s.events)

	if !opts.Since.IsZero() {
		var byTime []*Event
		for _, event := range filtered {
			if event.Timestamp.After(opts.Since) || event.Timestamp.Equal(opts.Since) {
				byTime = append(byTime, event)
			}
		}
		filtered = byTime
	}

	if opts.Direction == ReadDescending {
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Timestamp.After(filtered[j].Timestamp)
		})
	} else {
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Timestamp.Before(filtered[j].Timestamp)
		})
	}

	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}

	if filtered == nil {
		return nil, nil
	}
	return filtered, nil
}

// Subscribe returns a channel that receives matching events.
func (s *MemoryEventStore) Subscribe(ctx context.Context, filter EventFilter) (<-chan *Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrEventStoreClosed
	}

	ch := make(chan *Event, 1)
	sub := subscription{
		id:        NewEventID(),
		filter:    filter,
		ch:        ch,
		closeOnce: &sync.Once{},
	}
	s.subscribers = append(s.subscribers, sub)

	// Unsubscribe when either the caller's context or the store is closed.
	go func() {
		select {
		case <-ctx.Done():
		case <-s.ctx.Done():
		}
		s.unsubscribe(sub.id)
	}()

	return ch, nil
}

// StreamVersion returns the current version of a stream.
func (s *MemoryEventStore) StreamVersion(_ context.Context, streamID string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return 0, ErrEventStoreClosed
	}

	return s.versions[streamID], nil
}

// Close closes the store and all subscriber channels.
func (s *MemoryEventStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	s.cancel()
	for _, sub := range s.subscribers {
		// Use sync.Once to ensure each channel is only closed once, preventing
		// panic if unsubscribe() is called concurrently.
		sub.closeOnce.Do(func() {
			close(sub.ch)
		})
	}
	s.subscribers = nil
	return nil
}

// notifySubscribers sends an event to all matching subscribers (non-blocking).
func (s *MemoryEventStore) notifySubscribers(event *Event) {
	for _, sub := range s.subscribers {
		if s.matchesFilter(event, sub.filter) {
			select {
			case sub.ch <- event:
			default:
				// Subscriber buffer full, drop event.
			}
		}
	}
}

// matchesFilter checks if an event matches a subscription filter.
func (s *MemoryEventStore) matchesFilter(event *Event, filter EventFilter) bool {
	// Filter by stream IDs.
	if len(filter.StreamIDs) > 0 {
		found := false
		for _, id := range filter.StreamIDs {
			if id == event.StreamID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by event types.
	if len(filter.Types) > 0 {
		found := false
		for _, t := range filter.Types {
			if t == event.Type {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by timestamp.
	if !filter.Since.IsZero() && event.Timestamp.Before(filter.Since) {
		return false
	}

	return true
}

// unsubscribe removes a subscriber by ID.
func (s *MemoryEventStore) unsubscribe(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, sub := range s.subscribers {
		if sub.id == id {
			// Use sync.Once to ensure the channel is only closed once, preventing
			// panic if Close() is called concurrently.
			sub.closeOnce.Do(func() {
				close(sub.ch)
			})
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			return
		}
	}
}

// timeNow returns the current time. Extracted for testability.
var timeNow = func() time.Time {
	return time.Now()
}
