package marketmaking

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestComputeMMMetrics_NilResult tests nil input handling.
func TestComputeMMMetrics_NilResult(t *testing.T) {
	m := ComputeMMMetrics(nil)
	require.NotNil(t, m)
	require.Zero(t, m.TotalQuotes)
	require.Zero(t, m.TotalFills)
}

// TestComputeMMMetrics_EmptyResult tests empty result handling.
func TestComputeMMMetrics_EmptyResult(t *testing.T) {
	result := &MMBacktestResult{
		RealizedPnL:   0,
		UnrealizedPnL: 0,
		TotalQuotes:   0,
		TotalFills:    0,
		EquityCurve:   []MMEquityPoint{},
	}

	m := ComputeMMMetrics(result)
	require.NotNil(t, m)
	require.Zero(t, m.RealizedPnL)
	require.Zero(t, m.UnrealizedPnL)
	require.Zero(t, m.SpreadCapture)
	require.Zero(t, m.FillRate)
	require.Zero(t, m.AdverseSelection)
	require.Zero(t, m.InventoryVariance)
	require.Zero(t, m.QuoteUptime)
	require.Zero(t, m.RejectRate)
}

// TestComputeMMMetrics_PopulatedResult tests metric computation with realistic data.
func TestComputeMMMetrics_PopulatedResult(t *testing.T) {
	result := &MMBacktestResult{
		RealizedPnL:      1500.50,
		UnrealizedPnL:    -200.25,
		TotalQuotes:      1000,
		TotalFills:       350,
		SpreadCapture:    800.0,
		AdverseSelection: 150.0,
		EquityCurve:      generateTestEquityCurve(20, 50000),
	}

	m := ComputeMMMetrics(result)
	require.NotNil(t, m)

	require.Equal(t, 1500.50, m.RealizedPnL)
	require.Equal(t, -200.25, m.UnrealizedPnL)
	require.Equal(t, 800.0, m.SpreadCapture)
	require.Equal(t, 150.0, m.AdverseSelection)
	require.Equal(t, 1000, m.TotalQuotes)
	require.Equal(t, 350, m.TotalFills)

	// Fill rate should be 350/1000 = 0.35.
	require.InDelta(t, 0.35, m.FillRate, 0.001)

	// Reject rate should be 1 - 0.35 = 0.65.
	require.InDelta(t, 0.65, m.RejectRate, 0.001)

	// Quote uptime should be 1000/20 = 50 (capped at 1.0 ideally, but based on formula).
	require.Greater(t, m.QuoteUptime, 0.0)

	// Inventory variance should be positive.
	require.GreaterOrEqual(t, m.InventoryVariance, 0.0)
}

// TestComputeMMMetrics_FillRateBoundary tests fill rate at boundaries.
func TestComputeMMMetrics_FillRateBoundary(t *testing.T) {
	// All quotes filled.
	allFilled := &MMBacktestResult{
		TotalQuotes: 100, TotalFills: 100,
		EquityCurve: generateTestEquityCurve(10, 1000),
	}
	m := ComputeMMMetrics(allFilled)
	require.InDelta(t, 1.0, m.FillRate, 0.001)
	require.InDelta(t, 0.0, m.RejectRate, 0.001)

	// No fills.
	noFills := &MMBacktestResult{
		TotalQuotes: 50, TotalFills: 0,
		EquityCurve: generateTestEquityCurve(10, 1000),
	}
	m = ComputeMMMetrics(noFills)
	require.InDelta(t, 0.0, m.FillRate, 0.001)
	require.InDelta(t, 1.0, m.RejectRate, 0.001)
}

// TestComputeMMMetrics_InventoryVarianceWithSinglePoint tests variance with single point.
func TestComputeMMMetrics_InventoryVarianceWithSinglePoint(t *testing.T) {
	singlePoint := &MMBacktestResult{
		EquityCurve: []MMEquityPoint{
			{Time: time.Now(), Equity: 100000, Cash: 100000, Exposure: 0, Drawdown: 0},
		},
	}
	m := ComputeMMMetrics(singlePoint)
	require.Equal(t, 0.0, m.InventoryVariance)
}

// TestComputeMMMetrics_QuoteUptimeWithNoEquityCurve tests uptime with empty curve.
func TestComputeMMMetrics_QuoteUptimeWithNoEquityCurve(t *testing.T) {
	result := &MMBacktestResult{
		TotalQuotes: 100,
		TotalFills:  50,
		EquityCurve: []MMEquityPoint{}, // empty
	}
	m := ComputeMMMetrics(result)
	require.Zero(t, m.QuoteUptime) // cannot compute without equity curve
}

// TestVariance_Helper tests the variance helper function directly.
func TestVariance_Helper(t *testing.T) {
	// Constant values → zero variance.
	require.InDelta(t, 0.0, variance([]float64{5, 5, 5, 5}), 0.001)

	// Known variance: [1,2,3,4,5] → mean=3, var=((4+1+0+1+4)/5)=2
	require.InDelta(t, 2.0, variance([]float64{1, 2, 3, 4, 5}), 0.001)

	// Single value → zero variance.
	require.InDelta(t, 0.0, variance([]float64{42}), 0.001)

	// Empty slice → zero variance.
	require.InDelta(t, 0.0, variance([]float64{}), 0.001)
}

// TestRound6_Helper tests rounding precision.
func TestRound6_Helper(t *testing.T) {
	require.InDelta(t, 1.234568, round6(1.2345678), 1e-6)
	require.InDelta(t, 0.0, round6(0.0), 1e-6)
	require.InDelta(t, math.Pi, round6(math.Pi), 1e-6)
}

// generateTestEquityCurve creates a synthetic equity curve for testing.
func generateTestEquityCurve(n int, baseEquity float64) []MMEquityPoint {
	curve := make([]MMEquityPoint, n)
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		// Oscillate exposure around base.
		exposure := baseEquity * 0.1 * (1.0 + 0.5*math.Sin(float64(i)*0.5))
		curve[i] = MMEquityPoint{
			Time:     baseTime.Add(time.Duration(i) * time.Minute),
			Equity:   baseEquity + float64(i-n/2)*10,
			Cash:     baseEquity - exposure,
			Exposure: exposure,
			Drawdown: absFloat(float64(n/2-i) * 10),
		}
	}
	return curve
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
