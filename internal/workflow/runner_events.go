package workflow

import (
	"context"
	"time"
)

// RunnerEventType classifies one native unified Runner lifecycle event.
type RunnerEventType string

const (
	// RunnerEventWorkflowStarted is emitted before scheduling begins.
	RunnerEventWorkflowStarted RunnerEventType = "workflow.started"
	// RunnerEventWorkflowResumed is emitted after a durable execution is restored.
	RunnerEventWorkflowResumed RunnerEventType = "workflow.resumed"
	// RunnerEventNodeStarted is emitted immediately before a node executes.
	RunnerEventNodeStarted RunnerEventType = "node.started"
	// RunnerEventNodeCompleted is emitted after a node result commits.
	RunnerEventNodeCompleted RunnerEventType = "node.completed"
	// RunnerEventNodeFailed is emitted after a node failure commits.
	RunnerEventNodeFailed RunnerEventType = "node.failed"
	// RunnerEventNodeSkipped is emitted when a node is not selected or reachable.
	RunnerEventNodeSkipped RunnerEventType = "node.skipped"
	// RunnerEventInterruptPending is emitted before waiting for human approval.
	RunnerEventInterruptPending RunnerEventType = "interrupt.pending"
	// RunnerEventInterruptResolved is emitted after human approval resolves.
	RunnerEventInterruptResolved RunnerEventType = "interrupt.resolved"
	// RunnerEventCheckpointSaved is emitted after an atomic checkpoint save.
	RunnerEventCheckpointSaved RunnerEventType = "checkpoint.saved"
	// RunnerEventMutationApplied is emitted after a queued mutation commits.
	RunnerEventMutationApplied RunnerEventType = "mutation.applied"
	// RunnerEventWorkflowCompleted is emitted on successful termination.
	RunnerEventWorkflowCompleted RunnerEventType = "workflow.completed"
	// RunnerEventWorkflowFailed is emitted on failed termination.
	RunnerEventWorkflowFailed RunnerEventType = "workflow.failed"
)

// RunnerEvent is the ordered native event contract for one execution.
type RunnerEvent struct {
	Sequence    uint64          `json:"sequence"`
	Type        RunnerEventType `json:"type"`
	ExecutionID string          `json:"execution_id"`
	WorkflowID  string          `json:"workflow_id"`
	NodeID      NodeID          `json:"node_id,omitempty"`
	Status      NodeStatus      `json:"status,omitempty"`
	Output      map[string]any  `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
}

// RunnerEventSink receives ordered native Runner events synchronously.
type RunnerEventSink interface {
	Publish(ctx context.Context, event RunnerEvent) error
}

func (r *Runner) publishEvent(ctx context.Context, scope *ExecutionScope, event RunnerEvent) error {
	if r.eventSink == nil || scope == nil {
		return nil
	}
	event = prepareRunnerEvent(scope, event)
	return scope.PublishOrderedEvent(func(sequence uint64) error {
		return r.publishSequencedEvent(ctx, event, sequence)
	})
}

func prepareRunnerEvent(scope *ExecutionScope, event RunnerEvent) RunnerEvent {
	event.ExecutionID = scope.ExecutionID
	event.WorkflowID = scope.Spec.ID
	event.Timestamp = time.Now()
	return event
}

func (r *Runner) publishSequencedEvent(ctx context.Context, event RunnerEvent, sequence uint64) error {
	event.Sequence = sequence
	return r.eventSink.Publish(ctx, event)
}
