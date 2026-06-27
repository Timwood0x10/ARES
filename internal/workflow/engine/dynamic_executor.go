package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/runtime"
)

// applyRoundMutations applies between-round DAG mutations suggested by
// MemoryPlugin (new nodes/edges) and EvolutionPlugin (strategy adjustments).
// This is the core mechanism of the Controlled Evolutionary Loop: the DAG
// evolves between rounds based on execution outcomes.
func (e *DynamicExecutor) applyRoundMutations(ctx context.Context, round int, execution *WorkflowExecution, mutableDAG *MutableDAG) {
	if e.pluginBus == nil {
		return
	}

	// 1. MemoryPlugin: suggest routing paths for next round
	for _, mp := range e.pluginBus.PluginsByCap(runtime.CapMemory) {
		if mem, ok := mp.(runtime.MemoryPlugin); ok {
			advice, err := mem.AdviseRoute(ctx, runtime.RouteState{
				ExecutionID:   execution.ID,
				CurrentStepID: "",
			})
			if err != nil {
				slog.Warn("round mutation: memory advise failed",
					"round", round, "execution_id", execution.ID, "error", err,
				)
				continue
			}
			for _, a := range advice {
				if a.NextStepID != "" && a.Confidence >= 0.5 {
					slog.Debug("round mutation: memory suggests path",
						"round", round, "next_step", a.NextStepID,
						"confidence", a.Confidence,
					)
					// Check if target step exists; add if not.
					// Use a default agent type so the step is executable;
					// the application can override it after creation.
					if _, exists := mutableDAG.StepIndex()[a.NextStepID]; !exists {
						_ = mutableDAG.AddNode(ctx, &Step{
							ID:        a.NextStepID,
							Name:      a.NextStepID,
							AgentType: "default",
						})
					}
				}
			}
		}
	}

	// 2. EvolutionPlugin: strategy recommendations for next round
	for _, ep := range e.pluginBus.PluginsByCap(runtime.CapEvolution) {
		if evo, ok := ep.(runtime.EvolutionPlugin); ok {
			rec, err := evo.Recommend(ctx, runtime.ExecutionState{
				ExecutionID:   execution.ID,
				CurrentStepID: "",
			})
			if err != nil {
				slog.Warn("round mutation: evolution recommend failed",
					"round", round, "execution_id", execution.ID, "error", err,
				)
				continue
			}
			if rec != nil && rec.PreferredAgent != "" {
				slog.Debug("round mutation: evolution suggests agent",
					"round", round, "preferred_agent", rec.PreferredAgent,
				)
				// PreferredAgent influences next round's agent selection.
				// Concrete mutation (mapping agent to step) is done by
				// the application layer via AgentStepResolver.
			}
		}
	}
}

// cleanupCheckpoint calls Cleanup on all registered checkpoint plugins
// to free in-memory snapshots after execution terminates.
func (e *DynamicExecutor) cleanupCheckpoint(executionID string) {
	if e.pluginBus == nil {
		return
	}
	for _, p := range e.pluginBus.PluginsByCap(runtime.CapCheckpoint) {
		if ckp, ok := p.(*runtime.CheckpointPlugin); ok {
			ckp.Cleanup(executionID)
		}
	}
}

// flushCheckpoint calls Flush on all registered checkpoint plugins.
func (e *DynamicExecutor) flushCheckpoint(ctx context.Context, executionID string) {
	if e.pluginBus == nil {
		return
	}
	for _, p := range e.pluginBus.PluginsByCap(runtime.CapCheckpoint) {
		if f, ok := p.(runtime.Flusher); ok {
			if err := f.Flush(ctx, executionID); err != nil {
				slog.Warn("checkpoint flush failed",
					"execution_id", executionID,
					"error", err,
				)
			}
		}
	}
}

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
	applyMode          ApplyMode
	hitlHandler        InterruptHandler
	hitlStore          InterruptStore
	recoveryHandler    StepRecoveryHandler
	recoveryEventSink  func(ctx context.Context, eventType ares_events.EventType, payload map[string]any)
	pluginBus          *runtime.PluginBus
	checkpointStore    runtime.CheckpointStore
	executionCollector *runtime.ExecutionCollector
}

// NewDynamicExecutor creates a DynamicExecutor with the given registry and options.
func NewDynamicExecutor(registry *AgentRegistry, applyMode ApplyMode, opts ...ExecutorOption) *DynamicExecutor {
	executor := &Executor{
		registry:    registry,
		maxParallel: DefaultMaxParallel,
		stepTimeout: DefaultExecutorStepTimeout,
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

// WithRecoveryHandler sets the step recovery handler for failed steps.
func (e *DynamicExecutor) WithRecoveryHandler(handler StepRecoveryHandler) *DynamicExecutor {
	e.recoveryHandler = handler
	return e
}

// WithRecoveryEventSink sets a sink for step recovery ares_events.
func (e *DynamicExecutor) WithRecoveryEventSink(sink func(ctx context.Context, eventType ares_events.EventType, payload map[string]any)) *DynamicExecutor {
	e.recoveryEventSink = sink
	return e
}

// WithPluginBus sets the plugin bus for BeforeStep/AfterStep hook invocation
// and workflow lifecycle event emission.
func (e *DynamicExecutor) WithPluginBus(bus *runtime.PluginBus) *DynamicExecutor {
	e.pluginBus = bus
	return e
}

// WithCheckpointStore sets the checkpoint store for execution resume.
func (e *DynamicExecutor) WithCheckpointStore(store runtime.CheckpointStore) *DynamicExecutor {
	e.checkpointStore = store
	return e
}

// WithExecutionCollector sets the execution collector for route recording
// and execution history tracking.
func (e *DynamicExecutor) WithExecutionCollector(c *runtime.ExecutionCollector) *DynamicExecutor {
	e.executionCollector = c
	return e
}

var dynamicExecIDCounter uint64

func generateDynamicExecutionID() string {
	id := atomic.AddUint64(&dynamicExecIDCounter, 1)
	return fmt.Sprintf("dyn-exec-%d-%d", time.Now().UnixNano(), id)
}

// ExecuteDynamic executes a workflow on a MutableDAG, applying mutations between steps.
// This is a fresh execution with a generated execution ID.
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

	if e.pluginBus != nil {
		e.pluginBus.Emit(ctx, execution.ID, runtime.EventWorkflowStarted, map[string]any{
			runtime.PayloadKeyExecutionID: execution.ID,
			runtime.PayloadKeyWorkflowID:  workflow.ID,
		})
	}

	return e.execLoop(ctx, workflow, initialInput, mutableDAG, execution, nil, nil, nil)
}

// ExecuteDynamicFromCheckpoint resumes a previously checkpointed workflow
// execution. Completed steps are skipped and execution continues from the
// last incomplete step. The execution ID is taken from the checkpoint.
//
// Returns an error if no checkpoint is found for the given execution ID.
func (e *DynamicExecutor) ExecuteDynamicFromCheckpoint(
	ctx context.Context,
	workflow *Workflow,
	initialInput string,
	mutableDAG *MutableDAG,
	executionID string,
) (*WorkflowResult, error) {
	if workflow == nil {
		return nil, errors.New("workflow must not be nil")
	}
	if mutableDAG == nil {
		return nil, errors.New("mutableDAG must not be nil")
	}
	if e.checkpointStore == nil {
		return nil, errors.New("checkpoint store not configured")
	}

	data, err := e.checkpointStore.Load(ctx, runtime.CheckpointKey(executionID))
	if err != nil {
		return nil, fmt.Errorf("load checkpoint: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("checkpoint not found: %s", executionID)
	}

	var ckpt runtime.ExperienceCheckpoint
	if err := json.Unmarshal(data, &ckpt); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}

	// Pre-populate completed and processed maps from checkpoint,
	// and build initial step results for already-completed steps.
	completed := make(map[string]bool)
	processed := make(map[string]bool)
	var initialStepResults []*StepResult
	for _, ss := range ckpt.StepStates {
		processed[ss.StepID] = true
		if ss.Status == runtime.StepStatusCompleted {
			completed[ss.StepID] = true
		}
		initialStepResults = append(initialStepResults, &StepResult{
			StepID: ss.StepID,
			Status: StepStatus(ss.Status),
			Output: ss.Output,
			Error:  ss.Error,
		})
	}

	execution := &WorkflowExecution{
		ID:         ckpt.ExecutionID,
		WorkflowID: workflow.ID,
		Status:     WorkflowStatusRunning,
		StepStates: make(map[string]*StepState),
		Variables:  make(map[string]interface{}),
		Context:    &models.TaskContext{},
		StartedAt:  time.Now(),
	}

	// Restore variables: checkpoint values take precedence over workflow defaults.
	for k, v := range workflow.Variables {
		execution.Variables[k] = v
	}
	for k, v := range ckpt.Variables {
		execution.Variables[k] = v
	}

	if e.pluginBus != nil {
		e.pluginBus.Emit(ctx, execution.ID, runtime.EventWorkflowStarted, map[string]any{
			runtime.PayloadKeyExecutionID: execution.ID,
			runtime.PayloadKeyWorkflowID:  workflow.ID,
			"resumed":                     true,
		})
	}

	return e.execLoop(ctx, workflow, initialInput, mutableDAG, execution, completed, processed, initialStepResults)
}

// findLoopPlugin returns the first LoopPlugin from the plugin bus, or nil.
func (e *DynamicExecutor) findLoopPlugin() *runtime.LoopPlugin {
	if e.pluginBus == nil {
		return nil
	}
	loopPlugins := e.pluginBus.PluginsByCap(runtime.CapLoop)
	for _, lp := range loopPlugins {
		if loop, ok := lp.(*runtime.LoopPlugin); ok {
			return loop
		}
	}
	return nil
}

// execLoop is the shared execution core used by both ExecuteDynamic and
// ExecuteDynamicFromCheckpoint. When completed/processed are non-nil they
// are used directly; otherwise fresh maps are created.
//
// execLoop now wraps execution in an outer round loop for Controlled
// Evolutionary Loop support. After the entire DAG executes once, the
// loop plugin decides whether to start another round with a mutated DAG.
func (e *DynamicExecutor) execLoop(
	ctx context.Context,
	workflow *Workflow,
	initialInput string,
	mutableDAG *MutableDAG,
	execution *WorkflowExecution,
	completed map[string]bool,
	processed map[string]bool,
	initialStepResults []*StepResult,
) (*WorkflowResult, error) {
	defer e.cleanupCheckpoint(execution.ID)

	executionOrder, err := mutableDAG.GetExecutionOrder()
	if err != nil {
		return nil, fmt.Errorf("get execution order: %w", err)
	}

	localOutputStore := NewOutputStore()
	bufSize := len(executionOrder) * 2
	if bufSize < 16 {
		bufSize = 16
	}

	if completed == nil {
		completed = make(map[string]bool)
	}
	if processed == nil {
		processed = make(map[string]bool)
	}

	stepResults := initialStepResults
	if stepResults == nil {
		stepResults = make([]*StepResult, 0)
	}

	orderSlice := make([]string, len(executionOrder))
	copy(orderSlice, executionOrder)

	recoveryCh := make(chan struct{}, 1)
	var mu sync.Mutex
	lastVersion := mutableDAG.Version()

	round := 1
	for {
		loopPlugin := e.findLoopPlugin()

		// Before round 2+, check if we should continue.
		if round > 1 {
			if loopPlugin == nil || !loopPlugin.ShouldExecuteRound(round, execution.Variables) {
				// No more rounds. The DAG completed normally; emit
				// workflow completed event and return the accumulated result.
				execution.Status = WorkflowStatusCompleted
				execution.FinishedAt = time.Now()
				e.flushCheckpoint(ctx, execution.ID)
				if e.pluginBus != nil {
					e.pluginBus.Emit(ctx, execution.ID, runtime.EventWorkflowCompleted, map[string]any{
						runtime.PayloadKeyExecutionID: execution.ID,
						runtime.PayloadKeyWorkflowID:  workflow.ID,
						runtime.PayloadKeyStatus:      execution.Status,
					})
				}
				output := make(map[string]interface{})
				for _, r := range stepResults {
					output[r.StepID] = r.Output
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
			// Between-round: apply DAG mutations suggested by memory
			// and evolution plugins, then flush checkpoint with round info.
			e.applyRoundMutations(ctx, round, execution, mutableDAG)
			for _, cp := range e.pluginBus.PluginsByCap(runtime.CapCheckpoint) {
				if ckp, ok := cp.(*runtime.CheckpointPlugin); ok {
					ckp.SetRound(execution.ID, round)
				}
			}
			e.flushCheckpoint(ctx, execution.ID)

			// Reset for next round: keep the same execution, but clear
			// per-round tracking state. The DAG has been mutated between
			// rounds so a fresh topological order is needed.
			completed = make(map[string]bool)
			processed = make(map[string]bool)
			executionOrder, err = mutableDAG.GetExecutionOrder()
			if err != nil {
				return nil, fmt.Errorf("round %d: get execution order: %w", round, err)
			}
			orderSlice = make([]string, len(executionOrder))
			copy(orderSlice, executionOrder)
			lastVersion = mutableDAG.Version()
		}

		currentOrder := &orderSlice
		sem := make(chan struct{}, e.maxParallel)
		resultChan := make(chan *StepResult, bufSize)
		errChan := make(chan error, 1)
		done := make(chan struct{})

		stepEg, _ := errgroup.WithContext(ctx)

		dispatchG, dispatchCtx := errgroup.WithContext(ctx)
		dispatchG.Go(func() error {
			defer close(done)
			e.runDynamicSteps(
				dispatchCtx,
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
				recoveryCh,
			)
			return nil
		})

		// Collect results until the dispatcher closes resultChan.
	roundLoop:
		for {
			select {
			case result, ok := <-resultChan:
				if !ok {
					<-done
					_ = stepEg.Wait()
					_ = dispatchG.Wait()

					// Round completed successfully.
					// Check if another round is needed.
					if loopPlugin != nil && loopPlugin.ShouldExecuteRound(round+1, execution.Variables) {
						slog.Debug("evolutionary loop: round completed, starting next",
							"round", round,
							"execution_id", execution.ID,
						)
						loopPlugin.OnRoundEnd(ctx, round, execution.ID)
						execution.Status = WorkflowStatusRunning
						e.flushCheckpoint(ctx, execution.ID)
						round++
						break roundLoop
					}

					// No more rounds: finalize successfully.
					execution.Status = WorkflowStatusCompleted
					execution.FinishedAt = time.Now()
					e.flushCheckpoint(ctx, execution.ID)
					if e.pluginBus != nil {
						e.pluginBus.Emit(ctx, execution.ID, runtime.EventWorkflowCompleted, map[string]any{
							runtime.PayloadKeyExecutionID: execution.ID,
							runtime.PayloadKeyWorkflowID:  workflow.ID,
							runtime.PayloadKeyStatus:      execution.Status,
						})
					}
					output := make(map[string]interface{})
					for _, r := range stepResults {
						output[r.StepID] = r.Output
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
					if e.handleStepFailure(ctx, result, workflow, execution, mutableDAG, &lastVersion, currentOrder, completed, processed, &mu, recoveryCh) {
						continue
					}
					execution.Status = WorkflowStatusFailed
					execution.Error = result.Error
					execution.FinishedAt = time.Now()
					e.flushCheckpoint(ctx, execution.ID)
					if e.pluginBus != nil {
						e.pluginBus.Emit(ctx, execution.ID, runtime.EventWorkflowFailed, map[string]any{
							runtime.PayloadKeyExecutionID: execution.ID,
							runtime.PayloadKeyWorkflowID:  workflow.ID,
							runtime.PayloadKeyStatus:      execution.Status,
							runtime.PayloadKeyError:       result.Error,
						})
					}
					<-done
					_ = stepEg.Wait()
					_ = dispatchG.Wait()
					return &WorkflowResult{
						ExecutionID: execution.ID,
						WorkflowID:  workflow.ID,
						Status:      WorkflowStatusFailed,
						Error:       result.Error,
						Duration:    execution.FinishedAt.Sub(execution.StartedAt),
						Steps:       stepResults,
					}, fmt.Errorf("step %s failed: %s", result.StepID, result.Error)
				}

				// After a completed step, check for routing decisions.
				if result.Status == StepStatusCompleted && e.pluginBus != nil {
					decision := e.handleStepRouting(ctx, execution, result, mutableDAG, currentOrder)
					if decision != nil {
						slog.Debug("route decision",
							"execution_id", execution.ID,
							"from_step", result.StepID,
							"to_step", decision.NextStepID,
							"reason", decision.Reason,
							"source", decision.Source,
						)
						mu.Lock()
						order := *currentOrder
						newOrder := make([]string, 0, len(order))
						targetAdded := false
						for _, sid := range order {
							if processed[sid] || completed[sid] {
								newOrder = append(newOrder, sid)
							} else if sid == decision.NextStepID && !targetAdded {
								newOrder = append(newOrder, sid)
								targetAdded = true
							}
						}
						for _, sid := range order {
							if !processed[sid] && !completed[sid] && sid != decision.NextStepID {
								newOrder = append(newOrder, sid)
							}
						}
						*currentOrder = newOrder
						mu.Unlock()
					}
				}

			case err := <-errChan:
				execution.Status = WorkflowStatusFailed
				execution.FinishedAt = time.Now()
				e.flushCheckpoint(ctx, execution.ID)
				if e.pluginBus != nil {
					e.pluginBus.Emit(ctx, execution.ID, runtime.EventWorkflowFailed, map[string]any{
						runtime.PayloadKeyExecutionID: execution.ID,
						runtime.PayloadKeyWorkflowID:  workflow.ID,
						runtime.PayloadKeyStatus:      execution.Status,
						runtime.PayloadKeyError:       err.Error(),
					})
				}
				<-done
				_ = stepEg.Wait()
				_ = dispatchG.Wait()
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
				e.flushCheckpoint(ctx, execution.ID)
				<-done
				_ = stepEg.Wait()
				_ = dispatchG.Wait()
				return nil, ctx.Err()
			}
		}
	}
}

// runDynamicSteps runs workflow steps with support for dynamic reordering.
// The outer recovery loop allows the scheduler to re-enter step dispatch after
// recovery adds replacement nodes.
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
	recoveryCh chan struct{},
) {
	stepIndex := 0

	// H3 fix: use a dedicated stepDone channel for dependency waiting
	// instead of stepEg.Wait() which races with stepEg.Go().
	stepDone := make(chan struct{}, 1)

	// Outer recovery loop: recovery may add steps after the inner dispatch
	// loop exits. When that happens, the inner loop re-enters so the
	// replacement steps get dispatched.
	var recoveryPending bool
	for recoveryRound := 0; recoveryRound < 5; recoveryRound++ {
		// When the outer loop re-enters after recovery, reset stepIndex so
		// the scheduler re-processes the new order from the beginning.
		// Already-processed steps are skipped via the processed map.
		if recoveryRound > 0 {
			stepIndex = 0
		}
		// Inner dispatch loop.
	innerLoop:
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

			// Read dependencies under DAG read lock to avoid data race
			// with ReplaceNode (which modifies step.DependsOn under the
			// DAG write lock during recovery).
			mutableDAG.mu.RLock()
			depsCopy := make([]string, len(step.DependsOn))
			copy(depsCopy, step.DependsOn)
			mutableDAG.mu.RUnlock()

			mu.Lock()
			canExec := e.canExecuteWithDeps(depsCopy, completed)
			alreadyProcessed := processed[stepID]
			mu.Unlock()

			if alreadyProcessed {
				stepIndex++
				continue
			}

			if !canExec {
				// H3 fix: wait for any step goroutine to complete via stepDone channel,
				// instead of stepEg.Wait() which blocks until ALL goroutines finish
				// and races with concurrent stepEg.Go() calls.
				deadlockTimer := time.NewTimer(DefaultDeadlockTimeout)
				select {
				case <-stepDone:
					deadlockTimer.Stop()
					// Some goroutine completed, re-check dependencies.
					continue
				case <-recoveryCh:
					deadlockTimer.Stop()
					stepIndex = 0
					recoveryPending = true
					// Recovery added steps that may unblock this step.
					// Break out of the inner loop so the outer recovery
					// loop can re-enter with stepIndex reset.
					break innerLoop
				case <-deadlockTimer.C:
					// Timeout: potential deadlock detected.
					select {
					case errChan <- fmt.Errorf("workflow deadlock detected: step %s waiting for dependencies", stepID):
					default:
					}
					_ = stepEg.Wait()
					close(resultChan)
					return
				case <-ctx.Done():
					deadlockTimer.Stop()
					_ = stepEg.Wait()
					close(resultChan)
					return
				}
			}

			sem <- struct{}{}

			stepIndex++

			sid := stepID

			// Evaluate step condition before dispatching.
			if step.Condition != nil {
				mu.Lock()
				varsCopy := make(map[string]any, len(execution.Variables))
				for k, v := range execution.Variables {
					varsCopy[k] = v
				}
				mu.Unlock()
				if !step.Condition(varsCopy) {
					<-sem // release semaphore acquired above
					stepResult := &StepResult{
						StepID: sid,
						Status: StepStatusSkipped,
						Error:  "skipped: condition not met",
					}
					select {
					case resultChan <- stepResult:
					case <-ctx.Done():
					}
					mu.Lock()
					processed[sid] = true
					mu.Unlock()
					continue
				}
			}

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

				if e.pluginBus != nil {
					if err := e.pluginBus.BeforeStep(ctx, execution.ID, toRuntimeStep(step)); err != nil {
						slog.Warn("before step hook failed (continuing)",
							"step_id", sid,
							"execution_id", execution.ID,
							"error", err,
						)
					}
					e.pluginBus.Emit(ctx, execution.ID, runtime.EventStepStarted, map[string]any{
						runtime.PayloadKeyExecutionID: execution.ID,
						runtime.PayloadKeyStepID:      sid,
					})
				}

				result := e.executeStepCore(ctx, step, sid, initialInput, completed, outputStore, mu, startTime)

				mu.Lock()
				processed[sid] = true
				if result.Status == StepStatusCompleted {
					completed[sid] = true
				}
				mu.Unlock()

				if e.pluginBus != nil {
					// Call AfterStep before emitting ares_events so plugins can
					// record/modify state before observers see the result.
					if err := e.pluginBus.AfterStep(ctx, execution.ID, toRuntimeStepResult(result)); err != nil {
						slog.Warn("after step hook failed (continuing)",
							"step_id", sid,
							"execution_id", execution.ID,
							"error", err,
						)
					}
					if result.Status == StepStatusFailed {
						e.pluginBus.Emit(ctx, execution.ID, runtime.EventStepFailed, map[string]any{
							runtime.PayloadKeyExecutionID: execution.ID,
							runtime.PayloadKeyStepID:      sid,
							runtime.PayloadKeyStatus:      result.Status,
							runtime.PayloadKeyError:       result.Error,
							runtime.PayloadKeyDuration:    result.Duration.Milliseconds(),
						})
					} else {
						e.pluginBus.Emit(ctx, execution.ID, runtime.EventStepCompleted, map[string]any{
							runtime.PayloadKeyExecutionID: execution.ID,
							runtime.PayloadKeyStepID:      sid,
							runtime.PayloadKeyStatus:      result.Status,
							runtime.PayloadKeyDuration:    result.Duration.Milliseconds(),
						})
					}
				}

				// Check for mutations after each step completes, regardless of mode.
				// This ensures steps added dynamically (e.g., by the step's own agent)
				// are picked up even when the scheduler loop has already exhausted
				// the original topological order.
				if result.Status == StepStatusCompleted {
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

		// Recovery may be triggered by the collection loop processing a
		// step failure result. After stepEg.Wait() returns, the collection
		// loop goroutine may not have had CPU time yet. We wait for
		// recoveryCh (up to 10ms) so the collection loop can signal it.
		if recoveryPending {
			recoveryPending = false
			select {
			case <-recoveryCh:
			default:
			}
			e.recomputeOrder(mutableDAG, lastVersion, currentOrder, completed, processed, mu)
			stepIndex = 0
		} else {
			pollTimer := time.NewTimer(DefaultRecoveryPollInterval)
			select {
			case <-recoveryCh:
				pollTimer.Stop()
				e.recomputeOrder(mutableDAG, lastVersion, currentOrder, completed, processed, mu)
				stepIndex = 0
			case <-pollTimer.C:
				// Give collection loop time to process pending results.
				// Poll one more time in case recovery was signaled
				// during the timeout.
				select {
				case <-recoveryCh:
					e.recomputeOrder(mutableDAG, lastVersion, currentOrder, completed, processed, mu)
					stepIndex = 0
				default:
				}
			}
		}

		// Check for recovery-added steps that haven't been dispatched yet.
		mu.Lock()
		if stepIndex >= len(*currentOrder) {
			mu.Unlock()
			break
		}
		mu.Unlock()

		// Recovery added more steps. Before re-entering the dispatch loop,
		// drain any stale stepDone signals so we don't get spurious wake-ups.
		select {
		case <-stepDone:
		default:
		}
	}

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
