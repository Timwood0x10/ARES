package marketmaking

import (
	"context"
	"errors"
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
				Symbol:        "BTCUSDT",
				Quantity:      5.0,
				AvgEntryPrice: 50000.0,
			},
		},
	}, nil
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
	quote, err := client.Quote(ctx, "BTCUSDT")
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
	quote, err := client.Quote(ctx, "BTCUSDT")
	require.NoError(t, err)
	require.NotNil(t, quote)
	require.Equal(t, "BTCUSDT", quote.Symbol)
	require.Equal(t, 50000.0, quote.BidPrice)
}

// TestClient_Quote_EngineError tests quote engine error propagation.
func TestClient_Quote_EngineError(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	client.SetQuoteEngine(&mockQuoteEngine{err: errors.New("data feed down")})

	ctx := context.Background()
	quote, err := client.Quote(ctx, "BTCUSDT")
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
	require.Equal(t, "BTCUSDT", report.Positions[0].Symbol)
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

// TestClient_Backtest_ValidRequest tests backtest returns ErrNotImplemented (skeleton).
func TestClient_Backtest_ValidRequest(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	req := &BacktestRequest{
		Symbols:        []string{"ETHUSDT"},
		StartTime:      time.Now().Add(-24 * time.Hour),
		EndTime:        time.Now(),
		InitialCapital: 50000.0,
	}
	resp, err := client.Backtest(context.Background(), req)
	// FIX: Skeleton implementation returns ErrNotImplemented.
	require.ErrorIs(t, err, ErrNotImplemented)
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

// TestClient_PaperTrade_ValidRequest tests paper trade returns ErrNotImplemented (skeleton).
func TestClient_PaperTrade_ValidRequest(t *testing.T) {
	cfg := DefaultConfig()
	client, err := NewClient(cfg)
	require.NoError(t, err)

	req := &PaperTradeRequest{
		Symbols:        []string{"ETHUSDT"},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	}
	resp, err := client.PaperTrade(context.Background(), req)
	// FIX: Skeleton implementation returns ErrNotImplemented.
	require.ErrorIs(t, err, ErrNotImplemented)
	require.Nil(t, resp)
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
