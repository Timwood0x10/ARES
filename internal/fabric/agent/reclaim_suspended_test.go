package agentfabric

// reclaim_suspended_test.go covers the SECOND state the reclamation sweep is
// allowed to retire.
//
// Why it exists: the sweep used to require StateIdle, so a SUSPENDED agent fell
// through every exit this fabric has — it is not a scheduling candidate (the pool
// requires IDLE), Resume has no production caller, and the reaper only removes
// RETIRED records. A paused agent therefore held its resource claim and its
// Cognition for the life of the process. The state table below is the exhaustive
// form of that fix (rules v2 §11): every state the sweep can meet, crossed with
// the two things that disqualify a candidate (a quantum in flight, a live lease)
// and with the windows on either side.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// fixedClock returns a fabric whose clock the test drives, plus the mutable
// instant it reads. Every case needs this: the sweep's whole decision is about
// elapsed time.
func fixedClock() (*Fabric, *time.Time) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	at := now
	return NewFabric().WithClock(func() time.Time { return at }), &at
}

// withoutSuspendedArm is the default policy with the suspended half switched off
// — the documented "keep paused agents" posture.
func withoutSuspendedArm() AgentReclaimPolicy {
	p := DefaultAgentReclaimPolicy()
	p.SuspendedTTL = 0
	return p
}

// TestReclaimIdle_StateTable walks the sweep's whole trigger surface. Each row
// gets its own fabric and a fresh keeper: the per-capability floor keeps the LAST
// holder alive, so without a second holder every row would be kept for a reason
// that has nothing to do with the state under test.
func TestReclaimIdle_StateTable(t *testing.T) {
	ctx := context.Background()
	const capability = "coder"

	cases := []struct {
		name        string
		suspend     bool
		retire      bool
		busy        bool
		suspendBusy bool
		inUse       bool
		age         time.Duration
		policy      func() AgentReclaimPolicy
		want        bool
	}{
		{name: "IDLE past the idle window is reclaimed", age: 11 * time.Minute, want: true},
		{name: "IDLE inside the idle window is kept", age: 5 * time.Minute},
		{name: "IDLE with a quantum in flight is kept", busy: true, age: 11 * time.Minute},
		{name: "IDLE named in a live lease is kept", inUse: true, age: 11 * time.Minute},
		{
			name:    "SUSPENDED inside its (longer) window is kept",
			suspend: true, age: 11 * time.Minute,
		},
		{
			name:    "SUSPENDED past its window is reclaimed",
			suspend: true, age: 31 * time.Minute, want: true,
		},
		{
			name:    "SUSPENDED with the arm disabled is kept however old",
			suspend: true, age: 10 * time.Hour, policy: withoutSuspendedArm,
		},
		{name: "SUSPENDED named in a live lease is kept", suspend: true, inUse: true, age: 31 * time.Minute},
		{
			// The defensive half of "a suspension happens at a quantum boundary":
			// a SUSPENDED agent should never have a quantum in flight, and if one
			// does the sweep WAITS rather than retiring an agent that is running.
			// That is the safe direction (the claim stays held, the work is not
			// interrupted), and it is the only outcome here — there is no alert for
			// it, because the fabric has no logger and the repo has no metrics
			// facility to reach for (rules v2 §10). Reachable only through the
			// unguarded SetBusy: the dispatch path claims with TryBeginQuantum,
			// which requires IDLE.
			name: "SUSPENDED with a quantum in flight is kept", suspend: true, suspendBusy: true,
			age: 10 * time.Hour,
		},
		{name: "RETIRED is never a reclamation target", retire: true, age: 10 * time.Hour},
		{name: "RETIRED is not touched even when very old", retire: true, age: 100 * time.Hour},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, at := fixedClock()
			subject, err := f.Spawn(ctx, SpawnSpec{Identity: "subject", Capabilities: []string{capability}})
			require.NoError(t, err)

			if tc.suspend {
				require.NoError(t, f.Suspend(ctx, "subject"))
			}
			if tc.retire {
				require.NoError(t, f.Retire(ctx, "subject"))
			}
			if tc.busy {
				// The real guarded claim, not a field write: this is what the
				// dispatcher does, and it only succeeds from IDLE.
				require.True(t, subject.TryBeginQuantum(), "the row must actually be busy")
			}
			if tc.suspendBusy {
				// The invariant violation the sweep defends against, reachable only
				// through this unguarded writer (see the row's comment).
				subject.SetBusy(true)
			}

			// Age the subject, then add the keeper the floor will count instead.
			*at = at.Add(tc.age)
			_, err = f.Spawn(ctx, SpawnSpec{Identity: "keeper", Capabilities: []string{capability}})
			require.NoError(t, err)

			policy := DefaultAgentReclaimPolicy()
			if tc.policy != nil {
				policy = tc.policy()
			}
			var inUse map[string]struct{}
			if tc.inUse {
				inUse = map[string]struct{}{"subject": {}}
			}

			reclaimed := f.ReclaimIdle(ctx, policy, inUse)

			reported := make([]string, 0, len(reclaimed))
			for _, r := range reclaimed {
				reported = append(reported, r.Identity)
			}
			require.Equal(t, tc.want, len(reclaimed) == 1, "reclaimed=%v", reported)
			if tc.want {
				assert.Equal(t, "subject", reported[0])
				// The state it was retired FROM, not the one it is in now: the
				// sweep reports what it found, and the record is RETIRED by the time
				// the caller reads it. That is the field's whole point — an operator
				// reading the log can tell "was unused" from "had paused work".
				wantState := StateIdle
				if tc.suspend {
					wantState = StatePaused
				}
				assert.Equal(t, wantState, reclaimed[0].State,
					"the report must carry the state the sweep found")
			}

			got, getErr := f.Get("subject")
			require.NoError(t, getErr, "the record survives retirement (observability)")
			if tc.want || tc.retire {
				assert.Equal(t, StateRetired, got.State)
			} else {
				assert.NotEqual(t, StateRetired, got.State,
					"a kept agent must not be retired as a side effect")
			}
		})
	}
}

// TestReclaimIdle_SuspendedAgentReleasesEverything is the leak itself, end to end:
// before this arm existed a suspended agent kept its claim and its execution body
// forever, so a budget configured by the operator filled up and every later spawn
// failed permanently — the same false death P1-6 fixed for idle agents, reachable
// through a state the sweep did not cover.
func TestReclaimIdle_SuspendedAgentReleasesEverything(t *testing.T) {
	ctx := context.Background()
	f, at := fixedClock()
	f.WithResourceBudget(map[string]float64{"slot": 2})

	agent, err := f.Spawn(ctx, SpawnSpec{
		Identity:         "paused",
		Capabilities:     []string{"coder"},
		Resources:        map[string]any{"slot": 1.0},
		CognitionFactory: func([]string) Cognition { return &stubCognition{} },
	})
	require.NoError(t, err)
	require.NoError(t, f.Suspend(ctx, "paused"))

	// Inside the suspended window — and already past the idle one on purpose, so
	// the assertion says which window governs a suspension.
	*at = at.Add(11 * time.Minute)
	assert.Empty(t, f.ReclaimIdle(ctx, DefaultAgentReclaimPolicy(), nil),
		"the idle window must not govern a deliberate suspension")

	*at = at.Add(20 * time.Minute) // 31m total, past SuspendedTTL
	// A second holder of the capability, so the floor is not what decides here.
	_, err = f.Spawn(ctx, SpawnSpec{Identity: "keeper", Capabilities: []string{"coder"}})
	require.NoError(t, err)

	reclaimed := f.ReclaimIdle(ctx, DefaultAgentReclaimPolicy(), nil)
	require.Len(t, reclaimed, 1)
	assert.Equal(t, StatePaused, reclaimed[0].State)

	allocated, _ := f.QuotaSnapshot()
	assert.Zero(t, allocated["slot"], "the suspended agent's claim must be back")

	_, err = agent.ExecuteStep(ctx, models.NewTask("q", "coder", nil))
	assert.ErrorIs(t, err, ErrAgentNotExecutable,
		"and its execution body must be released, not parked forever")
}
