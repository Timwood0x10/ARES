package evolution

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestWiring_ShadowSampler_WiredInBootstrapShape locks the sampler wiring
// against
// the SHAPE bootstrap actually uses: EnableDreamCycle=false but
// EnableScheduler=true. NewWiredEvolutionSystem builds a DreamCycle whenever
// EITHER flag is set (needDreamCycle = EnableDreamCycle || EnableScheduler), so
// a `system.DreamCycle == nil` guard would silently skip the sampler in every
// production config — the exact path the sampler exists to fix.
func TestWiring_ShadowSampler_WiredInBootstrapShape(t *testing.T) {
	defer discardLogs()()
	base := &mutation.Strategy{
		ID:     "bootstrap-root",
		Params: map[string]any{"temperature": 0.7},
	}
	cfg := DefaultSystemConfig()
	cfg.PopulationSize = 4
	// Bootstrap's exact shape (bootstrap_steps.go): DreamCycle off, scheduler on.
	cfg.EnableDreamCycle = false
	cfg.EnableScheduler = true
	cfg.EventStore = newMockCallbackRegistrarForTest()
	cfg.StrategyStore = newMockStrategyStore()
	cfg.RollbackPolicyConfig = RollbackPolicyConfig{Enabled: true}
	cfg.ShadowEvalConfig = ShadowEvaluationConfig{Enabled: true, MinSamples: 3, MinWinRate: 0.55}

	system, err := NewWiredEvolutionSystem(base, cfg)
	if err != nil {
		t.Fatalf("NewWiredEvolutionSystem failed: %v", err)
	}
	defer Shutdown(system)

	if system.Lifecycle == nil {
		t.Fatal("expected non-nil Lifecycle")
	}
	if system.ShadowEvaluator == nil {
		t.Fatal("expected non-nil ShadowEvaluator")
	}
	if system.Lifecycle.sampler == nil {
		t.Fatal("P0-9 regression: shadow sampler must be wired when DreamCycle " +
			"does not feed comparisons (EnableDreamCycle=false)")
	}
}

// TestWiring_ShadowSampler_NotWiredWhenDreamCycleFeeds locks the exclusivity:
// when DreamCycle IS the feeder it owns StartShadow/RecordResult, and wiring the
// sampler too would reset its accumulated comparisons on every Submit.
func TestWiring_ShadowSampler_NotWiredWhenDreamCycleFeeds(t *testing.T) {
	defer discardLogs()()
	base := &mutation.Strategy{
		ID:     "bootstrap-root",
		Params: map[string]any{"temperature": 0.7},
	}
	cfg := DefaultSystemConfig()
	cfg.PopulationSize = 4
	cfg.EnableDreamCycle = true
	cfg.EnableScheduler = true
	cfg.EventStore = newMockCallbackRegistrarForTest()
	cfg.StrategyStore = newMockStrategyStore()
	cfg.RollbackPolicyConfig = RollbackPolicyConfig{Enabled: true}
	cfg.ShadowEvalConfig = ShadowEvaluationConfig{Enabled: true, MinSamples: 3, MinWinRate: 0.55}

	system, err := NewWiredEvolutionSystem(base, cfg)
	if err != nil {
		t.Fatalf("NewWiredEvolutionSystem failed: %v", err)
	}
	defer Shutdown(system)

	if system.Lifecycle == nil {
		t.Fatal("expected non-nil Lifecycle")
	}
	if system.Lifecycle.sampler != nil {
		t.Fatal("sampler must NOT be wired when DreamCycle is the shadow feeder " +
			"(exactly one feeder owns StartShadow/RecordResult)")
	}
}

// TestWiring_ShadowSampler_DedicatedEvidenceBudget locks the GA-soak fix:
// shadow evidence draws run on a DEDICATED budget derived from (but not
// capped by) MaxLLMCallsPerGeneration — population scoring can no longer
// starve the gate. The dedicated cap is max(4, pop/4); draws beyond the cap
// fall back to the heuristic so Prime still fills the full comparison
// window.
func TestWiring_ShadowSampler_DedicatedEvidenceBudget(t *testing.T) {
	defer discardLogs()()
	base := &mutation.Strategy{
		ID:     "bootstrap-root",
		Params: map[string]any{"temperature": 0.7},
	}
	cfg := DefaultSystemConfig()
	cfg.PopulationSize = 4
	cfg.EnableDreamCycle = false
	cfg.EnableScheduler = true
	cfg.EventStore = newMockCallbackRegistrarForTest()
	cfg.StrategyStore = newMockStrategyStore()
	cfg.RollbackPolicyConfig = RollbackPolicyConfig{Enabled: true}
	cfg.ShadowEvalConfig = ShadowEvaluationConfig{Enabled: true, MinSamples: 3, MinWinRate: 0.55}
	// Population budget is deliberately TINY: with the old shared budget the
	// gate could draw at most 1 LLM call per generation. The dedicated
	// shadow budget must exceed it.
	var llmCalls atomic.Int64
	cfg.Scorer = genome.ScorerFunc(func(*mutation.Strategy) float64 {
		llmCalls.Add(1)
		return 0.8
	})
	cfg.HeuristicScorer = genome.ScorerFunc(func(*mutation.Strategy) float64 { return 0.5 })
	cfg.MaxLLMCallsPerGeneration = 1

	system, err := NewWiredEvolutionSystem(base, cfg)
	if err != nil {
		t.Fatalf("NewWiredEvolutionSystem failed: %v", err)
	}
	defer Shutdown(system)

	if system.ShadowEvaluator == nil {
		t.Fatal("expected non-nil ShadowEvaluator")
	}
	if !system.ShadowEvaluator.HasIndependentScorer() {
		t.Fatal("with an LLM scorer wired, the shadow scorer must be budget-gated (tiered), not absent")
	}

	dedicated := shadowEvidenceBudget(cfg.MaxLLMCallsPerGeneration)
	if dedicated <= cfg.MaxLLMCallsPerGeneration {
		t.Fatalf("dedicated shadow budget %d must exceed the population budget %d",
			dedicated, cfg.MaxLLMCallsPerGeneration)
	}

	sampler := NewShadowSampler(system.ShadowEvaluator, cfg.ShadowEvalConfig.MinSamples)
	sampler.Prime(context.Background(), &mutation.Strategy{ID: "cand"}, &mutation.Strategy{ID: "active"})

	got := llmCalls.Load()
	if got > int64(dedicated) {
		t.Fatalf("shadow Prime exceeded its DEDICATED budget: %d calls, cap %d", got, dedicated)
	}
	if got <= int64(cfg.MaxLLMCallsPerGeneration) {
		t.Fatalf("shadow draws = %d, want > population budget %d — separation must let the gate draw past population exhaustion",
			got, cfg.MaxLLMCallsPerGeneration)
	}
	if n := len(system.ShadowEvaluator.Results()); n != cfg.ShadowEvalConfig.MinSamples {
		t.Fatalf("expected a full comparison window (%d), got %d — heuristic fallback must keep the gate able to judge",
			cfg.ShadowEvalConfig.MinSamples, n)
	}
	// ScoreEvidence cache-bypass (GA-3): no fixed seed ⇒ not deterministic.
	if system.ShadowEvaluator.IsDeterministicScorer() {
		t.Fatal("cache-bypassed evidence draws must not be reported as deterministic without a fixed seed")
	}
}

// TestShadowEvidenceBudgetDerivation pins the code-level cap formula.
func TestShadowEvidenceBudgetDerivation(t *testing.T) {
	if got := shadowEvidenceBudget(1); got != 4 {
		t.Fatalf("budget(1) = %d, want floor 4", got)
	}
	if got := shadowEvidenceBudget(40); got != 10 {
		t.Fatalf("budget(40) = %d, want 10 (pop/4)", got)
	}
	if got := shadowEvidenceBudget(200); got != 50 {
		t.Fatalf("budget(200) = %d, want 50 (pop/4)", got)
	}
}
