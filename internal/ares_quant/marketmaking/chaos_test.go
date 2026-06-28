package marketmaking

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewChaosExecutor tests constructor.
func TestNewChaosExecutor(t *testing.T) {
	actions := []ChaosAction{{Name: "test-stale", Type: "market_data_stale"}}
	executor := NewChaosExecutor(actions)
	require.NotNil(t, executor)
	require.Len(t, executor.actions, 1)
}

// TestChaosExecutor_Execute_BasicRun tests basic chaos execution with no actions.
func TestChaosExecutor_Execute_BasicRun(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)
	executor := NewChaosExecutor(nil)

	report, err := executor.Execute(context.Background(), engine, inv, []MarketDataEvent{})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.NotEmpty(t, report.RunID)
	require.Equal(t, 100.0, report.Score.Overall)
}

// TestChaosExecutor_Execute_MarketDataStale tests stale data chaos action.
func TestChaosExecutor_Execute_MarketDataStale(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)

	events := make([]MarketDataEvent, 15)
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range events {
		events[i] = MarketDataEvent{
			Symbol: "TEST", Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			BidPrice: 99.9, AskPrice: 100.1, MidPrice: 100.0, Spread: 0.2, Volume: 100, IsStale: false,
		}
	}

	actions := []ChaosAction{{Name: "stale-test", Type: "market_data_stale", TriggerTime: baseTime}}
	executor := NewChaosExecutor(actions)
	report, err := executor.Execute(context.Background(), engine, inv, events)
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Greater(t, report.Score.Overall, 0.0)
	require.NotEmpty(t, report.EventsLog)
}

// TestChaosExecutor_Execute_InventoryBreach tests inventory limit breach action.
func TestChaosExecutor_Execute_InventoryBreach(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)

	actions := []ChaosAction{{Name: "breach-test", Type: "inventory_limit_breach", Config: map[string]interface{}{"breach_quantity": 100.0}}}
	executor := NewChaosExecutor(actions)
	report, err := executor.Execute(context.Background(), engine, inv, []MarketDataEvent{})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.LessOrEqual(t, report.Score.Overall, 100.0)
}

// TestChaosExecutor_Execute_RejectSpike tests order reject spike action.
func TestChaosExecutor_Execute_RejectSpike(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)

	actions := []ChaosAction{{Name: "reject-spike", Type: "order_reject_spike", Config: map[string]interface{}{"reject_rate": 0.9, "num_quotes": 200}}}
	executor := NewChaosExecutor(actions)
	report, err := executor.Execute(context.Background(), engine, inv, []MarketDataEvent{})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.LessOrEqual(t, report.Score.Overall, 100.0)
}

// TestChaosExecutor_Execute_PartialFillStorm tests partial fill storm action.
func TestChaosExecutor_Execute_PartialFillStorm(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)

	actions := []ChaosAction{{Name: "fill-storm", Type: "partial_fill_storm", Config: map[string]interface{}{"fill_count": 20}}}
	executor := NewChaosExecutor(actions)
	report, err := executor.Execute(context.Background(), engine, inv, []MarketDataEvent{})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.NotEmpty(t, report.EventsLog)
}

// TestChaosExecutor_Execute_LatencySpike tests latency spike action.
func TestChaosExecutor_Execute_LatencySpike(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)

	events := make([]MarketDataEvent, 5)
	baseTime := time.Now()
	for i := range events {
		events[i] = MarketDataEvent{
			Symbol: "LAT", Timestamp: baseTime.Add(time.Duration(i) * time.Millisecond),
			BidPrice: 99.9, AskPrice: 100.1, MidPrice: 100.0, Spread: 0.2, Volume: 100, IsStale: false,
		}
	}

	actions := []ChaosAction{{Name: "latency-spike", Type: "latency_spike", Config: map[string]interface{}{"delay_ms": 1}}}
	executor := NewChaosExecutor(actions)
	report, err := executor.Execute(context.Background(), engine, inv, events)
	require.NoError(t, err)
	require.NotNil(t, report)
}

// TestChaosExecutor_Execute_ExchangeDisconnect tests exchange disconnect action.
func TestChaosExecutor_Execute_ExchangeDisconnect(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)

	actions := []ChaosAction{{Name: "disconnect-test", Type: "exchange_disconnect", Config: map[string]interface{}{"duration_ms": 1}}}
	executor := NewChaosExecutor(actions)
	report, err := executor.Execute(context.Background(), engine, inv, []MarketDataEvent{})
	require.NoError(t, err)
	require.NotNil(t, report)
}

// TestChaosExecutor_Execute_ContextCancellation tests context cancellation.
func TestChaosExecutor_Execute_ContextCancellation(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 10, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)

	actions := []ChaosAction{{Name: "slow-action", Type: "latency_spike", Config: map[string]interface{}{"delay_ms": 5000}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	executor := NewChaosExecutor(actions)
	_, err := executor.Execute(ctx, engine, inv, []MarketDataEvent{})
	require.ErrorIs(t, err, context.Canceled)
}

// TestChaosReport_ToJSON tests JSON serialization of the report.
func TestChaosReport_ToJSON(t *testing.T) {
	report := &ChaosReport{
		RunID:   "test-run-001",
		Actions: []ChaosAction{{Name: "a1", Type: "market_data_stale"}},
		Score: &SurvivalScore{
			Overall: 85.5, StoppedDangerousQuotes: true,
			MaintainedCapitalConstraint: true, CompleteAuditLog: false,
			RecoveredControlledState: true,
		},
		EventsLog:   []string{"event1", "event2"},
		GeneratedAt: time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
	}

	data, err := report.ToJSON()
	require.NoError(t, err)
	require.Contains(t, string(data), `"overall": 85.5`)
	require.Contains(t, string(data), `"run_id": "test-run-001"`)

	var parsed ChaosReport
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	require.Equal(t, report.RunID, parsed.RunID)
}

// TestSurvivalScore_Range verifies score is always within [0, 100].
func TestSurvivalScore_Range(t *testing.T) {
	engine, _ := NewQuoteEngine(&QuoteEngineConfig{
		BaseSpread: 0.001, MaxInventory: 1, RiskLimit: 0.8,
		MaxQuoteSize: 1, StaleThreshold: 5 * time.Second,
	})
	inv := NewInventory(100000)

	actions := []ChaosAction{
		{Name: "b1", Type: "inventory_limit_breach", Config: map[string]interface{}{"breach_quantity": 999}},
		{Name: "b2", Type: "inventory_limit_breach", Config: map[string]interface{}{"breach_quantity": 999}},
		{Name: "r1", Type: "order_reject_spike", Config: map[string]interface{}{"reject_rate": 1.0, "num_quotes": 1000}},
	}

	executor := NewChaosExecutor(actions)
	report, err := executor.Execute(context.Background(), engine, inv, []MarketDataEvent{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, report.Score.Overall, 0.0)
	require.LessOrEqual(t, report.Score.Overall, 100.0)
}

// TestComputeScore_EdgeCases tests score calculation edge cases.
func TestComputeScore_EdgeCases(t *testing.T) {
	executor := NewChaosExecutor(nil)

	perfect := executor.computeScore(ScoreInputs{
		StoppedDangerous: true, MaintainedCapital: true,
		AuditLogComplete: true, RecoveredControlled: true,
	})
	require.Equal(t, 100.0, perfect)

	worst := executor.computeScore(ScoreInputs{
		StoppedDangerous: false, MaintainedCapital: false,
		AuditLogComplete: false, RecoveredControlled: false,
		TotalQuotes: 100, RejectedOrders: 100,
	})
	require.GreaterOrEqual(t, worst, 0.0)
	require.LessOrEqual(t, worst, 100.0)
}
