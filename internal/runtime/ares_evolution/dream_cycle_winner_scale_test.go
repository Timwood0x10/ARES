package evolution

// dream_cycle_winner_scale_test.go pins the unit of the score a dream-cycle winner is
// deployed with.
//
// Strategy.Score is the GA's 0-JudgeScaleCeiling score: every scorer that fills it
// produces 0-100 (DeterministicScore base 50 clamped [5,100], the LLM tier prompted for
// "0 to 100", ConstantScorer(50)), elitism and best-ever compare it by magnitude, and
// evoapi publishes it to the dashboard. A win rate is a [0,1] ratio, so writing it
// straight into the field made a winner that beat its parent 3 times out of 4 look 100x
// worse than a mutant the constant scorer had just given 50.

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

func TestWinnerToMutationStrategy_LiftsTheWinRateOntoTheScoreScale(t *testing.T) {
	t.Parallel()

	const winRate = 0.75
	ms := winnerToMutationStrategy(&candidateResult{
		strategy: Strategy{ID: "winner", Version: 3, Params: map[string]any{"temperature": 0.4}},
		winRate:  winRate,
	})
	if ms == nil {
		t.Fatal("expected a deployed strategy")
	}
	if want := winRate * JudgeScaleCeiling; ms.Score != want {
		t.Fatalf("Score = %v, want %v (the [0,1] win rate lifted onto the score scale)",
			ms.Score, want)
	}
	if ms.Score <= 50 {
		t.Fatalf("a 0.75 winner scored %v, not above the 50 a fresh mutant gets: "+
			"the two producers are on different scales", ms.Score)
	}
	if ms.ID != "winner" {
		t.Fatalf("ID = %q, the conversion must not touch identity", ms.ID)
	}
}

// TestWinnerToMutationStrategy_NilResultStaysNil keeps the existing defensive contract.
func TestWinnerToMutationStrategy_NilResultStaysNil(t *testing.T) {
	t.Parallel()
	if got := winnerToMutationStrategy(nil); got != nil {
		t.Fatalf("winnerToMutationStrategy(nil) = %v, want nil", got)
	}
}

// TestStrategyOutcomeScoreIsNotTheStrategyScore records the OTHER field the same value
// flows into: mutation.StrategyOutcome.Score is a quality ratio, so it keeps the win
// rate as-is. Only Strategy.Score is on the judge scale — one field, one unit, is the
// whole point.
func TestStrategyOutcomeScoreIsNotTheStrategyScore(t *testing.T) {
	t.Parallel()
	outcome := mutation.StrategyOutcome{Score: 0.75}
	if outcome.Score != 0.75 {
		t.Fatalf("outcome.Score = %v: this field is a [0,1] ratio and must not be lifted",
			outcome.Score)
	}
}
