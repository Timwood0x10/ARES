// Package api provides high-level abstractions for ares services.
// This file exposes event storage with automatic compaction at the API boundary,
// hiding internal/ares_events implementation details from external consumers.
package apiimpl

import (
	"github.com/Timwood0x10/ares/internal/ares_events"
)

// EventStore provides event storage with automatic compaction.
// When a stream exceeds the threshold, old ares_events are summarized into
// compact snapshots and older raw ares_events may be trimmed.
//
// Create via NewEventStore(), then use wherever ares_events.EventStore is accepted.
// The underlying ares_events.CompactableEventStore methods (ForceCompact, Summaries, etc.)
// are promoted and available directly.
type EventStore struct {
	*ares_events.CompactableEventStore
	raw *ares_events.MemoryEventStore
}

// NewEventStore creates an event store with auto-compaction.
// Events are stored in-memory with default compaction thresholds
// (500 ares_events per stream, keep recent 100).
func NewEventStore() *EventStore {
	mem := ares_events.NewMemoryEventStore()
	repo := ares_events.NewMemorySummaryRepository()
	ces, err := ares_events.NewCompactableEventStore(
		mem, repo, nil, ares_events.DefaultCompactionConfig(),
	)
	if err != nil {
		// This should never happen with valid in-memory components.
		panic(err)
	}
	return &EventStore{
		CompactableEventStore: ces,
		raw:                   mem,
	}
}

// RawStore exposes the underlying MemoryEventStore for components
// that require the concrete type (e.g., dashboard.Orchestrator.SetEventStore).
func (s *EventStore) RawStore() *ares_events.MemoryEventStore {
	return s.raw
}
