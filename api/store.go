// Package api provides high-level abstractions for GoAgentX services.
// This file exposes event storage with automatic compaction at the API boundary,
// hiding internal/events implementation details from external consumers.
package api

import (
	"goagentx/internal/events"
)

// EventStore provides event storage with automatic compaction.
// When a stream exceeds the threshold, old events are summarized into
// compact snapshots and older raw events may be trimmed.
//
// Create via NewEventStore(), then use wherever events.EventStore is accepted.
// The underlying events.CompactableEventStore methods (ForceCompact, Summaries, etc.)
// are promoted and available directly.
type EventStore struct {
	*events.CompactableEventStore
	raw *events.MemoryEventStore
}

// NewEventStore creates an event store with auto-compaction.
// Events are stored in-memory with default compaction thresholds
// (500 events per stream, keep recent 100).
func NewEventStore() *EventStore {
	mem := events.NewMemoryEventStore()
	repo := events.NewMemorySummaryRepository()
	return &EventStore{
		CompactableEventStore: events.NewCompactableEventStore(
			mem, repo, nil, events.DefaultCompactionConfig(),
		),
		raw: mem,
	}
}

// RawStore exposes the underlying MemoryEventStore for components
// that require the concrete type (e.g., dashboard.Orchestrator.SetEventStore).
func (s *EventStore) RawStore() *events.MemoryEventStore {
	return s.raw
}
