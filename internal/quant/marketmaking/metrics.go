package marketmaking

import (
	"math"
)

// MMMetrics captures market-making performance metrics.
type MMMetrics struct {
	SpreadCapture     float64 `json:"spread_capture"`
	FillRate          float64 `json:"fill_rate"`
	AdverseSelection  float64 `json:"adverse_selection"`
	InventoryVariance float64 `json:"inventory_variance"`
	RealizedPnL       float64 `json:"realized_pnl"`
	UnrealizedPnL     float64 `json:"unrealized_pnl"`
	QuoteUptime       float64 `json:"quote_uptime"` // fraction of time quoting
	RejectRate        float64 `json:"reject_rate"`
	TotalQuotes       int     `json:"total_quotes"`
	TotalFills        int     `json:"total_fills"`
}

// ComputeMMMetrics calculates market-making performance metrics from a backtest result.
// It extracts key performance indicators from the raw backtest output into a
// structured metrics object suitable for reporting and comparison.
//
// Args:
//
//	result - the MMBacktestResult produced by MMBacktestRunner.Run().
//
// Returns:
//
//	a populated MMMetrics instance.
func ComputeMMMetrics(result *MMBacktestResult) *MMMetrics {
	if result == nil {
		return &MMMetrics{}
	}

	m := &MMMetrics{
		SpreadCapture:    result.SpreadCapture,
		AdverseSelection: result.AdverseSelection,
		RealizedPnL:      result.RealizedPnL,
		UnrealizedPnL:    result.UnrealizedPnL,
		TotalQuotes:      result.TotalQuotes,
		TotalFills:       result.TotalFills,
	}

	// Fill rate: compute from total fills / total quotes.
	if result.TotalQuotes > 0 {
		m.FillRate = round6(float64(result.TotalFills) / float64(result.TotalQuotes))
	}

	// Inventory variance: compute from equity curve position changes.
	m.InventoryVariance = computeInventoryVariance(result)

	// Quote uptime: fraction of events that generated a quote.
	if result.TotalQuotes > 0 && len(result.EquityCurve) > 0 {
		m.QuoteUptime = round6(float64(result.TotalQuotes) / float64(len(result.EquityCurve)))
	}

	// Reject rate: quotes that did not result in fills.
	if result.TotalQuotes > 0 {
		m.RejectRate = round6(1.0 - m.FillRate)
	}

	return m
}

// computeInventoryVariance calculates the variance of inventory levels over time
// using equity curve exposure data as a proxy.
func computeInventoryVariance(result *MMBacktestResult) float64 {
	if len(result.EquityCurve) < 2 {
		return 0
	}

	// Use exposure as a proxy for inventory level.
	values := make([]float64, len(result.EquityCurve))
	for i, ep := range result.EquityCurve {
		values[i] = ep.Exposure
	}

	return round6(variance(values))
}

// variance computes the population variance of a slice of floats.
func variance(values []float64) float64 {
	n := float64(len(values))
	if n == 0 {
		return 0
	}

	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= n

	sumSqDiff := 0.0
	for _, v := range values {
		diff := v - mean
		sumSqDiff += diff * diff
	}
	return sumSqDiff / n
}

// round6 rounds to 6 decimal places.
func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}
