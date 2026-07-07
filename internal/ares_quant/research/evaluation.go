package research

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// EvaluationMetrics holds accuracy metrics for research layer decisions.
type EvaluationMetrics struct {
	DirectionHitRate    float64        `json:"direction_hit_rate"`    // direction prediction accuracy
	FutureReturnBucket  map[string]int `json:"future_return_bucket"`  // N-day return distribution
	DrawdownAwareReturn float64        `json:"drawdown_aware_return"` // return adjusted for drawdown
	CalibrationScore    float64        `json:"calibration_score"`     // confidence vs actual match
	TotalDecisions      int            `json:"total_decisions"`
	EvalPeriodStart     time.Time      `json:"eval_period_start"`
	EvalPeriodEnd       time.Time      `json:"eval_period_end"`
}

// HistoricalDecision pairs a past decision with its outcome.
type HistoricalDecision struct {
	Symbol           string    `json:"symbol"`
	Date             time.Time `json:"date"`
	Rating           string    `json:"rating"`               // Buy/Sell/Hold
	Confidence       float64   `json:"confidence"`           // 0–1
	FutureReturn     float64   `json:"future_return_n_days"` // actual N-day return
	DirectionCorrect bool      `json:"direction_correct"`
}

// Evaluator batches historical decisions and computes accuracy metrics.
type Evaluator struct {
	decisions []*HistoricalDecision
}

// NewEvaluator creates a new empty Evaluator.
//
// Returns:
//
//	a ready-to-use Evaluator instance.
func NewEvaluator() *Evaluator {
	return &Evaluator{
		decisions: make([]*HistoricalDecision, 0),
	}
}

// AddDecision appends a historical decision for later evaluation.
//
// Args:
//
//	d - the decision record to add.
func (ev *Evaluator) AddDecision(d *HistoricalDecision) {
	if d == nil {
		return
	}
	ev.decisions = append(ev.decisions, d)
}

// Evaluate computes accuracy metrics from all accumulated decisions.
//
// Args:
//
//	ndays - the look-ahead window used for FutureReturn (for documentation).
//
// Returns:
//
//	computed EvaluationMetrics, or an error if no decisions were added.
func (ev *Evaluator) Evaluate(ndays int) (*EvaluationMetrics, error) {
	if len(ev.decisions) == 0 {
		return nil, fmt.Errorf("no decisions to evaluate")
	}

	metrics := &EvaluationMetrics{
		TotalDecisions:     len(ev.decisions),
		FutureReturnBucket: make(map[string]int),
		EvalPeriodStart:    ev.decisions[0].Date,
		EvalPeriodEnd:      ev.decisions[len(ev.decisions)-1].Date,
	}

	// Compute direction hit rate.
	hits := 0
	var returns []float64
	var confidenceSum float64
	calibratedHits := 0.0

	for _, d := range ev.decisions {
		if d.DirectionCorrect {
			hits++
		}

		returns = append(returns, d.FutureReturn)
		confidenceSum += d.Confidence

		// Calibration: check if confidence matches correctness.
		// High confidence (>0.7) should be correct more often than low confidence.
		if d.Confidence > 0.7 && d.DirectionCorrect {
			calibratedHits++
		} else if d.Confidence <= 0.3 && !d.DirectionCorrect {
			calibratedHits++
		}

		// Bucket future returns.
		bucket := returnBucket(d.FutureReturn)
		metrics.FutureReturnBucket[bucket]++
	}

	if metrics.TotalDecisions > 0 {
		metrics.DirectionHitRate = round4(float64(hits) / float64(metrics.TotalDecisions))
	}

	// Drawdown-aware return: use Sortino-like adjustment.
	metrics.DrawdownAwareReturn = round4(drawdownAwareReturn(returns))

	// Calibration score: fraction of well-calibrated predictions.
	if metrics.TotalDecisions > 0 {
		metrics.CalibrationScore = round4(calibratedHits / float64(metrics.TotalDecisions))
	}

	return metrics, nil
}

// ToJSON serializes the evaluator's current state and computed metrics to JSON.
//
// Returns:
//
//	JSON bytes containing decisions and latest evaluation, or an error.
func (ev *Evaluator) ToJSON() ([]byte, error) {
	data := struct {
		Decisions  []*HistoricalDecision `json:"decisions"`
		TotalAdded int                   `json:"total_added"`
	}{
		Decisions:  ev.decisions,
		TotalAdded: len(ev.decisions),
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal evaluator: %w", err)
	}
	return raw, nil
}

// returnBucket classifies a return value into a descriptive bucket.
func returnBucket(ret float64) string {
	switch {
	case ret > 0.10:
		return ">+10%"
	case ret > 0.05:
		return "+5%~+10%"
	case ret > 0:
		return "0%~+5%"
	case ret == 0:
		return "0%"
	case ret > -0.05:
		return "-5%~0%"
	case ret > -0.10:
		return "-10%~-5%"
	default:
		return "<-10%"
	}
}

// drawdownAwareReturn computes a drawdown-adjusted average return.
// It penalizes sequences of negative returns more heavily.
func drawdownAwareReturn(returns []float64) float64 {
	if len(returns) == 0 {
		return 0
	}

	sort.Float64s(returns)

	avgReturn := 0.0
	negativeSum := 0.0
	negativeCount := 0

	for _, r := range returns {
		avgReturn += r
		if r < 0 {
			negativeSum += r
			negativeCount++
		}
	}

	avgReturn /= float64(len(returns))

	// Adjust for downside: subtract squared negative component.
	variancePenalty := 0.0
	if negativeCount > 0 {
		avgNeg := negativeSum / float64(negativeCount)
		variancePenalty = avgNeg * avgNeg // square of avg negative return
	}

	return avgReturn + variancePenalty
}

// round4 rounds a float64 to 4 decimal places.
func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
