package agentfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestReclaimIdle_ReleasesQuota is the A-factory3 / P1-6 regression: with no
// reclamation every spawned agent held its claim for the life of the process, so
// a configured budget filled up and every later spawn failed permanently. The
// sweep must give the claim back AND leave a later spawn of the same size working
// — the false-death is the symptom, so "the quota went down" alone is not enough.
func TestReclaimIdle_ReleasesQuota(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return now })
	f.WithResourceBudget(map[string]float64{"slot": 2})

	ctx := context.Background()
	for _, id := range []string{"a1", "a2"} {
		_, err := f.Spawn(ctx, SpawnSpec{
			Identity: id, Capabilities: []string{"coder"},
			Resources: map[string]any{"slot": 1.0},
		})
		require.NoError(t, err)
	}
	// The pool is full, and the refusal explains itself (P1-6 visibility: a bare
	// ErrResourceQuotaExceeded reads as "the pool is broken").
	_, err := f.Spawn(ctx, SpawnSpec{
		Identity: "a3", Capabilities: []string{"coder"},
		Resources: map[string]any{"slot": 1.0},
	})
	require.ErrorIs(t, err, ErrResourceQuotaExceeded)
	assert.Contains(t, err.Error(), "allocated", "the refusal must carry the accounting")

	// No quantum ran, so both are inactive; the floor keeps one holder of "coder".
	now = now.Add(11 * time.Minute)
	reclaimed := f.ReclaimIdle(ctx, DefaultAgentReclaimPolicy(), nil)
	require.Len(t, reclaimed, 1, "the capability floor must keep exactly one holder")
	assert.Equal(t, "a2", reclaimed[0].Identity,
		"candidates are processed in id order, so the first one is KEPT for the floor")
	assert.Equal(t, 1.0, reclaimed[0].Resources["slot"])
	assert.Equal(t, []string{"coder"}, reclaimed[0].Capabilities)

	allocated, budget := f.QuotaSnapshot()
	assert.Equal(t, 1.0, allocated["slot"], "the retired agent's claim must be back")
	assert.Equal(t, 2.0, budget["slot"])

	_, err = f.Spawn(ctx, SpawnSpec{
		Identity: "a3", Capabilities: []string{"coder"},
		Resources: map[string]any{"slot": 1.0},
	})
	require.NoError(t, err, "the released slot must be usable again")
}

// TestReclaimIdle_KeepsLastCapabilityHolder pins the floor on its own: retiring
// the only agent able to serve a capability would trade a quota leak for tasks
// that can never be scheduled at all (ErrNoCapableCandidate, defect A-F3).
func TestReclaimIdle_KeepsLastCapabilityHolder(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return now })
	ctx := context.Background()

	_, err := f.Spawn(ctx, SpawnSpec{Identity: "solo", Capabilities: []string{"reviewer"}})
	require.NoError(t, err)

	now = now.Add(time.Hour)
	assert.Empty(t, f.ReclaimIdle(ctx, DefaultAgentReclaimPolicy(), nil),
		"the last holder of a capability must survive the sweep")
	_, err = f.Get("solo")
	assert.NoError(t, err, "and it is still a candidate for scheduling")
}

// TestReclaimIdle_SkipsRecentAndBusy pins the two conditions that keep the sweep
// from retiring an agent that is working: an agent active within the window, and
// an agent with a quantum in flight (busy). Either must survive.
func TestReclaimIdle_SkipsRecentAndBusy(t *testing.T) {
	ctx := context.Background()

	t.Run("active within the window", func(t *testing.T) {
		now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
		f := NewFabric().WithClock(func() time.Time { return now })
		_, err := f.Spawn(ctx, SpawnSpec{Identity: "stale", Capabilities: []string{"coder"}})
		require.NoError(t, err)
		now = now.Add(11 * time.Minute)
		_, err = f.Spawn(ctx, SpawnSpec{Identity: "fresh", Capabilities: []string{"coder"}})
		require.NoError(t, err)
		now = now.Add(2 * time.Minute) // stale: 13m idle; fresh: 2m idle

		reclaimed := f.ReclaimIdle(ctx, DefaultAgentReclaimPolicy(), nil)
		require.Len(t, reclaimed, 1)
		assert.Equal(t, "stale", reclaimed[0].Identity)
		_, err = f.Get("fresh")
		assert.NoError(t, err, "an agent active inside the window must survive")
	})

	t.Run("quantum in flight", func(t *testing.T) {
		now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
		f := NewFabric().WithClock(func() time.Time { return now })
		_, err := f.Spawn(ctx, SpawnSpec{Identity: "stale", Capabilities: []string{"coder"}})
		require.NoError(t, err)
		working, err := f.Spawn(ctx, SpawnSpec{Identity: "working", Capabilities: []string{"coder"}})
		require.NoError(t, err)
		working.SetBusy(true)

		now = now.Add(time.Hour)
		reclaimed := f.ReclaimIdle(ctx, DefaultAgentReclaimPolicy(), nil)
		require.Len(t, reclaimed, 1)
		assert.Equal(t, "stale", reclaimed[0].Identity, "the busy agent must be skipped")
		_, err = f.Get("working")
		assert.NoError(t, err)
	})
}

// TestReclaimIdle_DisabledByZeroTTL pins the off switch: a zero window is the
// documented way to keep every agent, and must retire nothing.
func TestReclaimIdle_DisabledByZeroTTL(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return now })
	ctx := context.Background()

	_, err := f.Spawn(ctx, SpawnSpec{Identity: "kept", Capabilities: []string{"coder"}})
	require.NoError(t, err)
	now = now.Add(24 * time.Hour)

	assert.Empty(t, f.ReclaimIdle(ctx, AgentReclaimPolicy{}, nil), "a zero TTL disables reclamation")
	assert.Empty(t, f.ReclaimIdle(ctx, AgentReclaimPolicy{IdleTTL: -time.Minute}, nil))
	_, err = f.Get("kept")
	assert.NoError(t, err)
}

// TestReclaimIdle_SkipsAgentsInUse pins the third condition the sweep honours:
// the caller's in-use set (in production, the owners of live task leases)
// outranks the activity clock. An agent whose task was dispatched but whose
// quantum has not started yet still looks idle, and reclaiming it would hand its
// quota to another spawn while that work is still holding the resources.
func TestReclaimIdle_SkipsAgentsInUse(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return now })
	ctx := context.Background()

	for _, id := range []string{"leased", "idle"} {
		_, err := f.Spawn(ctx, SpawnSpec{Identity: id, Capabilities: []string{"coder"}})
		require.NoError(t, err)
	}
	now = now.Add(time.Hour)

	reclaimed := f.ReclaimIdle(ctx, DefaultAgentReclaimPolicy(), map[string]struct{}{"leased": {}})
	require.Len(t, reclaimed, 1)
	assert.Equal(t, "idle", reclaimed[0].Identity,
		"an agent the caller reports as in use must survive the sweep")
	_, err := f.Get("leased")
	assert.NoError(t, err)
}

// TestRetireReleasesRuntimeState pins the MEMORY half of the pool leak (P1-6):
// giving the quota back is not enough, because an agent's Cognition holds the
// execution body (LLM client, tool bundle). A retired agent is a record, not a
// worker — it can never execute again — so both the claim and the body must go.
// Without this, reclamation drains the counter and keeps the memory.
func TestRetireReleasesRuntimeState(t *testing.T) {
	ctx := context.Background()
	f := NewFabric().WithResourceBudget(map[string]float64{"slot": 1})

	agent, err := f.Spawn(ctx, SpawnSpec{
		Identity:         "used",
		Capabilities:     []string{"coder"},
		Resources:        map[string]any{"slot": 1.0},
		CognitionFactory: func([]string) Cognition { return &stubCognition{} },
	})
	require.NoError(t, err)

	// A real worker before retirement: the execution body is installed.
	_, err = agent.ExecuteStep(ctx, models.NewTask("q1", "coder", nil))
	require.NoError(t, err)

	require.NoError(t, f.Retire(ctx, "used"))

	_, err = agent.ExecuteStep(ctx, models.NewTask("q2", "coder", nil))
	assert.ErrorIs(t, err, ErrAgentNotExecutable,
		"a retired agent must not keep an execution body")
	allocated, _ := f.QuotaSnapshot()
	assert.Zero(t, allocated["slot"], "its claim must be back in the quota")
	retired, getErr := f.Get("used")
	require.NoError(t, getErr)
	assert.Equal(t, StateRetired, retired.State, "the record itself stays (observability)")
}
