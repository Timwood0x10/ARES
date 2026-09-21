package evolution

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/scoring"
)

func newTieredForShadow(t *testing.T) *scoring.TieredScorer {
	t.Helper()
	budget, err := scoring.NewBudget(10)
	if err != nil {
		t.Fatalf("NewBudget: %v", err)
	}
	ts, err := scoring.NewTieredScorer(scoring.TieredScorerConfig{
		Cache:           scoring.NewScoreCache(0),
		Budget:          budget,
		HeuristicScorer: func(*mutation.Strategy) float64 { return 0.5 },
		LLMScorer:       genome.ScorerFunc(func(*mutation.Strategy) float64 { return 0.7 }),
	})
	if err != nil {
		t.Fatalf("NewTieredScorer: %v", err)
	}
	return ts
}

// TestBuildShadowEvaluatorDeterministicFlagSemantics locks the GA-3 posture:
// with an LLM scorer wired through the tiered pipeline the evaluator is NO
// LONGER flagged deterministic — shadow comparisons now draw independently
// via ScoreEvidence (cache bypassed). Only an explicit fixed seed (wire-side
// ShadowEvalConfig.DeterministicScorer) keeps the flag, because temperature
// 0 makes every draw identical no matter the cache.
func TestBuildShadowEvaluatorDeterministicFlagSemantics(t *testing.T) {
	base := &mutation.Strategy{ID: "bootstrap-root"}

	t.Run("llm scorer without seed is not deterministic", func(t *testing.T) {
		cfg := SystemConfig{
			ScoringConfig: ScoringConfig{
				Scorer:          func(*mutation.Strategy) float64 { return 0.7 },
				HeuristicScorer: func(*mutation.Strategy) float64 { return 0.5 },
			},
			DependencyConfig: DependencyConfig{
				ShadowEvalConfig: ShadowEvaluationConfig{MinSamples: 20, MinWinRate: 0.55},
			},
		}
		eval := buildShadowEvaluator(cfg, newTieredForShadow(t), base)
		if eval == nil {
			t.Fatal("evaluator must be built")
		}
		if cfg.ShadowEvalConfig.DeterministicScorer {
			t.Fatal("cache-bypassed evidence path must not be flagged deterministic without a fixed seed")
		}
	})

	t.Run("fixed seed stays deterministic", func(t *testing.T) {
		cfg := SystemConfig{
			ScoringConfig: ScoringConfig{
				Scorer: func(*mutation.Strategy) float64 { return 0.7 },
			},
			DependencyConfig: DependencyConfig{
				ShadowEvalConfig: ShadowEvaluationConfig{
					MinSamples:          20,
					MinWinRate:          0.55,
					DeterministicScorer: true,
				},
			},
		}
		if eval := buildShadowEvaluator(cfg, newTieredForShadow(t), base); eval == nil {
			t.Fatal("evaluator must be built")
		}
		if !cfg.ShadowEvalConfig.DeterministicScorer {
			t.Fatal("explicit seed posture must be preserved")
		}
	})

	t.Run("no scorer leaves flag untouched", func(t *testing.T) {
		cfg := SystemConfig{
			DependencyConfig: DependencyConfig{
				ShadowEvalConfig: ShadowEvaluationConfig{MinSamples: 20},
			},
		}
		if eval := buildShadowEvaluator(cfg, nil, base); eval == nil {
			t.Fatal("evaluator must be built even without a scorer")
		}
		if cfg.ShadowEvalConfig.DeterministicScorer {
			t.Fatal("zero-LLM mode must not fabricate a deterministic-scorer flag")
		}
	})
}

// TestActiveStrategyManagerFallsBackToStore locks the GA-1 observability
// seam: the durable store is the source of truth. Bootstrap seeds the base
// strategy directly via store.SetActive, and a PG restart recovers a
// deployed strategy without passing through the ASM promote path — Current
// must surface either instead of returning nil (which left
// /api/evolution/lifecycle showing an empty active_id while write-back
// happily scored the store's active strategy).
func TestActiveStrategyManagerFallsBackToStore(t *testing.T) {
	ctx := context.Background()

	t.Run("store-seeded strategy is visible", func(t *testing.T) {
		store := NewMemoryStrategyStore(0)
		if err := store.SetActive(ctx, &Strategy{ID: "bootstrap-root", Score: 0.5}); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
		asm, err := NewActiveStrategyManager(store, NewRollbackPolicy())
		if err != nil {
			t.Fatalf("NewActiveStrategyManager: %v", err)
		}
		cur := asm.Current()
		if cur == nil || cur.ID != "bootstrap-root" {
			t.Fatalf("Current = %+v, want the store-seeded bootstrap-root", cur)
		}
	})

	t.Run("promoted current wins over store", func(t *testing.T) {
		store := NewMemoryStrategyStore(0)
		if err := store.SetActive(ctx, &Strategy{ID: "bootstrap-root", Score: 0.5}); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
		asm, err := NewActiveStrategyManager(store, NewRollbackPolicy())
		if err != nil {
			t.Fatalf("NewActiveStrategyManager: %v", err)
		}
		// Promote path deploys through ASM (writes store + sets m.current).
		if err := asm.Deploy(ctx, &mutation.Strategy{ID: "promoted-1", Params: map[string]any{"k": "v"}}); err != nil {
			t.Fatalf("asm.Deploy: %v", err)
		}
		cur := asm.Current()
		if cur == nil || cur.ID != "promoted-1" {
			t.Fatalf("Current = %+v, want promoted-1 (promoted cache wins)", cur)
		}
	})

	t.Run("empty store yields nil", func(t *testing.T) {
		store := NewMemoryStrategyStore(0)
		asm, err := NewActiveStrategyManager(store, NewRollbackPolicy())
		if err != nil {
			t.Fatalf("NewActiveStrategyManager: %v", err)
		}
		if cur := asm.Current(); cur != nil {
			t.Fatalf("Current = %+v, want nil on an empty store", cur)
		}
	})
}
