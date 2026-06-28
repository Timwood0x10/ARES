package dataflow

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_quant/market"
)

// ValidationConfig configures the data freshness and lookahead guard behavior.
type ValidationConfig struct {
	MaxStaleDuration   time.Duration // e.g., 5 * 24 * time.Hour
	HolidayGracePeriod time.Duration // e.g., 72 * time.Hour
	AnalysisDate       time.Time
	IsBacktestMode     bool
}

// Validator checks market data for staleness, lookahead violations, and completeness.
type Validator struct {
	maxStaleDuration   time.Duration
	holidayGracePeriod time.Duration
	analysisDate       time.Time
	isBacktestMode     bool
}

// NewValidator creates a new Validator with the given configuration.
func NewValidator(cfg *ValidationConfig) *Validator {
	if cfg == nil {
		return &Validator{
			maxStaleDuration:   5 * 24 * time.Hour,
			holidayGracePeriod: 72 * time.Hour,
			isBacktestMode:     false,
		}
	}
	return &Validator{
		maxStaleDuration:   cfg.MaxStaleDuration,
		holidayGracePeriod: cfg.HolidayGracePeriod,
		analysisDate:       cfg.AnalysisDate,
		isBacktestMode:     cfg.IsBacktestMode,
	}
}

// ValidateOHLCV checks that OHLCV data is fresh enough for analysis.
//
// Rules:
//   - Empty candles → ErrNoMarketData
//   - Latest candle older than maxStaleDuration (minus grace) → ErrStaleData
//   - In backtest mode, any candle date after analysisDate → ErrLookaheadData
func (v *Validator) ValidateOHLCV(candles []market.Candle) error {
	if len(candles) == 0 {
		return fmt.Errorf("%w: candle list is empty", ErrNoMarketData)
	}

	latest := candles[len(candles)-1]
	effectiveStale := v.maxStaleDuration

	// Check for stale data.
	age := v.analysisDate.Sub(latest.Date)
	if age > effectiveStale {
		return fmt.Errorf(
			"%w: latest candle dated %s is %v old relative to analysis date %s (threshold: %v)",
			ErrStaleData, latest.Date.Format("2006-01-02"), age,
			v.analysisDate.Format("2006-01-02"), effectiveStale,
		)
	}

	// In backtest mode, reject future data.
	if v.isBacktestMode {
		for i := range candles {
			if candles[i].Date.After(v.analysisDate) {
				return fmt.Errorf(
					"%w: candle at index %d dated %s is after analysis date %s",
					ErrLookaheadData, i, candles[i].Date.Format("2006-01-02"),
					v.analysisDate.Format("2006-01-02"),
				)
			}
		}
	}

	return nil
}

// ValidateNewsDates checks that no news article date falls after the analysis
// date in backtest mode. This prevents the model from using future information.
func (v *Validator) ValidateNewsDates(dates []time.Time) error {
	if !v.isBacktestMode || len(dates) == 0 {
		return nil
	}
	for i, d := range dates {
		if d.After(v.analysisDate) {
			return fmt.Errorf(
				"%w: news at index %d dated %s is after analysis date %s",
				ErrLookaheadData, i, d.Format("2006-01-02"),
				v.analysisDate.Format("2006-01-02"),
			)
		}
	}
	return nil
}

// ValidateWithContext runs validation with a context check for cancellation.
func (v *Validator) ValidateWithContext(ctx context.Context, candles []market.Candle) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return v.ValidateOHLCV(candles)
	}
}
