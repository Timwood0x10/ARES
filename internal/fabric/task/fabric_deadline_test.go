package taskfabric

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// deadlineFixture builds a fabric with an injected clock and an event store, and
// one task whose deadline sits deadlineOffset away from that clock. The clock is
// returned by pointer so a test can move it forward the way the recovery tick
// does, instead of creating a task that is already past its deadline.
func deadlineFixture(
	t *testing.T,
	taskID string,
	deadlineOffset time.Duration,
) (*Fabric, *ares_events.MemoryEventStore, *time.Time, *Task) {
	t.Helper()
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	withClock(f, &now)

	tk := newTask(taskID)
	tk.Deadline = now.Add(deadlineOffset)
	tk.Checkpoint = NewCheckpointEnvelope(map[string]any{"input": "scan the repo"})
	require.NoError(t, f.Create(tk))
	return f, store, &now, tk
}

// advance moves the injected clock forward.
func advance(now *time.Time, d time.Duration) { *now = now.Add(d) }

// recordedTypes returns the in-memory event types recorded for one task, in
// order: "was it requeued?" is a follow-up-event question, not only a state one.
func recordedTypes(f *Fabric, taskID string) []EventType {
	var out []EventType
	for _, ev := range f.events {
		if ev.TaskID == taskID {
			out = append(out, ev.Type)
		}
	}
	return out
}

// failedRetryable returns the retryable annotation of the last task.failed event
// persisted for the task, and whether that annotation was present at all.
func failedRetryable(t *testing.T, store *ares_events.MemoryEventStore, taskID string) (bool, bool) {
	t.Helper()
	value, found := false, false
	for _, ev := range readEvents(t, store, taskID) {
		if ev.Type != ares_events.EventTaskFailed {
			continue
		}
		v, ok := ev.Payload[payloadKeyRetryable]
		if !ok {
			continue
		}
		value, found = v.(bool), true
	}
	return value, found
}

// TestExpireDeadlines_FailsReadyTaskPastDeadline is the core M1 contract: a task
// that never ran (READY, no owner, no lease) is still stopped — the case Fail
// cannot express, because its ownership check would reject it.
func TestExpireDeadlines_FailsReadyTaskPastDeadline(t *testing.T) {
	f, store, now, _ := deadlineFixture(t, "t-deadline", -time.Minute)

	expired := f.ExpireDeadlines()
	require.Equal(t, []string{"t-deadline"}, expired, "the sweep must report what it stopped")

	got, err := f.Task("t-deadline")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, got.State)
	assert.Equal(t, now.Add(-time.Minute), got.Deadline, "the deadline itself is untouched")
	assert.Zero(t, got.RetryPolicy.Attempts, "a deadline is not an attempt; the budget stays unspent")

	dc, err := DecodeCheckpoint(got.Checkpoint)
	require.NoError(t, err)
	assert.Equal(t, ErrTaskDeadlineExceeded.Error(), dc.LastError, "the reason must be readable")
	assert.Equal(t, "scan the repo", dc.Payload["input"], "the payload must survive the cause stamp")

	assert.Equal(t, []EventType{EventTaskCreated, EventTaskFailed}, recordedTypes(f, "t-deadline"),
		"terminal deadline expiry must not be followed by task.ready")

	retryable, found := failedRetryable(t, store, "t-deadline")
	require.True(t, found, "task.failed must carry the retry annotation")
	assert.False(t, retryable, "a deadline failure is terminal even with budget left")
}

// TestExpireDeadlines_LeavesUnfinishedTasksAlone pins the other side: a task
// still inside its deadline, and a task that never declared one, are untouched.
func TestExpireDeadlines_LeavesUnfinishedTasksAlone(t *testing.T) {
	t.Run("deadline in the future", func(t *testing.T) {
		f, _, _, _ := deadlineFixture(t, "t-future", time.Minute)
		assert.Empty(t, f.ExpireDeadlines())
		got, err := f.Task("t-future")
		require.NoError(t, err)
		assert.Equal(t, StateReady, got.State)
	})

	t.Run("no deadline declared", func(t *testing.T) {
		store := ares_events.NewMemoryEventStore()
		f := NewFabric().WithEventStore(store)
		now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
		withClock(f, &now)
		require.NoError(t, f.Create(newTask("t-none")))

		advance(&now, 24*time.Hour)

		assert.Empty(t, f.ExpireDeadlines(), "a zero deadline means no deadline, ever")
		got, err := f.Task("t-none")
		require.NoError(t, err)
		assert.Equal(t, StateReady, got.State)
	})
}

// TestExpireDeadlines_IgnoresTerminalTasks keeps history immutable: a completed
// task whose deadline passes later must not be rewritten into a failure.
func TestExpireDeadlines_IgnoresTerminalTasks(t *testing.T) {
	f, _, now, _ := deadlineFixture(t, "t-done", time.Minute)
	epoch, err := f.Acquire("t-done", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t-done", "agent-a", epoch))
	require.NoError(t, f.Complete("t-done", "agent-a", epoch))

	advance(now, 2*time.Minute)

	assert.Empty(t, f.ExpireDeadlines(), "a terminal task is history, not a deadline candidate")
	got, err := f.Task("t-done")
	require.NoError(t, err)
	assert.Equal(t, StateCompleted, got.State)
}

// TestExpireDeadlines_StopsRunningTaskAndFencesHolder covers the mid-quantum
// case: the sweep fails a RUNNING task, and the (still attached) holder cannot
// write to it afterwards.
func TestExpireDeadlines_StopsRunningTaskAndFencesHolder(t *testing.T) {
	f, store, now, _ := deadlineFixture(t, "t-running", time.Minute)
	epoch, err := f.Acquire("t-running", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t-running", "agent-a", epoch))

	advance(now, 2*time.Minute)
	require.Equal(t, []string{"t-running"}, f.ExpireDeadlines())

	got, err := f.Task("t-running")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, got.State)
	assert.Equal(t, "agent-a", got.Owner, "ownership stays attached as terminal provenance")
	assert.Zero(t, got.RetryPolicy.Attempts, "the deadline path must not spend the retry budget")

	assert.Error(t, f.Complete("t-running", "agent-a", epoch),
		"a late holder must not complete a task the deadline stopped")
	got, err = f.Task("t-running")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, got.State, "the rejected write must not change the outcome")

	retryable, found := failedRetryable(t, store, "t-running")
	require.True(t, found)
	assert.False(t, retryable)
}

// TestExpireDeadlines_CascadesToReadyDependents keeps failure propagation intact
// on the new terminal path: a READY dependent of an expired task can never
// become schedulable, so it fails with the provenance recorded.
func TestExpireDeadlines_CascadesToReadyDependents(t *testing.T) {
	f, _, _, _ := deadlineFixture(t, "t-upstream", -time.Minute)

	downstream := newTask("t-downstream")
	downstream.Dependencies = []string{"t-upstream"}
	require.NoError(t, f.Create(downstream))

	expired := f.ExpireDeadlines()
	require.Equal(t, []string{"t-upstream"}, expired, "only the deadline holder is reported")

	got, err := f.Task("t-downstream")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, got.State, "a cascaded dependent fails immediately")
	assert.Equal(t, "t-upstream", got.FailedDependency, "and names the predecessor that killed it")
}

// TestAcquire_RejectsTaskPastDeadline closes the scan-vs-acquire race: the sweep
// runs on its own tick, so the grant point itself must refuse a task whose
// deadline has already passed.
func TestAcquire_RejectsTaskPastDeadline(t *testing.T) {
	f, _, _, _ := deadlineFixture(t, "t-stale", -time.Minute)

	_, err := f.Acquire("t-stale", "agent-a", time.Minute)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTaskDeadlineExceeded, "the refusal must be identifiable")

	got, err := f.Task("t-stale")
	require.NoError(t, err)
	assert.Equal(t, StateReady, got.State, "a refused acquire must not move the task")
	assert.Nil(t, got.Lease, "and must not mint a lease")

	t.Run("still grants a task inside its deadline", func(t *testing.T) {
		f2, _, _, _ := deadlineFixture(t, "t-fresh", time.Minute)
		epoch, err := f2.Acquire("t-fresh", "agent-a", time.Minute)
		require.NoError(t, err)
		assert.Positive(t, epoch)
	})
}

// TestFail_PermanentCauseIsTerminalWithBudgetLeft is the M1 classification
// contract end to end: a marked-permanent cause ends the task even though the
// retry budget is untouched.
func TestFail_PermanentCauseIsTerminalWithBudgetLeft(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	tk := newTask("t-perm")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 3}
	require.NoError(t, f.Create(tk))
	epoch, err := f.Acquire("t-perm", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t-perm", "agent-a", epoch))

	permanent := MarkPermanent(errors.New("tool cannot parse this language"))
	require.NoError(t, f.Fail("t-perm", "agent-a", epoch, permanent))

	got, err := f.Task("t-perm")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, got.State, "a permanent cause is terminal despite MaxRetries=3")
	assert.Equal(t, 1, got.RetryPolicy.Attempts, "the attempt is still counted")
	assert.Equal(t, []EventType{EventTaskCreated, EventTaskAcquired, EventTaskStarted, EventTaskFailed},
		recordedTypes(f, "t-perm"), "no requeue event may follow a permanent failure")

	retryable, found := failedRetryable(t, store, "t-perm")
	require.True(t, found)
	assert.False(t, retryable)
}

// TestFail_UnclassifiedCauseStillRequeues is the 0.3.2 compatibility contract:
// with no marker and budget left, the requeue sequence is exactly as before.
func TestFail_UnclassifiedCauseStillRequeues(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	tk := newTask("t-requeue")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 3}
	require.NoError(t, f.Create(tk))
	epoch, err := f.Acquire("t-requeue", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t-requeue", "agent-a", epoch))

	require.NoError(t, f.Fail("t-requeue", "agent-a", epoch, errors.New("transient boom")))

	got, err := f.Task("t-requeue")
	require.NoError(t, err)
	assert.Equal(t, StateReady, got.State)
	assert.Equal(t, 1, got.RetryPolicy.Attempts)
	assert.Nil(t, got.Lease, "a requeued task is unowned again")
	assert.Equal(t, []EventType{EventTaskCreated, EventTaskAcquired, EventTaskStarted, EventTaskFailed, EventTaskReady},
		recordedTypes(f, "t-requeue"))

	dc, err := DecodeCheckpoint(got.Checkpoint)
	require.NoError(t, err)
	assert.Empty(t, dc.LastError, "a requeue must not stamp a stale cause")

	retryable, found := failedRetryable(t, store, "t-requeue")
	require.True(t, found)
	assert.True(t, retryable)
}

// TestFail_RetryableMarkerStillRequeues pins the explicit marker path: a cause
// that a lower layer knows is transient requeues without relying on the default.
// MaxRetries is the TOTAL attempt budget, so 2 allows exactly one requeue.
func TestFail_RetryableMarkerStillRequeues(t *testing.T) {
	f := NewFabric()
	tk := newTask("t-marked")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 2}
	require.NoError(t, f.Create(tk))
	epoch, err := f.Acquire("t-marked", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t-marked", "agent-a", epoch))

	require.NoError(t, f.Fail("t-marked", "agent-a", epoch, MarkRetryable(errors.New("upstream 503"))))

	got, err := f.Task("t-marked")
	require.NoError(t, err)
	assert.Equal(t, StateReady, got.State)
}
