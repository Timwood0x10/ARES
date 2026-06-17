package dataflow

import (
	"context"
	"fmt"
	"time"

	"goagentx/internal/quant/indicators"
	"goagentx/internal/quant/market"
)

// VerifiedMarketSnapshot provides a deterministic, validated view of market data
// for a specific analysis date. It constrains analysts to use only verified data.
type VerifiedMarketSnapshot struct {
	RequestedDate time.Time          `json:"requested_date"`
	LatestRowDate time.Time          `json:"latest_row_date"`
	OHLCV         *market.Candle     `json:"ohlcv"`
	Indicators    map[string]float64 `json:"indicators"`
	RecentCloses  []float64          `json:"recent_closes"`
	Warning       string             `json:"warning"`
}

// SnapshotWarning is the standard warning attached to every verified snapshot.
const SnapshotWarning = "conflicting tool output must be flagged, not reconciled by imagination"

// recentCloseCount is the number of recent closing prices to include in the snapshot.
const recentCloseCount = 10

// SnapshotBuilder constructs validated market snapshots using a router and validator.
type SnapshotBuilder struct {
	router    *VendorRouter
	validator *Validator
}

// NewSnapshotBuilder creates a new SnapshotBuilder.
func NewSnapshotBuilder(router *VendorRouter, validator *Validator) *SnapshotBuilder {
	return &SnapshotBuilder{
		router:    router,
		validator: validator,
	}
}

// Build constructs a VerifiedMarketSnapshot for the given symbol and analysis date.
// It:
//  1. Fetches OHLCV data via the router.
//  2. Validates freshness via the validator.
//  3. Computes common technical indicators on the close prices.
//  4. Extracts the most recent N closing prices.
//  5. Assembles the snapshot with the anti-hallucination warning attached.
func (b *SnapshotBuilder) Build(ctx context.Context, symbol string, date time.Time) (*VerifiedMarketSnapshot, error) {
	if symbol == "" {
		return nil, fmt.Errorf("%w: symbol is empty", ErrSymbolEmpty)
	}

	// Step 1: Fetch OHLCV via router.
	result, err := b.router.Route(ctx, "candles", VendorArgs{
		Symbol: symbol,
		Days:   180,
		Method: "candles",
	})
	if err != nil {
		return nil, fmt.Errorf("build snapshot: %w", err)
	}

	candles, ok := result.Data.([]market.Candle)
	if !ok || len(candles) == 0 {
		return nil, fmt.Errorf("build snapshot: %w: no candles returned from vendor %s",
			ErrNoMarketData, result.Vendor)
	}

	// Step 2: Validate freshness.
	val := NewValidator(&ValidationConfig{
		MaxStaleDuration:   5 * 24 * time.Hour,
		HolidayGracePeriod: 72 * time.Hour,
		AnalysisDate:       date,
		IsBacktestMode:     !date.IsZero() && date.Before(time.Now()),
	})
	if err := val.ValidateOHLCV(candles); err != nil {
		return nil, fmt.Errorf("build snapshot validation failed: %w", err)
	}

	// Step 3: Extract latest OHLCV candle.
	latestCandle := candles[len(candles)-1]

	// Step 4: Compute indicators from close prices.
	closes := extractCloses(candles)
	indicators := computeIndicators(closes)

	// Step 5: Recent closes (last N).
	recentCloses := lastNCloses(closes, recentCloseCount)

	// Step 6: Assemble snapshot with warning.
	snapshot := &VerifiedMarketSnapshot{
		RequestedDate: date,
		LatestRowDate: latestCandle.Date,
		OHLCV:         &latestCandle,
		Indicators:    indicators,
		RecentCloses:  recentCloses,
		Warning:       SnapshotWarning,
	}
	return snapshot, nil
}

// computeIndicators calculates common technical indicator values from a price series.
func computeIndicators(prices []float64) map[string]float64 {
	result := make(map[string]float64, 8)

	rsi := indicators.RSI(prices, 14)
	if len(rsi) > 0 {
		result["RSI_14"] = rsi[len(rsi)-1]
	}

	sma20 := indicators.SMA(prices, 20)
	if len(sma20) > 0 {
		result["SMA_20"] = sma20[len(sma20)-1]
	}

	sma50 := indicators.SMA(prices, 50)
	if len(sma50) > 0 {
		result["SMA_50"] = sma50[len(sma50)-1]
	}

	macdLine, signalLine, hist := indicators.MACD(prices, 12, 26, 9)
	if len(macdLine) > 0 {
		result["MACD"] = macdLine[len(macdLine)-1]
	}
	if len(signalLine) > 0 {
		result["MACD_Signal"] = signalLine[len(signalLine)-1]
	}
	if len(hist) > 0 {
		result["MACD_Hist"] = hist[len(hist)-1]
	}

	upper, lower, _ := indicators.BollingerBands(prices, 20, 2.0)
	if len(upper) > 0 && len(lower) > 0 {
		result["BB_Upper"] = upper[len(upper)-1]
		result["BB_Lower"] = lower[len(lower)-1]
	}

	return result
}

// extractCloses extracts closing prices from a slice of candles.
func extractCloses(candles []market.Candle) []float64 {
	if len(candles) == 0 {
		return nil
	}
	prices := make([]float64, len(candles))
	for i, c := range candles {
		prices[i] = c.Close
	}
	return prices
}

// lastNCloses returns the last n closing prices, or all if fewer than n exist.
func lastNCloses(prices []float64, n int) []float64 {
	if len(prices) == 0 {
		return nil
	}
	if len(prices) <= n {
		cp := make([]float64, len(prices))
		copy(cp, prices)
		return cp
	}
	cp := make([]float64, n)
	copy(cp, prices[len(prices)-n:])
	return cp
}
