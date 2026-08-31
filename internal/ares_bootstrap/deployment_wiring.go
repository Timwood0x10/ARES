package ares_bootstrap

import (
	"context"
	"fmt"

	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/evolution/deployment"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// deploymentStagingRuntime is a shadow runtime used by the DeploymentPipeline.
// It NEVER mutates live state — Apply is a read-only preflight (the patch must
// have a registered executor) and Evaluate returns the recent real fitness
// mean from the shared EvidenceStore (Stage 7): promotion only proceeds when
// observed fitness supports the threshold.
//
// B6 fix: Evaluate aggregates fitness from multiple evidence sources via the
// SHARED RuntimeFitnessAggregator (the same scoring backend the lifecycle's
// rollback window uses) instead of a local equal-weight mean. Two aggregation
// semantics for one "shared scoring backend" was a latent disagreement: the
// local mean treated all sources equally while the aggregator applies
// configured per-source weights.
//
// Cold start: when no evidence exists in any source (Window count == 0),
// Evaluate returns coldStartScore. There is NO implicit default — bootstrap
// sets it explicitly (0.5) at construction. A zero-valued struct yields 0.0,
// which means "reject everything without evidence"; that is a deliberate
// construction-site choice, not a hidden default (the old doc comment claimed
// "Default 0.5" while the zero value was 0.0 — the claim was false).
//
// History: this struct previously called r.reg.Apply on the SAME *patch.Registry
// the live runtime uses. Staging therefore mutated production state (memory
// config, knowledge runtime, DAG recovery policies) for patches that were then
// REJECTED, and its "rollback" re-applied the identical patch instead of an
// inverse. For ID-bearing patches the staging apply also poisoned the shared
// idempotency map, so the later live promotion silently no-op'd.
type deploymentStagingRuntime struct {
	reg *patch.Registry
	// agg is the shared fitness scoring backend. Nil means "no evidence
	// backend wired" → Evaluate always returns coldStartScore.
	agg            *evolution.RuntimeFitnessAggregator
	applyCount     int
	coldStartScore float64
}

func (r *deploymentStagingRuntime) Apply(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Preflight only: reject patches no executor can handle (same rejection
	// class as before), but do NOT touch any registry state.
	if !r.reg.CanApply(p.Target) {
		return nil, fmt.Errorf("staging preflight: no executor registered for target %q", p.Target)
	}
	r.applyCount++
	return &p, nil
}

// Evaluate scores the current runtime state via the shared fitness
// aggregator. The aggregator's MinSamplesBeforeJudge is deliberately NOT
// enforced here: partial evidence (any count > 0) still yields the weighted
// mean — only a completely empty store falls back to coldStartScore. This
// preserves the pre-existing staging contract ("some evidence beats a
// nominal score") while inheriting the aggregator's per-source weights and
// [0,1] value filter.
func (r *deploymentStagingRuntime) Evaluate(ctx context.Context) (float64, error) {
	if r.agg == nil {
		return r.coldStartScore, nil
	}
	res := r.agg.Window(ctx, "")
	if res.Count == 0 {
		return r.coldStartScore, nil
	}
	return res.Mean, nil
}

func (r *deploymentStagingRuntime) Rollback(_ context.Context, _ *patch.RuntimePatch) error {
	// Nothing was applied to any registry during staging, so there is nothing
	// to roll back. The counter keeps the pipeline's bookkeeping honest.
	if r.applyCount > 0 {
		r.applyCount--
	}
	return nil
}

// deploymentLiveRuntime promotes a patch to the real executor registry, which
// applies it to the actual components: memory patches are written to the live
// comp.Memory; workflow/scheduler/recovery/knowledge patches are written to
// their (currently synthetic) executors. This is the genuine "deploy to
// production" step — it is exactly what the Coordinator did before, now routed
// through the deployment pipeline.
type deploymentLiveRuntime struct {
	reg *patch.Registry
}

func (r *deploymentLiveRuntime) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	if err := r.reg.Apply(ctx, p); err != nil {
		return nil, err
	}
	return &p, nil
}

// deploymentAdapter bridges the deployment.DeploymentPipeline to the
// Coordinator's PatchDeployer interface. Only catastrophic failures surface as
// errors; a normal reject/rollback is reported by the pipeline and treated as
// handled here.
type deploymentAdapter struct {
	dp *deployment.DeploymentPipeline
}

func (a *deploymentAdapter) Enabled() bool {
	return a.dp != nil && a.dp.IsEnabled()
}

func (a *deploymentAdapter) Deploy(ctx context.Context, p patch.RuntimePatch) error {
	rec, err := a.dp.Deploy(ctx, p)
	if err != nil {
		return err
	}
	// A pipeline REJECTION (shadow score below threshold) or ROLLBACK is a
	// normal, non-error return of Deploy — but the Coordinator treats a nil
	// error as "applied successfully" and records PatchResult{Error: nil} in
	// its decision history. Translate the outcome so the operator-facing
	// history reflects reality: only DeploymentPromoted counts as success.
	if rec != nil && rec.Status != deployment.DeploymentPromoted {
		return fmt.Errorf("deployment not applied (status %s): %s", rec.Status, rec.Reason)
	}
	return nil
}
