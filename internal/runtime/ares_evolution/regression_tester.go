package evolution

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	mathrand "math/rand"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// tieBreakJitter is the width of the random band added to each trial's scores in a
// MULTI-trial regression test.
//
// It exists for one reason: the trial loop scores the SAME two strategies every
// iteration and the scorer is deterministic, so without a perturbation the win rate
// could only ever be 0 or 1 — an exact tie would be reported as a unanimous loss and
// a strategy that is merely equivalent to the incumbent would look strictly worse.
// With the band, a tie produces a win rate around chance, which any threshold above
// chance rejects: "no evidence of improvement" rather than "proven regression".
//
// It is EXPRESSED AS A FRACTION of JudgeScaleCeiling rather than as a bare constant,
// so the two can only be edited together: an independent 0.02 would be a tie-breaker on
// a 0-100 judge and a decision-maker on a 0-1 one, with nothing in the type system to
// say so.
//
// That is single-sourcing, NOT self-adaptation: JudgeScaleCeiling is itself a constant
// a human maintains, and today the fraction works out to the same 0.02 it always was.
// A reader must not read "derived" as "it will follow a scale change on its own" — a
// judge that moves to [0,1] needs JudgeScaleCeiling updated by hand, and the blend in
// genome_wiring_run.go lifted by the same fact.
const tieBreakJitter = JudgeScaleCeiling / 5000.0

// RegressionTester implements TesterInterface by scoring candidate and baseline
// strategies using a provided scorer function.
type RegressionTester struct {
	scorer func(*mutation.Strategy) float64

	// rngMu serializes access to rng: math/rand.Rand is not goroutine-safe,
	// and Run may be called concurrently by findWinner's parallel evaluation
	// (errgroup). Without the lock the shared rng would race.
	rngMu sync.Mutex
	rng   *mathrand.Rand
}

// NewRegressionTester creates a regression tester for arena strategy comparison.
//
// Args:
//
//	scorer - scoring function for strategy evaluation (must not be nil).
//
// Returns:
//
//	*RegressionTester - the configured tester.
//	error - non-nil if scorer is nil.
func NewRegressionTester(scorer func(*mutation.Strategy) float64) (*RegressionTester, error) {
	if scorer == nil {
		return nil, errors.New("scorer must not be nil")
	}
	return &RegressionTester{
		scorer: scorer,
		rng:    newCryptoRand(),
	}, nil
}

// newCryptoRand creates a math/rand source seeded from crypto/rand.
func newCryptoRand() *mathrand.Rand {
	var seed int64
	if err := binary.Read(rand.Reader, binary.LittleEndian, &seed); err != nil {
		seed = time.Now().UnixNano()
	}
	return mathrand.New(mathrand.NewSource(seed)) //nolint:gosec // seeded rand for test perturbation
}

// Run executes a regression test comparing a candidate strategy against a baseline.
// With TaskSampleSize > 1, it runs multiple trials with slight score perturbation
// to produce a meaningful win rate.
//
// Args:
//
//	ctx - operation context for cancellation.
//	cfg - regression test configuration.
//
// Returns:
//
//	*RegressionResult - evaluation result with win rate and scores.
//	error - non-nil if scoring fails.
func (t *RegressionTester) Run(ctx context.Context, cfg RegressionConfig) (*RegressionResult, error) {
	candidateMS := evolutionToMutationStrategy(cfg.Candidate)
	baselineMS := evolutionToMutationStrategy(cfg.Baseline)

	sampleSize := cfg.TaskSampleSize
	if sampleSize <= 0 {
		sampleSize = 1
	}

	var (
		candidateTotal float64
		baselineTotal  float64
		wins           int
	)

	for i := 0; i < sampleSize; i++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("regression test cancelled: %w", err)
		}

		candScore := t.scorer(&candidateMS)
		baseScore := t.scorer(&baselineMS)

		// Break exact ties between trials (see tieBreakJitter). Only meaningful for a
		// multi-trial run: with one trial there is nothing to distinguish, and adding
		// noise would make a single deterministic comparison random.
		if sampleSize > 1 {
			t.rngMu.Lock()
			candScore += (t.rng.Float64() - 0.5) * tieBreakJitter
			baseScore += (t.rng.Float64() - 0.5) * tieBreakJitter
			t.rngMu.Unlock()
		}

		candidateTotal += candScore
		baselineTotal += baseScore
		if candScore > baseScore {
			wins++
		}
	}

	n := float64(sampleSize)
	winRate := float64(wins) / n

	if winRate > 1.0 {
		winRate = 1.0
	}
	if winRate < 0 {
		winRate = 0
	}

	log.Debug("[RegressionTester] Evaluation complete",
		"candidate_id", cfg.Candidate.ID,
		"candidate_score", candidateTotal/n,
		"baseline_score", baselineTotal/n,
		"win_rate", winRate,
		"samples", sampleSize,
	)

	return &RegressionResult{
		CandidateScore: candidateTotal / n,
		BaselineScore:  baselineTotal / n,
		WinRate:        winRate,
		TotalTasks:     sampleSize,
	}, nil
}
