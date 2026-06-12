// package integration provides end-to-end integration tests for HITL (Human-in-the-Loop)
// workflow execution with real DAG ordering and interrupt handling.
package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/agents/base"
	"goagentx/internal/core/models"
	"goagentx/internal/workflow/engine"
)

// hitlAgent is a test agent for HITL integration tests.
type hitlAgent struct {
	mu       sync.Mutex
	id       string
	executed bool
	output   string
}

func newHitlAgent(id, output string) *hitlAgent {
	return &hitlAgent{id: id, output: output}
}

func (a *hitlAgent) ID() string                    { return a.id }
func (a *hitlAgent) Type() models.AgentType        { return "hitl-mock" }
func (a *hitlAgent) Status() models.AgentStatus    { return models.AgentStatusReady }
func (a *hitlAgent) Start(_ context.Context) error { return nil }
func (a *hitlAgent) Stop(_ context.Context) error  { return nil }

func (a *hitlAgent) Process(_ context.Context, _ any) (any, error) {
	a.mu.Lock()
	a.executed = true
	a.mu.Unlock()
	result := models.NewRecommendResult("test-session", "test-user")
	result.AddItem(&models.RecommendItem{Description: a.output, Content: a.output})
	return result, nil
}

func (a *hitlAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
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

func (a *hitlAgent) isExecuted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.executed
}

// createHitlRegistry creates an AgentRegistry mapping step Input values to hitlAgents.
func createHitlRegistry(agentMap map[string]*hitlAgent) *engine.AgentRegistry {
	registry := engine.NewAgentRegistry()
	_ = registry.Register("hitl-mock", func(_ context.Context, cfg interface{}) (base.Agent, error) {
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

// TestHITLInterruptApproved verifies that a workflow step with an interrupt
// handler executes normally when the handler approves.
func TestHITLInterruptApproved(t *testing.T) {
	agentMap := map[string]*hitlAgent{
		"step-a-input": newHitlAgent("step-a", "output-a"),
		"step-b-input": newHitlAgent("step-b", "output-b"),
	}
	registry := createHitlRegistry(agentMap)
	executor := engine.NewExecutor(registry).
		WithHitlHandler(func(_ context.Context, _ *engine.InterruptPoint) (*engine.InterruptResult, error) {
			return &engine.InterruptResult{
				Approved: true,
				Feedback: "approved for testing",
			}, nil
		})

	workflow := &engine.Workflow{
		ID:   "hitl-approved",
		Name: "HITL Approved Test",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A with interrupt",
				AgentType: "hitl-mock",
				Input:     "step-a-input",
				Interrupt: &engine.InterruptConfig{Message: "Please approve step A"},
			},
			{
				ID:        "step-b",
				Name:      "Step B",
				AgentType: "hitl-mock",
				Input:     "step-b-input",
				DependsOn: []string{"step-a"},
			},
		},
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, workflow, "initial")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)
	assert.Len(t, result.Steps, 2)

	// Both steps should have executed.
	assert.True(t, agentMap["step-a-input"].isExecuted())
	assert.True(t, agentMap["step-b-input"].isExecuted())
}

// TestHITLInterruptRejected verifies that when the interrupt handler rejects,
// the step is marked as skipped and the rejected agent is not executed.
func TestHITLInterruptRejected(t *testing.T) {
	agentMap := map[string]*hitlAgent{
		"step-a-input": newHitlAgent("step-a", "output-a"),
	}
	registry := createHitlRegistry(agentMap)

	var handlerCalled bool
	executor := engine.NewExecutor(registry).
		WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
			handlerCalled = true
			return &engine.InterruptResult{
				Approved: false,
				Feedback: "rejected for testing",
			}, nil
		})

	// Single step workflow: rejection causes the step to be skipped.
	workflow := &engine.Workflow{
		ID:   "hitl-rejected",
		Name: "HITL Rejected Test",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A with interrupt",
				AgentType: "hitl-mock",
				Input:     "step-a-input",
				Interrupt: &engine.InterruptConfig{Message: "Please approve step A"},
			},
		},
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, workflow, "initial")
	require.NoError(t, err)
	require.NotNil(t, result)

	// The handler should have been called.
	assert.True(t, handlerCalled, "interrupt handler should have been called")

	// Step A should be marked as skipped (not executed by the agent).
	foundSkipped := false
	for _, step := range result.Steps {
		if step.StepID == "step-a" {
			assert.Equal(t, engine.StepStatusSkipped, step.Status)
			foundSkipped = true
		}
	}
	assert.True(t, foundSkipped, "step-a should be marked as skipped")

	// The agent should NOT have been executed.
	assert.False(t, agentMap["step-a-input"].isExecuted(),
		"agent should not be executed when interrupt is rejected")
}

// TestHITLInterruptModifiesData verifies that the interrupt handler receives
// the step's interrupt payload and can return data in the result.
func TestHITLInterruptModifiesData(t *testing.T) {
	var capturedPayload map[string]any
	agentMap := map[string]*hitlAgent{
		"step-a-input": newHitlAgent("step-a", "output-a"),
	}
	registry := createHitlRegistry(agentMap)
	executor := engine.NewExecutor(registry).
		WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
			capturedPayload = point.Payload
			return &engine.InterruptResult{
				Approved: true,
				Data:     map[string]any{"override": "modified-value"},
			}, nil
		})

	workflow := &engine.Workflow{
		ID:   "hitl-modify",
		Name: "HITL Modify Data Test",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A with payload",
				AgentType: "hitl-mock",
				Input:     "step-a-input",
				Interrupt: &engine.InterruptConfig{
					Message: "Review payload",
					Payload: map[string]any{"key": "original-value"},
				},
			},
		},
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, workflow, "initial")
	require.NoError(t, err)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// Verify the handler received the payload.
	require.NotNil(t, capturedPayload)
	assert.Equal(t, "original-value", capturedPayload["key"])
}

// TestHITLInterruptStorePersistence verifies that the interrupt store persists
// interrupt points and cleans them up after approval.
func TestHITLInterruptStorePersistence(t *testing.T) {
	store := engine.NewMemoryInterruptStore()
	agentMap := map[string]*hitlAgent{
		"step-a-input": newHitlAgent("step-a", "output-a"),
	}
	registry := createHitlRegistry(agentMap)
	executor := engine.NewExecutor(registry).
		WithHitlHandler(func(_ context.Context, _ *engine.InterruptPoint) (*engine.InterruptResult, error) {
			return &engine.InterruptResult{Approved: true}, nil
		}).
		WithHitlStore(store)

	workflowID := "hitl-store-test"
	workflow := &engine.Workflow{
		ID:   workflowID,
		Name: "HITL Store Test",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A with store",
				AgentType: "hitl-mock",
				Input:     "step-a-input",
				Interrupt: &engine.InterruptConfig{Message: "Persist this"},
			},
		},
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, workflow, "initial")
	require.NoError(t, err)
	assert.Equal(t, engine.WorkflowStatusCompleted, result.Status)

	// After approval, the interrupt state should be cleaned up.
	pending, err := store.ListPending(ctx, workflowID)
	require.NoError(t, err)
	assert.Empty(t, pending, "interrupt state should be cleaned up after approval")
}

// TestHITLMultipleInterruptsInSameWorkflow verifies that multiple steps with
// interrupts can be handled in a single workflow execution.
func TestHITLMultipleInterruptsInSameWorkflow(t *testing.T) {
	var mu sync.Mutex
	approvedSteps := make(map[string]bool)

	agentMap := map[string]*hitlAgent{
		"step-a-input": newHitlAgent("step-a", "output-a"),
		"step-b-input": newHitlAgent("step-b", "output-b"),
		"step-c-input": newHitlAgent("step-c", "output-c"),
	}
	registry := createHitlRegistry(agentMap)
	executor := engine.NewExecutor(registry).
		WithHitlHandler(func(_ context.Context, point *engine.InterruptPoint) (*engine.InterruptResult, error) {
			mu.Lock()
			approvedSteps[point.StepID] = true
			mu.Unlock()
			return &engine.InterruptResult{Approved: true}, nil
		})

	workflow := &engine.Workflow{
		ID:   "hitl-multi",
		Name: "HITL Multiple Interrupts Test",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A",
				AgentType: "hitl-mock",
				Input:     "step-a-input",
				Interrupt: &engine.InterruptConfig{Message: "Approve A"},
			},
			{
				ID:        "step-b",
				Name:      "Step B",
				AgentType: "hitl-mock",
				Input:     "step-b-input",
				DependsOn: []string{"step-a"},
				Interrupt: &engine.InterruptConfig{Message: "Approve B"},
			},
			{
				ID:        "step-c",
				Name:      "Step C",
				AgentType: "hitl-mock",
				Input:     "step-c-input",
				DependsOn: []string{"step-b"},
				Interrupt: &engine.InterruptConfig{Message: "Approve C"},
			},
		},
		Variables: make(map[string]string),
	}

	ctx := context.Background()
	result, err := executor.Execute(ctx, workflow, "initial")
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
}

// TestHITLContextCancellationDuringInterruptWait verifies that context
// cancellation during an interrupt handler properly terminates the workflow.
func TestHITLContextCancellationDuringInterruptWait(t *testing.T) {
	agentMap := map[string]*hitlAgent{
		"step-a-input": newHitlAgent("step-a", "output-a"),
	}
	registry := createHitlRegistry(agentMap)

	handlerStarted := make(chan struct{})
	executor := engine.NewExecutor(registry).
		WithHitlHandler(func(ctx context.Context, _ *engine.InterruptPoint) (*engine.InterruptResult, error) {
			close(handlerStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		})

	workflow := &engine.Workflow{
		ID:   "hitl-cancel",
		Name: "HITL Cancel Test",
		Steps: []*engine.Step{
			{
				ID:        "step-a",
				Name:      "Step A blocks on interrupt",
				AgentType: "hitl-mock",
				Input:     "step-a-input",
				Interrupt: &engine.InterruptConfig{Message: "Waiting for approval..."},
			},
		},
		Variables: make(map[string]string),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := executor.Execute(ctx, workflow, "initial")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
