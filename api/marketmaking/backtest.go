package marketmaking

import (
	"context"
	"errors"
)

// BacktestRunner defines the interface for executing backtests.
// Implementations encapsulate data loading, strategy simulation, and
// performance metric computation.
type BacktestRunner interface {
	// Run executes the backtest with the given parameters.
	Run(ctx context.Context, req *BacktestRequest) (*BacktestResponse, error)
}

// DefaultBacktestRunner is a skeleton implementation of BacktestRunner.
// It validates input and returns a zero-value response. Replace this with
// a real implementation that loads historical data, runs strategy logic,
// simulates fills, and computes metrics.
type DefaultBacktestRunner struct {
	// TODO: add fields for data source connector, strategy engine,
	// fill simulator, and metric calculator once internal/quant
	// exposes stable interfaces for these components.
}

// NewDefaultBacktestRunner creates a new skeleton backtest runner.
//
// Returns:
//
//	runner - a backtest runner instance (skeleton implementation).
func NewDefaultBacktestRunner() *DefaultBacktestRunner {
	return &DefaultBacktestRunner{}
}

// Run executes a backtest with the given request parameters.
//
// This is a skeleton implementation that validates inputs and returns an empty
// response. A full implementation would:
//
//  1. Load historical OHLCV/tick data for req.Symbols from req.StartTime to req.EndTime.
//  2. Initialize virtual account with req.InitialCapital.
//  3. Iterate through data bars, calling strategy signal generation at each step.
//  4. Simulate order fills (mid-price or configurable slippage model).
//  5. Track positions, PnL, and trade log.
//  6. Compute performance metrics: total PnL, Sharpe ratio, max drawdown, win rate.
//
// Args:
//
//	ctx - operation context supporting cancellation.
//	req - backtest parameters including symbols, time window, and initial capital.
//
// Returns:
//
//	response - detailed backtest results with PnL, metrics, and trade log.
//	err - validation error if request is invalid, or execution error.
func (r *DefaultBacktestRunner) Run(ctx context.Context, req *BacktestRequest) (*BacktestResponse, error) {
	if req == nil {
		return nil, errNilRequest
	}
	if len(req.Symbols) == 0 {
		return nil, errNoSymbols
	}
	if req.InitialCapital <= 0 {
		return nil, errInvalidCapital
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// FIX: return ErrNotImplemented instead of zero-value response so callers
	// can distinguish "feature not wired" from legitimate empty results (code rule 9).
	return nil, ErrNotImplemented
}

var (
	errNilRequest     = errors.New("backtest request is nil")
	errNoSymbols      = errors.New("no symbols in backtest request")
	errInvalidCapital = errors.New("initial capital must be > 0")
)
