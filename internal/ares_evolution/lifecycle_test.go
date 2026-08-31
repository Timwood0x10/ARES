package evolution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// --- LifecycleSnapshot tests ---

func TestCandidateState_String(t *testing.T) {
	tests := []struct {
		state CandidateState
		want  string
	}{
		{StateCandidate, "candidate"},
		{StateShadow, "shadow"},
		{StateActive, "active"},
		{StateDegraded, "degraded"},
		{CandidateState(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.state.String())
		})
	}
}

// --- LifecycleConfig tests ---

func TestDefaultLifecycleConfig(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	assert.True(t, cfg.Enabled)
	assert.Equal(t, 50, cfg.FitnessWindow)
	assert.Equal(t, 10, cfg.MinSamplesBeforeJudge)
	assert.InDelta(t, 0.5, cfg.ColdStartScore, 0.001)
	assert.True(t, cfg.Shadow.Enabled)
	assert.Equal(t, 20, cfg.Shadow.MinSamples)
	assert.InDelta(t, 0.55, cfg.Shadow.MinWinRate, 0.001)
	assert.True(t, cfg.Rollback.Enabled)
	assert.InDelta(t, 0.15, cfg.Rollback.DegradationThreshold, 0.001)
	assert.Equal(t, 5, cfg.Rollback.WindowSize)
	assert.Equal(t, 3, cfg.Rollback.MinSamples)
	assert.InDelta(t, 0.7, cfg.Gates.EvalMinScore, 0.001)
	assert.False(t, cfg.Gates.RequireManualApproval)
}

// --- StrategyLifecycle tests ---

func newTestLifecycle(t *testing.T, cfg LifecycleConfig) (*StrategyLifecycle, *ActiveStrategyManager, evidence.Store) {
	t.Helper()
	store := evidence.NewMemoryStore()
	asm, err := NewActiveStrategyManager(newMockStrategyStore(), NewRollbackPolicy())
	require.NoError(t, err)

	aggCfg := DefaultAggregatorConfig()
	agg := NewRuntimeFitnessAggregator(store, aggCfg)

	lc := NewStrategyLifecycle(asm, agg, cfg,
		WithLifecycleEvidenceStore(store),
	)
	return lc, asm, store
}

func TestNewStrategyLifecycle_NilSafe(t *testing.T) {
	// nil lifecycle must not panic on any method
	var lc *StrategyLifecycle
	assert.NotPanics(t, func() { lc.Start(context.Background()) })
	assert.NotPanics(t, func() { lc.Stop() })
	assert.NotPanics(t, func() { lc.Submit(context.Background(), nil, 0) })
	assert.NotPanics(t, func() { lc.Approve() })

	snap := lc.Snapshot()
	assert.Equal(t, "disabled", snap.State)
}

func TestStrategyLifecycle_Snapshot_InitialState(t *testing.T) {
	lc, asm, _ := newTestLifecycle(t, DefaultLifecycleConfig())
	// Deploy a base strategy so Current() is non-nil.
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	snap := lc.Snapshot()
	assert.Equal(t, "active", snap.State)
	assert.Equal(t, "base", snap.ActiveID)
	assert.Equal(t, 0, snap.Generation)
	assert.False(t, snap.PendingApproval)
}

func TestStrategyLifecycle_LifecycleSnapshotMap(t *testing.T) {
	lc, asm, _ := newTestLifecycle(t, DefaultLifecycleConfig())
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	m := lc.LifecycleSnapshot()
	assert.Equal(t, "active", m["state"])
	assert.Equal(t, "base", m["active_id"])
	assert.Equal(t, 0, m["generation"])
	// last_decision should not be present when empty
	_, ok := m["last_decision"]
	assert.False(t, ok)
}

func TestStrategyLifecycle_Submit_Blacklisted(t *testing.T) {
	lc, asm, _ := newTestLifecycle(t, DefaultLifecycleConfig())
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	candidate := &mutation.Strategy{ID: "bad", Version: 2, Score: 30.0}

	// Manually blacklist the candidate for generation 1.
	lc.mu.Lock()
	lc.blacklist[candidate.ID] = 1
	lc.mu.Unlock()

	// Submit must be a no-op.
	lc.Submit(context.Background(), candidate, 1)

	// Active strategy should remain "base".
	assert.Equal(t, "base", asm.Current().ID)
}

func TestStrategyLifecycle_Submit_NoGates(t *testing.T) {
	lc, asm, _ := newTestLifecycle(t, DefaultLifecycleConfig())
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	candidate := &mutation.Strategy{ID: "better", Version: 2, Score: 80.0}

	// No gates configured → Submit promotes directly.
	lc.Submit(context.Background(), candidate, 1)

	assert.Equal(t, "better", asm.Current().ID)
	assert.Equal(t, "base", asm.Previous().ID)

	snap := lc.Snapshot()
	assert.Equal(t, "promoted", snap.LastDecision)
}

func TestStrategyLifecycle_Submit_GateReject(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	lc, asm, _ := newTestLifecycle(t, cfg)
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	// Add a gate that always rejects.
	rejectGate := &mockGate{name: "always-reject", pass: false, reason: "too low"}
	WithLifecycleGates(rejectGate)(lc)

	candidate := &mutation.Strategy{ID: "cand", Version: 2, Score: 40.0}
	lc.Submit(context.Background(), candidate, 1)

	// Active strategy should remain "base".
	assert.Equal(t, "base", asm.Current().ID)

	snap := lc.Snapshot()
	assert.Equal(t, "active", snap.State)
}

func TestStrategyLifecycle_Submit_GatePass(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	lc, asm, _ := newTestLifecycle(t, cfg)
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	passGate := &mockGate{name: "always-pass", pass: true, reason: "ok"}
	WithLifecycleGates(passGate)(lc)

	candidate := &mutation.Strategy{ID: "better", Version: 2, Score: 80.0}
	lc.Submit(context.Background(), candidate, 1)

	assert.Equal(t, "better", asm.Current().ID)
}

func TestStrategyLifecycle_ManualApproval(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.Gates.RequireManualApproval = true

	lc, asm, _ := newTestLifecycle(t, cfg)
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	candidate := &mutation.Strategy{ID: "approved-cand", Version: 2, Score: 80.0}

	// Submit must block until Approve is called.
	approved := make(chan struct{})
	go func() {
		lc.Submit(context.Background(), candidate, 1)
		close(approved)
	}()

	// Give Submit time to enter the wait.
	time.Sleep(50 * time.Millisecond)

	// The candidate should be in SHADOW state, pending approval.
	snap := lc.Snapshot()
	assert.Equal(t, "shadow", snap.State)
	assert.True(t, snap.PendingApproval)

	// Approve should unblock Submit.
	lc.Approve()

	select {
	case <-approved:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Submit did not complete after Approve")
	}

	// After approval, the candidate should be promoted.
	assert.Equal(t, "approved-cand", asm.Current().ID)

	snap = lc.Snapshot()
	assert.False(t, snap.PendingApproval)
}

func TestStrategyLifecycle_ManualApproval_ContextCancel(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.Gates.RequireManualApproval = true

	lc, asm, _ := newTestLifecycle(t, cfg)
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	candidate := &mutation.Strategy{ID: "cand-cancel", Version: 2, Score: 80.0}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		lc.Submit(ctx, candidate, 1)
		close(done)
	}()

	// Wait for Submit to enter the wait.
	time.Sleep(50 * time.Millisecond)

	// Cancel the context → Submit should return without promoting.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Submit did not return after context cancel")
	}

	// The candidate should NOT be promoted.
	assert.Equal(t, "base", asm.Current().ID)

	// pendingApproval should be reset.
	lc.mu.Lock()
	assert.False(t, lc.pendingApproval)
	lc.mu.Unlock()
}

func TestStrategyLifecycle_WriteDecisionEvidence(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	lc, asm, store := newTestLifecycle(t, cfg)
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	candidate := &mutation.Strategy{ID: "better", Version: 2, Score: 80.0}
	lc.Submit(context.Background(), candidate, 1)

	// Check that promote evidence was written.
	evs, err := store.Query(context.Background(), evidence.Filter{
		Source: "strategy",
		Kind:   evidence.KindFitness,
		Limit:  10,
	})
	require.NoError(t, err)
	require.Len(t, evs, 1)

	// The evidence ID should contain "promote".
	assert.Contains(t, evs[0].ID, "promote")
	assert.Contains(t, evs[0].ID, "better")
}

func TestStrategyLifecycle_Approve_NoOpWhenNotPending(t *testing.T) {
	lc, _, _ := newTestLifecycle(t, DefaultLifecycleConfig())
	// Approve must be a no-op when no candidate is pending.
	assert.NotPanics(t, func() { lc.Approve() })
}

func TestStrategyLifecycle_StartStop(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	lc, _, _ := newTestLifecycle(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lc.Start(ctx)
	// Start is idempotent.
	lc.Start(ctx)

	// Stop is safe.
	lc.Stop()
	// Stop is idempotent.
	lc.Stop()
}

func TestStrategyLifecycle_Disabled(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.Enabled = false
	lc, asm, _ := newTestLifecycle(t, cfg)
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	candidate := &mutation.Strategy{ID: "cand", Version: 2, Score: 80.0}
	// Submit must be a no-op when disabled.
	lc.Submit(context.Background(), candidate, 1)
	assert.Equal(t, "base", asm.Current().ID)

	// Start must be a no-op when disabled.
	lc.Start(context.Background())
}

// --- clamp01 tests ---

func TestClamp01(t *testing.T) {
	assert.Equal(t, 0.0, clamp01(-1))
	assert.Equal(t, 0.0, clamp01(0))
	assert.Equal(t, 0.5, clamp01(0.5))
	assert.Equal(t, 1.0, clamp01(1))
	assert.Equal(t, 1.0, clamp01(2))
}

// --- mockGate ---

type mockGate struct {
	name   string
	pass   bool
	score  float64
	reason string
}

func (g *mockGate) Name() string { return g.name }
func (g *mockGate) Check(_ context.Context, _, _ *mutation.Strategy) (bool, float64, string) {
	return g.pass, g.score, g.reason
}
