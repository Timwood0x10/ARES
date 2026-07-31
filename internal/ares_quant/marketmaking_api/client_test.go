package marketmakingapi

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockQuoteEngine is a test double for QuoteEngine.
type mockQuoteEngine struct {
	err error
}

func (m *mockQuoteEngine) GenerateQuote(_ context.Context, symbol string) (*QuoteDecision, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &QuoteDecision{
		Symbol:    symbol,
		BidPrice:  50000.0,
		AskPrice:  50010.0,
		BidSize:   1.0,
		AskSize:   1.0,
		TTLMillis: 5000,
		RiskState: "normal",
	}, nil
}

// mockRiskManager is a test double for RiskManager.
type mockRiskManager struct{}

func (m *mockRiskManager) CheckPreTrade(_ context.Context, _ string, _ string, _ float64) error {
	return nil
}

func (m *mockRiskManager) GetReport(_ context.Context) (*RiskReport, error) {
	return &RiskReport{
		Timestamp:     time.Now().UTC(),
		TotalExposure: 50000.0,
		Utilization:   0.5,
		Health:        "healthy",
	}, nil
}

// mockInventoryManager is a test double for InventoryManager.
type mockInventoryManager struct{}

func (m *mockInventoryManager) GetPositions(_ context.Context) (*InventoryReport, error) {
	return &InventoryReport{
		Timestamp:   time.Now().UTC(),
		NetDelta:    5.0,
		CashBalance: 50000.0,
		Positions: []Position{
			{
				Symbol:        SymbolBTCUSDT,
				Quantity:      5.0,
				AvgEntryPrice: 50000.0,
			},
		},
	}, nil
}

// mockDataFeed is a test double for DataFeed that counts Close calls
// to verify cleanup runs exactly once.
type mockDataFeed struct {
	closeCount atomic.Int32
}

func (m *mockDataFeed) Connect(_ context.Context, _ []string) error { return nil }
func (m *mockDataFeed) Close() error {
	m.closeCount.Add(1)
	return nil
}

// mockBacktestRunner is a test double for BacktestRunner that captures
// the request it receives for inspection.
type mockBacktestRunner struct {
	receivedReq *BacktestRequest
}

func (m *mockBacktestRunner) Run(_ context.Context, req *BacktestRequest) (*BacktestResponse, error) {
	m.receivedReq = req
	return &BacktestResponse{Request: req}, nil
}

// TestNewClient_ValidConfig tests successful client creation.
func TestNewClient_ValidConfig(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
}

// TestNewClient_NilConfig tests that nil config returns an error.
func TestNewClient_NilConfig(t *testing.T) {
	client, err := NewClient(nil)
	require.Error(t, err)
	require.Nil(t, client)
}

// TestNewClient_InvalidConfig tests validation failure propagation.
func TestNewClient_InvalidConfig(t *testing.T) {
	cfg := &MarketMakingConfig{
		Symbols: []string{},
		Mode:    ModePaper,
		RiskLimits: RiskLimitConfig{
			MaxPosition:  10.0,
			MaxOrderSize: 1.0,
		},
	}
	client, err := NewClient(cfg)
	require.Error(t, err)
	require.Nil(t, client)
}

// TestClient_Quote_NoEngine tests quote without engine returns error.
func TestClient_Quote_NoEngine(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	quote, err := client.Quote(ctx, SymbolBTCUSDT)
	require.ErrorIs(t, err, ErrNotInitialized)
	require.Nil(t, quote)
}

// TestClient_Quote_WithEngine tests quote with injected engine.
func TestClient_Quote_WithEngine(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	client.SetQuoteEngine(&mockQuoteEngine{})

	ctx := context.Background()
	quote, err := client.Quote(ctx, SymbolBTCUSDT)
	require.NoError(t, err)
	require.NotNil(t, quote)
	require.Equal(t, SymbolBTCUSDT, quote.Symbol)
	require.Equal(t, 50000.0, quote.BidPrice)
}

// TestClient_Quote_EngineError tests quote engine error propagation.
func TestClient_Quote_EngineError(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	client.SetQuoteEngine(&mockQuoteEngine{err: errors.New("data feed down")})

	ctx := context.Background()
	quote, err := client.Quote(ctx, SymbolBTCUSDT)
	require.Error(t, err)
	require.Contains(t, err.Error(), "data feed down")
	require.Nil(t, quote)
}

// TestClient_GetRisk_NoManager tests get risk without manager.
func TestClient_GetRisk_NoManager(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	report, err := client.GetRisk(ctx)
	require.ErrorIs(t, err, ErrNotInitialized)
	require.Nil(t, report)
}

// TestClient_GetRisk_WithManager tests get risk with injected manager.
func TestClient_GetRisk_WithManager(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	client.SetRiskManager(&mockRiskManager{})

	ctx := context.Background()
	report, err := client.GetRisk(ctx)
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, "healthy", report.Health)
	require.Equal(t, 0.5, report.Utilization)
}

// TestClient_GetInventory_WithManager tests inventory with injected manager.
func TestClient_GetInventory_WithManager(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	client.SetInventoryManager(&mockInventoryManager{})

	ctx := context.Background()
	report, err := client.GetInventory(ctx)
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Len(t, report.Positions, 1)
	require.Equal(t, SymbolBTCUSDT, report.Positions[0].Symbol)
}

// TestClient_StartStop tests start and stop lifecycle.
func TestClient_StartStop(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	ctx := context.Background()

	err = client.Start(ctx)
	require.NoError(t, err)

	err = client.Start(ctx)
	require.Error(t, err) // already started

	err = client.Stop(ctx)
	require.NoError(t, err)

	err = client.Stop(ctx)
	require.NoError(t, err) // idempotent
}

// TestClient_Backtest_NilRequest tests backtest with nil request.
func TestClient_Backtest_NilRequest(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	resp, err := client.Backtest(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestClient_Backtest_ValidRequest tests backtest returns ErrNotInitialized
// when no runner is injected.
func TestClient_Backtest_ValidRequest(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	req := &BacktestRequest{
		Symbols:        []string{SymbolETHUSDT},
		StartTime:      time.Now().Add(-24 * time.Hour),
		EndTime:        time.Now(),
		InitialCapital: 50000.0,
	}
	resp, err := client.Backtest(context.Background(), req)
	// Backtest without an injected runner returns ErrNotInitialized.
	require.ErrorIs(t, err, ErrNotInitialized)
	require.Nil(t, resp)
}

// TestClient_PaperTrade_NilRequest tests paper trade with nil request.
func TestClient_PaperTrade_NilRequest(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	resp, err := client.PaperTrade(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestClient_PaperTrade_NoTrader tests paper trade returns ErrNotInitialized.
func TestClient_PaperTrade_NoTrader(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	req := &PaperTradeRequest{
		Symbols:        []string{SymbolETHUSDT},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	}
	resp, err := client.PaperTrade(context.Background(), req)
	require.ErrorIs(t, err, ErrNotInitialized)
	require.Nil(t, resp)
}

// TestClient_PaperTrade_WithTrader tests paper trade delegates to PaperTrader.
func TestClient_PaperTrade_WithTrader(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	trader := NewDefaultPaperTrader()
	client.SetPaperTrader(trader)

	req := &PaperTradeRequest{
		Symbols:        []string{"BTCUSDT"},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
	}
	resp, err := client.PaperTrade(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.SessionID)
	require.Equal(t, 100000.0, resp.Equity)
}

// TestClient_Close_Unstarted tests close on unstarted client.
func TestClient_Close_Unstarted(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	err = client.Close()
	require.NoError(t, err)
}

// TestClient_Close_AfterStart tests close after start calls stop internally.
func TestClient_Close_AfterStart(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	err = client.Start(ctx)
	require.NoError(t, err)

	err = client.Close()
	require.NoError(t, err)
}

// TestClient_Backtest_DoesNotMutateRequest verifies that Backtest does not
// mutate the caller's request when falling back to config symbols.
func TestClient_Backtest_DoesNotMutateRequest(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	runner := &mockBacktestRunner{}
	client.SetBacktestRunner(runner)

	req := &BacktestRequest{
		Symbols:        []string{},
		InitialCapital: 50000.0,
		StartTime:      time.Now().Add(-24 * time.Hour),
		EndTime:        time.Now(),
	}
	resp, err := client.Backtest(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Caller's request must not be mutated.
	require.Empty(t, req.Symbols)
	// Runner received the effective symbols copied from config.
	require.Equal(t, cfg.Symbols, runner.receivedReq.Symbols)
	// Runner received a different request object, not the caller's.
	require.NotSame(t, req, runner.receivedReq)
}

// TestClient_PaperTrade_DoesNotMutateRequest verifies that PaperTrade does
// not mutate the caller's request when falling back to config symbols.
func TestClient_PaperTrade_DoesNotMutateRequest(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	trader := NewDefaultPaperTrader()
	client.SetPaperTrader(trader)

	req := &PaperTradeRequest{
		Symbols:        []string{},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
	}
	resp, err := client.PaperTrade(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Caller's request must not be mutated.
	require.Empty(t, req.Symbols)
}

// TestClient_Close_ConcurrentNoDoubleCleanup verifies that concurrent
// Close and Stop calls do not cause double cleanup (data feed closed
// more than once). stopOnce must guarantee single execution.
func TestClient_Close_ConcurrentNoDoubleCleanup(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	feed := &mockDataFeed{}
	client.SetDataFeed(feed)

	err = client.Start(context.Background())
	require.NoError(t, err)

	// Call Close and Stop concurrently — stopOnce must prevent double cleanup.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = client.Close()
		}()
		go func() {
			defer wg.Done()
			_ = client.Stop(context.Background())
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), feed.closeCount.Load(),
		"data feed must be closed exactly once")
}

// TestClient_Stop_IdempotentAfterClose verifies that Stop after Close
// is a no-op and does not panic or double-clean.
func TestClient_Stop_IdempotentAfterClose(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	feed := &mockDataFeed{}
	client.SetDataFeed(feed)

	err = client.Start(context.Background())
	require.NoError(t, err)

	require.NoError(t, client.Close())
	require.NoError(t, client.Stop(context.Background()))
	require.NoError(t, client.Close())

	require.Equal(t, int32(1), feed.closeCount.Load(),
		"data feed must be closed exactly once")
}
