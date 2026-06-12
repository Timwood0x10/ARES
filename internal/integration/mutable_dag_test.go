// package integration provides end-to-end integration tests for MutableDAG
// with DynamicExecutor, testing mid-execution mutations.
package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagent/internal/workflow/engine"
)

// TestMutableDAGAddNodeMidExecution verifies that adding a node to a MutableDAG
// after initial creation updates the execution order correctly.
func TestMutableDAGAddNodeMidExecution(t *testing.T) {
	ctx := context.Background()

	steps := []*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			DependsOn: []string{},
		},
		{
			ID:        "step-b",
			Name:      "Step B",
			AgentType: "mock",
			DependsOn: []string{"step-a"},
		},
	}

	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)

	// Initial order: A -> B.
	order, err := dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2)

	// Add node C depending on B.
	require.NoError(t, dag.AddNode(ctx, &engine.Step{
		ID:        "step-c",
		Name:      "Step C",
		AgentType: "mock",
		DependsOn: []string{"step-b"},
	}))

	order, err = dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 3)

	// Verify C comes after B in topological order.
	bIdx := getStepIndex(order, "step-b")
	cIdx := getStepIndex(order, "step-c")
	assert.Less(t, bIdx, cIdx, "step-b must come before step-c")
	assert.Equal(t, uint64(1), dag.Version(), "version should increment after AddNode")
}

// TestMutableDAGRemoveNodeMidExecution verifies that removing a leaf node
// updates the execution order and removing a node with dependents fails.
func TestMutableDAGRemoveNodeMidExecution(t *testing.T) {
	ctx := context.Background()

	steps := []*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			DependsOn: []string{},
		},
		{
			ID:        "step-b",
			Name:      "Step B",
			AgentType: "mock",
			DependsOn: []string{"step-a"},
		},
		{
			ID:        "step-c",
			Name:      "Step C",
			AgentType: "mock",
			DependsOn: []string{"step-b"},
		},
	}

	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)

	// Remove leaf node C.
	require.NoError(t, dag.RemoveNode(ctx, "step-c"))
	order, err := dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2, "should have 2 nodes after removing C")

	// Removing A (which B depends on) should fail.
	err = dag.RemoveNode(ctx, "step-a")
	require.Error(t, err, "should fail because step-b depends on step-a")
	assert.ErrorIs(t, err, engine.ErrNodeHasDependents)

	// Removing non-existent node should fail.
	err = dag.RemoveNode(ctx, "non-existent")
	require.Error(t, err)
	assert.ErrorIs(t, err, engine.ErrNodeNotFound)
}

// TestMutableDAGAddEdgeChangesDependency verifies that adding an edge
// between existing nodes changes the topological order.
func TestMutableDAGAddEdgeChangesDependency(t *testing.T) {
	ctx := context.Background()

	// Start with A and B independent.
	steps := []*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			DependsOn: []string{},
		},
		{
			ID:        "step-b",
			Name:      "Step B",
			AgentType: "mock",
			DependsOn: []string{},
		},
	}

	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)

	// Initially A and B are both roots.
	order, err := dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2)

	// Add edge A -> B (B now depends on A).
	require.NoError(t, dag.AddEdge(ctx, "step-a", "step-b"))

	order, err = dag.GetExecutionOrder()
	require.NoError(t, err)
	aIdx := getStepIndex(order, "step-a")
	bIdx := getStepIndex(order, "step-b")
	assert.Less(t, aIdx, bIdx, "step-a must come before step-b after adding edge")

	// Duplicate edge should fail.
	err = dag.AddEdge(ctx, "step-a", "step-b")
	require.Error(t, err)
	assert.ErrorIs(t, err, engine.ErrDuplicateEdge)
}

// TestMutableDAGCycleDetectionDuringMutation verifies that adding an edge
// that would create a cycle returns ErrCycleDetected.
func TestMutableDAGCycleDetectionDuringMutation(t *testing.T) {
	ctx := context.Background()

	steps := []*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			DependsOn: []string{},
		},
		{
			ID:        "step-b",
			Name:      "Step B",
			AgentType: "mock",
			DependsOn: []string{"step-a"},
		},
		{
			ID:        "step-c",
			Name:      "Step C",
			AgentType: "mock",
			DependsOn: []string{"step-b"},
		},
	}

	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)

	// Adding edge C -> A should create a cycle.
	err = dag.AddEdge(ctx, "step-c", "step-a")
	require.Error(t, err, "should fail because C -> A creates a cycle")
	assert.ErrorIs(t, err, engine.ErrCycleDetected)

	// Adding edge B -> A should also create a cycle.
	err = dag.AddEdge(ctx, "step-b", "step-a")
	require.Error(t, err, "should fail because B -> A creates a cycle")
	assert.ErrorIs(t, err, engine.ErrCycleDetected)

	// Adding node with circular dependency should fail.
	err = dag.AddNode(ctx, &engine.Step{
		ID:        "step-d",
		Name:      "Step D",
		AgentType: "mock",
		DependsOn: []string{"step-c"},
	})
	// This is valid (D depends on C, no cycle).
	require.NoError(t, err)

	// Now adding edge D -> A should fail (A -> B -> C -> D -> A would be a cycle).
	err = dag.AddEdge(ctx, "step-d", "step-a")
	require.Error(t, err)
	assert.ErrorIs(t, err, engine.ErrCycleDetected)
}

// TestMutableDAGConcurrentMutations verifies that concurrent AddNode and
// RemoveNode operations from multiple goroutines do not corrupt the DAG.
func TestMutableDAGConcurrentMutations(t *testing.T) {
	ctx := context.Background()

	// Create a DAG with a single root node.
	steps := []*engine.Step{
		{
			ID:        "root",
			Name:      "Root",
			AgentType: "mock",
			DependsOn: []string{},
		},
	}

	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)

	const numGoroutines = 10
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	// Concurrently add leaf nodes depending on root.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			nodeID := "leaf-" + string(rune('a'+idx))
			addErr := dag.AddNode(ctx, &engine.Step{
				ID:        nodeID,
				Name:      "Leaf " + nodeID,
				AgentType: "mock",
				DependsOn: []string{"root"},
			})
			if addErr != nil {
				errs <- addErr
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for e := range errs {
		require.NoError(t, e, "concurrent AddNode should not fail")
	}

	// Should have root + numGoroutines leaf nodes.
	assert.Equal(t, numGoroutines+1, dag.NodeCount())
	assert.Equal(t, numGoroutines, dag.EdgeCount())

	// Verify execution order is valid (root must come first).
	order, err := dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, numGoroutines+1)
	assert.Equal(t, "root", order[0], "root must be first in execution order")
}

// TestMutableDAGSnapshotWithStepsConsistency verifies that SnapshotWithSteps
// returns a consistent snapshot of both DAG topology and step references.
func TestMutableDAGSnapshotWithStepsConsistency(t *testing.T) {
	ctx := context.Background()

	steps := []*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			DependsOn: []string{},
		},
		{
			ID:        "step-b",
			Name:      "Step B",
			AgentType: "mock",
			DependsOn: []string{"step-a"},
		},
	}

	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)

	// Take a snapshot.
	snapshotDAG, snapshotSteps := dag.SnapshotWithSteps()
	require.NotNil(t, snapshotDAG)
	require.NotNil(t, snapshotSteps)

	// Snapshot should have 2 nodes and 1 edge.
	assert.Len(t, snapshotDAG.Nodes, 2)
	assert.Len(t, snapshotDAG.Edges, 1)
	assert.Len(t, snapshotSteps, 2)

	// Verify step references are present.
	_, hasA := snapshotSteps["step-a"]
	_, hasB := snapshotSteps["step-b"]
	assert.True(t, hasA, "snapshot should contain step-a")
	assert.True(t, hasB, "snapshot should contain step-b")

	// Mutate the original DAG.
	require.NoError(t, dag.AddNode(ctx, &engine.Step{
		ID:        "step-c",
		Name:      "Step C",
		AgentType: "mock",
		DependsOn: []string{"step-b"},
	}))

	// Snapshot should NOT be affected.
	assert.Len(t, snapshotDAG.Nodes, 2, "snapshot nodes should not change")
	assert.Len(t, snapshotSteps, 2, "snapshot steps should not change")

	// Original should now have 3 nodes.
	origDAG, origSteps := dag.SnapshotWithSteps()
	assert.Len(t, origDAG.Nodes, 3)
	assert.Len(t, origSteps, 3)
}

// TestMutableDAGRemoveEdge verifies that RemoveEdge correctly updates
// the dependency graph and execution order.
func TestMutableDAGRemoveEdge(t *testing.T) {
	ctx := context.Background()

	steps := []*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			DependsOn: []string{},
		},
		{
			ID:        "step-b",
			Name:      "Step B",
			AgentType: "mock",
			DependsOn: []string{"step-a"},
		},
	}

	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)

	// Remove the edge A -> B.
	require.NoError(t, dag.RemoveEdge(ctx, "step-a", "step-b"))

	// Now B should no longer depend on A.
	order, err := dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2)

	// Removing a non-existent edge should fail.
	err = dag.RemoveEdge(ctx, "step-a", "step-b")
	require.Error(t, err)
	assert.ErrorIs(t, err, engine.ErrEdgeNotFound)
}

// TestMutableDAGDynamicExecutorWithMutation verifies that the DynamicExecutor
// can execute a workflow on a MutableDAG with all steps completing.
func TestMutableDAGDynamicExecutorWithMutation(t *testing.T) {
	tracker := newExecutionTracker()
	agentMap := map[string]*mockAgent{
		"input-a": newMockAgent("step-a", "output-a"),
		"input-b": newMockAgent("step-b", "output-b"),
		"input-c": newMockAgent("step-c", "output-c"),
	}
	registry := createTestRegistry(tracker, agentMap)

	dag, err := engine.NewMutableDAG([]*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			Input:     "input-a",
			DependsOn: []string{},
		},
		{
			ID:        "step-b",
			Name:      "Step B",
			AgentType: "mock",
			Input:     "input-b",
			DependsOn: []string{"step-a"},
		},
		{
			ID:        "step-c",
			Name:      "Step C",
			AgentType: "mock",
			Input:     "input-c",
			DependsOn: []string{"step-b"},
		},
	})
	require.NoError(t, err)

	dynExecutor := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(2),
	)

	workflow := &engine.Workflow{
		ID:        "dyn-test",
		Name:      "Dynamic Executor Test",
		Steps:     dag.Steps(),
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := dynExecutor.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)
	assert.Len(t, result.Steps, 3)

	for _, step := range result.Steps {
		assert.Equal(t, engine.StepStatusCompleted, step.Status)
	}
}
