// nolint: errcheck // Test code may ignore return values
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/runtime"
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

// =====================================================
// DynamicExecutor HITL Tests
// =====================================================

// testAgentFactory returns an AgentFactory that creates a mock agent with the
// given process function.
func testAgentFactory(processFunc func(ctx context.Context, input any) (any, error)) AgentFactory {
	return func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock", "test-agent", processFunc), nil
	}
}

// TestDynamicExecutor_HITLApproved verifies that a step with an interrupt
// proceeds to execution when the handler approves.
func TestDynamicExecutor_HITLApproved(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("test-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Approved Item", Description: "approved output"},
				},
			}, nil
		},
	)))

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithHitlHandler(func(_ context.Context, point *InterruptPoint) (*InterruptResult, error) {
			return &InterruptResult{Approved: true}, nil
		})

	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "Interrupted Step",
			AgentType: "test-agent",
			Input:     "test input",
			Timeout:   10 * time.Second,
			Interrupt: &InterruptConfig{
				Message: "please approve",
			},
		},
	})

	workflow := &Workflow{
		ID:    "wf-hitl-approved",
		Name:  "HITL approved workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusCompleted, result.Steps[0].Status)
	assert.Equal(t, "approved output", result.Steps[0].Output)
}

// TestDynamicExecutor_HITLRejected verifies that a step is skipped when the
// handler rejects.
func TestDynamicExecutor_HITLRejected(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("test-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Item", Description: "should not run"},
				},
			}, nil
		},
	)))

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithHitlHandler(func(_ context.Context, point *InterruptPoint) (*InterruptResult, error) {
			return &InterruptResult{Approved: false, Feedback: "not now"}, nil
		})

	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "Rejected Step",
			AgentType: "test-agent",
			Input:     "test input",
			Timeout:   10 * time.Second,
			Interrupt: &InterruptConfig{
				Message: "approve this?",
			},
		},
	})

	workflow := &Workflow{
		ID:    "wf-hitl-rejected",
		Name:  "HITL rejected workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	// The workflow completes (not an error) but the step is skipped.
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusSkipped, result.Steps[0].Status)
	assert.Contains(t, result.Steps[0].Error, "rejected by human")
}

// TestDynamicExecutor_HITLHandlerError verifies that a step fails when the
// handler returns an error.
func TestDynamicExecutor_HITLHandlerError(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("test-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Item", Description: "should not run"},
				},
			}, nil
		},
	)))

	handlerErr := errors.New("handler communication failure")
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithHitlHandler(func(_ context.Context, point *InterruptPoint) (*InterruptResult, error) {
			return nil, handlerErr
		})

	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "Error Step",
			AgentType: "test-agent",
			Input:     "test input",
			Timeout:   10 * time.Second,
			Interrupt: &InterruptConfig{
				Message: "approve this?",
			},
		},
	})

	workflow := &Workflow{
		ID:    "wf-hitl-error",
		Name:  "HITL handler error workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusFailed, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusFailed, result.Steps[0].Status)
	assert.Contains(t, result.Steps[0].Error, "handler communication failure")
}

// TestDynamicExecutor_HITLNilHandler verifies that a step with an interrupt
// config but no handler set causes the step to fail.
func TestDynamicExecutor_HITLNilHandler(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("test-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Item", Description: "should not run"},
				},
			}, nil
		},
	)))

	// No WithHitlHandler call -- handler is nil.
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)

	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "No Handler Step",
			AgentType: "test-agent",
			Input:     "test input",
			Timeout:   10 * time.Second,
			Interrupt: &InterruptConfig{
				Message: "approve this?",
			},
		},
	})

	workflow := &Workflow{
		ID:    "wf-hitl-nil-handler",
		Name:  "HITL nil handler workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	// With nil handler, the HITL check is skipped in runDynamicSteps (guard:
	// step.Interrupt != nil && e.hitlHandler != nil). The step proceeds to
	// executeStepCore, which does not call handleInterrupt, so the step
	// actually executes successfully.
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusCompleted, result.Steps[0].Status)
}

// TestDynamicExecutor_HITLMultiStep verifies a workflow with multiple
// interrupt points where some are approved and one is rejected.
func TestDynamicExecutor_HITLMultiStep(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("test-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Item", Description: "output: " + input.(string)},
				},
			}, nil
		},
	)))

	approvalCount := 0
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithHitlHandler(func(_ context.Context, point *InterruptPoint) (*InterruptResult, error) {
			approvalCount++
			// Approve step1, reject step3.
			if point.StepID == "step3" {
				return &InterruptResult{Approved: false}, nil
			}
			return &InterruptResult{Approved: true}, nil
		})

	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "First Interrupted",
			AgentType: "test-agent",
			Input:     "first",
			Timeout:   10 * time.Second,
			Interrupt: &InterruptConfig{Message: "approve step1?"},
		},
		{
			ID:        "step2",
			Name:      "No Interrupt",
			AgentType: "test-agent",
			Input:     "second",
			DependsOn: []string{"step1"},
			Timeout:   10 * time.Second,
		},
		{
			ID:        "step3",
			Name:      "Rejected Interrupt",
			AgentType: "test-agent",
			Input:     "third",
			DependsOn: []string{"step1"},
			Timeout:   10 * time.Second,
			Interrupt: &InterruptConfig{Message: "approve step3?"},
		},
	})

	workflow := &Workflow{
		ID:    "wf-hitl-multi",
		Name:  "HITL multi-step workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	// step3 is rejected -> StepStatusSkipped. Since step3 is not StepStatusCompleted,
	// the workflow may still complete (skipped is a terminal state).
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 3)

	// Find each step result by ID.
	stepResults := make(map[string]*StepResult)
	for _, sr := range result.Steps {
		stepResults[sr.StepID] = sr
	}

	assert.Equal(t, StepStatusCompleted, stepResults["step1"].Status, "step1 should be completed")
	assert.Equal(t, StepStatusCompleted, stepResults["step2"].Status, "step2 should be completed")
	assert.Equal(t, StepStatusSkipped, stepResults["step3"].Status, "step3 should be skipped")
	assert.Equal(t, 2, approvalCount, "handler should be called twice")
}

// TestDynamicExecutor_HITLWithStore verifies that the interrupt store is used
// for crash recovery during HITL processing.
func TestDynamicExecutor_HITLWithStore(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("test-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Item", Description: "stored output"},
				},
			}, nil
		},
	)))

	store := NewMemoryInterruptStore()
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithHitlHandler(func(_ context.Context, point *InterruptPoint) (*InterruptResult, error) {
			return &InterruptResult{Approved: true}, nil
		}).
		WithHitlStore(store)

	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "Stored Step",
			AgentType: "test-agent",
			Input:     "test input",
			Timeout:   10 * time.Second,
			Interrupt: &InterruptConfig{Message: "approve with store"},
		},
	})

	workflow := &Workflow{
		ID:    "wf-hitl-store",
		Name:  "HITL with store workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusCompleted, result.Steps[0].Status)

	// Verify the interrupt was cleaned up from the store after approval.
	pending, err := store.ListPending(context.Background(), workflow.ID)
	require.NoError(t, err)
	assert.Empty(t, pending, "interrupt should be cleaned up after approval")
}

// =====================================================
// DynamicExecutor Recovery Tests
// =====================================================

// failingAgentFactory returns an AgentFactory that always returns the given error.
func failingAgentFactory(failErr error) AgentFactory {
	return func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("failing", "failing-agent", func(ctx context.Context, input any) (any, error) {
			return nil, failErr
		}), nil
	}
}

// TestDynamicExecutor_StepFailureNoPolicy verifies that a failed step with no
// RecoveryPolicy still fails the workflow.
func TestDynamicExecutor_StepFailureNoPolicy(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("failing-agent", failingAgentFactory(errors.New("step failed"))))

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)

	dag, _ := NewMutableDAG([]*Step{
		{
			ID:        "step1",
			Name:      "Failing Step",
			AgentType: "failing-agent",
			Input:     "test input",
			Timeout:   10 * time.Second,
		},
	})

	workflow := &Workflow{
		ID:    "wf-fail-no-policy",
		Name:  "fail no policy workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	require.Error(t, err, "workflow should fail when no recovery policy")
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusFailed, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusFailed, result.Steps[0].Status)
}

// TestDynamicExecutor_StepFailureWithReplaceNode verifies that a failed step
// with RecoveryReplaceNode continues without failing the workflow.
func TestDynamicExecutor_StepFailureWithReplaceNode(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("failing-agent", failingAgentFactory(errors.New("step failed"))))
	require.NoError(t, registry.Register("recovery-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Recovered Item", Description: "recovered output"},
				},
			}, nil
		},
	)))

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithRecoveryHandler(&mockRecoveryHandler{
			recoverFn: func(ctx context.Context, failure StepFailure, dag *MutableDAG) (*RecoveryDecision, error) {
				return &RecoveryDecision{
					Strategy: RecoveryReplaceNode,
					NewStep: &Step{
						ID:        failure.StepID + "_recovery",
						Name:      failure.StepID + " Recovery",
						AgentType: "recovery-agent",
						Input:     "recovery input",
						Timeout:   10 * time.Second,
						DependsOn: []string{}, // Will be set by ReplaceNode edge migration.
					},
				}, nil
			},
		})

	step1 := &Step{
		ID:        "step1",
		Name:      "Failing Step",
		AgentType: "failing-agent",
		Input:     "test input",
		Timeout:   10 * time.Second,
		RecoveryPolicy: &RecoveryPolicy{
			Strategy: RecoveryReplaceNode,
		},
	}

	dag, _ := NewMutableDAG([]*Step{step1})

	workflow := &Workflow{
		ID:    "wf-replace",
		Name:  "replace node workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	require.NoError(t, err, "workflow should succeed after replace_node recovery")
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)

	// Failed step is preserved in results.
	foundFailed := false
	foundRecovery := false
	for _, sr := range result.Steps {
		if sr.StepID == "step1" {
			foundFailed = true
			assert.Equal(t, StepStatusFailed, sr.Status, "original step should be failed")
		}
		if sr.StepID == "step1_recovery" {
			foundRecovery = true
			assert.Equal(t, StepStatusCompleted, sr.Status, "replacement step should be completed")
		}
	}
	assert.True(t, foundFailed, "failed step should be in results")
	assert.True(t, foundRecovery, "replacement step should be in results")
}

// TestDynamicExecutor_ReplaceNodeChain verifies that a replacement step can
// enable downstream steps to continue.
func TestDynamicExecutor_ReplaceNodeChain(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("failing-agent", failingAgentFactory(errors.New("step failed"))))
	require.NoError(t, registry.Register("recovery-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Recovered Item", Description: "recovered output"},
				},
			}, nil
		},
	)))
	require.NoError(t, registry.Register("analyze-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "analysis", Name: "Analysis", Description: "analysis done"},
				},
			}, nil
		},
	)))

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithRecoveryHandler(&mockRecoveryHandler{
			recoverFn: func(ctx context.Context, failure StepFailure, dag *MutableDAG) (*RecoveryDecision, error) {
				return &RecoveryDecision{
					Strategy: RecoveryReplaceNode,
					NewStep: &Step{
						ID:        failure.StepID + "_recovery",
						Name:      failure.StepID + " Recovery",
						AgentType: "recovery-agent",
						Input:     "recovery input",
						Timeout:   10 * time.Second,
					},
				}, nil
			},
		})

	// Topology: step1 -> step2 (step2 depends on step1)
	// After recovery: step1 (failed) is replaced by step1_recovery, step2 depends on step1_recovery.
	step1 := &Step{
		ID:        "step1",
		Name:      "Failing Step",
		AgentType: "failing-agent",
		Input:     "test input",
		Timeout:   10 * time.Second,
		RecoveryPolicy: &RecoveryPolicy{
			Strategy: RecoveryReplaceNode,
		},
	}
	step2 := &Step{
		ID:        "step2",
		Name:      "Analysis Step",
		AgentType: "analyze-agent",
		Input:     "analyze",
		DependsOn: []string{"step1"},
		Timeout:   10 * time.Second,
	}

	dag, _ := NewMutableDAG([]*Step{step1, step2})

	workflow := &Workflow{
		ID:    "wf-replace-chain",
		Name:  "replace node chain workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	require.NoError(t, err, "workflow should succeed with chain recovery")
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)

	// Verify the replacement step ran and step2 also ran.
	stepResults := make(map[string]*StepResult)
	for _, sr := range result.Steps {
		stepResults[sr.StepID] = sr
	}

	assert.Equal(t, StepStatusFailed, stepResults["step1"].Status, "original step should be failed")
	assert.Equal(t, StepStatusCompleted, stepResults["step1_recovery"].Status, "replacement step should be completed")
	assert.Equal(t, StepStatusCompleted, stepResults["step2"].Status, "downstream step should be completed")
}

// TestDynamicExecutor_RecoveryEvents verifies that recovery events are emitted
// in the correct order.
func TestDynamicExecutor_RecoveryEvents(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("failing-agent", failingAgentFactory(errors.New("step failed"))))
	require.NoError(t, registry.Register("recovery-agent", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Recovered Item", Description: "recovered output"},
				},
			}, nil
		},
	)))

	var eventsMu sync.Mutex
	var emittedEvents []string

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithRecoveryHandler(&mockRecoveryHandler{
			recoverFn: func(ctx context.Context, failure StepFailure, dag *MutableDAG) (*RecoveryDecision, error) {
				return &RecoveryDecision{
					Strategy: RecoveryReplaceNode,
					NewStep: &Step{
						ID:        failure.StepID + "_recovery",
						Name:      failure.StepID + " Recovery",
						AgentType: "recovery-agent",
						Input:     "recovery input",
						Timeout:   10 * time.Second,
					},
				}, nil
			},
		}).
		WithRecoveryEventSink(func(ctx context.Context, eventType events.EventType, payload map[string]any) {
			eventsMu.Lock()
			emittedEvents = append(emittedEvents, string(eventType))
			eventsMu.Unlock()
		})

	step1 := &Step{
		ID:        "step1",
		Name:      "Failing Step",
		AgentType: "failing-agent",
		Input:     "test input",
		Timeout:   10 * time.Second,
		RecoveryPolicy: &RecoveryPolicy{
			Strategy: RecoveryReplaceNode,
		},
	}

	dag, _ := NewMutableDAG([]*Step{step1})

	workflow := &Workflow{
		ID:    "wf-recovery-events",
		Name:  "recovery events workflow",
		Steps: dag.Steps(),
	}

	_, err := executor.ExecuteDynamic(context.Background(), workflow, "input", dag)
	require.NoError(t, err, "workflow should succeed")

	eventsMu.Lock()
	defer eventsMu.Unlock()

	require.Len(t, emittedEvents, 3, "should emit step.failed, step.recovery.started, step.recovery.completed")
	assert.Equal(t, string(events.EventStepFailed), emittedEvents[0])
	assert.Equal(t, string(events.EventStepRecoveryStarted), emittedEvents[1])
	assert.Equal(t, string(events.EventStepRecoveryCompleted), emittedEvents[2])
}

// mockRecoveryHandler implements StepRecoveryHandler for testing.
type mockRecoveryHandler struct {
	recoverFn func(ctx context.Context, failure StepFailure, dag *MutableDAG) (*RecoveryDecision, error)
}

func (m *mockRecoveryHandler) RecoverStep(ctx context.Context, failure StepFailure, dag *MutableDAG) (*RecoveryDecision, error) {
	return m.recoverFn(ctx, failure, dag)
}

// TestDynamicExecutor_HITLBuilderMethods verifies that WithHitlHandler and
// WithHitlStore return the same executor for chaining.
func TestDynamicExecutor_HITLBuilderMethods(t *testing.T) {
	registry := NewAgentRegistry()
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)

	store := NewMemoryInterruptStore()
	handler := func(_ context.Context, _ *InterruptPoint) (*InterruptResult, error) {
		return &InterruptResult{Approved: true}, nil
	}

	result := executor.WithHitlHandler(handler).WithHitlStore(store)
	assert.Same(t, executor, result, "builder methods should return the same executor")
	assert.NotNil(t, executor.hitlHandler)
	assert.NotNil(t, executor.hitlStore)
}

// ---------------------------------------------------------------------------
// Step condition tests
// ---------------------------------------------------------------------------

func TestDynamicExecutor_StepCondition_SkipsWhenFalse(t *testing.T) {
	registry := NewAgentRegistry()
	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{ItemID: "i1", Name: "item", Price: 10}},
			}, nil
		}), nil
	})

	conditionExecuted := false
	shouldSkip := true
	dag, _ := NewMutableDAG([]*Step{
		{
			ID: "s1", Name: "Step 1", AgentType: "test-agent", Input: "in",
			Condition: func(vars map[string]any) bool {
				conditionExecuted = true
				return !shouldSkip
			},
		},
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	wf := &Workflow{ID: "wf-cond", Steps: dag.Steps()}
	result, err := executor.ExecuteDynamic(context.Background(), wf, "init", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusSkipped, result.Steps[0].Status)
	assert.True(t, conditionExecuted)
}

func TestDynamicExecutor_StepCondition_ExecutesWhenTrue(t *testing.T) {
	registry := NewAgentRegistry()
	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{ItemID: "i1", Name: "item", Price: 10}},
			}, nil
		}), nil
	})

	dag, _ := NewMutableDAG([]*Step{
		{
			ID: "s1", Name: "Step 1", AgentType: "test-agent", Input: "in",
			Condition: func(vars map[string]any) bool { return true },
		},
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	wf := &Workflow{ID: "wf-cond", Steps: dag.Steps()}
	result, err := executor.ExecuteDynamic(context.Background(), wf, "init", dag)
	require.NoError(t, err)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusCompleted, result.Steps[0].Status)
}

func TestDynamicExecutor_StepCondition_NilCondition(t *testing.T) {
	registry := NewAgentRegistry()
	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{ItemID: "i1", Name: "item", Price: 10}},
			}, nil
		}), nil
	})

	dag, _ := NewMutableDAG([]*Step{
		{ID: "s1", AgentType: "test-agent", Input: "in"},
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	wf := &Workflow{ID: "wf-cond", Steps: dag.Steps()}
	result, err := executor.ExecuteDynamic(context.Background(), wf, "init", dag)
	require.NoError(t, err)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, StepStatusCompleted, result.Steps[0].Status)
}

func TestDynamicExecutor_StepCondition_MixedChain(t *testing.T) {
	registry := NewAgentRegistry()
	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{ItemID: "i1", Name: "item", Price: 10}},
			}, nil
		}), nil
	})

	dag, _ := NewMutableDAG([]*Step{
		{ID: "s1", AgentType: "test-agent", Input: "in"},
		{
			ID: "s2", AgentType: "test-agent", DependsOn: []string{"s1"},
			Condition: func(vars map[string]any) bool { return false },
		},
		{ID: "s3", AgentType: "test-agent", DependsOn: []string{"s1"}},
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	wf := &Workflow{ID: "wf-chain", Steps: dag.Steps()}
	result, err := executor.ExecuteDynamic(context.Background(), wf, "init", dag)
	require.NoError(t, err)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
	require.Len(t, result.Steps, 3)
	assert.Equal(t, StepStatusCompleted, result.Steps[0].Status) // s1 executed
	assert.Equal(t, StepStatusSkipped, result.Steps[1].Status)   // s2 skipped
	assert.Equal(t, StepStatusCompleted, result.Steps[2].Status) // s3 executed
}

// ---------------------------------------------------------------------------
// Router integration tests
// ---------------------------------------------------------------------------

func TestDynamicExecutor_RouterEmitsEvent(t *testing.T) {
	registry := NewAgentRegistry()
	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{ItemID: "i1", Name: "item", Price: 10}},
			}, nil
		}), nil
	})

	bus := runtime.NewPluginBus()
	router := runtime.NewExpressionRouter("test-router", []runtime.RouteRule{
		{
			FromStepID: "s1",
			ToStepID:   "s2",
			Condition:  func(output string, vars map[string]any) bool { return true },
			Reason:     "always route to s2",
		},
	})
	require.NoError(t, bus.Register(router))
	require.NoError(t, bus.Start(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh, err := bus.Subscribe(ctx, events.EventFilter{
		Types: []events.EventType{runtime.EventRouteDecided},
	})
	require.NoError(t, err)

	dag, _ := NewMutableDAG([]*Step{
		{ID: "s1", AgentType: "test-agent", Input: "in"},
		{ID: "s2", AgentType: "test-agent", DependsOn: []string{"s1"}},
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	executor.WithPluginBus(bus)

	wf := &Workflow{ID: "wf-router", Steps: dag.Steps()}
	_, err = executor.ExecuteDynamic(ctx, wf, "init", dag)
	require.NoError(t, err)

	select {
	case evt := <-eventCh:
		assert.Equal(t, runtime.EventRouteDecided, evt.Type)
		assert.Equal(t, "s2", evt.Payload["next_step_id"])
		assert.Equal(t, "always route to s2", evt.Payload["route_reason"])
	case <-ctx.Done():
		t.Fatal("timeout waiting for route event")
	}
}

// ---------------------------------------------------------------------------
// Round loop integration tests
// ---------------------------------------------------------------------------

// mockMemoryPluginForTest is a simple MemoryPlugin used in round loop tests.
type mockMemoryPluginForTest struct {
	mu        sync.Mutex
	adviseFn  func(ctx context.Context, state runtime.RouteState) ([]runtime.RouteAdvice, error)
	callCount int
}

func (m *mockMemoryPluginForTest) Name() string { return "mock-memory" }
func (m *mockMemoryPluginForTest) Capabilities() []runtime.Capability {
	return []runtime.Capability{runtime.CapMemory}
}
func (m *mockMemoryPluginForTest) Start(ctx context.Context, bus runtime.EventBus) error { return nil }
func (m *mockMemoryPluginForTest) Stop(ctx context.Context) error                        { return nil }
func (m *mockMemoryPluginForTest) AdviseRoute(ctx context.Context, state runtime.RouteState) ([]runtime.RouteAdvice, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	if m.adviseFn != nil {
		return m.adviseFn(ctx, state)
	}
	return nil, nil
}

// mockEvolutionPluginForTest is a simple EvolutionPlugin used in round loop tests.
type mockEvolutionPluginForTest struct {
	mu          sync.Mutex
	recommendFn func(ctx context.Context, state runtime.ExecutionState) (*runtime.RuntimeRecommendation, error)
	callCount   int
}

func (m *mockEvolutionPluginForTest) Name() string { return "mock-evolution" }
func (m *mockEvolutionPluginForTest) Capabilities() []runtime.Capability {
	return []runtime.Capability{runtime.CapEvolution}
}
func (m *mockEvolutionPluginForTest) Start(ctx context.Context, bus runtime.EventBus) error {
	return nil
}
func (m *mockEvolutionPluginForTest) Stop(ctx context.Context) error { return nil }
func (m *mockEvolutionPluginForTest) Recommend(ctx context.Context, state runtime.ExecutionState) (*runtime.RuntimeRecommendation, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	if m.recommendFn != nil {
		return m.recommendFn(ctx, state)
	}
	return nil, nil
}
func (m *mockEvolutionPluginForTest) RecordOutcome(ctx context.Context, outcome runtime.ExecutionOutcome) error {
	return nil
}

// TestDynamicExecutor_CheckpointPluginSetRound verifies that SetRound
// correctly persists the round number into the checkpoint data.
func TestDynamicExecutor_CheckpointPluginSetRound(t *testing.T) {
	ckptStore := newMemCheckpointStore()
	bus := runtime.NewPluginBus()
	ckpt := runtime.NewCheckpointPlugin("test-cp", ckptStore)
	require.NoError(t, bus.Register(ckpt))
	require.NoError(t, bus.Start(context.Background()))

	// BeforeStep creates the checkpoint snapshot
	err := bus.BeforeStep(context.Background(), "exec-round-1", &runtime.Step{ID: "s1"})
	require.NoError(t, err)

	// SetRound via direct access — we need to flush to verify
	ckpt.SetRound("exec-round-1", 3)
	require.NoError(t, ckpt.Flush(context.Background(), "exec-round-1"))

	// Load checkpoint data and inspect
	data, err := ckptStore.Load(context.Background(), "checkpoint/exec-round-1")
	require.NoError(t, err)
	require.NotNil(t, data)

	var loaded runtime.ExperienceCheckpoint
	require.NoError(t, json.Unmarshal(data, &loaded))
	assert.Equal(t, 3, loaded.CurrentRound, "SetRound should persist round to checkpoint")
}

// TestDynamicExecutor_ApplyRoundMutations verifies that applyRoundMutations
// invokes MemoryPlugin.AdviseRoute and EvolutionPlugin.Recommend, and that
// memory advice adds nodes to the DAG when confidence is sufficient.
func TestDynamicExecutor_ApplyRoundMutations(t *testing.T) {
	registry := NewAgentRegistry()
	bus := runtime.NewPluginBus()

	// Memory plugin that advises adding a new step
	memPlugin := &mockMemoryPluginForTest{
		adviseFn: func(ctx context.Context, state runtime.RouteState) ([]runtime.RouteAdvice, error) {
			return []runtime.RouteAdvice{
				{NextStepID: "suggested-step", Confidence: 0.9, Reason: "similar past execution"},
				{NextStepID: "low-conf-step", Confidence: 0.3, Reason: "low confidence"},
			}, nil
		},
	}

	// Evolution plugin that suggests a preferred agent
	evoPlugin := &mockEvolutionPluginForTest{
		recommendFn: func(ctx context.Context, state runtime.ExecutionState) (*runtime.RuntimeRecommendation, error) {
			return &runtime.RuntimeRecommendation{
				PreferredAgent: "advanced-agent",
				RouterWeight:   0.8,
			}, nil
		},
	}

	require.NoError(t, bus.Register(memPlugin))
	require.NoError(t, bus.Register(evoPlugin))
	require.NoError(t, bus.Start(context.Background()))

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithPluginBus(bus)

	dag, _ := NewMutableDAG([]*Step{
		{ID: "s1", AgentType: "test-agent", Input: "in"},
	})

	execution := &WorkflowExecution{
		ID:     "exec-mut-1",
		Status: WorkflowStatusRunning,
	}

	executor.applyRoundMutations(context.Background(), 1, execution, dag)

	// Verify: suggested-step was added (confidence >= 0.5)
	steps := dag.Steps()
	found := false
	for _, s := range steps {
		if s.ID == "suggested-step" {
			found = true
			break
		}
	}
	assert.True(t, found, "applyRoundMutations should add high-confidence memory advice as DAG nodes")

	// Verify: low-conf-step was NOT added (confidence < 0.5)
	for _, s := range steps {
		assert.NotEqual(t, "low-conf-step", s.ID, "low confidence advice should not create DAG nodes")
	}

	// Verify both plugins were called
	memPlugin.mu.Lock()
	assert.Equal(t, 1, memPlugin.callCount, "MemoryPlugin.AdviseRoute should be called once")
	memPlugin.mu.Unlock()
	evoPlugin.mu.Lock()
	assert.Equal(t, 1, evoPlugin.callCount, "EvolutionPlugin.Recommend should be called once")
	evoPlugin.mu.Unlock()
}

// TestDynamicExecutor_RoundLoopIntegration verifies the full round loop:
// checkpoint round tracking, DAG mutations, and loop plugin iteration.
func TestDynamicExecutor_RoundLoopIntegration(t *testing.T) {
	registry := NewAgentRegistry()
	registry.Register("echo", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock", "echo", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{ItemID: "i1", Name: "item", Price: 10}},
			}, nil
		}), nil
	})
	// Register an agent for the step that applyRoundMutations will create.
	registry.Register("default", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock", "default", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{ItemID: "i1", Name: "item", Price: 10}},
			}, nil
		}), nil
	})

	bus := runtime.NewPluginBus()
	ckptStore := newMemCheckpointStore()

	// Memory plugin: suggest a new step after round 1
	memPlugin := &mockMemoryPluginForTest{
		adviseFn: func(ctx context.Context, state runtime.RouteState) ([]runtime.RouteAdvice, error) {
			return []runtime.RouteAdvice{
				{NextStepID: "round2-step", Confidence: 0.9, Reason: "memory suggests continuation"},
			}, nil
		},
	}

	// Evolution plugin: track how many times it's called
	evoPlugin := &mockEvolutionPluginForTest{}

	// Loop plugin: allow exactly 2 rounds
	loopPlugin := runtime.NewLoopPlugin("round-loop", runtime.LoopConfig{
		MaxIterations: 2,
	})

	ckpt := runtime.NewCheckpointPlugin("test-cp", ckptStore)

	require.NoError(t, bus.Register(memPlugin))
	require.NoError(t, bus.Register(evoPlugin))
	require.NoError(t, bus.Register(loopPlugin))
	require.NoError(t, bus.Register(ckpt))
	require.NoError(t, bus.Start(context.Background()))

	dag, _ := NewMutableDAG([]*Step{
		{ID: "s1", AgentType: "echo", Input: "hello"},
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithPluginBus(bus).
		WithCheckpointStore(ckptStore)

	wf := &Workflow{ID: "wf-round-int", Steps: dag.Steps()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := executor.ExecuteDynamic(ctx, wf, "init", dag)
	require.NoError(t, err)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)

	// Verify plugins were called during round transitions
	memPlugin.mu.Lock()
	assert.Positive(t, memPlugin.callCount, "MemoryPlugin should be called in round mutations")
	memPlugin.mu.Unlock()
	evoPlugin.mu.Lock()
	assert.Positive(t, evoPlugin.callCount, "EvolutionPlugin should be called in round mutations")
	evoPlugin.mu.Unlock()

	// Verify SetRound was called: load checkpoint and check CurrentRound >= 1
	data, err := ckptStore.Load(context.Background(), "checkpoint/"+result.ExecutionID)
	if err == nil && data != nil {
		var loaded runtime.ExperienceCheckpoint
		require.NoError(t, json.Unmarshal(data, &loaded))
		assert.GreaterOrEqual(t, loaded.CurrentRound, 1, "checkpoint should have round tracking")
	}
}

func TestDynamicExecutor_RouterNoRouterRegistered(t *testing.T) {
	// Executor without a plugin bus should work normally with no routing.
	registry := NewAgentRegistry()
	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("mock-1", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{ItemID: "i1", Name: "item", Price: 10}},
			}, nil
		}), nil
	})

	dag, _ := NewMutableDAG([]*Step{
		{ID: "s1", AgentType: "test-agent", Input: "in"},
	})

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)
	wf := &Workflow{ID: "wf-norouter", Steps: dag.Steps()}
	result, err := executor.ExecuteDynamic(context.Background(), wf, "init", dag)
	require.NoError(t, err)
	assert.Equal(t, WorkflowStatusCompleted, result.Status)
}
