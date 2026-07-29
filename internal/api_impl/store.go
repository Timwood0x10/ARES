// Package api provides high-level abstractions for ares services.
// This file exposes event storage with automatic compaction at the API boundary,
// hiding internal/ares_events implementation details from external consumers.
package apiimpl

import (
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_archive"
	"github.com/Timwood0x10/ares/internal/ares_config"
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
// Returns an error if the underlying compactable event store cannot be created.
func NewEventStore() (*EventStore, error) {
	mem := ares_events.NewMemoryEventStore()
	repo := ares_events.NewMemorySummaryRepository()
	ces, err := ares_events.NewCompactableEventStore(
		mem, repo, nil, ares_events.DefaultCompactionConfig(),
	)
	if err != nil {
		return nil, fmt.Errorf("create compactable event store: %w", err)
	}
	return &EventStore{
		CompactableEventStore: ces,
		raw:                   mem,
	}, nil
}

// NewEventStoreWithArchive creates an event store with optional round archiving.
// When archiveCfg.IsEnabled() is false, behaves identically to NewEventStore.
// When enabled, attaches an ares_archive.ArchiveWriter via an ArchiveSink so
// rounds are persisted before compaction.
//
// Construction is delegated to ares_archive.NewCompactableStoreWithArchive —
// the single construction source shared by both `ares serve` and `ares start`
// — so the two entry points never diverge on how the archive-enabled store is
// wired. This wrapper only adapts the result into the api_impl *EventStore
// shape (compactable + raw) that local callers and tests depend on.
func NewEventStoreWithArchive(archiveCfg ares_config.ArchiveConfig) (*EventStore, error) {
	ces, mem, err := ares_archive.NewCompactableStoreWithArchive(archiveCfg)
	if err != nil {
		return nil, err
	}
	return &EventStore{
		CompactableEventStore: ces,
		raw:                   mem,
	}, nil
}

// RawStore exposes the underlying MemoryEventStore for components
// that require the concrete type (e.g., dashboard.Orchestrator.SetEventStore).
func (s *EventStore) RawStore() *ares_events.MemoryEventStore {
	return s.raw
}
