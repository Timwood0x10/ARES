// nolint: errcheck // Test code may ignore return values
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// =====================================================
// Executor Coverage Tests
// =====================================================

func TestExecutorCoverage(t *testing.T) {
	t.Run("create executor", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		if executor == nil {
			t.Error("Executor should not be nil")
			return
		}

		if executor.maxParallel != 10 {
			t.Errorf("Expected maxParallel 10, got %d", executor.maxParallel)
		}

		if executor.stepTimeout != 300*time.Second {
			t.Errorf("Expected stepTimeout 300s, got %v", executor.stepTimeout)
		}
	})

	t.Run("execute simple workflow", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		// Register a mock agent
		registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{
							ItemID:      "item1",
							Name:        "Test Item",
							Description: "Test result",
							Price:       100.0,
						},
					},
				}, nil
			}), nil
		})

		workflow := &Workflow{
			ID:   "wf1",
			Name: "Test Workflow",
			Steps: []*Step{
				{
					ID:        "step1",
					Name:      "First Step",
					AgentType: "test-agent",
					Input:     "test input",
					Timeout:   10 * time.Second,
				},
			},
		}

		result, err := executor.Execute(context.Background(), workflow, "initial input")
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}

		if result.Status != WorkflowStatusCompleted {
			t.Errorf("Expected status %s, got %s", WorkflowStatusCompleted, result.Status)
		}

		if len(result.Steps) != 1 {
			t.Errorf("Expected 1 step result, got %d", len(result.Steps))
		}
	})

	t.Run("execute workflow with dependencies", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{
							ItemID:      "item1",
							Name:        "Test Item",
							Description: "Test result",
							Price:       100.0,
						},
					},
				}, nil
			}), nil
		})

		workflow := &Workflow{
			ID:   "wf2",
			Name: "Test Workflow with Dependencies",
			Steps: []*Step{
				{
					ID:        "step1",
					Name:      "First Step",
					AgentType: "test-agent",
					Input:     "step1 input",
					Timeout:   10 * time.Second,
				},
				{
					ID:        "step2",
					Name:      "Second Step",
					AgentType: "test-agent",
					DependsOn: []string{"step1"},
					Timeout:   10 * time.Second,
				},
				{
					ID:        "step3",
					Name:      "Third Step",
					AgentType: "test-agent",
					DependsOn: []string{"step1", "step2"},
					Timeout:   10 * time.Second,
				},
			},
		}

		result, err := executor.Execute(context.Background(), workflow, "initial input")
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}

		if result.Status != WorkflowStatusCompleted {
			t.Errorf("Expected status %s, got %s", WorkflowStatusCompleted, result.Status)
		}

		if len(result.Steps) != 3 {
			t.Errorf("Expected 3 step results, got %d", len(result.Steps))
		}
	})

	t.Run("execute workflow with agent error", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		registry.Register("failing-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "failing-agent", func(ctx context.Context, input any) (any, error) {
				return nil, errors.New("agent error")
			}), nil
		})

		workflow := &Workflow{
			ID:   "wf3",
			Name: "Test Workflow with Error",
			Steps: []*Step{
				{
					ID:        "step1",
					Name:      "Failing Step",
					AgentType: "failing-agent",
					Timeout:   10 * time.Second,
				},
			},
		}

		result, err := executor.Execute(context.Background(), workflow, "initial input")
		if err == nil {
			t.Error("Expected error from failing agent")
		}

		if result.Status != WorkflowStatusFailed {
			t.Errorf("Expected status %s, got %s", WorkflowStatusFailed, result.Status)
		}
	})

	t.Run("execute workflow with invalid agent type", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		workflow := &Workflow{
			ID:   "wf4",
			Name: "Test Workflow with Invalid Agent",
			Steps: []*Step{
				{
					ID:        "step1",
					Name:      "Invalid Step",
					AgentType: "non-existent-agent",
					Timeout:   10 * time.Second,
				},
			},
		}

		result, err := executor.Execute(context.Background(), workflow, "initial input")
		if err == nil {
			t.Error("Expected error with non-existent agent type")
		}

		if result.Status != WorkflowStatusFailed {
			t.Errorf("Expected status %s, got %s", WorkflowStatusFailed, result.Status)
		}
	})

	t.Run("execute workflow with context cancellation", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		registry.Register("slow-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "slow-agent", func(ctx context.Context, input any) (any, error) {
				time.Sleep(100 * time.Millisecond)
				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{
							ItemID:      "item1",
							Name:        "Test Item",
							Description: "Test result",
							Price:       100.0,
						},
					},
				}, nil
			}), nil
		})

		workflow := &Workflow{
			ID:   "wf5",
			Name: "Test Workflow with Cancellation",
			Steps: []*Step{
				{
					ID:        "step1",
					Name:      "Slow Step",
					AgentType: "slow-agent",
					Timeout:   1 * time.Second,
				},
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := executor.Execute(ctx, workflow, "initial input")
		if err == nil {
			t.Error("Expected error with cancelled context")
		}
	})
}

// =====================================================
// Executor Helper Functions Coverage Tests
// =====================================================

func TestExecutorHelperFunctionsCoverage(t *testing.T) {
	t.Run("find step by ID", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		workflow := &Workflow{
			Steps: []*Step{
				{ID: "step1", Name: "Step 1"},
				{ID: "step2", Name: "Step 2"},
				{ID: "step3", Name: "Step 3"},
			},
		}

		step := executor.findStep(workflow.Steps, "step2")
		if step == nil {
			t.Error("Step should not be nil")
			return
		}

		if step.ID != "step2" {
			t.Errorf("Expected step ID 'step2', got %s", step.ID)
		}

		nonExistentStep := executor.findStep(workflow.Steps, "non-existent")
		if nonExistentStep != nil {
			t.Error("Non-existent step should be nil")
		}
	})

	t.Run("can execute step", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		step1 := &Step{ID: "step1", DependsOn: []string{}}
		step2 := &Step{ID: "step2", DependsOn: []string{"step1"}}
		step3 := &Step{ID: "step3", DependsOn: []string{"step1", "step2"}}

		completed := make(map[string]bool)
		var mu sync.Mutex

		// Step1 should be executable (no dependencies)
		mu.Lock()
		if !executor.canExecute(step1, completed) {
			t.Error("Step1 should be executable")
		}
		mu.Unlock()

		// Step2 should not be executable yet
		mu.Lock()
		if executor.canExecute(step2, completed) {
			t.Error("Step2 should not be executable yet")
		}
		mu.Unlock()

		// Mark step1 as completed
		mu.Lock()
		completed["step1"] = true
		mu.Unlock()

		// Step2 should now be executable
		mu.Lock()
		if !executor.canExecute(step2, completed) {
			t.Error("Step2 should be executable after step1 completes")
		}
		mu.Unlock()

		// Step3 should not be executable yet
		mu.Lock()
		if executor.canExecute(step3, completed) {
			t.Error("Step3 should not be executable yet")
		}
		mu.Unlock()

		// Mark step2 as completed
		mu.Lock()
		completed["step2"] = true
		mu.Unlock()

		// Step3 should now be executable
		mu.Lock()
		if !executor.canExecute(step3, completed) {
			t.Error("Step3 should be executable after step1 and step2 complete")
		}
		mu.Unlock()
	})

	t.Run("resolve input for step", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)
		outputStore := NewOutputStore()

		// Test step with no dependencies and input
		step1 := &Step{
			ID:    "step1",
			Input: "step1 input",
		}

		completed := make(map[string]bool)
		input := executor.resolveInput(step1, "initial input", completed, outputStore)
		if input != "step1 input" {
			t.Errorf("Expected 'step1 input', got %s", input)
		}

		// Test step with dependencies but with its own input
		step2 := &Step{
			ID:        "step2",
			DependsOn: []string{"step1"},
			Input:     "step2 input",
		}

		input = executor.resolveInput(step2, "initial input", completed, outputStore)
		if input != "step2 input" {
			t.Errorf("Expected 'step2 input', got %s", input)
		}

		// Test step with dependencies and no input
		step3 := &Step{
			ID:        "step3",
			DependsOn: []string{"step1"},
		}

		// Set output for step1
		outputStore.Set("step1", &StepOutput{
			StepID: "step1",
			Output: "step1 output",
		})

		input = executor.resolveInput(step3, "initial input", completed, outputStore)
		if input != "step1 output" {
			t.Errorf("Expected 'step1 output', got %s", input)
		}
	})

	t.Run("execute single step", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)
		outputStore := NewOutputStore()

		registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{
							ItemID:      "item1",
							Name:        "Test Item",
							Description: "Test result",
							Price:       100.0,
						},
					},
				}, nil
			}), nil
		})

		step := &Step{
			ID:        "step1",
			Name:      "Test Step",
			AgentType: "test-agent",
			Input:     "test input",
		}

		completed := make(map[string]bool)
		var mu sync.Mutex
		result := executor.executeStep(context.Background(), &Workflow{
			Steps: []*Step{step},
		}, "step1", "initial input", completed, outputStore, &mu)

		if result.Status != StepStatusCompleted {
			t.Errorf("Expected status %s, got %s", StepStatusCompleted, result.Status)
		}

		if result.Error != "" {
			t.Errorf("Expected no error, got %s", result.Error)
		}
	})

	t.Run("execute step with timeout", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)
		outputStore := NewOutputStore()

		// Register an agent that will take longer than the timeout
		registry.Register("slow-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "slow-agent", func(ctx context.Context, input any) (any, error) {
				// Simulate slow operation that exceeds timeout
				select {
				case <-time.After(200 * time.Millisecond):
					return &models.RecommendResult{
						Items: []*models.RecommendItem{
							{
								ItemID:      "item1",
								Name:        "Test Item",
								Description: "Test result",
								Price:       100.0,
							},
						},
					}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}), nil
		})

		step := &Step{
			ID:        "step1",
			Name:      "Slow Step",
			AgentType: "slow-agent",
			Timeout:   50 * time.Millisecond, // Shorter timeout
		}

		completed := make(map[string]bool)
		var mu sync.Mutex
		result := executor.executeStep(context.Background(), &Workflow{
			Steps: []*Step{step},
		}, "step1", "initial input", completed, outputStore, &mu)

		if result.Status == StepStatusCompleted {
			t.Error("Expected failure due to timeout")
		}
	})
}

// =====================================================
// Retry Logic Coverage Tests
// =====================================================

func TestRetryLogicCoverage(t *testing.T) {
	t.Run("execute with retry policy", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		attemptCount := 0
		registry.Register("flaky-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "flaky-agent", func(ctx context.Context, input any) (any, error) {
				attemptCount++
				if attemptCount < 3 {
					return nil, errors.New("temporary error")
				}
				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{
							ItemID:      "item1",
							Name:        "Test Item",
							Description: "Test result",
							Price:       100.0,
						},
					},
				}, nil
			}), nil
		})

		step := &Step{
			ID:        "step1",
			Name:      "Flaky Step",
			AgentType: "flaky-agent",
			RetryPolicy: &RetryPolicy{
				MaxAttempts:       3,
				InitialDelay:      10 * time.Millisecond,
				MaxDelay:          100 * time.Millisecond,
				BackoffMultiplier: 1.5,
			},
		}

		output, err := executor.executeWithRetry(context.Background(), step, "test input")
		if err != nil {
			t.Errorf("Expected success after retries, got error: %v", err)
		}

		if output == "" {
			t.Error("Expected output after successful retry")
		}

		if attemptCount != 3 {
			t.Errorf("Expected 3 attempts, got %d", attemptCount)
		}
	})

	t.Run("execute with retry policy exhausted", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		registry.Register("failing-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "failing-agent", func(ctx context.Context, input any) (any, error) {
				return nil, errors.New("persistent error")
			}), nil
		})

		step := &Step{
			ID:        "step1",
			Name:      "Failing Step",
			AgentType: "failing-agent",
			RetryPolicy: &RetryPolicy{
				MaxAttempts:  2,
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
			},
		}

		_, err := executor.executeWithRetry(context.Background(), step, "test input")
		if err == nil {
			t.Error("Expected error after exhausting retries")
		}
	})

	t.Run("execute without retry policy", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		registry.Register("test-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "test-agent", func(ctx context.Context, input any) (any, error) {
				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{
							ItemID:      "item1",
							Name:        "Test Item",
							Description: "Test result",
							Price:       100.0,
						},
					},
				}, nil
			}), nil
		})

		step := &Step{
			ID:        "step1",
			Name:      "Test Step",
			AgentType: "test-agent",
		}

		output, err := executor.executeWithRetry(context.Background(), step, "test input")
		if err != nil {
			t.Errorf("Expected success, got error: %v", err)
		}

		if output == "" {
			t.Error("Expected output")
		}
	})
}

// =====================================================
// Workflow Execution State Coverage Tests
// =====================================================

func TestWorkflowExecutionStateCoverage(t *testing.T) {
	t.Run("create workflow execution", func(t *testing.T) {
		execution := &WorkflowExecution{
			ID:         "exec1",
			WorkflowID: "wf1",
			Status:     WorkflowStatusRunning,
			StepStates: map[string]*StepState{
				"step1": {
					StepID: "step1",
					Status: StepStatusRunning,
				},
			},
			Variables: map[string]interface{}{
				"var1": "value1",
			},
			Context:   &models.TaskContext{},
			StartedAt: time.Now(),
		}

		if execution.ID != "exec1" {
			t.Errorf("Expected ID 'exec1', got %s", execution.ID)
		}

		if execution.Status != WorkflowStatusRunning {
			t.Errorf("Expected status %s, got %s", WorkflowStatusRunning, execution.Status)
		}
	})
}

// =====================================================
// Concurrent Execution Tests
// =====================================================

func TestConcurrentExecution(t *testing.T) {
	t.Run("fan-out fan-in workflow", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		registry.Register("branch-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "branch-agent", func(ctx context.Context, input any) (any, error) {
				desc, _ := input.(string)
				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{
							ItemID:      "item1",
							Name:        "Branch Result",
							Description: desc,
							Price:       100.0,
						},
					},
				}, nil
			}), nil
		})

		// Workflow: step1 -> step2a, step2b (parallel) -> step3 (join)
		workflow := &Workflow{
			ID:   "wf-fanout",
			Name: "Fan-out Fan-in Workflow",
			Steps: []*Step{
				{
					ID:        "step1",
					Name:      "Root Step",
					AgentType: "branch-agent",
					Input:     "root input",
					Timeout:   10 * time.Second,
				},
				{
					ID:        "step2a",
					Name:      "Branch A",
					AgentType: "branch-agent",
					DependsOn: []string{"step1"},
					Timeout:   10 * time.Second,
				},
				{
					ID:        "step2b",
					Name:      "Branch B",
					AgentType: "branch-agent",
					DependsOn: []string{"step1"},
					Timeout:   10 * time.Second,
				},
				{
					ID:        "step3",
					Name:      "Join Step",
					AgentType: "branch-agent",
					DependsOn: []string{"step2a", "step2b"},
					Timeout:   10 * time.Second,
				},
			},
		}

		result, err := executor.Execute(context.Background(), workflow, "initial input")
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}

		if result.Status != WorkflowStatusCompleted {
			t.Errorf("Expected status %s, got %s", WorkflowStatusCompleted, result.Status)
		}

		if len(result.Steps) != 4 {
			t.Errorf("Expected 4 step results, got %d", len(result.Steps))
		}
	})

	t.Run("max parallel enforcement", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)
		executor.maxParallel = 2

		var mu sync.Mutex
		concurrentCount := 0
		maxConcurrent := 0

		registry.Register("throttled-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "throttled-agent", func(ctx context.Context, input any) (any, error) {
				mu.Lock()
				concurrentCount++
				if concurrentCount > maxConcurrent {
					maxConcurrent = concurrentCount
				}
				mu.Unlock()

				time.Sleep(50 * time.Millisecond)

				mu.Lock()
				concurrentCount--
				mu.Unlock()

				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{ItemID: "item1", Name: "Test", Description: "result", Price: 100.0},
					},
				}, nil
			}), nil
		})

		steps := make([]*Step, 5)
		for i := range steps {
			steps[i] = &Step{
				ID:        fmt.Sprintf("step%d", i+1),
				Name:      fmt.Sprintf("Step %d", i+1),
				AgentType: "throttled-agent",
				Timeout:   10 * time.Second,
			}
		}

		workflow := &Workflow{
			ID:    "wf-throttle",
			Name:  "Throttle Workflow",
			Steps: steps,
		}

		_, err := executor.Execute(context.Background(), workflow, "input")
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}

		if maxConcurrent > 2 {
			t.Errorf("Expected max 2 concurrent steps, got %d", maxConcurrent)
		}

		if maxConcurrent < 2 {
			t.Errorf("Expected up to 2 concurrent steps, got %d", maxConcurrent)
		}
	})

	t.Run("cancellation mid-execution", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		registry.Register("blocking-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "blocking-agent", func(ctx context.Context, input any) (any, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}), nil
		})

		workflow := &Workflow{
			ID:   "wf-cancel",
			Name: "Cancellation Test",
			Steps: []*Step{
				{
					ID:        "step1",
					Name:      "Blocking Step",
					AgentType: "blocking-agent",
					Timeout:   30 * time.Second,
				},
			},
		}

		ctx, cancel := context.WithCancel(context.Background())

		resultCh := make(chan error, 1)
		go func() {
			_, err := executor.Execute(ctx, workflow, "input")
			resultCh <- err
		}()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case err := <-resultCh:
			if err == nil {
				t.Error("Expected error from cancelled context")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Executor did not respond to cancellation within 5s")
		}
	})

	t.Run("step timeout", func(t *testing.T) {
		registry := NewAgentRegistry()
		executor := NewExecutor(registry)

		registry.Register("slow-agent", func(ctx context.Context, config interface{}) (base.Agent, error) {
			return NewMockAgent("test", "slow-agent", func(ctx context.Context, input any) (any, error) {
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return &models.RecommendResult{
					Items: []*models.RecommendItem{
						{ItemID: "item1", Name: "Slow", Description: "result", Price: 100.0},
					},
				}, nil
			}), nil
		})

		workflow := &Workflow{
			ID:   "wf-timeout",
			Name: "Timeout Test",
			Steps: []*Step{
				{
					ID:        "step1",
					Name:      "Slow Step",
					AgentType: "slow-agent",
					Timeout:   50 * time.Millisecond,
				},
			},
		}

		result, err := executor.Execute(context.Background(), workflow, "input")
		if err == nil {
			t.Error("Expected timeout error")
		}

		if result == nil || result.Status != WorkflowStatusFailed {
			t.Errorf("Expected failed status, got %v", result)
		}
	})
}

// nolint: errcheck // Test code may ignore return values
