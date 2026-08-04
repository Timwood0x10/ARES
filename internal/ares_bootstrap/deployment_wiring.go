package ares_bootstrap

import (
	"context"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/deployment"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// deploymentStagingRuntime is a shadow runtime used by the DeploymentPipeline.
// It does NOT mutate live state — Apply records the patch on a private
// registry copy — and Evaluate returns the recent real fitness mean from the
// shared EvidenceStore (Stage 7): promotion only proceeds when observed
// workflow/scheduler fitness supports the threshold, instead of the previous
// constant 1.0 that let every patch through.
type deploymentStagingRuntime struct {
	reg           *patch.Registry
	evidenceStore *evidence.MemoryStore
	applyCount    int
}

func (r *deploymentStagingRuntime) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Shadow apply: do not touch live state, but record the patch so the
	// staging runtime's evaluation reflects what was proposed.
	if err := r.reg.Apply(ctx, p); err != nil {
		return nil, err
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

func (r *deploymentStagingRuntime) Rollback(ctx context.Context, rollback *patch.RuntimePatch) error {
	if rollback == nil || r.applyCount == 0 {
		return nil
	}
	if err := r.reg.Apply(ctx, *rollback); err != nil {
		return err
	}
	r.applyCount--
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
	_ = rec // outcome (promoted/rejected/rolled_back) is recorded inside Deploy.
	return nil
}
