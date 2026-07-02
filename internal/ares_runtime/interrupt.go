package ares_runtime

import (
	"context"
)

// InterruptPlugin observes HITL lifecycle events and records them via the
// ExecutionCollector and EventBus. It implements both RuntimePlugin and
// WorkflowHook.
//
// The plugin works alongside the existing HITL mechanism in DynamicExecutor.
// It does not handle interrupts itself (that is done by the executor's
// handleDynamicInterrupt) but records what happened for observability,
// checkpoint, memory distill, and evolution scoring.
type InterruptPlugin struct {
	name      string
	collector *ExecutionCollector // optional; if set, interrupts are recorded
	bus       EventBus
}

// NewInterruptPlugin creates an InterruptPlugin.
func NewInterruptPlugin(name string) *InterruptPlugin {
	if name == "" {
		name = "interrupt"
	}
	return &InterruptPlugin{name: name}
}

// WithCollector sets the execution collector for interrupt recording.
func (p *InterruptPlugin) WithCollector(c *ExecutionCollector) *InterruptPlugin {
	p.collector = c
	return p
}

// Name returns the plugin name.
func (p *InterruptPlugin) Name() string { return p.name }

// Capabilities returns the capabilities.
func (p *InterruptPlugin) Capabilities() []Capability { return nil }

// Start saves the EventBus reference for emitting events.
func (p *InterruptPlugin) Start(_ context.Context, bus EventBus) error {
	p.bus = bus
	return nil
}

// Stop shuts down the plugin.
func (p *InterruptPlugin) Stop(_ context.Context) error { return nil }

// BeforeStep is a no-op for this plugin.
func (p *InterruptPlugin) BeforeStep(_ context.Context, _ string, _ *Step) error { return nil }

// AfterStep inspects the step result for interrupt-related metadata and
// records the outcome via collector and EventBus.
func (p *InterruptPlugin) AfterStep(_ context.Context, executionID string, result *StepResult) error {
	// Check for interrupt metadata from the step result (set by the executor
	// when an interrupt was handled before step execution).
	if result.Metadata != nil {
		if action, ok := result.Metadata[PayloadKeyInterruptAction]; ok {
			feedback := result.Metadata[PayloadKeyInterruptFeedback]
			p.emitInterruptEvent(executionID, result.StepID, action, feedback)
			if p.collector != nil {
				p.collector.RecordInterrupt(result.StepID, action, feedback)
			}
			return nil
		}
	}

	// Fallback: detect rejected interrupts by status and error pattern.
	if result.Status == StepStatusSkipped && result.Error != "" {
		p.emitInterruptEvent(executionID, result.StepID, "reject", result.Error)
		if p.collector != nil {
			p.collector.RecordInterrupt(result.StepID, "reject", result.Error)
		}
	}

	return nil
}

func (p *InterruptPlugin) emitInterruptEvent(executionID, stepID, action, feedback string) {
	if p.bus == nil {
		return
	}
	p.bus.Emit(context.Background(), executionID, EventInterruptCreated, "runtime", map[string]any{
		PayloadKeyExecutionID: executionID,
		PayloadKeyStepID:      stepID,
		"action":              action,
		"feedback":            feedback,
	})
	log.Debug("interrupt plugin: recorded interrupt",
		"execution_id", executionID,
		"step_id", stepID,
		"action", action,
	)
}
