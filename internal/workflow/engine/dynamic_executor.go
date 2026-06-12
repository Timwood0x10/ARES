package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"goagentx/internal/core/models"
)

// ApplyMode controls when graph mutations take effect during execution.
type ApplyMode int

const (
	// ApplyAtCheckpoint recomputes execution order after each step completes.
	ApplyAtCheckpoint ApplyMode = iota
	// ApplyImmediate recomputes execution order before each step starts.
	ApplyImmediate
)

// ExecutorOption configures the underlying Executor.
type ExecutorOption func(*Executor)

// WithMaxParallel sets the max parallel steps.
func WithMaxParallel(n int) ExecutorOption {
	return func(e *Executor) {
		e.maxParallel = n
	}
}

// WithStepTimeout sets the step timeout.
func WithStepTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) {
		e.stepTimeout = d
	}
}

// DynamicExecutor extends Executor to support mid-execution graph mutations.
type DynamicExecutor struct {
	*Executor
	applyMode   ApplyMode
	hitlHandler InterruptHandler
	hitlStore   InterruptStore
}

// NewDynamicExecutor creates a DynamicExecutor with the given registry and options.
func NewDynamicExecutor(registry *AgentRegistry, applyMode ApplyMode, opts ...ExecutorOption) *DynamicExecutor {
	executor := &Executor{
		registry:    registry,
		maxParallel: DefaultMaxParallel,
		stepTimeout: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt(executor)
	}
	return &DynamicExecutor{
		Executor:  executor,
		applyMode: applyMode,
	}
}

// WithHitlHandler sets the interrupt handler for human-in-the-loop support.
func (e *DynamicExecutor) WithHitlHandler(handler InterruptHandler) *DynamicExecutor {
	e.hitlHandler = handler
	return e
}

// WithHitlStore sets the interrupt store for crash recovery.
func (e *DynamicExecutor) WithHitlStore(store InterruptStore) *DynamicExecutor {
	e.hitlStore = store
	return e
}

// dynamicExecIDCounter is an atomic counter for dynamic execution IDs.
var dynamicExecIDCounter uint64

func generateDynamicExecutionID() string {
	id := atomic.AddUint64(&dynamicExecIDCounter, 1)
	return fmt.Sprintf("dyn-exec-%d-%d", time.Now().UnixNano(), id)
}

// ExecuteDynamic executes a workflow on a MutableDAG, applying mutations between steps.
func (e *DynamicExecutor) ExecuteDynamic(
	ctx context.Context,
	workflow *Workflow,
	initialInput string,
	mutableDAG *MutableDAG,
) (*WorkflowResult, error) {
	if workflow == nil {
		return nil, errors.New("workflow must not be nil")
	}
	if mutableDAG == nil {
		return nil, errors.New("mutableDAG must not be nil")
	}

	executionOrder, err := mutableDAG.GetExecutionOrder()
	if err != nil {
		return nil, fmt.Errorf("get execution order: %w", err)
	}

	execution := &WorkflowExecution{
		ID:         generateDynamicExecutionID(),
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
	resultChan := make(chan *StepResult, len(executionOrder))
	errChan := make(chan error, 1)

	completed := make(map[string]bool)
	processed := make(map[string]bool)
	var mu sync.Mutex
	stepEg, _ := errgroup.WithContext(ctx)

	sem := make(chan struct{}, e.maxParallel)

	// Track the DAG version for mutation detection.
	lastVersion := mutableDAG.Version()

	// Build a mutable order slice shared between runner and recompute calls.
	// Use a pointer wrapper so recomputeOrder can append new steps.
	orderSlice := make([]string, len(executionOrder))
	copy(orderSlice, executionOrder)
	currentOrder := &orderSlice

	var stepResults []*StepResult

	g, gctx := errgroup.WithContext(ctx)
	done := make(chan struct{})

	g.Go(func() error {
		defer close(done)
		e.runDynamicSteps(
			gctx,
			execution,
			workflow,
			mutableDAG,
			initialInput,
			currentOrder,
			&lastVersion,
			completed,
			processed,
			&mu,
			stepEg,
			sem,
			resultChan,
			errChan,
			localOutputStore,
		)
		return nil
	})

	// Collect results. Update expected count after each result to handle DAG expansion.
	collected := 0
	for {
		// Re-read expected count under lock to pick up DAG expansions.
		mu.Lock()
		expectedResults := len(*currentOrder)
		mu.Unlock()
		if collected >= expectedResults {
			// Check once more after a brief yield to catch late-arriving results.
			select {
			case result, ok := <-resultChan:
				if !ok {
					break
				}
				if result != nil {
					collected++
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
				}
			default:
				// No more results pending. Re-check expected in case DAG grew.
				mu.Lock()
				newExpected := len(*currentOrder)
				mu.Unlock()
				if collected >= newExpected {
					break
				}
				// DAG expanded, continue collecting.
				continue
			}
			// Final check after drain.
			mu.Lock()
			finalExpected := len(*currentOrder)
			mu.Unlock()
			if collected >= finalExpected {
				break
			}
			continue
		}

		select {
		case result := <-resultChan:
			if result == nil {
				continue
			}
			collected++
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
			<-done
			return &WorkflowResult{
				ExecutionID: execution.ID,
				WorkflowID:  workflow.ID,
				Status:      WorkflowStatusFailed,
				Error:       err.Error(),
				Duration:    execution.FinishedAt.Sub(execution.StartedAt),
				Steps:       stepResults,
			}, err
		case <-ctx.Done():
			execution.Status = WorkflowStatusCancelled
			execution.FinishedAt = time.Now()
			<-done
			return nil, ctx.Err()
		}
	}

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

// runDynamicSteps runs workflow steps with support for dynamic reordering.
func (e *DynamicExecutor) runDynamicSteps(
	ctx context.Context,
	execution *WorkflowExecution,
	workflow *Workflow,
	mutableDAG *MutableDAG,
	initialInput string,
	currentOrder *[]string,
	lastVersion *uint64,
	completed map[string]bool,
	processed map[string]bool,
	mu *sync.Mutex,
	stepEg *errgroup.Group,
	sem chan struct{},
	resultChan chan *StepResult,
	errChan chan error,
	outputStore *OutputStore,
) {
	stepIndex := 0

	// H3 fix: use a dedicated stepDone channel for dependency waiting
	// instead of stepEg.Wait() which races with stepEg.Go().
	stepDone := make(chan struct{}, 1)

	for {
		mu.Lock()
		orderLen := len(*currentOrder)
		mu.Unlock()
		if stepIndex >= orderLen {
			break
		}
		select {
		case <-ctx.Done():
			_ = stepEg.Wait()
			close(resultChan)
			return
		default:
		}

		// In ApplyImmediate mode, check for mutations before each step.
		if e.applyMode == ApplyImmediate {
			e.recomputeOrder(mutableDAG, lastVersion, currentOrder, completed, processed, mu)
		}

		mu.Lock()
		order := *currentOrder
		mu.Unlock()
		stepID := order[stepIndex]
		step := e.findStepInDAG(mutableDAG, stepID)
		if step == nil {
			// H2 fix: send synthetic result so the collection loop does not hang.
			mu.Lock()
			processed[stepID] = true
			mu.Unlock()
			select {
			case resultChan <- &StepResult{
				StepID: stepID,
				Status: StepStatusSkipped,
			}:
			case <-ctx.Done():
				_ = stepEg.Wait()
				close(resultChan)
				return
			}
			stepIndex++
			continue
		}

		if !e.canExecute(step, completed, mu) {
			mu.Lock()
			alreadyProcessed := processed[stepID]
			mu.Unlock()

			if alreadyProcessed {
				stepIndex++
				continue
			}

			// H3 fix: wait for any step goroutine to complete via stepDone channel,
			// instead of stepEg.Wait() which blocks until ALL goroutines finish
			// and races with concurrent stepEg.Go() calls.
			select {
			case <-stepDone:
				// Some goroutine completed, re-check dependencies.
				continue
			case <-time.After(5 * time.Second):
				// Timeout: potential deadlock detected.
				errChan <- fmt.Errorf("workflow deadlock detected: step %s waiting for dependencies", stepID)
				_ = stepEg.Wait()
				close(resultChan)
				return
			case <-ctx.Done():
				_ = stepEg.Wait()
				close(resultChan)
				return
			}
		}

		sem <- struct{}{}

		stepIndex++

		sid := stepID

		// Check for HITL interrupt before dispatching the step goroutine.
		if step.Interrupt != nil && e.hitlHandler == nil {
			slog.Warn("step has interrupt config but no HITL handler, skipping interrupt check",
				"step_id", step.ID)
		}
		if step.Interrupt != nil && e.hitlHandler != nil {
			if handled := e.handleDynamicInterrupt(
				ctx, execution.ID, step, resultChan, mu, processed,
			); handled {
				// stepIndex already incremented above; release semaphore and continue.
				<-sem
				continue
			}
		}

		stepEg.Go(func() error {
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

				// H3 fix: signal stepDone so the scheduler can re-check dependencies.
				select {
				case stepDone <- struct{}{}:
				default:
				}
			}()

			startTime := time.Now()
			result := e.executeStepCore(ctx, step, sid, initialInput, completed, outputStore, mu, startTime)

			mu.Lock()
			processed[sid] = true
			if result.Status == StepStatusCompleted {
				completed[sid] = true
			}
			mu.Unlock()

			// In ApplyAtCheckpoint mode, check for mutations after each step completes.
			if e.applyMode == ApplyAtCheckpoint && result.Status == StepStatusCompleted {
				e.recomputeOrder(mutableDAG, lastVersion, currentOrder, completed, processed, mu)
			}

			select {
			case resultChan <- result:
			case <-ctx.Done():
			}
			return nil
		})
	}

	// Wait for all step goroutines to complete.
	_ = stepEg.Wait()

	select {
	case <-ctx.Done():
		close(resultChan)
		return
	default:
	}

	// Check for unprocessed steps (e.g., from mutations that added new steps).
	mu.Lock()
	pending := false
	for _, sid := range *currentOrder {
		if !processed[sid] {
			pending = true
			break
		}
	}
	mu.Unlock()

	if pending {
		select {
		case errChan <- ErrWorkflowIncomplete:
		case <-ctx.Done():
		}
	}

	close(resultChan)
}

// handleDynamicInterrupt processes HITL interrupt for a step in the dynamic
// executor. It blocks until the human responds. Returns true if the step was
// handled (approved, rejected, or errored) and should be skipped by the caller.
// Returns false if the step has no interrupt configured.
func (e *DynamicExecutor) handleDynamicInterrupt(
	ctx context.Context,
	executionID string,
	step *Step,
	resultChan chan *StepResult,
	mu *sync.Mutex,
	processed map[string]bool,
) bool {
	if step.Interrupt == nil || e.hitlHandler == nil {
		return false
	}

	point := &InterruptPoint{
		StepID:  step.ID,
		Message: step.Interrupt.Message,
		Payload: step.Interrupt.Payload,
	}

	// Save to store for crash recovery.
	if e.hitlStore != nil {
		if err := e.hitlStore.Save(ctx, executionID, point); err != nil {
			slog.Warn("failed to save interrupt point", "error", err, "step_id", step.ID)
		}
	}

	// Call handler (blocks until human responds).
	result, err := e.hitlHandler(ctx, point)
	if err != nil {
		// Handler error -> fail the step.
		select {
		case resultChan <- &StepResult{
			StepID: step.ID,
			Name:   step.Name,
			Status: StepStatusFailed,
			Error:  err.Error(),
		}:
		case <-ctx.Done():
		}
		mu.Lock()
		processed[step.ID] = true
		mu.Unlock()
		return true
	}

	if result != nil && !result.Approved {
		// Human rejected -> skip the step.
		select {
		case resultChan <- &StepResult{
			StepID: step.ID,
			Name:   step.Name,
			Status: StepStatusSkipped,
			Error:  "rejected by human",
		}:
		case <-ctx.Done():
		}
		mu.Lock()
		processed[step.ID] = true
		mu.Unlock()

		// Clean up interrupt from store on rejection.
		if e.hitlStore != nil {
			_ = e.hitlStore.Delete(ctx, executionID, step.ID)
		}
		return true
	}

	// Approved: clean up interrupt from store.
	if e.hitlStore != nil {
		_ = e.hitlStore.Delete(ctx, executionID, step.ID)
	}

	// Return false to let the step proceed to execution.
	return false
}

// recomputeOrder checks if the DAG version changed and appends new steps to the order.
func (e *DynamicExecutor) recomputeOrder(
	mutableDAG *MutableDAG,
	lastVersion *uint64,
	currentOrder *[]string,
	completed map[string]bool,
	processed map[string]bool,
	mu *sync.Mutex,
) {
	// M9 fix: hold mu across the entire version-check-and-update operation
	// to prevent concurrent recomputeOrder calls from both detecting the
	// same version change and appending duplicate steps.
	mu.Lock()
	defer mu.Unlock()

	currentVersion := mutableDAG.Version()
	if *lastVersion == currentVersion {
		return
	}

	newOrder, err := mutableDAG.GetExecutionOrder()
	if err != nil {
		slog.Warn("recomputeOrder failed, keeping existing order",
			"error", err,
			"version", currentVersion,
		)
		// Update lastVersion to prevent repeated detection of the same cycle.
		*lastVersion = currentVersion
		return
	}

	*lastVersion = currentVersion

	// Find steps in newOrder not yet in currentOrder.
	existing := make(map[string]bool, len(*currentOrder))
	for _, id := range *currentOrder {
		existing[id] = true
	}

	for _, id := range newOrder {
		if !existing[id] {
			*currentOrder = append(*currentOrder, id)
		}
	}
}

// findStepInDAG finds a step by ID in the MutableDAG.
func (e *DynamicExecutor) findStepInDAG(mutableDAG *MutableDAG, stepID string) *Step {
	steps := mutableDAG.Steps()
	for _, step := range steps {
		if step.ID == stepID {
			return step
		}
	}
	return nil
}
