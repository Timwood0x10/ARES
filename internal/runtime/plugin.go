// Package runtime defines the plugin contract for extending workflow execution.
// Plugins are registered on a PluginBus which manages their lifecycle and
// invokes them at defined extension points (BeforeStep, AfterStep).
package runtime

import (
	"context"

	"github.com/Timwood0x10/ares/internal/events"
)

// Capability represents a functional area a plugin provides.
type Capability string

const (
	CapObserver   Capability = "observer"
	CapCheckpoint Capability = "checkpoint"
	CapRouter     Capability = "router"
	CapLoop       Capability = "loop"
	CapMemory     Capability = "memory"
	CapEvolution  Capability = "evolution"
	CapTool       Capability = "tool"
	CapRecovery   Capability = "recovery"
)

// RuntimePlugin is the interface all plugins must implement.
type RuntimePlugin interface {
	// Name returns a unique identifier for this plugin instance.
	Name() string

	// Capabilities returns the set of capabilities this plugin provides.
	Capabilities() []Capability

	// Start initializes the plugin. The plugin receives the EventBus for
	// emitting and subscribing to workflow events.
	// Start MUST be non-blocking; long-running work should use a goroutine.
	Start(ctx context.Context, bus EventBus) error

	// Stop shuts down the plugin and releases resources.
	Stop(ctx context.Context) error
}

// WorkflowHook is an optional interface a plugin may implement to intercept
// step-level lifecycle events. Hooks are called synchronously by the bus
// before and after each step executes.
type WorkflowHook interface {
	// BeforeStep is called before a step executes. Returning an error
	// aborts the step for required hooks; for optional hooks the error
	// is logged and execution continues.
	BeforeStep(ctx context.Context, executionID string, step *Step) error

	// AfterStep is called after a step completes, regardless of success or
	// failure. The result parameter contains the final status and output.
	AfterStep(ctx context.Context, executionID string, result *StepResult) error
}

// EventBus is the event system exposed to plugins. It allows emitting
// structured events that are fanned out to all subscribers.
type EventBus interface {
	// Emit publishes an event with the given stream ID to all subscribers.
	// Implementations MUST NOT block on slow subscribers (drop events if
	// buffers are full).
	Emit(ctx context.Context, streamID string, eventType events.EventType, payload map[string]any)

	// Subscribe returns a channel that receives events matching the filter.
	// The channel is closed when the context is cancelled.
	Subscribe(ctx context.Context, filter events.EventFilter) (<-chan *events.Event, error)
}
