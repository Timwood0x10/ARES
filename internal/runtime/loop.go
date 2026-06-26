package runtime

import (
	"context"
	"log/slog"
	"sync"
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
	mu        sync.Mutex
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
	p.mu.Lock()
	p.iteration = 0
	p.mu.Unlock()
	return nil
}

// BeforeStep tracks iteration state for loop sub-steps.
func (p *LoopPlugin) BeforeStep(_ context.Context, executionID string, step *Step) error {
	p.mu.Lock()
	p.iteration++
	iter := p.iteration
	p.mu.Unlock()
	slog.Debug("loop iteration",
		"iteration", iter,
		"step_id", step.ID,
		"execution_id", executionID,
	)
	return nil
}

// AfterStep records loop iteration completion.
func (p *LoopPlugin) AfterStep(_ context.Context, executionID string, result *StepResult) error {
	p.mu.Lock()
	iter := p.iteration
	p.mu.Unlock()
	if p.collector != nil && result.Status == StepStatusFailed {
		p.collector.RecordError(result.StepID, result.Error)
	}
	slog.Debug("loop step completed",
		"iteration", iter,
		"execution_id", executionID,
	)
	return nil
}

// Iteration returns the current iteration count.
func (p *LoopPlugin) Iteration() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.iteration
}

// Config returns the loop configuration.
func (p *LoopPlugin) Config() LoopConfig { return p.config }

// ShouldContinue checks whether the loop should continue based on config.
func (p *LoopPlugin) ShouldContinue(vars map[string]any) bool {
	p.mu.Lock()
	iter := p.iteration
	p.mu.Unlock()
	if p.config.MaxIterations > 0 && iter >= p.config.MaxIterations {
		return false
	}
	if p.config.UntilCondition != nil && p.config.UntilCondition(vars) {
		return false
	}
	return true
}
