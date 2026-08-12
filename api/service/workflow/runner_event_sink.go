package workflow

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/api/core"
	workflowcore "github.com/Timwood0x10/ares/internal/workflow"
)

type serviceRunnerEventSink struct {
	events chan<- core.WorkflowEvent
	// terminalEmitted is set once a terminal (Completed/Failed) event has been
	// published. The stream wrapper uses it to decide whether a synthetic
	// failure event is still needed when the runner returns an error.
	terminalEmitted bool
}

func (s *serviceRunnerEventSink) Publish(ctx context.Context, event workflowcore.RunnerEvent) error {
	mapped, visible := mapNativeRunnerEvent(event)
	if !visible {
		return nil
	}
	if mapped.Type == core.WorkflowEventCompleted || mapped.Type == core.WorkflowEventFailed {
		s.terminalEmitted = true
	}
	select {
	case s.events <- mapped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func mapNativeRunnerEvent(event workflowcore.RunnerEvent) (core.WorkflowEvent, bool) {
	mapped := core.WorkflowEvent{
		ExecutionID: event.ExecutionID,
		WorkflowID:  event.WorkflowID,
		StepID:      string(event.NodeID),
		Status:      mapRunnerStatus(event.Status),
		Output:      runnerNodeOutput(event.Output),
		Error:       event.Error,
		Timestamp:   event.Timestamp,
	}
	switch event.Type {
	case workflowcore.RunnerEventWorkflowStarted:
		mapped.Type = core.WorkflowEventStarted
	case workflowcore.RunnerEventNodeStarted:
		mapped.Type = core.WorkflowEventStepStarted
	case workflowcore.RunnerEventNodeCompleted:
		mapped.Type = core.WorkflowEventStepCompleted
	case workflowcore.RunnerEventNodeFailed:
		mapped.Type = core.WorkflowEventStepFailed
	case workflowcore.RunnerEventWorkflowCompleted:
		mapped.Type = core.WorkflowEventCompleted
	case workflowcore.RunnerEventWorkflowFailed:
		mapped.Type = core.WorkflowEventFailed
	default:
		return core.WorkflowEvent{}, false
	}
	if mapped.Error == "" && event.Status == workflowcore.NodeStatusFailed {
		mapped.Error = fmt.Sprintf("%s failed", event.NodeID)
	}
	return mapped, true
}
