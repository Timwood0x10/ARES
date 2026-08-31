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
// B6 fix: Evaluate now aggregates fitness from multiple evidence sources
// (workflow, scheduler, recovery, strategy) instead of only "workflow".
// When no evidence exists across all sources, it returns ColdStartScore
// (default 0.5, configurable) so cold-start patches are not universally
// rejected — the caller decides the conservative policy.
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
	// coldStartScore is the fitness returned when no evidence exists across
	// any source (B6 fix). Default 0.5 — conservative but not a universal reject.
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

// stagingFitnessSources are the evidence sources whose fitness is aggregated
// to score a staging patch. B6 fix: previously only "workflow" was queried,
// causing cold-start patches to be universally rejected (score 0.0).
var stagingFitnessSources = []string{"workflow", "scheduler", "recovery", "strategy"}

func (r *deploymentStagingRuntime) Evaluate(ctx context.Context) (float64, error) {
	// B6 fix: aggregate fitness from multiple evidence sources instead of
	// only "workflow". When no evidence exists across any source, return
	// ColdStartScore so cold-start patches are not universally rejected.
	if r.evidenceStore == nil {
		return r.coldStartScore, nil
	}
	var sum float64
	var count int
	for _, src := range stagingFitnessSources {
		mean, c, ok := recentFitnessSummary(ctx, r.evidenceStore, src, fitnessWindowSize)
		if !ok || c == 0 {
			continue
		}
		sum += mean
		count++
	}
	if count == 0 {
		return r.coldStartScore, nil
	}
	return sum / float64(count), nil
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
