package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

// roundContext holds per-round execution state shared between the dispatcher
// goroutine and the result collection loop. Grouping these fields avoids
// passing 15+ individual parameters through helper functions.
type roundContext struct {
	sem          chan struct{}
	resultChan   chan *StepResult
	errChan      chan error
	done         chan struct{}
	stepEg       *errgroup.Group
	dispatchG    *errgroup.Group
	currentOrder *[]string
	lastVersion  *uint64
	completed    map[string]bool
	processed    map[string]bool
	mu           *sync.Mutex
	recoveryCh   chan struct{}
}

// finalizeDynamicSuccess sets the execution status to completed, emits the
// workflow-completed event, and builds the final WorkflowResult from the
// accumulated step results. Used by both the round-exit and normal-completion
// code paths to avoid duplication.
func (e *DynamicExecutor) finalizeDynamicSuccess(
	ctx context.Context,
	execution *WorkflowExecution,
	workflow *Workflow,
	stepResults []*StepResult,
) *WorkflowResult {
	execution.Status = WorkflowStatusCompleted
	execution.FinishedAt = time.Now()
	e.flushCheckpoint(ctx, execution.ID)
	if e.pluginBus != nil {
		e.pluginBus.Emit(ctx, execution.ID, ares_runtime.EventWorkflowCompleted, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: execution.ID,
			ares_runtime.PayloadKeyWorkflowID:  workflow.ID,
			ares_runtime.PayloadKeyStatus:      execution.Status,
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
	}
}

// prepareRoundTwo checks whether another evolutionary round should execute and,
// if so, applies between-round DAG mutations and returns a fresh topological
// order. Returns proceed=false when no more rounds are needed (the caller
// should finalize successfully).
func (e *DynamicExecutor) prepareRoundTwo(
	ctx context.Context,
	round int,
	execution *WorkflowExecution,
	mutableDAG *MutableDAG,
	loopPlugin *ares_runtime.LoopPlugin,
) (order []string, version uint64, proceed bool, err error) {
	if loopPlugin == nil || !loopPlugin.ShouldExecuteRound(round, execution.Variables) {
		return nil, 0, false, nil
	}
	e.applyRoundMutations(ctx, round, execution, mutableDAG)
	for _, cp := range e.pluginBus.PluginsByCap(ares_runtime.CapCheckpoint) {
		if ckp, ok := cp.(*ares_runtime.CheckpointPlugin); ok {
			ckp.SetRound(execution.ID, round)
		}
	}
	e.flushCheckpoint(ctx, execution.ID)
	executionOrder, err := mutableDAG.GetExecutionOrder()
	if err != nil {
		return nil, 0, false, fmt.Errorf("round %d: get execution order: %w", round, err)
	}
	orderSlice := make([]string, len(executionOrder))
	copy(orderSlice, executionOrder)
	return orderSlice, mutableDAG.Version(), true, nil
}

// collectRoundResults processes results from the dispatcher until the round
// completes, fails, or the context is cancelled.
//
// Returns:
//   - proceedNextRound=true, result=nil, err=nil: start another round (caller
//     increments round and continues the outer loop).
//   - proceedNextRound=false, result, err: execution finished; the caller
//     returns the result and error directly.
func (e *DynamicExecutor) collectRoundResults(
	ctx context.Context,
	execution *WorkflowExecution,
	workflow *Workflow,
	mutableDAG *MutableDAG,
	loopPlugin *ares_runtime.LoopPlugin,
	round int,
	stepResults *[]*StepResult,
	rc *roundContext,
) (proceedNextRound bool, result *WorkflowResult, err error) {
	for {
		select {
		case res, ok := <-rc.resultChan:
			if !ok {
				return e.handleRoundComplete(ctx, execution, workflow, loopPlugin, round, stepResults, rc)
			}
			if res == nil {
				continue
			}
			*stepResults = append(*stepResults, res)
			execution.StepStates[res.StepID] = &StepState{
				StepID:     res.StepID,
				Status:     res.Status,
				Output:     res.Output,
				Error:      res.Error,
				FinishedAt: time.Now(),
			}
			if res.Status == StepStatusFailed {
				if e.handleStepFailure(ctx, res, workflow, execution, mutableDAG, rc.lastVersion, rc.currentOrder, rc.completed, rc.processed, rc.mu, rc.recoveryCh) {
					continue
				}
				fr, ferr := e.finalizeStepFailure(ctx, execution, workflow, *stepResults, res, rc)
				return false, fr, ferr
			}
			if res.Status == StepStatusCompleted && e.pluginBus != nil {
				e.maybeApplyRouting(ctx, execution, res, mutableDAG, rc)
			}

		case streamErr := <-rc.errChan:
			fr, ferr := e.handleStreamError(ctx, execution, workflow, *stepResults, streamErr, rc)
			return false, fr, ferr

		case <-ctx.Done():
			execution.Status = WorkflowStatusCancelled
			execution.FinishedAt = time.Now()
			e.flushCheckpoint(ctx, execution.ID)
			<-rc.done
			_ = rc.stepEg.Wait()
			_ = rc.dispatchG.Wait()
			return false, nil, ctx.Err()
		}
	}
}

// handleRoundComplete is called when the dispatcher closes resultChan. It
// waits for all goroutines to finish, then either signals that another round
// should start or finalizes the workflow successfully.
func (e *DynamicExecutor) handleRoundComplete(
	ctx context.Context,
	execution *WorkflowExecution,
	workflow *Workflow,
	loopPlugin *ares_runtime.LoopPlugin,
	round int,
	stepResults *[]*StepResult,
	rc *roundContext,
) (proceedNextRound bool, result *WorkflowResult, err error) {
	<-rc.done
	_ = rc.stepEg.Wait()
	_ = rc.dispatchG.Wait()

	if loopPlugin != nil && loopPlugin.ShouldExecuteRound(round+1, execution.Variables) {
		log.Debug("evolutionary loop: round completed, starting next",
			"round", round,
			"execution_id", execution.ID,
		)
		loopPlugin.OnRoundEnd(ctx, round, execution.ID)
		execution.Status = WorkflowStatusRunning
		e.flushCheckpoint(ctx, execution.ID)
		return true, nil, nil
	}
	return false, e.finalizeDynamicSuccess(ctx, execution, workflow, *stepResults), nil
}

// finalizeStepFailure marks the execution as failed, emits the workflow-failed
// event, waits for goroutines, and builds a failure WorkflowResult wrapping
// the step error.
func (e *DynamicExecutor) finalizeStepFailure(
	ctx context.Context,
	execution *WorkflowExecution,
	workflow *Workflow,
	stepResults []*StepResult,
	result *StepResult,
	rc *roundContext,
) (*WorkflowResult, error) {
	execution.Status = WorkflowStatusFailed
	execution.Error = result.Error
	execution.FinishedAt = time.Now()
	e.flushCheckpoint(ctx, execution.ID)
	if e.pluginBus != nil {
		e.pluginBus.Emit(ctx, execution.ID, ares_runtime.EventWorkflowFailed, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: execution.ID,
			ares_runtime.PayloadKeyWorkflowID:  workflow.ID,
			ares_runtime.PayloadKeyStatus:      execution.Status,
			ares_runtime.PayloadKeyError:       result.Error,
		})
	}
	<-rc.done
	_ = rc.stepEg.Wait()
	_ = rc.dispatchG.Wait()
	return &WorkflowResult{
		ExecutionID: execution.ID,
		WorkflowID:  workflow.ID,
		Status:      WorkflowStatusFailed,
		Error:       result.Error,
		Duration:    execution.FinishedAt.Sub(execution.StartedAt),
		Steps:       stepResults,
	}, fmt.Errorf("step %s failed: %s", result.StepID, result.Error)
}

// handleStreamError processes an error received on errChan. It marks the
// execution as failed, emits the workflow-failed event, waits for goroutines,
// and returns a failure WorkflowResult wrapping the original error.
func (e *DynamicExecutor) handleStreamError(
	ctx context.Context,
	execution *WorkflowExecution,
	workflow *Workflow,
	stepResults []*StepResult,
	streamErr error,
	rc *roundContext,
) (*WorkflowResult, error) {
	execution.Status = WorkflowStatusFailed
	execution.FinishedAt = time.Now()
	e.flushCheckpoint(ctx, execution.ID)
	if e.pluginBus != nil {
		e.pluginBus.Emit(ctx, execution.ID, ares_runtime.EventWorkflowFailed, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: execution.ID,
			ares_runtime.PayloadKeyWorkflowID:  workflow.ID,
			ares_runtime.PayloadKeyStatus:      execution.Status,
			ares_runtime.PayloadKeyError:       streamErr.Error(),
		})
	}
	<-rc.done
	_ = rc.stepEg.Wait()
	_ = rc.dispatchG.Wait()
	return &WorkflowResult{
		ExecutionID: execution.ID,
		WorkflowID:  workflow.ID,
		Status:      WorkflowStatusFailed,
		Error:       streamErr.Error(),
		Duration:    execution.FinishedAt.Sub(execution.StartedAt),
		Steps:       stepResults,
	}, streamErr
}

// maybeApplyRouting checks for routing decisions after a step completes and
// reorders the remaining steps to prioritize the routed target step.
func (e *DynamicExecutor) maybeApplyRouting(
	ctx context.Context,
	execution *WorkflowExecution,
	result *StepResult,
	mutableDAG *MutableDAG,
	rc *roundContext,
) {
	decision := e.handleStepRouting(ctx, execution, result, mutableDAG, rc.currentOrder)
	if decision == nil {
		return
	}
	log.Debug("route decision",
		"execution_id", execution.ID,
		"from_step", result.StepID,
		"to_step", decision.NextStepID,
		"reason", decision.Reason,
		"source", decision.Source,
	)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	order := *rc.currentOrder
	newOrder := make([]string, 0, len(order))
	targetAdded := false
	for _, sid := range order {
		if rc.processed[sid] || rc.completed[sid] {
			newOrder = append(newOrder, sid)
		} else if sid == decision.NextStepID && !targetAdded {
			newOrder = append(newOrder, sid)
			targetAdded = true
		}
	}
	for _, sid := range order {
		if !rc.processed[sid] && !rc.completed[sid] && sid != decision.NextStepID {
			newOrder = append(newOrder, sid)
		}
	}
	*rc.currentOrder = newOrder
}
