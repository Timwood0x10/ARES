package ares_bootstrap

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/deployment"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// deploymentStagingRuntime is a shadow runtime used by the DeploymentPipeline.
// It NEVER mutates live state — Apply is a read-only preflight (the patch must
// have a registered executor) and Evaluate returns the recent real fitness
// mean from the shared EvidenceStore (Stage 7): promotion only proceeds when
// observed workflow/scheduler fitness supports the threshold.
//
// History: this struct previously called r.reg.Apply on the SAME *patch.Registry
// the live runtime uses. Staging therefore mutated production state (memory
// config, knowledge runtime, DAG recovery policies) for patches that were then
// REJECTED, and its "rollback" re-applied the identical patch instead of an
// inverse. For ID-bearing patches the staging apply also poisoned the shared
// idempotency map, so the later live promotion silently no-op'd.
type deploymentStagingRuntime struct {
	reg           *patch.Registry
	evidenceStore evidence.Store
	applyCount    int
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

func (r *deploymentStagingRuntime) Evaluate(ctx context.Context) (float64, error) {
	// Stage 7: score against real observed fitness instead of a nominal 1.0.
	// When no fitness evidence exists yet, return 0.0 so the pipeline rejects
	// the patch (no fabricated pass). The mean across workflow fitness is used
	// as the shadow score; scheduler/recovery can be added when their genomes
	// produce fitness for the same window.
	if r.evidenceStore == nil {
		return 0.0, nil
	}
	mean, count, ok := recentFitnessSummary(ctx, r.evidenceStore, "workflow", 50)
	if !ok || count == 0 {
		return 0.0, nil
	}
	return mean, nil
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
