package scoring

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// countingScorer returns a fixed score and counts invocations — the stand-in
// for a non-deterministic LLM scorer whose draws must stay independent.
type countingScorer struct {
	calls int
	value float64
}

func (c *countingScorer) score(*mutation.Strategy) float64 {
	c.calls++
	c.value += 0.01
	return c.value
}

func newTestTiered(t *testing.T, llmCalls int, llm genome.ScorerFunc) *TieredScorer {
	t.Helper()
	cache := NewScoreCache(0)
	budget, err := NewBudget(llmCalls)
	if err != nil {
		t.Fatalf("NewBudget: %v", err)
	}
	ts, err := NewTieredScorer(TieredScorerConfig{
		Cache:           cache,
		Budget:          budget,
		HeuristicScorer: func(*mutation.Strategy) float64 { return 0.25 },
		LLMScorer:       llm,
	})
	if err != nil {
		t.Fatalf("NewTieredScorer: %v", err)
	}
	return ts
}

// TestTieredScorerScoreUsesCacheForPopulationFitness pins the legitimate
// cache path: repeated Score calls on the same strategy within a generation
// return the first draw — population fitness pays once per candidate.
func TestTieredScorerScoreUsesCacheForPopulationFitness(t *testing.T) {
	llm := &countingScorer{}
	ts := newTestTiered(t, 10, llm.score)
	ctx := context.Background()
	s := &mutation.Strategy{ID: "cand-1", Params: map[string]any{"temperature": 0.7}}

	first, tier1, err := ts.Score(ctx, s)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if tier1 != TierLLM {
		t.Fatalf("tier = %v, want TierLLM", tier1)
	}
	second, tier2, err := ts.Score(ctx, s)
	if err != nil {
		t.Fatalf("Score second: %v", err)
	}
	if tier2 != TierCache || second != first {
		t.Fatalf("second = (%v,%v), want cache hit returning %v", second, tier2, first)
	}
	if llm.calls != 1 {
		t.Fatalf("llm calls = %d, want 1 (cache must absorb the repeat)", llm.calls)
	}
}

// TestTieredScorerScoreEvidenceBypassesCache locks the GA-3 fix: shadow
// comparison evidence must be INDEPENDENT draws. Even with a cache entry
// present, ScoreEvidence re-scores and returns a fresh value, consuming
// budget — the pre-fix defect had every replay window return the first
// draw's score (MinSamples satisfied by repetition, win rate 0/1).
func TestTieredScorerScoreEvidenceBypassesCache(t *testing.T) {
	llm := &countingScorer{}
	ts := newTestTiered(t, 10, llm.score)
	ctx := context.Background()
	s := &mutation.Strategy{ID: "cand-1", Params: map[string]any{"temperature": 0.7}}

	first, _, err := ts.Score(ctx, s)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	// Warm the cache further via the fitness path.
	if _, _, err := ts.Score(ctx, s); err != nil {
		t.Fatalf("Score warm: %v", err)
	}

	evidence1, tier1, err := ts.ScoreEvidence(ctx, s)
	if err != nil {
		t.Fatalf("ScoreEvidence: %v", err)
	}
	evidence2, tier2, err := ts.ScoreEvidence(ctx, s)
	if err != nil {
		t.Fatalf("ScoreEvidence second: %v", err)
	}
	if tier1 == TierCache || tier2 == TierCache {
		t.Fatalf("evidence tiers = (%v,%v), must never credit a cache hit", tier1, tier2)
	}
	if evidence1 == first && evidence2 == first {
		t.Fatalf("evidence draws = (%v,%v), both equal fitness score %v — comparisons would be identical", evidence1, evidence2, first)
	}
	if evidence2 == evidence1 {
		t.Fatalf("evidence draws identical (%v) — scoring must not collapse to repetition", evidence1)
	}
	if llm.calls != 3 {
		t.Fatalf("llm calls = %d, want 3 (1 fitness + 2 independent evidence draws)", llm.calls)
	}
}

// TestTieredScorerScoreEvidenceFallsBackWithoutBudget pins the budget floor:
// once the generation's LLM budget is exhausted, evidence draws degrade to
// the heuristic tier rather than failing — same posture as fitness scoring.
func TestTieredScorerScoreEvidenceFallsBackWithoutBudget(t *testing.T) {
	llm := &countingScorer{}
	ts := newTestTiered(t, 1, llm.score)
	ctx := context.Background()
	s := &mutation.Strategy{ID: "cand-1", Params: map[string]any{"k": "v"}}

	if _, _, err := ts.ScoreEvidence(ctx, s); err != nil {
		t.Fatalf("first evidence draw: %v", err)
	}
	score, tier, err := ts.ScoreEvidence(ctx, s)
	if err != nil {
		t.Fatalf("second evidence draw: %v", err)
	}
	if tier != TierHeuristic || score != 0.25 {
		t.Fatalf("over-budget evidence = (%v,%v), want heuristic 0.25", score, tier)
	}
}
