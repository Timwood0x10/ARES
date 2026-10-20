package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	aresevolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// fakeLifecycle answers the one question the gate asks.
type fakeLifecycle struct{ held string }

func (f fakeLifecycle) Snapshot() aresevolution.LifecycleState {
	return aresevolution.LifecycleState{HeldID: f.held, HeldGeneration: 3}
}

// TestStrategyApprovalGate_RefusesAPatchForAHeldStrategy is M8b's reason to exist: a candidate
// waiting for a human must not be promoted through the patch path.
func TestStrategyApprovalGate_RefusesAPatchForAHeldStrategy(t *testing.T) {
	gate := newStrategyApprovalGate(fakeLifecycle{held: "strategy-1"})

	ok, reason := gate.Check(context.Background(), patch.RuntimePatch{StrategyID: "strategy-1"})
	assert.False(t, ok, "a patch belonging to the held candidate must not apply")
	assert.Contains(t, reason, "held for manual approval")
	assert.Contains(t, reason, "strategy-1")
	assert.Contains(t, reason, "generation 3", "the reason must say WHICH generation is waiting")
}

// TestStrategyApprovalGate_PassesWhatItIsNotResponsibleFor pins the narrowness: the gate owns
// exactly one decision, and widening it here would stop flows that never claimed to be
// comparable.
func TestStrategyApprovalGate_PassesWhatItIsNotResponsibleFor(t *testing.T) {
	gate := newStrategyApprovalGate(fakeLifecycle{held: "strategy-1"})
	ctx := context.Background()

	ok, _ := gate.Check(ctx, patch.RuntimePatch{StrategyID: "strategy-2"})
	assert.True(t, ok, "a different strategy is not the held one")

	ok, _ = gate.Check(ctx, patch.RuntimePatch{})
	assert.True(t, ok,
		"an unattributed patch is not this gate's subject: the deployment adapter already refuses it as unmeasurable")

	// Nothing held is the DEFAULT (RequireManualApproval=false): installing the gate must not
	// change behaviour for a deployment that never asked for approvals.
	open := newStrategyApprovalGate(fakeLifecycle{})
	ok, _ = open.Check(ctx, patch.RuntimePatch{StrategyID: "strategy-1"})
	assert.True(t, ok, "with nothing held, every patch passes")

	// No lifecycle at all: passing is the honest answer — a gate that invented a hold would
	// stop a deployment for a reason nobody configured.
	none := newStrategyApprovalGate(nil)
	ok, _ = none.Check(ctx, patch.RuntimePatch{StrategyID: "strategy-1"})
	assert.True(t, ok)
}
