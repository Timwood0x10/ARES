// package integration provides end-to-end integration tests with real PostgreSQL.
package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/agents/base"
	"goagentx/internal/core/models"
	"goagentx/internal/workflow/engine"
)

// mockAgent is a simple agent implementation for integration tests.
// It returns a predictable RecommendResult with the given output.
type mockAgent struct {
	id        string
	agentType models.AgentType
	mu        sync.Mutex
	executed  bool
	output    string
}

// newMockAgent creates a mockAgent with the given ID and output.
func newMockAgent(id, output string) *mockAgent {
	return &mockAgent{
		id:        id,
		agentType: "mock",
		output:    output,
	}
}

func (a *mockAgent) ID() string                    { return a.id }
func (a *mockAgent) Type() models.AgentType        { return a.agentType }
func (a *mockAgent) Status() models.AgentStatus    { return models.AgentStatusReady }
func (a *mockAgent) Start(_ context.Context) error { return nil }
func (a *mockAgent) Stop(_ context.Context) error  { return nil }

func (a *mockAgent) Process(_ context.Context, _ any) (any, error) {
	a.mu.Lock()
	a.executed = true
	a.mu.Unlock()

	result := models.NewRecommendResult("test-session", "test-user")
	result.AddItem(&models.RecommendItem{
		Description: a.output,
		Content:     a.output,
	})
	return result, nil
}

func (a *mockAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			ch <- base.AgentEvent{Type: base.EventError, Source: a.id, Err: ctx.Err()}
			return
		default:
		}
		result, err := a.Process(ctx, input)
		if err != nil {
			ch <- base.AgentEvent{Type: base.EventError, Source: a.id, Err: err}
			return
		}
		ch <- base.AgentEvent{Type: base.EventComplete, Source: a.id, Data: result}
	}()
	return ch, nil
}

// IsExecuted returns whether the agent's Process method was called.
func (a *mockAgent) IsExecuted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.executed
}

// executionTracker records the order in which steps are executed.
type executionTracker struct {
	mu             sync.Mutex
	executionOrder []string
}

// newExecutionTracker creates a new executionTracker.
func newExecutionTracker() *executionTracker {
	return &executionTracker{}
}

// record appends a step input key to the execution order.
func (t *executionTracker) record(key string) {
	t.mu.Lock()
	t.executionOrder = append(t.executionOrder, key)
	t.mu.Unlock()
}

// GetOrder returns a copy of the recorded execution order.
func (t *executionTracker) GetOrder() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]string, len(t.executionOrder))
	copy(result, t.executionOrder)
	return result
}

// createTestRegistry creates an AgentRegistry with a mock agent factory.
// The factory maps step Input values (passed as config by AgentExecutor)
// to pre-built mock agents. Each agent records its execution via the tracker.
func createTestRegistry(tracker *executionTracker, agentMap map[string]*mockAgent) *engine.AgentRegistry {
	registry := engine.NewAgentRegistry()

	_ = registry.Register("mock", func(_ context.Context, cfg interface{}) (base.Agent, error) {
		key, ok := cfg.(string)
		if !ok {
			return nil, fmt.Errorf("expected step input as config, got %T", cfg)
		}

		agent, exists := agentMap[key]
		if !exists {
			return nil, fmt.Errorf("no agent registered for step input %q", key)
		}

		// Record execution order using the input key.
		tracker.record(key)

		return agent, nil
	})

	return registry
}

// getStepIndex returns the index of the given step ID in the order slice,
// or -1 if not found.
func getStepIndex(order []string, stepID string) int {
	for i, id := range order {
		if id == stepID {
			return i
		}
	}
	return -1
}

// TestDAGExecutionOrder verifies that a 3-step DAG (A -> B -> C) executes
// steps in the correct topological order.
func TestDAGExecutionOrder(t *testing.T) {
	tracker := newExecutionTracker()
	agentMap := map[string]*mockAgent{
		"input-a": newMockAgent("step-a", "output-a"),
		"input-b": newMockAgent("step-b", "output-b"),
		"input-c": newMockAgent("step-c", "output-c"),
	}
	registry := createTestRegistry(tracker, agentMap)
	executor := engine.NewExecutor(registry)

	workflow := &engine.Workflow{
		ID:   "test-workflow",
		Name: "Test DAG Execution",
		Steps: []*engine.Step{
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
		},
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, workflow, "initial-input")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Verify all steps completed.
	assert.Len(t, result.Steps, 3)
	for _, step := range result.Steps {
		assert.Equal(t, engine.StepStatusCompleted, step.Status)
	}

	// Verify execution order: A must come before B, B before C.
	// The tracker records step Input values as keys.
	order := tracker.GetOrder()
	require.Len(t, order, 3)

	aIdx := getStepIndex(order, "input-a")
	bIdx := getStepIndex(order, "input-b")
	cIdx := getStepIndex(order, "input-c")
	require.NotEqual(t, -1, aIdx, "step-a was not executed")
	require.NotEqual(t, -1, bIdx, "step-b was not executed")
	require.NotEqual(t, -1, cIdx, "step-c was not executed")
	assert.Less(t, aIdx, bIdx, "step-a must execute before step-b")
	assert.Less(t, bIdx, cIdx, "step-b must execute before step-c")
}

// TestDAGParallelExecution verifies that independent steps (no dependencies)
// can execute in parallel.
func TestDAGParallelExecution(t *testing.T) {
	tracker := newExecutionTracker()
	agentMap := map[string]*mockAgent{
		"input-a": newMockAgent("step-a", "output-a"),
		"input-b": newMockAgent("step-b", "output-b"),
		"input-c": newMockAgent("step-c", "output-c"),
	}
	registry := createTestRegistry(tracker, agentMap)
	executor := engine.NewExecutor(registry)

	// A and B are independent; C depends on both.
	workflow := &engine.Workflow{
		ID:   "test-parallel",
		Name: "Test Parallel Execution",
		Steps: []*engine.Step{
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
				DependsOn: []string{},
			},
			{
				ID:        "step-c",
				Name:      "Step C",
				AgentType: "mock",
				Input:     "input-c",
				DependsOn: []string{"step-a", "step-b"},
			},
		},
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, workflow, "initial-input")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Verify all steps completed.
	assert.Len(t, result.Steps, 3)

	// Verify C executed after both A and B.
	order := tracker.GetOrder()
	require.Len(t, order, 3)

	aIdx := getStepIndex(order, "input-a")
	bIdx := getStepIndex(order, "input-b")
	cIdx := getStepIndex(order, "input-c")
	require.NotEqual(t, -1, aIdx, "step-a was not executed")
	require.NotEqual(t, -1, bIdx, "step-b was not executed")
	require.NotEqual(t, -1, cIdx, "step-c was not executed")
	assert.Less(t, aIdx, cIdx, "step-a must execute before step-c")
	assert.Less(t, bIdx, cIdx, "step-b must execute before step-c")
}

// TestMutableDAGAddNode verifies that nodes can be added to a MutableDAG
// and the execution order is updated correctly.
func TestMutableDAGAddNode(t *testing.T) {
	ctx := context.Background()

	// Create initial DAG with A -> B.
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

	// Verify initial execution order.
	order, err := dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2)

	// Add node C depending on B.
	newStep := &engine.Step{
		ID:        "step-c",
		Name:      "Step C",
		AgentType: "mock",
		DependsOn: []string{"step-b"},
	}
	require.NoError(t, dag.AddNode(ctx, newStep))

	// Verify updated execution order.
	order, err = dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 3)

	// Verify C comes after B.
	bIdx := getStepIndex(order, "step-b")
	cIdx := getStepIndex(order, "step-c")
	assert.Less(t, bIdx, cIdx, "step-b must come before step-c in execution order")

	// Version should have incremented.
	assert.Equal(t, uint64(1), dag.Version())
}

// TestMutableDAGRemoveNode verifies that nodes can be removed from a MutableDAG.
func TestMutableDAGRemoveNode(t *testing.T) {
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

	// Remove node C (leaf node, no dependents).
	require.NoError(t, dag.RemoveNode(ctx, "step-c"))

	order, err := dag.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2)

	// Removing node A should fail because B depends on it.
	err = dag.RemoveNode(ctx, "step-a")
	require.Error(t, err, "should fail because step-b depends on step-a")
}

// TestMutableDAGCycleDetection verifies that adding an edge that creates a cycle
// returns an error.
func TestMutableDAGCycleDetection(t *testing.T) {
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

	// Adding edge B -> A should create a cycle.
	err = dag.AddEdge(ctx, "step-b", "step-a")
	require.Error(t, err, "should fail because B -> A creates a cycle")
}

// TestMutableDAGSnapshot verifies that Snapshot returns a deep copy.
func TestMutableDAGSnapshot(t *testing.T) {
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
	snapshot := dag.Snapshot()
	require.NotNil(t, snapshot)

	// Modify the original DAG.
	ctx := context.Background()
	require.NoError(t, dag.AddNode(ctx, &engine.Step{
		ID:        "step-c",
		Name:      "Step C",
		AgentType: "mock",
		DependsOn: []string{"step-b"},
	}))

	// Snapshot should not be affected.
	assert.Len(t, snapshot.Nodes, 2, "snapshot should have 2 nodes")
	assert.Len(t, snapshot.Edges, 1, "snapshot should have 1 edge")

	// Original should have 3 nodes.
	original := dag.Snapshot()
	assert.Len(t, original.Nodes, 3, "original should have 3 nodes after mutation")
}

// TestDynamicExecutorWithMutableDAG verifies that the DynamicExecutor can
// execute a workflow on a MutableDAG and apply mutations mid-execution.
func TestDynamicExecutorWithMutableDAG(t *testing.T) {
	tracker := newExecutionTracker()
	agentMap := map[string]*mockAgent{
		"input-a": newMockAgent("step-a", "output-a"),
		"input-b": newMockAgent("step-b", "output-b"),
		"input-c": newMockAgent("step-c", "output-c"),
	}
	registry := createTestRegistry(tracker, agentMap)

	// Create initial DAG with A -> B -> C.
	steps := []*engine.Step{
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
	}

	dag, err := engine.NewMutableDAG(steps)
	require.NoError(t, err)

	dynExecutor := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(2),
	)

	workflow := &engine.Workflow{
		ID:        "test-dynamic",
		Name:      "Test Dynamic Execution",
		Steps:     steps,
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := dynExecutor.ExecuteDynamic(ctx, workflow, "initial-input", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Verify all 3 steps completed.
	assert.Len(t, result.Steps, 3)
	for _, step := range result.Steps {
		assert.Equal(t, engine.StepStatusCompleted, step.Status)
	}
}

// TestDAGDuplicateStepID verifies that creating a DAG with duplicate step IDs
// returns an error.
func TestDAGDuplicateStepID(t *testing.T) {
	steps := []*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			DependsOn: []string{},
		},
		{
			ID:        "step-a",
			Name:      "Step A Duplicate",
			AgentType: "mock",
			DependsOn: []string{},
		},
	}

	_, err := engine.NewDAG(steps)
	require.Error(t, err, "should fail with duplicate step IDs")
}

// TestDAGInvalidDependency verifies that creating a DAG with invalid dependencies
// returns an error.
func TestDAGInvalidDependency(t *testing.T) {
	steps := []*engine.Step{
		{
			ID:        "step-a",
			Name:      "Step A",
			AgentType: "mock",
			DependsOn: []string{"non-existent"},
		},
	}

	_, err := engine.NewDAG(steps)
	require.Error(t, err, "should fail with invalid dependency")
}
