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

// TestComplete_DependentIsNotReleasedWhileTheAppendIsInFlight is the dependency half of the visibility
// gate.
//
// Complete marks the task COMPLETED in memory and only then appends; a drain running in that window
// saw a completed predecessor and released its dependent. If the append was then refused, the
// completion reverted and the dependent was already running on a result the store never had. The
// window is short, which is exactly why it survived: nothing observes it unless a store is slow.
func TestComplete_DependentIsNotReleasedWhileTheAppendIsInFlight(t *testing.T) {
	f := NewFabric()

	require.NoError(t, f.Create(newTask("gate-pred")))
	dependent := newTask("gate-dep")
	dependent.Dependencies = []string{"gate-pred"}
	require.NoError(t, f.Create(dependent))

	epoch, err := f.Acquire("gate-pred", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("gate-pred", "agent-a", epoch))

	// From here the completion's append blocks until released.
	store := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
	f = f.WithEventStore(store)

	done := make(chan error, 1)
	go func() { done <- f.Complete("gate-pred", "agent-a", epoch) }()
	<-store.entered

	// In the window: memory says COMPLETED, the store has not answered.
	ready, readyErr := f.IsReady("gate-dep")
	require.NoError(t, readyErr)
	assert.False(t, ready, "a dependent must not unlock on a completion the store has not acknowledged")
	assert.NotContains(t, f.ReadyTasks(), "gate-dep")

	close(store.release)
	require.NoError(t, <-done)

	ready, readyErr = f.IsReady("gate-dep")
	require.NoError(t, readyErr)
	assert.True(t, ready, "once acknowledged, the dependent is released as before")
	assert.Contains(t, f.ReadyTasks(), "gate-dep")
}

// TestComplete_TaskIsNotSchedulableWhileTheAppendIsInFlight is the same window from the task's own
// side: the completed task must not be handed to an executor before its completion is durable.
func TestComplete_TaskIsNotSchedulableWhileTheAppendIsInFlight(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(newTask("gate-2")))
	epoch, err := f.Acquire("gate-2", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("gate-2", "agent-a", epoch))

	store := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
	f = f.WithEventStore(store)

	done := make(chan error, 1)
	go func() { done <- f.Complete("gate-2", "agent-a", epoch) }()
	<-store.entered

	task, taskErr := f.Task("gate-2")
	require.NoError(t, taskErr)
	assert.True(t, task.CommitPending, "the in-flight transition must be marked")
	assert.Equal(t, StateCompleted, task.State)
	// The predicate, not the queue: COMPLETED is absent from ResumableTasks for an unrelated reason,
	// so asserting on the queue would pass even with the gate removed. schedulable() is the single
	// gate every entry point shares, and it is what must say no while the append is unsettled.
	assert.False(t, task.schedulable(), "an unsettled transition must not be handed to an executor")
	// The same predicate the waiters use (agentruntime.await/session): a completion in flight is not a
	// result anyone may consume.
	assert.False(t, task.CompletionSettled(), "a waiter must not be handed an unacknowledged completion")
	assert.NotContains(t, f.ResumableTasks(), "gate-2")

	close(store.release)
	require.NoError(t, <-done)

	task, taskErr = f.Task("gate-2")
	require.NoError(t, taskErr)
	assert.False(t, task.CommitPending, "and the mark must be cleared once settled")
	assert.True(t, task.CompletionSettled(), "after acknowledgement the result is consumable")
}

// TestFail_RetryIsNotAcquirableWhileTheAppendIsInFlight is Fail's half of the same window: the retry
// transition (RUNNING → READY) happens before the failure is recorded, so a drain could hand the task
// out again while the record of why it came back was still in flight.
func TestFail_RetryIsNotAcquirableWhileTheAppendIsInFlight(t *testing.T) {
	f := NewFabric()
	fabric := f
	require.NoError(t, fabric.Create(&Task{ID: "fail-gate", Capability: "code",
		RetryPolicy: RetryPolicy{MaxRetries: 3}}))
	epoch, err := fabric.Acquire("fail-gate", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, fabric.Start("fail-gate", "agent-a", epoch))

	store := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
	fabric = fabric.WithEventStore(store)

	done := make(chan error, 1)
	go func() { done <- fabric.Fail("fail-gate", "agent-a", epoch, errors.New("tool exploded")) }()
	<-store.entered

	// Released on a timer, not after the assertion: without the gate Acquire does not return
	// immediately — it parks on the durable-append barrier — and a hang reports nothing, while a
	// bounded release turns that case into the plain assertion failure it is.
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(store.release)
	}()

	if _, acquireErr := fabric.Acquire("fail-gate", "agent-b", time.Minute); acquireErr == nil {
		t.Fatal("the task was acquirable while its failure was still being recorded")
	}

	require.NoError(t, <-done)

	task, taskErr := fabric.Task("fail-gate")
	require.NoError(t, taskErr)
	assert.False(t, task.CommitPending)
	assert.Equal(t, StateReady, task.State)
}

// TestFail_UndurableFailureStaysUnconfirmed is the persistence half: a failure whose record was refused
// leaves the task in memory (the cascade already ran) but must keep it out of every scheduling entry,
// including after further calls — the marker is a state, not a one-shot flag.
func TestFail_UndurableFailureStaysUnconfirmed(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskFailed), err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{ID: "fail-unknown", Capability: "code",
		RetryPolicy: RetryPolicy{MaxRetries: 3}}))
	epoch, err := f.Acquire("fail-unknown", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("fail-unknown", "agent-a", epoch))

	require.Error(t, f.Fail("fail-unknown", "agent-a", epoch, errors.New("tool exploded")))

	task, taskErr := f.Task("fail-unknown")
	require.NoError(t, taskErr)
	assert.True(t, task.CommitUnconfirmed, "an unrecorded failure must be marked")

	// Later calls must not quietly reopen it: no drain, no acquire, no ready-queue entry.
	assert.NotContains(t, f.ReadyTasks(), "fail-unknown")
	assert.NotContains(t, f.ResumableTasks(), "fail-unknown")
	_, acquireErr := f.Acquire("fail-unknown", "agent-b", time.Minute)
	assert.Error(t, acquireErr)

	// And it stays that way after the fabric is used for something else.
	require.NoError(t, f.Create(&Task{ID: "other", Capability: "code"}))
	_, acquireErr = f.Acquire("fail-unknown", "agent-b", time.Minute)
	assert.Error(t, acquireErr, "the marker survives unrelated traffic")
}

// TestYield_SuspendedTaskIsNotResumableWhileTheAppendIsInFlight: SUSPENDED is a resumable state, so a
// yield whose checkpoint has not been recorded could be picked up and continued from the previous
// checkpoint — the work of the quantum that just yielded would run twice.
func TestYield_SuspendedTaskIsNotResumableWhileTheAppendIsInFlight(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "yield-gate", Capability: "code"}))
	epoch, err := f.Acquire("yield-gate", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("yield-gate", "agent-a", epoch))

	store := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
	f = f.WithEventStore(store)

	done := make(chan error, 1)
	go func() { done <- f.Yield("yield-gate", "agent-a", epoch, map[string]any{"step": 2}) }()
	<-store.entered

	task, taskErr := f.Task("yield-gate")
	require.NoError(t, taskErr)
	assert.Equal(t, StateSuspended, task.State)
	assert.True(t, task.CommitPending)
	assert.False(t, task.schedulable(), "an unacknowledged yield must not be resumable")
	assert.NotContains(t, f.ResumableTasks(), "yield-gate")

	close(store.release)
	require.NoError(t, <-done)

	task, taskErr = f.Task("yield-gate")
	require.NoError(t, taskErr)
	assert.False(t, task.CommitPending, "once acknowledged the task resumes normally")
}

// TestYield_UnknownKeepsTheTaskIsolated: an undetermined yield may already be durable, so resuming
// from the OLD checkpoint would repeat a quantum the store recorded as complete. The task waits for
// reconciliation instead of being handed out.
func TestYield_UnknownKeepsTheTaskIsolated(t *testing.T) {
	// task.checkpointed is the AUTHORITATIVE event of a yield (it carries the resume point);
	// task.yielded is observability. Failing the authoritative one is what can strand the task.
	store := &selectiveStubStore{failOn: string(EventTaskCheckpointed), err: context.DeadlineExceeded}
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{ID: "yield-unknown", Capability: "code"}))
	epoch, err := f.Acquire("yield-unknown", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("yield-unknown", "agent-a", epoch))

	err = f.Yield("yield-unknown", "agent-a", epoch, map[string]any{"step": 2})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCommitUnknown))

	task, taskErr := f.Task("yield-unknown")
	require.NoError(t, taskErr)
	assert.True(t, task.CommitUnconfirmed)
	assert.NotContains(t, f.ResumableTasks(), "yield-unknown")
	_, acquireErr := f.Acquire("yield-unknown", "agent-b", time.Minute)
	assert.Error(t, acquireErr)
}

// TestYield_RefusedReturnsTheTaskToReady: a refusal is a known outcome — nothing was written — so the
// task must not stay suspended on progress that does not exist. READY is the state it can legally
// reach, and it means the same thing here: unowned, to be re-acquired. Marking it
// unconfirmed would strand it on a transient rejection.
func TestYield_RefusedReturnsTheTaskToRunning(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskCheckpointed), err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(&Task{ID: "yield-refused", Capability: "code"}))
	epoch, err := f.Acquire("yield-refused", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("yield-refused", "agent-a", epoch))

	require.Error(t, f.Yield("yield-refused", "agent-a", epoch, map[string]any{"step": 2}))

	task, taskErr := f.Task("yield-refused")
	require.NoError(t, taskErr)
	assert.Equal(t, StateReady, task.State, "the unrecorded yield is undone, not left suspended")
	assert.False(t, task.CommitUnconfirmed, "a refusal is not an unknown: nothing to reconcile")
}

// TestComplete_DuplicateCallDoesNotClearTheInFlightMark is the flag-ownership bug: a second Complete
// fails the transition, writes nothing, and its empty flush reports CommitSkipped — which the handler
// read as "settled" and used to clear a mark belonging to the call still waiting on the store. The
// dependent was released with the completion unacknowledged.
func TestComplete_DuplicateCallDoesNotClearTheInFlightMark(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(newTask("dup-pred")))
	dep := newTask("dup-dep")
	dep.Dependencies = []string{"dup-pred"}
	require.NoError(t, f.Create(dep))

	epoch, err := f.Acquire("dup-pred", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("dup-pred", "agent-a", epoch))

	store := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
	f = f.WithEventStore(store)

	first := make(chan error, 1)
	go func() { first <- f.Complete("dup-pred", "agent-a", epoch) }()
	<-store.entered

	// Duplicate while the first append is in flight.
	dupErr := f.Complete("dup-pred", "agent-a", epoch)
	require.Error(t, dupErr, "the duplicate cannot complete a task that is already completed")

	task, taskErr := f.Task("dup-pred")
	require.NoError(t, taskErr)
	assert.True(t, task.CommitPending, "the duplicate must not clear the first call's in-flight mark")

	ready, readyErr := f.IsReady("dup-dep")
	require.NoError(t, readyErr)
	assert.False(t, ready, "and the dependent must still be blocked")

	close(store.release)
	require.NoError(t, <-first)

	ready, readyErr = f.IsReady("dup-dep")
	require.NoError(t, readyErr)
	assert.True(t, ready)
}

// onlyObservedFailedStore fails the observational task.ready append and accepts everything else.
type onlyObservedFailedStore struct {
	blockingStore
}

func (s *onlyObservedFailedStore) Append(ctx context.Context, stream string, events []*ares_events.Event, v int64) error {
	for _, ev := range events {
		if string(ev.Type) == string(EventTaskReady) {
			return errors.New("observability sink unavailable")
		}
	}
	return nil
}

// TestFail_ObservationalFailureDoesNotStrandTheRetry: a failing quantum emits task.failed (the
// authoritative record) and task.ready (observability). If only the latter failed, the retry IS
// recorded and the task must stay runnable — gating on the aggregate outcome stranded it forever.
func TestFail_ObservationalFailureDoesNotStrandTheRetry(t *testing.T) {
	f := NewFabric().WithEventStore(&onlyObservedFailedStore{})
	require.NoError(t, f.Create(&Task{ID: "obs-1", Capability: "code", RetryPolicy: RetryPolicy{MaxRetries: 3}}))
	epoch, err := f.Acquire("obs-1", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("obs-1", "agent-a", epoch))

	require.NoError(t, f.Fail("obs-1", "agent-a", epoch, errors.New("tool exploded")))

	task, taskErr := f.Task("obs-1")
	require.NoError(t, taskErr)
	assert.False(t, task.CommitUnconfirmed,
		"the authoritative failure committed, so the retry must not be isolated")
	assert.Equal(t, StateReady, task.State)

	epoch2, acquireErr := f.Acquire("obs-1", "agent-b", time.Minute)
	require.NoError(t, acquireErr, "the retry must be runnable")
	require.NotZero(t, epoch2)
}

// TestReconcile_AppliesTheConfirmedCompletion: removing the ledger entry without applying the
// transition left a task the store says is COMPLETED sitting in RUNNING forever, with its dependents
// blocked.
func TestReconcile_AppliesTheConfirmedCompletion(t *testing.T) {
	unknown := &selectiveStubStore{failOn: string(EventTaskCompleted), err: context.DeadlineExceeded}
	f := NewFabric()
	require.NoError(t, f.Create(newTask("rec-complete")))
	dep := newTask("rec-complete-dep")
	dep.Dependencies = []string{"rec-complete"}
	require.NoError(t, f.Create(dep))

	epoch, err := f.Acquire("rec-complete", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("rec-complete", "agent-a", epoch))
	f = f.WithEventStore(unknown)

	require.Error(t, f.Complete("rec-complete", "agent-a", epoch))

	reverted, err := f.Task("rec-complete")
	require.NoError(t, err)
	require.Equal(t, StateRunning, reverted.State, "precondition: the completion was reverted")

	pending := f.PendingReconciliations()
	require.Len(t, pending, 1)

	require.NoError(t, f.Reconcile(context.Background(), pending[0].EventID, true, reverted.Generation))

	settled, err := f.Task("rec-complete")
	require.NoError(t, err)
	assert.Equal(t, StateCompleted, settled.State, "a confirmed completion must be applied, not just forgotten")
	assert.False(t, settled.CommitUnconfirmed)

	ready, err := f.IsReady("rec-complete-dep")
	require.NoError(t, err)
	assert.True(t, ready, "and the dependent must finally be released")
	assert.Empty(t, f.PendingReconciliations())
}

// TestReconcile_ComparesGenerationWithTheLiveTask: comparing the caller's generation against the
// LEDGER accepted a stale generation whenever the task had advanced since the event was recorded, and
// then acted on the new state (deleting a task that had since moved on).
func TestReconcile_ComparesGenerationWithTheLiveTask(t *testing.T) {
	unknown := &selectiveStubStore{failOn: string(EventTaskDeleted), err: context.DeadlineExceeded}
	f := NewFabric().WithEventStore(unknown)
	approval := newTask("rec-gen")
	approval.RequiresApproval = true
	require.NoError(t, f.Create(approval))
	require.Error(t, f.Delete("rec-gen"))

	rec := f.PendingReconciliations()[0]

	// Advance the task through a transition that does not need ownership: rejecting the approval gate
	// drives READY → FAILED, which bumps the generation. (Acquire is unavailable here — the task is
	// isolated by PendingDelete, which is the point.)
	require.NoError(t, f.ResolveApproval("rec-gen", ApprovalDecisionRejected, "not now", "operator"))

	task, err := f.Task("rec-gen")
	require.NoError(t, err)
	require.Greater(t, task.Generation, rec.Generation, "the test needs the task to have moved on")

	err = f.Reconcile(context.Background(), rec.EventID, true, rec.Generation)

	assert.ErrorIs(t, err, ErrReconcileGenerationMismatch,
		"the stale generation must be refused: the task has moved on")
	_, taskErr := f.Task("rec-gen")
	assert.NoError(t, taskErr, "and it must not be deleted on the strength of a stale record")
}

// TestFail_LosingTheStateRaceDoesNotClearAnotherCallsMark: a same-owner Fail that arrives while a
// Complete is blocked fails its transition, writes nothing, and its empty flush reports
// CommitSkipped. Without the ownership guard that cleared the Complete's in-flight mark.
func TestFail_LosingTheStateRaceDoesNotClearAnotherCallsMark(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(newTask("race-pred")))
	dep := newTask("race-dep")
	dep.Dependencies = []string{"race-pred"}
	require.NoError(t, f.Create(dep))

	epoch, err := f.Acquire("race-pred", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("race-pred", "agent-a", epoch))

	store := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
	f = f.WithEventStore(store)

	done := make(chan error, 1)
	go func() { done <- f.Complete("race-pred", "agent-a", epoch) }()
	<-store.entered

	// Same owner, same epoch, but the task is already COMPLETED: this call changes nothing.
	require.Error(t, f.Fail("race-pred", "agent-a", epoch, errors.New("tool exploded")))

	task, taskErr := f.Task("race-pred")
	require.NoError(t, taskErr)
	assert.True(t, task.CommitPending, "the loser must not clear the winner's mark")
	ready, readyErr := f.IsReady("race-dep")
	require.NoError(t, readyErr)
	assert.False(t, ready, "and the dependent must stay blocked until the store answers")

	close(store.release)
	require.NoError(t, <-done)
}

// TestReconcile_RestoresTheConfirmedCheckpoint: CompleteWithCheckpoint reverted the task when its
// append failed, and reconciliation restored COMPLETED without the result — consumers got a settled
// completion carrying the pre-quantum (here nil) checkpoint.
func TestReconcile_RestoresTheConfirmedCheckpoint(t *testing.T) {
	unknown := &selectiveStubStore{failOn: string(EventTaskCompleted), err: context.DeadlineExceeded}
	f := NewFabric()
	require.NoError(t, f.Create(newTask("rec-ckpt")))
	epoch, err := f.Acquire("rec-ckpt", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("rec-ckpt", "agent-a", epoch))
	f = f.WithEventStore(unknown)

	checkpoint := map[string]any{"answer": 42}
	require.Error(t, f.CompleteWithCheckpoint("rec-ckpt", "agent-a", epoch, checkpoint))

	rec := f.PendingReconciliations()[0]
	require.NoError(t, f.Reconcile(context.Background(), rec.EventID, true, rec.Generation))

	task, taskErr := f.Task("rec-ckpt")
	require.NoError(t, taskErr)
	require.Equal(t, StateCompleted, task.State)
	assert.Equal(t, checkpoint, task.Checkpoint,
		"a confirmed completion must carry the result it produced, not the one before it")
}

// TestReconcile_SettlesAYield: task.yielded and task.checkpointed were missing from the switch, so a
// confirmed yield returned success, removed its record, and left the task isolated by
// CommitUnconfirmed — no later acquisition could succeed.
func TestReconcile_SettlesAYield(t *testing.T) {
	unknown := &selectiveStubStore{failOn: string(EventTaskCheckpointed), err: context.DeadlineExceeded}
	f := NewFabric()
	require.NoError(t, f.Create(newTask("rec-yield")))
	epoch, err := f.Acquire("rec-yield", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("rec-yield", "agent-a", epoch))
	f = f.WithEventStore(unknown)

	require.Error(t, f.Yield("rec-yield", "agent-a", epoch, map[string]any{"step": 2}))

	pending := f.PendingReconciliations()
	require.Len(t, pending, 1)
	require.NoError(t, f.Reconcile(context.Background(), pending[0].EventID, true, pending[0].Generation))

	task, taskErr := f.Task("rec-yield")
	require.NoError(t, taskErr)
	assert.False(t, task.CommitUnconfirmed, "a confirmed yield must release the isolation")
	assert.True(t, task.schedulable(), "and the task must be resumable again")
}

// TestYield_ObservationalFailureKeepsTheTaskResumable: task.checkpointed is the authoritative record
// (it carries the resume point) and task.yielded is observability. If only the latter failed, the
// checkpoint is recorded and the task must not be isolated.
func TestYield_ObservationalFailureKeepsTheTaskResumable(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskYielded), err: errors.New("sink unavailable")}
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(newTask("yield-obs")))
	epoch, err := f.Acquire("yield-obs", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("yield-obs", "agent-a", epoch))

	require.NoError(t, f.Yield("yield-obs", "agent-a", epoch, map[string]any{"step": 2}))

	task, taskErr := f.Task("yield-obs")
	require.NoError(t, taskErr)
	assert.False(t, task.CommitUnconfirmed, "the resume point is durable, so nothing is unsettled")
	assert.True(t, task.schedulable())
}

// TestReconcile_DoesNotResurrectADeletedTask: an observational yield can time out while its
// checkpoint succeeds, leaving a record for a task that then completes and is deleted. Settling that
// record used to restore its snapshot — READY, as of the yield — and resurrect the task.
func TestReconcile_DoesNotResurrectADeletedTask(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskYielded), err: context.DeadlineExceeded}
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(newTask("res-1")))
	epoch, err := f.Acquire("res-1", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("res-1", "agent-a", epoch))

	// Observational failure only: the checkpoint is durable, so the yield succeeds and the task stays
	// usable, but the ledger remembers the failed task.yielded.
	require.NoError(t, f.Yield("res-1", "agent-a", epoch, map[string]any{"step": 2}))
	pending := f.PendingReconciliations()
	require.Len(t, pending, 1)
	rec := pending[0]

	f = f.WithEventStore(nil)
	epoch2, err := f.Acquire("res-1", "agent-b", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("res-1", "agent-b", epoch2))
	require.NoError(t, f.Complete("res-1", "agent-b", epoch2))
	require.NoError(t, f.Delete("res-1"))

	// The durable delete retires the record: an entry recorded before it describes a task the store
	// no longer has.
	assert.Empty(t, f.PendingReconciliations(), "a confirmed deletion must retire the task's records")
	assert.ErrorIs(t, f.Reconcile(context.Background(), rec.EventID, true, rec.Generation),
		ErrReconcileUnknownEvent, "and settling one of them is no longer possible")

	_, taskErr := f.Task("res-1")
	assert.Error(t, taskErr, "nothing may resurrect a task that was completed and deleted")
}

// TestReconcile_OneConfirmationDoesNotReleaseTheOther: a yield emits task.yielded (observational) and
// task.checkpointed (authoritative). When BOTH are unresolved, confirming only the observational one
// used to clear the isolation, letting the task be acquired while its resume point was still unknown.
func TestReconcile_OneConfirmationDoesNotReleaseTheOther(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskCheckpointed), err: context.DeadlineExceeded}
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(newTask("two-1")))
	epoch, err := f.Acquire("two-1", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("two-1", "agent-a", epoch))
	require.Error(t, f.Yield("two-1", "agent-a", epoch, map[string]any{"step": 2}))

	pending := f.PendingReconciliations()
	require.Len(t, pending, 1)
	authoritative := pending[0]
	require.Equal(t, EventTaskCheckpointed, authoritative.Type)

	// A second, observational record for the same task, left unresolved too.
	f.recordPendingReconciliation(appendResult{
		TaskID: "two-1", EventID: "obs-only", Type: EventTaskYielded,
		Generation: authoritative.Generation, Outcome: CommitUnknown,
	})

	require.NoError(t, f.Reconcile(context.Background(), "obs-only", true, authoritative.Generation))

	task, taskErr := f.Task("two-1")
	require.NoError(t, taskErr)
	assert.True(t, task.CommitUnconfirmed,
		"the checkpoint is still unresolved: confirming the observational event releases nothing")

	require.NoError(t, f.Reconcile(context.Background(), authoritative.EventID, true, authoritative.Generation))
	task, taskErr = f.Task("two-1")
	require.NoError(t, taskErr)
	assert.False(t, task.CommitUnconfirmed, "the authoritative record settles it")
}

// TestCloneTask_DoesNotShareMutableLease: the ledger snapshot shared the *Lease with the live task, so
// Renew mutated an already-published record's expiry (and raced with anything reading it).
func TestCloneTask_DoesNotShareMutableLease(t *testing.T) {
	original := newTask("clone-1")
	original.Lease = &Lease{Owner: "agent-a", ExpiresAt: time.Now(), Epoch: 1}
	original.DegradedInputs = []string{"a"}

	snapshot := cloneTask(original)
	original.Lease.ExpiresAt = original.Lease.ExpiresAt.Add(time.Hour)
	original.Lease.Epoch = 7
	original.DegradedInputs[0] = "changed"

	assert.NotSame(t, original.Lease, snapshot.Lease)
	assert.Equal(t, uint64(1), snapshot.Lease.Epoch, "the snapshot must keep the lease it recorded")
	assert.Equal(t, "a", snapshot.DegradedInputs[0])
}

// TestReconcile_RestoresANilCheckpoint: CompleteWithCheckpoint(..., nil) means "the quantum produced no
// result". The restore skipped nil (guarded on != nil), so reconciliation published the PREVIOUS
// checkpoint as this completion's output.
func TestReconcile_RestoresANilCheckpoint(t *testing.T) {
	unknown := &selectiveStubStore{failOn: string(EventTaskCompleted), err: context.DeadlineExceeded}
	f := NewFabric()
	stale := map[string]any{"answer": "previous run"}
	require.NoError(t, f.Create(&Task{ID: "nil-1", Capability: "code", Checkpoint: stale}))
	epoch, err := f.Acquire("nil-1", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("nil-1", "agent-a", epoch))
	f = f.WithEventStore(unknown)

	require.Error(t, f.CompleteWithCheckpoint("nil-1", "agent-a", epoch, nil))

	rec := f.PendingReconciliations()[0]
	require.NoError(t, f.Reconcile(context.Background(), rec.EventID, true, rec.Generation))

	task, taskErr := f.Task("nil-1")
	require.NoError(t, taskErr)
	assert.Equal(t, StateCompleted, task.State)
	assert.Nil(t, task.Checkpoint,
		"a confirmed completion that produced nothing must not publish the previous result")
}

// TestReconcile_DoesNotReleaseWhileAnAuthoritativeAppendIsInFlight: an append that is still running has
// no ledger entry (entries are written when it fails), so confirming an earlier observational record
// used to clear the gate and make the task resumable before that append was acknowledged.
func TestReconcile_DoesNotReleaseWhileAnAuthoritativeAppendIsInFlight(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "flight-1", Capability: "code"}))
	epoch, err := f.Acquire("flight-1", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("flight-1", "agent-a", epoch))

	// The authoritative append (task.checkpointed) blocks in the store. The transition already
	// happened, so the task is SUSPENDED with CommitPending set and no ledger entry — entries are
	// written when an append FAILS, and this one has not returned yet.
	store := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
	f = f.WithEventStore(store)
	yieldDone := make(chan error, 1)
	go func() { yieldDone <- f.Yield("flight-1", "agent-a", epoch, map[string]any{"step": 2}) }()
	<-store.entered

	live, liveErr := f.Task("flight-1")
	require.NoError(t, liveErr)
	require.True(t, live.CommitPending, "precondition: the append is in flight")

	// An earlier observational record at the live generation, settled while the append is running.
	f.recordPendingReconciliation(appendResult{
		TaskID: "flight-1", EventID: "old-observational", Type: EventTaskYielded,
		Generation: live.Generation, Outcome: CommitUnknown,
	})
	require.NoError(t, f.Reconcile(context.Background(), "old-observational", true, live.Generation))

	still, stillErr := f.Task("flight-1")
	require.NoError(t, stillErr)
	assert.True(t, still.CommitPending,
		"the in-flight authoritative append still owns the gate")
	assert.False(t, still.schedulable(), "so the task is not resumable yet")

	close(store.release)
	require.NoError(t, <-yieldDone)
}

// TestReconcile_ObsoleteRecordDoesNotBlockALaterRetry: a refused checkpoint leaves a record at an older
// generation. That record can never be reconciled against the current task, so it must not keep a
// later attempt isolated — doing so stranded the retry permanently.
func TestReconcile_ObsoleteRecordDoesNotBlockALaterRetry(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "obsolete-1", Capability: "code", RetryPolicy: RetryPolicy{MaxRetries: 3}}))

	// A record from an older generation: the task has moved on since.
	older, err := f.Task("obsolete-1")
	require.NoError(t, err)
	f.recordPendingReconciliation(appendResult{
		TaskID: "obsolete-1", EventID: "stale-refusal", Type: EventTaskCheckpointed,
		Generation: older.Generation - 1, Outcome: CommitNotCommitted,
	})

	// A later attempt fails durably in a way that isolates the task, then a reconciliation settles it.
	epoch, err := f.Acquire("obsolete-1", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("obsolete-1", "agent-a", epoch))

	// Model the isolation the failed authoritative append would have produced. Without this the
	// assertion below would hold for an implementation that never clears anything, i.e. it would be
	// unable to fail.
	f.mu.Lock()
	if t1, ok := f.tasks["obsolete-1"]; ok {
		t1.CommitUnconfirmed = true
	}
	f.mu.Unlock()
	f.recordPendingReconciliation(appendResult{
		TaskID: "obsolete-1", EventID: "current-failure", Type: EventTaskFailed,
		Generation: mustTaskGeneration(t, f, "obsolete-1"), Outcome: CommitUnknown,
	})

	require.NoError(t, f.Reconcile(context.Background(), "current-failure", true,
		mustTaskGeneration(t, f, "obsolete-1")))

	live, liveErr := f.Task("obsolete-1")
	require.NoError(t, liveErr)
	assert.False(t, live.CommitUnconfirmed,
		"an unreconcilable older record must not hold the current generation isolated")
	assert.True(t, live.schedulable())
}
