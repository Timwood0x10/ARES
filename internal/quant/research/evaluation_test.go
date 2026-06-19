package research

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewEvaluator tests constructor.
func TestNewEvaluator(t *testing.T) {
	ev := NewEvaluator()
	require.NotNil(t, ev)
	require.Empty(t, ev.decisions)
}

// TestEvaluator_AddDecision tests adding decisions.
func TestEvaluator_AddDecision(t *testing.T) {
	ev := NewEvaluator()

	ev.AddDecision(&HistoricalDecision{
		Symbol: "AAPL", Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Rating: "Buy", Confidence: 0.8, FutureReturn: 0.05, DirectionCorrect: true,
	})
	ev.AddDecision(&HistoricalDecision{
		Symbol: "GOOG", Date: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Rating: "Sell", Confidence: 0.6, FutureReturn: -0.03, DirectionCorrect: true,
	})
	ev.AddDecision(nil) // nil should be ignored

	require.Len(t, ev.decisions, 2)
}

// TestEvaluator_Evaluate_HitRate tests direction hit rate computation.
func TestEvaluator_Evaluate_HitRate(t *testing.T) {
	ev := NewEvaluator()

	// 7 correct out of 10 = 70%
	for i := 0; i < 7; i++ {
		ev.AddDecision(&HistoricalDecision{
			Symbol: "STOCK", Date: time.Date(2024, 1, 1+i, 0, 0, 0, 0, time.UTC),
			Rating: "Buy", Confidence: 0.7, FutureReturn: 0.02, DirectionCorrect: true,
		})
	}
	for i := 0; i < 3; i++ {
		ev.AddDecision(&HistoricalDecision{
			Symbol: "STOCK", Date: time.Date(2024, 1, 10+i, 0, 0, 0, 0, time.UTC),
			Rating: "Sell", Confidence: 0.5, FutureReturn: 0.01, DirectionCorrect: false,
		})
	}

	metrics, err := ev.Evaluate(5)
	require.NoError(t, err)
	require.InDelta(t, 0.7, metrics.DirectionHitRate, 0.001)
	require.Equal(t, 10, metrics.TotalDecisions)
}

// TestEvaluator_Evaluate_ReturnBuckets tests future return bucketing.
func TestEvaluator_Evaluate_ReturnBuckets(t *testing.T) {
	ev := NewEvaluator()

	ev.AddDecision(&HistoricalDecision{
		Symbol: "H", Date: time.Now(), Rating: "Hold",
		Confidence: 0.5, FutureReturn: 0.12, DirectionCorrect: true,
	})
	ev.AddDecision(&HistoricalDecision{
		Symbol: "L", Date: time.Now(), Rating: "Buy",
		Confidence: 0.8, FutureReturn: -0.15, DirectionCorrect: false,
	})
	ev.AddDecision(&HistoricalDecision{
		Symbol: "M", Date: time.Now(), Rating: "Sell",
		Confidence: 0.3, FutureReturn: 0.02, DirectionCorrect: true,
	})

	metrics, err := ev.Evaluate(10)
	require.NoError(t, err)
	require.Contains(t, metrics.FutureReturnBucket, ">+10%")
	require.Contains(t, metrics.FutureReturnBucket, "<-10%")
	require.Contains(t, metrics.FutureReturnBucket, "0%~+5%")
	require.Equal(t, 1, metrics.FutureReturnBucket[">+10%"])
	require.Equal(t, 1, metrics.FutureReturnBucket["<-10%"])
}

// TestEvaluator_Evaluate_CalibrationScore tests calibration computation.
func TestEvaluator_Evaluate_CalibrationScore(t *testing.T) {
	ev := NewEvaluator()

	// Well-calibrated: high confidence predictions are correct.
	ev.AddDecision(&HistoricalDecision{
		Symbol: "A", Date: time.Now(), Rating: "Buy",
		Confidence: 0.9, FutureReturn: 0.08, DirectionCorrect: true,
	})
	ev.AddDecision(&HistoricalDecision{
		Symbol: "B", Date: time.Now(), Rating: "Sell",
		Confidence: 0.85, FutureReturn: -0.06, DirectionCorrect: true,
	})
	// Low confidence wrong prediction (also calibrated).
	ev.AddDecision(&HistoricalDecision{
		Symbol: "C", Date: time.Now(), Rating: "Hold",
		Confidence: 0.2, FutureReturn: 0.01, DirectionCorrect: false,
	})
	// Miscalibrated: high confidence but wrong.
	ev.AddDecision(&HistoricalDecision{
		Symbol: "D", Date: time.Now(), Rating: "Buy",
		Confidence: 0.95, FutureReturn: -0.05, DirectionCorrect: false,
	})

	metrics, err := ev.Evaluate(5)
	require.NoError(t, err)
	// 3 out of 4 are calibrated (A, B, C correct; D is miscalibrated).
	require.InDelta(t, 0.75, metrics.CalibrationScore, 0.01)
}

// TestEvaluator_Evaluate_Empty tests evaluation with no decisions.
func TestEvaluator_Evaluate_Empty(t *testing.T) {
	ev := NewEvaluator()
	_, err := ev.Evaluate(5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no decisions")
}

// TestEvaluator_ToJSON tests JSON serialization.
func TestEvaluator_ToJSON(t *testing.T) {
	ev := NewEvaluator()
	ev.AddDecision(&HistoricalDecision{
		Symbol: "JSON", Date: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
		Rating: "Buy", Confidence: 0.75, FutureReturn: 0.04, DirectionCorrect: true,
	})

	data, err := ev.ToJSON()
	require.NoError(t, err)
	require.Contains(t, string(data), `"symbol": "JSON"`)
	require.Contains(t, string(data), `"rating": "Buy"`)
	require.Contains(t, string(data), `"total_added": 1`)

	// Verify it's valid JSON.
	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
}

// TestEvaluator_EvaluationSummaryFormat tests output format validation.
func TestEvaluator_EvaluationSummaryFormat(t *testing.T) {
	ev := NewEvaluator()

	// Add fixture data: mixed outcomes.
	fixtureData := []struct {
		rating  string
		conf    float64
		ret     float64
		correct bool
	}{
		{"Buy", 0.85, 0.07, true},
		{"Sell", 0.70, -0.04, true},
		{"Buy", 0.60, -0.02, false},
		{"Hold", 0.40, 0.01, false},
		{"Buy", 0.90, 0.12, true},
		{"Sell", 0.55, -0.08, true},
	}

	for _, f := range fixtureData {
		ev.AddDecision(&HistoricalDecision{
			Symbol: "FIX", Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Rating: f.rating, Confidence: f.conf, FutureReturn: f.ret, DirectionCorrect: f.correct,
		})
	}

	metrics, err := ev.Evaluate(10)
	require.NoError(t, err)

	// Verify all required fields are populated.
	require.GreaterOrEqual(t, metrics.DirectionHitRate, 0.0)
	require.LessOrEqual(t, metrics.DirectionHitRate, 1.0)
	require.NotEmpty(t, metrics.FutureReturnBucket)
	require.GreaterOrEqual(t, metrics.CalibrationScore, 0.0)
	require.LessOrEqual(t, metrics.CalibrationScore, 1.0)
	require.Equal(t, 6, metrics.TotalDecisions)
	require.False(t, metrics.EvalPeriodStart.IsZero())
	require.False(t, metrics.EvalPeriodEnd.IsZero())

	// Verify JSON round-trip produces valid evaluation_summary.json format.
	jsonData, _ := json.MarshalIndent(metrics, "", "  ")
	require.Contains(t, string(jsonData), `"direction_hit_rate"`)
	require.Contains(t, string(jsonData), `"future_return_bucket"`)
	require.Contains(t, string(jsonData), `"calibration_score"`)
	require.Contains(t, string(jsonData), `"total_decisions"`)
}

// TestEvaluator_PerfectAccuracy tests edge case where all decisions are correct.
func TestEvaluator_PerfectAccuracy(t *testing.T) {
	ev := NewEvaluator()
	for i := 0; i < 5; i++ {
		ev.AddDecision(&HistoricalDecision{
			Symbol: "PERFECT", Date: time.Now(),
			Rating: "Buy", Confidence: 0.8, FutureReturn: 0.05, DirectionCorrect: true,
		})
	}

	metrics, _ := ev.Evaluate(5)
	require.InDelta(t, 1.0, metrics.DirectionHitRate, 0.001)
}

// TestEvaluator_ZeroAccuracy tests edge case where all decisions are wrong.
func TestEvaluator_ZeroAccuracy(t *testing.T) {
	ev := NewEvaluator()
	for i := 0; i < 5; i++ {
		ev.AddDecision(&HistoricalDecision{
			Symbol: "WRONG", Date: time.Now(),
			Rating: "Buy", Confidence: 0.8, FutureReturn: -0.05, DirectionCorrect: false,
		})
	}

	metrics, _ := ev.Evaluate(5)
	require.InDelta(t, 0.0, metrics.DirectionHitRate, 0.001)
}
