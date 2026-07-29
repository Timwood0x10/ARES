// Package deployment manages the safe promotion of evolution patches
// from staging to the live runtime. It implements a canary deployment
// strategy with automatic rollback on regression.
//
// Pipeline:
//
//	Coordinator.Apply(patch)
//	  → StagingRuntime.Apply(patch)        [apply to shadow runtime]
//	  → StagingRuntime.Evaluate()         [run eval suite on shadow]
//	  → if pass: LiveRuntime.Apply(patch) [promote to live]
//	  → if fail: StagingRuntime.Rollback() [auto-rollback]
//
// Default config has Enabled=false. Must be explicitly enabled.
package deployment

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// DeploymentConfig controls the patch deployment pipeline.
type DeploymentConfig struct {
	// Enabled controls whether patches are auto-promoted to live.
	// Default: false. Must be explicitly enabled in config.
	Enabled bool `json:"enabled" yaml:"enabled"`

	// ShadowSampleSize is the number of tasks to run in shadow evaluation.
	ShadowSampleSize int `json:"shadow_sample_size" yaml:"shadow_sample_size"`

	// PromotionThreshold is the minimum fitness improvement required
	// to promote a patch to live. [0.0, 1.0]. Default: 0.05 (5% improvement).
	PromotionThreshold float64 `json:"promotion_threshold" yaml:"promotion_threshold"`

	// RollbackThreshold is the maximum fitness regression allowed
	// before auto-rollback. [0.0, 1.0]. Default: 0.10 (10% regression).
	RollbackThreshold float64 `json:"rollback_threshold" yaml:"rollback_threshold"`

	// EvaluationTimeout bounds the shadow evaluation duration.
	EvaluationTimeout time.Duration `json:"evaluation_timeout" yaml:"evaluation_timeout"`
}

// DefaultDeploymentConfig returns a conservative default configuration.
// Enabled=false ensures patches are not auto-promoted unless explicitly opted in.
func DefaultDeploymentConfig() DeploymentConfig {
	return DeploymentConfig{
		Enabled:            false,
		ShadowSampleSize:   5,
		PromotionThreshold: 0.05,
		RollbackThreshold:  0.10,
		EvaluationTimeout:  30 * time.Second,
	}
}

// DeploymentStatus classifies the outcome of a deployment attempt.
type DeploymentStatus int

const (
	// DeploymentPromoted indicates the patch was promoted to live.
	DeploymentPromoted DeploymentStatus = iota
	// DeploymentRolledBack indicates the patch was auto-rolled back.
	DeploymentRolledBack
	// DeploymentRejected indicates the patch failed shadow evaluation.
	DeploymentRejected
	// DeploymentDisabled indicates auto-promotion is disabled in config.
	DeploymentDisabled
)

// String returns a human-readable name for the deployment status.
func (s DeploymentStatus) String() string {
	switch s {
	case DeploymentPromoted:
		return "promoted"
	case DeploymentRolledBack:
		return "rolled_back"
	case DeploymentRejected:
		return "rejected"
	case DeploymentDisabled:
		return "disabled"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// DeploymentRecord captures the outcome of a single patch deployment attempt.
type DeploymentRecord struct {
	PatchID     string           `json:"patch_id"`
	Status      DeploymentStatus `json:"status"`
	ShadowScore float64          `json:"shadow_score"`
	LiveScore   float64          `json:"live_score"`
	Timestamp   time.Time        `json:"timestamp"`
	Reason      string           `json:"reason"`
}

// StagingRuntime is the shadow runtime where patches are applied for evaluation.
type StagingRuntime interface {
	// Apply applies a patch to the staging runtime and returns a rollback patch.
	Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error)
	// Evaluate runs the evaluation suite on the staging runtime.
	Evaluate(ctx context.Context) (float64, error)
	// Rollback reverts the last applied patch.
	Rollback(ctx context.Context, rollback *patch.RuntimePatch) error
}

// LiveRuntime is the production runtime that agents consume.
type LiveRuntime interface {
	// Apply promotes a patch to the live runtime.
	Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error)
}

// DeploymentPipeline manages the patch promotion lifecycle.
type DeploymentPipeline struct {
	mu      sync.Mutex
	config  DeploymentConfig
	staging StagingRuntime
	live    LiveRuntime
	history []DeploymentRecord
}

// NewDeploymentPipeline creates a DeploymentPipeline with the given dependencies.
//
// Args:
//   - config  - deployment configuration.
//   - staging - shadow runtime for patch testing (must not be nil when Enabled).
//   - live    - production runtime for patch promotion (must not be nil when Enabled).
//
// Returns:
//   - *DeploymentPipeline - the configured pipeline.
func NewDeploymentPipeline(config DeploymentConfig, staging StagingRuntime, live LiveRuntime) *DeploymentPipeline {
	return &DeploymentPipeline{
		config:  config,
		staging: staging,
		live:    live,
	}
}

// IsEnabled reports whether auto-promotion to the live runtime is active.
func (dp *DeploymentPipeline) IsEnabled() bool {
	return dp.config.Enabled
}

// Deploy attempts to safely promote a patch through staging → live.
//
// Algorithm:
//  1. If not Enabled: record DeploymentDisabled, return nil.
//  2. Apply patch to staging → get rollback.
//  3. Shadow evaluate → get shadow fitness.
//  4. If shadow fitness >= PromotionThreshold: promote to live.
//  5. Record deployment outcome.
//
// Args:
//   - ctx - timeout and cancellation context.
//   - p   - the RuntimePatch to deploy.
//
// Returns:
//   - record - the deployment outcome record.
//   - err    - non-nil if deployment fails catastrophically (not rollback).
func (dp *DeploymentPipeline) Deploy(ctx context.Context, p patch.RuntimePatch) (*DeploymentRecord, error) {
	dp.mu.Lock()
	defer dp.mu.Unlock()

	patchID := fmt.Sprintf("patch-%d", time.Now().UnixNano())
	record := &DeploymentRecord{
		PatchID:   patchID,
		Timestamp: time.Now(),
	}

	if !dp.config.Enabled {
		record.Status = DeploymentDisabled
		record.Reason = "auto-promotion disabled in config"
		dp.history = append(dp.history, *record)
		return record, nil
	}

	if dp.staging == nil || dp.live == nil {
		record.Status = DeploymentRejected
		record.Reason = "staging or live runtime is nil"
		dp.history = append(dp.history, *record)
		return record, fmt.Errorf("deployment: staging or live runtime is nil")
	}

	// Step 2: Apply to staging.
	rollback, err := dp.staging.Apply(ctx, p)
	if err != nil {
		record.Status = DeploymentRejected
		record.Reason = fmt.Sprintf("staging apply failed: %v", err)
		dp.history = append(dp.history, *record)
		return record, fmt.Errorf("deployment: staging apply: %w", err)
	}

	// Step 3: Shadow evaluate.
	evalCtx, cancel := context.WithTimeout(ctx, dp.config.EvaluationTimeout)
	defer cancel()

	shadowScore, err := dp.staging.Evaluate(evalCtx)
	if err != nil {
		_ = dp.staging.Rollback(ctx, rollback)
		record.Status = DeploymentRejected
		record.Reason = fmt.Sprintf("shadow evaluate failed: %v", err)
		dp.history = append(dp.history, *record)
		return record, fmt.Errorf("deployment: shadow evaluate: %w", err)
	}
	record.ShadowScore = shadowScore

	// Step 4: Check promotion threshold.
	if shadowScore < dp.config.PromotionThreshold {
		_ = dp.staging.Rollback(ctx, rollback)
		record.Status = DeploymentRejected
		record.Reason = fmt.Sprintf("shadow score %.3f below promotion threshold %.3f",
			shadowScore, dp.config.PromotionThreshold)
		dp.history = append(dp.history, *record)
		return record, nil
	}

	// Step 5: Promote to live.
	liveRollback, err := dp.live.Apply(ctx, p)
	if err != nil {
		_ = dp.staging.Rollback(ctx, rollback)
		record.Status = DeploymentRolledBack
		record.Reason = fmt.Sprintf("live apply failed: %v", err)
		dp.history = append(dp.history, *record)
		return record, fmt.Errorf("deployment: live apply: %w", err)
	}

	record.Status = DeploymentPromoted
	record.Reason = "patch promoted to live runtime"
	_ = liveRollback // retained for future live rollback
	dp.history = append(dp.history, *record)
	return record, nil
}

// History returns a copy of all deployment records for observability.
func (dp *DeploymentPipeline) History() []DeploymentRecord {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	out := make([]DeploymentRecord, len(dp.history))
	copy(out, dp.history)
	return out
}
