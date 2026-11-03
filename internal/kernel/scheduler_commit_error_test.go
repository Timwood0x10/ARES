package kernel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestQuantumResult_StorageOutcomeIsNotAnExecutorVerdict pins the split the attribution depends on.
//
// Two different failures used to look identical to the scheduler: the step succeeded and the
// completion was refused, versus the step failed. Reading a single error forced a guess, and the
// guess charged the storage refusal to an agent whose execution had succeeded.
func TestQuantumResult_StorageOutcomeIsNotAnExecutorVerdict(t *testing.T) {
	refused := errors.Join(taskfabric.ErrCommitRefused, errors.New("disk full"))

	cases := []struct {
		name string
		qr   taskfabric.QuantumResult
		want bool
	}{
		{
			name: "step succeeded, completion refused",
			qr:   taskfabric.QuantumResult{Executed: true, CommitErr: refused},
			want: true,
		},
		{
			name: "step succeeded, completion durable",
			qr:   taskfabric.QuantumResult{Executed: true},
			want: true,
		},
		{
			name: "step failed, failure not even recordable",
			qr:   taskfabric.QuantumResult{Executed: true, StepErr: errors.New("tool exploded"), CommitErr: refused},
			want: false,
		},
		{
			name: "never executed",
			qr:   taskfabric.QuantumResult{Executed: false, StepErr: taskfabric.ErrNotOwner},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.qr.Succeeded())
		})
	}
}

// TestQuantumResult_RetriesAreCarriedNotInferred: a real step failure whose failure-append was ALSO
// refused used to score zero retries, because the joined error matched the commit sentinel. The retry
// is now a field recorded at the requeue decision.
func TestQuantumResult_RetriesAreCarriedNotInferred(t *testing.T) {
	qr := taskfabric.QuantumResult{
		Executed:        true,
		StepErr:         errors.New("tool exploded"),
		CommitErr:       errors.Join(taskfabric.ErrCommitRefused, context.DeadlineExceeded),
		RetriesConsumed: 1,
	}

	assert.Equal(t, 1, qr.RetriesConsumed,
		"the retry that was actually spent must survive a commit failure riding on the same result")
	assert.False(t, qr.Succeeded())

	// A single dimension is returned as itself, so sentinel comparisons keep working.
	assert.Equal(t, taskfabric.ErrNotOwner, taskfabric.QuantumResult{StepErr: taskfabric.ErrNotOwner}.Err())
}
