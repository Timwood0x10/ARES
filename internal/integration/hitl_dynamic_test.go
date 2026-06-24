// package integration provides end-to-end integration tests for HITL
// (Human-in-the-Loop) combined with DynamicExecutor and MutableDAG.
package integration

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

// dynHitlAgent is a test agent for HITL + DynamicExecutor integration tests.
// It records execution state and returns a predictable output.
type dynHitlAgent struct {
	mu       sync.Mutex
	id       string
	executed bool
	output   string
}

func newDynHitlAgent(id, output string) *dynHitlAgent {
	return &dynHitlAgent{id: id, output: output}
}

func (a *dynHitlAgent) ID() string                    { return a.id }
func (a *dynHitlAgent) Type() models.AgentType        { return "dyn-hitl-mock" }
func (a *dynHitlAgent) Status() models.AgentStatus    { return models.AgentStatusReady }
func (a *dynHitlAgent) Start(_ context.Context) error { return nil }
func (a *dynHitlAgent) Stop(_ context.Context) error  { return nil }

func (a *dynHitlAgent) Process(_ context.Context, _ any) (any, error) {
	a.mu.Lock()
	a.executed = true
	a.mu.Unlock()

	result := models.NewRecommendResult("test-session", "test-user")
	result.AddItem(&models.RecommendItem{Description: a.output, Content: a.output})
	return result, nil
}

func (a *dynHitlAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
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

// isExecuted returns whether the agent's Process method was called.
func (a *dynHitlAgent) isExecuted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.executed
}

// createDynHitlRegistry creates an AgentRegistry mapping step Input values to dynHitlAgents.
func createDynHitlRegistry(agentMap map[string]*dynHitlAgent) *engine.AgentRegistry {
	registry := engine.NewAgentRegistry()
	_ = registry.Register("dyn-hitl-mock", func(_ context.Context, cfg interface{}) (base.Agent, error) {
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

// newDynHitlWorkflow creates a 3-step linear workflow (A -> B -> C) where
// step B has the given interrupt configuration.
func newDynHitlWorkflow(interruptB *engine.InterruptConfig) *engine.Workflow {
	return &engine.Workflow{
		ID:   "dyn-hitl-wf",
		Name: "Dynamic HITL Workflow",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A",
				AgentType: "dyn-hitl-mock",
				Input:     "input-a",
			},
			{
				ID:        "step-b",
				Name:      "Step B with interrupt",
				AgentType: "dyn-hitl-mock",
				Input:     "input-b",
				DependsOn: []string{"step-a"},
				Interrupt: interruptB,
			},
			{
				ID:        "step-c",
				Name:      "Step C",
				AgentType: "dyn-hitl-mock",
				Input:     "input-c",
				DependsOn: []string{"step-b"},
			},
		},
		Variables: make(map[string]string),
	}
}

// TestHITL_DynamicExecutor_ApprovedWorkflow verifies that a MutableDAG with
// 3 steps (A -> B -> C) executes all steps in order when the interrupt
// handler on step B approves.
func TestHITL_DynamicExecutor_ApprovedWorkflow(t *testing.T) {
	agentMap := map[string]*dynHitlAgent{
		"input-a": newDynHitlAgent("step-a", "output-a"),
		"input-b": newDynHitlAgent("step-b", "output-b"),
		"input-c": newDynHitlAgent("step-c", "output-c"),
	}
	registry := createDynHitlRegistry(agentMap)

	var mu sync.Mutex
	approvedSteps := make(map[string]bool)

	dynExec := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint)
	dynExec.WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
		mu.Lock()
		approvedSteps[point.StepID] = true
		mu.Unlock()
		return &engine.InterruptResult{
			Approved: true,
			Feedback: "approved for testing",
		}, nil
	})

	workflow := newDynHitlWorkflow(&engine.InterruptConfig{
		Message: "Please approve step B",
	})

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx := context.Background()
	result, err := dynExec.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)
	assert.Len(t, result.Steps, 3)

	// All three steps should have executed.
	assert.True(t, agentMap["input-a"].isExecuted(), "step A should execute")
	assert.True(t, agentMap["input-b"].isExecuted(), "step B should execute after approval")
	assert.True(t, agentMap["input-c"].isExecuted(), "step C should execute")

	// The interrupt handler should have been called for step B.
	mu.Lock()
	assert.True(t, approvedSteps["step-b"], "step-b interrupt should have been handled")
	mu.Unlock()

	// Verify step order: A before B, B before C.
	order := make([]string, 0, len(result.Steps))
	for _, step := range result.Steps {
		order = append(order, step.StepID)
	}
	aIdx := getStepIndex(order, "step-a")
	bIdx := getStepIndex(order, "step-b")
	cIdx := getStepIndex(order, "step-c")
	require.NotEqual(t, -1, aIdx, "step-a not found in results")
	require.NotEqual(t, -1, bIdx, "step-b not found in results")
	require.NotEqual(t, -1, cIdx, "step-c not found in results")
	assert.Less(t, aIdx, bIdx, "step-a must execute before step-b")
	assert.Less(t, bIdx, cIdx, "step-b must execute before step-c")
}

// TestHITL_DynamicExecutor_RejectedWorkflow verifies that when the interrupt
// handler rejects step B, step A executes, step B is skipped, and step C
// fails because its dependency on B is not satisfied.
func TestHITL_DynamicExecutor_RejectedWorkflow(t *testing.T) {
	agentMap := map[string]*dynHitlAgent{
		"input-a": newDynHitlAgent("step-a", "output-a"),
		"input-b": newDynHitlAgent("step-b", "output-b"),
		"input-c": newDynHitlAgent("step-c", "output-c"),
	}
	registry := createDynHitlRegistry(agentMap)

	var handlerCalled bool
	dynExec := engine.NewDynamicExecutor(
		registry,
		engine.ApplyAtCheckpoint,
		engine.WithStepTimeout(2*time.Second),
	)
	dynExec.WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
		handlerCalled = true
		return &engine.InterruptResult{
			Approved: false,
			Feedback: "rejected for testing",
		}, nil
	})

	workflow := newDynHitlWorkflow(&engine.InterruptConfig{
		Message: "Please approve step B",
	})

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = dynExec.ExecuteDynamic(ctx, workflow, "initial", dag)

	// The handler should have been called.
	assert.True(t, handlerCalled, "interrupt handler should have been called")

	// Step A should have executed, step B should be skipped.
	assert.True(t, agentMap["input-a"].isExecuted(), "step A should execute")
	assert.False(t, agentMap["input-b"].isExecuted(), "step B should NOT execute (rejected)")

	// The workflow should fail because C depends on B which was skipped.
	require.Error(t, err, "workflow should fail when dependency is not satisfied")
}

// TestHITL_DynamicExecutor_HandlerModifiesData verifies that the interrupt
// handler receives the step's interrupt payload and can return modified data
// in the InterruptResult.
func TestHITL_DynamicExecutor_HandlerModifiesData(t *testing.T) {
	var capturedPayload map[string]any

	agentMap := map[string]*dynHitlAgent{
		"input-a": newDynHitlAgent("step-a", "output-a"),
		"input-b": newDynHitlAgent("step-b", "output-b"),
		"input-c": newDynHitlAgent("step-c", "output-c"),
	}
	registry := createDynHitlRegistry(agentMap)

	dynExec := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint)
	dynExec.WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
		capturedPayload = point.Payload
		return &engine.InterruptResult{
			Approved: true,
			Data:     map[string]any{"override": "modified-value"},
		}, nil
	})

	workflow := newDynHitlWorkflow(&engine.InterruptConfig{
		Message: "Review payload",
		Payload: map[string]any{"key": "original-value"},
	})

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx := context.Background()
	result, err := dynExec.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Verify the handler received the payload.
	require.NotNil(t, capturedPayload, "handler should have received a payload")
	assert.Equal(t, "original-value", capturedPayload["key"])

	// All steps should have completed.
	for _, step := range result.Steps {
		assert.Equal(t, engine.StepStatusCompleted, step.Status)
	}
}

// TestHITL_DynamicExecutor_MultipleInterrupts verifies that a workflow where
// all 3 steps have interrupts executes correctly when every handler approves.
func TestHITL_DynamicExecutor_MultipleInterrupts(t *testing.T) {
	var mu sync.Mutex
	approvedSteps := make(map[string]bool)

	agentMap := map[string]*dynHitlAgent{
		"input-a": newDynHitlAgent("step-a", "output-a"),
		"input-b": newDynHitlAgent("step-b", "output-b"),
		"input-c": newDynHitlAgent("step-c", "output-c"),
	}
	registry := createDynHitlRegistry(agentMap)

	dynExec := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint)
	dynExec.WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
		mu.Lock()
		approvedSteps[point.StepID] = true
		mu.Unlock()
		return &engine.InterruptResult{Approved: true}, nil
	})

	workflow := &engine.Workflow{
		ID:   "dyn-multi-interrupt",
		Name: "Multiple Interrupts Workflow",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A",
				AgentType: "dyn-hitl-mock",
				Input:     "input-a",
				Interrupt: &engine.InterruptConfig{Message: "Approve A"},
			},
			{
				ID:        "step-b",
				Name:      "Step B",
				AgentType: "dyn-hitl-mock",
				Input:     "input-b",
				DependsOn: []string{"step-a"},
				Interrupt: &engine.InterruptConfig{Message: "Approve B"},
			},
			{
				ID:        "step-c",
				Name:      "Step C",
				AgentType: "dyn-hitl-mock",
				Input:     "input-c",
				DependsOn: []string{"step-b"},
				Interrupt: &engine.InterruptConfig{Message: "Approve C"},
			},
		},
		Variables: make(map[string]string),
	}

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx := context.Background()
	result, err := dynExec.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)
	assert.Len(t, result.Steps, 3)

	// All three interrupt handlers should have been called.
	mu.Lock()
	assert.True(t, approvedSteps["step-a"], "step-a interrupt should have been handled")
	assert.True(t, approvedSteps["step-b"], "step-b interrupt should have been handled")
	assert.True(t, approvedSteps["step-c"], "step-c interrupt should have been handled")
	mu.Unlock()

	// All agents should have executed.
	assert.True(t, agentMap["input-a"].isExecuted())
	assert.True(t, agentMap["input-b"].isExecuted())
	assert.True(t, agentMap["input-c"].isExecuted())
}

// TestHITL_DynamicExecutor_ContextCancellation verifies that context
// cancellation during an interrupt handler properly terminates the workflow.
func TestHITL_DynamicExecutor_ContextCancellation(t *testing.T) {
	agentMap := map[string]*dynHitlAgent{
		"input-a": newDynHitlAgent("step-a", "output-a"),
		"input-b": newDynHitlAgent("step-b", "output-b"),
		"input-c": newDynHitlAgent("step-c", "output-c"),
	}
	registry := createDynHitlRegistry(agentMap)

	handlerStarted := make(chan struct{})
	dynExec := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint)
	dynExec.WithHitlHandler(func(ctx context.Context, _ *engine.InterruptPoint) (*engine.InterruptResult, error) {
		close(handlerStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	workflow := newDynHitlWorkflow(&engine.InterruptConfig{
		Message: "Waiting for approval...",
	})

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = dynExec.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.Error(t, err, "should error when context is cancelled during interrupt")
}

// TestHITL_DynamicExecutor_StorePersistence verifies that the interrupt store
// persists interrupt points and cleans them up after approval.
func TestHITL_DynamicExecutor_StorePersistence(t *testing.T) {
	store := engine.NewMemoryInterruptStore()

	agentMap := map[string]*dynHitlAgent{
		"input-a": newDynHitlAgent("step-a", "output-a"),
		"input-b": newDynHitlAgent("step-b", "output-b"),
		"input-c": newDynHitlAgent("step-c", "output-c"),
	}
	registry := createDynHitlRegistry(agentMap)

	dynExec := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint)
	dynExec.WithHitlHandler(func(_ context.Context, _ *engine.InterruptPoint) (*engine.InterruptResult, error) {
		return &engine.InterruptResult{Approved: true}, nil
	})
	dynExec.WithHitlStore(store)

	workflow := newDynHitlWorkflow(&engine.InterruptConfig{
		Message: "Persist this interrupt",
	})

	dag, err := engine.NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ctx := context.Background()
	result, err := dynExec.ExecuteDynamic(ctx, workflow, "initial", dag)
	require.NoError(t, err)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// After approval, the interrupt state should be cleaned up from the store.
	pending, err := store.ListPending(ctx, workflow.ID)
	require.NoError(t, err)
	assert.Empty(t, pending, "interrupt state should be cleaned up after approval")

	// All steps should have completed.
	for _, step := range result.Steps {
		assert.Equal(t, engine.StepStatusCompleted, step.Status)
	}
}
