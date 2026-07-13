package engine

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
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
		log.Warn("recomputeOrder failed, keeping existing order",
			"error", err,
			"version", currentVersion,
		)
		*lastVersion = currentVersion
		return
	}

	*lastVersion = currentVersion
	*currentOrder = newOrder
}

// findStepInDAG finds a step by ID in the MutableDAG using the index map.
func (e *DynamicExecutor) findStepInDAG(mutableDAG *MutableDAG, stepID string) *Step {
	idx := mutableDAG.StepIndex()
	return idx[stepID]
}

// toRuntimeStep converts an engine Step to the ares_runtime Step mirror type
// for WorkflowHook invocation.
func toRuntimeStep(s *Step) *ares_runtime.Step {
	return &ares_runtime.Step{
		ID:        s.ID,
		Name:      s.Name,
		AgentType: s.AgentType,
		Status:    ares_runtime.StepStatus(s.Status),
		Output:    s.Output,
		Error:     s.Error,
		StartedAt: s.StartedAt,
	}
}

// toRuntimeStepResult converts an engine StepResult to the ares_runtime mirror
// type for WorkflowHook invocation.
func toRuntimeStepResult(r *StepResult) *ares_runtime.StepResult {
	meta := make(map[string]string, len(r.Metadata))
	for k, v := range r.Metadata {
		meta[k] = v
	}
	return &ares_runtime.StepResult{
		StepID:   r.StepID,
		Name:     r.Name,
		Status:   ares_runtime.StepStatus(r.Status),
		Output:   r.Output,
		Error:    r.Error,
		Duration: r.Duration,
		Metadata: meta,
	}
}

// handleStepRouting calls the RouterPlugin after a step completes and emits
// route ares_events. It returns the route decision if one was made, or nil.
func (e *DynamicExecutor) handleStepRouting(
	ctx context.Context,
	execution *WorkflowExecution,
	result *StepResult,
	mutableDAG *MutableDAG,
	currentOrder *[]string,
) *ares_runtime.RouteDecision {
	if e.pluginBus == nil {
		return nil
	}

	routers := e.pluginBus.PluginsByCap(ares_runtime.CapRouter)
	if len(routers) == 0 {
		return nil
	}
	router, ok := routers[0].(ares_runtime.RouterPlugin)
	if !ok || router == nil {
		return nil
	}

	state := ares_runtime.RouteState{
		ExecutionID:       execution.ID,
		WorkflowID:        execution.WorkflowID,
		CurrentStepID:     result.StepID,
		CurrentStepOutput: result.Output,
		Variables:         execution.Variables,
	}
	if e.executionCollector != nil {
		state.Collector = e.executionCollector
		state.CollectedRoutes = e.executionCollector.RouteHistory()
		state.CollectedTools = e.executionCollector.ToolHistory()
		state.CollectedMemory = e.executionCollector.MemoryHits()
	}

	decision, err := router.Route(ctx, state)
	if err != nil {
		log.Warn("router plugin returned error, ignoring",
			"router", router.Name(),
			"error", err,
		)
		return nil
	}
	if decision == nil {
		return nil
	}

	e.pluginBus.Emit(ctx, execution.ID, ares_runtime.EventRouteDecided, "workflow", map[string]any{
		ares_runtime.PayloadKeyExecutionID: execution.ID,
		ares_runtime.PayloadKeyStepID:      result.StepID,
		ares_runtime.PayloadKeyRouteReason: decision.Reason,
		"next_step_id":                     decision.NextStepID,
		"source":                           decision.Source,
	})

	if e.executionCollector != nil {
		e.executionCollector.RecordRoute(result.StepID, decision.NextStepID, decision.Reason, decision.Source)
	}

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
	if step == nil {
		return false
	}

	// Check if recovery is enabled: either a RecoveryPlugin says yes, or the
	// step has a RecoveryPolicy with a recoveryHandler configured.
	recoveryEnabled := e.recoveryHandler != nil && step.RecoveryPolicy != nil
	if e.pluginBus != nil && !recoveryEnabled {
		for _, p := range e.pluginBus.PluginsByCap(ares_runtime.CapRecovery) {
			if rp, ok := p.(ares_runtime.RecoveryPlugin); ok {
				rpState := ares_runtime.ExecutionState{
					ExecutionID:   execution.ID,
					WorkflowID:    workflow.ID,
					CurrentStepID: result.StepID,
				}
				if rp.ShouldRecover(ctx, ares_runtime.StepFailure{
					ExecutionID: execution.ID,
					WorkflowID:  workflow.ID,
					StepID:      result.StepID,
					Error:       result.Error,
				}, rpState) {
					recoveryEnabled = true
					break
				}
			}
		}
	}
	if !recoveryEnabled {
		return false
	}

	if e.recoveryEventSink != nil {
		e.recoveryEventSink(ctx, ares_events.EventStepFailed, map[string]any{
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
		log.Warn("recovery handler returned error, failing workflow",
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
			log.Warn("replace_node decision missing NewStep, failing workflow",
				"step_id", result.StepID,
			)
			return false
		}

		if e.recoveryEventSink != nil {
			e.recoveryEventSink(ctx, ares_events.EventStepRecoveryStarted, map[string]any{
				"execution_id":   execution.ID,
				"workflow_id":    workflow.ID,
				"failed_step_id": result.StepID,
				"strategy":       decision.Strategy,
			})
		}

		if err := mutableDAG.ReplaceNode(ctx, result.StepID, decision.NewStep); err != nil {
			log.Warn("ReplaceNode failed during recovery, failing workflow",
				"step_id", result.StepID,
				"error", err,
			)
			if e.recoveryEventSink != nil {
				e.recoveryEventSink(ctx, ares_events.EventStepRecoveryFailed, map[string]any{
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
			e.recoveryEventSink(ctx, ares_events.EventStepRecoveryCompleted, map[string]any{
				"execution_id":        execution.ID,
				"workflow_id":         workflow.ID,
				"failed_step_id":      result.StepID,
				"replacement_step_id": decision.NewStep.ID,
				"strategy":            decision.Strategy,
			})
		}

		// Emit a recovery patch to the evolution system when a registry is wired.
		if e.patchRegistry != nil && decision.NewStep != nil {
			recoveryPatch := patch.RuntimePatch{
				Type:   patch.PatchReplaceNode,
				Target: result.StepID,
				Value:  decision.NewStep.AgentType,
				Reason: "recovery: replace_node after step failure",
				Source: "engine.recovery",
			}
			// Best-effort: the patch registry may not have an executor for this target.
			_ = e.patchRegistry.Apply(ctx, recoveryPatch)
		}

		return true

	default:
		return false
	}
}
