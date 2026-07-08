package engine

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

// Executor executes workflows based on DAG ordering.
// OutputStore is execution-scoped (created per Execute call) rather than
// executor-scoped, ensuring thread-safety and preventing data races
// when multiple workflows execute concurrently.
type Executor struct {
	mu              sync.RWMutex // protects hitlHandler and hitlStore during concurrent access
	registry        *AgentRegistry
	maxParallel     int
	stepTimeout     time.Duration
	hitlHandler     InterruptHandler
	hitlStore       InterruptStore
	checkpointStore ares_runtime.CheckpointStore // optional, for state persistence
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
	return e.executeWithLoop(ctx, workflow, initialInput, nil)
}

// executeWithLoop runs the workflow with optional loop support.
// iteration records are appended across loop iterations for accumulating step results.
func (e *Executor) executeWithLoop(
	ctx context.Context,
	workflow *Workflow,
	initialInput string,
	existingResults []*StepResult,
) (*WorkflowResult, error) {
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

	localOutputStore := NewOutputStore()
	defer localOutputStore.Close()

	resultChan := make(chan *StepResult, len(workflow.Steps)*2)
	errChan := make(chan error, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.runSteps(ctx, execution, workflow, executionOrder, initialInput, resultChan, errChan, localOutputStore)
	}()

	stepResults := e.collectStepResults(ctx, execution, workflow, resultChan, errChan, done, existingResults)
	if stepResults == nil {
		return nil, ctx.Err()
	}
	if execution.Status == WorkflowStatusFailed {
		// Build a descriptive error that identifies the failing step.
		errMsg := execution.Error
		for _, sr := range stepResults {
			if sr.Status == StepStatusFailed {
				errMsg = fmt.Sprintf("step %s failed: %s", sr.StepID, sr.Error)
				break
			}
		}
		return &WorkflowResult{
			ExecutionID: execution.ID,
			WorkflowID:  workflow.ID,
			Status:      WorkflowStatusFailed,
			Error:       execution.Error,
			Duration:    execution.FinishedAt.Sub(execution.StartedAt),
			Steps:       stepResults,
		}, stderrors.New(errMsg)
	}
	if execution.Status == WorkflowStatusCancelled {
		return nil, ctx.Err()
	}

	// ── Loop handling ──
	if e.shouldContinueLoopRun(workflow.LoopConfig, execution, stepResults) {
		return e.executeWithLoop(ctx, workflow, initialInput, stepResults)
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

// collectStepResults reads step results from resultChan and errChan,
// updating execution state and saving checkpoints. Returns nil on context
// cancellation so the caller can detect early exit.
func (e *Executor) collectStepResults(
	ctx context.Context,
	execution *WorkflowExecution,
	workflow *Workflow,
	resultChan chan *StepResult,
	errChan chan error,
	done chan struct{},
	existingResults []*StepResult,
) []*StepResult {
	var stepResults []*StepResult
	if existingResults != nil {
		stepResults = existingResults
	}

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

			if e.checkpointStore != nil {
				if err := e.saveCheckpoint(ctx, execution, stepResults, workflow.LoopConfig); err != nil {
					log.Warn("checkpoint save failed (continuing)",
						"execution_id", execution.ID,
						"error", err,
					)
				}
			}

			if result.Status == StepStatusFailed {
				execution.Status = WorkflowStatusFailed
				execution.Error = result.Error
				execution.FinishedAt = time.Now()
				_ = e.waitForDone(done, ctx)
				return stepResults
			}

		case err := <-errChan:
			execution.Status = WorkflowStatusFailed
			execution.FinishedAt = time.Now()
			execution.Error = err.Error()
			_ = e.waitForDone(done, ctx)
			return stepResults

		case <-ctx.Done():
			execution.Status = WorkflowStatusCancelled
			execution.FinishedAt = time.Now()
			_ = e.waitForDone(done, ctx)
			return nil
		}
	}

	// Wait for runSteps goroutine to finish.
	if err := e.waitForDone(done, ctx); err != nil {
		execution.Status = WorkflowStatusFailed
		execution.FinishedAt = time.Now()
		execution.Error = err.Error()
	}
	return stepResults
}

// waitForDone waits for the runSteps goroutine to finish with a safety timeout.
func (e *Executor) waitForDone(done chan struct{}, ctx context.Context) error {
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

// shouldContinueLoopRun checks whether the loop should continue based on
// MaxIterations and UntilCondition. Returns true if another iteration is needed.
func (e *Executor) shouldContinueLoopRun(
	loopConfig *LoopConfig,
	execution *WorkflowExecution,
	stepResults []*StepResult,
) bool {
	if loopConfig == nil || len(loopConfig.LoopSteps) == 0 {
		return false
	}

	loopStepSet := make(map[string]bool, len(loopConfig.LoopSteps))
	for _, ls := range loopConfig.LoopSteps {
		loopStepSet[ls] = true
	}

	loopStepCount := 0
	for _, sr := range stepResults {
		if loopStepSet[sr.StepID] {
			loopStepCount++
		}
	}
	loopRounds := loopStepCount / len(loopConfig.LoopSteps)

	// MaxIterations == 0 means run once (no loop). Only continue when
	// MaxIterations > 0 and the loop hasn't reached the limit yet.
	if loopConfig.MaxIterations <= 0 {
		return false
	}
	if loopRounds >= loopConfig.MaxIterations {
		return false
	}

	if loopConfig.UntilCondition != nil {
		varsCopy := make(map[string]any, len(execution.Variables))
		for k, v := range execution.Variables {
			varsCopy[k] = v
		}
		if loopConfig.UntilCondition(varsCopy, loopRounds) {
			return false
		}
	}

	return true
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
	completed := make(map[string]bool)
	processed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, e.maxParallel)

	// stepDone signals when any step goroutine completes, allowing
	// the scheduler to re-check dependencies without false deadlock detection.
	stepDone := make(chan struct{}, 1)

	stepsByID := buildStepIndex(workflow.Steps)

	// routedSteps holds step IDs that a Router decided to execute.
	routedSteps := make(map[string]bool)
	var routedMu sync.Mutex

	stepIndex := 0
	for stepIndex < len(executionOrder) {
		select {
		case <-ctx.Done():
			wg.Wait()
			close(resultChan)
			return
		default:
		}

		stepID := executionOrder[stepIndex]
		step := stepsByID[stepID]
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

		routedMu.Lock()
		wasRouted := routedSteps[stepID]
		routedMu.Unlock()

		if !canExec && !wasRouted {
			if alreadyProcessed {
				stepIndex++
				continue
			}

			if e.waitForStepDependency(ctx, stepID, stepDone, errChan, &wg, resultChan) {
				continue
			}
			return
		}

		// Evaluate condition before dispatching.
		if e.evaluateAndSkipStep(ctx, step, stepID, execution, resultChan, &mu, completed, &stepIndex) {
			continue
		}

		// Acquire semaphore with cancellation support so the scheduler
		// doesn't block forever on a full semaphore when ctx is cancelled.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			close(resultChan)
			return
		}
		stepIndex++

		e.dispatchStepGoroutine(ctx, step, stepID, execution, workflow, initialInput,
			completed, processed, outputStore, resultChan,
			stepsByID, routedSteps, &routedMu, &mu, &wg, sem, stepDone)
	}

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
			step := stepsByID[sid]
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

// waitForStepDependency blocks until a step's dependencies are met or the
// context is cancelled. Returns true if the caller should continue the loop
// (i.e., re-check dependencies), false if the workflow should abort.
func (e *Executor) waitForStepDependency(
	ctx context.Context,
	stepID string,
	stepDone chan struct{},
	errChan chan error,
	wg *sync.WaitGroup,
	resultChan chan *StepResult,
) (shouldContinue bool) {
	deadlockTimer := time.NewTimer(DefaultDeadlockTimeout)
	defer deadlockTimer.Stop()

	select {
	case <-stepDone:
		return true
	case <-deadlockTimer.C:
		select {
		case errChan <- fmt.Errorf("workflow deadlock detected: step %s waiting for dependencies that may never complete", stepID):
		default:
		}
		wg.Wait()
		close(resultChan)
		return false
	case <-ctx.Done():
		wg.Wait()
		close(resultChan)
		return false
	}
}

// evaluateAndSkipStep checks the step's Condition and skips it if not met.
// Returns true if the step was skipped (caller should continue to next step).
func (e *Executor) evaluateAndSkipStep(
	ctx context.Context,
	step *Step,
	stepID string,
	execution *WorkflowExecution,
	resultChan chan *StepResult,
	mu *sync.Mutex,
	completed map[string]bool,
	stepIndex *int,
) bool {
	if step.Condition == nil {
		return false
	}

	mu.Lock()
	varsCopy := make(map[string]any, len(execution.Variables))
	for k, v := range execution.Variables {
		varsCopy[k] = v
	}
	mu.Unlock()

	if step.Condition(varsCopy) {
		return false
	}

	// Mark as completed for dependency resolution so downstream steps can proceed.
	mu.Lock()
	completed[stepID] = true
	mu.Unlock()

	stepResult := &StepResult{
		StepID: stepID,
		Name:   step.Name,
		Status: StepStatusSkipped,
		Error:  "skipped: condition not met",
	}
	select {
	case resultChan <- stepResult:
	case <-ctx.Done():
	}
	*stepIndex++
	return true
}

// dispatchStepGoroutine starts a goroutine that executes a single workflow step.
// It handles panic recovery, step execution, dynamic routing, and result sending.
func (e *Executor) dispatchStepGoroutine(
	ctx context.Context,
	step *Step,
	stepID string,
	execution *WorkflowExecution,
	workflow *Workflow,
	initialInput string,
	completed map[string]bool,
	processed map[string]bool,
	outputStore *OutputStore,
	resultChan chan *StepResult,
	stepsByID map[string]*Step,
	routedSteps map[string]bool,
	routedMu *sync.Mutex,
	mu *sync.Mutex,
	wg *sync.WaitGroup,
	sem chan struct{},
	stepDone chan struct{},
) {
	sid := stepID
	st := step

	wg.Add(1)
	go func() {
		defer func() {
			<-sem

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

			select {
			case stepDone <- struct{}{}:
			default:
			}
		}()

		result := e.executeStep(ctx, workflow, st, sid, initialInput, completed, outputStore, mu)

		mu.Lock()
		processed[sid] = true
		if result.Status == StepStatusCompleted {
			// Release the lock before calling handleStepRouter so that a
			// Router panic does not leave the mutex in an inconsistent state.
			mu.Unlock()
			e.handleStepRouter(ctx, st, sid, execution, result, stepsByID, routedSteps, routedMu)
			mu.Lock()
			completed[sid] = true
		}
		mu.Unlock()

		select {
		case resultChan <- result:
		case <-ctx.Done():
		}
	}()
}

// handleStepRouter calls the step's Router callback if set, recording the
// routed target in the routedSteps set for the main loop to pick up.
// The caller must NOT hold mu when calling this; if the Router panics it
// must not leave any mutex in an inconsistent state.
func (e *Executor) handleStepRouter(
	ctx context.Context,
	step *Step,
	stepID string,
	execution *WorkflowExecution,
	result *StepResult,
	stepsByID map[string]*Step,
	routedSteps map[string]bool,
	routedMu *sync.Mutex,
) {
	if step.Router == nil {
		return
	}

	varsCopy := make(map[string]any, len(execution.Variables))
	for k, v := range execution.Variables {
		varsCopy[k] = v
	}
	routedID := step.Router(ctx, stepID, varsCopy, result.Output)

	if routedID != "" {
		if _, ok := stepsByID[routedID]; ok && !routedSteps[routedID] {
			routedMu.Lock()
			routedSteps[routedID] = true
			routedMu.Unlock()
		}
	}
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

// buildStepIndex builds a step lookup map from a slice of steps.
func buildStepIndex(steps []*Step) map[string]*Step {
	m := make(map[string]*Step, len(steps))
	for _, s := range steps {
		m[s.ID] = s
	}
	return m
}

// executeStep executes a single step with HITL interrupt handling.
func (e *Executor) executeStep(
	ctx context.Context,
	workflow *Workflow,
	step *Step,
	stepID string,
	initialInput string,
	completed map[string]bool,
	outputStore *OutputStore,
	mu *sync.Mutex,
) *StepResult {
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
		if stderrors.Is(err, ErrInterruptRejected) {
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
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
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
	// If the step has a sub-workflow, execute it recursively instead of calling an agent.
	if step.SubWorkflow != nil {
		timeout := step.Timeout
		if timeout == 0 {
			timeout = DefaultStepTimeout
		}
		subCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		subResult, err := e.executeWithLoop(subCtx, step.SubWorkflow, input, nil)
		if err != nil {
			return "", err
		}
		if subResult.Status != WorkflowStatusCompleted {
			return "", fmt.Errorf("sub-workflow %s failed: %s", step.SubWorkflow.ID, subResult.Error)
		}
		// Aggregate outputs into a single JSON-like string for the parent step.
		var outputs []string
		for _, sr := range subResult.Steps {
			if sr.Output != "" {
				outputs = append(outputs, sr.StepID+": "+sr.Output)
			}
		}
		return strings.Join(outputs, "\n"), nil
	}

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
			log.Warn("failed to cleanup interrupt store", "error", err, "step_id", step.ID)
		}
	}

	return nil
}

// saveCheckpoint persists the current workflow execution state for crash recovery.
// The checkpoint stores accumulated step results so the workflow can be resumed
// from the last completed step if the process restarts.
func (e *Executor) saveCheckpoint(
	ctx context.Context,
	execution *WorkflowExecution,
	stepResults []*StepResult,
	loopConfig *LoopConfig,
) error {
	if e.checkpointStore == nil {
		return nil
	}

	ckpt := ares_runtime.ExperienceCheckpoint{
		SchemaVersion: 1,
		ExecutionID:   execution.ID,
		WorkflowID:    execution.WorkflowID,
		Status:        string(execution.Status),
		Variables:     execution.Variables,
		CreatedAt:     execution.StartedAt,
	}

	// Serialize step results into the checkpoint.
	for _, sr := range stepResults {
		ckpt.StepStates = append(ckpt.StepStates, ares_runtime.StepStateSnapshot{
			StepID: sr.StepID,
			Status: ares_runtime.StepStatus(sr.Status),
			Output: sr.Output,
			Error:  sr.Error,
		})
	}

	data, err := json.Marshal(ckpt)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	key := ares_runtime.CheckpointKey(execution.ID)
	return e.checkpointStore.Save(ctx, key, data)
}
