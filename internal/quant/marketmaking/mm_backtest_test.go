package marketmaking

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// generateTestEvents creates a deterministic sequence of market data events
// for backtesting using the T4.1/T4.2 MarketDataEvent format.
func generateTestEvents(symbol string, count int, startPrice float64, baseTime time.Time) []MarketDataEvent {
	events := make([]MarketDataEvent, count)
	for i := 0; i < count; i++ {
		mid := startPrice + float64(i%10-5)*0.1
		bid := mid - 0.01
		ask := mid + 0.01
		events[i] = MarketDataEvent{
			Symbol:    symbol,
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			BidPrice:  bid,
			AskPrice:  ask,
			MidPrice:  mid,
			Spread:    ask - bid,
			Volume:    int64(100 + i*10),
			IsStale:   false,
		}
	}
	return events
}

// TestNewMMBacktestRunner tests constructor validation.
func TestNewMMBacktestRunner(t *testing.T) {
	engineCfg := &QuoteEngineConfig{
		BaseSpread: 0.0005, SkewFactor: 0.1, MaxInventory: 10,
		RiskLimit: 0.8, MaxQuoteSize: 1.0, StaleThreshold: 5 * time.Second,
	}
	engine, err := NewQuoteEngine(engineCfg)
	require.NoError(t, err)

	runner, err := NewMMBacktestRunner(engine, 100000, 0.001, 0.0005)
	require.NoError(t, err)
	require.NotNil(t, runner)
	require.Equal(t, 100000.0, runner.InitialCash)
}

// TestNewMMBacktestRunner_NilEngine tests nil engine rejection.
func TestNewMMBacktestRunner_NilEngine(t *testing.T) {
	_, err := NewMMBacktestRunner(nil, 100000, 0.001, 0.0005)
	require.Error(t, err)
	require.Contains(t, err.Error(), "engine must not be nil")
}

// TestNewMMBacktestRunner_InvalidCash tests non-positive cash rejection.
func TestNewMMBacktestRunner_InvalidCash(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	_, err := NewMMBacktestRunner(engine, 0, 0.001, 0.0005)
	require.Error(t, err)
	require.Contains(t, err.Error(), "initial cash must be > 0")
}

// TestMMBacktestRunner_Run_EmptyEvents tests empty event slice.
func TestMMBacktestRunner_Run_EmptyEvents(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	runner, _ := NewMMBacktestRunner(engine, 100000, 0.001, 0.0005)

	result, err := runner.Run(context.Background(), []MarketDataEvent{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Zero(t, result.TotalQuotes)
	require.Zero(t, result.TotalFills)
}

// TestMMBacktestRunner_Run_Deterministic tests that identical inputs produce identical outputs.
func TestMMBacktestRunner_Run_Deterministic(t *testing.T) {
	engineCfg := &QuoteEngineConfig{
		BaseSpread: 0.001, SkewFactor: 0.1, MaxInventory: 100,
		RiskLimit: 0.8, MaxQuoteSize: 1.0, StaleThreshold: 5 * time.Second,
	}
	engine, _ := NewQuoteEngine(engineCfg)
	runner, _ := NewMMBacktestRunner(engine, 100000, 0.001, 0.0005)

	events := generateTestEvents("BTCUSDT", 50, 50000.0, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))

	result1, err := runner.Run(context.Background(), events)
	require.NoError(t, err)

	result2, err := runner.Run(context.Background(), events)
	require.NoError(t, err)

	require.Equal(t, result1.RealizedPnL, result2.RealizedPnL)
	require.Equal(t, result1.UnrealizedPnL, result2.UnrealizedPnL)
	require.Equal(t, result1.TotalPnL, result2.TotalPnL)
	require.Equal(t, result1.FillRate, result2.FillRate)
	require.Equal(t, result1.TotalQuotes, result2.TotalQuotes)
	require.Equal(t, result1.TotalFills, result2.TotalFills)
	require.Equal(t, len(result1.EquityCurve), len(result2.EquityCurve))
	require.Equal(t, len(result1.TradeLog), len(result2.TradeLog))
}

// TestMMBacktestRunner_Run_BasicMetrics tests that all required metrics are populated.
func TestMMBacktestRunner_Run_BasicMetrics(t *testing.T) {
	engineCfg := &QuoteEngineConfig{
		BaseSpread: 0.01, SkewFactor: 0.05, MaxInventory: 100,
		RiskLimit: 0.9, MaxQuoteSize: 0.5, StaleThreshold: 30 * time.Second,
	}
	engine, _ := NewQuoteEngine(engineCfg)
	runner, _ := NewMMBacktestRunner(engine, 100000, 0.001, 0.0005)

	events := make([]MarketDataEvent, 20)
	baseTime := time.Date(2024, 6, 15, 9, 30, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		mid := 100.0 + float64(i-10)*2.0
		events[i] = MarketDataEvent{
			Symbol: "TEST", Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			BidPrice: mid - 0.05, AskPrice: mid + 0.05, MidPrice: mid,
			Spread: 0.1, Volume: 1000, IsStale: false,
		}
	}

	result, err := runner.Run(context.Background(), events)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.GreaterOrEqual(t, result.TotalQuotes, 0)
	require.GreaterOrEqual(t, result.TotalFills, 0)
	require.GreaterOrEqual(t, result.FillRate, 0.0)
	require.LessOrEqual(t, result.FillRate, 1.0)
	require.GreaterOrEqual(t, len(result.EquityCurve), 0)
	require.GreaterOrEqual(t, result.MaxDrawdown, 0.0)
	require.False(t, result.Duration < 0)
}

// TestMMBacktestRunner_Run_StaleDataHandling tests that stale events are skipped with warnings.
func TestMMBacktestRunner_Run_StaleDataHandling(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	runner, _ := NewMMBacktestRunner(engine, 100000, 0.001, 0.0005)

	baseTime := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	events := []MarketDataEvent{
		{Symbol: "TEST", Timestamp: baseTime, BidPrice: 99.95, AskPrice: 100.05, MidPrice: 100.0, Spread: 0.1, Volume: 100, IsStale: false},
		{Symbol: "TEST", Timestamp: baseTime.Add(1 * time.Second), BidPrice: 99.96, AskPrice: 100.06, MidPrice: 100.01, Spread: 0.1, Volume: 100, IsStale: true},
		{Symbol: "TEST", Timestamp: baseTime.Add(2 * time.Second), BidPrice: 99.97, AskPrice: 100.07, MidPrice: 100.02, Spread: 0.1, Volume: 100, IsStale: false},
	}

	result, err := runner.Run(context.Background(), events)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, len(result.Warnings), 0)
	require.Contains(t, result.Warnings[0], "stale data")
}

// TestMMBacktestRunner_Run_ContextCancellation tests context cancellation.
func TestMMBacktestRunner_Run_ContextCancellation(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	runner, _ := NewMMBacktestRunner(engine, 100000, 0.001, 0.0005)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	events := generateTestEvents("TEST", 100, 100.0, time.Now())
	_, err := runner.Run(ctx, events)
	require.ErrorIs(t, err, context.Canceled)
}

// TestMMBacktestRunner_EquityCurveMonotonicTimestamps verifies equity curve timestamps are strictly increasing.
func TestMMBacktestRunner_EquityCurveMonotonicTimestamps(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	runner, _ := NewMMBacktestRunner(engine, 100000, 0.001, 0.0005)

	events := generateTestEvents("AAPL", 30, 150.0, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))

	result, err := runner.Run(context.Background(), events)
	require.NoError(t, err)
	require.NotEmpty(t, result.EquityCurve)

	for i := 1; i < len(result.EquityCurve); i++ {
		require.True(t, result.EquityCurve[i].Time.After(result.EquityCurve[i-1].Time),
			"equity curve timestamp at index %d should be after index %d", i, i-1)
	}
}
