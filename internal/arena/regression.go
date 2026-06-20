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

// RegressionConfig configures a regression test between two strategies.
type RegressionConfig struct {
	OldStrategy  any     // old strategy (baseline)
	NewStrategy  any     // new strategy (strategy under test)
	BaselineRuns int     // number of runs for baseline (old) strategy, default 5
	CompareRuns  int     // number of runs for new strategy, default 5
	TestSuite    string  // test suite name or identifier
	Confidence   float64 // significance level for statistical test, default 0.05
	MinWinRate   float64 // minimum win rate to consider new strategy better, default 0.55
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

	// Run both strategies concurrently using errgroup.
	var oldScores, newScores []float64
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		scores, err := rt.runStrategy(gCtx, cfg.OldStrategy, cfg.BaselineRuns)
		if err != nil {
			return fmt.Errorf("arena: run old strategy: %w", err)
		}
		oldScores = scores
		return nil
	})

	g.Go(func() error {
		scores, err := rt.runStrategy(gCtx, cfg.NewStrategy, cfg.CompareRuns)
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

// runStrategy executes a single strategy multiple times and collects scores.
// Each execution is scored via the configured Scorer interface.
func (rt *RegressionTester) runStrategy(ctx context.Context, strategy any, n int) ([]float64, error) {
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

		// Score the strategy directly.
		// In production, this could involve executing the strategy through the arena
		// service first, but for flexibility we delegate all logic to the scorer.
		score, err := rt.scorer.Score(strategy)
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
