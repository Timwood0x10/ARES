package workflow

import (
	"context"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

func stepFromSpec(spec *NodeSpec) *ares_runtime.Step {
	return &ares_runtime.Step{
		ID:        string(spec.ID),
		Name:      spec.Name,
		AgentType: spec.AgentType,
		Status:    ares_runtime.StepStatusRunning,
		StartedAt: time.Now(),
	}
}

func stepResultFromOutput(spec *NodeSpec, output map[string]any, execErr error, startedAt time.Time) *ares_runtime.StepResult {
	result := &ares_runtime.StepResult{
		StepID:   string(spec.ID),
		Name:     spec.Name,
		Duration: time.Since(startedAt),
		Output:   formatStepOutput(output),
	}
	if execErr != nil {
		result.Status = ares_runtime.StepStatusFailed
		result.Error = execErr.Error()
	} else {
		result.Status = ares_runtime.StepStatusCompleted
	}
	return result
}

func (r *Runner) emitWorkflowStarted(ctx context.Context, scope *ExecutionScope) {
	if r.pluginBus == nil {
		return
	}
	r.pluginBus.Emit(ctx, scope.ExecutionID, ares_runtime.EventWorkflowStarted, "workflow", map[string]any{
		ares_runtime.PayloadKeyExecutionID: scope.ExecutionID,
		ares_runtime.PayloadKeyWorkflowID:  scope.Spec.ID,
	})
}

func (r *Runner) emitWorkflowFinished(ctx context.Context, scope *ExecutionScope, execErr error) {
	if r.pluginBus == nil {
		return
	}
	eventType := ares_runtime.EventWorkflowCompleted
	status := ares_runtime.StepStatusCompleted
	payload := map[string]any{
		ares_runtime.PayloadKeyExecutionID: scope.ExecutionID,
		ares_runtime.PayloadKeyWorkflowID:  scope.Spec.ID,
		ares_runtime.PayloadKeyDuration:    scope.FinishedAt().Sub(scope.StartedAt()).Milliseconds(),
	}
	if execErr != nil {
		eventType = ares_runtime.EventWorkflowFailed
		status = ares_runtime.StepStatusFailed
		payload[ares_runtime.PayloadKeyError] = execErr.Error()
	}
	payload[ares_runtime.PayloadKeyStatus] = status
	r.pluginBus.Emit(ctx, scope.ExecutionID, eventType, "workflow", payload)
	r.recordEvolutionOutcome(ctx, scope, status)
	r.flushLifecycleCheckpoints(ctx, scope.ExecutionID)
}

func (r *Runner) flushLifecycleCheckpoints(ctx context.Context, executionID string) {
	for _, plugin := range r.pluginBus.PluginsByCap(ares_runtime.CapCheckpoint) {
		flusher, ok := plugin.(ares_runtime.Flusher)
		if !ok {
			continue
		}
		if err := flusher.Flush(ctx, executionID); err != nil {
			slog.WarnContext(ctx, "runner checkpoint flush failed", "execution_id", executionID, "error", err)
		}
	}
}

func (r *Runner) recordEvolutionOutcome(ctx context.Context, scope *ExecutionScope, status ares_runtime.StepStatus) {
	outcome := executionOutcome(scope, status)
	for _, plugin := range r.pluginBus.PluginsByCap(ares_runtime.CapEvolution) {
		evolution, ok := plugin.(ares_runtime.EvolutionPlugin)
		if !ok {
			continue
		}
		if err := evolution.RecordOutcome(ctx, outcome); err != nil {
			slog.WarnContext(ctx, "runner evolution outcome failed", "execution_id", scope.ExecutionID, "plugin", plugin.Name(), "error", err)
		}
	}
}

func executionOutcome(scope *ExecutionScope, status ares_runtime.StepStatus) ares_runtime.ExecutionOutcome {
	collector := scope.Collector()
	outcome := ares_runtime.ExecutionOutcome{
		ExecutionID:    scope.ExecutionID,
		WorkflowID:     scope.Spec.ID,
		Status:         string(status),
		Duration:       scope.FinishedAt().Sub(scope.StartedAt()).Milliseconds(),
		RouteCount:     len(collector.RouteHistory()),
		ToolCount:      len(collector.ToolHistory()),
		MemoryHitCount: len(collector.MemoryHits()),
		InterruptCount: len(collector.InterruptLog()),
		ErrorCount:     len(collector.ErrorLog()),
	}
	for _, node := range scope.NodeStates() {
		outcome.TotalSteps++
		switch node.Status {
		case NodeStatusFailed:
			outcome.FailedSteps++
		case NodeStatusNotSelected, NodeStatusUnreachable, NodeStatusBlocked, NodeStatusCancelled:
			outcome.SkippedSteps++
		}
	}
	return outcome
}

func formatStepOutput(output map[string]any) string {
	if value, ok := output["output"].(string); ok {
		return value
	}
	return ""
}

func (r *Runner) emitBeforeStep(ctx context.Context, executionID string, spec *NodeSpec) {
	if r.pluginBus == nil {
		return
	}
	if err := r.pluginBus.BeforeStep(ctx, executionID, stepFromSpec(spec)); err != nil {
		slog.WarnContext(ctx, "runner BeforeStep hook failed", "node_id", spec.ID, "error", err)
	}
	r.pluginBus.Emit(ctx, executionID, ares_runtime.EventStepStarted, "workflow", map[string]any{
		ares_runtime.PayloadKeyExecutionID: executionID,
		ares_runtime.PayloadKeyStepID:      string(spec.ID),
	})
}

func (r *Runner) emitAfterStep(ctx context.Context, executionID string, spec *NodeSpec, output map[string]any, execErr error, startedAt time.Time) {
	if r.pluginBus == nil {
		return
	}
	result := stepResultFromOutput(spec, output, execErr, startedAt)
	if err := r.pluginBus.AfterStep(ctx, executionID, result); err != nil {
		slog.WarnContext(ctx, "runner AfterStep hook failed", "node_id", spec.ID, "error", err)
	}
	eventType := ares_runtime.EventStepCompleted
	if execErr != nil {
		eventType = ares_runtime.EventStepFailed
	}
	r.pluginBus.Emit(ctx, executionID, eventType, "workflow", map[string]any{
		ares_runtime.PayloadKeyExecutionID: executionID,
		ares_runtime.PayloadKeyStepID:      string(spec.ID),
		ares_runtime.PayloadKeyStatus:      string(result.Status),
		ares_runtime.PayloadKeyDuration:    result.Duration.Milliseconds(),
	})
}
