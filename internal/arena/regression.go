package arena

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"golang.org/x/sync/errgroup"
)

// Common errors for regression testing.
var (
	ErrNilArena        = errors.New("arena: arena service is nil")
	ErrNilScorer       = errors.New("arena: scorer is nil")
	ErrNilStrategy     = errors.New("arena: strategy is nil")
	ErrInvalidRuns     = errors.New("arena: number of runs must be positive")
	ErrEmptyScores     = errors.New("arena: no scores collected for strategy")
	ErrConfidenceRange = errors.New("arena: confidence level must be between 0 and 1")
)

// RegressionResult holds the comparison result between two strategies.
type RegressionResult struct {
	OldStrategyID string    // old strategy identifier
	NewStrategyID string    // new strategy identifier
	OldAvg        float64   // average score of old strategy runs
	NewAvg        float64   // average score of new strategy runs
	OldScores     []float64 // individual run scores for old strategy
	NewScores     []float64 // individual run scores for new strategy
	WinRate       float64   // fraction where new >= old (0.0 to 1.0)
	Confident     bool      // statistically significant (p < 0.05 via Welch's t-test)
	Samples       int       // number of sample runs per strategy
	PValue        float64   // computed p-value from statistical test
	TestedAt      time.Time // when this regression was run
}

// TestCaseInput wraps a strategy with its test case context for scoring.
// When RegressionConfig.TestCases is set, the Scorer receives this instead
// of the raw strategy. Scorers should type-assert input to TestCaseInput
// to access both the strategy and the specific test case.
type TestCaseInput struct {
	Strategy any
	TestCase any
	Index    int // iteration index, shared between baseline and compare
}

// RegressionConfig configures a regression test between two strategies.
type RegressionConfig struct {
	OldStrategy  any     // old strategy (baseline)
	NewStrategy  any     // new strategy (strategy under test)
	BaselineRuns int     // number of runs for baseline (old) strategy, default 5
	CompareRuns  int     // number of runs for new strategy, default 5
	TestSuite    string  // test suite name or identifier
	Confidence   float64 // significance level for statistical test, default 0.05
	MinWinRate   float64 // minimum win rate to consider new strategy better, default 0.55
	// MinAdaptiveRuns is the minimum runs before early stopping kicks in (adaptive mode only, default 10).
	// Adaptive mode scores in batches and stops early when statistical significance is reached.
	MinAdaptiveRuns int
	// AdaptiveBatchSize is the number of scores to collect per batch in adaptive mode, default 5.
	AdaptiveBatchSize int
	// MaxAdaptiveRuns overrides BaselineRuns/CompareRuns as the upper bound for adaptive mode, default 0 (use BaselineRuns/CompareRuns).
	MaxAdaptiveRuns int
	// TestCases provides a fixed list of test cases for paired scoring.
	// When set, iteration i of both baseline and compare receives TestCaseInput
	// with the same TestCase and Index, ensuring fair paired comparison.
	// Length should cover max(BaselineRuns, CompareRuns).
	TestCases []any
}

// DefaultRegressionConfig returns a RegressionConfig with sensible defaults.
func DefaultRegressionConfig() RegressionConfig {
	return RegressionConfig{
		BaselineRuns: 5,
		CompareRuns:  5,
		Confidence:   0.05,
		MinWinRate:   0.55,
	}
}

// Scorer defines how to score a single strategy or its execution result.
type Scorer interface {
	// Score evaluates a strategy and returns a numeric score.
	// The input can be a strategy object, execution result, or any relevant data.
	Score(input any) (float64, error)
}

// RegressionTester performs A/B style comparison tests on strategies.
type RegressionTester struct {
	arena  *Service
	scorer Scorer
}

// NewRegressionTester creates a new regression tester.
// Args:
//   - arena: arena service for running scenarios, must not be nil.
//   - scorer: scoring function interface, must not be nil.
//
// Returns:
//   - *RegressionTester: the configured tester.
//   - error: ErrNilArena or ErrNilScorer if arguments are nil.
func NewRegressionTester(arena *Service, scorer Scorer) (*RegressionTester, error) {
	if arena == nil {
		return nil, ErrNilArena
	}
	if scorer == nil {
		return nil, ErrNilScorer
	}
	return &RegressionTester{
		arena:  arena,
		scorer: scorer,
	}, nil
}

// Run executes the regression test and returns comparison results.
// It runs oldStrategy baselineRuns times and newStrategy compareRuns times,
// then computes statistical significance using Welch's t-test approximation.
//
// When cfg.AdaptiveBatchSize > 0, runs in batches with early stopping:
//   - Scores are collected in batches of AdaptiveBatchSize for both strategies
//   - After each batch, Welch's t-test is computed
//   - Stops early if p < confidence (winner found) or if p > 0.5 after MinAdaptiveRuns
//   - Caps at MaxAdaptiveRuns if set, otherwise at BaselineRuns/CompareRuns
//
// Args:
//   - ctx: context for cancellation and timeout.
//   - cfg: configuration for the regression test.
//
// Returns:
//   - *RegressionResult: detailed comparison results.
//   - error: validation error, context cancellation, or scoring failure.
func (rt *RegressionTester) Run(ctx context.Context, cfg RegressionConfig) (*RegressionResult, error) {
	// Validate configuration.
	if err := validateRegressionConfig(cfg); err != nil {
		return nil, err
	}

	// Apply defaults where needed.
	if cfg.BaselineRuns <= 0 {
		cfg.BaselineRuns = 5
	}
	if cfg.CompareRuns <= 0 {
		cfg.CompareRuns = 5
	}
	if cfg.Confidence <= 0 {
		cfg.Confidence = 0.05
	}
	if cfg.MinWinRate <= 0 {
		cfg.MinWinRate = 0.55
	}
	if cfg.MinAdaptiveRuns <= 0 {
		cfg.MinAdaptiveRuns = 10
	}
	if cfg.AdaptiveBatchSize <= 0 {
		cfg.AdaptiveBatchSize = 5
	}
	if cfg.AdaptiveBatchSize >= cfg.BaselineRuns && cfg.AdaptiveBatchSize >= cfg.CompareRuns {
		// Batch size covers all runs, no adaptive benefit; fall through to standard mode.
		cfg.AdaptiveBatchSize = 0
	}
	if cfg.MaxAdaptiveRuns <= 0 {
		cfg.MaxAdaptiveRuns = cfg.BaselineRuns
		if cfg.CompareRuns > cfg.MaxAdaptiveRuns {
			cfg.MaxAdaptiveRuns = cfg.CompareRuns
		}
	}

	// Adaptive batched mode with early stopping.
	if cfg.AdaptiveBatchSize > 0 && cfg.MaxAdaptiveRuns > 0 {
		return rt.runAdaptive(ctx, cfg)
	}

	// Pre-sample test cases for paired scoring.
	testCases := cfg.TestCases
	if len(testCases) == 0 {
		// Generate a deterministic sequence of nil test cases so both strategies
		// at least agree on the iteration index (Index field in TestCaseInput).
		totalRuns := cfg.BaselineRuns
		if cfg.CompareRuns > totalRuns {
			totalRuns = cfg.CompareRuns
		}
		testCases = make([]any, totalRuns)
	}

	// Standard mode: run both strategies concurrently using errgroup.
	var oldScores, newScores []float64
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		scores, err := rt.runStrategy(gCtx, cfg.OldStrategy, cfg.BaselineRuns, testCases)
		if err != nil {
			return fmt.Errorf("arena: run old strategy: %w", err)
		}
		oldScores = scores
		return nil
	})

	g.Go(func() error {
		scores, err := rt.runStrategy(gCtx, cfg.NewStrategy, cfg.CompareRuns, testCases)
		if err != nil {
			return fmt.Errorf("arena: run new strategy: %w", err)
		}
		newScores = scores
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Build result with statistical analysis.
	result := rt.buildResult(cfg, oldScores, newScores)
	return result, nil
}

// runAdaptive runs regression in batches with early stopping via sequential testing.
// Both strategies are scored in parallel batches. After each batch, significance is
// computed. Stops early if the outcome is already clear.
func (rt *RegressionTester) runAdaptive(ctx context.Context, cfg RegressionConfig) (*RegressionResult, error) {
	var oldScores, newScores []float64
	maxRuns := cfg.MaxAdaptiveRuns
	batchSize := cfg.AdaptiveBatchSize
	minRuns := cfg.MinAdaptiveRuns

	// Pre-sample test cases for paired scoring across all batches.
	testCases := cfg.TestCases
	if len(testCases) == 0 {
		testCases = make([]any, maxRuns)
	}

	for len(oldScores) < maxRuns || len(newScores) < maxRuns {
		// Determine next batch size (last batch may be smaller).
		offset := len(oldScores)
		if len(newScores) < offset {
			offset = len(newScores)
		}
		oldRemaining := maxRuns - len(oldScores)
		newRemaining := maxRuns - len(newScores)
		thisBatch := batchSize
		if oldRemaining < thisBatch {
			thisBatch = oldRemaining
		}
		if newRemaining < thisBatch {
			thisBatch = newRemaining
		}
		if thisBatch <= 0 {
			break
		}

		// Both strategies score the same test case slice (paired by index).
		batchTestCases := testCases[offset : offset+thisBatch]

		// Run one batch for both strategies in parallel.
		var batchOld, batchNew []float64
		g, gCtx := errgroup.WithContext(ctx)

		g.Go(func() error {
			scores, err := rt.runStrategy(gCtx, cfg.OldStrategy, thisBatch, batchTestCases)
			if err != nil {
				return fmt.Errorf("arena: run old strategy: %w", err)
			}
			batchOld = scores
			return nil
		})

		g.Go(func() error {
			scores, err := rt.runStrategy(gCtx, cfg.NewStrategy, thisBatch, batchTestCases)
			if err != nil {
				return fmt.Errorf("arena: run new strategy: %w", err)
			}
			batchNew = scores
			return nil
		})

		if err := g.Wait(); err != nil {
			return nil, err
		}

		oldScores = append(oldScores, batchOld...)
		newScores = append(newScores, batchNew...)

		// Check for early stopping after reaching minimum runs.
		n := len(oldScores)
		if len(newScores) < n {
			n = len(newScores)
		}
		if n >= minRuns && n >= 2 {
			_, pVal := computeSignificance(oldScores[:n], newScores[:n], cfg.Confidence)
			// Stop if significant (p < confidence) or hopeless (p > 0.5).
			if pVal < cfg.Confidence || pVal > 0.5 {
				break
			}
		}
	}

	// Trim to equal length for win rate calculation.
	n := len(oldScores)
	if len(newScores) < n {
		n = len(newScores)
	}

	result := rt.buildResult(cfg, oldScores[:n], newScores[:n])
	return result, nil
}

// runStrategy executes a single strategy multiple times and collects scores.
// Each execution is scored via the configured Scorer interface.
// When testCases is provided, the Scorer receives TestCaseInput wrapping both
// the strategy and the specific test case for that iteration.
func (rt *RegressionTester) runStrategy(ctx context.Context, strategy any, n int, testCases []any) ([]float64, error) {
	if strategy == nil {
		return nil, ErrNilStrategy
	}
	if n <= 0 {
		return nil, ErrInvalidRuns
	}

	scores := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var input any = strategy
		if len(testCases) > 0 {
			input = TestCaseInput{
				Strategy: strategy,
				TestCase: testCases[i%len(testCases)],
				Index:    i,
			}
		}

		score, err := rt.scorer.Score(input)
		if err != nil {
			return nil, fmt.Errorf("arena: score run %d: %w", i, err)
		}
		scores = append(scores, score)
	}

	if len(scores) == 0 {
		return nil, ErrEmptyScores
	}
	return scores, nil
}

// buildResult constructs the final RegressionResult from collected scores.
func (rt *RegressionTester) buildResult(cfg RegressionConfig, oldScores, newScores []float64) *RegressionResult {
	oldAvg := computeMean(oldScores)
	newAvg := computeMean(newScores)
	winRate := computeWinRate(oldScores, newScores)
	confident, pValue := computeSignificance(oldScores, newScores, cfg.Confidence)

	samples := cfg.BaselineRuns
	if cfg.CompareRuns < samples {
		samples = cfg.CompareRuns
	}

	return &RegressionResult{
		OldStrategyID: fmt.Sprintf("%v", cfg.OldStrategy),
		NewStrategyID: fmt.Sprintf("%v", cfg.NewStrategy),
		OldAvg:        oldAvg,
		NewAvg:        newAvg,
		OldScores:     oldScores,
		NewScores:     newScores,
		WinRate:       winRate,
		Confident:     confident,
		Samples:       samples,
		PValue:        pValue,
		TestedAt:      time.Now(),
	}
}

// computeMean calculates the arithmetic mean of a slice of floats.
func computeMean(scores []float64) float64 {
	if len(scores) == 0 {
		return 0
	}
	sum := 0.0
	for _, s := range scores {
		sum += s
	}
	return sum / float64(len(scores))
}

// computeVariance calculates the sample variance of a slice of floats using Bessel's correction (n-1 denominator).
// Returns 0 for empty or single-element slices where sample variance is undefined.
func computeVariance(scores []float64) float64 {
	if len(scores) <= 1 {
		return 0
	}
	mean := computeMean(scores)
	sumSqDiff := 0.0
	for _, s := range scores {
		diff := s - mean
		sumSqDiff += diff * diff
	}
	return sumSqDiff / float64(len(scores)-1)
}

// computeWinRate calculates fraction where new score >= old score in pairwise comparison.
func computeWinRate(oldScores, newScores []float64) float64 {
	if len(oldScores) == 0 || len(newScores) == 0 {
		return 0
	}
	minLen := len(oldScores)
	if len(newScores) < minLen {
		minLen = len(newScores)
	}
	wins := 0
	for i := 0; i < minLen; i++ {
		if newScores[i] >= oldScores[i] {
			wins++
		}
	}
	return float64(wins) / float64(minLen)
}

// computeSignificance performs a simplified Welch's t-test.
// Returns true if the difference is statistically significant at the given confidence level.
// Uses a basic t-statistic approximation with normal distribution for large samples.
//
// Args:
//   - oldScores: baseline strategy scores.
//   - newScores: new strategy scores.
//   - confidenceLevel: significance threshold (e.g., 0.05 for 95% confidence).
//
// Returns:
//   - bool: true if statistically significant (p-value < confidenceLevel).
//   - float64: computed approximate p-value.
func computeSignificance(oldScores, newScores []float64, confidenceLevel float64) (bool, float64) {
	if len(oldScores) < 2 || len(newScores) < 2 {
		return false, 1.0
	}

	oldMean := computeMean(oldScores)
	newMean := computeMean(newScores)
	oldVar := computeVariance(oldScores)
	newVar := computeVariance(newScores)
	nOld := float64(len(oldScores))
	nNew := float64(len(newScores))

	// Welch's t-test standard error calculation.
	se := math.Sqrt(oldVar/nOld + newVar/nNew)
	if se == 0 {
		// Identical means with zero variance: no significant difference.
		if oldMean == newMean {
			return false, 1.0
		}
		// Different means but zero variance: highly significant.
		return true, 0.0
	}

	tStat := math.Abs(newMean-oldMean) / se

	// Approximate degrees of freedom using Welch-Satterthwaite equation.
	numerator := (oldVar/nOld + newVar/nNew) * (oldVar/nOld + newVar/nNew)
	denominator := (oldVar*oldVar)/(nOld*nOld*(nOld-1)) +
		(newVar*newVar)/(nNew*nNew*(nNew-1))
	if denominator == 0 {
		denominator = 1e-10
	}
	df := numerator / denominator

	// Approximate p-value using normal distribution for large samples.
	// For small df, this is conservative (overestimates p-value).
	pValue := approximatePValue(tStat, df)

	confident := pValue < confidenceLevel
	return confident, pValue
}

// approximatePValue computes an approximate two-tailed p-value from t-statistic.
// Uses normal distribution approximation for simplicity.
func approximatePValue(tStat float64, df float64) float64 {
	// For large degrees of freedom (>30), use normal approximation.
	if df > 30 {
		return normalApproximationPValue(tStat)
	}

	// For smaller df, use a simple scaling factor to be more conservative.
	// This avoids complex beta function calculations while remaining reasonable.
	scale := 1.0 + (30-df)/60.0 // Scale up p-value for small df (more conservative).
	pVal := normalApproximationPValue(tStat) * scale
	if pVal > 1.0 {
		pVal = 1.0
	}
	return pVal
}

// normalApproximationPValue approximates two-tailed p-value using error function.
func normalApproximationPValue(z float64) float64 {
	// Use the complementary error function approximation.
	// For large |z|, return very small values.
	// This is a simplified implementation suitable for regression testing purposes.
	absZ := math.Abs(z)

	// Approximation formula based on Abramowitz and Stegun.
	t := 1.0 / (1.0 + 0.2316419*absZ)
	poly := t * (0.319381530 + t*(-0.356563782+t*(1.781477937+t*(-1.821255978+t*1.330274429))))
	cdf := 1.0 - 0.3989422804014327*math.Exp(-z*z/2)*poly

	// Two-tailed p-value.
	pVal := 2.0 * (1.0 - cdf)
	if pVal > 1.0 {
		pVal = 1.0
	}
	if pVal < 0.0 {
		pVal = 0.0
	}
	return pVal
}

// validateRegressionConfig checks that all required fields are valid.
func validateRegressionConfig(cfg RegressionConfig) error {
	if cfg.OldStrategy == nil {
		return ErrNilStrategy
	}
	if cfg.NewStrategy == nil {
		return ErrNilStrategy
	}
	if cfg.BaselineRuns <= 0 && cfg.BaselineRuns != 0 { // 0 means "use default"
		return ErrInvalidRuns
	}
	if cfg.CompareRuns <= 0 && cfg.CompareRuns != 0 {
		return ErrInvalidRuns
	}
	if cfg.Confidence < 0 || cfg.Confidence > 1 {
		return ErrConfidenceRange
	}
	if cfg.MinWinRate < 0 || cfg.MinWinRate > 1 {
		return fmt.Errorf("arena: min_win_rate must be between 0 and 1, got %f", cfg.MinWinRate)
	}
	return nil
}
