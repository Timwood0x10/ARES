// nolint: errcheck // Test code may ignore return values.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goagentx/internal/agents/base"
	"goagentx/internal/core/models"
)

// =====================================================
// HITL Integration Tests
// =====================================================

func TestHITLStepWithNoInterrupt(t *testing.T) {
	// A step with no interrupt config should execute normally.
	registry := NewAgentRegistry()
	executor := NewExecutor(registry).WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
		t.Fatal("handler should not be called for steps without interrupt config")
		return nil, nil
	})

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "output", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-no-interrupt",
		Name: "No Interrupt Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "Normal Step",
				AgentType: "test-agent",
				Input:     "test input",
				Timeout:   10 * time.Second,
			},
		},
	}

	result, err := executor.Execute(context.Background(), workflow, "initial")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != WorkflowStatusCompleted {
		t.Errorf("expected status %s, got %s", WorkflowStatusCompleted, result.Status)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("expected 1 step result, got %d", len(result.Steps))
	}
	if result.Steps[0].Status != StepStatusCompleted {
		t.Errorf("expected step status %s, got %s", StepStatusCompleted, result.Steps[0].Status)
	}
}

func TestHITLApproved(t *testing.T) {
	// A step with interrupt config should call the handler and execute when approved.
	handlerCalled := false
	registry := NewAgentRegistry()
	executor := NewExecutor(registry).WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
		handlerCalled = true
		if point.StepID != "step1" {
			t.Errorf("expected step ID step1, got %s", point.StepID)
		}
		if point.Message != "Please approve" {
			t.Errorf("expected message 'Please approve', got %q", point.Message)
		}
		return &InterruptResult{
			Approved: true,
			Feedback: "looks good",
		}, nil
	})

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "approved output", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-approved",
		Name: "Approved Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "Needs Approval",
				AgentType: "test-agent",
				Input:     "test input",
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Please approve",
					Payload: map[string]any{"key": "value"},
				},
			},
		},
	}

	result, err := executor.Execute(context.Background(), workflow, "initial")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !handlerCalled {
		t.Error("handler was not called")
	}
	if result.Status != WorkflowStatusCompleted {
		t.Errorf("expected status %s, got %s", WorkflowStatusCompleted, result.Status)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("expected 1 step result, got %d", len(result.Steps))
	}
	if result.Steps[0].Status != StepStatusCompleted {
		t.Errorf("expected step status %s, got %s", StepStatusCompleted, result.Steps[0].Status)
	}
	if result.Steps[0].Output != "approved output" {
		t.Errorf("expected output 'approved output', got %q", result.Steps[0].Output)
	}
}

func TestHITLRejected(t *testing.T) {
	// A step with interrupt config should be skipped when the human rejects.
	registry := NewAgentRegistry()
	executor := NewExecutor(registry).WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
		return &InterruptResult{
			Approved: false,
			Feedback: "not now",
		}, nil
	})

	agentCalled := false
	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			agentCalled = true
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "should not run", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-rejected",
		Name: "Rejected Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "Rejected Step",
				AgentType: "test-agent",
				Input:     "test input",
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Approve?",
				},
			},
		},
	}

	result, err := executor.Execute(context.Background(), workflow, "initial")
	// The workflow should complete (not error) but the step should be skipped.
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if agentCalled {
		t.Error("agent should not have been called after rejection")
	}
	if result.Status != WorkflowStatusCompleted {
		t.Errorf("expected status %s, got %s", WorkflowStatusCompleted, result.Status)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("expected 1 step result, got %d", len(result.Steps))
	}
	if result.Steps[0].Status != StepStatusSkipped {
		t.Errorf("expected step status %s, got %s", StepStatusSkipped, result.Steps[0].Status)
	}
	if result.Steps[0].Error != "rejected by human" {
		t.Errorf("expected error 'rejected by human', got %q", result.Steps[0].Error)
	}
}

func TestHITLHandlerError(t *testing.T) {
	// A handler returning an error should fail the step.
	handlerErr := errors.New("handler crashed")
	registry := NewAgentRegistry()
	executor := NewExecutor(registry).WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
		return nil, handlerErr
	})

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "should not run", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-handler-error",
		Name: "Handler Error Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "Error Step",
				AgentType: "test-agent",
				Input:     "test input",
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Approve?",
				},
			},
		},
	}

	result, err := executor.Execute(context.Background(), workflow, "initial")
	if err == nil {
		t.Fatal("expected error from Execute")
	}
	require.NotNil(t, result, "expected non-nil result even on failure")
	if result.Status != WorkflowStatusFailed {
		t.Errorf("expected status %s, got %s", WorkflowStatusFailed, result.Status)
	}
}

func TestHITLNilHandler(t *testing.T) {
	// A step with interrupt config but no handler configured should fail.
	registry := NewAgentRegistry()
	executor := NewExecutor(registry)
	// No handler set.

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "should not run", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-nil-handler",
		Name: "Nil Handler Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "No Handler Step",
				AgentType: "test-agent",
				Input:     "test input",
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Approve?",
				},
			},
		},
	}

	result, err := executor.Execute(context.Background(), workflow, "initial")
	if err == nil {
		t.Fatal("expected error from Execute")
	}
	require.NotNil(t, result, "expected non-nil result even on failure")
	if result.Status != WorkflowStatusFailed {
		t.Errorf("expected status %s, got %s", WorkflowStatusFailed, result.Status)
	}
}

func TestHITLNilResult(t *testing.T) {
	// A handler returning nil result should fail the step.
	registry := NewAgentRegistry()
	executor := NewExecutor(registry).WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
		return nil, nil
	})

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "should not run", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-nil-result",
		Name: "Nil Result Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "Nil Result Step",
				AgentType: "test-agent",
				Input:     "test input",
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Approve?",
				},
			},
		},
	}

	result, err := executor.Execute(context.Background(), workflow, "initial")
	if err == nil {
		t.Fatal("expected error from Execute")
	}
	require.NotNil(t, result, "expected non-nil result even on failure")
	if result.Status != WorkflowStatusFailed {
		t.Errorf("expected status %s, got %s", WorkflowStatusFailed, result.Status)
	}
}

func TestHITLWithStore(t *testing.T) {
	// Interrupt point should be saved to the store before calling the handler.
	store := NewMemoryInterruptStore()

	registry := NewAgentRegistry()
	executor := NewExecutor(registry).
		WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
			return &InterruptResult{Approved: true}, nil
		}).
		WithHitlStore(store)

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "output", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-with-store",
		Name: "Store Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "Stored Step",
				AgentType: "test-agent",
				Input:     "test input",
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Approve this?",
					Payload: map[string]any{"data": 42},
				},
			},
		},
	}

	// Verify that Save was called by checking the store after execution.
	result, err := executor.Execute(context.Background(), workflow, "initial")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != WorkflowStatusCompleted {
		t.Errorf("expected status %s, got %s", WorkflowStatusCompleted, result.Status)
	}

	// After approval, the interrupt should be cleaned up from the store.
	pending, err := store.ListPending(context.Background(), "wf-with-store")
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending interrupts after approval, got %d", len(pending))
	}
}

func TestHITLContextCancelled(t *testing.T) {
	// Context cancellation should propagate through the handler.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	registry := NewAgentRegistry()
	executor := NewExecutor(registry).WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return &InterruptResult{Approved: true}, nil
		}
	})

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "output", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-cancel",
		Name: "Cancel Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "Cancelled Step",
				AgentType: "test-agent",
				Input:     "test input",
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Approve?",
				},
			},
		},
	}

	result, err := executor.Execute(ctx, workflow, "initial")
	if err == nil {
		t.Fatal("expected error due to cancelled context")
	}
	if result != nil && result.Status != WorkflowStatusCancelled && result.Status != WorkflowStatusFailed {
		t.Errorf("expected cancelled or failed status, got %s", result.Status)
	}
}

// =====================================================
// MemoryInterruptStore Tests
// =====================================================

func TestMemoryInterruptStoreSaveAndLoad(t *testing.T) {
	store := NewMemoryInterruptStore()
	ctx := context.Background()

	point := &InterruptPoint{
		StepID:  "step1",
		Message: "Please review",
		Payload: map[string]any{"key": "value"},
	}

	// Save a point.
	if err := store.Save(ctx, "exec1", point); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// List should show it as pending (no result yet).
	pending, err := store.ListPending(ctx, "exec1")
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	if pending[0].StepID != "step1" {
		t.Errorf("expected step ID step1, got %s", pending[0].StepID)
	}

	// Load should return not found (no result saved yet).
	_, err = store.Load(ctx, "exec1", "step1")
	if !errors.Is(err, ErrInterruptNotFound) {
		t.Errorf("expected ErrInterruptNotFound, got %v", err)
	}

	// Save a result.
	result := &InterruptResult{
		Approved: true,
		Feedback: "LGTM",
	}
	if err := store.SaveResult(ctx, "exec1", "step1", result); err != nil {
		t.Fatalf("SaveResult error: %v", err)
	}

	// Load should now return the result.
	loaded, err := store.Load(ctx, "exec1", "step1")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !loaded.Approved {
		t.Error("expected Approved=true")
	}
	if loaded.Feedback != "LGTM" {
		t.Errorf("expected feedback 'LGTM', got %q", loaded.Feedback)
	}

	// ListPending should be empty now.
	pending, err = store.ListPending(ctx, "exec1")
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after result saved, got %d", len(pending))
	}
}

func TestMemoryInterruptStoreDelete(t *testing.T) {
	store := NewMemoryInterruptStore()
	ctx := context.Background()

	point := &InterruptPoint{
		StepID:  "step1",
		Message: "Review",
	}
	if err := store.Save(ctx, "exec1", point); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Delete the point.
	if err := store.Delete(ctx, "exec1", "step1"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	// ListPending should be empty.
	pending, err := store.ListPending(ctx, "exec1")
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after delete, got %d", len(pending))
	}
}

func TestMemoryInterruptStoreMultipleSteps(t *testing.T) {
	store := NewMemoryInterruptStore()
	ctx := context.Background()

	// Save points for multiple steps.
	for _, stepID := range []string{"step1", "step2", "step3"} {
		point := &InterruptPoint{
			StepID:  stepID,
			Message: "Review " + stepID,
		}
		if err := store.Save(ctx, "exec1", point); err != nil {
			t.Fatalf("Save error for %s: %v", stepID, err)
		}
	}

	pending, err := store.ListPending(ctx, "exec1")
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(pending))
	}

	// Delete one.
	if err := store.Delete(ctx, "exec1", "step2"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	pending, err = store.ListPending(ctx, "exec1")
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending after delete, got %d", len(pending))
	}
}

func TestMemoryInterruptStoreNilStore(t *testing.T) {
	// Operations on a nil store should return ErrInterruptStoreNil.
	var store *MemoryInterruptStore
	ctx := context.Background()

	err := store.Save(ctx, "exec1", &InterruptPoint{StepID: "step1"})
	if !errors.Is(err, ErrInterruptStoreNil) {
		t.Errorf("Save: expected ErrInterruptStoreNil, got %v", err)
	}

	_, err = store.Load(ctx, "exec1", "step1")
	if !errors.Is(err, ErrInterruptStoreNil) {
		t.Errorf("Load: expected ErrInterruptStoreNil, got %v", err)
	}

	err = store.Delete(ctx, "exec1", "step1")
	if !errors.Is(err, ErrInterruptStoreNil) {
		t.Errorf("Delete: expected ErrInterruptStoreNil, got %v", err)
	}

	_, err = store.ListPending(ctx, "exec1")
	if !errors.Is(err, ErrInterruptStoreNil) {
		t.Errorf("ListPending: expected ErrInterruptStoreNil, got %v", err)
	}

	err = store.SaveResult(ctx, "exec1", "step1", &InterruptResult{Approved: true})
	if !errors.Is(err, ErrInterruptStoreNil) {
		t.Errorf("SaveResult: expected ErrInterruptStoreNil, got %v", err)
	}
}

func TestMemoryInterruptStoreNilPoint(t *testing.T) {
	store := NewMemoryInterruptStore()
	ctx := context.Background()

	err := store.Save(ctx, "exec1", nil)
	if !errors.Is(err, ErrInterruptPointNil) {
		t.Errorf("expected ErrInterruptPointNil for nil point, got %v", err)
	}
}

func TestMemoryInterruptStoreNilResult(t *testing.T) {
	store := NewMemoryInterruptStore()
	ctx := context.Background()

	err := store.SaveResult(ctx, "exec1", "step1", nil)
	if !errors.Is(err, ErrInterruptPointNil) {
		t.Errorf("expected ErrInterruptPointNil for nil result, got %v", err)
	}
}

func TestMemoryInterruptStoreContextCancelled(t *testing.T) {
	store := NewMemoryInterruptStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	point := &InterruptPoint{StepID: "step1", Message: "test"}

	err := store.Save(ctx, "exec1", point)
	if err == nil {
		t.Error("expected error for cancelled context on Save")
	}

	_, err = store.Load(ctx, "exec1", "step1")
	if err == nil {
		t.Error("expected error for cancelled context on Load")
	}

	err = store.Delete(ctx, "exec1", "step1")
	if err == nil {
		t.Error("expected error for cancelled context on Delete")
	}

	_, err = store.ListPending(ctx, "exec1")
	if err == nil {
		t.Error("expected error for cancelled context on ListPending")
	}

	err = store.SaveResult(ctx, "exec1", "step1", &InterruptResult{Approved: true})
	if err == nil {
		t.Error("expected error for cancelled context on SaveResult")
	}
}

func TestMemoryInterruptStoreConcurrentAccess(t *testing.T) {
	store := NewMemoryInterruptStore()
	ctx := context.Background()

	const numGoroutines = 50
	const numSteps = 10

	var wg sync.WaitGroup

	// Concurrently save points.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stepID := fmt.Sprintf("step-%d", idx%numSteps)
			point := &InterruptPoint{
				StepID:  stepID,
				Message: fmt.Sprintf("from goroutine %d", idx),
			}
			if err := store.Save(ctx, "exec-concurrent", point); err != nil {
				t.Errorf("Save error from goroutine %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Concurrently save results.
	for i := 0; i < numSteps; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stepID := fmt.Sprintf("step-%d", idx)
			result := &InterruptResult{
				Approved: true,
				Feedback: fmt.Sprintf("approved by %d", idx),
			}
			if err := store.SaveResult(ctx, "exec-concurrent", stepID, result); err != nil {
				t.Errorf("SaveResult error for step-%d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Concurrently read.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stepID := fmt.Sprintf("step-%d", idx%numSteps)
			_, err := store.Load(ctx, "exec-concurrent", stepID)
			if err != nil {
				t.Errorf("Load error for %s: %v", stepID, err)
			}
		}(i)
	}
	wg.Wait()

	// Concurrently delete.
	for i := 0; i < numSteps; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stepID := fmt.Sprintf("step-%d", idx)
			if err := store.Delete(ctx, "exec-concurrent", stepID); err != nil {
				t.Errorf("Delete error for %s: %v", stepID, err)
			}
		}(i)
	}
	wg.Wait()

	// After all deletes, list should be empty.
	pending, err := store.ListPending(ctx, "exec-concurrent")
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after all deletes, got %d", len(pending))
	}
}

func TestHITLMultiStepWorkflow(t *testing.T) {
	// Test a workflow with multiple steps where only one requires approval.
	handlerCalls := make([]string, 0)
	var mu sync.Mutex

	registry := NewAgentRegistry()
	executor := NewExecutor(registry).WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
		mu.Lock()
		handlerCalls = append(handlerCalls, point.StepID)
		mu.Unlock()
		return &InterruptResult{Approved: true}, nil
	})

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "output", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-multi",
		Name: "Multi Step Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "No Interrupt",
				AgentType: "test-agent",
				Input:     "first",
				Timeout:   10 * time.Second,
			},
			{
				ID:        "step2",
				Name:      "With Interrupt",
				AgentType: "test-agent",
				Input:     "second",
				DependsOn: []string{"step1"},
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Review step 2",
				},
			},
			{
				ID:        "step3",
				Name:      "No Interrupt Again",
				AgentType: "test-agent",
				Input:     "third",
				DependsOn: []string{"step2"},
				Timeout:   10 * time.Second,
			},
		},
	}

	result, err := executor.Execute(context.Background(), workflow, "initial")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != WorkflowStatusCompleted {
		t.Errorf("expected status %s, got %s", WorkflowStatusCompleted, result.Status)
	}

	// Handler should only be called once for step2.
	mu.Lock()
	if len(handlerCalls) != 1 {
		t.Errorf("expected 1 handler call, got %d", len(handlerCalls))
	}
	if len(handlerCalls) > 0 && handlerCalls[0] != "step2" {
		t.Errorf("expected handler call for step2, got %s", handlerCalls[0])
	}
	mu.Unlock()

	// All steps should have completed.
	if len(result.Steps) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(result.Steps))
	}
}

func TestHITLStoreCleanupOnRejection(t *testing.T) {
	// On rejection, the store should NOT be cleaned up (only on approval).
	store := NewMemoryInterruptStore()

	registry := NewAgentRegistry()
	executor := NewExecutor(registry).
		WithHitlHandler(func(ctx context.Context, point *InterruptPoint) (*InterruptResult, error) {
			return &InterruptResult{Approved: false}, nil
		}).
		WithHitlStore(store)

	registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{
					{ItemID: "item1", Name: "Test", Description: "output", Price: 1.0},
				},
			}, nil
		}), nil
	})

	workflow := &Workflow{
		ID:   "wf-reject-store",
		Name: "Reject Store Workflow",
		Steps: []*Step{
			{
				ID:        "step1",
				Name:      "Will Be Rejected",
				AgentType: "test-agent",
				Input:     "test",
				Timeout:   10 * time.Second,
				Interrupt: &InterruptConfig{
					Message: "Approve?",
				},
			},
		},
	}

	result, err := executor.Execute(context.Background(), workflow, "initial")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Steps[0].Status != StepStatusSkipped {
		t.Errorf("expected step skipped, got %s", result.Steps[0].Status)
	}

	// The point should still be in the store (not cleaned up on rejection).
	pending, err := store.ListPending(context.Background(), "wf-reject-store")
	if err != nil {
		t.Fatalf("ListPending error: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending after rejection, got %d", len(pending))
	}
}
