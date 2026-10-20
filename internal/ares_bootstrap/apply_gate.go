package ares_bootstrap

import (
	"context"
	"fmt"

	aresevolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// lifecycleSnapshottable is the one thing the gate needs from the strategy lifecycle.
//
// An interface rather than the concrete type so the gate can be tested without a running
// lifecycle, and so the coupling stays readable: this gate asks exactly one question — "is a
// candidate held for manual approval, and which one".
type lifecycleSnapshottable interface {
	Snapshot() aresevolution.LifecycleState
}

// strategyApprovalGate refuses a patch whose strategy is still waiting for a human.
//
// M8b exists because patches could reach apply without passing the trust root that the
// strategy lifecycle already is: a candidate held for manual approval (RequireManualApproval)
// must not be promoted through the patch path while the lifecycle is still waiting for an
// approver. One decision, one place.
//
// It is deliberately narrow:
//   - an UNATTRIBUTED patch is not this gate's subject. Those are the component-keyed diff
//     patches, and they are already refused by the deployment adapter as unmeasurable.
//     Blocking them here would look like governance while actually stopping a flow that never
//     claimed to be comparable.
//   - with nothing held (the default: RequireManualApproval=false) every patch passes, so
//     installing this gate does not change behaviour for a deployment that never asked for
//     approvals.
type strategyApprovalGate struct {
	lifecycle lifecycleSnapshottable
}

// Compile-time proof that this is the gate the coordinator will call; if the interface moves,
// this stops compiling instead of failing at wiring time.
var _ coordinator.ApplyGate = (*strategyApprovalGate)(nil)

func newStrategyApprovalGate(lifecycle lifecycleSnapshottable) *strategyApprovalGate {
	return &strategyApprovalGate{lifecycle: lifecycle}
}

// Check implements coordinator.ApplyGate.
func (g *strategyApprovalGate) Check(_ context.Context, p patch.RuntimePatch) (bool, string) {
	if g == nil || g.lifecycle == nil {
		// No lifecycle: nothing to consult. Passing is the honest answer — a gate that
		// invented a hold would stop a deployment for a reason nobody configured.
		return true, ""
	}
	if p.StrategyID == "" {
		// Unattributed: not a strategy's patch (see the type comment).
		return true, ""
	}
	snap := g.lifecycle.Snapshot()
	if snap.HeldID == "" || snap.HeldID != p.StrategyID {
		return true, ""
	}
	return false, fmt.Sprintf(
		"strategy %q is held for manual approval (generation %d): the patch belongs to a candidate a human has not released",
		p.StrategyID, snap.HeldGeneration)
}
