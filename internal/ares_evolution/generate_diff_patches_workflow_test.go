package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evolution/diff"
	evogenome "github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// buildDiffTestDAG builds a small deterministic MutableDAG for diff tests.
func buildDiffTestDAG(t *testing.T, steps []*engine.Step) *engine.MutableDAG {
	t.Helper()
	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)
	return dag
}

// liveWorkflowGraph is the live topology the evolution genome should be
// repointed at (the analogue of UpdateLiveDAG → WorkflowGenome.SetDAG). It is a
// real agent DAG with distinct node IDs, deliberately NOT sharing any node with
// the bootstrap placeholder graph.
func liveWorkflowGraph(t *testing.T) *engine.MutableDAG {
	t.Helper()
	return buildDiffTestDAG(t, []*engine.Step{
		{ID: "live-a", Name: "Live A", AgentType: "agent", Input: "in"},
		{ID: "live-b", Name: "Live B", AgentType: "agent", Input: "in", DependsOn: []string{"live-a"}},
		{ID: "live-c", Name: "Live C", AgentType: "agent", Input: "in", DependsOn: []string{"live-b"}},
		{ID: "live-d", Name: "Live D", AgentType: "agent", Input: "in", DependsOn: []string{"live-b"}},
	})
}

// placeholderWorkflowGraph is the bootstrap placeholder topology the genome used
// to evolve before the Step 7.1 repair. Its node IDs share nothing with the live
// graph, so a diff computed over it produces patches targeting nodes that do not
// exist on the live topology.
func placeholderWorkflowGraph(t *testing.T) *engine.MutableDAG {
	t.Helper()
	return buildDiffTestDAG(t, []*engine.Step{
		{ID: "input", Name: "Input", AgentType: "input", Input: "in"},
		{ID: "process", Name: "Process", AgentType: "process", Input: "in", DependsOn: []string{"input"}},
		{ID: "output", Name: "Output", AgentType: "output", Input: "in", DependsOn: []string{"process"}},
	})
}

// nodeRefsOutsideLive returns the node IDs referenced by the produced workflow
// patches that are neither a live-DAG node nor a node introduced by an
// InsertNode patch in the same run. Empty result means every reference
// resolves against the live topology.
//
// The differ only emits node/edge patches (no "replace" for workflow), so patch
// semantics are: InsertNode introduces a new node; RemoveNode/RemoveEdge target
// a pre-existing node; AddEdge connects a (possibly freshly introduced) node.
func nodeRefsOutsideLive(patches []patch.RuntimePatch, liveNodeIDs map[string]bool) []string {
	introduced := make(map[string]bool)
	for _, p := range patches {
		if p.Type == patch.PatchInsertNode {
			introduced[p.Target] = true
		}
	}
	var bad []string
	resolve := func(id string) {
		if id == "" {
			return
		}
		if !liveNodeIDs[id] && !introduced[id] {
			bad = append(bad, id)
		}
	}
	for _, p := range patches {
		resolve(p.Target)
		to, ok := p.Value.(string)
		if ok {
			resolve(to)
		}
	}
	return bad
}

// runGenerateDiffPatchesUntilNonEmpty calls generateDiffPatches repeatedly until
// it yields a non-empty patch set. Mutations clone the parent genome and only
// mutate the clone, so the live DAG / registries stay pristine across calls —
// repeated draws are safe. This removes the flake where the random operator
// selection occasionally no-ops (e.g. an insert when MaxNodes is reached or a
// split on a too-small graph), which would otherwise make a same-graph run
// vacuously empty.
func runGenerateDiffPatchesUntilNonEmpty(
	t *testing.T,
	genomeReg *evogenome.Registry,
	diffReg *diff.Registry,
	nChildren int,
	strategyID string,
) []patch.RuntimePatch {
	t.Helper()
	ctx := context.Background()
	var patches []patch.RuntimePatch
	for attempt := 0; attempt < 100; attempt++ {
		var err error
		patches, err = generateDiffPatches(ctx, genomeReg, diffReg, nChildren, strategyID)
		require.NoError(t, err)
		if len(patches) > 0 {
			return patches
		}
	}
	t.Fatalf("could not produce a non-empty diff over the given topology in 100 attempts")
	return nil
}

// liveNodeIDSet returns the set of node IDs present on the given DAG.
func liveNodeIDSet(dag *engine.MutableDAG) map[string]bool {
	set := make(map[string]bool)
	for id := range dag.StepIndex() {
		set[id] = true
	}
	return set
}

// TestGenerateDiffPatches_SameGraphNodeRefsResolveInLiveDAG is the literal 7.2.1
// same-graph (同图性) assertion. It runs generateDiffPatches over a REAL
// WorkflowGenome + WorkflowDiffer whose genome has been repointed at the live
// DAG — the sole thing UpdateLiveDAG is supposed to guarantee.
//
// Invariant: every node ID referenced by the produced workflow patches must be
// either a live-DAG node or a node introduced by an InsertNode patch in the same
// run. Pre-repair the genome kept evolving the bootstrap placeholder graph, so
// patches referenced placeholder node IDs ("input"/"process"/"output") that do
// not exist on the live topology — a silent no-op or a spurious error, both
// misattributed as a bad patch. This test fails on that cross-graph mismatch.
func TestGenerateDiffPatches_SameGraphNodeRefsResolveInLiveDAG(t *testing.T) {
	liveDAG := liveWorkflowGraph(t)

	// Genome repointed at the LIVE topology (what UpdateLiveDAG does via SetDAG).
	wf := evogenome.NewWorkflowGenome(liveDAG, evogenome.DefaultWorkflowGenomeConfig())
	genomeReg := evogenome.NewRegistry()
	require.NoError(t, genomeReg.Register(wf))
	diffReg := diff.NewRegistry()
	require.NoError(t, diffReg.Register(diff.NewWorkflowDiffer()))

	// Retry until a non-empty diff so the same-graph assertion is non-vacuous.
	patches := runGenerateDiffPatchesUntilNonEmpty(t, genomeReg, diffReg, 3, "strategy-under-test")

	bad := nodeRefsOutsideLive(patches, liveNodeIDSet(liveDAG))
	assert.Empty(t, bad,
		"every node ID referenced by a workflow patch must be a live-DAG node (got foreign refs: %v)",
		bad)
}

// TestGenerateDiffPatches_CrossGraphMismatchIsDetected proves the 7.2.1
// assertion is actually sensitive to the bug it guards. With the genome pointed
// at the bootstrap placeholder (pre-7.1 behavior) while the live topology is a
// different graph, nodeRefsOutsideLive must be NON-empty — the produced patches
// reference placeholder node IDs that have no counterpart on the live DAG.
func TestGenerateDiffPatches_CrossGraphMismatchIsDetected(t *testing.T) {
	liveDAG := liveWorkflowGraph(t)
	placeholderDAG := placeholderWorkflowGraph(t)

	// Genome still pointed at the placeholder (the pre-fix wiring mistake).
	wf := evogenome.NewWorkflowGenome(placeholderDAG, evogenome.DefaultWorkflowGenomeConfig())
	genomeReg := evogenome.NewRegistry()
	require.NoError(t, genomeReg.Register(wf))
	diffReg := diff.NewRegistry()
	require.NoError(t, diffReg.Register(diff.NewWorkflowDiffer()))

	patches := runGenerateDiffPatchesUntilNonEmpty(t, genomeReg, diffReg, 3, "strategy-under-test")

	bad := nodeRefsOutsideLive(patches, liveNodeIDSet(liveDAG))
	assert.NotEmpty(t, bad,
		"a patch computed over a foreign graph must reference nodes outside the live DAG, "+
			"which is exactly the cross-graph mismatch 7.2.1 must catch")
}
