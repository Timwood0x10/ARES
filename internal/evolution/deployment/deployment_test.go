package deployment

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStaging is a test double for StagingRuntime.
type fakeStaging struct {
	applyErr      error
	evaluateErr   error
	rollbackErr   error
	shadowScore   float64
	applyCalls    int
	evalCalls     int
	rollbackCalls int
}

func (s *fakeStaging) Apply(_ context.Context, _ patch.RuntimePatch) (*patch.RuntimePatch, error) {
	s.applyCalls++
	if s.applyErr != nil {
		return nil, s.applyErr
	}
	return &patch.RuntimePatch{Type: patch.PatchChangePlanner, Target: "rollback"}, nil
}

func (s *fakeStaging) Evaluate(_ context.Context) (float64, error) {
	s.evalCalls++
	if s.evaluateErr != nil {
		return 0, s.evaluateErr
	}
	return s.shadowScore, nil
}

func (s *fakeStaging) Rollback(_ context.Context, _ *patch.RuntimePatch) error {
	s.rollbackCalls++
	return s.rollbackErr
}

// fakeLive is a test double for LiveRuntime.
type fakeLive struct {
	applyErr   error
	applyCalls int
}

func (l *fakeLive) Apply(_ context.Context, _ patch.RuntimePatch) (*patch.RuntimePatch, error) {
	l.applyCalls++
	if l.applyErr != nil {
		return nil, l.applyErr
	}
	return &patch.RuntimePatch{Type: patch.PatchChangePlanner, Target: "rollback"}, nil
}

// TestDeploy_DisabledReturnsDisabled verifies that when Enabled=false,
// the pipeline records DeploymentDisabled and does not touch staging/live.
func TestDeploy_DisabledReturnsDisabled(t *testing.T) {
	dp := NewDeploymentPipeline(DeploymentConfig{Enabled: false}, nil, nil)
	rec, err := dp.Deploy(context.Background(), patch.RuntimePatch{Target: "memory"})
	require.NoError(t, err)
	assert.Equal(t, DeploymentDisabled, rec.Status)
}

// TestDeploy_PromotionPasses verifies the happy path: staging apply →
// shadow eval ≥ threshold → live apply.
func TestDeploy_PromotionPasses(t *testing.T) {
	staging := &fakeStaging{shadowScore: 0.80}
	live := &fakeLive{}
	dp := NewDeploymentPipeline(DeploymentConfig{
		Enabled:            true,
		PromotionThreshold: 0.50,
		EvaluationTimeout:  0,
	}, staging, live)

	rec, err := dp.Deploy(context.Background(), patch.RuntimePatch{Target: "memory"})
	require.NoError(t, err)
	assert.Equal(t, DeploymentPromoted, rec.Status)
	assert.Equal(t, 0.80, rec.ShadowScore)
	assert.Equal(t, 1, staging.applyCalls)
	assert.Equal(t, 1, staging.evalCalls)
	assert.Equal(t, 1, live.applyCalls)
}

// TestDeploy_BelowThresholdRollsBackStaging verifies that a shadow score
// below PromotionThreshold rejects the patch and rolls back staging.
func TestDeploy_BelowThresholdRollsBackStaging(t *testing.T) {
	staging := &fakeStaging{shadowScore: 0.01}
	live := &fakeLive{}
	dp := NewDeploymentPipeline(DeploymentConfig{
		Enabled:            true,
		PromotionThreshold: 0.50,
		EvaluationTimeout:  0,
	}, staging, live)

	rec, err := dp.Deploy(context.Background(), patch.RuntimePatch{Target: "memory"})
	require.NoError(t, err)
	assert.Equal(t, DeploymentRejected, rec.Status)
	assert.Equal(t, 1, staging.rollbackCalls, "staging should be rolled back on rejection")
	assert.Equal(t, 0, live.applyCalls, "live should not be touched on rejection")
}

// TestDeploy_StagingApplyFails verifies that a staging apply error rejects
// the patch without touching live.
func TestDeploy_StagingApplyFails(t *testing.T) {
	staging := &fakeStaging{applyErr: errors.New("staging boom")}
	live := &fakeLive{}
	dp := NewDeploymentPipeline(DeploymentConfig{Enabled: true, EvaluationTimeout: 0}, staging, live)

	rec, err := dp.Deploy(context.Background(), patch.RuntimePatch{Target: "memory"})
	require.Error(t, err)
	assert.Equal(t, DeploymentRejected, rec.Status)
	assert.Equal(t, 0, live.applyCalls)
}

// TestDeploy_ShadowEvalFails verifies that a shadow eval error rolls back
// staging and returns an error.
func TestDeploy_ShadowEvalFails(t *testing.T) {
	staging := &fakeStaging{evaluateErr: errors.New("eval boom")}
	live := &fakeLive{}
	dp := NewDeploymentPipeline(DeploymentConfig{Enabled: true, EvaluationTimeout: 0}, staging, live)

	rec, err := dp.Deploy(context.Background(), patch.RuntimePatch{Target: "memory"})
	require.Error(t, err)
	assert.Equal(t, DeploymentRejected, rec.Status)
	assert.Equal(t, 1, staging.rollbackCalls, "staging should be rolled back on eval failure")
}

// TestDeploy_LiveApplyFails verifies that a live apply error rolls back
// staging and marks the deployment as rolled back.
func TestDeploy_LiveApplyFails(t *testing.T) {
	staging := &fakeStaging{shadowScore: 0.80}
	live := &fakeLive{applyErr: errors.New("live boom")}
	dp := NewDeploymentPipeline(DeploymentConfig{
		Enabled:            true,
		PromotionThreshold: 0.50,
		EvaluationTimeout:  0,
	}, staging, live)

	rec, err := dp.Deploy(context.Background(), patch.RuntimePatch{Target: "memory"})
	require.Error(t, err)
	assert.Equal(t, DeploymentRolledBack, rec.Status)
	assert.Equal(t, 1, staging.rollbackCalls, "staging should be rolled back on live failure")
}

// TestHistory_RecordsAllDeployments verifies that History returns all
// deployment records in order.
func TestHistory_RecordsAllDeployments(t *testing.T) {
	dp := NewDeploymentPipeline(DeploymentConfig{Enabled: false}, nil, nil)
	for i := 0; i < 3; i++ {
		_, _ = dp.Deploy(context.Background(), patch.RuntimePatch{Target: "memory"})
	}
	history := dp.History()
	assert.Len(t, history, 3)
	for _, rec := range history {
		assert.Equal(t, DeploymentDisabled, rec.Status)
	}
}
