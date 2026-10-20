package evolution

// regression_tiebreak_test.go pins WHY the regression tester perturbs its scores.
//
// The trial loop scores the same two strategies every iteration with a deterministic
// scorer, so the perturbation is the only thing that can make an exact tie produce a
// win rate other than 0 or 1. Without it, a strategy that is merely EQUIVALENT to the
// incumbent is reported as a unanimous loss — a false regression verdict on no
// evidence, which is the opposite of what this gate exists for.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// tiedScorer cannot tell two strategies apart: the shape a real judge produces for
// two equivalent strategies (and the shape any deterministic judge produces for the
// same strategy twice).
func tiedScorer(*mutation.Strategy) float64 { return 70 }

func tieBreakConfig(samples int) RegressionConfig {
	return RegressionConfig{
		Candidate:      Strategy{ID: "candidate", Version: 2},
		Baseline:       Strategy{ID: "baseline", Version: 1},
		TaskSampleSize: samples,
	}
}

func TestRegressionTester_ExactTieIsRejectableNotAUnanimousLoss(t *testing.T) {
	defer discardLogs()()
	ctx := context.Background()
	tester, err := NewRegressionTester(tiedScorer)
	require.NoError(t, err)

	// A single trial has no perturbation, so an exact tie is not a win — the
	// documented "only meaningful for a multi-trial run" half of the rule.
	single, err := tester.Run(ctx, tieBreakConfig(1))
	require.NoError(t, err)
	assert.Zero(t, single.WinRate, "with one trial an exact tie cannot be a win")

	// Many trials: the band turns the tie into a coin flip. The assertion is a wide
	// band around chance on purpose — the property is "rejectable", not a number.
	const samples = 40
	multi, err := tester.Run(ctx, tieBreakConfig(samples))
	require.NoError(t, err)
	assert.Equal(t, samples, multi.TotalTasks)
	assert.Greater(t, multi.WinRate, 0.0, "a tie must not read as a unanimous loss")
	assert.Less(t, multi.WinRate, 1.0, "nor as a unanimous win")
	assert.InDelta(t, 0.5, multi.WinRate, 0.35, "a tie should sit around chance")

	// The reported scores stay on the judge's scale: the perturbation is a tie
	// breaker, not a rescaling.
	assert.InDelta(t, 70, multi.CandidateScore, 0.1)
	assert.InDelta(t, 70, multi.BaselineScore, 0.1)
}

// TestRegressionTester_JitterCannotOutvoteARealDifference is the other half of the
// contract: the band must be far too small to flip a genuine gap, which is what makes
// "near chance ⇒ no evidence" a safe reading.
func TestRegressionTester_JitterCannotOutvoteARealDifference(t *testing.T) {
	defer discardLogs()()
	ctx := context.Background()

	// One point apart on the judge's scale — the smallest difference a 0-100 judge
	// can express — must still win every trial.
	scorer := func(s *mutation.Strategy) float64 {
		if s.ID == "candidate" {
			return 71
		}
		return 70
	}
	tester, err := NewRegressionTester(scorer)
	require.NoError(t, err)

	result, err := tester.Run(ctx, tieBreakConfig(40))
	require.NoError(t, err)
	assert.InDelta(t, 1.0, result.WinRate, 1e-9,
		"a one-point advantage must not be at the mercy of the tie breaker")
}

// TestTieBreakJitter_StaysUnderOneJudgePoint documents the invariant the constant is
// derived to keep: it must remain well below the smallest meaningful difference on the
// judge's scale (one point), so it can never decide a real comparison.
func TestTieBreakJitter_StaysUnderOneJudgePoint(t *testing.T) {
	assert.Greater(t, tieBreakJitter, 0.0, "a zero band cannot break a tie")
	assert.Less(t, tieBreakJitter, 1.0,
		"the band must stay under one point of a 0-100 judge")
}
