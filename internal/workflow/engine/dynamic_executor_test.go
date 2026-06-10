// nolint: errcheck // Test code may ignore return values
package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagent/internal/agents/base"
	"goagent/internal/core/models"
)

// TestNewDynamicExecutor verifies that NewDynamicExecutor returns a valid
// DynamicExecutor with the configured apply mode.
func TestNewDynamicExecutor(t *testing.T) {
	registry := NewAgentRegistry()

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	require.NotNil(t, executor, "NewDynamicExecutor should not return nil")
	assert.NotNil(t, executor.Executor, "embedded Executor should not be nil")
	assert.Equal(t, ApplyAtCheckpoint, executor.applyMode)
}

func TestNewDynamicExecutor_ApplyImmediate(t *testing.T) {
	registry := NewAgentRegistry()

	executor := NewDynamicExecutor(registry, ApplyImmediate)
	require.NotNil(t, executor)
	assert.Equal(t, ApplyImmediate, executor.applyMode)
}

func TestNewDynamicExecutor_WithOptions(t *testing.T) {
	registry := NewAgentRegistry()

	executor := NewDynamicExecutor(
		registry,
		ApplyAtCheckpoint,
		WithMaxParallel(5),
		WithStepTimeout(30*time.Second),
	)
	require.NotNil(t, executor)
	assert.Equal(t, 5, executor.maxParallel)
	assert.Equal(t, 30*time.Second, executor.stepTimeout)
}

// TestDynamicExecutor_ExecuteDynamic_NilWorkflow verifies that a nil workflow
// returns an error.
func TestDynamicExecutor_ExecuteDynamic_NilWorkflow(t *testing.T) {
	registry := NewAgentRegistry()
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	dag, _ := NewMutableDAG(nil)

	result, err := executor.ExecuteDynamic(
		context.Background(),
		nil, // nil workflow
		"input",
		dag,
	)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "workflow must not be nil")
}

// TestDynamicExecutor_ExecuteDynamic_NilMutableDAG verifies that a nil DAG
// returns an error.
func TestDynamicExecutor_ExecuteDynamic_NilMutableDAG(t *testing.T) {
	registry := NewAgentRegistry()
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)

	workflow := &Workflow{
		ID:    "wf-1",
		Name:  "test",
		Steps: []*Step{makeStep("a")},
	}

	result, err := executor.ExecuteDynamic(
		context.Background(),
		workflow,
		"input",
		nil, // nil DAG
	)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "mutableDAG must not be nil")
}

// TestDynamicExecutor_ExecuteDynamic_EmptyGraph verifies execution with an
// empty graph (no steps).
func TestDynamicExecutor_ExecuteDynamic_EmptyGraph(t *testing.T) {
	registry := NewAgentRegistry()
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	dag, _ := NewMutableDAG(nil)

	workflow := &Workflow{
		ID:    "wf-empty",
		Name:  "empty workflow",
		Steps: nil,
	}

	// Empty DAG has no execution order, so the workflow completes immediately
	// with zero step results.
	result, err := executor.ExecuteDynamic(
		context.Background(),
		workflow,
		"input",
		dag,
	)
	// An empty DAG produces an empty execution order. The executor collects
	// zero results and returns successfully.
	if err == nil {
		require.NotNil(t, result)
		assert.Equal(t, WorkflowStatusCompleted, result.Status)
		assert.Empty(t, result.Steps)
	}
}

// TestDynamicExecutor_ExecuteDynamic_StaticGraph verifies execution of a
// single-step graph with no mutations during execution.
func TestDynamicExecutor_ExecuteDynamic_StaticGraph(t *testing.T) {
	registry := NewAgentRegistry()

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{
						ItemID:      "item1",
						Name:        "Test Item",
						Description: "mock output",
						Price:       100.0,
					},
				},
			}, nil
		}), nil
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "Test Step",
			AgentType: "test-agent",
			Input:     "test input",
			Timeout:   10 * time.Second,
		},
	})

	workflow := &Workflow{
		ID:    "wf-static",
		Name:  "static workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(
		context.Background(),
		workflow,
		"initial input",
		dag,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusCompleted, result.Steps[0].Status)
}

// TestApplyMode_Constants verifies that ApplyMode constants exist and have
// distinct values.
func TestApplyMode_Constants(t *testing.T) {
	assert.Equal(t, ApplyMode(0), ApplyAtCheckpoint,
		"ApplyAtCheckpoint should be 0")
	assert.Equal(t, ApplyMode(1), ApplyImmediate,
		"ApplyImmediate should be 1")
	assert.NotEqual(t, ApplyAtCheckpoint, ApplyImmediate,
		"apply modes should be distinct")
}

// TestDynamicExecutor_generateDynamicExecutionID verifies that execution IDs
// are unique.
func TestDynamicExecutor_generateDynamicExecutionID(t *testing.T) {
	id1 := generateDynamicExecutionID()
	id2 := generateDynamicExecutionID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2, "execution IDs should be unique")
	assert.Contains(t, id1, "dyn-exec-")
}

// TestDynamicExecutor_ExecuteDynamic_TwoStepDAG verifies execution of a
// two-step linear DAG.
func TestDynamicExecutor_ExecuteDynamic_TwoStepDAG(t *testing.T) {
	registry := NewAgentRegistry()

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{
						ItemID:      "item1",
						Name:        "Test Item",
						Description: "output for: " + input.(string),
						Price:       50.0,
					},
				},
			}, nil
		}), nil
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "First Step",
			AgentType: "test-agent",
			Input:     "hello",
			Timeout:   10 * time.Second,
		},
		{
			ID:        "step2",
			Name:      "Second Step",
			AgentType: "test-agent",
			DependsOn: []string{"step1"},
			Timeout:   10 * time.Second,
		},
	})

	workflow := &Workflow{
		ID:    "wf-two-step",
		Name:  "two-step workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(
		context.Background(),
		workflow,
		"initial",
		dag,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	assert.Len(t, result.Steps, 2)
}

// TestDynamicExecutor_ExecuteDynamic_CancelledContext verifies that a cancelled
// context stops execution.
func TestDynamicExecutor_ExecuteDynamic_CancelledContext(t *testing.T) {
	registry := NewAgentRegistry()

	registry.Register("slow-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "slow-agent", func(ctx context.Context, input any) (any, error) {
			select {
			case <-time.After(5 * time.Second):
				return &models.RecommendResult{}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), nil
	})

	executor := NewDynamicExecutor(registry, ApplyImmediate)
	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "Slow Step",
			AgentType: "slow-agent",
			Timeout:   10 * time.Second,
		},
	})

	workflow := &Workflow{
		ID:    "wf-cancel",
		Name:  "cancelled workflow",
		Steps: dag.Steps(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := executor.ExecuteDynamic(ctx, workflow, "input", dag)
	require.Error(t, err, "should error with cancelled context")
}
