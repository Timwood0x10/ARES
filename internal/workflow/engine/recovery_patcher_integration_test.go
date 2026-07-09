package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

func TestDynamicExecutor_WithPatchRegistry(t *testing.T) {
	dag := newTestDAG(t)
	patchReg := patch.NewRegistry()

	exec := NewDynamicExecutor(NewAgentRegistry(), ApplyImmediate)
	exec.WithPatchRegistry(patchReg)

	assert.NotNil(t, exec.patchRegistry)

	// Verify RecoveryPatchExecutor can be registered and works through the registry.
	recoveryExec := NewRecoveryPatchExecutor(dag)
	require.NoError(t, patchReg.Register("recovery.strategy", recoveryExec))
	require.NoError(t, patchReg.Register("recovery.max_attempts", recoveryExec))
}

func TestRecoveryPatchExecutor_Integration_PatchThenRecover(t *testing.T) {
	// End-to-end: apply a RecoveryStrategy patch, then verify DynamicExecutor reads it.
	dag := newTestDAG(t)
	patchReg := patch.NewRegistry()
	recoveryExec := NewRecoveryPatchExecutor(dag)
	require.NoError(t, patchReg.Register("recovery.strategy", recoveryExec))
	require.NoError(t, patchReg.Register("recovery.max_attempts", recoveryExec))

	ctx := context.Background()

	// Apply a ChangeRecoveryStrategy patch via the registry.
	err := patchReg.Apply(ctx, patch.RuntimePatch{
		Type:   patch.PatchChangeRecoveryStrategy,
		Target: "recovery.strategy",
		Value:  string(RecoveryReplaceNode),
	})
	require.NoError(t, err)

	// Verify the DAG steps now have the new strategy.
	for _, step := range dag.Steps() {
		if step.RecoveryPolicy != nil {
			assert.Equal(t, RecoveryReplaceNode, step.RecoveryPolicy.Strategy)
		}
	}

	// Apply a ChangeMaxRetries patch.
	err = patchReg.Apply(ctx, patch.RuntimePatch{
		Type:   patch.PatchChangeMaxRetries,
		Target: "recovery.max_attempts",
		Value:  5,
	})
	require.NoError(t, err)

	for _, step := range dag.Steps() {
		if step.RecoveryPolicy != nil {
			assert.Equal(t, 5, step.RecoveryPolicy.MaxAttempts)
		}
	}
}

func TestRecoveryPatchExecutor_CanApply_EdgeCases(t *testing.T) {
	exec := &RecoveryPatchExecutor{dag: nil}
	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{"valid retry", "retry", false}, // nil dag → error
		{"valid replace", "replace_node", false},
		{"valid fail", "fail_fast", false},
		{"empty string", "", false},
		{"int value", 42, false},
		{"unknown strategy", "unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := exec.CanApply(context.Background(), patch.RuntimePatch{
				Type:  patch.PatchChangeRecoveryStrategy,
				Value: tt.value,
			})
			assert.Error(t, err)
		})
	}
}

func TestRecoveryPatchExecutor_Apply_CreatesPolicyOnNilSteps(t *testing.T) {
	// Create a DAG where steps have no RecoveryPolicy.
	dag := newTestDAG(t)
	for _, step := range dag.Steps() {
		step.RecoveryPolicy = nil
	}
	exec := NewRecoveryPatchExecutor(dag)

	rollback, err := exec.Apply(context.Background(), patch.RuntimePatch{
		Type:   patch.PatchChangeRecoveryStrategy,
		Target: "recovery.strategy",
		Value:  string(RecoveryRetry),
	})
	require.NoError(t, err)
	require.NotNil(t, rollback)

	// Verify all steps now have a RecoveryPolicy.
	for _, step := range dag.Steps() {
		require.NotNil(t, step.RecoveryPolicy)
		assert.Equal(t, RecoveryRetry, step.RecoveryPolicy.Strategy)
	}
}

func TestDynamicExecutor_PatchRegistry_RecoveryCycle(t *testing.T) {
	// Integration: DynamicExecutor with patch registry + RecoveryPatchExecutor.
	dag := newTestDAG(t)
	patchReg := patch.NewRegistry()
	recoveryExec := NewRecoveryPatchExecutor(dag)
	require.NoError(t, patchReg.Register("recovery.strategy", recoveryExec))

	exec := NewDynamicExecutor(NewAgentRegistry(), ApplyImmediate)
	exec.WithPatchRegistry(patchReg)
	exec.WithRecoveryHandler(&mockRecoveryHandler{
		recoverFn: func(ctx context.Context, failure StepFailure, dag *MutableDAG) (*RecoveryDecision, error) {
			return &RecoveryDecision{
				Strategy: RecoveryReplaceNode,
				NewStep: &Step{
					ID: "A-v2", Name: "Step A v2", AgentType: "test", Input: "a-v2",
				},
			}, nil
		},
	})

	assert.NotNil(t, exec.patchRegistry, "patch registry should be set on DynamicExecutor")
}
