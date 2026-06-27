// Package evolution provides automatic experience extraction from flight recorder diagnostics.
// It bridges the flight recording system with the experience store to enable
// continuous learning from agent execution failures and anomalies.
package evolution

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// ShadowComparison records the result of comparing active vs shadow strategy
// performance on a single evaluation.
type ShadowComparison struct {
	// ActiveScore is the score achieved by the active (current) strategy.
	ActiveScore float64

	// ShadowScore is the score achieved by the shadow (candidate) strategy.
	ShadowScore float64

	// ShadowWon indicates whether the shadow strategy outperformed the active one.
	ShadowWon bool

	// Timestamp records when this comparison was made.
	Timestamp time.Time
}

// ShadowReport summarizes shadow evaluation results and provides a deployment
// recommendation.
type ShadowReport struct {
	// TotalComparisons is the number of comparison results collected.
	TotalComparisons int

	// ShadowWins is the count of comparisons where the shadow strategy won.
	ShadowWins int

	// WinRate is the proportion of comparisons won by the shadow strategy.
	WinRate float64

	// Recommendation describes the suggested action based on evaluation results.
	Recommendation string
}

// ShadowEvaluationConfig configures the shadow evaluation behavior for safe
// strategy deployment.
type ShadowEvaluationConfig struct {
	// Enabled enables shadow evaluation when true.
	Enabled bool `json:"enabled"`

	// MinSamples is the minimum number of comparison samples required before
	// making a deployment decision. Default is 10.
	MinSamples int `json:"min_samples"`

	// MinWinRate is the minimum win rate required for the shadow strategy to
	// be recommended for deployment. Default is 0.55.
	MinWinRate float64 `json:"min_win_rate"`

	// EvaluationInterval is the time between evaluation rounds.
	EvaluationInterval time.Duration `json:"evaluation_interval"`
}

// DefaultShadowEvaluationConfig returns sensible defaults for shadow evaluation.
//
// Returns:
//
//	ShadowEvaluationConfig - configuration with default values.
func DefaultShadowEvaluationConfig() ShadowEvaluationConfig {
	return ShadowEvaluationConfig{
		Enabled:            false,
		MinSamples:         10,
		MinWinRate:         0.55,
		EvaluationInterval: 10 * time.Minute,
	}
}

// ShadowEvaluator enables safe deployment comparison by running the active and
// a candidate strategy side by side, collecting comparison results, and
// recommending deployment only when the candidate demonstrates sufficient
// improvement.
type ShadowEvaluator struct {
	activeStrategy *mutation.Strategy
	shadowStrategy *mutation.Strategy
	shadowResults  []ShadowComparison
	minSamples     int
	minWinRate     float64
	shadowScorer   func(*mutation.Strategy) float64 // optional independent scorer
	mu             sync.RWMutex
}

// NewShadowEvaluator creates a ShadowEvaluator for safe strategy comparison.
//
// Args:
//
//	cfg - configuration for shadow evaluation behavior.
//
// Returns:
//
//	*ShadowEvaluator - the configured evaluator instance.
func NewShadowEvaluator(cfg ShadowEvaluationConfig) *ShadowEvaluator {
	minSamples := cfg.MinSamples
	if minSamples <= 0 {
		minSamples = 10
	}
	minWinRate := cfg.MinWinRate
	if minWinRate <= 0 {
		minWinRate = 0.55
	}

	return &ShadowEvaluator{
		shadowResults: make([]ShadowComparison, 0),
		minSamples:    minSamples,
		minWinRate:    minWinRate,
	}
}

// StartShadow begins shadow evaluation of a candidate strategy. The active
// strategy should be set before calling this.
//
// Args:
//
//	candidate - the candidate strategy to evaluate.
func (e *ShadowEvaluator) StartShadow(candidate *mutation.Strategy) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.shadowStrategy = candidate
	// Reset previous results when starting a new shadow evaluation.
	e.shadowResults = make([]ShadowComparison, 0)
}

// RecordResult records a comparison result between the active and shadow
// strategies.
//
// Args:
//
//	activeScore - the score from the active strategy.
//	shadowScore - the score from the shadow strategy.
func (e *ShadowEvaluator) RecordResult(activeScore, shadowScore float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	comparison := ShadowComparison{
		ActiveScore: activeScore,
		ShadowScore: shadowScore,
		ShadowWon:   shadowScore > activeScore,
		Timestamp:   time.Now(),
	}
	e.shadowResults = append(e.shadowResults, comparison)
}

// ShouldDeploy determines whether the shadow strategy should be deployed based
// on accumulated comparison results. It uses majority voting with a configurable
// minimum sample count and win rate threshold.
//
// Returns:
//
//	bool - true if the shadow strategy should be deployed.
//	*ShadowReport - detailed report of the evaluation, or nil if no results exist.
func (e *ShadowEvaluator) ShouldDeploy() (bool, *ShadowReport) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	total := len(e.shadowResults)
	if total == 0 {
		return false, nil
	}

	shadowWins := 0
	for _, r := range e.shadowResults {
		if r.ShadowWon {
			shadowWins++
		}
	}

	winRate := float64(shadowWins) / float64(total)
	report := &ShadowReport{
		TotalComparisons: total,
		ShadowWins:       shadowWins,
		WinRate:          winRate,
	}

	if total < e.minSamples {
		report.Recommendation = fmt.Sprintf(
			"insufficient samples: need %d, have %d",
			e.minSamples, total,
		)
		return false, report
	}

	if winRate >= e.minWinRate {
		report.Recommendation = "shadow strategy outperforms active, recommend deployment"
		return true, report
	}

	report.Recommendation = fmt.Sprintf(
		"shadow win rate %.1f%% below threshold %.1f%%, keep active",
		winRate*100, e.minWinRate*100,
	)
	return false, report
}

// ActiveStrategy returns the active strategy.
//
// Returns:
//
//	*mutation.Strategy - the active strategy, or nil if not set.
func (e *ShadowEvaluator) ActiveStrategy() *mutation.Strategy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeStrategy
}

// SetActiveStrategy sets the active strategy for comparison.
//
// Args:
//
//	s - the active strategy.
func (e *ShadowEvaluator) SetActiveStrategy(s *mutation.Strategy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeStrategy = s
}

// ShadowStrategy returns the shadow (candidate) strategy.
//
// Returns:
//
//	*mutation.Strategy - the shadow strategy, or nil if not set.
func (e *ShadowEvaluator) ShadowStrategy() *mutation.Strategy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.shadowStrategy
}

// SetShadowScorer sets an independent scoring function for shadow evaluation.
// When set, Evaluate() uses this scorer to compare active vs shadow strategies
// independently of the caller-provided scores.
//
// Args:
//   - scorer: scoring function (use nil to clear).
func (e *ShadowEvaluator) SetShadowScorer(scorer func(*mutation.Strategy) float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shadowScorer = scorer
}

// HasIndependentScorer returns true if an independent scorer is configured.
// When true, Evaluate() can be used instead of manual RecordResult() calls.
//
// Returns:
//
//	bool - true if an independent scorer is set.
func (e *ShadowEvaluator) HasIndependentScorer() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.shadowScorer != nil
}

// Evaluate scores both active and shadow strategies using the independent
// scorer (if set) and records the comparison result. Returns the active and
// shadow scores. If no scorer is set, returns (-1, -1) without recording.
//
// Args:
//   - ctx: operation context for cancellation.
//
// Returns:
//   - activeScore: the score from the active strategy.
//   - shadowScore: the score from the shadow strategy.
func (e *ShadowEvaluator) Evaluate(ctx context.Context) (float64, float64) {
	e.mu.RLock()
	scorer := e.shadowScorer
	active := e.activeStrategy
	shadow := e.shadowStrategy
	e.mu.RUnlock()

	if scorer == nil || active == nil || shadow == nil {
		return -1, -1
	}

	activeScore := scorer(active)
	shadowScore := scorer(shadow)

	e.RecordResult(activeScore, shadowScore)
	return activeScore, shadowScore
}

// Results returns a copy of all recorded comparison results.
//
// Returns:
//
//	[]ShadowComparison - copy of all recorded comparisons.
func (e *ShadowEvaluator) Results() []ShadowComparison {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]ShadowComparison, len(e.shadowResults))
	copy(result, e.shadowResults)
	return result
}

// Reset clears all evaluation state.
func (e *ShadowEvaluator) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.shadowStrategy = nil
	e.shadowResults = make([]ShadowComparison, 0)
}
