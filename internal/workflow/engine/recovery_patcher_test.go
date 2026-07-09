package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

func TestNewRecoveryPatchExecutor(t *testing.T) {
	dag := newTestDAG(t)
	exec := NewRecoveryPatchExecutor(dag)
	require.NotNil(t, exec)
	assert.Same(t, dag, exec.dag)
}

func TestRecoveryPatchExecutor_Apply_ChangeRecoveryStrategy(t *testing.T) {
	dag := newTestDAG(t)
	exec := NewRecoveryPatchExecutor(dag)

	rollback, err := exec.Apply(context.Background(), patch.RuntimePatch{
		Type:   patch.PatchChangeRecoveryStrategy,
		Target: "recovery.strategy",
		Value:  string(RecoveryReplaceNode),
	})
	require.NoError(t, err)
	require.NotNil(t, rollback)
	assert.Equal(t, patch.PatchChangeRecoveryStrategy, rollback.Type)

	// Verify all steps have the new strategy.
	for _, step := range dag.Steps() {
		if step.RecoveryPolicy != nil {
			assert.Equal(t, RecoveryReplaceNode, step.RecoveryPolicy.Strategy,
				"step %s should have ReplaceNode strategy", step.ID)
		}
	}
}

func TestRecoveryPatchExecutor_Apply_ChangeMaxRetries(t *testing.T) {
	dag := newTestDAG(t)
	exec := NewRecoveryPatchExecutor(dag)

	// First set a recovery policy on step A.
	steps := dag.Steps()
	require.Greater(t, len(steps), 0)
	steps[0].RecoveryPolicy = &RecoveryPolicy{Strategy: RecoveryRetry, MaxAttempts: 2}

	rollback, err := exec.Apply(context.Background(), patch.RuntimePatch{
		Type:   patch.PatchChangeMaxRetries,
		Target: "recovery.max_attempts",
		Value:  5,
	})
	require.NoError(t, err)
	require.NotNil(t, rollback)
	assert.Equal(t, patch.PatchChangeMaxRetries, rollback.Type)

	// Verify the policy was updated.
	assert.Equal(t, 5, steps[0].RecoveryPolicy.MaxAttempts)
}

func TestRecoveryPatchExecutor_Apply_UnsupportedType(t *testing.T) {
	dag := newTestDAG(t)
	exec := NewRecoveryPatchExecutor(dag)

	_, err := exec.Apply(context.Background(), patch.RuntimePatch{
		Type: patch.PatchType(999),
	})
	assert.Error(t, err)
}

func TestRecoveryPatchExecutor_CanApply(t *testing.T) {
	dag := newTestDAG(t)
	exec := NewRecoveryPatchExecutor(dag)

	tests := []struct {
		name  string
		patch patch.RuntimePatch
		want  bool
	}{
		{"change strategy valid", patch.RuntimePatch{Type: patch.PatchChangeRecoveryStrategy, Target: "strategy", Value: "retry"}, true},
		{"change strategy valid replace", patch.RuntimePatch{Type: patch.PatchChangeRecoveryStrategy, Target: "strategy", Value: "replace_node"}, true},
		{"change strategy valid fail_fast", patch.RuntimePatch{Type: patch.PatchChangeRecoveryStrategy, Target: "strategy", Value: "fail_fast"}, true},
		{"change strategy invalid value", patch.RuntimePatch{Type: patch.PatchChangeRecoveryStrategy, Value: 42}, false},
		{"change strategy unknown", patch.RuntimePatch{Type: patch.PatchChangeRecoveryStrategy, Value: "unknown"}, false},
		{"change max retries valid", patch.RuntimePatch{Type: patch.PatchChangeMaxRetries, Value: 3}, true},
		{"change max retries invalid", patch.RuntimePatch{Type: patch.PatchChangeMaxRetries, Value: "bad"}, false},
		{"unsupported type", patch.RuntimePatch{Type: patch.PatchType(999)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := exec.CanApply(context.Background(), tt.patch)
			if tt.want {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestRecoveryPatchExecutor_CanApply_NilDAG(t *testing.T) {
	exec := &RecoveryPatchExecutor{dag: nil}
	err := exec.CanApply(context.Background(), patch.RuntimePatch{
		Type: patch.PatchChangeRecoveryStrategy, Value: "retry",
	})
	assert.Error(t, err)
}

// newTestDAG creates a simple test DAG for recovery tests.
func newTestDAG(t *testing.T) *MutableDAG {
	t.Helper()
	steps := []*Step{
		{ID: "A", Name: "Step A", AgentType: "test", Input: "a"},
		{ID: "B", Name: "Step B", AgentType: "test", Input: "b", DependsOn: []string{"A"}},
	}
	dag, err := NewMutableDAG(steps)
	require.NoError(t, err)
	return dag
}
