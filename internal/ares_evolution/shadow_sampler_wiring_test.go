package evolution

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// TestWiring_ShadowSampler_WiredInBootstrapShape locks the P0-9 wiring against
// the SHAPE bootstrap actually uses: EnableDreamCycle=false but
// EnableScheduler=true. NewWiredEvolutionSystem builds a DreamCycle whenever
// EITHER flag is set (needDreamCycle = EnableDreamCycle || EnableScheduler), so
// a `system.DreamCycle == nil` guard would silently skip the sampler in every
// production config — the exact path P0-9 exists to fix.
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
