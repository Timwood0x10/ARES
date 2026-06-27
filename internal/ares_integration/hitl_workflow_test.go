// package integration provides end-to-end integration tests for HITL
// (Human-in-the-Loop) workflows with real MutableDAG + DynamicExecutor.
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

// --- Test helpers for HITL workflow tests ---

// hitlWorkflowAgent is a test agent that records execution and returns
// a predictable output for HITL workflow integration tests.
type hitlWorkflowAgent struct {
	mu       sync.Mutex
	id       string
	executed bool
	output   string
}

func newHitlWorkflowAgent(id, output string) *hitlWorkflowAgent {
	return &hitlWorkflowAgent{id: id, output: output}
}

func (a *hitlWorkflowAgent) ID() string                    { return a.id }
func (a *hitlWorkflowAgent) Type() models.AgentType        { return "hitl-wf-mock" }
func (a *hitlWorkflowAgent) Status() models.AgentStatus    { return models.AgentStatusReady }
func (a *hitlWorkflowAgent) Start(_ context.Context) error { return nil }
func (a *hitlWorkflowAgent) Stop(_ context.Context) error  { return nil }

func (a *hitlWorkflowAgent) Process(_ context.Context, _ any) (any, error) {
	a.mu.Lock()
	a.executed = true
	a.mu.Unlock()
	result := models.NewRecommendResult("test-session", "test-user")
	result.AddItem(&models.RecommendItem{Description: a.output, Content: a.output})
	return result, nil
}

func (a *hitlWorkflowAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
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

func (a *hitlWorkflowAgent) isExecuted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.executed
}

// createHitlWorkflowRegistry creates an AgentRegistry mapping step Input
// values to hitlWorkflowAgent instances.
func createHitlWorkflowRegistry(agentMap map[string]*hitlWorkflowAgent) *engine.AgentRegistry {
	registry := engine.NewAgentRegistry()
	_ = registry.Register("hitl-wf-mock", func(_ context.Context, cfg interface{}) (base.Agent, error) {
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

// TestHITLWorkflow_ApproveAndExecute verifies that a step with an interrupt
// configuration executes normally when the handler approves.
func TestHITLWorkflow_ApproveAndExecute(t *testing.T) {
	agentMap := map[string]*hitlWorkflowAgent{
		"input-a": newHitlWorkflowAgent("step-a", "output-a"),
		"input-b": newHitlWorkflowAgent("step-b", "output-b"),
	}
	registry := createHitlWorkflowRegistry(agentMap)

	var handlerCalled bool
	executor := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint)
	executor.WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
		handlerCalled = true
		assert.Equal(t, "step-b", point.StepID)
		assert.Equal(t, "Please approve step B", point.Message)
		return &engine.InterruptResult{Approved: true}, nil
	})

	workflow := &engine.Workflow{
		ID:   "hitl-approve",
		Name: "HITL Approve Workflow",
		Steps: []*engine.Step{
			{ID: "step-a", Name: "Step A", AgentType: "hitl-wf-mock", Input: "input-a"},
			{
				ID:        "step-b",
				Name:      "Step B",
				AgentType: "hitl-wf-mock",
				Input:     "input-b",
				DependsOn: []string{"step-a"},
				Interrupt: &engine.InterruptConfig{Message: "Please approve step B"},
			},
		},
		Variables: make(map[string]string),
	}

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx := context.Background()
	result, err := executor.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Both steps should have executed.
	assert.True(t, agentMap["input-a"].isExecuted(), "step A should execute")
	assert.True(t, agentMap["input-b"].isExecuted(), "step B should execute after approval")
	assert.True(t, handlerCalled, "interrupt handler should have been called")

	// Verify step order.
	require.Len(t, result.Steps, 2)
	assert.Equal(t, "step-a", result.Steps[0].StepID)
	assert.Equal(t, "step-b", result.Steps[1].StepID)
}

// TestHITLWorkflow_RejectAndSkip verifies that when the handler rejects,
// the step is skipped and the workflow fails because downstream depends on it.
func TestHITLWorkflow_RejectAndSkip(t *testing.T) {
	agentMap := map[string]*hitlWorkflowAgent{
		"input-a": newHitlWorkflowAgent("step-a", "output-a"),
		"input-b": newHitlWorkflowAgent("step-b", "output-b"),
		"input-c": newHitlWorkflowAgent("step-c", "output-c"),
	}
	registry := createHitlWorkflowRegistry(agentMap)

	executor := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithStepTimeout(2*time.Second),
	)
	executor.WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
		return &engine.InterruptResult{
			Approved: false,
			Feedback: "rejected for testing",
		}, nil
	})

	workflow := &engine.Workflow{
		ID:   "hitl-reject",
		Name: "HITL Reject Workflow",
		Steps: []*engine.Step{
			{ID: "step-a", Name: "Step A", AgentType: "hitl-wf-mock", Input: "input-a"},
			{
				ID:        "step-b",
				Name:      "Step B",
				AgentType: "hitl-wf-mock",
				Input:     "input-b",
				DependsOn: []string{"step-a"},
				Interrupt: &engine.InterruptConfig{Message: "Approve B?"},
			},
			{ID: "step-c", Name: "Step C", AgentType: "hitl-wf-mock", Input: "input-c", DependsOn: []string{"step-b"}},
		},
		Variables: make(map[string]string),
	}

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = executor.ExecuteDynamic(ctx, workflow, "initial", dag)

	// Step A should execute, step B should be skipped, step C fails because
	// its dependency (B) was not completed.
	assert.True(t, agentMap["input-a"].isExecuted(), "step A should execute")
	assert.False(t, agentMap["input-b"].isExecuted(), "step B should NOT execute (rejected)")
	require.Error(t, err, "workflow should fail when dependency is not satisfied")
}

// TestHITLWorkflow_ModifyData verifies that the interrupt handler receives
// the step's payload and the step proceeds after approval.
func TestHITLWorkflow_ModifyData(t *testing.T) {
	var capturedPayload map[string]any

	agentMap := map[string]*hitlWorkflowAgent{
		"input-a": newHitlWorkflowAgent("step-a", "output-a"),
	}
	registry := createHitlWorkflowRegistry(agentMap)

	executor := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint)
	executor.WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
		capturedPayload = point.Payload
		return &engine.InterruptResult{
			Approved: true,
			Data:     map[string]any{"override": "modified-value"},
		}, nil
	})

	workflow := &engine.Workflow{
		ID:   "hitl-modify",
		Name: "HITL Modify Data Workflow",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A",
				AgentType: "hitl-wf-mock",
				Input:     "input-a",
				Interrupt: &engine.InterruptConfig{
					Message: "Review payload",
					Payload: map[string]any{"key": "original-value"},
				},
			},
		},
		Variables: make(map[string]string),
	}

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx := context.Background()
	result, err := executor.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Verify the handler received the payload.
	require.NotNil(t, capturedPayload, "handler should have received a payload")
	assert.Equal(t, "original-value", capturedPayload["key"])

	// Step should have completed.
	require.Len(t, result.Steps, 1)
	assert.Equal(t, engine.StepStatusCompleted, result.Steps[0].Status)
	assert.True(t, agentMap["input-a"].isExecuted(), "step A should execute after approval")
}

// TestHITLWorkflow_Timeout verifies that when the handler blocks and the
// context times out, the workflow fails gracefully.
func TestHITLWorkflow_Timeout(t *testing.T) {
	agentMap := map[string]*hitlWorkflowAgent{
		"input-a": newHitlWorkflowAgent("step-a", "output-a"),
	}
	registry := createHitlWorkflowRegistry(agentMap)

	handlerStarted := make(chan struct{})
	executor := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint)
	executor.WithHitlHandler(func(ctx context.Context, _ *engine.InterruptPoint) (*engine.InterruptResult, error) {
		close(handlerStarted)
		// Block until context is cancelled.
		<-ctx.Done()
		return nil, ctx.Err()
	})

	workflow := &engine.Workflow{
		ID:   "hitl-timeout",
		Name: "HITL Timeout Workflow",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A",
				AgentType: "hitl-wf-mock",
				Input:     "input-a",
				Interrupt: &engine.InterruptConfig{Message: "Waiting for approval..."},
			},
		},
		Variables: make(map[string]string),
	}

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	// Use a short timeout to trigger context cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err = executor.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.Error(t, err, "workflow should fail when handler blocks and context times out")

	// Verify handler was actually started (not skipped).
	select {
	case <-handlerStarted:
		// Handler was called as expected.
	default:
		t.Fatal("handler should have been started before timeout")
	}
}

// TestHITLWorkflow_MultipleInterrupts verifies a workflow where 3 steps each
// have interrupts with mixed approve/reject decisions.
func TestHITLWorkflow_MultipleInterrupts(t *testing.T) {
	var mu sync.Mutex
	handlerCalls := make(map[string]string) // stepID -> "approved" or "rejected"

	agentMap := map[string]*hitlWorkflowAgent{
		"input-a": newHitlWorkflowAgent("step-a", "output-a"),
		"input-b": newHitlWorkflowAgent("step-b", "output-b"),
		"input-c": newHitlWorkflowAgent("step-c", "output-c"),
	}
	registry := createHitlWorkflowRegistry(agentMap)

	executor := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithStepTimeout(3*time.Second),
	)
	executor.WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
		mu.Lock()
		defer mu.Unlock()

		// Approve step-a, reject step-b, approve step-c.
		switch point.StepID {
		case "step-a":
			handlerCalls["step-a"] = "approved"
			return &engine.InterruptResult{Approved: true}, nil
		case "step-b":
			handlerCalls["step-b"] = "rejected"
			return &engine.InterruptResult{Approved: false, Feedback: "nope"}, nil
		case "step-c":
			handlerCalls["step-c"] = "approved"
			return &engine.InterruptResult{Approved: true}, nil
		default:
			return &engine.InterruptResult{Approved: true}, nil
		}
	})

	// Linear chain: A -> B -> C. If B is rejected, C cannot execute.
	workflow := &engine.Workflow{
		ID:   "hitl-multi",
		Name: "HITL Multiple Interrupts Workflow",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A",
				AgentType: "hitl-wf-mock",
				Input:     "input-a",
				Interrupt: &engine.InterruptConfig{Message: "Approve A"},
			},
			{
				ID:        "step-b",
				Name:      "Step B",
				AgentType: "hitl-wf-mock",
				Input:     "input-b",
				DependsOn: []string{"step-a"},
				Interrupt: &engine.InterruptConfig{Message: "Approve B"},
			},
			{
				ID:        "step-c",
				Name:      "Step C",
				AgentType: "hitl-wf-mock",
				Input:     "input-c",
				DependsOn: []string{"step-b"},
				Interrupt: &engine.InterruptConfig{Message: "Approve C"},
			},
		},
		Variables: make(map[string]string),
	}

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err = executor.ExecuteDynamic(ctx, workflow, "initial", dag)

	// Step A should have executed (approved), step B should be skipped (rejected).
	assert.True(t, agentMap["input-a"].isExecuted(), "step A should execute")
	assert.False(t, agentMap["input-b"].isExecuted(), "step B should NOT execute (rejected)")

	// The workflow should fail because C depends on B which was skipped.
	require.Error(t, err, "workflow should fail when B is rejected and C depends on B")

	// Verify handler was called for A and B (C is never reached because B is rejected).
	mu.Lock()
	assert.Equal(t, "approved", handlerCalls["step-a"], "step-a should be approved")
	assert.Equal(t, "rejected", handlerCalls["step-b"], "step-b should be rejected")
	// step-c handler should NOT have been called because B was rejected
	// and C's dependency is not satisfied, so the executor never reaches C's interrupt.
	mu.Unlock()
}
