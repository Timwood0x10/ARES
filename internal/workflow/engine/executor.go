package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/errors"
)

// Executor executes workflows based on DAG ordering.
// OutputStore is execution-scoped (created per Execute call) rather than
// executor-scoped, ensuring thread-safety and preventing data races
// when multiple workflows execute concurrently.
type Executor struct {
	registry    *AgentRegistry
	maxParallel int
	stepTimeout time.Duration
}

// NewExecutor creates a new Executor.
func NewExecutor(registry *AgentRegistry) *Executor {
	return &Executor{
		registry:    registry,
		maxParallel: DefaultMaxParallel,
		stepTimeout: 5 * time.Minute,
	}
}

// Execute executes a workflow.
func (e *Executor) Execute(ctx context.Context, workflow *Workflow, initialInput string) (*WorkflowResult, error) {
	dag, err := NewDAG(workflow.Steps)
	if err != nil {
		return nil, errors.Wrap(err, "create DAG")
	}

	executionOrder, err := dag.GetExecutionOrder()
	if err != nil {
		return nil, errors.Wrap(err, "get execution order")
	}

	execution := &WorkflowExecution{
		ID:         generateExecutionID(),
		WorkflowID: workflow.ID,
		Status:     WorkflowStatusRunning,
		StepStates: make(map[string]*StepState),
		Variables:  make(map[string]interface{}),
		Context:    &models.TaskContext{},
		StartedAt:  time.Now(),
	}

	for k, v := range workflow.Variables {
		execution.Variables[k] = v
	}

	// Create independent OutputStore for this execution to prevent concurrent data corruption
	localOutputStore := NewOutputStore()

	resultChan := make(chan *StepResult, len(workflow.Steps))
	errChan := make(chan error, 1)

	// Use errgroup to manage the runSteps goroutine
	g, gctx := errgroup.WithContext(ctx)
	done := make(chan struct{})
	g.Go(func() error {
		defer close(done)
		e.runSteps(gctx, execution, workflow, executionOrder, initialInput, resultChan, errChan, localOutputStore)
		return nil
	})

	var stepResults []*StepResult
	for i := 0; i < len(workflow.Steps); i++ {
		select {
		case result := <-resultChan:
			if result == nil {
				continue
			}
			stepResults = append(stepResults, result)
			execution.StepStates[result.StepID] = &StepState{
				StepID:     result.StepID,
				Status:     result.Status,
				Output:     result.Output,
				Error:      result.Error,
				FinishedAt: time.Now(),
			}
			if result.Status == StepStatusFailed {
				execution.Status = WorkflowStatusFailed
				execution.Error = result.Error
				execution.FinishedAt = time.Now()
				// Wait for runSteps to finish before returning
				<-done
				return &WorkflowResult{
					ExecutionID: execution.ID,
					WorkflowID:  workflow.ID,
					Status:      WorkflowStatusFailed,
					Error:       result.Error,
					Duration:    execution.FinishedAt.Sub(execution.StartedAt),
					Steps:       stepResults,
				}, fmt.Errorf("step %s failed: %s", result.StepID, result.Error)
			}
		case err := <-errChan:
			execution.Status = WorkflowStatusFailed
			execution.FinishedAt = time.Now()
			// Wait for runSteps to finish before returning
			<-done
			return &WorkflowResult{
				ExecutionID: execution.ID,
				WorkflowID:  workflow.ID,
				Status:      WorkflowStatusFailed,
				Error:       err.Error(),
				Duration:    execution.FinishedAt.Sub(execution.StartedAt),
			}, err
		case <-ctx.Done():
			execution.Status = WorkflowStatusCancelled
			execution.FinishedAt = time.Now()
			// Wait for runSteps to finish before returning
			<-done
			return nil, ctx.Err()
		}
	}

	// Wait for runSteps to finish
	<-done

	execution.Status = WorkflowStatusCompleted
	execution.FinishedAt = time.Now()

	output := make(map[string]interface{})
	for _, result := range stepResults {
		output[result.StepID] = result.Output
	}

	return &WorkflowResult{
		ExecutionID: execution.ID,
		WorkflowID:  workflow.ID,
		Status:      execution.Status,
		Output:      output,
		Duration:    execution.FinishedAt.Sub(execution.StartedAt),
		Steps:       stepResults,
	}, nil
}

// runSteps runs workflow steps in parallel where possible.
func (e *Executor) runSteps(
	ctx context.Context,
	execution *WorkflowExecution,
	workflow *Workflow,
	executionOrder []string,
	initialInput string,
	resultChan chan *StepResult,
	errChan chan error,
	outputStore *OutputStore,
) {
	stepIndex := 0
	completed := make(map[string]bool)
	processed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, e.maxParallel)

	for stepIndex < len(executionOrder) {
		select {
		case <-ctx.Done():
			wg.Wait()
			close(resultChan)
			return
		default:
		}

		stepID := executionOrder[stepIndex]
		step := e.findStep(workflow.Steps, stepID)
		if step == nil {
			errChan <- fmt.Errorf("step %q not found in workflow definition", stepID)
			wg.Wait()
			close(resultChan)
			return
		}

		if !e.canExecute(step, completed, &mu) {
			mu.Lock()
			alreadyProcessed := processed[stepID]
			mu.Unlock()

			if alreadyProcessed {
				stepIndex++
				continue
			}

			// Wait for some goroutines to complete, but with timeout to avoid deadlock
			// Use errgroup to manage the wait goroutine
			waitG, _ := errgroup.WithContext(ctx)
			waitDone := make(chan struct{})
			waitG.Go(func() error {
				defer close(waitDone)
				wg.Wait()
				return nil
			})

			select {
			case <-waitDone:
				// Some goroutines completed, retry
				continue
			case <-time.After(5 * time.Second):
				// Timeout: potential deadlock detected, abort workflow
				errChan <- fmt.Errorf("workflow deadlock detected: step %s waiting for dependencies that may never complete", stepID)
				wg.Wait()
				// Wait for waitG to complete
				_ = waitG.Wait()
				close(resultChan)
				return
			case <-ctx.Done():
				wg.Wait()
				// Wait for waitG to complete
				_ = waitG.Wait()
				close(resultChan)
				return
			}
		}

		sem <- struct{}{}

		stepIndex++

		// Capture current stepID for goroutine
		sid := stepID

		wg.Add(1)
		// Create local errgroup for this step execution
		stepG, stepCtx := errgroup.WithContext(ctx)
		stepG.Go(func() error {
			defer func() {
				// Release semaphore and notify wait group
				<-sem
				wg.Done()

				if r := recover(); r != nil {
					mu.Lock()
					processed[sid] = true
					mu.Unlock()

					result := &StepResult{
						StepID: sid,
						Status: StepStatusFailed,
						Error:  fmt.Sprintf("panic: %v", r),
					}
					select {
					case resultChan <- result:
					case <-stepCtx.Done():
					}
				}
			}()

			result := e.executeStep(stepCtx, workflow, sid, initialInput, completed, outputStore)

			mu.Lock()
			processed[sid] = true
			if result.Status == StepStatusCompleted {
				completed[sid] = true
			}
			mu.Unlock()

			select {
			case resultChan <- result:
			case <-stepCtx.Done():
				return stepCtx.Err()
			}
			return nil
		})

		// Don't wait for individual step, continue to next step
		// The stepG.Wait() will be called when needed for deadlock detection
	}

	// Wait for all step goroutines to complete
	wg.Wait()

	select {
	case <-ctx.Done():
		close(resultChan)
		return
	default:
	}

	mu.Lock()
	allCompleted := len(completed) == len(workflow.Steps)
	mu.Unlock()

	if allCompleted {
		close(resultChan)
		return
	}

	pending := false
	for _, sid := range executionOrder {
		mu.Lock()
		isProcessed := processed[sid]
		mu.Unlock()

		if !isProcessed {
			step := e.findStep(workflow.Steps, sid)
			if step == nil || !e.canExecute(step, completed, &mu) {
				pending = true
				break
			}
		}
	}

	if pending {
		select {
		case errChan <- ErrWorkflowIncomplete:
		case <-ctx.Done():
		}
	}
	close(resultChan)
}

// canExecute checks if a step can be executed.
func (e *Executor) canExecute(step *Step, completed map[string]bool, mu *sync.Mutex) bool {
	mu.Lock()
	defer mu.Unlock()
	for _, dep := range step.DependsOn {
		if !completed[dep] {
			return false
		}
	}
	return true
}

// findStep finds a step by ID.
func (e *Executor) findStep(steps []*Step, stepID string) *Step {
	for _, step := range steps {
		if step.ID == stepID {
			return step
		}
	}
	return nil
}

// executeStep executes a single step.
func (e *Executor) executeStep(
	ctx context.Context,
	workflow *Workflow,
	stepID string,
	initialInput string,
	completed map[string]bool,
	outputStore *OutputStore,
) *StepResult {
	step := e.findStep(workflow.Steps, stepID)
	if step == nil {
		return &StepResult{
			StepID: stepID,
			Status: StepStatusFailed,
			Error:  "step not found",
		}
	}

	startTime := time.Now()

	completedCopy := make(map[string]bool)
	for k, v := range completed {
		completedCopy[k] = v
	}
	input := e.resolveInput(step, initialInput, completedCopy, outputStore)

	output, err := e.executeWithRetry(ctx, step, input)

	result := &StepResult{
		StepID:   stepID,
		Name:     step.Name,
		Status:   StepStatusCompleted,
		Output:   output,
		Duration: time.Since(startTime),
	}

	if err != nil {
		result.Status = StepStatusFailed
		result.Error = err.Error()
	}

	outputStore.Set(stepID, &StepOutput{
		StepID:    stepID,
		Output:    output,
		Variables: make(map[string]interface{}),
	})

	return result
}

// resolveInput resolves the input for a step.
func (e *Executor) resolveInput(step *Step, initialInput string, completed map[string]bool, outputStore *OutputStore) string {
	if len(step.DependsOn) == 0 {
		// For steps with no dependencies, replace {{.input}} with initialInput
		if step.Input != "" {
			return e.replaceTemplateVariables(step.Input, initialInput, nil, outputStore)
		}
		return initialInput
	}

	if step.Input != "" {
		// For steps with dependencies, replace template variables with actual outputs
		return e.replaceTemplateVariables(step.Input, initialInput, completed, outputStore)
	}

	// Fallback: concatenate all dependency outputs
	var depsOutput string
	for _, dep := range step.DependsOn {
		if output, exists := outputStore.Get(dep); exists {
			if depsOutput != "" {
				depsOutput += "\n\n"
			}
			depsOutput += output.Output
		}
	}

	if depsOutput != "" {
		return depsOutput
	}

	return initialInput
}

// replaceTemplateVariables replaces template variables in input with actual values.
func (e *Executor) replaceTemplateVariables(input, initialInput string, completed map[string]bool, outputStore *OutputStore) string {
	result := input

	// Replace {{.input}} with initial input
	result = strings.ReplaceAll(result, "{{.input}}", initialInput)

	// Replace {{.step_id}} templates with actual outputs
	// Find all template variables
	replacements := make(map[string]string)

	// Collect outputs from completed steps
	for stepID := range completed {
		if output, exists := outputStore.Get(stepID); exists {
			replacements[fmt.Sprintf("{{.%s}}", stepID)] = output.Output
		}
	}

	// Apply replacements
	for template, value := range replacements {
		result = strings.ReplaceAll(result, template, value)
	}

	return result
}

// executeWithRetry executes a step with retry logic.
func (e *Executor) executeWithRetry(ctx context.Context, step *Step, input string) (string, error) {
	maxAttempts := 1
	initialDelay := time.Second

	if step.RetryPolicy != nil {
		maxAttempts = step.RetryPolicy.MaxAttempts
		initialDelay = step.RetryPolicy.InitialDelay
	}

	var lastErr error
	delay := initialDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		output, err := e.executeSingle(ctx, step, input)
		if err == nil {
			return output, nil
		}

		lastErr = err

		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}

			if step.RetryPolicy != nil {
				delay = time.Duration(float64(delay) * step.RetryPolicy.BackoffMultiplier)
				if delay > step.RetryPolicy.MaxDelay {
					delay = step.RetryPolicy.MaxDelay
				}
			}
		}
	}

	return "", lastErr
}

// executeSingle executes a step once.
func (e *Executor) executeSingle(ctx context.Context, step *Step, input string) (string, error) {
	timeout := step.Timeout
	if timeout == 0 {
		timeout = DefaultStepTimeout
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	executor := NewAgentExecutor(e.registry)
	return executor.Execute(stepCtx, step, input, &models.TaskContext{})
}

// generateExecutionID generates a unique execution ID using atomic counter.
var executionIDCounter uint64

func generateExecutionID() string {
	id := atomic.AddUint64(&executionIDCounter, 1)
	return fmt.Sprintf("exec-%d-%d", time.Now().UnixNano(), id)
}
