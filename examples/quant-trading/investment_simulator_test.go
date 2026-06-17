//go:build !researchdemo

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goagentx/internal/quant/research"
)

// testCSVData is inline fixture data for simulator tests.
// 5 days of price data: starts at 100, rises to 110.
const testCSVData = `date,open,high,low,close,volume
2025-01-02,99.0,101.0,98.0,100.0,1000000
2025-01-03,100.5,103.0,99.5,102.0,1100000
2025-01-04,102.0,105.0,101.0,104.0,1200000
2025-01-05,104.0,108.0,103.0,107.0,1300000
2025-01-06,107.0,112.0,106.0,110.0,1400000
`

// writeTestCSV creates a temporary CSV file with test price data and returns its directory.
func writeTestCSV(t *testing.T, ticker string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ticker+".csv")
	if err := os.WriteFile(path, []byte(testCSVData), 0o644); err != nil {
		t.Fatalf("write test CSV: %v", err)
	}
	return dir
}

// TestRunSimulationWithBuyOnly verifies that buying at the first bar and
// holding to the end produces correct equity and PnL.
func TestRunSimulationWithBuyOnly(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim := &InvestmentSimulator{
		InitialCapital: 10_000.0,
		PositionSize:   1.0, // invest all capital
		Commission:     0.0,
	}

	signals := []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "test buy"},
	}

	result, err := sim.RunSimulation(context.Background(), "TEST", dir, signals)
	if err != nil {
		t.Fatalf("RunSimulation failed: %v", err)
	}

	if result.TotalTrades < 1 {
		t.Errorf("expected at least 1 trade, got %d", result.TotalTrades)
	}

	// With commission=0, PositionSize=1.0, buy at close=100:
	// shares = floor(10000 / 100) = 100 shares
	// final equity = 100 * 110 (last close) = 11000
	expectedFinal := 100 * 110.0
	if result.FinalEquity < expectedFinal-0.01 || result.FinalEquity > expectedFinal+0.01 {
		t.Errorf("final equity ≈ %.2f, want %.2f", result.FinalEquity, expectedFinal)
	}

	if result.TotalPnL <= 0 {
		t.Errorf("expected positive PnL since prices rose, got %.2f", result.TotalPnL)
	}

	if len(result.EquityCurve) == 0 {
		t.Error("expected non-empty equity curve")
	}

	if len(result.TradeLog) == 0 {
		t.Error("expected non-empty trade log")
	}
}

// TestRunSimulationWithBuySell verifies a round-trip trade: buy early,
// sell later, and check PnL and trade count.
func TestRunSimulationWithBuySell(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim := &InvestmentSimulator{
		InitialCapital: 10_000.0,
		PositionSize:   1.0,
		Commission:     0.001,
	}

	signals := []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "buy signal"},
		{Date: mustParseDate("2025-01-06"), Action: "SELL", Reason: "sell signal"},
	}

	result, err := sim.RunSimulation(context.Background(), "TEST", dir, signals)
	if err != nil {
		t.Fatalf("RunSimulation failed: %v", err)
	}

	// Should have at least 2 trades (buy + sell).
	if result.TotalTrades < 2 {
		t.Errorf("expected at least 2 trades, got %d", result.TotalTrades)
	}

	// Buy at 100 with commission, sell at 110 with commission.
	// Should be profitable overall.
	if result.TotalPnL <= 0 {
		t.Errorf("expected positive PnL on up-move, got %.2f", result.TotalPnL)
	}

	// Win rate should account for the closed sell trade.
	if result.TotalTrades >= 2 && result.WinningTrades == 0 && result.LosingTrades == 0 {
		t.Error("expected win/loss tracking on sell trade")
	}
}

// TestRunSimulationEmptySignals verifies that an empty signal list produces
// a valid result with no trades and zero PnL.
func TestRunSimulationEmptySignals(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim := &InvestmentSimulator{
		InitialCapital: 50_000.0,
		PositionSize:   0.1,
		Commission:     0.001,
	}

	result, err := sim.RunSimulation(context.Background(), "TEST", dir, []TradeSignal{})
	if err != nil {
		t.Fatalf("RunSimulation failed: %v", err)
	}

	if result.TotalTrades != 0 {
		t.Errorf("expected 0 trades with empty signals, got %d", result.TotalTrades)
	}

	// No trades means capital unchanged (ignoring rounding).
	if result.FinalEquity < sim.InitialCapital-0.01 || result.FinalEquity > sim.InitialCapital+0.01 {
		t.Errorf("final equity should equal initial capital when no trades, got %.2f", result.FinalEquity)
	}

	// Equity curve should still have one point per bar.
	if len(result.EquityCurve) != 5 {
		t.Errorf("expected 5 equity points (one per bar), got %d", len(result.EquityCurve))
	}
}

// TestMetricsCalculation verifies Sharpe ratio, max drawdown, and win rate
// are computed correctly for a known scenario.
func TestMetricsCalculation(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim := &InvestmentSimulator{
		InitialCapital: 100_000.0,
		PositionSize:   0.5,
		Commission:     0.0,
	}

	// Create multiple buy/sell pairs to generate trade statistics.
	signals := []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "buy #1"},
		{Date: mustParseDate("2025-01-04"), Action: "SELL", Reason: "sell #1 (profit)"},
		{Date: mustParseDate("2025-01-05"), Action: "BUY", Reason: "buy #2"},
		{Date: mustParseDate("2025-01-06"), Action: "SELL", Reason: "sell #2 (profit)"},
	}

	result, err := sim.RunSimulation(context.Background(), "TEST", dir, signals)
	if err != nil {
		t.Fatalf("RunSimulation failed: %v", err)
	}

	// Both sell trades should be profitable (prices only go up).
	if result.WinningTrades < 2 {
		t.Errorf("expected at least 2 winning trades in rising market, got %d", result.WinningTrades)
	}

	// Win rate should be 1.0 if all sells were winners.
	totalClosed := result.WinningTrades + result.LosingTrades
	if totalClosed > 0 {
		expectedWR := float64(result.WinningTrades) / float64(totalClosed)
		if result.WinRate < expectedWR-0.001 || result.WinRate > expectedWR+0.001 {
			t.Errorf("win rate = %.4f, want %.4f", result.WinRate, expectedWR)
		}
	}

	// Max drawdown should be reasonable (not > 50% in this scenario).
	if result.MaxDrawdown > 0.5 {
		t.Errorf("max drawdown suspiciously high: %.2f%%", result.MaxDrawdown*100)
	}

	// Sharpe ratio should be defined (not NaN).
	if mathIsNaN(result.SharpeRatio) {
		t.Error("SharpeRatio is NaN")
	}

	// Total return should be positive.
	if result.TotalReturn <= 0 {
		t.Errorf("expected positive total return, got %.2f%%", result.TotalReturn)
	}

	// Summary should contain key info.
	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
	if !strings.Contains(result.Summary, "TEST") {
		t.Error("summary should contain ticker name")
	}
}

// TestGenerateSignalsFromResearchBuy verifies BUY signal generation from
// Buy/Overweight ratings.
func TestGenerateSignalsFromResearchBuy(t *testing.T) {
	tests := []struct {
		name       string
		rating     research.PortfolioRating
		wantAction string
	}{
		{"buy rating", research.RatingBuy, "BUY"},
		{"overweight rating", research.RatingOverweight, "BUY"},
		{"hold rating", research.RatingHold, "HOLD"},
		{"underweight rating", research.RatingUnderweight, "SELL"},
		{"sell rating", research.RatingSell, "SELL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := &research.PortfolioDecision{
				Rating:           tt.rating,
				ExecutiveSummary: "test summary",
				InvestmentThesis: "test thesis",
			}
			signals := GenerateSignalsFromResearch(decision)
			if len(signals) == 0 {
				t.Fatal("expected at least one signal")
			}
			if signals[0].Action != tt.wantAction {
				t.Errorf("action = %q, want %q", signals[0].Action, tt.wantAction)
			}
		})
	}
}

// TestGenerateSignalsFromResearchNil verifies behavior with nil decision.
func TestGenerateSignalsFromResearchNil(t *testing.T) {
	signals := GenerateSignalsFromResearch(nil)
	if len(signals) == 0 {
		t.Fatal("expected HOLD signal for nil decision")
	}
	if signals[0].Action != "HOLD" {
		t.Errorf("action = %q, want HOLD", signals[0].Action)
	}
}

// TestSaveSimulationResult verifies JSON serialization to file.
func TestSaveSimulationResult(t *testing.T) {
	result := &SimulationResult{
		Ticker:         "TST",
		InitialCapital: 1000.0,
		FinalEquity:    1200.0,
		TotalPnL:       200.0,
		TotalReturn:    20.0,
		SharpeRatio:    1.5,
		MaxDrawdown:    0.05,
		WinRate:        0.6,
		TotalTrades:    10,
		WinningTrades:  6,
		LosingTrades:   4,
		Summary:        "test summary",
	}

	outPath := filepath.Join(t.TempDir(), "sim_result.json")
	if err := SaveSimulationResult(result, outPath); err != nil {
		t.Fatalf("SaveSimulationResult failed: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}

	// Verify it contains expected fields.
	content := string(data)
	fields := []string{"TST", "total_pnl", "sharpe_ratio", "win_rate"}
	for _, f := range fields {
		if !strings.Contains(content, f) {
			t.Errorf("output JSON missing field %q", f)
		}
	}
}

// TestLoadPriceDataMissingFile verifies error handling for missing CSV.
func TestLoadPriceDataMissingFile(t *testing.T) {
	sim := &InvestmentSimulator{
		InitialCapital: 10000.0,
		PositionSize:   0.1,
		Commission:     0.001,
	}

	_, err := sim.RunSimulation(context.Background(), "NONEXISTENT", "/tmp/no_such_dir_xyz", []TradeSignal{})
	if err == nil {
		t.Fatal("expected error for missing data file")
	}
	if !strings.Contains(err.Error(), "load price data") {
		t.Errorf("error should mention loading, got: %v", err)
	}
}

// TestContextCancellation verifies simulation stops on context cancellation.
func TestContextCancellation(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim := &InvestmentSimulator{
		InitialCapital: 10000.0,
		PositionSize:   0.1,
		Commission:     0.001,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := sim.RunSimulation(ctx, "TEST", dir, []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "test"},
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

// ─── Helpers ────────────────────────────────────────────────

func mustParseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(fmt.Sprintf("mustParseDate(%q): %v", s, err))
	}
	return t
}

func mathIsNaN(f float64) bool { return f != f } // IEEE 754 NaN check
