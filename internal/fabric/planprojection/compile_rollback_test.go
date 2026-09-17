package planprojection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// TestCompileDAGFailedRecompileRestoresDeletedTasks pins HIGH #4: the full
// compile path reclaims the previous compile's tasks BEFORE compiling the
// replacement batch. When the rebuild fails, the reclaimed snapshots must be
// restored — the regression deleted the old tasks first and left them
// permanently lost on any compile failure (cancelled context, invalid batch,
// ErrTaskExists from an undeletable task).
func TestCompileDAGFailedRecompileRestoresDeletedTasks(t *testing.T) {
	dag, err := engine.NewMutableDAG([]*engine.Step{
		{ID: "a", AgentType: "x"},
		{ID: "b", AgentType: "y", DependsOn: []string{"a"}},
	})
	require.NoError(t, err)

	fabric := taskfabric.NewFabric()
	coord := NewCompileCoordinator(fabric, nil)

	// First compile succeeds: tasks a and b exist.
	_, err = coord.CompileDAG(context.Background(), dag)
	require.NoError(t, err)

	// Drive "a" to a terminal state so the restore must preserve more than
	// the default READY: a COMPLETED task resurrected as READY would be
	// re-executed (duplicate side effects).
	epoch, err := fabric.Acquire("a", "agent-a", 0)
	require.NoError(t, err)
	require.NoError(t, fabric.Start("a", "agent-a", epoch))
	require.NoError(t, fabric.Complete("a", "agent-a", epoch))

	// The recompile fails (cancelled context is the deterministic trigger;
	// the delete has already happened by the time CompilePlan checks ctx).
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = coord.CompileDAG(cancelledCtx, dag)
	require.Error(t, err, "the failed recompile must surface the compile error")

	// Rollback: both tasks must be back, with their pre-delete state intact.
	tk, err := fabric.Task("a")
	require.NoError(t, err, "deleted task a must be restored after the failed recompile")
	assert.Equal(t, taskfabric.StateCompleted, tk.State, "restored task a must keep its COMPLETED state (not resurrected as READY)")

	tk, err = fabric.Task("b")
	require.NoError(t, err, "deleted task b must be restored after the failed recompile")
	assert.Equal(t, taskfabric.StateReady, tk.State)
	assert.Equal(t, []string{"a"}, tk.Dependencies, "restored task b must keep its dependencies")
}

// TestCompileDAGSuccessfulRecompileStillReplaces pins the unchanged half of
// #4: a SUCCESSFUL full compile still replaces the previous batch (the
// rollback must not resurrect the old tasks alongside the new ones).
func TestCompileDAGSuccessfulRecompileStillReplaces(t *testing.T) {
	dag, err := engine.NewMutableDAG([]*engine.Step{
		{ID: "a", AgentType: "x"},
		{ID: "b", AgentType: "y", DependsOn: []string{"a"}},
	})
	require.NoError(t, err)

	fabric := taskfabric.NewFabric()
	coord := NewCompileCoordinator(fabric, nil)

	rec1, err := coord.CompileDAG(context.Background(), dag)
	require.NoError(t, err)
	require.Len(t, rec1.PlanIDs, 2)

	// Recompile the same DAG: succeeds, and the tracked set is exactly the
	// new batch (no duplicates, no leftovers).
	rec2, err := coord.CompileDAG(context.Background(), dag)
	require.NoError(t, err)
	require.Len(t, rec2.PlanIDs, 2)

	for _, id := range []string{"a", "b"} {
		tk, terr := fabric.Task(id)
		require.NoError(t, terr)
		require.Equal(t, taskfabric.StateReady, tk.State)
	}
}
