package taskfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompilePlan_DistinguishesExplicitZeroRetries is the reason PlanStep carries MaxRetriesSet.
//
// Zero used to mean "unset", so a step configured as max_attempts=1 — never retry — was silently
// upgraded to the two attempts of the default. The two cases now differ: an explicit zero reaches
// the task as zero, while an absent policy still gets the plan default.
func TestCompilePlan_DistinguishesExplicitZeroRetries(t *testing.T) {
	f := NewFabric()

	ids, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "no-retry", Capability: "code", MaxRetries: 0, MaxRetriesSet: true},
		{ID: "unset", Capability: "code"},
	})
	require.NoError(t, err)
	require.Len(t, ids, 2)

	noRetry, err := f.Task("no-retry")
	require.NoError(t, err)
	assert.Zero(t, noRetry.RetryPolicy.MaxRetries, "an explicit zero must not be replaced by the default")
	assert.False(t, noRetry.CanRetry(), "and it must actually stop after the first attempt")

	unset, err := f.Task("unset")
	require.NoError(t, err)
	assert.Equal(t, planDefaultMaxRetries, unset.RetryPolicy.MaxRetries, "an unset policy keeps the documented default")
}

// TestCompilePlan_ResolvesRelativeDeadline is the other half of the L1 contract: task_timeout is a
// RELATIVE budget, and the absolute instant must be stamped at creation so a restart does not reset
// the clock.
func TestCompilePlan_ResolvesRelativeDeadline(t *testing.T) {
	f := NewFabric()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	withClock(f, &now)

	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "bounded", Capability: "code", Deadline: 30 * time.Second},
	})
	require.NoError(t, err)

	tk, err := f.Task("bounded")
	require.NoError(t, err)
	assert.Equal(t, now.Add(30*time.Second), tk.Deadline, "the relative budget must become an absolute deadline")
}
