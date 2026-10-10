package taskfabric

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// backoffFixture builds a fabric with an injected clock, an event store and one
// READY task carrying the given retry policy. The clock comes back by pointer so
// a test can serve the delay the way the scheduler's next drain does.
func backoffFixture(
	t *testing.T,
	taskID string,
	policy RetryPolicy,
	base, max time.Duration,
) (*Fabric, *ares_events.MemoryEventStore, *time.Time) {
	t.Helper()
	store := ares_events.NewMemoryEventStore()
	f := NewFabric().WithEventStore(store)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	withClock(f, &now)

	tk := newTask(taskID)
	tk.RetryPolicy = policy
	tk.BackoffBase = base
	tk.BackoffMax = max
	require.NoError(t, f.Create(tk))
	return f, store, &now
}

// runOneAttempt drives acquire → start → fail, the same transitions the kernel
// drives, so retry scheduling is exercised on the production path.
func runOneAttempt(t *testing.T, f *Fabric, taskID string, cause error) {
	t.Helper()
	epoch, err := f.Acquire(taskID, "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start(taskID, "agent-a", epoch))
	require.NoError(t, f.Fail(taskID, "agent-a", epoch, cause))
}

// readyPayload returns the last persisted task.ready payload for a task.
func readyPayload(t *testing.T, store *ares_events.MemoryEventStore, taskID string) map[string]any {
	t.Helper()
	var payload map[string]any
	for _, ev := range readEvents(t, store, taskID) {
		if ev.Type == ares_events.EventTaskReady {
			payload = ev.Payload
		}
	}
	require.NotNil(t, payload, "no task.ready event persisted for %s", taskID)
	return payload
}

// failedPayload returns the last persisted task.failed payload for a task.
func failedPayload(t *testing.T, store *ares_events.MemoryEventStore, taskID string) map[string]any {
	t.Helper()
	var payload map[string]any
	for _, ev := range readEvents(t, store, taskID) {
		if ev.Type == ares_events.EventTaskFailed {
			payload = ev.Payload
		}
	}
	require.NotNil(t, payload, "no task.failed event persisted for %s", taskID)
	return payload
}

// TestFail_BackoffDelaysTheRetryPastBothGates is the M2 core contract: a
// requeued task is READY but not yet runnable, and neither the query gates nor
// the public Acquire grant may hand it out before the delay is served.
func TestFail_BackoffDelaysTheRetryPastBothGates(t *testing.T) {
	f, store, now := backoffFixture(t, "t-backoff", RetryPolicy{MaxRetries: 3}, time.Second, 0)

	runOneAttempt(t, f, "t-backoff", errors.New("transient boom"))

	got, err := f.Task("t-backoff")
	require.NoError(t, err)
	require.Equal(t, StateReady, got.State, "a retryable failure still requeues")
	assert.Equal(t, now.Add(time.Second), got.NextAttemptAt, "the first retry waits one base")

	// Operators read the schedule off the event stream: the ready event must say
	// when the retry is due, not leave it looking stuck.
	assert.Equal(t, now.Add(time.Second).UTC().Format(time.RFC3339),
		readyPayload(t, store, "t-backoff")[restoreKeyNextAttemptAt])

	assert.NotContains(t, f.ResumableTasks(), "t-backoff", "the scheduler must not see it yet")
	assert.NotContains(t, f.ReadyTasks(), "t-backoff")

	_, err = f.Acquire("t-backoff", "agent-b", time.Minute)
	assert.ErrorIs(t, err, ErrTaskNotReady, "a direct acquire must not start the retry early")

	// Serving the delay is all it takes: no event drives this, the gates compare
	// against the fabric clock.
	advance(now, time.Second)

	assert.Contains(t, f.ResumableTasks(), "t-backoff")
	assert.Contains(t, f.ReadyTasks(), "t-backoff")
	epoch, err := f.Acquire("t-backoff", "agent-b", time.Minute)
	require.NoError(t, err)
	assert.Positive(t, epoch)
}

// TestFail_BackoffEscalatesPerAttempt pins the ladder end to end: the delay
// comes from the attempt count Fail just incremented.
func TestFail_BackoffEscalatesPerAttempt(t *testing.T) {
	f, _, now := backoffFixture(t, "t-escalate", RetryPolicy{MaxRetries: 4}, time.Second, 0)

	runOneAttempt(t, f, "t-escalate", errors.New("boom"))
	advance(now, time.Second)
	runOneAttempt(t, f, "t-escalate", errors.New("boom"))

	got, err := f.Task("t-escalate")
	require.NoError(t, err)
	assert.Equal(t, now.Add(2*time.Second), got.NextAttemptAt, "the second retry waits two bases")
	assert.Equal(t, 2, got.RetryPolicy.Attempts)

	advance(now, 2*time.Second)
	runOneAttempt(t, f, "t-escalate", errors.New("boom"))

	got, err = f.Task("t-escalate")
	require.NoError(t, err)
	assert.Equal(t, now.Add(4*time.Second), got.NextAttemptAt, "the third retry waits four bases")
}

// TestFail_BackoffRespectsCap pins the ceiling: escalation stops at BackoffMax
// instead of doubling forever.
func TestFail_BackoffRespectsCap(t *testing.T) {
	f, _, now := backoffFixture(t, "t-cap", RetryPolicy{MaxRetries: 5}, time.Second, 3*time.Second)

	runOneAttempt(t, f, "t-cap", errors.New("boom"))
	advance(now, time.Second)
	runOneAttempt(t, f, "t-cap", errors.New("boom"))
	advance(now, 2*time.Second)
	runOneAttempt(t, f, "t-cap", errors.New("boom"))

	got, err := f.Task("t-cap")
	require.NoError(t, err)
	assert.Equal(t, now.Add(3*time.Second), got.NextAttemptAt, "the third retry is capped at BackoffMax")
}

// TestFail_ZeroBackoffKeepsLegacyImmediateRequeue is the 0.3.2 compatibility
// contract: with no policy the requeue is immediately runnable and the durable
// payload does not grow scheduling keys (so default-path event bytes are
// unchanged).
func TestFail_ZeroBackoffKeepsLegacyImmediateRequeue(t *testing.T) {
	f, store, _ := backoffFixture(t, "t-legacy", RetryPolicy{MaxRetries: 2}, 0, 0)

	runOneAttempt(t, f, "t-legacy", errors.New("boom"))

	got, err := f.Task("t-legacy")
	require.NoError(t, err)
	assert.True(t, got.NextAttemptAt.IsZero(), "no policy means no due time")
	assert.Contains(t, f.ResumableTasks(), "t-legacy", "the legacy requeue is immediately runnable")

	payload := failedPayload(t, store, "t-legacy")
	for _, key := range []string{restoreKeyNextAttemptAt, restoreKeyBackoffBaseMS, restoreKeyBackoffMaxMS} {
		assert.NotContains(t, payload, key, "the default path must not grow payload key %q", key)
	}
}

// TestRestore_PreservesPendingRetryBackoff is the restart half of M2: a rebuilt
// fabric must keep both the due time and the policy, so a crash cannot make a
// pending backoff retry early (or lose its escalation).
func TestRestore_PreservesPendingRetryBackoff(t *testing.T) {
	f, store, now := backoffFixture(t, "t-restore", RetryPolicy{MaxRetries: 3}, time.Second, 4*time.Second)

	runOneAttempt(t, f, "t-restore", errors.New("boom"))

	before, err := f.Task("t-restore")
	require.NoError(t, err)
	require.False(t, before.NextAttemptAt.IsZero(), "the fixture must leave a pending retry")

	rebuilt := NewFabric().WithEventStore(store)
	rebuiltClock := *now
	withClock(rebuilt, &rebuiltClock)
	require.NoError(t, rebuilt.RestoreFromStore(context.Background()))

	got, err := rebuilt.Task("t-restore")
	require.NoError(t, err)
	assert.Equal(t, StateReady, got.State)
	assert.Equal(t, before.NextAttemptAt, got.NextAttemptAt, "the pending due time must survive")
	assert.Equal(t, time.Second, got.BackoffBase, "the policy must survive, or the next retry is unescalated")
	assert.Equal(t, 4*time.Second, got.BackoffMax)
	assert.Equal(t, before.RetryPolicy.Attempts, got.RetryPolicy.Attempts)

	assert.NotContains(t, rebuilt.ResumableTasks(), "t-restore", "a restart must not retry early")

	advance(&rebuiltClock, time.Second)
	assert.Contains(t, rebuilt.ResumableTasks(), "t-restore", "and it must return once the delay is served")
}

// TestRestore_PreservesBackoffPolicyFromCreation covers the other carrier: a
// task that never failed still has to keep its policy, which travels on the
// creation event.
func TestRestore_PreservesBackoffPolicyFromCreation(t *testing.T) {
	_, store, now := backoffFixture(t, "t-policy", RetryPolicy{MaxRetries: 2}, 2*time.Second, 8*time.Second)

	rebuilt := NewFabric().WithEventStore(store)
	rebuiltClock := *now
	withClock(rebuilt, &rebuiltClock)
	require.NoError(t, rebuilt.RestoreFromStore(context.Background()))

	got, err := rebuilt.Task("t-policy")
	require.NoError(t, err)
	assert.Equal(t, 2*time.Second, got.BackoffBase)
	assert.Equal(t, 8*time.Second, got.BackoffMax)
	assert.True(t, got.NextAttemptAt.IsZero(), "nothing is pending before a failure")
}
