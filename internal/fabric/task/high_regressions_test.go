package taskfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// The DEEP_CODE_REVIEW_2026 HIGH regressions for the task fabric: the
// durable-append ordering barrier recovering after a timeout skip (#5) and
// the reaper's dangling-dependency guard (#7).

// TestFlushOrderTimeoutSkipDoesNotPoisonLaterEvents pins #5: when a flush
// gives up waiting for an earlier sequence (orderTimedOut), the skipped seq
// must still advance flushedSeq — "skipped" means "processed" for the
// barrier. The regression: the skipped seq was never completed by anyone,
// so EVERY later event re-waited the full flushOrderWaitTimeout only to skip
// as well — one stall permanently degraded every subsequent fabric mutation
// to one 30s timeout each.
func TestFlushOrderTimeoutSkipDoesNotPoisonLaterEvents(t *testing.T) {
	// Shrink the order-wait bound so the skip path is exercisable in test
	// time (white-box override; restored on cleanup).
	origWait := flushOrderWaitTimeout
	flushOrderWaitTimeout = 300 * time.Millisecond
	t.Cleanup(func() { flushOrderWaitTimeout = origWait })

	store := newRecordingEventStore()
	f := NewFabric()
	f.store = store

	// seq 1 is recorded but its flusher never runs (the owning fabric method
	// is still in flight — simulated by simply never flushing it).
	// seq 2 therefore cannot satisfy its causal barrier and must time out
	// and SKIP after the (shrunk) bound.
	p2 := &pendingAppend{
		store:  store,
		typ:    EventTaskStarted,
		taskID: "t-skip",
		event:  &ares_events.Event{Type: ares_events.EventTaskStarted, StreamID: "t-skip", ModuleName: "taskfabric"},
		seq:    2,
	}
	start := time.Now()
	f.flushAppends(&[]*pendingAppend{p2})
	skipDur := time.Since(start)
	require.GreaterOrEqual(t, skipDur, 250*time.Millisecond, "the skip must respect the order-wait bound")
	require.Less(t, skipDur, 5*time.Second, "the skip must be bounded by flushOrderWaitTimeout, not flushAppendTimeout or worse")

	// The skip advanced the barrier past the ghost seq.
	f.flushCond.L.Lock()
	flushed := f.flushedSeq
	f.flushCond.L.Unlock()
	require.Equal(t, uint64(2), flushed, "a timed-out skip must advance flushedSeq to the skipped seq")

	// seq 3 must NOT wait for the never-completing seq 2 again: it flushes
	// promptly and lands in the store.
	p3 := &pendingAppend{
		store:  store,
		typ:    EventTaskCompleted,
		taskID: "t-next",
		event:  &ares_events.Event{Type: ares_events.EventTaskCompleted, StreamID: "t-next", ModuleName: "taskfabric"},
		seq:    3,
	}
	start = time.Now()
	f.flushAppends(&[]*pendingAppend{p3})
	nextDur := time.Since(start)
	require.Less(t, nextDur, 150*time.Millisecond,
		"the event after a skip must not re-wait the full order timeout (poisoned barrier); took %s", nextDur)

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.order, 1, "exactly one event (seq 3) must reach the store; seq 2 was skipped")
	require.Equal(t, ares_events.EventTaskCompleted, store.order[0])
}

// TestFlushOrderStillEnforcedWithoutTimeout pins the unchanged half of #5:
// with the predecessor landing in time, the barrier still holds the later
// event back until the earlier one is appended (the skip-advance must not
// turn into an unconditional bypass).
func TestFlushOrderStillEnforcedWithoutTimeout(t *testing.T) {
	store := newRecordingEventStore()
	f := NewFabric()
	f.store = store

	p1 := &pendingAppend{
		store:  store,
		typ:    EventTaskStarted,
		taskID: "t1",
		event:  &ares_events.Event{Type: ares_events.EventTaskStarted, StreamID: "t1", ModuleName: "taskfabric"},
		seq:    1,
	}
	p2 := &pendingAppend{
		store:  store,
		typ:    EventTaskCompleted,
		taskID: "t1",
		event:  &ares_events.Event{Type: ares_events.EventTaskCompleted, StreamID: "t1", ModuleName: "taskfabric"},
		seq:    2,
	}

	// Launch the LATER event's flush first; the barrier must still order
	// started before completed in the store.
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.flushAppends(&[]*pendingAppend{p2})
	}()
	// Give p2's flusher a head start into its wait, then flush p1.
	time.Sleep(20 * time.Millisecond)
	f.flushAppends(&[]*pendingAppend{p1})
	<-done

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.order, 2)
	require.Equal(t, ares_events.EventTaskStarted, store.order[0])
	require.Equal(t, ares_events.EventTaskCompleted, store.order[1])
}

// --- #7: reaper dangling-dependency guard ---

// completeTask drives a task through acquire → start → complete so the reaper
// tests have terminal tasks to harvest.
func completeTask(t *testing.T, f *Fabric, id, agent string) {
	t.Helper()
	epoch, err := f.Acquire(id, agent, time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start(id, agent, epoch))
	require.NoError(t, f.Complete(id, agent, epoch))
}

// TestReaperSkipsReferencedCompletedTasks pins #7: a COMPLETED task that
// another task still lists in Dependencies must NOT be harvested —
// depsCompletedLocked treats a missing dependency as unsatisfied forever, so
// deleting the predecessor would strand the dependent permanently. The
// harvest converges over sweeps: once the dependent itself is terminal and
// harvested, a later sweep reclaims the predecessor.
func TestReaperSkipsReferencedCompletedTasks(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "sess/s1/x", Capability: "rust"}))
	require.NoError(t, f.Create(&Task{
		ID:           "sess/s1/y",
		Capability:   "rust",
		Dependencies: []string{"sess/s1/x"},
	}))
	completeTask(t, f, "sess/s1/x", "agent-a")

	reaper := NewReaper(f, "sess/s1/", time.Nanosecond) // grace elapsed

	// Sweep 1: x is COMPLETED and referenced by y (still READY) — x must
	// survive; y is not terminal so nothing is harvested.
	if n := reaper.Sweep(); n != 0 {
		t.Fatalf("sweep 1 must harvest nothing (y is live, x is referenced), got %d", n)
	}
	if _, err := f.Task("sess/s1/x"); err != nil {
		t.Fatalf("referenced completed task must survive the sweep: %v", err)
	}

	// Complete the dependent too: sweep 2 harvests y (unreferenced), x is
	// still referenced by y's terminal record and survives one more pass.
	completeTask(t, f, "sess/s1/y", "agent-a")
	if n := reaper.Sweep(); n != 1 {
		t.Fatalf("sweep 2 must harvest exactly the unreferenced dependent y, got %d", n)
	}
	if _, err := f.Task("sess/s1/x"); err != nil {
		t.Fatalf("predecessor must survive while its dependent record exists: %v", err)
	}
	if _, err := f.Task("sess/s1/y"); err == nil {
		t.Fatal("terminal unreferenced dependent must be harvested")
	}

	// Sweep 3: with y gone, x is unreferenced and is reclaimed — convergence.
	if n := reaper.Sweep(); n != 1 {
		t.Fatalf("sweep 3 must reclaim the now-unreferenced predecessor, got %d", n)
	}
	if _, err := f.Task("sess/s1/x"); err == nil {
		t.Fatal("unreferenced completed task must be harvested on the converging sweep")
	}
}

// TestReaperStillHarvestsUnreferencedTerminalTasks pins the unchanged half
// of #7: terminal tasks with no dependents are harvested exactly as before.
func TestReaperStillHarvestsUnreferencedTerminalTasks(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "sess/s2/a", Capability: "rust"}))
	// Zero retry budget so Fail finalizes instead of requeueing.
	require.NoError(t, f.Create(&Task{ID: "sess/s2/b", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 0}}))
	completeTask(t, f, "sess/s2/a", "agent-a")
	epoch, err := f.Acquire("sess/s2/b", "agent-b", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("sess/s2/b", "agent-b", epoch))
	require.NoError(t, f.Fail("sess/s2/b", "agent-b", epoch))

	reaper := NewReaper(f, "sess/s2/", time.Nanosecond)
	if n := reaper.Sweep(); n != 2 {
		t.Fatalf("unreferenced terminal tasks must be harvested, got %d", n)
	}
}

// TestCompilePlanRollbackDoesNotPoisonFlushBarrier pins the durable-append
// barrier across a CompilePlan rollback: the batch's task.created appends
// must NOT reach the store (their tasks were rolled back — flushing would
// publish phantom events), but their sequence numbers must still be CLAIMED
// via discardAppends. Regression: the rollback returned without claiming the
// abandoned seqs, so the next fabric mutation's flushAppends waited the full
// flushOrderWaitTimeout on a gap nobody would ever complete, then took the
// timeout-skip path and lost that next event from the durable log.
func TestCompilePlanRollbackDoesNotPoisonFlushBarrier(t *testing.T) {
	// Shrink the order-wait bound so an unclaimed gap would surface as a
	// measurable stall (and a lost event) instead of a 30s hang.
	origWait := flushOrderWaitTimeout
	flushOrderWaitTimeout = 300 * time.Millisecond
	t.Cleanup(func() { flushOrderWaitTimeout = origWait })

	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)

	// Occupy one step id so the batch's second create fails with
	// ErrTaskExists and triggers the rollback path mid-batch.
	require.NoError(t, f.Create(&Task{ID: "dup", Capability: "noop"}))

	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "ok-step", Capability: "noop"},
		{ID: "dup", Capability: "noop"},
	})
	require.Error(t, err, "the duplicate step id must fail the batch")

	// The rolled-back step must NOT have a durable record.
	evs, readErr := store.Read(context.Background(), "ok-step", ares_events.ReadOptions{Direction: ares_events.ReadAscending})
	require.NoError(t, readErr)
	require.Empty(t, evs, "rollback must not publish a task.created event for a rolled-back task")

	// A subsequent mutation must flush promptly — the discarded seqs must
	// not sit in the barrier. Without the fix this Create's flush waits the
	// full (shrunk) order timeout and then loses its own event to the skip.
	start := time.Now()
	require.NoError(t, f.Create(&Task{ID: "after", Capability: "noop"}))
	dur := time.Since(start)
	require.Less(t, dur, 150*time.Millisecond,
		"the mutation after a CompilePlan rollback must not wait the order timeout; took %s", dur)

	after, readErr := store.Read(context.Background(), "after", ares_events.ReadOptions{Direction: ares_events.ReadAscending})
	require.NoError(t, readErr)
	require.Len(t, after, 1, "the post-rollback mutation's event must land in the durable log (not be timeout-skipped)")
	require.Equal(t, ares_events.EventTaskCreated, after[0].Type)
}
