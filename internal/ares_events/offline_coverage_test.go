// nolint: errcheck // Test code may ignore return values
package ares_events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTrimStore embeds a MemoryEventStore (satisfying EventStore) and adds the
// TrimBefore method, so it can back the Compactor.WithTrimStore path offline.
type stubTrimStore struct {
	*MemoryEventStore
	trimmed int64
}

func (s *stubTrimStore) TrimBefore(_ context.Context, _ string, endVersion int64) (int64, error) {
	s.trimmed += endVersion
	return endVersion, nil
}

func TestVerifyStreamIntegrity(t *testing.T) {
	ev := func(ver int64) *Event { return &Event{ID: "e", Type: "x", Version: ver} }

	// Empty and single-event streams are trivially valid.
	assert.NoError(t, VerifyStreamIntegrity(nil))
	assert.NoError(t, VerifyStreamIntegrity([]*Event{ev(1)}))

	// Contiguous versions pass.
	assert.NoError(t, VerifyStreamIntegrity([]*Event{ev(3), ev(4), ev(5)}))

	// Gap fails.
	err := VerifyStreamIntegrity([]*Event{ev(1), ev(3)})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEventIntegrity)

	// Legacy (version 0) first event short-circuits to success.
	assert.NoError(t, VerifyStreamIntegrity([]*Event{ev(0), ev(5), ev(9)}))

	// A zero version appearing later short-circuits too.
	assert.NoError(t, VerifyStreamIntegrity([]*Event{ev(1), ev(0), ev(3)}))
}

func TestStreamHash(t *testing.T) {
	assert.Equal(t, "", StreamHash(nil), "empty stream hashes to empty string")

	a1 := &Event{ID: "id-1", Type: "t", Version: 1}
	a2 := &Event{ID: "id-2", Type: "t", Version: 2}
	b2 := &Event{ID: "id-2", Type: "t", Version: 2}

	h := StreamHash([]*Event{a1, a2})
	assert.NotEmpty(t, h)
	// Deterministic: identical inputs produce the same hash.
	assert.Equal(t, h, StreamHash([]*Event{a1, b2}))
	// Different content differs.
	assert.NotEqual(t, h, StreamHash([]*Event{a2, a1}))
}

func TestMemoryEventStoreStats(t *testing.T) {
	s := NewMemoryEventStore()
	stats := s.Stats()
	assert.Equal(t, int64(0), stats["dropped_events"])
}

func TestCompactorWithTrimStoreSetter(t *testing.T) {
	repo := NewMemorySummaryRepository()
	c := NewCompactor(NewMemoryEventStore(), repo, DefaultCompactionConfig())
	trim := &stubTrimStore{MemoryEventStore: NewMemoryEventStore()}
	assert.Same(t, c, c.WithTrimStore(trim))
}

// TestCompactableEventStoreReadFallback covers CompactableEventStore.Read
// three paths: events present (returned directly), empty store + no summaries
// (empty result), and empty store + summaries present (synthetic events).
func TestCompactableEventStoreReadFallback(t *testing.T) {
	ctx := context.Background()
	memStore := NewMemoryEventStore()
	repo := NewMemorySummaryRepository()

	cs, err := NewCompactableEventStore(memStore, repo, nil, DefaultCompactionConfig())
	require.NoError(t, err)
	defer cs.Close()

	// 1. Underlying store empty + no summaries → empty slice.
	got, err := cs.Read(ctx, "s-a", ReadOptions{})
	require.NoError(t, err)
	assert.Empty(t, got)

	// 2. With a summary on the stream, the read falls back to synthetic events.
	base := time.Now()
	require.NoError(t, repo.Save(ctx, &EventSummary{
		ID:           "sum-1",
		StreamID:     "s-a",
		AgentID:      "agent-1",
		SummaryText:  "did work",
		EventCount:   5,
		StartVersion: 1,
		EndVersion:   6,
		Outcome:      "completed",
		CreatedAt:    base,
	}))
	got, err = cs.Read(ctx, "s-a", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, EventType("event.summary"), got[0].Type)
	assert.Equal(t, int64(6), got[0].Version)

	// 3. Events present in the store are returned directly (no summary fallback).
	evt := &Event{ID: "evt-1", StreamID: "s-b", Type: "x", Version: 1, Payload: map[string]any{"k": "v"}}
	require.NoError(t, memStore.Append(ctx, "s-b", []*Event{evt}, 0))
	got, err = cs.Read(ctx, "s-b", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "evt-1", got[0].ID)
}
