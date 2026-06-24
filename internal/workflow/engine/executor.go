package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

// Executor executes workflows based on DAG ordering.
// OutputStore is execution-scoped (created per Execute call) rather than
// executor-scoped, ensuring thread-safety and preventing data races
// when multiple workflows execute concurrently.
type Executor struct {
	mu          sync.RWMutex // protects hitlHandler and hitlStore during concurrent access
	registry    *AgentRegistry
	maxParallel int
	stepTimeout time.Duration
	hitlHandler InterruptHandler
	hitlStore   InterruptStore
}

// NewExecutor creates a new Executor.
func NewExecutor(registry *AgentRegistry) *Executor {
	return &Executor{
		registry:    registry,
		maxParallel: DefaultMaxParallel,
		stepTimeout: DefaultExecutorStepTimeout,
	}
}

// WithHitlHandler sets the interrupt handler for human-in-the-loop support.
func (e *Executor) WithHitlHandler(handler InterruptHandler) *Executor {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hitlHandler = handler
	return e
}

// WithHitlStore sets the interrupt store for crash recovery.
func (e *Executor) WithHitlStore(store InterruptStore) *Executor {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hitlStore = store
	return e
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
	defer localOutputStore.Close()

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

	// waitForDone waits for the runSteps goroutine to finish with a safety timeout
	// to prevent indefinite blocking if runSteps gets stuck.
	waitForDone := func() error {
		timeout := time.NewTimer(DefaultWorkflowTimeout)
		defer timeout.Stop()
		select {
		case <-done:
			return nil
		case <-timeout.C:
			return fmt.Errorf("workflow execution timed out after %v", DefaultWorkflowTimeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}

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
				if err := waitForDone(); err != nil {
					return &WorkflowResult{
						ExecutionID: execution.ID,
						WorkflowID:  workflow.ID,
						Status:      WorkflowStatusFailed,
						Error:       result.Error,
						Duration:    execution.FinishedAt.Sub(execution.StartedAt),
						Steps:       stepResults,
					}, fmt.Errorf("step %s failed: %s", result.StepID, result.Error)
				}
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
			if waitErr := waitForDone(); waitErr != nil {
				return &WorkflowResult{
					ExecutionID: execution.ID,
					WorkflowID:  workflow.ID,
					Status:      WorkflowStatusFailed,
					Error:       err.Error(),
					Duration:    execution.FinishedAt.Sub(execution.StartedAt),
				}, err
			}
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
			_ = waitForDone()
			return nil, ctx.Err()
		}
	}

	// Wait for runSteps to finish
	if err := waitForDone(); err != nil {
		execution.Status = WorkflowStatusFailed
		execution.FinishedAt = time.Now()
		return &WorkflowResult{
			ExecutionID: execution.ID,
			WorkflowID:  workflow.ID,
			Status:      WorkflowStatusFailed,
			Error:       err.Error(),
			Duration:    execution.FinishedAt.Sub(execution.StartedAt),
			Steps:       stepResults,
		}, err
	}

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

	// stepDone signals when any step goroutine completes, allowing
	// the scheduler to re-check dependencies without false deadlock detection.
	stepDone := make(chan struct{}, 1)

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
			select {
			case errChan <- fmt.Errorf("step %q not found in workflow definition", stepID):
			default:
			}
			wg.Wait()
			close(resultChan)
			return
		}

		mu.Lock()
		canExec := e.canExecute(step, completed)
		alreadyProcessed := processed[stepID]
		mu.Unlock()

		if !canExec {
			if alreadyProcessed {
				stepIndex++
				continue
			}

			// Wait for any step goroutine to complete via stepDone channel,
			// instead of wg.Wait() which blocks until ALL goroutines finish.
			deadlockTimer := time.NewTimer(DefaultDeadlockTimeout)
			select {
			case <-stepDone:
				deadlockTimer.Stop()
				// Some goroutine completed, re-check dependencies.
				continue
			case <-deadlockTimer.C:
				// Timeout: potential deadlock detected, abort workflow.
				select {
				case errChan <- fmt.Errorf("workflow deadlock detected: step %s waiting for dependencies that may never complete", stepID):
				default:
				}
				wg.Wait()
				close(resultChan)
				return
			case <-ctx.Done():
				deadlockTimer.Stop()
				wg.Wait()
				close(resultChan)
				return
			}
		}

		sem <- struct{}{}

		stepIndex++

		// Capture current stepID for goroutine.
		sid := stepID

		wg.Add(1)
		go func() {
			defer func() {
				<-sem

				// C6 fix: recover BEFORE wg.Done() so the main goroutine
				// cannot close resultChan before recovery sends on it.
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
					case <-ctx.Done():
					}
				}

				wg.Done()

				// Signal stepDone so the scheduler can re-check dependencies.
				select {
				case stepDone <- struct{}{}:
				default:
				}
			}()

			result := e.executeStep(ctx, workflow, sid, initialInput, completed, outputStore, &mu)

			mu.Lock()
			processed[sid] = true
			if result.Status == StepStatusCompleted {
				completed[sid] = true
			}
			mu.Unlock()

			select {
			case resultChan <- result:
			case <-ctx.Done():
			}
		}()

		// Don't wait for individual step, continue to next step.
	}

	// Wait for all step goroutines to complete.
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
		if !isProcessed {
			step := e.findStep(workflow.Steps, sid)
			if step == nil || !e.canExecute(step, completed) {
				pending = true
				mu.Unlock()
				break
			}
		}
		mu.Unlock()
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
// Caller must hold the mutex protecting completed.
func (e *Executor) canExecute(step *Step, completed map[string]bool) bool {
	for _, dep := range step.DependsOn {
		if !completed[dep] {
			return false
		}
	}
	return true
}

// canExecuteWithDeps checks if a step can be executed given its dependencies.
// Caller must hold the mutex protecting completed.
// deps must not be the step's own DependsOn slice if concurrent mutations
// may modify it (e.g., from ReplaceNode). Pass a copy for thread safety.
func (e *Executor) canExecuteWithDeps(deps []string, completed map[string]bool) bool {
	for _, dep := range deps {
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

// executeStep executes a single step with HITL interrupt handling.
func (e *Executor) executeStep(
	ctx context.Context,
	workflow *Workflow,
	stepID string,
	initialInput string,
	completed map[string]bool,
	outputStore *OutputStore,
	mu *sync.Mutex,
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

	// HITL: check if this step requires human approval.
	if err := e.handleInterrupt(ctx, workflow, step); err != nil {
		if err == ErrInterruptRejected {
			return &StepResult{
				StepID:   stepID,
				Name:     step.Name,
				Status:   StepStatusSkipped,
				Error:    "rejected by human",
				Duration: time.Since(startTime),
			}
		}
		return &StepResult{
			StepID:   stepID,
			Name:     step.Name,
			Status:   StepStatusFailed,
			Error:    err.Error(),
			Duration: time.Since(startTime),
		}
	}

	return e.executeStepCore(ctx, step, stepID, initialInput, completed, outputStore, mu, startTime)
}

// executeStepCore executes the core step logic (input resolution, agent call,
// retry) without HITL interrupt handling. Used by DynamicExecutor which handles
// HITL at the scheduling level.
func (e *Executor) executeStepCore(
	ctx context.Context,
	step *Step,
	stepID string,
	initialInput string,
	completed map[string]bool,
	outputStore *OutputStore,
	mu *sync.Mutex,
	startTime time.Time,
) *StepResult {
	// Copy completed map under lock to avoid data race with main loop.
	mu.Lock()
	completedCopy := make(map[string]bool, len(completed))
	for k, v := range completed {
		completedCopy[k] = v
	}
	mu.Unlock()
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
	} else {
		outputStore.Set(stepID, &StepOutput{
			StepID:    stepID,
			Output:    output,
			Variables: make(map[string]interface{}),
		})
	}

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

	// M5 fix: clamp maxAttempts to minimum 1 so that MaxAttempts=0
	// does not skip execution entirely.
	if maxAttempts < 1 {
		maxAttempts = 1
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

// handleInterrupt checks if a step requires human approval and processes it.
// Returns nil if no interrupt is configured or the human approved.
// Returns ErrInterruptRejected if the human rejected.
// Returns an error if the handler failed.
func (e *Executor) handleInterrupt(ctx context.Context, workflow *Workflow, step *Step) error {
	if step.Interrupt == nil {
		return nil
	}
	e.mu.RLock()
	handler := e.hitlHandler
	store := e.hitlStore
	e.mu.RUnlock()

	if handler == nil {
		return ErrInterruptHandlerNil
	}

	point := &InterruptPoint{
		StepID:  step.ID,
		Message: step.Interrupt.Message,
		Payload: step.Interrupt.Payload,
	}

	// Persist interrupt point for crash recovery if store is available.
	if store != nil {
		if err := store.Save(ctx, workflow.ID, point); err != nil {
			return fmt.Errorf("save interrupt point: %w", err)
		}
	}

	result, err := handler(ctx, point)
	if err != nil {
		return fmt.Errorf("interrupt handler: %w", err)
	}
	if result == nil {
		return fmt.Errorf("interrupt handler returned nil result")
	}
	if !result.Approved {
		return ErrInterruptRejected
	}

	// Clean up the interrupt state after approval.
	if store != nil {
		if err := store.Delete(ctx, workflow.ID, step.ID); err != nil {
			// Log but do not fail the step on cleanup error.
			slog.Warn("failed to cleanup interrupt store", "error", err, "step_id", step.ID)
		}
	}

	return nil
}
