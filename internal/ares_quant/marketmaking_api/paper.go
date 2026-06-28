package marketmakingapi

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
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

// DefaultPaperTrader is an in-memory implementation of PaperTrader.
// It manages virtual sessions with simulated positions and PnL tracking.
// Replace this with a real implementation that connects to live market data
// and simulates fills once internal/quant exposes stable interfaces.
type DefaultPaperTrader struct {
	mu       sync.RWMutex
	sessions map[string]*paperSession
	nextID   atomic.Int64
}

// paperSession holds the mutable state of a single paper trading session.
type paperSession struct {
	Capital   float64
	Symbols   []string
	Positions map[string]float64 // symbol -> quantity (signed)
	Trades    []TradeRecord
	StartTime time.Time
}

// NewDefaultPaperTrader creates a new in-memory paper trader.
//
// Returns:
//
//	trader - a paper trader instance.
func NewDefaultPaperTrader() *DefaultPaperTrader {
	return &DefaultPaperTrader{
		sessions: make(map[string]*paperSession),
	}
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

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	sessionID := fmt.Sprintf("paper-%d", t.nextID.Add(1))
	session := &paperSession{
		Capital:   req.InitialCapital,
		Symbols:   req.Symbols,
		Positions: make(map[string]float64),
		StartTime: time.Now(),
	}
	t.sessions[sessionID] = session

	return &PaperTradeResponse{
		SessionID:  sessionID,
		CurrentPnL: 0,
		Equity:     req.InitialCapital,
		Trades:     []TradeRecord{},
		StartedAt:  session.StartTime,
	}, nil
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

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	t.mu.RLock()
	session, ok := t.sessions[sessionID]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	equity := session.Capital
	for sym, qty := range session.Positions {
		_ = sym
		equity += qty * 0 // placeholder: mark-to-market not wired
	}

	return &PaperTradeResponse{
		SessionID:  sessionID,
		CurrentPnL: equity - session.Capital,
		Equity:     equity,
		Trades:     session.Trades,
		StartedAt:  session.StartTime,
	}, nil
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

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	t.mu.Lock()
	session, ok := t.sessions[sessionID]
	if ok {
		delete(t.sessions, sessionID)
	}
	t.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	equity := session.Capital
	for sym, qty := range session.Positions {
		_ = sym
		equity += qty * 0 // placeholder: mark-to-market not wired
	}

	return &PaperTradeResponse{
		SessionID:  sessionID,
		CurrentPnL: equity - session.Capital,
		Equity:     equity,
		Trades:     session.Trades,
		StartedAt:  session.StartTime,
	}, nil
}
