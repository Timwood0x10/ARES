package dataflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_quant/market"
)

// ─── SnapshotBuilder Error Scenario Tests ───────────────────
// These tests verify that SnapshotBuilder.Build returns distinct, identifiable
// errors for each data quality violation: staleness, missing data, and lookahead.

// TestSnapshotBuilder_StaleData verifies that a candle older than MaxStaleDuration
// triggers ErrStaleData from the validator inside Build.
func TestSnapshotBuilder_StaleData(t *testing.T) {
	analysisDate := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	// Candle is 6 days old — exceeds the 5-day threshold.
	staleDate := analysisDate.Add(-6 * 24 * time.Hour)

	candles := []market.Candle{
		{Ticker: "STALE", Date: staleDate, Open: 99, High: 101, Low: 98, Close: 100, Volume: 1_000_000},
	}

	router := NewVendorRouter(&RouterConfig{CoreStockAPIs: []string{"test"}})
	router.Register(&mockVendor{
		name:      "test",
		available: true,
		candles:   candles,
	})

	val := NewValidator(&ValidationConfig{
		MaxStaleDuration: 5 * 24 * time.Hour,
		AnalysisDate:     analysisDate,
		IsBacktestMode:   false,
	})
	builder := NewSnapshotBuilder(router, val)

	_, err := builder.Build(context.Background(), "STALE", analysisDate)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrStaleData),
		"expected ErrStaleData for stale candle, got: %v", err)
}

// TestSnapshotBuilder_NoMarketData verifies that an empty candle list returned
// from the vendor triggers ErrNoMarketData.
func TestSnapshotBuilder_NoMarketData(t *testing.T) {
	router := NewVendorRouter(&RouterConfig{CoreStockAPIs: []string{"empty"}})
	router.Register(&mockVendor{
		name:      "empty",
		available: true,
		candles:   []market.Candle{}, // empty — no data
	})

	val := NewValidator(&ValidationConfig{
		AnalysisDate: time.Now(),
	})
	builder := NewSnapshotBuilder(router, val)

	_, err := builder.Build(context.Background(), "EMPTY", time.Now())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoMarketData),
		"expected ErrNoMarketData for empty candles, got: %v", err)
}

// TestSnapshotBuilder_LookaheadData verifies that in backtest mode, a candle
// dated after the analysis date triggers ErrLookaheadData.
func TestSnapshotBuilder_LookaheadData(t *testing.T) {
	analysisDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	futureDate := analysisDate.Add(24 * time.Hour) // June 2 — after analysis date

	candles := []market.Candle{
		{Ticker: "FUTURE", Date: analysisDate.Add(-24 * time.Hour), Close: 99},
		{Ticker: "FUTURE", Date: futureDate, Close: 101}, // future candle!
	}

	router := NewVendorRouter(&RouterConfig{CoreStockAPIs: []string{"bt"}})
	router.Register(&mockVendor{
		name:      "bt",
		available: true,
		candles:   candles,
	})

	val := NewValidator(&ValidationConfig{
		AnalysisDate:   analysisDate,
		IsBacktestMode: true, // backtest mode enables lookahead check
	})
	builder := NewSnapshotBuilder(router, val)

	_, err := builder.Build(context.Background(), "FUTURE", analysisDate)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrLookaheadData),
		"expected ErrLookaheadData for future candle in backtest mode, got: %v", err)
}

// TestSnapshotBuilder_DistinctErrors verifies that all three error types are
// distinguishable via errors.Is (no false positives between sentinels).
func TestSnapshotBuilder_DistinctErrors(t *testing.T) {
	analysisDate := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)

	sentinels := []struct {
		name    string
		err     error
		target  error
		candles []market.Candle
		cfg     *ValidationConfig
	}{
		{
			name:    "stale",
			target:  ErrStaleData,
			candles: []market.Candle{{Date: analysisDate.Add(-6 * 24 * time.Hour), Close: 100}},
			cfg: &ValidationConfig{
				MaxStaleDuration: 5 * 24 * time.Hour,
				AnalysisDate:     analysisDate,
			},
		},
		{
			name:    "no_data",
			target:  ErrNoMarketData,
			candles: []market.Candle{},
			cfg:     &ValidationConfig{AnalysisDate: analysisDate},
		},
		{
			name:   "lookahead",
			target: ErrLookaheadData,
			candles: []market.Candle{
				{Date: analysisDate.Add(24 * time.Hour), Close: 101},
			},
			cfg: &ValidationConfig{
				AnalysisDate:   analysisDate,
				IsBacktestMode: true,
			},
		},
	}

	for _, tc := range sentinels {
		t.Run(tc.name, func(t *testing.T) {
			router := NewVendorRouter(&RouterConfig{CoreStockAPIs: []string{tc.name}})
			router.Register(&mockVendor{
				name:      tc.name,
				available: true,
				candles:   tc.candles,
			})
			val := NewValidator(tc.cfg)
			builder := NewSnapshotBuilder(router, val)

			_, err := builder.Build(context.Background(), tc.name, analysisDate)
			require.Error(t, err, "%s: expected error", tc.name)
			assert.True(t, errors.Is(err, tc.target),
				"%s: expected %v, got: %v", tc.name, tc.target, err)

			// Verify no cross-matching with other sentinels.
			for _, other := range sentinels {
				if other.target != tc.target {
					assert.False(t, errors.Is(err, other.target),
						"%s: error should NOT match %v", tc.name, other.target)
				}
			}
		})
	}
}
