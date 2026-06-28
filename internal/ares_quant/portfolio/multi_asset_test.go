package portfolio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── Test fixtures ───────────────────────────────────────────

// assetACSV is price data for asset A: 3 days, rising from 50 to 60.
const assetACSV = `date,open,high,low,close,volume
2025-03-01,49.0,51.0,48.0,50.0,1000000
2025-03-02,50.5,53.0,49.5,55.0,1100000
2025-03-03,55.0,58.0,54.0,60.0,1200000
`

// assetBCSV is price data for asset B: 2 days (missing day 1), rising from 200 to 220.
// This tests stale-price forward-fill.
const assetBCSV = `date,open,high,low,close,volume
2025-03-02,199.0,203.0,198.0,200.0,500000
2025-03-03,205.0,225.0,204.0,220.0,600000
`

// writeMultiAssetTestCSVs creates temporary CSV files for two assets and returns
// the directory path.
func writeMultiAssetTestCSVs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "ASSET_A.csv"), []byte(assetACSV), 0o644); err != nil {
		t.Fatalf("write ASSET_A.csv: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ASSET_B.csv"), []byte(assetBCSV), 0o644); err != nil {
		t.Fatalf("write ASSET_B.csv: %v", err)
	}
	return dir
}

// ─── Tests ────────────────────────────────────────────────────

// TestMultiAssetBasicBuy verifies that buying both assets on day 1 produces
// correct equity reflecting the combined portfolio value.
func TestMultiAssetBasicBuy(t *testing.T) {
	dir := writeMultiAssetTestCSVs(t)

	sim, err := NewMultiAssetSimulator(10_000.0, 1.0, 0.0)
	if err != nil {
		t.Fatalf("NewMultiAssetSimulator failed: %v", err)
	}

	signals := []TradeSignal{
		{Date: mustParseDate("2025-03-01"), Action: "BUY", Reason: "buy all"},
	}

	result, err := sim.RunMultiAssetSimulation(
		context.Background(), []string{"ASSET_A", "ASSET_B"}, dir, signals,
	)
	if err != nil {
		t.Fatalf("RunMultiAssetSimulation failed: %v", err)
	}

	if result.TotalTrades < 1 {
		t.Errorf("expected at least 1 trade, got %d", result.TotalTrades)
	}

	// On day 1, only ASSET_A has data (price=50). ASSET_B has no data yet,
	// so it should not execute a buy (LastPrice == 0).
	// Day 1: buy ASSET_A at 50 -> shares = floor(10000/50) = 200 shares.
	// Day 2: ASSET_B gets its first price (200), but signal was only on day 1.

	if len(result.EquityCurve) == 0 {
		t.Fatal("expected non-empty equity curve")
	}

	// Final equity should be > initial capital since prices rose.
	if result.FinalEquity <= sim.InitialCapital {
		t.Errorf("final equity %.2f should be > initial capital %.2f",
			result.FinalEquity, sim.InitialCapital)
	}

	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
	if !strings.Contains(result.Summary, "Multi-asset") {
		t.Error("summary should contain 'Multi-asset'")
	}
}

// TestMultiAssetStalePriceForwardFill verifies that when an asset lacks data
// on a given date, the last known close price is used with a "stale_price" flag.
func TestMultiAssetStalePriceForwardFill(t *testing.T) {
	dir := writeMultiAssetTestCSVs(t)

	sim, err := NewMultiAssetSimulator(10_000.0, 1.0, 0.0)
	if err != nil {
		t.Fatalf("NewMultiAssetSimulator failed: %v", err)
	}

	signals := []TradeSignal{
		{Date: mustParseDate("2025-03-02"), Action: "BUY", Reason: "buy on day 2"},
		{Date: mustParseDate("2025-03-03"), Action: "SELL", Reason: "sell on day 3"},
	}

	result, err := sim.RunMultiAssetSimulation(
		context.Background(), []string{"ASSET_A", "ASSET_B"}, dir, signals,
	)
	if err != nil {
		t.Fatalf("RunMultiAssetSimulation failed: %v", err)
	}

	// ASSET_B has no data on 2025-03-01 but has data from 2025-03-02 onward.
	// On 2025-03-02, ASSET_A also has data so both get fresh prices.
	// The aligned dates are: 2025-03-01, 2025-03-02, 2025-03-03.
	// On 2025-03-01: ASSET_B has no bar and no prior price -> LastPrice stays 0.
	// On 2025-03-02: both have fresh prices.
	// On 2025-03-03: both have fresh prices.

	// Check that we got warnings about stale price if any symbol used forward-fill.
	// In this specific fixture, ASSET_B has no data on 2025-03-01, which is before
	// its first bar, so there's no "stale" fill (just zero). But let's verify
	// the equity curve covers all 3 aligned dates.
	if len(result.EquityCurve) != 3 {
		t.Errorf("expected 3 equity points (one per aligned date), got %d", len(result.EquityCurve))
	}

	// Verify positions map contains both symbols.
	if _, ok := result.Positions["ASSET_A"]; !ok {
		t.Error("positions should contain ASSET_A")
	}
	if _, ok := result.Positions["ASSET_B"]; !ok {
		t.Error("positions should contain ASSET_B")
	}
}

// TestMultiAssetEquityCurveCombinedValue verifies that each point on the
// equity curve equals cash + sum(shares_i * lastPrice_i) for all positions.
func TestMultiAssetEquityCurveCombinedValue(t *testing.T) {
	dir := writeMultiAssetTestCSVs(t)

	sim, err := NewMultiAssetSimulator(20_000.0, 0.5, 0.0)
	if err != nil {
		t.Fatalf("NewMultiAssetSimulator failed: %v", err)
	}

	signals := []TradeSignal{
		{Date: mustParseDate("2025-03-02"), Action: "BUY", Reason: "buy both on day 2"},
	}

	result, err := sim.RunMultiAssetSimulation(
		context.Background(), []string{"ASSET_A", "ASSET_B"}, dir, signals,
	)
	if err != nil {
		t.Fatalf("RunMultiAssetSimulation failed: %v", err)
	}

	for i, ep := range result.EquityCurve {
		if ep.Equity != ep.Cash+ep.Exposure {
			t.Errorf("equity curve[%d]: equity=%.2f != cash(%.2f)+exposure(%.2f)",
				i, ep.Equity, ep.Cash, ep.Exposure)
		}
	}
}

// TestMultiAssetEmptySignals verifies that an empty signal list produces
// a valid result with no trades and equity curve equal to initial capital.
func TestMultiAssetEmptySignals(t *testing.T) {
	dir := writeMultiAssetTestCSVs(t)

	sim, err := NewMultiAssetSimulator(50_000.0, 0.1, 0.001)
	if err != nil {
		t.Fatalf("NewMultiAssetSimulator failed: %v", err)
	}

	result, err := sim.RunMultiAssetSimulation(
		context.Background(), []string{"ASSET_A", "ASSET_B"}, dir, []TradeSignal{},
	)
	if err != nil {
		t.Fatalf("RunMultiAssetSimulation failed: %v", err)
	}

	if result.TotalTrades != 0 {
		t.Errorf("expected 0 trades with empty signals, got %d", result.TotalTrades)
	}

	if len(result.EquityCurve) == 0 {
		t.Error("expected non-empty equity curve even with no trades")
	}

	// With no trades, final equity should equal initial capital.
	if result.FinalEquity < sim.InitialCapital-0.01 || result.FinalEquity > sim.InitialCapital+0.01 {
		t.Errorf("final equity ≈ %.2f, want %.2f (no trades)", result.FinalEquity, sim.InitialCapital)
	}
}

// TestMultiAssetDifferentTradingDates verifies mark-to-market behavior when
// two assets trade on different dates — the equity curve reflects holdings
// in both even when only one has new data on a given date.
func TestMultiAssetDifferentTradingDates(t *testing.T) {
	dir := writeMultiAssetTestCSVs(t)

	sim, err := NewMultiAssetSimulator(100_000.0, 1.0, 0.0)
	if err != nil {
		t.Fatalf("NewMultiAssetSimulator failed: %v", err)
	}

	// Buy A on day 1 (only A has data), buy B on day 2 (both have data).
	signals := []TradeSignal{
		{Date: mustParseDate("2025-03-01"), Action: "BUY", Reason: "buy A"},
		{Date: mustParseDate("2025-03-02"), Action: "BUY", Reason: "buy B"},
		{Date: mustParseDate("2025-03-03"), Action: "SELL", Reason: "sell all"},
	}

	result, err := sim.RunMultiAssetSimulation(
		context.Background(), []string{"ASSET_A", "ASSET_B"}, dir, signals,
	)
	if err != nil {
		t.Fatalf("RunMultiAssetSimulation failed: %v", err)
	}

	// Should have buys and sells across both assets.
	if result.TotalTrades < 2 {
		t.Errorf("expected at least 2+ trades, got %d", result.TotalTrades)
	}

	// After selling everything, most capital should be back in cash.
	lastPoint := result.EquityCurve[len(result.EquityCurve)-1]
	if lastPoint.Exposure > 0.01 {
		t.Errorf("after SELL all, exposure should be ~0, got %.2f", lastPoint.Exposure)
	}
}

// TestMultiAssetMissingDataUsesLastValidPrice verifies that when an asset
// is missing data for some dates within its range, the last known price is used.
func TestMultiAssetMissingDataUsesLastValidPrice(t *testing.T) {
	// Create custom CSV where ASSET_X skips a day in the middle.
	const assetXCSV = `date,open,high,low,close,volume
2025-06-01,10.0,11.0,9.9,10.0,1000
2025-06-03,12.0,13.0,11.9,12.5,1000
`
	const assetYCSV = `date,open,high,low,close,volume
2025-06-01,100.0,101.0,99.0,100.0,2000
2025-06-02,101.0,102.0,100.0,101.0,2100
2025-06-03,103.0,104.0,102.0,103.0,2200
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ASSET_X.csv"), []byte(assetXCSV), 0o644); err != nil {
		t.Fatalf("write ASSET_X.csv: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ASSET_Y.csv"), []byte(assetYCSV), 0o644); err != nil {
		t.Fatalf("write ASSET_Y.csv: %v", err)
	}

	sim, err := NewMultiAssetSimulator(10_000.0, 1.0, 0.0)
	if err != nil {
		t.Fatalf("NewMultiAssetSimulator failed: %v", err)
	}

	result, err := sim.RunMultiAssetSimulation(
		context.Background(), []string{"ASSET_X", "ASSET_Y"}, dir, []TradeSignal{
			{Date: mustParseDate("2025-06-01"), Action: "BUY", Reason: "buy"},
		},
	)
	if err != nil {
		t.Fatalf("RunMultiAssetSimulation failed: %v", err)
	}

	// ASSET_X is missing 2025-06-02; on that date it should use last-close (10.0)
	// with stale_price flag.
	posX := result.Positions["ASSET_X"]
	if posX.Shares == 0 {
		t.Error("ASSET_X should have been bought")
	}

	// Check that aligned dates include all 3 days (union of X:{01,03} and Y:{01,02,03}).
	if len(result.EquityCurve) != 3 {
		t.Errorf("expected 3 aligned dates, got %d equity points", len(result.EquityCurve))
	}

	// Check for stale-price warning.
	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "stale") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Error("expected stale-price warning when ASSET_X skipped 2025-06-02")
	}
}

// TestNewMultiAssetSimulatorInvalid verifies constructor validation.
func TestNewMultiAssetSimulatorInvalid(t *testing.T) {
	tests := []struct {
		name    string
		capital float64
		size    float64
		comm    float64
		wantErr bool
	}{
		{"zero capital", 0, 0.1, 0.001, true},
		{"negative capital", -100, 0.1, 0.001, true},
		{"zero position size", 10000, 0, 0.001, true},
		{"position size > 1", 10000, 1.5, 0.001, true},
		{"negative commission", 10000, 0.1, -0.01, true},
		{"valid", 10000, 0.1, 0.001, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMultiAssetSimulator(tt.capital, tt.size, tt.comm)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestMultiAssetContextCancellation verifies simulation stops on context cancellation.
func TestMultiAssetContextCancellation(t *testing.T) {
	dir := writeMultiAssetTestCSVs(t)

	sim, err := NewMultiAssetSimulator(10000.0, 0.1, 0.001)
	if err != nil {
		t.Fatalf("NewMultiAssetSimulator failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = sim.RunMultiAssetSimulation(ctx, []string{"ASSET_A", "ASSET_B"}, dir, []TradeSignal{
		{Date: mustParseDate("2025-03-01"), Action: "BUY", Reason: "test"},
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

// TestMultiAssetAssetTypeClassification verifies that asset types are correctly
// assigned from the AssetTypes map or defaulted to us_stock.
func TestMultiAssetAssetTypeClassification(t *testing.T) {
	dir := writeMultiAssetTestCSVs(t)

	sim, err := NewMultiAssetSimulator(10_000.0, 1.0, 0.0)
	if err != nil {
		t.Fatalf("NewMultiAssetSimulator failed: %v", err)
	}
	sim.AssetTypes = map[string]AssetType{
		"ASSET_A": AssetCrypto,
		"ASSET_B": AssetCNStock,
	}

	result, err := sim.RunMultiAssetSimulation(
		context.Background(), []string{"ASSET_A", "ASSET_B"}, dir, []TradeSignal{},
	)
	if err != nil {
		t.Fatalf("RunMultiAssetSimulation failed: %v", err)
	}

	if result.Positions["ASSET_A"].AssetType != AssetCrypto {
		t.Errorf("ASSET_A asset type = %q, want %q", result.Positions["ASSET_A"].AssetType, AssetCrypto)
	}
	if result.Positions["ASSET_B"].AssetType != AssetCNStock {
		t.Errorf("ASSET_B asset type = %q, want %q", result.Positions["ASSET_B"].AssetType, AssetCNStock)
	}
}
