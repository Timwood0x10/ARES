package taskfabric

import (
	"context"
	"errors"
	"strings"
	"testing"

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
