package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

// TestUpdateLiveDAG_DoesNotFailOnRegisteredExecutors verifies the fix for the
// "UpdateLiveDAG always failed" defect.
//
// Pre-fix, UpdateLiveDAG rebuilt a new GraphPatchExecutor and called
// RegisterComponent + Register("graph.scheduler", ...). Because bootstrap had
// already registered those same keys, both calls returned an error on every
// invocation, so serve.go's consistency update always logged a failure and the
// graph executor's DAG was never actually swapped to the live one.
//
// Post-fix, UpdateLiveDAG updates the already-registered executor in place via
// GraphPatchExecutor.SetGraph, mirroring RecoveryPatchExecutor.SetDAG. It must
// return nil (not error) and both executors must reference the live DAG.
func TestUpdateLiveDAG_DoesNotFailOnRegisteredExecutors(t *testing.T) {
	ctx := context.Background()

	// Simulate the bootstrap state: a patch registry with graph.scheduler and
	// recovery executors already registered.
	patchReg := patch.NewRegistry()
	graphExec := wfgraph.NewGraphPatchExecutor(mustGraph(t, "bootstrap-graph"))
	require.NoError(t, patchReg.RegisterComponent(graphExec))
	require.NoError(t, patchReg.Register("graph.scheduler", graphExec))
	recoveryExec := engine.NewRecoveryPatchExecutor(&engine.MutableDAG{})
	require.NoError(t, patchReg.RegisterComponent(recoveryExec))

	components := &NewEvolutionComponents{
		PatchReg:     patchReg,
		graphExec:    graphExec,
		recoveryExec: recoveryExec,
	}

	// Live DAG the manager would hold after agents are created.
	liveDAG := mustDAG(t, []*engine.Step{
		{ID: "live-a", Name: "Live A", AgentType: "test", Input: "in-a"},
		{ID: "live-b", Name: "Live B", AgentType: "test", Input: "in-b", DependsOn: []string{"live-a"}},
	})

	// The bug: this returned an error on every call because the executor keys
	// were already registered.
	err := components.UpdateLiveDAG(liveDAG)
	require.NoError(t, err, "UpdateLiveDAG must not fail when executors are already registered")

	// The graph executor's snapshot must now reflect the live DAG's steps.
	snapshot, err := graphExec.Snapshot(ctx)
	require.NoError(t, err)
	g, ok := snapshot.(*wfgraph.Graph)
	require.True(t, ok, "graph executor snapshot should be the live graph")
	require.NotNil(t, g, "graph executor should hold the live graph")
	nodeIDs := g.NodeIDs()
	assert.ElementsMatch(t, []string{"live-a", "live-b"}, nodeIDs,
		"graph executor should expose the live DAG's nodes")

	// The recovery executor must also hold the live DAG.
	recSnapshot, err := recoveryExec.Snapshot(ctx)
	require.NoError(t, err)
	assert.Same(t, liveDAG, recSnapshot, "recovery executor should reference the live DAG")
}

// TestUpdateLiveDAG_NilLiveDAG verifies the nil guard still rejects a nil DAG.
func TestUpdateLiveDAG_NilLiveDAG(t *testing.T) {
	components := &NewEvolutionComponents{}
	err := components.UpdateLiveDAG(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func mustGraph(t *testing.T, name string) *wfgraph.Graph {
	t.Helper()
	g, err := wfgraph.NewGraph(name)
	require.NoError(t, err)
	return g
}

func mustDAG(t *testing.T, steps []*engine.Step) *engine.MutableDAG {
	t.Helper()
	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)
	return dag
}
