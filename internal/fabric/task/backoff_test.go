package taskfabric

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestRetryBackoff pins the delay ladder and, above all, the two settings whose
// meaning must not drift: a non-positive base disables backoff (so the zero
// value keeps 0.3.2's immediate requeue), and a non-positive max means uncapped.
func TestRetryBackoff(t *testing.T) {
	const second = time.Second

	tests := []struct {
		name     string
		attempts int
		base     time.Duration
		max      time.Duration
		want     time.Duration
	}{
		{name: "disabled backoff stays immediate", attempts: 3, base: 0, max: second, want: 0},
		{name: "negative base is disabled too", attempts: 3, base: -second, max: second, want: 0},
		{name: "first retry waits one base", attempts: 1, base: second, want: second},
		{name: "second retry doubles", attempts: 2, base: second, want: 2 * second},
		{name: "third retry doubles again", attempts: 3, base: second, want: 4 * second},
		{name: "cap clamps the ladder", attempts: 3, base: second, max: 3 * second, want: 3 * second},
		{name: "cap below the base clamps the first retry", attempts: 1, base: 5 * second, max: 2 * second, want: 2 * second},
		{name: "zero attempts behaves like the first retry", attempts: 0, base: second, want: second},
		{name: "absurd attempt count saturates at the cap", attempts: 5000, base: second, max: time.Hour, want: time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, retryBackoff(tc.attempts, tc.base, tc.max))
		})
	}
}

// TestRetryBackoff_SaturatesInsteadOfOverflowing pins the property the guard
// exists for: an uncapped policy with an absurd attempt count must stay a huge
// positive delay, never wrap int64 into a tiny one (which would turn a
// "retry in 146 years" policy into "retry almost immediately").
func TestRetryBackoff_SaturatesInsteadOfOverflowing(t *testing.T) {
	const second = time.Second

	saturated := retryBackoff(1_000_000, second, 0)
	assert.Positive(t, saturated, "an overflow would wrap to a negative or tiny delay")
	assert.GreaterOrEqual(t, saturated, overflowGuard, "saturation happens at the guard")
	assert.Less(t, saturated, time.Duration(math.MaxInt64), "and stays inside positive int64")

	assert.Greater(t, saturated, retryBackoff(5, second, 0), "the ladder must stay monotone")
	assert.Equal(t, saturated, retryBackoff(2_000_000, second, 0), "and stable past saturation")
}

// TestNextAttemptAt pins the zero-value contract Fail relies on: no delay means
// the zero time, i.e. "immediately runnable", not "runnable at now".
func TestNextAttemptAt(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	assert.True(t, nextAttemptAt(now, 1, 0, 0).IsZero(), "no backoff must leave the due time unset")
	assert.Equal(t, now.Add(2*time.Second), nextAttemptAt(now, 2, time.Second, 0))
}

// TestBackoffMillis pins the persistence conversion: durations travel as whole
// milliseconds, and a positive sub-millisecond policy must not round down to 0
// (that would silently disable the policy after a restore).
func TestBackoffMillis(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want int
	}{
		{name: "zero stays zero", in: 0, want: 0},
		{name: "negative stays zero", in: -time.Second, want: 0},
		{name: "sub-millisecond rounds up to one", in: 500 * time.Microsecond, want: 1},
		{name: "exact milliseconds pass through", in: 1500 * time.Millisecond, want: 1500},
		{name: "fractional milliseconds truncate", in: 1500*time.Millisecond + 900*time.Microsecond, want: 1500},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, backoffMillis(tc.in))
		})
	}
}
