package runtime

import (
	"context"
	"log/slog"
)

// LoopConfig defines the parameters for a controlled execution loop.
type LoopConfig struct {
	MaxIterations  int                            // maximum loop iterations (0 = no limit)
	UntilCondition func(vars map[string]any) bool // exit condition; nil means run to max
	SubStepIDs     []string                       // step IDs executed each iteration
}

// LoopPlugin manages controlled execution loops with per-round checkpointing
// and exit condition evaluation. It implements RuntimePlugin and WorkflowHook.
//
// The LoopPlugin does not drive the loop itself (that is the executor's job).
// It provides configuration, iteration tracking, and lifecycle hooks so the
// executor can implement controlled loops without hardcoding loop logic.
type LoopPlugin struct {
	name      string
	config    LoopConfig
	collector *ExecutionCollector // optional
	iteration int                 // current iteration (1-based)
}

// NewLoopPlugin creates a LoopPlugin with the given configuration.
func NewLoopPlugin(name string, config LoopConfig) *LoopPlugin {
	if name == "" {
		name = "loop"
	}
	return &LoopPlugin{
		name:   name,
		config: config,
	}
}

// WithCollector sets the execution collector for loop recording.
func (p *LoopPlugin) WithCollector(c *ExecutionCollector) *LoopPlugin {
	p.collector = c
	return p
}

// Name returns the plugin name.
func (p *LoopPlugin) Name() string { return p.name }

// Capabilities returns the capabilities.
func (p *LoopPlugin) Capabilities() []Capability {
	return []Capability{CapLoop}
}

// Start initializes the loop plugin.
func (p *LoopPlugin) Start(_ context.Context, _ EventBus) error { return nil }

// Stop resets iteration state.
func (p *LoopPlugin) Stop(_ context.Context) error {
	p.iteration = 0
	return nil
}

// BeforeStep tracks iteration state for loop sub-steps.
func (p *LoopPlugin) BeforeStep(_ context.Context, executionID string, step *Step) error {
	p.iteration++
	if p.collector != nil {
		reason := ""
		if p.config.UntilCondition != nil {
			reason = "condition not met"
		} else if p.config.MaxIterations > 0 && p.iteration >= p.config.MaxIterations {
			reason = "max iterations reached"
		}
		_ = reason // future use: record iteration exit reason
	}
	slog.Debug("loop iteration",
		"iteration", p.iteration,
		"step_id", step.ID,
		"execution_id", executionID,
	)
	return nil
}

// AfterStep records loop iteration completion.
func (p *LoopPlugin) AfterStep(_ context.Context, executionID string, _ *StepResult) error {
	slog.Debug("loop step completed",
		"iteration", p.iteration,
		"execution_id", executionID,
	)
	return nil
}

// Iteration returns the current iteration count.
func (p *LoopPlugin) Iteration() int { return p.iteration }

// Config returns the loop configuration.
func (p *LoopPlugin) Config() LoopConfig { return p.config }

// ShouldContinue checks whether the loop should continue based on config.
func (p *LoopPlugin) ShouldContinue(vars map[string]any) bool {
	if p.config.MaxIterations > 0 && p.iteration >= p.config.MaxIterations {
		return false
	}
	if p.config.UntilCondition != nil && p.config.UntilCondition(vars) {
		return false
	}
	return true
}
