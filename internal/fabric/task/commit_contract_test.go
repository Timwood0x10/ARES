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
}

func (s *selectiveStubStore) Append(_ context.Context, _ string, events []*ares_events.Event, _ int64) error {
	for _, ev := range events {
		if string(ev.Type) == s.failOn {
			return s.err
		}
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
