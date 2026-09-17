package runtime

import (
	"context"
	"sync"
)

// LoopConfig defines the parameters for a controlled evolutionary loop.
// Unlike a fixed ReAct loop, the LoopConfig drives the outer round loop
// that re-executes the entire DAG with mutations applied between rounds.
type LoopConfig struct {
	// MaxIterations caps the number of rounds. 0 (or negative) means
	// UNLIMITED — ShouldExecuteRound only consults it when > 0, so a zero
	// value falls through to UntilCondition (and to "always true" when that
	// is nil as well). Note round 1 is always allowed regardless.
	MaxIterations  int
	UntilCondition func(vars map[string]any) bool // exit condition; nil means max rounds
}

// LoopPlugin manages the controlled evolutionary loop lifecycle.
//
// It does NOT drive the loop itself (the executor does). Instead it provides:
// - Round boundary decisions (ShouldExecuteRound)
// - Round tracking (OnRoundEnd records the settled round; Iteration reads it)
// - Configuration and round budgeting
//
// C1.3 (runtime plugin half-closed-loop burial): OnRoundEnd no longer
// dispatches to capability plugins — the CapCheckpoint flush / CapMemory
// advise / CapEvolution record blocks were deleted with those capability
// faces (zero production registrations). Successor paths: fabric/task
// CheckpointEnvelope (checkpointing), retriever_wiring (memory),
// ares_evolution direct consumption (evolution outcomes).
type LoopPlugin struct {
	mu        sync.Mutex
	name      string
	config    LoopConfig
	iteration int // current round (1-based)
}

// NewLoopPlugin creates a LoopPlugin with the given configuration.
// MaxIterations of 0 means unlimited rounds (bounded only by UntilCondition,
// or unbounded when that is nil too).
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

// Start satisfies the RuntimePlugin contract. The bus parameter is ignored:
// the plugin no longer discovers capability services (C1.3).
func (p *LoopPlugin) Start(_ context.Context, _ EventBus) error {
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
// - Stop when MaxIterations > 0 and nextRound > MaxIterations (0 = unlimited)
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

// OnRoundEnd is called after each round completes. It records the settled
// round for Iteration() readers.
//
// C1.3: the former capability dispatch blocks (CheckpointPlugin flush,
// MemoryPlugin advise, EvolutionPlugin record) were deleted with those
// capability faces. Successor paths: fabric/task CheckpointEnvelope,
// retriever_wiring memory, ares_evolution direct consumption.
func (p *LoopPlugin) OnRoundEnd(_ context.Context, round int, executionID string) {
	p.mu.Lock()
	p.iteration = round
	p.mu.Unlock()

	log.Debug("loop: round end",
		"round", round,
		"execution_id", executionID,
	)
}

var _ RuntimePlugin = (*LoopPlugin)(nil)
var _ WorkflowHook = (*LoopPlugin)(nil)
