package marketmakingapi

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewDefaultPaperTrader tests constructor.
func TestNewDefaultPaperTrader(t *testing.T) {
	trader := NewDefaultPaperTrader()
	require.NotNil(t, trader)
}

// TestPaperTrader_Start_NilRequest tests nil request handling.
func TestPaperTrader_Start_NilRequest(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Start(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestPaperTrader_Start_NoSymbols tests empty symbols.
func TestPaperTrader_Start_NoSymbols(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no symbols")
	require.Nil(t, resp)
}

// TestPaperTrader_Start_InvalidCapital tests zero capital.
func TestPaperTrader_Start_InvalidCapital(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 0,
		Duration:       time.Hour,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "capital")
	require.Nil(t, resp)
}

// TestPaperTrader_Start_ValidRequest tests that start creates a session and returns it.
func TestPaperTrader_Start_ValidRequest(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT, SymbolETHUSDT},
		InitialCapital: 100000.0,
		Duration:       2 * time.Hour,
	}
	resp, err := trader.Start(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.SessionID)
	require.Equal(t, 100000.0, resp.Equity)
}

// TestPaperTrader_Status_EmptySessionID tests status with empty session ID.
func TestPaperTrader_Status_EmptySessionID(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Status(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID")
	require.Nil(t, resp)
}

// TestPaperTrader_Status_ValidSessionID tests that status returns session data for started session.
func TestPaperTrader_Status_ValidSessionID(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, created.SessionID, resp.SessionID)
}

// TestPaperTrader_Stop_EmptySessionID tests stop with empty session ID.
func TestPaperTrader_Stop_EmptySessionID(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Stop(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID")
	require.Nil(t, resp)
}

// TestPaperTrader_Stop_ValidSessionID tests that stop returns session data and removes it.
func TestPaperTrader_Stop_ValidSessionID(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Stop(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, created.SessionID, resp.SessionID)

	// Verify session was removed
	_, err = trader.Status(context.Background(), created.SessionID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

// TestPaperTrader_CancelledContext tests context cancellation on Start.
func TestPaperTrader_CancelledContext(t *testing.T) {
	trader := NewDefaultPaperTrader()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := trader.Start(ctx, &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 10000.0,
		Duration:       time.Minute,
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, resp)
}

// TestPaperTrader_Status_NoSignals_PnLZero verifies that a session with no
// signals reports real PnL 0 and equity equal to capital (no positions).
func TestPaperTrader_Status_NoSignals_PnLZero(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 0.0, resp.CurrentPnL)
	require.Equal(t, 50000.0, resp.Equity)
	require.Empty(t, resp.Trades)
}

// TestPaperTrader_Stop_ReturnsRealPnL verifies Stop returns the real
// mark-to-market PnL computed from the session's signals.
func TestPaperTrader_Stop_ReturnsRealPnL(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 75000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 10},
			{Symbol: SymbolBTCUSDT, Side: PaperSideSell, Price: 120, Quantity: 10},
		},
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Stop(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	// Bought 10 @ 100, sold 10 @ 120 -> realized 200.
	require.InDelta(t, 200.0, resp.CurrentPnL, 1e-9)
	require.InDelta(t, 75200.0, resp.Equity, 1e-9)
	require.Len(t, resp.Trades, 2)
}

// TestPaperTrader_UnrealizedPnL verifies mark-to-market unrealized PnL:
// BUY 10 @ 100 then mark to 110 -> unrealized PnL = 10*(110-100) = 100.
func TestPaperTrader_UnrealizedPnL(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 10},
			{Symbol: SymbolBTCUSDT, Side: PaperSideMark, Price: 110},
		},
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.InDelta(t, 100.0, resp.CurrentPnL, 1e-9)
	require.InDelta(t, 50100.0, resp.Equity, 1e-9)
	// Only the buy filled a trade; mark does not record a trade.
	require.Len(t, resp.Trades, 1)
	require.InDelta(t, 0.0, resp.Trades[0].PnL, 1e-9)
}

// TestPaperTrader_RealizedPnL_FullClose verifies realized PnL on a full
// close: BUY 10 @ 100, SELL 10 @ 120 -> realized 200, position flat.
func TestPaperTrader_RealizedPnL_FullClose(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 10},
			{Symbol: SymbolBTCUSDT, Side: PaperSideSell, Price: 120, Quantity: 10},
		},
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	// Position is flat (closed), so all PnL is realized.
	require.InDelta(t, 200.0, resp.CurrentPnL, 1e-9)
	require.InDelta(t, 100200.0, resp.Equity, 1e-9)
	require.Len(t, resp.Trades, 2)
	// Opening trade has 0 PnL; closing trade carries the realized 200.
	require.InDelta(t, 0.0, resp.Trades[0].PnL, 1e-9)
	require.InDelta(t, 200.0, resp.Trades[1].PnL, 1e-9)
}

// TestPaperTrader_PartialClose_MixedPnL verifies mixed realized + unrealized
// PnL: BUY 10 @ 100, SELL 4 @ 110 (realized 40), mark 110 (unrealized 60).
func TestPaperTrader_PartialClose_MixedPnL(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 10},
			{Symbol: SymbolBTCUSDT, Side: PaperSideSell, Price: 110, Quantity: 4},
			{Symbol: SymbolBTCUSDT, Side: PaperSideMark, Price: 110},
		},
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	// Realized 4*(110-100)=40 plus unrealized 6*(110-100)=60 -> 100.
	require.InDelta(t, 40.0+60.0, resp.CurrentPnL, 1e-9)
	require.InDelta(t, 100100.0, resp.Equity, 1e-9)
	require.Len(t, resp.Trades, 2)
	require.InDelta(t, 40.0, resp.Trades[1].PnL, 1e-9)
}

// TestPaperTrader_AverageEntry_Reweighted verifies cost-basis reweighting:
// BUY 10 @ 100, BUY 10 @ 110 -> avg 105; mark 110 -> unrealized 20*5 = 100.
func TestPaperTrader_AverageEntry_Reweighted(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 10},
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 110, Quantity: 10},
			{Symbol: SymbolBTCUSDT, Side: PaperSideMark, Price: 110},
		},
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	// Avg entry (10*100 + 10*110)/20 = 105; unrealized 20*(110-105) = 100.
	require.InDelta(t, 100.0, resp.CurrentPnL, 1e-9)
}

// TestPaperTrader_Short_UnrealizedAndRealized verifies short-side PnL:
// SELL 10 @ 100 (short), mark 90 -> unrealized 100; BUY 10 @ 90 -> realized 100.
func TestPaperTrader_Short_UnrealizedAndRealized(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideSell, Price: 100, Quantity: 10},
			{Symbol: SymbolBTCUSDT, Side: PaperSideMark, Price: 90},
		},
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	// Open short: unrealized = -10*(90-100) = 100 (profit when price drops).
	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.InDelta(t, 100.0, resp.CurrentPnL, 1e-9)

	// Cover short at 90: realized = (90-100)*10*-1 sign-adjusted = 100, flat.
	resp, err = trader.ApplySignals(context.Background(), created.SessionID, []PaperTradeSignal{
		{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 90, Quantity: 10},
	})
	require.NoError(t, err)
	require.InDelta(t, 100.0, resp.CurrentPnL, 1e-9)
	require.Len(t, resp.Trades, 2)
}

// TestPaperTrader_ApplySignals_Incremental verifies feeding signals
// incrementally to a live session and observing PnL evolve.
func TestPaperTrader_ApplySignals_Incremental(t *testing.T) {
	trader := NewDefaultPaperTrader()
	created, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 100},
		},
	})
	require.NoError(t, err)

	// After open: last price == entry, so PnL is 0.
	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.InDelta(t, 0.0, resp.CurrentPnL, 1e-9)

	// Mark to 110: unrealized 100*(110-100) = 1000.
	resp, err = trader.ApplySignals(context.Background(), created.SessionID, []PaperTradeSignal{
		{Symbol: SymbolBTCUSDT, Side: PaperSideMark, Price: 110},
	})
	require.NoError(t, err)
	require.InDelta(t, 1000.0, resp.CurrentPnL, 1e-9)

	// Close at 120: realized 100*(120-100) = 2000, position flat.
	resp, err = trader.ApplySignals(context.Background(), created.SessionID, []PaperTradeSignal{
		{Symbol: SymbolBTCUSDT, Side: PaperSideSell, Price: 120, Quantity: 100},
	})
	require.NoError(t, err)
	require.InDelta(t, 2000.0, resp.CurrentPnL, 1e-9)
	require.Len(t, resp.Trades, 2)
}

// TestPaperTrader_PositionFlip verifies a flip from long to short realizes
// the long leg and opens a new short at the fill price.
func TestPaperTrader_PositionFlip(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 10},
			{Symbol: SymbolBTCUSDT, Side: PaperSideSell, Price: 110, Quantity: 15},
			{Symbol: SymbolBTCUSDT, Side: PaperSideMark, Price: 110},
		},
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	// Long 10 closed at 110 -> realized 10*(110-100)=100.
	// New short 5 @ 110, last 110 -> unrealized 0. Total 100.
	require.InDelta(t, 100.0, resp.CurrentPnL, 1e-9)
}

// TestPaperTrader_ApplySignals_InvalidSide verifies validation errors.
func TestPaperTrader_ApplySignals_InvalidSide(t *testing.T) {
	trader := NewDefaultPaperTrader()
	created, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	})
	require.NoError(t, err)

	resp, err := trader.ApplySignals(context.Background(), created.SessionID, []PaperTradeSignal{
		{Symbol: SymbolBTCUSDT, Side: "hold", Price: 100, Quantity: 1},
	})
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestPaperTrader_ApplySignals_InvalidPrice verifies a non-positive price is rejected.
func TestPaperTrader_ApplySignals_InvalidPrice(t *testing.T) {
	trader := NewDefaultPaperTrader()
	created, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	})
	require.NoError(t, err)

	resp, err := trader.ApplySignals(context.Background(), created.SessionID, []PaperTradeSignal{
		{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 0, Quantity: 1},
	})
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestPaperTrader_ApplySignals_InvalidQuantity_NoStateMutation verifies the
// atomicity guarantee: a buy/sell signal with a valid side+price but invalid
// (<=0) quantity must be rejected WITHOUT mutating the live session. Previously
// applySignal set pos.LastPrice before the quantity check, so the rejected
// signal corrupted LastPrice and inflated/deflated subsequent unrealized PnL.
func TestPaperTrader_ApplySignals_InvalidQuantity_NoStateMutation(t *testing.T) {
	trader := NewDefaultPaperTrader()
	created, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 10000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 10},
		},
	})
	require.NoError(t, err)
	// Baseline: long 10 @ 100, last price 100 -> PnL 0, equity 10000.
	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.InDelta(t, 0.0, resp.CurrentPnL, 1e-9)
	require.InDelta(t, 10000.0, resp.Equity, 1e-9)

	// Rejected buy with a different price must NOT leak into LastPrice.
	resp, err = trader.ApplySignals(context.Background(), created.SessionID, []PaperTradeSignal{
		{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 200, Quantity: 0},
	})
	require.Error(t, err)
	require.Nil(t, resp)

	// PnL/equity must be unchanged: the rejected signal must not have moved
	// LastPrice to 200 (which would inflate unrealized PnL by 10*(200-100)=1000).
	resp, err = trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.InDelta(t, 0.0, resp.CurrentPnL, 1e-9, "rejected signal must not alter PnL")
	require.InDelta(t, 10000.0, resp.Equity, 1e-9, "rejected signal must not alter equity")
	require.Len(t, resp.Trades, 1, "rejected signal must not append a trade")
}

// TestPaperTrader_ApplySignals_SessionNotFound verifies the not-found error.
func TestPaperTrader_ApplySignals_SessionNotFound(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.ApplySignals(context.Background(), "nope", []PaperTradeSignal{
		{Symbol: SymbolBTCUSDT, Side: PaperSideMark, Price: 100},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
	require.Nil(t, resp)
}

// TestPaperTrader_Start_InvalidSignal_NotStored verifies that a Start whose
// signal batch fails returns an error and leaves no session behind.
func TestPaperTrader_Start_InvalidSignal_NotStored(t *testing.T) {
	trader := NewDefaultPaperTrader()
	// Second signal is invalid (zero quantity on a buy); Start must fail.
	resp, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 1},
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 0},
		},
	})
	require.Error(t, err)
	require.Nil(t, resp)
	// No session should be queryable: the only IDs are internal, so a fresh
	// valid Start must still succeed and be independent of the failed one.
	created, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.SessionID)
}

// TestPaperTrader_StatusStop_ConcurrentNoRace exercises concurrent Status
// (read) and ApplySignals (write) calls to verify lock-protected snapshots
// don't race. Run with -race to detect data races.
func TestPaperTrader_StatusStop_ConcurrentNoRace(t *testing.T) {
	trader := NewDefaultPaperTrader()
	created, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{SymbolBTCUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
		Signals: []PaperTradeSignal{
			{Symbol: SymbolBTCUSDT, Side: PaperSideBuy, Price: 100, Quantity: 10},
		},
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = trader.Status(context.Background(), created.SessionID)
		}()
		go func(n int) {
			defer wg.Done()
			price := 100.0 + float64(n%10)
			_, _ = trader.ApplySignals(context.Background(), created.SessionID, []PaperTradeSignal{
				{Symbol: SymbolBTCUSDT, Side: PaperSideMark, Price: price},
			})
		}(i)
	}
	wg.Wait()

	// Final stop returns a real, race-free snapshot.
	resp, err := trader.Stop(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.NotNil(t, resp)
}
