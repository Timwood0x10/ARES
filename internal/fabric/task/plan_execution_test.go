package taskfabric

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlanMaxAttempts_BoundsActualExecutions is the execution-level half of S10: the previous tests
// stopped at "the field reached the task", which cannot distinguish a policy that works from one that
// is merely stored. Here the task is actually acquired and failed until the fabric refuses it.
func TestPlanMaxAttempts_BoundsActualExecutions(t *testing.T) {
	cases := []struct {
		attempts int
	}{
		{attempts: 1},
		{attempts: 2},
		{attempts: 3},
	}

	for _, tc := range cases {
		t.Run(string(rune('0'+tc.attempts))+" attempts", func(t *testing.T) {
			f := NewFabric()
			_, err := f.CompilePlan(context.Background(), []PlanStep{{
				ID:         "t",
				Capability: "code",
				// PlanStep.MaxRetries and the catalogue's max_attempts are the same unit: total
				// attempts, first execution included.
				MaxRetries:    tc.attempts,
				MaxRetriesSet: true,
			}})
			require.NoError(t, err)

			executions := 0
			for {
				epoch, acqErr := f.Acquire("t", "agent-a", time.Minute)
				if acqErr != nil {
					break // the budget is spent; this is where execution stops
				}
				executions++
				require.NoError(t, f.Start("t", "agent-a", epoch), "a claimed task must enter RUNNING before it can fail")
				require.NoError(t, f.Fail("t", "agent-a", epoch, errors.New("disk: i/o error")))
				if executions > 10 {
					t.Fatal("the task kept coming back: the retry budget is not being enforced")
				}
			}

			assert.Equal(t, tc.attempts, executions,
				"max_attempts must bound how many times the work actually runs")

			final, err := f.Task("t")
			require.NoError(t, err)
			assert.Equal(t, StateFailed, final.State, "and it must end terminally, not parked forever")
			assert.Equal(t, tc.attempts, final.RetryPolicy.Attempts)
		})
	}
}

// TestPlanDeadline_IsNotExtendedByQueueingOrRetry: task_timeout is a wall-clock budget resolved once
// at compile time. If a retry re-armed it, a task with a 30s budget could run for as long as it keeps
// failing — which is the failure mode the field exists to prevent.
func TestPlanDeadline_IsNotExtendedByQueueingOrRetry(t *testing.T) {
	f := NewFabric()
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	withClock(f, &now)

	_, err := f.CompilePlan(context.Background(), []PlanStep{{
		ID:            "t",
		Capability:    "code",
		Deadline:      30 * time.Second,
		MaxRetries:    2,
		MaxRetriesSet: true,
	}})
	require.NoError(t, err)

	compiled, err := f.Task("t")
	require.NoError(t, err)
	deadline := compiled.Deadline
	require.Equal(t, now.Add(30*time.Second), deadline)

	// Queueing does not extend it.
	now = now.Add(10 * time.Second)
	queued, err := f.Task("t")
	require.NoError(t, err)
	assert.Equal(t, deadline, queued.Deadline, "time spent waiting must come out of the budget")

	// A failed attempt does not extend it either.
	epoch, err := f.Acquire("t", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t", "agent-a", epoch))
	require.NoError(t, f.Fail("t", "agent-a", epoch, errors.New("i/o error")))
	retried, err := f.Task("t")
	require.NoError(t, err)
	assert.Equal(t, deadline, retried.Deadline, "a retry must not re-arm the clock")
	assert.Equal(t, StateReady, retried.State, "the retry itself is still scheduled")

	// Past the deadline the task cannot be picked up again, even though retries remain.
	now = now.Add(25 * time.Second)
	_, err = f.Acquire("t", "agent-b", time.Minute)
	assert.ErrorIs(t, err, ErrTaskDeadlineExceeded)

	expired := f.ExpireDeadlines()
	assert.Contains(t, expired, "t")

	final, err := f.Task("t")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, final.State)
}
