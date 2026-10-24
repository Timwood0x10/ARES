package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// TestL1PatchExecutorAppliesMetadata verifies the M6 L1 patch executor
// applies PatchSetNodeMetadata to the L1 ToolClass DAG and returns an
// inverse patch for rollback.
func TestL1PatchExecutorAppliesMetadata(t *testing.T) {
	// Build an L1 DAG with one tool node.
	dag, err := NewMutableDAG([]*Step{
		{ID: "grep#args", AgentType: "grep", Metadata: map[string]string{"enabled": "true", "budget": "3"}},
	})
	require.NoError(t, err)

	exec := NewL1PatchExecutor(dag)

	// Apply a metadata patch: disable the grep tool.
	p := patch.RuntimePatch{
		Type:   patch.PatchSetNodeMetadata,
		Target: "grep#args",
		Value:  map[string]string{"enabled": "false", "budget": "3"},
	}
	require.NoError(t, exec.CanApply(context.Background(), p))

	inverse, err := exec.Apply(context.Background(), p)
	require.NoError(t, err)
	require.NotNil(t, inverse)
	require.Equal(t, patch.PatchSetNodeMetadata, inverse.Type)

	// Verify the metadata was applied.
	md, ok := dag.NodeMetadata("grep#args")
	require.True(t, ok)
	require.Equal(t, "false", md["enabled"])

	// Verify the inverse carries the old metadata.
	oldStep, ok := inverse.Value.(*Step)
	require.True(t, ok)
	require.Equal(t, "true", oldStep.Metadata["enabled"], "inverse must carry the pre-patch metadata")
}

// TestL1PatchExecutorRejectsTopologyPatches verifies that the L1 executor
// rejects patch types that would change the L1 graph's topology (only
// metadata patches are allowed).
func TestL1PatchExecutorRejectsTopologyPatches(t *testing.T) {
	dag, err := NewMutableDAG([]*Step{{ID: "grep", AgentType: "grep"}})
	require.NoError(t, err)

	exec := NewL1PatchExecutor(dag)

	p := patch.RuntimePatch{Type: patch.PatchInsertNode, Target: "new-tool"}
	err = exec.CanApply(context.Background(), p)
	require.Error(t, err, "L1 executor must reject topology patches")
}

// TestL1PatchExecutorNilDAGReturnsError verifies that applying a patch when
// no L1 DAG is wired returns a clear error (backward compatible).
func TestL1PatchExecutorNilDAGReturnsError(t *testing.T) {
	exec := NewL1PatchExecutor(nil)

	p := patch.RuntimePatch{
		Type:   patch.PatchSetNodeMetadata,
		Target: "grep",
		Value:  map[string]string{"enabled": "false"},
	}
	_, err := exec.Apply(context.Background(), p)
	require.Error(t, err)
}

// TestL1PatchExecutorSnapshotRestore verifies the rollback path: snapshot
// before apply, apply, then restore reverts the metadata.
func TestL1PatchExecutorSnapshotRestore(t *testing.T) {
	dag, err := NewMutableDAG([]*Step{
		{ID: "grep", AgentType: "grep", Metadata: map[string]string{"enabled": "true"}},
	})
	require.NoError(t, err)

	exec := NewL1PatchExecutor(dag)

	// Snapshot before.
	snap := exec.Snapshot()
	require.NotNil(t, snap)

	// Apply a metadata change.
	_, err = exec.Apply(context.Background(), patch.RuntimePatch{
		Type:   patch.PatchSetNodeMetadata,
		Target: "grep",
		Value:  map[string]string{"enabled": "false"},
	})
	require.NoError(t, err)

	// Verify the change took effect.
	md, _ := dag.NodeMetadata("grep")
	require.Equal(t, "false", md["enabled"])

	// Restore the snapshot.
	require.NoError(t, exec.Restore(snap))

	// Verify the metadata was reverted.
	md, _ = dag.NodeMetadata("grep")
	require.Equal(t, "true", md["enabled"], "restore must revert the metadata to the snapshot")
}
