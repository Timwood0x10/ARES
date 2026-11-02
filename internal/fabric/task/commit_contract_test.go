package taskfabric

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// commitStubStore fails every append with a caller-chosen error.
type commitStubStore struct{ err error }

func (s *commitStubStore) Append(context.Context, string, []*ares_events.Event, int64) error {
	return s.err
}
func (s *commitStubStore) Read(context.Context, string, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *commitStubStore) ReadAll(context.Context, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *commitStubStore) Subscribe(context.Context, ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	return nil, nil
}
func (s *commitStubStore) StreamVersion(context.Context, string) (int64, error) { return 0, nil }

// TestCommitOutcome_ClassifiesWhatIsKnown pins the three states the contract is built on.
//
// The important one is the deadline: a write that TIMED OUT may have been applied, so it must not be
// classified as a refusal. Re-running work on that reading is how a duplicate execution is born.
func TestCommitOutcome_ClassifiesWhatIsKnown(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want CommitOutcome
	}{
		{"acknowledged is committed", nil, CommitCommitted},
		{"a refusal is not committed", errors.New("constraint violation"), CommitNotCommitted},
		{"a deadline is unknown, not a refusal", context.DeadlineExceeded, CommitUnknown},
		{"a cancellation is unknown too", context.Canceled, CommitUnknown},
		{"a wrapped deadline stays unknown", errors.Join(errors.New("store"), context.DeadlineExceeded), CommitUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyAppendError(tc.err))
		})
	}

	// A batch reports its least reassuring member: one unknown poisons the whole batch.
	assert.Equal(t, CommitUnknown, worse(CommitCommitted, CommitUnknown))
	assert.Equal(t, CommitNotCommitted, worse(CommitCommitted, CommitNotCommitted))
	assert.Equal(t, CommitUnknown, worse(CommitNotCommitted, CommitUnknown))
	assert.Equal(t, CommitCommitted, worse(CommitSkipped, CommitCommitted))
}

// TestAuditCreateMustNotAcknowledgeFailedPersistence is the RT-01 counterexample: a store that
// refuses the task.created append must not result in a successful Create.
func TestAuditCreateMustNotAcknowledgeFailedPersistence(t *testing.T) {
	f := NewFabric().WithEventStore(&commitStubStore{err: errors.New("disk full")})

	err := f.Create(newTask("rt01-create"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not persisted")
	assert.False(t, errors.Is(err, ErrCommitUnknown), "a refusal is a known outcome, not an unknown one")

	_, taskErr := f.Task("rt01-create")
	assert.Error(t, taskErr, "an unpersisted task must not be executable")
}

// TestAuditCreateUnknownMustNotReportSuccessAndMustNotRetry is the second half of the same contract:
// when the append's fate is undetermined, the caller gets an identifiable unknown — not success, and
// not a silent retry with a freshly generated event, which would produce two task.created records for
// one task.
func TestAuditCreateUnknownMustNotReportSuccessAndMustNotRetry(t *testing.T) {
	store := &commitStubStore{err: context.DeadlineExceeded}
	f := NewFabric().WithEventStore(store)

	err := f.Create(newTask("rt01-unknown"))

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCommitUnknown), "the caller must be able to tell 'unknown' from 'refused'")
	assert.Contains(t, err.Error(), "reconcile by event id",
		"the message must say how to settle it, because the answer is not 'try again'")

	_, taskErr := f.Task("rt01-unknown")
	assert.Error(t, taskErr, "an undetermined create must not leave an executable task behind")
	assert.True(t, strings.Contains(err.Error(), "rt01-unknown"))
}

// selectiveStubStore fails only appends whose event type matches, so a test can let the creates
// through and refuse exactly the completion.
type selectiveStubStore struct {
	failOn string
	err    error
	// failTaskID narrows it further, so a batch test can fail exactly one member.
	failTaskID string
}

func (s *selectiveStubStore) Append(_ context.Context, _ string, events []*ares_events.Event, _ int64) error {
	for _, ev := range events {
		if string(ev.Type) != s.failOn {
			continue
		}
		if s.failTaskID != "" && ev.StreamID != s.failTaskID {
			continue
		}
		return s.err
	}
	return nil
}
func (s *selectiveStubStore) Read(context.Context, string, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *selectiveStubStore) ReadAll(context.Context, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *selectiveStubStore) Subscribe(context.Context, ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	return nil, nil
}
func (s *selectiveStubStore) StreamVersion(context.Context, string) (int64, error) { return 0, nil }

// TestAuditFailedCompleteMustNotUnlockDependent is the RT-01 counterexample for completion: when the
// task.completed append is refused, the task must NOT stay COMPLETED, because that is what makes its
// dependents eligible. A dependent running on the strength of a completion no store saw is work whose
// predecessor will be replayed after a restart.
func TestAuditFailedCompleteMustNotUnlockDependent(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskCompleted), err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)

	predecessor := newTask("rt01-pred")
	require.NoError(t, f.Create(predecessor))
	dependent := newTask("rt01-dep")
	dependent.Dependencies = []string{"rt01-pred"}
	require.NoError(t, f.Create(dependent))

	epoch, err := f.Acquire("rt01-pred", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("rt01-pred", "agent-a", epoch))

	err = f.Complete("rt01-pred", "agent-a", epoch)

	require.Error(t, err, "a completion the store refused must not be reported as success")
	assert.Contains(t, err.Error(), "not persisted")

	pred, err := f.Task("rt01-pred")
	require.NoError(t, err)
	assert.NotEqual(t, StateCompleted, pred.State,
		"the task must not remain COMPLETED: that state is what releases its dependents")

	// Readiness — not Acquire — is where a dependency gate is evaluated: Acquire only checks the
	// task's own state, so the dependent must still be missing from ReadyTasks.
	assert.NotContains(t, f.ReadyTasks(), "rt01-dep",
		"the dependent must not become ready on an unpersisted completion")

	// And the same holds through the explicit gate the scheduler consults.
	ready, gateErr := f.IsReady("rt01-dep")
	require.NoError(t, gateErr)
	assert.False(t, ready, "the dependency gate must still report not-ready")
}

// TestFail_ReportsAnUndurableFailure: Fail advanced the state (READY for a retry, FAILED for a
// terminal cause) and returned nil while the append's outcome was ignored, so a caller was told the
// failure was recorded when it was not.
//
// The state is deliberately NOT rolled back here — see the comment on Fail: the cascade has already
// run, and reverting only the task would leave dependents failed for a predecessor that is running
// again. What must change is the report.
func TestFail_ReportsAnUndurableFailure(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskFailed), err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)

	require.NoError(t, f.Create(newTask("rt01-fail")))
	epoch, err := f.Acquire("rt01-fail", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("rt01-fail", "agent-a", epoch))

	err = f.Fail("rt01-fail", "agent-a", epoch, errors.New("tool exploded"))

	require.Error(t, err, "a failure the store refused must not be reported as recorded")
	assert.Contains(t, err.Error(), "not persisted")
}

// TestDelete_KeepsTheTaskWhenTheTombstoneIsNotDurable: removing the task locally while the store has
// no tombstone means a restart resurrects something this process reported as gone.
func TestDelete_KeepsTheTaskWhenTheTombstoneIsNotDurable(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskDeleted), err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)

	require.NoError(t, f.Create(newTask("rt01-del")))

	err := f.Delete("rt01-del")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not persisted")

	_, taskErr := f.Task("rt01-del")
	assert.NoError(t, taskErr, "the task must still be there: its tombstone was never written")
}

// TestDelete_RefusedTombstoneLeavesTheTaskSchedulable: a definitively refused tombstone means the
// deletion did not happen, so the task stays exactly as it was — and, because it was never removed
// from the map, a Create that reused its id could not have been clobbered by a later rollback.
func TestDelete_RefusedTombstoneLeavesTheTaskSchedulable(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskDeleted), err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)

	require.NoError(t, f.Create(newTask("rt01-refused")))
	err := f.Delete("rt01-refused")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCommitRefused))

	task, taskErr := f.Task("rt01-refused")
	require.NoError(t, taskErr)
	assert.False(t, task.PendingDelete, "a refusal is not an unknown: nothing to reconcile")
	assert.Contains(t, f.ReadyTasks(), "rt01-refused", "the task is still runnable, as before the attempt")
}

// TestDelete_UnknownTombstoneBlocksExecution: when the tombstone may have been written, restoring a
// schedulable state would run work the durable log may already consider deleted.
func TestDelete_UnknownTombstoneBlocksExecution(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskDeleted), err: context.DeadlineExceeded}
	f := NewFabric().WithEventStore(store)

	require.NoError(t, f.Create(newTask("rt01-unknown-del")))
	err := f.Delete("rt01-unknown-del")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCommitUnknown))

	task, taskErr := f.Task("rt01-unknown-del")
	require.NoError(t, taskErr)
	assert.True(t, task.PendingDelete, "the task must be held for reconciliation")

	assert.NotContains(t, f.ReadyTasks(), "rt01-unknown-del", "it must not be scheduled again")
	_, acquireErr := f.Acquire("rt01-unknown-del", "agent-a", time.Minute)
	assert.Error(t, acquireErr, "and it must not be acquirable")

	ready, isReadyErr := f.IsReady("rt01-unknown-del")
	require.NoError(t, isReadyErr)
	assert.False(t, ready)
}

// TestCompilePlan_PartialCommitIsReportedAsPartial is the batch boundary: three of five created events
// land, and reporting that as "the plan was not committed" would make the caller retry all five and
// duplicate the three that are already durable.
func TestCompilePlan_PartialCommitIsReportedAsPartial(t *testing.T) {
	store := &selectiveStubStore{
		failOn:     string(EventTaskCreated),
		failTaskID: "plan-c",
		err:        errors.New("disk full"),
	}
	f := NewFabric().WithEventStore(store)

	ids, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "plan-a", Capability: "code"},
		{ID: "plan-b", Capability: "code"},
		{ID: "plan-c", Capability: "code"},
	})

	require.Error(t, err, "a partially committed batch must not be reported as success")
	assert.True(t, errors.Is(err, ErrPartialCommit), "the class must say partial, not total failure")

	var partial *PartialCommitError
	require.True(t, errors.As(err, &partial))
	assert.ElementsMatch(t, []string{"plan-a", "plan-b"}, partial.Committed)
	assert.Equal(t, []string{"plan-c"}, partial.Refused)
	assert.Empty(t, partial.Unknown)

	assert.ElementsMatch(t, []string{"plan-a", "plan-b"}, ids, "returns what is durable")

	// The durable members stay usable, and the refused one is kept too: nothing is rolled back when
	// part of the batch landed, because deleting a committed task loses work.
	for _, id := range []string{"plan-a", "plan-b", "plan-c"} {
		_, taskErr := f.Task(id)
		assert.NoError(t, taskErr, "task %s must still be present", id)
	}
}

// TestCompilePlan_AllRefusedRollsBack is the other end: nothing durable, so the all-or-nothing promise
// still holds and no task is left behind.
func TestCompilePlan_AllRefusedRollsBack(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskCreated), err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)

	ids, err := f.CompilePlan(context.Background(), []PlanStep{{ID: "plan-x", Capability: "code"}})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBatchNotCommitted))
	assert.Empty(t, ids)
	_, taskErr := f.Task("plan-x")
	assert.Error(t, taskErr, "a batch nothing persisted must leave no task behind")
}

// TestCompilePlan_UnknownIsNotRolledBack: an undetermined append may have landed, so the task stays
// (an unverified rollback is forbidden) and the caller is told to reconcile rather than retry.
func TestCompilePlan_UnknownIsNotRolledBack(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskCreated), err: context.DeadlineExceeded}
	f := NewFabric().WithEventStore(store)

	ids, err := f.CompilePlan(context.Background(), []PlanStep{{ID: "plan-u", Capability: "code"}})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPartialCommit))

	var partial *PartialCommitError
	require.True(t, errors.As(err, &partial))
	assert.Equal(t, []string{"plan-u"}, partial.Unknown)

	_, taskErr := f.Task("plan-u")
	assert.NoError(t, taskErr, "an unknown outcome must not delete the task: the event may exist")
	assert.Empty(t, ids)
}

// TestExpireDeadlines_ReportsAnUndurableExpiry: the sweep failed tasks in memory and reported the ids
// while the durability of those failures went unexamined — the same gap Create and Complete had.
//
// The ids are still returned when the report carries an error: the tasks ARE failed and their
// dependents HAVE been cascaded, so withholding them would be a second incorrect statement. What
// changes is that the caller now learns the durable side is unsettled.
func TestExpireDeadlines_ReportsAnUndurableExpiry(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskFailed), err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	withClock(f, &now)

	require.NoError(t, f.Create(&Task{ID: "exp-1", State: StateReady, Deadline: now.Add(-time.Second)}))

	expired, err := f.ExpireDeadlinesWithOutcome()

	require.Error(t, err, "an expiry the store refused must not be reported as recorded")
	assert.Contains(t, expired, "exp-1", "the task is failed in memory and must be named")

	task, taskErr := f.Task("exp-1")
	require.NoError(t, taskErr)
	assert.Equal(t, StateFailed, task.State)
}
