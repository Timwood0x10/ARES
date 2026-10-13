package agentfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReapRetired_RemovesOnlyAgedTombstones is the contract for the second half
// of the pool growth (P1-6): reclamation retires but never removes, so without
// reaping the registry keeps one record per spawn. The record must go exactly
// when it is RETIRED and older than the window — a live worker and a suspended
// one are never eligible, however long they sit there.
func TestReapRetired_RemovesOnlyAgedTombstones(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return now })
	ctx := context.Background()

	for _, id := range []string{"retired", "fresh", "live", "paused"} {
		_, err := f.Spawn(ctx, SpawnSpec{Identity: id, Capabilities: []string{"coder"}})
		require.NoError(t, err)
	}
	require.NoError(t, f.Retire(ctx, "retired"))
	require.NoError(t, f.Suspend(ctx, "paused"))

	// Two hours on: "retired" is past the one-hour window, while "fresh" is
	// retired only now and must stay — the age basis is retirement, not spawn.
	now = now.Add(2 * time.Hour)
	require.NoError(t, f.Retire(ctx, "fresh"))

	reaped := f.ReapRetired(ctx, DefaultRetiredAgentRetention)
	assert.Equal(t, []string{"retired"}, reaped,
		"only the tombstone past the retention window is reaped")

	for _, id := range []string{"fresh", "live", "paused"} {
		assert.Contains(t, f.Agents(), id, "%s must survive the reap", id)
	}
	_, err := f.Get("retired")
	assert.ErrorIs(t, err, ErrAgentNotFound,
		"a reaped record is gone; it must not come back as a retired shell")
}

// TestReapRetired_DisabledByZeroRetention pins the safe direction of the switch:
// a zero retention keeps every record, which is the pre-fix behaviour.
func TestReapRetired_DisabledByZeroRetention(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return now })
	ctx := context.Background()

	_, err := f.Spawn(ctx, SpawnSpec{Identity: "gone", Capabilities: []string{"coder"}})
	require.NoError(t, err)
	require.NoError(t, f.Retire(ctx, "gone"))
	now = now.Add(24 * time.Hour)

	assert.Empty(t, f.ReapRetired(ctx, 0), "a zero retention disables reaping")
	assert.Empty(t, f.ReapRetired(ctx, -time.Minute))
	assert.Contains(t, f.Agents(), "gone")
}

// TestReapRetired_NeverReapsAnUnstampedTombstone guards the age basis itself: a
// record with no retirement time would otherwise read as "retired at the
// beginning of time" and be reaped the instant it appeared.
func TestReapRetired_NeverReapsAnUnstampedTombstone(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return now })
	ctx := context.Background()

	_, err := f.Spawn(ctx, SpawnSpec{Identity: "unstamped", Capabilities: []string{"coder"}})
	require.NoError(t, err)
	agent, err := f.Get("unstamped")
	require.NoError(t, err)
	// Reach in deliberately: the state that must be guarded is "retired with no
	// stamp", which no production path produces — that is the point.
	agent.mu.Lock()
	agent.State = StateRetired
	agent.retiredAt = time.Time{}
	agent.mu.Unlock()

	assert.Empty(t, f.ReapRetired(ctx, time.Nanosecond))
	assert.Contains(t, f.Agents(), "unstamped",
		"an unstamped tombstone is never reaped")
}
