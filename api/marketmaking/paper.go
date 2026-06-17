package marketmaking

import (
	"context"
	"errors"
)

// PaperTrader defines the interface for simulated (paper) trading sessions.
// Implementations manage virtual order books, track simulated positions,
// and compute real-time PnL against live market data.
type PaperTrader interface {
	// Start begins a new paper trading session with the given parameters.
	Start(ctx context.Context, req *PaperTradeRequest) (*PaperTradeResponse, error)
	// Status returns the current state of an active session.
	Status(ctx context.Context, sessionID string) (*PaperTradeResponse, error)
	// Stop terminates a running session and returns final results.
	Stop(ctx context.Context, sessionID string) (*PaperTradeResponse, error)
}

// DefaultPaperTrader is a skeleton implementation of PaperTrader.
// It validates inputs and returns placeholder responses. Replace this with
// a real implementation that connects to live market data and simulates fills.
type DefaultPaperTrader struct {
	// TODO: add fields for market data connector, virtual order book manager,
	// position tracker, and fill simulator once internal/quant exposes
	// stable interfaces for these components.
}

// NewDefaultPaperTrader creates a new skeleton paper trader.
//
// Returns:
//
//	trader - a paper trader instance (skeleton implementation).
func NewDefaultPaperTrader() *DefaultPaperTrader {
	return &DefaultPaperTrader{}
}

// Start begins a new paper trading session.
//
// This is a skeleton implementation that validates inputs and returns a
// placeholder response. A full implementation would:
//
//  1. Validate req.Symbols against available instruments.
//  2. Initialize virtual account with req.InitialCapital.
//  3. Connect to live market data feed for all symbols.
//  4. Start strategy signal generation loop (via errgroup).
//  5. Simulate fills at mid-price or configurable slippage.
//  6. Return session ID for subsequent Status/Stop calls.
//
// Args:
//
//	ctx - operation context supporting cancellation.
//	req - paper trade parameters including symbols, capital, and duration.
//
// Returns:
//
//	response - initial session state with session ID.
//	err - validation error or connection failure.
func (t *DefaultPaperTrader) Start(ctx context.Context, req *PaperTradeRequest) (*PaperTradeResponse, error) {
	if req == nil {
		return nil, errors.New("paper trade request is nil")
	}
	if len(req.Symbols) == 0 {
		return nil, errors.New("no symbols in paper trade request")
	}
	if req.InitialCapital <= 0 {
		return nil, errors.New("initial capital must be > 0")
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

// Status returns the current state of an active paper trading session.
//
// Args:
//
//	ctx - operation context.
//	sessionID - the session identifier returned by Start.
//
// Returns:
//
//	response - current PnL, equity, and trade log for the session.
//	err - session-not-found error or context error.
func (t *DefaultPaperTrader) Status(ctx context.Context, sessionID string) (*PaperTradeResponse, error) {
	if sessionID == "" {
		return nil, errors.New("session ID must not be empty")
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

// Stop terminates a running paper trading session and returns final results.
//
// Args:
//
//	ctx - operation context with timeout for graceful shutdown.
//	sessionID - the session identifier to stop.
//
// Returns:
//
//	response - final session state with total PnL and complete trade log.
//	err - session-not-found error or shutdown error.
func (t *DefaultPaperTrader) Stop(ctx context.Context, sessionID string) (*PaperTradeResponse, error) {
	if sessionID == "" {
		return nil, errors.New("session ID must not be empty")
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
