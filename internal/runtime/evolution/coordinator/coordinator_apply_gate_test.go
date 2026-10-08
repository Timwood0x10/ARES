package coordinator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// mockApplyGate is a test-only ApplyGate whose verdict and reason are fixed
// by the test.
type mockApplyGate struct {
	pass   bool
	reason string
}

func (g *mockApplyGate) Check(_ context.Context, _ patch.RuntimePatch) (bool, string) {
	return g.pass, g.reason
}

// TestCoordinator_ApplyGate_RejectsAndDelays pins the contract:
// when an ApplyGate is set and returns false, the Coordinator downgrades
// DecisionApply to DecisionDelay and re-queues the proposal.
func TestCoordinator_ApplyGate_RejectsAndDelays(t *testing.T) {
	patchReg := patch.NewRegistry()
	exec := &recordingExecutor{}
	require.NoError(t, patchReg.Register("gate-test", exec))

	coord := NewUngatedPatcher(PolicyGenome{
		AutoApplyThreshold:    8,
		MaxPatchesPerMinute:   100,
		MinFitnessThreshold:   30.0,
		ApplyFitnessThreshold: 60.0,
	}, patchReg)

	coord.SetApplyGate(&mockApplyGate{pass: false, reason: "shadow evidence insufficient"})

	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "gate-test"},
		Source:   SourceGA,
		Priority: 5,
		Fitness:  80.0, // >= 60 → decide() returns Apply, but gate rejects
	})

	coord.Evaluate(context.Background())
	decisions := coord.DecisionHistory()
	require.Len(t, decisions, 1)
	assert.Equal(t, DecisionDelay, decisions[0].Decision,
		"gate rejection should downgrade Apply to Delay")
	assert.Contains(t, decisions[0].Reason, "apply gate rejected")
	assert.Contains(t, decisions[0].Reason, "shadow evidence insufficient")
	assert.Len(t, exec.applied, 0, "patch must NOT be applied when gate rejects")
	assert.Equal(t, 1, coord.PendingCount(), "proposal should be re-queued")
}

// TestCoordinator_ApplyGate_PassesAndApplies pins that when the gate passes,
// the normal apply path runs unchanged.
func TestCoordinator_ApplyGate_PassesAndApplies(t *testing.T) {
	patchReg := patch.NewRegistry()
	exec := &recordingExecutor{}
	require.NoError(t, patchReg.Register("gate-ok", exec))

	coord := NewUngatedPatcher(PolicyGenome{
		AutoApplyThreshold:    8,
		MaxPatchesPerMinute:   100,
		MinFitnessThreshold:   30.0,
		ApplyFitnessThreshold: 60.0,
	}, patchReg)

	coord.SetApplyGate(&mockApplyGate{pass: true, reason: "ok"})

	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "gate-ok"},
		Source:   SourceGA,
		Priority: 5,
		Fitness:  80.0,
	})

	coord.Evaluate(context.Background())
	decisions := coord.DecisionHistory()
	require.Len(t, decisions, 1)
	assert.Equal(t, DecisionApply, decisions[0].Decision)
	assert.Len(t, exec.applied, 1, "patch should be applied when gate passes")
}

// TestCoordinator_ApplyGate_ExhaustsRetryBudget pins the retry budget on the
// gate-rejection path: a high-fitness proposal (whose fitness bypasses the
// delay bucket's own retry check inside decide()) combined with a
// persistently rejecting gate must still be DROPPED once maxProposalRetries
// is exhausted — never re-queued forever.
func TestCoordinator_ApplyGate_ExhaustsRetryBudget(t *testing.T) {
	patchReg := patch.NewRegistry()
	exec := &recordingExecutor{}
	require.NoError(t, patchReg.Register("gate-loop", exec))

	coord := NewUngatedPatcher(PolicyGenome{
		AutoApplyThreshold:    8,
		MaxPatchesPerMinute:   100,
		MinFitnessThreshold:   30.0,
		ApplyFitnessThreshold: 60.0,
	}, patchReg)
	coord.SetApplyGate(&mockApplyGate{pass: false, reason: "shadow evidence insufficient"})

	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "gate-loop"},
		Source:   SourceGA,
		Priority: 5,
		Fitness:  80.0, // >= 60 → decide() returns Apply, gate then rejects
	})

	// Each Evaluate re-queues the rejected proposal and bumps RetryCount;
	// the loop gives the coordinator more rounds than the budget allows and
	// stops as soon as the drop is observed.
	var last Decision
	for i := 0; i <= maxProposalRetries+1; i++ {
		coord.Evaluate(context.Background())
		decisions := coord.DecisionHistory()
		last = decisions[len(decisions)-1].Decision
		if last == DecisionDrop {
			break
		}
	}
	assert.Equal(t, DecisionDrop, last,
		"a persistently gate-rejected proposal must be dropped after maxProposalRetries")
	assert.Equal(t, 0, coord.PendingCount(),
		"a dropped proposal must not stay in the queue")
	assert.Len(t, exec.applied, 0,
		"the gate rejected every round, so nothing may be applied")
}

// TestCoordinator_ApplyGate_NilGateBackwardCompatible verifies that no gate
// set means no gate checked — the original apply path runs directly.
func TestCoordinator_ApplyGate_NilGateBackwardCompatible(t *testing.T) {
	patchReg := patch.NewRegistry()
	exec := &recordingExecutor{}
	require.NoError(t, patchReg.Register("no-gate", exec))

	coord := NewUngatedPatcher(DefaultPolicy(), patchReg)
	// No SetApplyGate call — applyGate is nil.

	coord.Submit(PatchProposal{
		Patch:    patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "no-gate"},
		Source:   SourceGA,
		Priority: 5,
		Fitness:  85.0, // >= 70 default → apply
	})

	coord.Evaluate(context.Background())
	decisions := coord.DecisionHistory()
	require.Len(t, decisions, 1)
	assert.Equal(t, DecisionApply, decisions[0].Decision)
	assert.Len(t, exec.applied, 1)
}
