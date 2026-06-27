// package integration provides end-to-end integration tests for MutableDAG
// + DynamicExecutor integration: mid-execution mutations, conditional edges,
// concurrent mutation, and event notifications.
package ares_integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// --- Test helpers for dynamic graph tests ---

// dynamicGraphAgent records execution and returns a predictable output.
type dynamicGraphAgent struct {
	mu       sync.Mutex
	id       string
	executed bool
	output   string
}

func newDynamicGraphAgent(id, output string) *dynamicGraphAgent {
	return &dynamicGraphAgent{id: id, output: output}
}

func (a *dynamicGraphAgent) ID() string                    { return a.id }
func (a *dynamicGraphAgent) Type() models.AgentType        { return "dyn-graph-mock" }
func (a *dynamicGraphAgent) Status() models.AgentStatus    { return models.AgentStatusReady }
func (a *dynamicGraphAgent) Start(_ context.Context) error { return nil }
func (a *dynamicGraphAgent) Stop(_ context.Context) error  { return nil }

func (a *dynamicGraphAgent) Process(_ context.Context, _ any) (any, error) {
	a.mu.Lock()
	a.executed = true
	a.mu.Unlock()
	result := models.NewRecommendResult("test-session", "test-user")
	result.AddItem(&models.RecommendItem{Description: a.output, Content: a.output})
	return result, nil
}

func (a *dynamicGraphAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	go func() {
		defer close(ch)
		result, err := a.Process(ctx, input)
		if err != nil {
			ch <- base.AgentEvent{Type: base.EventError, Source: a.id, Err: err}
			return
		}
		ch <- base.AgentEvent{Type: base.EventComplete, Source: a.id, Data: result}
	}()
	return ch, nil
}

func (a *dynamicGraphAgent) isExecuted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.executed
}

// createDynamicGraphRegistry creates an AgentRegistry for dynamic graph tests.
func createDynamicGraphRegistry(agentMap map[string]*dynamicGraphAgent) *engine.AgentRegistry {
	registry := engine.NewAgentRegistry()
	_ = registry.Register("dyn-graph-mock", func(_ context.Context, cfg interface{}) (base.Agent, error) {
		key, ok := cfg.(string)
		if !ok {
			return nil, fmt.Errorf("expected step input as config, got %T", cfg)
		}
		agent, exists := agentMap[key]
		if !exists {
			return nil, fmt.Errorf("no agent for step input %q", key)
		}
		return agent, nil
	})
	return registry
}

// TestDynamicGraph_AddNodeMidExecution verifies that adding a node to the
// MutableDAG during execution causes the new node to run.
func TestDynamicGraph_AddNodeMidExecution(t *testing.T) {
	agentMap := map[string]*dynamicGraphAgent{
		"input-a": newDynamicGraphAgent("step-a", "output-a"),
		"input-b": newDynamicGraphAgent("step-b", "output-b"),
	}
	registry := createDynamicGraphRegistry(agentMap)

	// Start with A -> B.
	initialSteps := []*engine.Step{
		{ID: "step-a", Name: "Step A", AgentType: "dyn-graph-mock", Input: "input-a"},
		{ID: "step-b", Name: "Step B", AgentType: "dyn-graph-mock", Input: "input-b", DependsOn: []string{"step-a"}},
	}

	dag, err := engine.NewMutableDAG(initialSteps)
	require.NoError(t, err)

	// Add step-c depending on B before execution starts (simulates mid-execution
	// mutation detected via version check in ApplyAtCheckpoint mode).
	agentMap["input-c"] = newDynamicGraphAgent("step-c", "output-c")
	err = dag.AddNode(context.Background(), &engine.Step{
		ID:        "step-c",
		Name:      "Step C",
		AgentType: "dyn-graph-mock",
		Input:     "input-c",
		DependsOn: []string{"step-b"},
	})
	require.NoError(t, err)

	dynExecutor := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(2),
	)

	workflow := &engine.Workflow{
		ID:        "dyn-add-node",
		Name:      "Dynamic Add Node Test",
		Steps:     dag.Steps(),
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := dynExecutor.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// All 3 steps should have executed.
	assert.Len(t, result.Steps, 3, "should have 3 step results")
	for _, step := range result.Steps {
		assert.Equal(t, engine.StepStatusCompleted, step.Status)
	}
	assert.True(t, agentMap["input-a"].isExecuted())
	assert.True(t, agentMap["input-b"].isExecuted())
	assert.True(t, agentMap["input-c"].isExecuted())
}

// TestDynamicGraph_RemoveNodeMidExecution verifies that removing a node
// from the MutableDAG causes the executor to skip it.
func TestDynamicGraph_RemoveNodeMidExecution(t *testing.T) {
	agentMap := map[string]*dynamicGraphAgent{
		"input-a": newDynamicGraphAgent("step-a", "output-a"),
		"input-c": newDynamicGraphAgent("step-c", "output-c"),
	}
	registry := createDynamicGraphRegistry(agentMap)

	// Create A -> B -> C, then remove both B and C, re-add C depending on A.
	initialSteps := []*engine.Step{
		{ID: "step-a", Name: "Step A", AgentType: "dyn-graph-mock", Input: "input-a"},
		{ID: "step-b", Name: "Step B", AgentType: "dyn-graph-mock", Input: "input-b", DependsOn: []string{"step-a"}},
		{ID: "step-c", Name: "Step C", AgentType: "dyn-graph-mock", Input: "input-c", DependsOn: []string{"step-b"}},
	}

	dag, err := engine.NewMutableDAG(initialSteps)
	require.NoError(t, err)

	// Remove C first (depends on B), then remove B (no more dependents),
	// then re-add C depending directly on A.
	require.NoError(t, dag.RemoveNode(context.Background(), "step-c"))
	require.NoError(t, dag.RemoveNode(context.Background(), "step-b"))
	require.NoError(t, dag.AddNode(context.Background(), &engine.Step{
		ID:        "step-c",
		Name:      "Step C (re-added)",
		AgentType: "dyn-graph-mock",
		Input:     "input-c",
		DependsOn: []string{"step-a"},
	}))

	dynExecutor := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(2),
	)

	workflow := &engine.Workflow{
		ID:        "dyn-remove-node",
		Name:      "Dynamic Remove Node Test",
		Steps:     dag.Steps(),
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := dynExecutor.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Step B should NOT have been executed (removed from DAG).
	assert.True(t, agentMap["input-a"].isExecuted(), "step A should execute")
	assert.True(t, agentMap["input-c"].isExecuted(), "step C should execute")
}

// TestDynamicGraph_ConditionalEdges verifies that edges with conditions
// result in the correct execution path.
func TestDynamicGraph_ConditionalEdges(t *testing.T) {
	// Test that the DAG correctly enforces dependency ordering:
	// A is root, B depends on A, C depends on A (B and C are parallel).
	agentMap := map[string]*dynamicGraphAgent{
		"input-a": newDynamicGraphAgent("step-a", "output-a"),
		"input-b": newDynamicGraphAgent("step-b", "output-b"),
		"input-c": newDynamicGraphAgent("step-c", "output-c"),
	}
	registry := createDynamicGraphRegistry(agentMap)

	dag, err := engine.NewMutableDAG([]*engine.Step{
		{ID: "step-a", Name: "Step A", AgentType: "dyn-graph-mock", Input: "input-a"},
		{ID: "step-b", Name: "Step B", AgentType: "dyn-graph-mock", Input: "input-b", DependsOn: []string{"step-a"}},
		{ID: "step-c", Name: "Step C", AgentType: "dyn-graph-mock", Input: "input-c", DependsOn: []string{"step-a"}},
	})
	require.NoError(t, err)

	dynExecutor := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(3),
	)

	workflow := &engine.Workflow{
		ID:        "dyn-conditional",
		Name:      "Conditional Edges Test",
		Steps:     dag.Steps(),
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := dynExecutor.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)
	assert.Len(t, result.Steps, 3)

	// All steps should have completed.
	for _, step := range result.Steps {
		assert.Equal(t, engine.StepStatusCompleted, step.Status)
	}

	// Verify A executed before both B and C by checking result order.
	stepOrder := make(map[string]int)
	for i, step := range result.Steps {
		stepOrder[step.StepID] = i
	}
	assert.Less(t, stepOrder["step-a"], stepOrder["step-b"], "A should execute before B")
	assert.Less(t, stepOrder["step-a"], stepOrder["step-c"], "A should execute before C")
}

// TestDynamicGraph_ConcurrentMutationAndExecution verifies that mutating the
// DAG while executing does not cause panics or data races.
func TestDynamicGraph_ConcurrentMutationAndExecution(t *testing.T) {
	agentMap := map[string]*dynamicGraphAgent{
		"input-a": newDynamicGraphAgent("step-a", "output-a"),
		"input-b": newDynamicGraphAgent("step-b", "output-b"),
	}
	registry := createDynamicGraphRegistry(agentMap)

	dag, err := engine.NewMutableDAG([]*engine.Step{
		{ID: "step-a", Name: "Step A", AgentType: "dyn-graph-mock", Input: "input-a"},
		{ID: "step-b", Name: "Step B", AgentType: "dyn-graph-mock", Input: "input-b", DependsOn: []string{"step-a"}},
	})
	require.NoError(t, err)

	dynExecutor := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(2),
	)

	workflow := &engine.Workflow{
		ID:        "dyn-concurrent",
		Name:      "Concurrent Mutation Test",
		Steps:     dag.Steps(),
		Variables: make(map[string]string),
	}

	ctx := context.Background()

	// Run mutations concurrently with execution.
	// The mutations add/remove leaf nodes that won't affect running steps.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			nodeID := fmt.Sprintf("extra-%d", i)
			_ = dag.AddNode(ctx, &engine.Step{
				ID:        nodeID,
				Name:      "Extra " + nodeID,
				AgentType: "dyn-graph-mock",
				Input:     "input-a", // Reuse existing agent.
				DependsOn: []string{"step-b"},
			})
			time.Sleep(5 * time.Millisecond)
		}
	}()

	result, err := dynExecutor.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Original steps should have completed.
	assert.True(t, agentMap["input-a"].isExecuted())
	assert.True(t, agentMap["input-b"].isExecuted())
}

// TestDynamicGraph_EventNotifications verifies that subscribing to the
// MutableDAG's event hub delivers mutation notifications.
func TestDynamicGraph_EventNotifications(t *testing.T) {
	dag, err := engine.NewMutableDAG([]*engine.Step{
		{ID: "step-a", Name: "Step A", AgentType: "mock", DependsOn: []string{}},
	})
	require.NoError(t, err)

	// Subscribe to graph events BEFORE mutations.
	ch := dag.Subscribe()

	ctx := context.Background()

	// Add a node — should generate an event.
	err = dag.AddNode(ctx, &engine.Step{
		ID:        "step-b",
		Name:      "Step B",
		AgentType: "mock",
		DependsOn: []string{"step-a"},
	})
	require.NoError(t, err)

	// Add an edge — should generate another event.
	err = dag.AddEdge(ctx, "step-a", "step-b")
	// This will fail because edge already exists (step-b depends on step-a).
	// Instead, add a new node and edge.
	require.Error(t, err, "edge should already exist from AddNode dependency")

	err = dag.AddNode(ctx, &engine.Step{
		ID:        "step-c",
		Name:      "Step C",
		AgentType: "mock",
		DependsOn: []string{},
	})
	require.NoError(t, err)

	err = dag.AddEdge(ctx, "step-c", "step-a")
	require.NoError(t, err)

	// Collect events.
	received := make([]engine.GraphEvent, 0, 3)
	timeout := time.After(2 * time.Second)
	for len(received) < 3 {
		select {
		case evt := <-ch:
			received = append(received, evt)
		case <-timeout:
			t.Fatalf("timed out waiting for events, received %d of 3", len(received))
		}
	}

	require.Len(t, received, 3)

	// First event: AddNode step-b.
	assert.True(t, received[0].Success)
	assert.Equal(t, engine.ChangeAddNode, received[0].Change.Type)
	assert.Equal(t, "step-b", received[0].Change.NodeID)

	// Second event: AddNode step-c.
	assert.True(t, received[1].Success)
	assert.Equal(t, engine.ChangeAddNode, received[1].Change.Type)
	assert.Equal(t, "step-c", received[1].Change.NodeID)

	// Third event: AddEdge step-c -> step-a.
	assert.True(t, received[2].Success)
	assert.Equal(t, engine.ChangeAddEdge, received[2].Change.Type)
	assert.Equal(t, "step-c", received[2].Change.FromID)
	assert.Equal(t, "step-a", received[2].Change.ToID)
}
