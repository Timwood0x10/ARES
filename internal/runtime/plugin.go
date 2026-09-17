// Package runtime defines the plugin contract for extending workflow execution.
// Plugins are registered on a PluginBus which manages their lifecycle and
// invokes them at defined extension points (BeforeStep, AfterStep).
package runtime

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// Capability represents a functional area a plugin provides.
type Capability string

const (
	CapObserver  Capability = "observer"
	CapRouter    Capability = "router"
	CapLoop      Capability = "loop"
	CapTool      Capability = "tool"
	CapRecovery  Capability = "recovery"
	CapInterrupt Capability = "interrupt"
)

// C1.3 (runtime plugin half-closed-loop burial, plan/review-followup-hardening
// plan): CapCheckpoint/CapMemory/CapEvolution and their plugin contracts
// (CheckpointPlugin+Flusher+CheckpointStore+ExperienceCheckpoint,
// MemoryPlugin+RouteAdvice, EvolutionPlugin+ExecutionState+
// RuntimeRecommendation+ExecutionOutcome) were deleted — they had zero
// production registrations and the loop's per-round capability dispatch was
// the only consumer. Successor paths: fabric/task CheckpointEnvelope
// (checkpointing), retriever_wiring (memory), ares_evolution direct
// consumption (evolution).

// RuntimePlugin is the interface all plugins must implement.
type RuntimePlugin interface {
	// Name returns a unique identifier for this plugin instance.
	Name() string

	// Capabilities returns the set of capabilities this plugin provides.
	Capabilities() []Capability

	// Start initializes the plugin. The plugin receives the EventBus for
	// emitting and subscribing to workflow ares_events.
	// Start MUST be non-blocking; long-running work should use a goroutine.
	Start(ctx context.Context, bus EventBus) error

	// Stop shuts down the plugin and releases resources.
	Stop(ctx context.Context) error
}

// WorkflowHook is an optional interface a plugin may implement to intercept
// step-level lifecycle ares_events. Hooks are called synchronously by the bus
// before and after each step executes.
type WorkflowHook interface {
	// BeforeStep is called before a step executes. Returning an error
	// causes the bus to log the error and continue execution.
	BeforeStep(ctx context.Context, executionID string, step *Step) error

	// AfterStep is called after a step completes, regardless of success or
	// failure. The result parameter contains the final status and output.
	AfterStep(ctx context.Context, executionID string, result *StepResult) error
}

// RecoveryPlugin provides step recovery decisions when a step fails.
type RecoveryPlugin interface {
	RuntimePlugin
	// ShouldRecover returns true if the step should be recovered. Plugins
	// may use the failure details to decide.
	ShouldRecover(ctx context.Context, failure StepFailure) bool
}

// StepFailure captures the context of a failed step for recovery decisions.
type StepFailure struct {
	ExecutionID string
	WorkflowID  string
	StepID      string
	Error       string
}

// EventBus is the event system exposed to plugins. It allows emitting
// structured ares_events that are fanned out to all subscribers.
type EventBus interface {
	// Emit publishes an event with the given stream ID to all subscribers.
	// moduleName identifies the emitting module for traceability.
	// Implementations MUST NOT block on slow subscribers (drop ares_events if
	// buffers are full).
	Emit(ctx context.Context, streamID string, eventType ares_events.EventType, moduleName string, payload map[string]any)

	// Subscribe returns a channel that receives ares_events matching the filter.
	// The channel is closed when the context is cancelled.
	Subscribe(ctx context.Context, filter ares_events.EventFilter) (<-chan *ares_events.Event, error)
}
