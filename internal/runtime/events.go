package runtime

import "github.com/Timwood0x10/ares/internal/events"

// Workflow event types emitted by the PluginBus.
const (
	EventWorkflowStarted   events.EventType = "workflow.started"
	EventWorkflowCompleted events.EventType = "workflow.completed"
	EventWorkflowFailed    events.EventType = "workflow.failed"
	EventStepStarted       events.EventType = "step.started"
	EventStepCompleted     events.EventType = "step.completed"
	EventStepFailed        events.EventType = "step.failed"
)

// EventCheckpointSaved is emitted after a checkpoint is persisted.
const EventCheckpointSaved events.EventType = "checkpoint.saved"

// Payload keys used in workflow events.
const (
	PayloadKeyExecutionID = "execution_id"
	PayloadKeyWorkflowID  = "workflow_id"
	PayloadKeyStepID      = "step_id"
	PayloadKeyStatus      = "status"
	PayloadKeyDuration    = "duration_ms"
	PayloadKeyError       = "error"
)
