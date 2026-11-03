package taskfabric

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunQuantumResult_LateResultIsDiscarded reproduces the fencing race deterministically, from
// inside the step:
//
//  1. the old holder is running,
//  2. it is preempted and the task is re-acquired (which clears PreemptRequested),
//  3. the step returns and its Complete/Fail is rejected by the ownership check.
//
// The earlier guard only caught the case where the flag was still set when the step returned, so a
// late result slipped through as Executed=true and was scored — rewarding a success the task never
// took, or punishing a failure nobody was waiting for.
func TestRunQuantumResult_LateResultIsDiscarded(t *testing.T) {
	cases := []struct {
		name    string
		stepErr error
	}{
		{name: "late success", stepErr: nil},
		{name: "late failure", stepErr: errors.New("tool exploded while being preempted")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFabric()
			require.NoError(t, f.Create(newTask("fence-1")))

			oldEpoch, err := f.Acquire("fence-1", "agent-old", time.Minute)
			require.NoError(t, err)

			qr := f.RunQuantumResult("fence-1", "agent-old", oldEpoch, func() (any, bool, error) {
				// Inside the step: preempt, then a new holder takes the task — the interleaving that
				// clears PreemptRequested before the step returns.
				require.NoError(t, f.Preempt("fence-1", "agent-old", oldEpoch, "higher priority work"))
				_, newErr := f.Acquire("fence-1", "agent-new", time.Minute)
				require.NoError(t, newErr)
				return "late result", true, tc.stepErr
			})

			assert.True(t, qr.Discarded,
				"a result the task did not take must be reported as discarded, not as a verdict")
			assert.Nil(t, qr.CommitErr,
				"there was no store write to judge: the fencing rejection is not a commit outcome")
			assert.NotEqual(t, 1, qr.RetriesConsumed,
				"a discarded result consumed no retry")
			assert.False(t, qr.Succeeded() && qr.Discarded,
				"a discarded result must never be reported as a success")
		})
	}
}

// TestRunQuantumResult_LiveFencingIsNotACommitOutcome keeps the other half honest: when ownership is
// lost before the step even starts, the result is not executed, and the reason is a runtime
// rejection rather than a storage failure.
func TestRunQuantumResult_LiveFencingIsNotACommitOutcome(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(newTask("fence-2")))

	// Nobody holds the task, so Start is rejected.
	qr := f.RunQuantumResult("fence-2", "agent-x", 1, func() (any, bool, error) {
		return nil, true, nil
	})

	assert.False(t, qr.Executed)
	assert.Nil(t, qr.CommitErr)
	assert.Error(t, qr.StepErr)
}
