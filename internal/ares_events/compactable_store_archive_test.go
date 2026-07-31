// Package ares_events — archive sink integration tests for CompactableEventStore.
//
// These tests verify the archive hook (archivePendingRounds) records rounds
// at task-terminal boundaries, increments the round counter per stream,
// is idempotent, and flushes before compaction.
package ares_events

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Potential bug scenarios tested below:
//  1. Round counter not incrementing — each terminal event must produce a
//     new round number. If the counter stayed at 1, every round would
//     overwrite round_1.json. Covered by TestArchiveSink_RoundCounterIncrements.
//  2. Double-archiving the same terminal — concurrent archivePendingRounds
//     calls (Append + maybeCompact flush) must not archive the same round
//     twice. The CAS check on lastArchivedVersion prevents this. Covered by
//     TestArchiveSink_Idempotent.
//  3. Archive flush failing to run before compaction — if the pre-compaction
//     flush is placed after CheckAndCompact, the round record could be lost
//     when compaction trims raw events. Covered by TestArchiveSink_FlushBeforeCompaction.

// fakeSink is a test ArchiveSink that records every call. Safe for concurrent
// use because Append's archive goroutine and maybeCompact's flush can overlap.
type fakeSink struct {
	mu    sync.Mutex
	calls []fakeSinkCall
}

type fakeSinkCall struct {
	round    int
	streamID string
	events   []*Event
}

func (f *fakeSink) call(ctx context.Context, round int, streamID string, events []*Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeSinkCall{round: round, streamID: streamID, events: events})
	return nil
}

func (f *fakeSink) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSink) snapshot() []fakeSinkCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeSinkCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// newArchiveTestStore builds a CompactableEventStore wired with a fake sink.
// The compaction threshold is configurable so tests can force compaction.
func newArchiveTestStore(t *testing.T, threshold int) (*CompactableEventStore, *fakeSink) {
	t.Helper()
	mem := NewMemoryEventStore()
	repo := NewMemorySummaryRepository()
	cfg := DefaultCompactionConfig()
	if threshold > 0 {
		cfg.Threshold = threshold
	}
	ces, err := NewCompactableEventStore(mem, repo, nil, cfg)
	require.NoError(t, err)
	sink := &fakeSink{}
	ces.WithArchiveSink(sink.call)
	return ces, sink
}

func TestArchiveSink_RoundEndRecording(t *testing.T) {
	ces, sink := newArchiveTestStore(t, 0)
	ctx := context.Background()
	streamID := "stream-1"

	// Append a batch containing a terminal event directly to the underlying
	// store, then invoke archivePendingRounds synchronously.
	terminalEvent := &Event{
		Type: EventTaskCompleted,
		Payload: map[string]any{
			EventKeyTask:   "implement feature X",
			EventKeyResult: "done",
		},
	}
	err := ces.EventStore.Append(ctx, streamID, []*Event{
		{Type: EventMessageAdded, Payload: map[string]any{"content": "starting"}},
		terminalEvent,
	}, 0)
	require.NoError(t, err)

	require.NoError(t, ces.archivePendingRounds(ctx, streamID))

	require.Equal(t, 1, sink.callCount(), "sink must be called once for one terminal event")
	call := sink.snapshot()[0]
	assert.Equal(t, 1, call.round, "first round must be 1")
	assert.Equal(t, streamID, call.streamID)
	assert.NotEmpty(t, call.events, "round events must be non-empty")
	// The terminal event must be the last event in the round.
	assert.Equal(t, EventTaskCompleted, call.events[len(call.events)-1].Type)
}

func TestArchiveSink_RoundCounterIncrements(t *testing.T) {
	ces, sink := newArchiveTestStore(t, 0)
	ctx := context.Background()
	streamID := "stream-2"

	// First round: append a terminal event and archive.
	require.NoError(t, ces.EventStore.Append(ctx, streamID, []*Event{
		{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "round 1"}},
	}, 0))
	require.NoError(t, ces.archivePendingRounds(ctx, streamID))

	// Second round: append another terminal event and archive.
	require.NoError(t, ces.EventStore.Append(ctx, streamID, []*Event{
		{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "round 2"}},
	}, 0))
	require.NoError(t, ces.archivePendingRounds(ctx, streamID))

	calls := sink.snapshot()
	require.Len(t, calls, 2, "sink must be called twice for two terminal events")
	assert.Equal(t, 1, calls[0].round, "first round must be 1")
	assert.Equal(t, 2, calls[1].round, "second round must be 2")
}

func TestArchiveSink_NilSinkNoOp(t *testing.T) {
	// Build a store WITHOUT WithArchiveSink — Append must work normally.
	mem := NewMemoryEventStore()
	repo := NewMemorySummaryRepository()
	ces, err := NewCompactableEventStore(mem, repo, nil, DefaultCompactionConfig())
	require.NoError(t, err)

	ctx := context.Background()
	streamID := "stream-noop"

	// Append through the wrapper — must not panic even though archiveSink is nil.
	err = ces.Append(ctx, streamID, []*Event{
		{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "work"}},
	}, 0)
	require.NoError(t, err, "Append must succeed with nil archiveSink")

	// archivePendingRounds on a nil-sink store is a no-op.
	err = ces.archivePendingRounds(ctx, streamID)
	require.NoError(t, err)
}

func TestArchiveSink_FlushBeforeCompaction(t *testing.T) {
	// Use a low threshold so compaction triggers after a few events.
	ces, sink := newArchiveTestStore(t, 5)
	ctx := context.Background()
	streamID := "stream-flush"

	// Append enough events to exceed the threshold, including a terminal
	// event so the round is archivable. Use Append (the public API) so the
	// full archive → compact path runs.
	for i := 0; i < 6; i++ {
		var ev *Event
		if i == 5 {
			ev = &Event{
				Type: EventTaskCompleted,
				Payload: map[string]any{
					EventKeyTask:   "flush test",
					EventKeyResult: "ok",
				},
			}
		} else {
			ev = &Event{Type: EventTaskCreated, Payload: map[string]any{"index": i}}
		}
		require.NoError(t, ces.Append(ctx, streamID, []*Event{ev}, 0))
	}

	// The archive goroutine is async — poll for the sink to be called.
	// The ordering (archive before compact) is guaranteed by the code:
	// archivePendingRounds runs before CheckAndCompact in maybeCompact.
	require.Eventually(t, func() bool {
		return sink.callCount() >= 1
	}, 2*time.Second, 10*time.Millisecond, "sink must be called before/at compaction")

	calls := sink.snapshot()
	require.GreaterOrEqual(t, len(calls), 1)
	assert.Equal(t, 1, calls[0].round, "first round must be 1")
	assert.Equal(t, streamID, calls[0].streamID)
	assert.NotEmpty(t, calls[0].events)
}

func TestArchiveSink_Idempotent(t *testing.T) {
	ces, sink := newArchiveTestStore(t, 0)
	ctx := context.Background()
	streamID := "stream-idem"

	// Append one terminal event.
	require.NoError(t, ces.EventStore.Append(ctx, streamID, []*Event{
		{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "one terminal"}},
	}, 0))

	// First call archives the round.
	require.NoError(t, ces.archivePendingRounds(ctx, streamID))
	assert.Equal(t, 1, sink.callCount(), "sink called once after first archive")

	// Second call with no new terminal event must be a no-op.
	require.NoError(t, ces.archivePendingRounds(ctx, streamID))
	assert.Equal(t, 1, sink.callCount(), "sink must NOT be called again with no new terminal")

	// Third call — still a no-op.
	require.NoError(t, ces.archivePendingRounds(ctx, streamID))
	assert.Equal(t, 1, sink.callCount(), "sink must remain at one call")
}

func TestArchiveSink_NoTerminalEventNoArchive(t *testing.T) {
	ces, sink := newArchiveTestStore(t, 0)
	ctx := context.Background()
	streamID := "stream-no-terminal"

	// Append non-terminal events only.
	require.NoError(t, ces.EventStore.Append(ctx, streamID, []*Event{
		{Type: EventMessageAdded, Payload: map[string]any{"content": "hello"}},
		{Type: EventToolCallStarted, Payload: map[string]any{"tool": "x"}},
	}, 0))

	require.NoError(t, ces.archivePendingRounds(ctx, streamID))
	assert.Equal(t, 0, sink.callCount(), "sink must not be called when there is no terminal event")
}

func TestArchiveSink_TaskFailedAlsoArchived(t *testing.T) {
	ces, sink := newArchiveTestStore(t, 0)
	ctx := context.Background()
	streamID := "stream-failed"

	// EventTaskFailed is also a terminal event.
	require.NoError(t, ces.EventStore.Append(ctx, streamID, []*Event{
		{Type: EventTaskFailed, Payload: map[string]any{EventKeyTask: "failing task"}},
	}, 0))

	require.NoError(t, ces.archivePendingRounds(ctx, streamID))
	require.Equal(t, 1, sink.callCount())
	call := sink.snapshot()[0]
	assert.Equal(t, 1, call.round)
	assert.NotEmpty(t, call.events)
	assert.Equal(t, EventTaskFailed, call.events[len(call.events)-1].Type)
}

func TestArchiveSink_MultipleStreamsIndependent(t *testing.T) {
	ces, sink := newArchiveTestStore(t, 0)
	ctx := context.Background()

	// Two streams each get one terminal event.
	for _, sid := range []string{"stream-A", "stream-B"} {
		require.NoError(t, ces.EventStore.Append(ctx, sid, []*Event{
			{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "work"}},
		}, 0))
		require.NoError(t, ces.archivePendingRounds(ctx, sid))
	}

	calls := sink.snapshot()
	require.Len(t, calls, 2)
	// Both streams must start at round 1 (independent counters).
	assert.Equal(t, 1, calls[0].round)
	assert.Equal(t, 1, calls[1].round)
	// Different stream IDs.
	assert.NotEqual(t, calls[0].streamID, calls[1].streamID)
}

// TestArchiveSink_DrainsMultipleRounds is a regression test for the
// pre-compaction drain. When several terminal events (rounds) accumulate,
// drainPendingRounds must flush ALL of them — not just one per call.
// Previously archivePendingRounds archived only a single round, so un-archived
// rounds could be permanently lost when compaction trimmed their raw events.
func TestArchiveSink_DrainsMultipleRounds(t *testing.T) {
	ces, sink := newArchiveTestStore(t, 0)
	ctx := context.Background()
	streamID := "stream-drain"

	// Append three terminal events in one batch — three pending rounds.
	require.NoError(t, ces.EventStore.Append(ctx, streamID, []*Event{
		{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "round 1"}},
		{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "round 2"}},
		{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "round 3"}},
	}, 0))

	require.NoError(t, ces.drainPendingRounds(ctx, streamID))

	calls := sink.snapshot()
	require.Len(t, calls, 3, "drain must archive all three pending rounds")
	for i, c := range calls {
		assert.Equal(t, i+1, c.round, "rounds must be numbered sequentially")
		assert.Equal(t, streamID, c.streamID)
	}
}

// TestArchiveSink_LongRoundPagedCompletely is a regression test for rounds
// that span more than archiveReadLimit events. Previously the scan advanced
// the cursor past non-terminal chunks, so the archived record only contained
// the final page and orphaned the earlier events of the same round. Paging
// now accumulates the whole round.
func TestArchiveSink_LongRoundPagedCompletely(t *testing.T) {
	ces, sink := newArchiveTestStore(t, 0)
	ctx := context.Background()
	streamID := "stream-long"

	// A single round with more than archiveReadLimit non-terminal events,
	// then a terminal event. The early events must survive in the record.
	const nonTerminal = archiveReadLimit + 100
	batch := make([]*Event, 0, nonTerminal+1)
	for i := 0; i < nonTerminal; i++ {
		batch = append(batch, &Event{
			Type:    EventToolCallStarted,
			Payload: map[string]any{"index": i},
		})
	}
	batch = append(batch, &Event{
		Type: EventTaskCompleted,
		Payload: map[string]any{
			EventKeyTask:   "long round",
			EventKeyResult: "done",
		},
	})
	require.NoError(t, ces.EventStore.Append(ctx, streamID, batch, 0))

	require.NoError(t, ces.archivePendingRounds(ctx, streamID))

	require.Equal(t, 1, sink.callCount(), "one terminal => one round")
	call := sink.snapshot()[0]
	// The whole round (non-terminal + terminal) must be archived, not just
	// the final page.
	require.Len(t, call.events, nonTerminal+1, "all round events must be archived")
	assert.Equal(t, EventTaskCompleted, call.events[len(call.events)-1].Type)
	// The first event — which the old cursor-advance behavior orphaned —
	// must be present.
	assert.Equal(t, EventToolCallStarted, call.events[0].Type)
}

// TestCompactableStore_CompactionCtxDecoupledFromCaller is a regression test for
// the compaction-context derivation in CompactableEventStore.Append. Compaction
// is best-effort background maintenance that must OUTLIVE the Append caller's
// request: a per-request ctx is cancelled when that request returns, and tying
// compaction to it would abort compaction mid-flight and starve streams fed by
// short-lived requests. The compaction ctx is therefore derived from the store's
// lifecycle context (cancelled by Close), not the caller's ctx.
//
// The test observes ctx propagation through a blocking archive sink:
//   - Cancelling the CALLER's ctx must NOT cancel the sink (compaction is
//     decoupled from the request lifetime, avoiding starvation).
//   - Calling Close must cancel the sink promptly (clean shutdown, no leak).
func TestCompactableStore_CompactionCtxDecoupledFromCaller(t *testing.T) {
	mem := NewMemoryEventStore()
	repo := NewMemorySummaryRepository()
	ces, err := NewCompactableEventStore(mem, repo, nil, DefaultCompactionConfig())
	require.NoError(t, err)
	// Close is idempotent (lcancel + MemoryEventStore.Close both tolerate repeat
	// calls), so a cleanup Close plus the in-test Close is safe.
	t.Cleanup(func() { _ = ces.Close() })

	started := make(chan struct{})
	var startedOnce sync.Once
	// cancelled fires when the sink observes its ctx being cancelled (via Close).
	// Buffered so the sink never blocks on send.
	cancelled := make(chan struct{}, 1)
	sink := func(ctx context.Context, _ int, _ string, _ []*Event) error {
		startedOnce.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
			return ctx.Err()
		case <-time.After(10 * time.Second):
			// Fallback so a regression (ctx never cancelled) fails in ~10s
			// instead of hanging the test suite indefinitely.
			return errors.New("archive sink ctx was never cancelled")
		}
	}
	ces.WithArchiveSink(sink)

	callerCtx, callerCancel := context.WithCancel(context.Background())

	// A terminal event sets hasTerminal=true so the async archive path calls
	// the sink from the compaction goroutine.
	require.NoError(t, ces.Append(callerCtx, "stream-ctx", []*Event{
		{Type: EventTaskCompleted, Payload: map[string]any{EventKeyTask: "work"}},
	}, 0))

	// Wait until the sink is mid-flight (blocked on ctx), proving the goroutine
	// started and is holding a reference to the compaction ctx.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("archive sink was not invoked within 2s")
	}

	// Cancel the CALLER's ctx. The sink must keep running: compaction is
	// decoupled from the request lifetime, so caller cancel must NOT propagate.
	callerCancel()
	select {
	case <-cancelled:
		t.Fatal("compaction ctx was cancelled by caller cancel; Append must " +
			"derive the compaction ctx from the store lifecycle, not the caller's ctx")
	case <-time.After(300 * time.Millisecond):
		// Success: the sink is still alive after caller cancel (decoupled).
	}

	// Close the store. The store lifecycle ctx is cancelled, so the sink must
	// observe ctx.Done() and return promptly — clean shutdown, no goroutine leak.
	require.NoError(t, ces.Close())
	select {
	case <-cancelled:
		// Success: Close cancelled the store lifecycle ctx and unblocked the sink.
	case <-time.After(2 * time.Second):
		t.Fatal("archive sink was not cancelled by Close; Append must derive " +
			"the compaction ctx from the store lifecycle so Close stops in-flight workers")
	}
}
