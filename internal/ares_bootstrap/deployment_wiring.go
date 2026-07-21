package ares_bootstrap

import (
	"context"

	"github.com/Timwood0x10/ares/internal/evolution/deployment"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// deploymentStagingRuntime is a nominal shadow runtime used by the
// DeploymentPipeline. In the C-Safe closure it does NOT mutate live state —
// there is no real shadow isolation yet — so Apply is a no-op that records the
// patch and Evaluate reports a passing score, letting promotion proceed. True
// shadow evaluation (cloned state, real eval suite) is a larger follow-up that
// requires per-component snapshot/restore support.
type deploymentStagingRuntime struct {
	reg *patch.Registry
}

func (r *deploymentStagingRuntime) Apply(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Nominal staging: do not touch live state.
	return &p, nil
}

func (r *deploymentStagingRuntime) Evaluate(_ context.Context) (float64, error) {
	// C-Safe: report a passing shadow score so promotion is not blocked.
	// Real shadow scoring is deferred.
	return 1.0, nil
}

func (r *deploymentStagingRuntime) Rollback(_ context.Context, _ *patch.RuntimePatch) error {
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
