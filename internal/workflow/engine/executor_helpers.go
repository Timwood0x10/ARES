package engine

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/runtime"
)

// recomputeOrder checks if the DAG version changed and updates the execution
// order to match the new topological sort. Replacing the entire order (rather
// than appending) ensures that replacement nodes appear before their downstream
// steps, preventing deadlock when a failed node is replaced.
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
		*lastVersion = currentVersion
		return
	}

	*lastVersion = currentVersion
	*currentOrder = newOrder
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

// toRuntimeStep converts an engine Step to the runtime Step mirror type
// for WorkflowHook invocation.
func toRuntimeStep(s *Step) *runtime.Step {
	return &runtime.Step{
		ID:        s.ID,
		Name:      s.Name,
		AgentType: s.AgentType,
		Status:    runtime.StepStatus(s.Status),
		Output:    s.Output,
		Error:     s.Error,
		StartedAt: s.StartedAt,
	}
}

// toRuntimeStepResult converts an engine StepResult to the runtime mirror
// type for WorkflowHook invocation.
func toRuntimeStepResult(r *StepResult) *runtime.StepResult {
	meta := make(map[string]string, len(r.Metadata))
	for k, v := range r.Metadata {
		meta[k] = v
	}
	return &runtime.StepResult{
		StepID:   r.StepID,
		Name:     r.Name,
		Status:   runtime.StepStatus(r.Status),
		Output:   r.Output,
		Error:    r.Error,
		Duration: r.Duration,
		Metadata: meta,
	}
}

// handleStepRouting calls the RouterPlugin after a step completes and emits
// route events. It returns the route decision if one was made, or nil.
func (e *DynamicExecutor) handleStepRouting(
	ctx context.Context,
	execution *WorkflowExecution,
	result *StepResult,
	mutableDAG *MutableDAG,
	currentOrder *[]string,
) *runtime.RouteDecision {
	if e.pluginBus == nil {
		return nil
	}

	routers := e.pluginBus.PluginsByCap(runtime.CapRouter)
	if len(routers) == 0 {
		return nil
	}
	router, ok := routers[0].(runtime.RouterPlugin)
	if !ok || router == nil {
		return nil
	}

	state := runtime.RouteState{
		ExecutionID:       execution.ID,
		WorkflowID:        execution.WorkflowID,
		CurrentStepID:     result.StepID,
		CurrentStepOutput: result.Output,
		Variables:         execution.Variables,
	}

	decision, err := router.Route(ctx, state)
	if err != nil {
		slog.Warn("router plugin returned error, ignoring",
			"router", router.Name(),
			"error", err,
		)
		return nil
	}
	if decision == nil {
		return nil
	}

	e.pluginBus.Emit(ctx, execution.ID, runtime.EventRouteDecided, map[string]any{
		runtime.PayloadKeyExecutionID: execution.ID,
		runtime.PayloadKeyStepID:      result.StepID,
		runtime.PayloadKeyRouteReason: decision.Reason,
		"next_step_id":                decision.NextStepID,
		"source":                      decision.Source,
	})

	return decision
}

// handleStepFailure attempts to recover a failed step. Returns true if the
// failure was handled and the workflow should continue. Returns false if the
// workflow should fail.
func (e *DynamicExecutor) handleStepFailure(
	ctx context.Context,
	result *StepResult,
	workflow *Workflow,
	execution *WorkflowExecution,
	mutableDAG *MutableDAG,
	lastVersion *uint64,
	currentOrder *[]string,
	completed map[string]bool,
	processed map[string]bool,
	mu *sync.Mutex,
	recoveryCh chan struct{},
) bool {
	step := e.findStepInDAG(mutableDAG, result.StepID)
	if step == nil || step.RecoveryPolicy == nil || e.recoveryHandler == nil {
		return false
	}

	if e.recoveryEventSink != nil {
		e.recoveryEventSink(ctx, events.EventStepFailed, map[string]any{
			"execution_id": execution.ID,
			"workflow_id":  workflow.ID,
			"step_id":      result.StepID,
			"error":        result.Error,
		})
	}

	failure := StepFailure{
		ExecutionID: execution.ID,
		WorkflowID:  workflow.ID,
		StepID:      result.StepID,
		Error:       result.Error,
		Input:       "",
	}

	decision, err := e.recoveryHandler.RecoverStep(ctx, failure, mutableDAG)
	if err != nil {
		slog.Warn("recovery handler returned error, failing workflow",
			"step_id", result.StepID,
			"error", err,
		)
		return false
	}
	if decision == nil {
		return false
	}

	switch decision.Strategy {
	case RecoveryReplaceNode:
		if decision.NewStep == nil {
			slog.Warn("replace_node decision missing NewStep, failing workflow",
				"step_id", result.StepID,
			)
			return false
		}

		if e.recoveryEventSink != nil {
			e.recoveryEventSink(ctx, events.EventStepRecoveryStarted, map[string]any{
				"execution_id":   execution.ID,
				"workflow_id":    workflow.ID,
				"failed_step_id": result.StepID,
				"strategy":       decision.Strategy,
			})
		}

		if err := mutableDAG.ReplaceNode(ctx, result.StepID, decision.NewStep); err != nil {
			slog.Warn("ReplaceNode failed during recovery, failing workflow",
				"step_id", result.StepID,
				"error", err,
			)
			if e.recoveryEventSink != nil {
				e.recoveryEventSink(ctx, events.EventStepRecoveryFailed, map[string]any{
					"execution_id":   execution.ID,
					"workflow_id":    workflow.ID,
					"failed_step_id": result.StepID,
					"error":          err.Error(),
				})
			}
			return false
		}

		e.recomputeOrder(mutableDAG, lastVersion, currentOrder, completed, processed, mu)

		select {
		case recoveryCh <- struct{}{}:
		default:
		}

		if e.recoveryEventSink != nil {
			e.recoveryEventSink(ctx, events.EventStepRecoveryCompleted, map[string]any{
				"execution_id":        execution.ID,
				"workflow_id":         workflow.ID,
				"failed_step_id":      result.StepID,
				"replacement_step_id": decision.NewStep.ID,
				"strategy":            decision.Strategy,
			})
		}

		return true

	default:
		return false
	}
}
