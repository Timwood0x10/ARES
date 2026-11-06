package taskfabric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// TestFailureLevel_MapsCauses pins the classification table. The mapping is the whole feature: a
// level that is attached to the wrong cause either burns retries on something that cannot succeed
// (L0 classified as retryable) or kills a task that would have recovered (retryable classified as
// L0).
func TestFailureLevel_MapsCauses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Level
	}{
		{"nil is unclassified", nil, LevelUnclassified},
		{"missing entity is fatal", apperrors.ErrNotFound, LevelFatal},
		{"malformed request is fatal", apperrors.ErrInvalidArgument, LevelFatal},
		{"permission is fatal", apperrors.ErrAccessDenied, LevelFatal},
		{"deadline is fatal", ErrTaskDeadlineExceeded, LevelFatal},
		{"rate limit is throttled", apperrors.ErrRateLimitExceeded, LevelThrottled},
		{"unavailable is transient", apperrors.ErrServiceUnavailable, LevelTransient},
		{"explicit fatal wins over a retryable sentinel", WithLevel(apperrors.ErrServiceUnavailable, LevelFatal), LevelFatal},
		{"explicit transient wins over a fatal sentinel", WithLevel(apperrors.ErrNotFound, LevelTransient), LevelTransient},
		{"unknown stays unclassified", assert.AnError, LevelUnclassified},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, failureLevel(tc.err))
		})
	}
}

// TestFail_FatalKillsEvenWithBudget is L0's definition: the task is killed, the remaining budget is
// not spent on attempts that reproduce the failure.
func TestFail_FatalKillsEvenWithBudget(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "t", Capability: "c", RetryPolicy: RetryPolicy{MaxRetries: 5}}))
	epoch, err := f.Acquire("t", "agent", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t", "agent", epoch))

	require.NoError(t, f.Fail("t", "agent", epoch, MarkPermanent(apperrors.ErrAccessDenied)))

	tk, err := f.Task("t")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, tk.State, "L0 must be terminal on the first failure")
	assert.Equal(t, 1, tk.RetryPolicy.Attempts, "the budget was not spent")
	assert.True(t, tk.NextAttemptAt.IsZero(), "no retry was scheduled")
}

// TestFail_TransientBacksOffByDefault: L1 is the IO case, and it must wait before the next attempt.
// The default matters because tasks created by Submit carry no backoff at all — without it, a
// transient failure retries immediately, which is a busy loop against a struggling dependency.
func TestFail_TransientBacksOffByDefault(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "t", Capability: "c", RetryPolicy: RetryPolicy{MaxRetries: 3}}))
	epoch, err := f.Acquire("t", "agent", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t", "agent", epoch))

	before := time.Now()
	require.NoError(t, f.Fail("t", "agent", epoch, MarkRetryable(apperrors.ErrServiceUnavailable)))

	tk, err := f.Task("t")
	require.NoError(t, err)
	assert.Equal(t, StateReady, tk.State, "L1 is retryable")
	require.False(t, tk.NextAttemptAt.IsZero(), "a transient retry must be scheduled, not immediate")
	assert.GreaterOrEqual(t, tk.NextAttemptAt.Sub(before), defaultTransientBackoff/2,
		"the first retry waits about the transient default")
}

// TestFail_ThrottledWaitsLongerThanTransient: L2's cause is "too fast", so waiting the L1 amount
// would reproduce it. The assertion is the relationship, not the constant.
func TestFail_ThrottledWaitsLongerThanTransient(t *testing.T) {
	nextDelay := func(t *testing.T, cause error) time.Duration {
		t.Helper()
		f := NewFabric()
		require.NoError(t, f.Create(&Task{ID: "t", Capability: "c", RetryPolicy: RetryPolicy{MaxRetries: 3}}))
		epoch, err := f.Acquire("t", "agent", time.Minute)
		require.NoError(t, err)
		require.NoError(t, f.Start("t", "agent", epoch))
		before := time.Now()
		require.NoError(t, f.Fail("t", "agent", epoch, cause))
		tk, err := f.Task("t")
		require.NoError(t, err)
		return tk.NextAttemptAt.Sub(before)
	}

	transient := nextDelay(t, MarkRetryable(apperrors.ErrServiceUnavailable))
	throttled := nextDelay(t, apperrors.ErrRateLimitExceeded)

	assert.GreaterOrEqual(t, throttled, 4*transient,
		"a rate limit must back off at least four times longer than a transient failure "+
			"(transient=%v throttled=%v)", transient, throttled)
}

// TestFail_UnclassifiedKeepsImmediateRetry is the control for the two tests above: introducing
// levels must not change what an unclassified failure does. 0.3.2 behaviour was "retry at once",
// and a task that never classifies its causes relies on that.
func TestFail_UnclassifiedKeepsImmediateRetry(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "t", Capability: "c", RetryPolicy: RetryPolicy{MaxRetries: 3}}))
	epoch, err := f.Acquire("t", "agent", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t", "agent", epoch))

	require.NoError(t, f.Fail("t", "agent", epoch, assert.AnError))

	tk, err := f.Task("t")
	require.NoError(t, err)
	assert.Equal(t, StateReady, tk.State)
	assert.True(t, tk.NextAttemptAt.IsZero(), "unclassified failures stay immediately runnable")
}
