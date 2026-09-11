package ares_events

// §3.6 (MEDIUM) regressions for the events package:
// bounded compaction reads, TTL=0 cleanup semantics, the per-stream summary
// cap, the compacted-Read fallback honoring ReadOptions, exclusive archive
// claims, the memory store's ToVersion window, and (integration-gated)
// concurrent PG appends.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── compactStream: bounded candidate read ──

// readOptionsSpy wraps a MemoryEventStore and records the ReadOptions every
// Read call received.
type readOptionsSpy struct {
	*MemoryEventStore
	mu     sync.Mutex
	opts   []ReadOptions
	stream []string
}

func (s *readOptionsSpy) Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error) {
	s.mu.Lock()
	s.opts = append(s.opts, opts)
	s.stream = append(s.stream, streamID)
	s.mu.Unlock()
	return s.MemoryEventStore.Read(ctx, streamID, opts)
}

func (s *readOptionsSpy) readCalls() ([]string, []ReadOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stream...), append([]ReadOptions(nil), s.opts...)
}

// TestCompactStreamReadIsBoundedToCandidateWindow pins the bounded read:
// compactStream must request ONLY the candidate window (versions up to
// streamVersion-KeepRecent) instead of loading the whole stream. Pre-fix the
// read carried no ToVersion at all — every event of the stream was
// materialized on every compaction (OOM on very long streams).
func TestCompactStreamReadIsBoundedToCandidateWindow(t *testing.T) {
	ctx := context.Background()
	spy := &readOptionsSpy{MemoryEventStore: NewMemoryEventStore()}
	appendTestEvents(t, spy.MemoryEventStore, "s", 10)

	c := NewCompactor(spy, newMockSummaryRepo(), CompactionConfig{
		Threshold:  1,
		KeepRecent: 3,
	})
	did, err := c.compactStream(ctx, "s")
	require.NoError(t, err)
	require.True(t, did)

	streams, opts := spy.readCalls()
	require.NotEmpty(t, opts, "compactStream must read the stream")
	for i, sid := range streams {
		if sid != "s" {
			continue
		}
		assert.Equal(t, int64(10-3), opts[i].ToVersion,
			"the candidate read must be capped at streamVersion-KeepRecent")
	}

	// The summary covers exactly the candidates (versions 1..7).
	summaries := c.repo.(*mockSummaryRepo).summariesFor("s")
	require.Len(t, summaries, 1)
	assert.Equal(t, int64(1), summaries[0].StartVersion)
	assert.Equal(t, int64(7), summaries[0].EndVersion)
	assert.Equal(t, 7, summaries[0].EventCount)
}

// ttlRepo extends the mock with a REAL DeleteOlderThan (the base mock stubs
// it to a no-op).
type ttlRepo struct {
	*mockSummaryRepo
	deleted []string
}

func (r *ttlRepo) DeleteOlderThan(_ context.Context, threshold time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for id, s := range r.summaries {
		if s.CreatedAt.Before(threshold) {
			delete(r.summaries, id)
			r.deleted = append(r.deleted, id)
			n++
		}
	}
	return n, nil
}

// ── SummaryTTL=0 disables cleanup ──

// TestCleanupOldSummariesZeroTTLDisablesCleanup pins the TTL semantics: a
// zero (or negative) SummaryTTL means "no expiry", NOT "everything already
// expired". Pre-fix, TTL=0 made the first cleanup pass delete every summary
// created before now — i.e. all of them.
func TestCleanupOldSummariesZeroTTLDisablesCleanup(t *testing.T) {
	ctx := context.Background()
	repo := &ttlRepo{mockSummaryRepo: newMockSummaryRepo()}
	// A summary created in the past — eligible under any positive TTL.
	require.NoError(t, repo.Save(ctx, &EventSummary{
		ID: "old-1", StreamID: "s", CreatedAt: time.Now().Add(-24 * time.Hour),
	}))
	require.NoError(t, repo.Save(ctx, &EventSummary{
		ID: "new-1", StreamID: "s", CreatedAt: time.Now(),
	}))

	for _, ttl := range []time.Duration{0, -time.Hour} {
		c := NewCompactor(NewMemoryEventStore(), repo, CompactionConfig{
			Threshold:  1,
			KeepRecent: 1,
			SummaryTTL: ttl,
		})
		removed, err := c.CleanupOldSummaries(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(0), removed, "TTL=%v must disable cleanup", ttl)
	}
	assert.Empty(t, repo.deleted, "no summary may be deleted by a disabled cleanup")

	// A positive TTL still cleans.
	c := NewCompactor(NewMemoryEventStore(), repo, CompactionConfig{
		Threshold:  1,
		KeepRecent: 1,
		SummaryTTL: time.Hour,
	})
	removed, err := c.CleanupOldSummaries(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed, "the 24h-old summary must be cleaned by a 1h TTL")
}

// summariesFor returns the mock repo's summaries for a stream (test helper
// defined here on the compactor_test.go mock, same package).
func (m *mockSummaryRepo) summariesFor(streamID string) []*EventSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*EventSummary
	for _, s := range m.summaries {
		if s.StreamID == streamID {
			out = append(out, s)
		}
	}
	return out
}

// ── MaxSummariesPerStream enforcement ──

// TestMaxSummariesPerStreamEnforced pins the documented cap: "older
// summaries are merged or pruned when this limit is exceeded". The field
// existed but nothing enforced it — summaries accumulated without bound.
func TestMaxSummariesPerStreamEnforced(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()
	repo := newMockSummaryRepo()
	c := NewCompactor(store, repo, CompactionConfig{
		Threshold:             1,
		KeepRecent:            0,
		MaxSummariesPerStream: 2,
	})

	// Three successive compactions on the same stream (each round appends
	// five more events so a fresh candidate window exists): each adds one
	// summary; the cap keeps only the newest two.
	appendAuto := func(n int) {
		for i := 0; i < n; i++ {
			require.NoError(t, store.Append(ctx, "s", []*Event{{
				StreamID: "s", Type: EventTaskCreated, Timestamp: time.Now(),
			}}, -1))
		}
	}
	for batch := 0; batch < 3; batch++ {
		appendAuto(5)
		did, err := c.compactStream(ctx, "s")
		require.NoError(t, err)
		require.True(t, did, "batch %d must compact", batch)
	}

	summaries := repo.summariesFor("s")
	assert.LessOrEqual(t, len(summaries), 2,
		"MaxSummariesPerStream=2 must prune the oldest summaries beyond the cap")
	// The survivors are the two NEWEST windows: the 1-5 window must be gone.
	// (All three windows share StartVersion=1, so "oldest" is decided by the
	// EndVersion tie-break — exactly the ambiguous case the prune must get
	// right.)
	endVersions := map[int64]bool{}
	for _, s := range summaries {
		endVersions[s.EndVersion] = true
	}
	assert.False(t, endVersions[5], "the oldest (1-5) summary must have been pruned, survivors: %v", endVersions)
	assert.True(t, endVersions[10], "the 1-10 summary must survive")
	assert.True(t, endVersions[15], "the newest (1-15) summary must survive")
}

// ── compacted Read fallback honors ReadOptions ──

// TestReadFallbackHonorsReadOptions pins the synthetic-summary fallback: a
// Read on a fully-compacted stream must apply the caller's ReadOptions to
// the synthetic events. Pre-fix the fallback ignored opts entirely — a
// Limit=1 read returned every summary; a DESC read returned ascending.
func TestReadFallbackHonorsReadOptions(t *testing.T) {
	ctx := context.Background()
	repo := newMockSummaryRepo()
	// Three summaries covering versions 1-10, 11-20, 21-30.
	for i := 1; i <= 3; i++ {
		require.NoError(t, repo.Save(ctx, &EventSummary{
			ID:           fmt.Sprintf("sum-%d", i),
			StreamID:     "s",
			SummaryText:  fmt.Sprintf("window %d", i),
			StartVersion: int64((i-1)*10 + 1),
			EndVersion:   int64(i * 10),
			CreatedAt:    time.Now().Add(time.Duration(i) * time.Minute),
		}))
	}
	store, err := NewCompactableEventStore(NewMemoryEventStore(), repo, nil, CompactionConfig{})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// Limit is honored.
	events, err := store.Read(ctx, "s", ReadOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, int64(10), events[0].Version, "ascending + limit 1 → first summary")

	// Direction is honored.
	events, err = store.Read(ctx, "s", ReadOptions{Direction: ReadDescending})
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, int64(30), events[0].Version, "descending → newest summary first")

	// Version window is honored.
	events, err = store.Read(ctx, "s", ReadOptions{FromVersion: 11, ToVersion: 20})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, int64(20), events[0].Version)

	// Since filter is honored (first summary is older than the cutoff).
	events, err = store.Read(ctx, "s", ReadOptions{Since: time.Now().Add(90 * time.Second)})
	require.NoError(t, err)
	require.Len(t, events, 2, "summaries created before the cutoff must be filtered")
}

// ── exclusive archive claim ──

// blockingSink records invocations and parks until released.
type blockingSink struct {
	mu       sync.Mutex
	invoked  int
	entered  chan struct{}
	release  chan struct{}
	failOnce sync.Once
}

func (s *blockingSink) archive(ctx context.Context, round int, streamID string, events []*Event) error {
	s.mu.Lock()
	s.invoked++
	s.mu.Unlock()
	s.failOnce.Do(func() { close(s.entered) })
	<-s.release
	return nil
}

// TestArchiveClaimIsExclusivePerStream pins the claim: two concurrent
// archivePendingRoundsOnce calls on the same stream must invoke the sink
// exactly ONCE for the round. Pre-fix the "CAS" only detected a claimant
// that arrived after a COMMIT — both goroutines passed the boundary check,
// and the round was archived twice.
func TestArchiveClaimIsExclusivePerStream(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()
	appendTestEvents(t, store, "s", 3)
	// A terminal event closes the round.
	require.NoError(t, store.Append(ctx, "s", []*Event{{
		StreamID: "s", Type: EventTaskCompleted, Version: 4, Timestamp: time.Now(),
	}}, -1))

	repo := newMockSummaryRepo()
	cs, err := NewCompactableEventStore(store, repo, nil, CompactionConfig{})
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()

	sink := &blockingSink{entered: make(chan struct{}), release: make(chan struct{})}
	cs.WithArchiveSink(sink.archive)

	first := make(chan error, 1)
	go func() { _, err := cs.archivePendingRoundsOnce(ctx, "s"); first <- err }()

	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first claim never reached the sink")
	}

	// Second claim while the first is inside the sink: must be refused
	// (in-flight), not parked on the same round.
	second := make(chan error, 1)
	go func() { _, err := cs.archivePendingRoundsOnce(ctx, "s"); second <- err }()
	select {
	case err := <-second:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("the in-flight claim must refuse a concurrent claim instead of blocking")
	}

	close(sink.release)
	require.NoError(t, <-first)

	sink.mu.Lock()
	invoked := sink.invoked
	sink.mu.Unlock()
	assert.Equal(t, 1, invoked, "the round must be archived exactly once")

	cs.archiveMu.Lock()
	round := cs.roundCounter["s"]
	boundary := cs.lastArchivedVersion["s"]
	cs.archiveMu.Unlock()
	assert.Equal(t, 1, round)
	assert.Equal(t, int64(4), boundary)
}

// ── memory store ToVersion window ──

// TestMemoryStoreReadToVersion pins the new inclusive upper bound on the
// memory store (the compactor's bounded read depends on it).
func TestMemoryStoreReadToVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()
	appendTestEvents(t, store, "s", 10)

	events, err := store.Read(ctx, "s", ReadOptions{ToVersion: 3})
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, int64(1), events[0].Version)
	assert.Equal(t, int64(3), events[2].Version)

	// Combined with FromVersion.
	events, err = store.Read(ctx, "s", ReadOptions{FromVersion: 2, ToVersion: 4})
	require.NoError(t, err)
	require.Len(t, events, 3)

	// Zero ToVersion = uncapped.
	events, err = store.Read(ctx, "s", ReadOptions{})
	require.NoError(t, err)
	require.Len(t, events, 10)
}

// ── PG query builder: ToVersion window ──

// TestBuildStreamReadQueryToVersion pins the PG-side candidate-window clause
// (unit-level; the live-PG append serialization is covered by the DSN-gated
// integration test above).
func TestBuildStreamReadQueryToVersion(t *testing.T) {
	q, args := buildStreamReadQuery("stream-x", ReadOptions{ToVersion: 7})
	assert.Contains(t, q, "AND version <= $2")
	require.Len(t, args, 2)
	assert.Equal(t, int64(7), args[1])

	// Window + direction + limit compose.
	q, args = buildStreamReadQuery("stream-x", ReadOptions{
		FromVersion: 2,
		ToVersion:   7,
		Direction:   ReadDescending,
		Limit:       3,
	})
	assert.Contains(t, q, "AND version >= $2")
	assert.Contains(t, q, "AND version <= $3")
	assert.Contains(t, q, "ORDER BY version DESC")
	assert.Contains(t, q, "LIMIT $4")
	require.Len(t, args, 4)
	assert.Equal(t, int64(2), args[1])
	assert.Equal(t, int64(7), args[2])
	assert.Equal(t, 3, args[3])
}

// ── PG Append serialization (integration, DSN-gated) ──

// TestPostgresAppendConcurrentSameStreamNoLostEvents pins the per-stream
// append serialization: N goroutines appending to the SAME stream with
// expectedVersion=0 (auto) must all succeed and produce a contiguous,
// gap-free version sequence. Pre-fix the MAX(version) read had no lock, so
// concurrent appends collided on the unique index and the losers' batches
// were dropped with a spurious ErrVersionConflict. Runs only when
// TEST_POSTGRES_DSN is set.
func TestPostgresAppendConcurrentSameStreamNoLostEvents(t *testing.T) {
	pool := getTestPool(t)
	s := newTestPostgresEventStore(t, pool)
	ctx := context.Background()

	const writers = 8
	const perWriter = 4
	streamID := fmt.Sprintf("concurrent-append-%d", time.Now().UnixNano())

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				ev := &Event{
					StreamID:  streamID,
					Type:      EventMessageAdded,
					Payload:   map[string]any{"i": i},
					Timestamp: time.Now(),
				}
				if err := s.Append(ctx, streamID, []*Event{ev}, 0); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent auto-append must not fail: %v", err)
	}

	events, err := s.Read(ctx, streamID, ReadOptions{Direction: ReadAscending})
	require.NoError(t, err)
	require.Len(t, events, writers*perWriter,
		"every concurrently appended event must survive")
	for i, ev := range events {
		assert.Equal(t, int64(i+1), ev.Version, "versions must be contiguous")
	}
}
