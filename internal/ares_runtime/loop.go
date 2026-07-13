package ares_runtime

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"sync"
)

// LoopConfig defines the parameters for a controlled evolutionary loop.
// Unlike a fixed ReAct loop, the LoopConfig drives the outer round loop
// that re-executes the entire DAG with mutations applied between rounds.
type LoopConfig struct {
	MaxIterations  int                            // max rounds/iterations (0 = run once)
	UntilCondition func(vars map[string]any) bool // exit condition; nil means max rounds
}

// LoopPlugin manages the controlled evolutionary loop lifecycle.
//
// It does NOT drive the loop itself (the executor does). Instead it provides:
// - Round boundary decisions (ShouldExecuteRound)
// - Between-round orchestration (OnRoundEnd: checkpoint + memory + evolution)
// - Configuration and round tracking
//
// Services (CheckpointPlugin, MemoryPlugin, EvolutionPlugin) are discovered
// via the EventBus / PluginBus at runtime.
type LoopPlugin struct {
	mu        sync.Mutex
	name      string
	config    LoopConfig
	bus       EventBus // saved from Start; used for service discovery
	iteration int      // current round (1-based)
}

// NewLoopPlugin creates a LoopPlugin with the given configuration.
// MaxRounds of 0 means the loop runs until UntilCondition is met.
func NewLoopPlugin(name string, config LoopConfig) *LoopPlugin {
	if name == "" {
		name = "loop"
	}
	return &LoopPlugin{
		name:   name,
		config: config,
	}
}

// Name returns the plugin name.
func (p *LoopPlugin) Name() string { return p.name }

// Capabilities returns the capabilities.
func (p *LoopPlugin) Capabilities() []Capability {
	return []Capability{CapLoop}
}

// Start saves the EventBus reference for service discovery.
func (p *LoopPlugin) Start(_ context.Context, bus EventBus) error {
	p.bus = bus
	return nil
}

// Stop resets iteration state.
func (p *LoopPlugin) Stop(_ context.Context) error {
	p.mu.Lock()
	p.iteration = 0
	p.mu.Unlock()
	return nil
}

// BeforeStep is a no-op in the evolutionary loop model. Round boundaries
// are managed by the executor via ShouldExecuteRound/OnRoundEnd, not by
// per-step hook counting.
func (p *LoopPlugin) BeforeStep(_ context.Context, _ string, _ *Step) error { return nil }

// AfterStep is a no-op for the same reason as BeforeStep.
func (p *LoopPlugin) AfterStep(_ context.Context, _ string, _ *StepResult) error { return nil }

// Iteration returns the current round count.
func (p *LoopPlugin) Iteration() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.iteration
}

// Config returns the loop configuration.
func (p *LoopPlugin) Config() LoopConfig { return p.config }

// ShouldExecuteRound returns true if the executor should proceed to the
// given round. Called BEFORE the round starts. Round numbering is 1-based.
// Conditions are:
// - Always execute round 1
// - Stop when MaxRounds > 0 and nextRound > MaxRounds
// - Stop when UntilCondition(vars) returns true (checked before the round)
func (p *LoopPlugin) ShouldExecuteRound(nextRound int, vars map[string]any) bool {
	if nextRound < 1 {
		return false
	}
	if nextRound == 1 {
		return true
	}
	if p.config.MaxIterations > 0 && nextRound > p.config.MaxIterations {
		log.Debug("loop: max rounds reached", "round", nextRound, "max", p.config.MaxIterations)
		return false
	}
	if p.config.UntilCondition != nil && p.config.UntilCondition(vars) {
		log.Debug("loop: until condition met", "round", nextRound)
		return false
	}
	return true
}

// OnRoundEnd is called after each round completes. It:
//  1. Flushes the CheckpointPlugin (if available on the bus)
//  2. Advises MemoryPlugin for round outcomes (if available)
//  3. Records outcomes to EvolutionPlugin (if available)
//
// This is the boundary where DAG mutations, strategy adjustments, and
// experience recording happen.
func (p *LoopPlugin) OnRoundEnd(ctx context.Context, round int, executionID string) {
	p.mu.Lock()
	p.iteration = round
	p.mu.Unlock()

	log.Debug("loop: round end",
		"round", round,
		"execution_id", executionID,
	)

	pb, ok := p.bus.(*PluginBus)
	if !ok || pb == nil {
		return
	}

	// 1. Flush checkpoint
	for _, cp := range pb.PluginsByCap(CapCheckpoint) {
		if f, ok := cp.(Flusher); ok {
			if err := f.Flush(ctx, executionID); err != nil {
				log.Warn("loop: checkpoint flush failed",
					"round", round,
					"execution_id", executionID,
					"error", err,
				)
			}
		}
	}

	// 2. Memory update — advise memory plugin for round completion
	for _, mp := range pb.PluginsByCap(CapMemory) {
		if mem, ok := mp.(MemoryPlugin); ok {
			state := ExecutionState{
				ExecutionID: executionID,
			}
			if _, err := mem.AdviseRoute(ctx, RouteState{
				ExecutionID: executionID,
			}); err != nil {
				log.Warn("loop: memory advise failed",
					"round", round,
					"execution_id", executionID,
					"error", err,
				)
			}
			_ = state // round data could feed into memory context
		}
	}

	// 3. Evolution outcome recording
	for _, ep := range pb.PluginsByCap(CapEvolution) {
		if evo, ok := ep.(EvolutionPlugin); ok {
			outcome := ExecutionOutcome{
				ExecutionID: executionID,
			}
			if err := evo.RecordOutcome(ctx, outcome); err != nil {
				log.Warn("loop: evolution record failed",
					"round", round,
					"execution_id", executionID,
					"error", err,
				)
			}
		}
	}
}

var _ RuntimePlugin = (*LoopPlugin)(nil)
var _ WorkflowHook = (*LoopPlugin)(nil)
