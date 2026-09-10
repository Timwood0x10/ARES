package graph

// DEEP_CODE_REVIEW_2026 §3.2 (MEDIUM) regressions for the graph patch
// executor: the SetGraph/Apply race, the unbound-executor contract, and the
// remove-node rollback losing every edge that touched the removed node.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// TestRemoveNodeRollbackRestoresEdges pins the composite rollback: removing
// a hub node destroys its in- and out-edges, so the rollback patch must
// restore the node AND every edge that touched it (plus the start pointer
// when the removed node was the start). Pre-fix the rollback was a bare
// PatchInsertNode — the node came back as an isolated vertex and the
// workflow stayed disconnected.
func TestRemoveNodeRollbackRestoresEdges(t *testing.T) {
	g := buildPatcherTestGraph(t) // A→B→C, start=A
	exec := NewGraphPatchExecutor(g)

	rollback, err := exec.Apply(context.Background(), patch.RuntimePatch{
		Type:   patch.PatchRemoveNode,
		Target: "B", // the hub: A→B and B→C both die
	})
	require.NoError(t, err)
	require.NotNil(t, rollback)
	require.Equal(t, patch.PatchRestoreNode, rollback.Type)

	// Apply the rollback exactly like patch.Registry does on failure.
	_, err = exec.Apply(context.Background(), *rollback)
	require.NoError(t, err)

	// The node is back…
	_, exists := g.nodes["B"]
	assert.True(t, exists, "rollback must re-insert the removed node")
	// …and BOTH edges are back.
	edgeSet := map[string]bool{}
	for _, e := range g.Edges() {
		edgeSet[e.From+"→"+e.To] = true
	}
	assert.True(t, edgeSet["A→B"], "rollback must restore the A→B edge, have %v", edgeSet)
	assert.True(t, edgeSet["B→C"], "rollback must restore the B→C edge, have %v", edgeSet)
}

// TestRemoveStartNodeRollbackRestoresStart extends the composite rollback to
// the start pointer: removing the START node clears g.start, and the
// rollback must put it back.
func TestRemoveStartNodeRollbackRestoresStart(t *testing.T) {
	g := buildPatcherTestGraph(t) // start = A
	exec := NewGraphPatchExecutor(g)

	rollback, err := exec.Apply(context.Background(), patch.RuntimePatch{
		Type:   patch.PatchRemoveNode,
		Target: "A",
	})
	require.NoError(t, err)
	require.Equal(t, patch.PatchRestoreNode, rollback.Type)
	assert.Empty(t, g.StartNode(), "removing the start node must clear the start pointer")

	_, err = exec.Apply(context.Background(), *rollback)
	require.NoError(t, err)
	assert.Equal(t, "A", g.StartNode(), "rollback must restore the start pointer")
}

// TestGraphPatchExecutorUnboundIsSafe pins the unbound-executor contract:
// before a live graph is attached (SetGraph), every entry point must return
// an error — never a nil-pointer dereference. Snapshot degrades to
// patch.ErrNoSnapshot; Apply and CanApply fail with a descriptive error.
func TestGraphPatchExecutorUnboundIsSafe(t *testing.T) {
	exec := NewGraphPatchExecutor(nil)

	_, err := exec.Snapshot(context.Background())
	assert.ErrorIs(t, err, patch.ErrNoSnapshot)

	_, err = exec.Apply(context.Background(), patch.RuntimePatch{
		Type: patch.PatchRemoveNode, Target: "A",
	})
	assert.ErrorContains(t, err, "graph is nil")

	err = exec.CanApply(context.Background(), patch.RuntimePatch{
		Type: patch.PatchRemoveNode, Target: "A",
	})
	assert.ErrorContains(t, err, "graph is nil")

	// Attaching a live graph afterwards works (the bootstrap flow).
	g := buildPatcherTestGraph(t)
	exec.SetGraph(g)
	_, err = exec.Apply(context.Background(), patch.RuntimePatch{
		Type: patch.PatchRemoveNode, Target: "C",
	})
	assert.NoError(t, err)
}

// TestGraphPatchExecutorSetGraphConcurrentWithApply pins the rebinding race:
// SetGraph (bootstrap rebinds the executor to the live graph after agents
// exist) may run while patches apply concurrently. Under -race, an unlocked
// pointer swap is a data race; the executor's lock must serialize it.
func TestGraphPatchExecutorSetGraphConcurrentWithApply(t *testing.T) {
	g1 := buildPatcherTestGraph(t)
	g2 := buildPatcherTestGraph(t)
	exec := NewGraphPatchExecutor(g1)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: continuously rebind between the two graphs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				exec.SetGraph(g2)
			} else {
				exec.SetGraph(g1)
			}
		}
	}()

	// Readers: apply patches / take snapshots against the rebound executor.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				p := patch.RuntimePatch{Type: patch.PatchRemoveNode, Target: "C"}
				_, _ = exec.Apply(context.Background(), p)
				_, _ = exec.Snapshot(context.Background())
				_ = exec.CanApply(context.Background(), p)
				// Re-add the node so the next remove can succeed (either graph).
				fn, err := NewFuncNode("C", func(_ context.Context, _ *State) error { return nil })
				if err == nil {
					_, _ = g1.Node("C", fn)
					_, _ = g2.Node("C", fn)
				}
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
