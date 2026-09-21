package main

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestApplyServeShadowScorerOverwriteContract locks the GA-soak wiring fix:
// the serve layer may install the ReplayScorer ONLY when the evaluator has
// no independent scorer (zero-LLM posture). With evolution.llm_scoring
// enabled, buildShadowEvaluator already wired the budget-gated tiered
// ScoreEvidence wrapper — clobbering it with replay.Score made serve shadow
// comparisons cold-start-prior ties that the gate then fail-closed on.
func TestApplyServeShadowScorerOverwriteContract(t *testing.T) {
	store := evidence.NewMemoryStore()
	replay := evolution.NewReplayScorer(store, func() float64 { return 0.5 })
	if !replay.HasStore() {
		t.Fatal("memory store must satisfy ReplayScorer.HasStore")
	}

	t.Run("llm-wired evaluator keeps its scorer", func(t *testing.T) {
		se := evolution.NewShadowEvaluator(evolution.ShadowEvaluationConfig{Enabled: true, MinSamples: 2})
		se.SetShadowScorer(func(context.Context, *mutation.Strategy) float64 { return 0.7 })
		if !se.HasIndependentScorer() {
			t.Fatal("fixture: evaluator must start with an independent scorer")
		}
		if applyServeShadowScorer(se, replay) {
			t.Fatal("overwrite must be refused when an independent scorer is wired (llm_scoring posture)")
		}
		if !se.HasIndependentScorer() {
			t.Fatal("evaluator scorer must be untouched after a refused overwrite")
		}
	})

	t.Run("scorer-less evaluator installs replay", func(t *testing.T) {
		se := evolution.NewShadowEvaluator(evolution.ShadowEvaluationConfig{Enabled: true, MinSamples: 2})
		if se.HasIndependentScorer() {
			t.Fatal("fixture: fresh evaluator must start without a scorer")
		}
		if !applyServeShadowScorer(se, replay) {
			t.Fatal("zero-LLM posture with an evidence store must install replay.Score")
		}
		if !se.HasIndependentScorer() {
			t.Fatal("after installation the evaluator must report an independent scorer")
		}
	})

	t.Run("nil guards", func(t *testing.T) {
		if applyServeShadowScorer(nil, replay) {
			t.Fatal("nil evaluator must refuse")
		}
		se := evolution.NewShadowEvaluator(evolution.ShadowEvaluationConfig{Enabled: true})
		if applyServeShadowScorer(se, nil) {
			t.Fatal("nil replay must refuse")
		}
		empty := evolution.NewReplayScorer(nil, func() float64 { return 0.5 })
		if empty.HasStore() {
			t.Fatal("fixture: replay without a store must report HasStore=false")
		}
		if applyServeShadowScorer(se, empty) {
			t.Fatal("store-less replay must refuse — prior-vs-prior ties deadlock the gate")
		}
	})
}
