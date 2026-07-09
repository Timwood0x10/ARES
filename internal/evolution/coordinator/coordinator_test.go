package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── PatchSource constants ───────────────────

func TestPatchSourceConstants(t *testing.T) {
	assert.Equal(t, PatchSource("genome"), SourceGA)
	assert.Equal(t, PatchSource("chaos"), SourceChaos)
	assert.Equal(t, PatchSource("akf"), SourceAKF)
	assert.Equal(t, PatchSource("human"), SourceHuman)
	assert.Equal(t, PatchSource("llm"), SourceLLM)
	assert.Equal(t, PatchSource("k8s"), SourceK8s)
	assert.Equal(t, PatchSource("rule"), SourceRule)
}

// ── Decision ────────────────────────────────

func TestDecision_String(t *testing.T) {
	assert.Equal(t, "apply", DecisionApply.String())
	assert.Equal(t, "reject", DecisionReject.String())
	assert.Equal(t, "delay", DecisionDelay.String())
}

// ── Policy ──────────────────────────────────

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	assert.Equal(t, 8, p.AutoApplyThreshold)
	assert.Equal(t, 4, p.MaxPatchesPerMinute)
	assert.False(t, p.RequireMultiSource)
}

// ── EvolutionCoordinator ────────────────────

func TestNewEvolutionCoordinator(t *testing.T) {
	patchReg := patch.NewRegistry()
	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)
	require.NotNil(t, coord)
	assert.Equal(t, 0, coord.PendingCount())
}

func TestCoordinator_Submit(t *testing.T) {
	patchReg := patch.NewRegistry()
	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)

	coord.Submit(PatchProposal{
		Patch:     patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "test"},
		Source:    SourceGA,
		Reason:    "test",
		Priority:  5,
		Timestamp: time.Now(),
	})
	assert.Equal(t, 1, coord.PendingCount())
}

func TestCoordinator_Submit_Multiple(t *testing.T) {
	patchReg := patch.NewRegistry()
	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)

	for i := 0; i < 5; i++ {
		coord.Submit(PatchProposal{
			Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "test"},
			Source:   SourceGA,
			Reason:   "test",
			Priority: i,
		})
	}
	assert.Equal(t, 5, coord.PendingCount())
}

// ── Evaluation ──────────────────────────────

func TestCoordinator_Evaluate_AppliesPatches(t *testing.T) {
	patchReg := patch.NewRegistry()
	exec := &recordingExecutor{}
	require.NoError(t, patchReg.Register("test-target", exec))

	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)
	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "test-target"},
		Source:   SourceGA,
		Reason:   "test",
		Priority: 5,
	})

	coord.Evaluate(context.Background())
	assert.Equal(t, 0, coord.PendingCount())
	assert.Len(t, exec.applied, 1)
}

func TestCoordinator_Evaluate_RejectsBlacklistedSource(t *testing.T) {
	patchReg := patch.NewRegistry()
	coord := NewEvolutionCoordinator(PolicyGenome{
		Blacklist: []PatchSource{SourceChaos},
	}, patchReg)

	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "test"},
		Source:   SourceChaos,
		Reason:   "chaos test",
		Priority: 10,
	})

	coord.Evaluate(context.Background())
	decisions := coord.DecisionHistory()
	require.Len(t, decisions, 1)
	assert.Equal(t, DecisionReject, decisions[0].Decision)
}

func TestCoordinator_Evaluate_AutoApplyHighPriority(t *testing.T) {
	patchReg := patch.NewRegistry()
	exec := &recordingExecutor{}
	require.NoError(t, patchReg.Register("urgent", exec))

	coord := NewEvolutionCoordinator(PolicyGenome{AutoApplyThreshold: 8, MaxPatchesPerMinute: 100}, patchReg)

	// Priority 10 >= threshold 8 → auto-apply.
	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "urgent"},
		Source:   SourceChaos,
		Priority: 10,
	})

	coord.Evaluate(context.Background())
	assert.Len(t, exec.applied, 1)
}

func TestCoordinator_Evaluate_DelaysOnRateLimit(t *testing.T) {
	patchReg := patch.NewRegistry()
	exec := &recordingExecutor{}
	require.NoError(t, patchReg.Register("rate-test", exec))

	coord := NewEvolutionCoordinator(PolicyGenome{MaxPatchesPerMinute: 0}, patchReg)

	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "rate-test"},
		Priority: 1,
	})

	coord.Evaluate(context.Background())
	decisions := coord.DecisionHistory()
	require.Len(t, decisions, 1)
	assert.Equal(t, DecisionDelay, decisions[0].Decision,
		"should delay when rate limit is 0")
}

func TestCoordinator_Policy(t *testing.T) {
	patchReg := patch.NewRegistry()
	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)

	p := coord.Policy()
	assert.Equal(t, 4, p.MaxPatchesPerMinute)
}

func TestCoordinator_UpdatePolicy(t *testing.T) {
	patchReg := patch.NewRegistry()
	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)

	newPolicy := PolicyGenome{AutoApplyThreshold: 3}
	coord.UpdatePolicy(newPolicy)

	p := coord.Policy()
	assert.Equal(t, 3, p.AutoApplyThreshold)
}

func TestCoordinator_DecisionHistory(t *testing.T) {
	patchReg := patch.NewRegistry()
	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)

	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "t"},
		Source:   SourceGA,
		Priority: 5,
	})
	coord.Evaluate(context.Background())

	assert.Len(t, coord.DecisionHistory(), 1)
}

func TestCoordinator_PatchHistory(t *testing.T) {
	patchReg := patch.NewRegistry()
	exec := &recordingExecutor{}
	require.NoError(t, patchReg.Register("test", exec))

	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)
	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "test"},
		Priority: 5,
	})
	coord.Evaluate(context.Background())

	assert.Len(t, coord.PatchHistory(), 1)
}

// ── Run (non-blocking test) ─────────────────

func TestCoordinator_Run_ContextCancellation(t *testing.T) {
	patchReg := patch.NewRegistry()
	coord := NewEvolutionCoordinator(DefaultPolicy(), patchReg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediate cancellation

	// Run should return immediately when context is cancelled.
	done := make(chan struct{})
	go func() {
		coord.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// OK — exited cleanly.
	case <-time.After(time.Second):
		t.Fatal("coordinator.Run did not exit after context cancellation")
	}
}

// ── Mock executor ───────────────────────────

type recordingExecutor struct {
	applied []patch.RuntimePatch
}

func (e *recordingExecutor) Apply(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	e.applied = append(e.applied, p)
	return &patch.RuntimePatch{Type: patch.PatchRemoveNode, Target: p.Target}, nil
}

func (e *recordingExecutor) CanApply(_ context.Context, _ patch.RuntimePatch) error { return nil }
