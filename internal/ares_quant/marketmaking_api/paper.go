package marketmakingapi

import (
	"context"
	"errors"
	"fmt"
	"math"
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
//
// It computes REAL mark-to-market PnL from trade/price signals: Start and
// ApplySignals consume PaperTradeSignal values, fill virtual trades at the
// signal price, track a volume-weighted average entry price per position,
// realize PnL on closes, and mark open positions to the last observed price.
// TODO(tech-debt): fills occur exactly at the signal price; model slippage
// and spread once a matching engine / order book is available.
type DefaultPaperTrader struct {
	// mu guards the sessions map and every field of every paperSession it
	// holds (Positions, Trades, RealizedPnL, etc.). All read/write access
	// to session state must occur while holding mu (read or write).
	mu       sync.RWMutex
	sessions map[string]*paperSession
	nextID   atomic.Int64
}

// paperPosition tracks the real cost basis and last market price for one
// symbol so equity can be mark-to-market without faking valuations.
type paperPosition struct {
	Qty           float64 // signed: positive long, negative short
	AvgEntryPrice float64 // volume-weighted average entry price
	LastPrice     float64 // last observed market price (0 if none yet)
}

// paperSession holds the mutable state of a single paper trading session.
// All fields are guarded by DefaultPaperTrader.mu.
type paperSession struct {
	InitialCapital float64
	Symbols        []string
	Positions      map[string]*paperPosition // symbol -> position
	Trades         []TradeRecord
	RealizedPnL    float64
	StartTime      time.Time
	nextTradeID    int64
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
// Start consumes req.Signals in order: each buy/sell fills a virtual trade
// at the signal price (updating the position, its average entry price, and
// realized PnL on closes); each mark updates the symbol's last price. The
// returned response reflects the resulting mark-to-market equity and PnL.
// Additional signals can be fed to the live session via ApplySignals.
//
// Args:
//
//	ctx - operation context supporting cancellation.
//	req - paper trade parameters including symbols, capital, and signals.
//
// Returns:
//
//	response - initial session state with session ID and real PnL.
//	err - validation error or signal processing error.
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
		InitialCapital: req.InitialCapital,
		Symbols:        req.Symbols,
		Positions:      make(map[string]*paperPosition),
		StartTime:      time.Now(),
	}
	// Consume signals before publishing the session so a mid-batch error
	// leaves no partially-applied session in the map (rule 3.6).
	for _, sig := range req.Signals {
		if err := session.applySignal(sig); err != nil {
			return nil, fmt.Errorf("paper session %s signal %s: %w", sessionID, sig.Symbol, err)
		}
	}
	t.sessions[sessionID] = session

	return t.snapshot(sessionID, session), nil
}

// ApplySignals feeds additional trade/price signals into an active session,
// updating positions, realized PnL, and last prices. It enables incremental
// mark-to-market for a long-running session beyond the initial Start batch.
//
// Args:
//
//	ctx - operation context.
//	sessionID - the session identifier returned by Start.
//	signals - trade/price signals to apply in order.
//
// Returns:
//
//	response - updated session state with real PnL.
//	err - session-not-found, context, or signal validation error.
func (t *DefaultPaperTrader) ApplySignals(
	ctx context.Context, sessionID string, signals []PaperTradeSignal,
) (*PaperTradeResponse, error) {
	if sessionID == "" {
		return nil, errors.New("session ID must not be empty")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	session, ok := t.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	for _, sig := range signals {
		if err := session.applySignal(sig); err != nil {
			return nil, fmt.Errorf("paper session %s signal %s: %w", sessionID, sig.Symbol, err)
		}
	}
	return t.snapshot(sessionID, session), nil
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

	// Snapshot all shared fields under the read lock so concurrent writers
	// cannot race the reads.
	t.mu.RLock()
	session, ok := t.sessions[sessionID]
	if !ok {
		t.mu.RUnlock()
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	resp := t.snapshot(sessionID, session)
	t.mu.RUnlock()

	return resp, nil
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

	// Remove the session under the write lock and snapshot its fields so
	// the response is built from consistent data.
	t.mu.Lock()
	session, ok := t.sessions[sessionID]
	if ok {
		delete(t.sessions, sessionID)
	}
	if !ok {
		t.mu.Unlock()
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	resp := t.snapshot(sessionID, session)
	t.mu.Unlock()

	return resp, nil
}

// snapshot builds a mark-to-market response from the session. The caller
// must hold DefaultPaperTrader.mu (read or write) for the duration of this
// call so the position/equity reads cannot race a writer.
func (t *DefaultPaperTrader) snapshot(sessionID string, session *paperSession) *PaperTradeResponse {
	trades := make([]TradeRecord, len(session.Trades))
	copy(trades, session.Trades)
	equity := session.equity()
	return &PaperTradeResponse{
		SessionID:  sessionID,
		CurrentPnL: equity - session.InitialCapital,
		Equity:     equity,
		Trades:     trades,
		StartedAt:  session.StartTime,
	}
}

// applySignal processes one paper trade signal against the session, updating
// positions, realized PnL, the last price, and the trade log. Must be called
// with DefaultPaperTrader.mu held by the caller.
func (s *paperSession) applySignal(sig PaperTradeSignal) error {
	if sig.Side != PaperSideBuy && sig.Side != PaperSideSell && sig.Side != PaperSideMark {
		return fmt.Errorf("invalid side %q", sig.Side)
	}
	if sig.Price <= 0 {
		return fmt.Errorf("price must be > 0, got %f", sig.Price)
	}
	// Validate quantity for buy/sell BEFORE any state mutation so a rejected
	// signal cannot partially corrupt the live session. ApplySignals runs against
	// an already-published session, so mutating LastPrice (or creating a position
	// entry) and then failing would leave the session in a partially-applied
	// state — violating the atomicity guarantee Start documents. Mark signals
	// ignore quantity, so they skip this check.
	if sig.Side != PaperSideMark && sig.Quantity <= 0 {
		return fmt.Errorf("quantity must be > 0, got %f", sig.Quantity)
	}
	pos := s.position(sig.Symbol)
	pos.LastPrice = sig.Price
	if sig.Side == PaperSideMark {
		return nil
	}
	delta := sig.Quantity
	if sig.Side == PaperSideSell {
		delta = -sig.Quantity
	}
	realized := fillTrade(pos, delta, sig.Price)
	s.RealizedPnL += realized
	s.nextTradeID++
	ts := sig.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	s.Trades = append(s.Trades, TradeRecord{
		ID:        fmt.Sprintf("%s-%d", sig.Symbol, s.nextTradeID),
		Symbol:    sig.Symbol,
		Side:      sig.Side,
		Price:     sig.Price,
		Quantity:  sig.Quantity,
		Timestamp: ts,
		PnL:       realized,
	})
	return nil
}

// position returns the position for sym, creating an empty one if absent.
// Must be called with DefaultPaperTrader.mu held.
func (s *paperSession) position(sym string) *paperPosition {
	pos, ok := s.Positions[sym]
	if !ok {
		pos = &paperPosition{}
		s.Positions[sym] = pos
	}
	return pos
}

// fillTrade applies a signed fill (delta>0 buy, delta<0 sell) at price to
// pos, updating pos.Qty and pos.AvgEntryPrice in place, and returns the
// realized PnL from any closing portion. Opening/increasing positions reweight
// the average entry; reducing positions realize (price - entry) per unit
// closed (sign-adjusted for shorts); flips close the old leg at entry and
// open the opposite leg at price.
func fillTrade(pos *paperPosition, delta, price float64) float64 {
	if pos.Qty == 0 {
		pos.Qty = delta
		pos.AvgEntryPrice = price
		return 0
	}
	if (pos.Qty > 0) == (delta > 0) {
		// Same direction: increase position, reweight average entry.
		totalCost := pos.Qty*pos.AvgEntryPrice + delta*price
		pos.Qty += delta
		pos.AvgEntryPrice = totalCost / pos.Qty
		return 0
	}
	// Opposite direction: close (and possibly flip).
	absQty := math.Abs(pos.Qty)
	absDelta := math.Abs(delta)
	closing := math.Min(absDelta, absQty)
	realized := (price - pos.AvgEntryPrice) * closing
	if pos.Qty < 0 {
		realized = -realized
	}
	pos.Qty += delta
	if absDelta > absQty {
		// Flipped to the opposite direction; new leg opens at fill price.
		pos.AvgEntryPrice = price
	}
	return realized
}

// equity returns the mark-to-market equity: initial capital + realized PnL +
// unrealized PnL across all open positions. A symbol with no last price yet
// contributes 0 unrealized PnL (valued at cost), which is honest, not faked.
// Must be called with DefaultPaperTrader.mu held.
func (s *paperSession) equity() float64 {
	unrealized := 0.0
	for _, pos := range s.Positions {
		if pos.Qty == 0 || pos.LastPrice <= 0 {
			continue
		}
		unrealized += pos.Qty * (pos.LastPrice - pos.AvgEntryPrice)
	}
	return s.InitialCapital + s.RealizedPnL + unrealized
}
