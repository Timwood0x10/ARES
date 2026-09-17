// nolint: errcheck // Test code may ignore return values
package ares_events

import (
	"context"
	"fmt"
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

// TestCompactableEventStoreReadMergesSummariesWithLiveTail pins the
// post-compaction read contract (P1): once compaction trimmed the head of a
// stream, the live tail is still non-empty, so the pre-fix early return at
// len(events) > 0 skipped the summary fallback entirely — ReplaySession and
// the dashboard silently showed a truncated history, exactly what the
// fallback exists to prevent. A full-range read must return the synthetic
// summaries (trimmed head) MERGED with the live tail, in version order.
func TestCompactableEventStoreReadMergesSummariesWithLiveTail(t *testing.T) {
	ctx := context.Background()
	mem := NewMemoryEventStore()
	repo := NewMemorySummaryRepository()
	cfg := CompactionConfig{Threshold: 10, KeepRecent: 5}

	cs, err := NewCompactableEventStore(mem, repo, mem, cfg)
	require.NoError(t, err)
	defer cs.Close()

	streamID := "merged-history"
	for i := 0; i < 20; i++ {
		require.NoError(t, cs.Append(ctx, streamID, []*Event{{
			ID: fmt.Sprintf("evt-%d", i), StreamID: streamID, Type: "x",
			Payload: map[string]any{"i": i},
		}}, int64(i)))
	}
	// Synchronous compact + trim: ForceCompact writes the summary, then trim
	// deletes the raw events it covers (the auto path's post-compact trim).
	didCompact, err := cs.ForceCompact(ctx, streamID)
	require.NoError(t, err)
	require.True(t, didCompact)
	summaries, err := repo.FindByStreamID(ctx, streamID)
	require.NoError(t, err)
	require.NotEmpty(t, summaries, "compaction must have produced a summary")
	latest := summaries[len(summaries)-1]
	trimmed, err := mem.TrimBefore(ctx, streamID, latest.EndVersion)
	require.NoError(t, err)
	require.Greater(t, trimmed, int64(0), "fixture must actually trim the head")

	// The live tail alone is non-empty — the pre-fix code returned here.
	live, err := mem.Read(ctx, streamID, ReadOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, live, "fixture must leave a live tail")

	got, err := cs.Read(ctx, streamID, ReadOptions{})
	require.NoError(t, err)

	var summaryCount, liveCount int
	for _, ev := range got {
		if ev.Type == EventType("event.summary") {
			summaryCount++
		} else {
			liveCount++
		}
	}
	require.NotZero(t, summaryCount,
		"trimmed head must surface as synthetic summary events alongside the live tail")
	require.Equal(t, len(live), liveCount, "every live event must still be returned")
	// Ascending version order across the merge.
	for i := 1; i < len(got); i++ {
		require.LessOrEqual(t, got[i-1].Version, got[i].Version,
			"merged history must be version-ordered")
	}

	// A window that starts AT the live head must not grow summaries: the
	// caller did not ask for trimmed history.
	fromLive := live[0].Version
	windowed, err := cs.Read(ctx, streamID, ReadOptions{FromVersion: fromLive})
	require.NoError(t, err)
	for _, ev := range windowed {
		require.NotEqual(t, EventType("event.summary"), ev.Type,
			"a window starting at the live head must not include summary events")
	}
}

// TestCompactableEventStoreSweepsIdleStreamBookkeeping pins the bounded
// bookkeeping contract (P1): serve mints one event stream per conversation,
// and the debounce cursor / touch time / archive round maps are keyed by
// stream — without a TTL sweep they kept three permanent entries per
// conversation ever seen. After the TTL, a stream's entries are dropped.
func TestCompactableEventStoreSweepsIdleStreamBookkeeping(t *testing.T) {
	ctx := context.Background()
	mem := NewMemoryEventStore()
	repo := NewMemorySummaryRepository()
	cs, err := NewCompactableEventStore(mem, repo, nil, DefaultCompactionConfig())
	require.NoError(t, err)
	defer cs.Close()
	// Test knobs (same package): shrink the TTL and the sweep interval so the
	// sweep runs on the next compaction check.
	cs.bookkeepingTTL = time.Millisecond
	cs.bookkeepingSweepEvery = 0

	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("conv-%d", i)
		require.NoError(t, cs.Append(ctx, id, []*Event{{
			ID: id + "-e0", StreamID: id, Type: "x", Payload: map[string]any{"i": i},
		}}, 0))
	}
	// Compaction checks run asynchronously; wait until every stream's
	// bookkeeping exists (no sleep-based sync — poll with a deadline).
	require.Eventually(t, func() bool {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		return len(cs.lastTouched) == 10
	}, 5*time.Second, 2*time.Millisecond, "fixture: every stream must have bookkeeping")

	// Simulate the streams going idle: rewind their touch times past the TTL,
	// then drive one more compaction check on a live stream.
	cs.mu.Lock()
	past := time.Now().Add(-time.Hour)
	for id := range cs.lastTouched {
		cs.lastTouched[id] = past
	}
	cs.mu.Unlock()

	require.NoError(t, cs.Append(ctx, "conv-live", []*Event{{
		ID: "live-e0", StreamID: "conv-live", Type: "x", Payload: map[string]any{},
	}}, 0))
	// The live stream's own async compaction check runs the TTL sweep; poll
	// until the idle entries are gone.
	require.Eventually(t, func() bool {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		return len(cs.lastTouched) <= 1 && len(cs.lastChecked) <= 1
	}, 5*time.Second, 2*time.Millisecond,
		"idle streams' bookkeeping must be swept")
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
