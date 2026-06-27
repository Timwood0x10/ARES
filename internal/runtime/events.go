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

// EventRouteDecided is emitted when a routing decision is made.
const EventRouteDecided events.EventType = "route.decided"

// EventToolCalled is emitted when a tool is invoked.
const EventToolCalled events.EventType = "tool.called"

// EventMemoryHit is emitted when memory is retrieved.
const EventMemoryHit events.EventType = "memory.hit"

// EventInterruptCreated is emitted when a HITL interrupt is created.
const EventInterruptCreated events.EventType = "interrupt.created"

// EventInterruptResolved is emitted when a HITL interrupt is resolved.
const EventInterruptResolved events.EventType = "interrupt.resolved"

// Plugin lifecycle event types emitted by the PluginBus.
const (
	EventPluginStarted events.EventType = "plugin.started"
	EventPluginStopped events.EventType = "plugin.stopped"
	EventPluginFailed  events.EventType = "plugin.failed"
)

// Payload keys used in workflow events.
const (
	PayloadKeyExecutionID        = "execution_id"
	PayloadKeyWorkflowID         = "workflow_id"
	PayloadKeyStepID             = "step_id"
	PayloadKeyStatus             = "status"
	PayloadKeyDuration           = "duration_ms"
	PayloadKeyError              = "error"
	PayloadKeyRouteReason        = "route_reason"
	PayloadKeyToolName           = "tool_name"
	PayloadKeyInterruptAction    = "interrupt_action"
	PayloadKeyInterruptFeedback  = "interrupt_feedback"
	PayloadKeyPluginName         = "plugin_name"
	PayloadKeyPluginCapabilities = "plugin_capabilities"
)
