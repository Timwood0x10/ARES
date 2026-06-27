package ares_events

import (
	"context"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// TrimAwareStore wraps an EventStore with the ability to delete (trim) old events
// after they have been compacted into summaries. This is implemented per-store-type
// since trimming requires different strategies for in-memory vs PostgreSQL stores.
type TrimAwareStore interface {
	EventStore
	// TrimBefore removes all events with version <= endVersion from the given stream.
	// This is called after compaction has successfully created a summary covering
	// those events. Returns the number of events removed.
	TrimBefore(ctx context.Context, streamID string, endVersion int64) (int64, error)
}

// PgTrimStore wraps PostgresEventStore to add trimming capability.
type PgTrimStore struct {
	*PostgresEventStore
	pool *postgres.Pool
}

// NewPgTrimStore creates a trim-capable wrapper around a PostgresEventStore.
func NewPgTrimStore(store *PostgresEventStore, pool *postgres.Pool) *PgTrimStore {
	return &PgTrimStore{PostgresEventStore: store, pool: pool}
}

// TrimBefore deletes events up to (and including) endVersion from the stream.
func (s *PgTrimStore) TrimBefore(ctx context.Context, streamID string, endVersion int64) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, ErrEventStoreClosed
	}

	result, err := s.pool.Exec(ctx,
		`DELETE FROM events WHERE stream_id = $1 AND version <= $2`,
		streamID, endVersion,
	)
	if err != nil {
		return 0, apperrors.Wrap(err, "trim events from stream")
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, apperrors.Wrap(err, "get rows affected after trim")
	}
	if removed > 0 {
		log.Info("event store: trimmed old events",
			"stream_id", streamID,
			"up_to_version", endVersion,
			"removed", removed,
		)
	}
	return removed, nil
}

// MemoryTrimStore wraps MemoryEventStore to add trimming capability.
type MemoryTrimStore struct {
	*MemoryEventStore
}

// NewMemoryTrimStore creates a trim-capable wrapper around a MemoryEventStore.
func NewMemoryTrimStore(store *MemoryEventStore) *MemoryTrimStore {
	return &MemoryTrimStore{MemoryEventStore: store}
}

// TrimBefore removes events up to (and including) endVersion from the in-memory stream.
func (s *MemoryTrimStore) TrimBefore(_ context.Context, streamID string, endVersion int64) (int64, error) {
	if s == nil || s.MemoryEventStore == nil {
		return 0, ErrEventStoreClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stream, exists := s.streams[streamID]
	if !exists {
		return 0, nil
	}

	// Find the first event beyond endVersion — everything before it is trimmed.
	split := len(stream) // Default: trim all.
	for i, evt := range stream {
		if evt.Version > endVersion {
			split = i
			break
		}
	}

	removed := int64(split)
	if split > 0 {
		s.streams[streamID] = stream[split:]

		var kept []*Event
		for _, evt := range s.events {
			if evt.StreamID != streamID || evt.Version > endVersion {
				kept = append(kept, evt)
			}
		}
		s.events = kept

		log.Info("memory event store: trimmed old events",
			"stream_id", streamID,
			"up_to_version", endVersion,
			"removed", removed,
		)
	}

	return removed, nil
}
