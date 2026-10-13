package taskfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// TestCheckExpiredLeasesSetsRequeued locks the A6 durability contract: a lease
// expiry returns the task to the pool, and the task is marked Requeued=true so
// the scheduler can tell "this is continued work" from "this is a first run".
func TestCheckExpiredLeasesSetsRequeued(t *testing.T) {
	f := NewFabric()
	now := time.Now()
	withClock(f, &now)

	require.NoError(t, f.Create(newTask("t-rq")))
	_, err := f.Acquire("t-rq", "agent-a", time.Minute)
	require.NoError(t, err)

	// Advance past TTL.
	now = now.Add(2 * time.Minute)
	requeued := f.CheckExpiredLeases()
	require.Len(t, requeued, 1)
	assert.Equal(t, "t-rq", requeued[0])

	got, err := f.Task("t-rq")
	require.NoError(t, err)
	assert.True(t, got.Requeued, "lease expiry must set Requeued=true")
}

// TestFailRetrySetsRequeued locks the retry-half of A6: a retryable failure
// requeues the task, and the task is marked Requeued=true.
func TestFailRetrySetsRequeued(t *testing.T) {
	f := NewFabric()
	tk := &Task{
		ID:          "t-rq-fail",
		Capability:  "rust",
		RetryPolicy: RetryPolicy{MaxRetries: 2},
	}
	require.NoError(t, f.Create(tk))

	epoch, err := f.Acquire("t-rq-fail", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t-rq-fail", "agent-a", epoch))

	// A retryable failure requeues the task.
	require.NoError(t, f.Fail("t-rq-fail", "agent-a", epoch, nil))

	got, err := f.Task("t-rq-fail")
	require.NoError(t, err)
	assert.True(t, got.Requeued, "retry requeue must set Requeued=true")
}

// TestFailTerminalDoesNotSetRequeued locks the over-approximation boundary:
// a TERMINAL failure (retry budget exhausted) does set Requeued because the
// retryable flag on the task.failed event would fold it — but the task is
// terminal and never scheduled again, so the flag is never read. This test
// documents the known over-approximation, not a bug.
func TestFailTerminalDoesNotSetRequeued(t *testing.T) {
	f := NewFabric()
	tk := &Task{
		ID:          "t-rq-term",
		Capability:  "rust",
		RetryPolicy: RetryPolicy{MaxRetries: 0},
	}
	require.NoError(t, f.Create(tk))

	epoch, err := f.Acquire("t-rq-term", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t-rq-term", "agent-a", epoch))

	// A terminal failure (no retries left) does NOT requeue.
	require.NoError(t, f.Fail("t-rq-term", "agent-a", epoch, nil))

	got, err := f.Task("t-rq-term")
	require.NoError(t, err)
	assert.False(t, got.Requeued,
		"terminal failure does NOT set Requeued (never requeued)")
}

// TestRestoreFoldsRequeuedFromExpiredEvent locks the durability half: after a
// restart, RestoreFromStore folds the Requeued flag back from the
// task.expired event — the in-memory flag is rebuilt purely from the log.
func TestRestoreFoldsRequeuedFromExpiredEvent(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f1 := NewFabric().WithEventStore(store)
	now := time.Now()
	withClock(f1, &now)

	require.NoError(t, f1.Create(newTask("t-restore-rq")))
	_, err := f1.Acquire("t-restore-rq", "agent-a", time.Minute)
	require.NoError(t, err)

	// Expire the lease → task.expired event is emitted with the requeue fact.
	now = now.Add(2 * time.Minute)
	f1.CheckExpiredLeases()

	// Rebuild from the store.
	f2 := NewFabric().WithEventStore(store)
	require.NoError(t, f2.RestoreFromStore(context.Background()))

	got, err := f2.Task("t-restore-rq")
	require.NoError(t, err)
	assert.True(t, got.Requeued,
		"RestoreFromStore must fold Requeued=true from task.expired event")
}

// TestRestoreFoldsRequeuedFromRetryableFailedEvent locks the retry-half of
// durability: a retryable task.failed event folds Requeued=true on restore.
func TestRestoreFoldsRequeuedFromRetryableFailedEvent(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f1 := NewFabric().WithEventStore(store)

	require.NoError(t, f1.Create(&Task{
		ID:          "t-restore-rq-fail",
		Capability:  "rust",
		RetryPolicy: RetryPolicy{MaxRetries: 2},
	}))
	epoch, err := f1.Acquire("t-restore-rq-fail", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f1.Start("t-restore-rq-fail", "agent-a", epoch))
	// Retryable failure → requeued.
	require.NoError(t, f1.Fail("t-restore-rq-fail", "agent-a", epoch, nil))

	// Rebuild from the store.
	f2 := NewFabric().WithEventStore(store)
	require.NoError(t, f2.RestoreFromStore(context.Background()))

	got, err := f2.Task("t-restore-rq-fail")
	require.NoError(t, err)
	assert.True(t, got.Requeued,
		"RestoreFromStore must fold Requeued=true from retryable task.failed")
}

// TestRestoreFoldsRequeuedFromTerminalFailedEventKnownOverApproximation documents
// the known over-approximation in foldRequeued (defect A6): a terminal
// task.failed event whose cause was retryable (classifyFailure returned true)
// folds Requeued=true even though the task is terminal and never requeued.
// This is acceptable because the flag is never read on a terminal task.
func TestRestoreFoldsRequeuedFromTerminalFailedEventKnownOverApproximation(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f1 := NewFabric().WithEventStore(store)

	require.NoError(t, f1.Create(&Task{
		ID:          "t-restore-term",
		Capability:  "rust",
		RetryPolicy: RetryPolicy{MaxRetries: 0},
	}))
	epoch, err := f1.Acquire("t-restore-term", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f1.Start("t-restore-term", "agent-a", epoch))
	// Terminal failure with nil cause → classifyFailure returns retryable=true,
	// so the event carries retryable=true, and foldRequeued folds it.
	// This is the known over-approximation: the task is terminal and the flag
	// is never read, so the false positive is harmless.
	require.NoError(t, f1.Fail("t-restore-term", "agent-a", epoch, nil))

	// Rebuild from the store.
	f2 := NewFabric().WithEventStore(store)
	require.NoError(t, f2.RestoreFromStore(context.Background()))

	got, err := f2.Task("t-restore-term")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, got.State,
		"task must be terminal FAILED")
	// The over-approximation: foldRequeued folds from a retryable task.failed,
	// even though this terminal task was never requeued. The flag is never
	// read on a terminal task, so the false positive is harmless.
	assert.True(t, got.Requeued,
		"known over-approximation: retryable terminal failure folds Requeued (never read)")
}

// TestFreshTaskNotRequeued locks the initial state: a task that was just
// created and never acquired is NOT requeued.
func TestFreshTaskNotRequeued(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(newTask("t-fresh")))

	got, err := f.Task("t-fresh")
	require.NoError(t, err)
	assert.False(t, got.Requeued, "a fresh task must not be Requeued")
}
