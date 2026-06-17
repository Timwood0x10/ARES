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

	"goagentx/api/marketmaking"
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

// runTestBacktest is a helper that runs a backtest via the public API with given signals.
func runTestBacktest(t *testing.T, ticker, dataDir string, signals []TradeSignal, capital float64) *marketmaking.BacktestResponse {
	t.Helper()
	apiSignals := make([]marketmaking.TradeSignal, len(signals))
	for i, s := range signals {
		apiSignals[i] = marketmaking.TradeSignal{
			Date:       s.Date,
			Action:     s.Action,
			Reason:     s.Reason,
			Confidence: s.Confidence,
		}
	}
	runner := marketmaking.NewDefaultBacktestRunnerWithDataDir(dataDir)
	req := &marketmaking.BacktestRequest{
		Symbols:        []string{ticker},
		InitialCapital: capital,
		PositionSize:   1.0,
		Commission:     1e-9,
		DataDir:        dataDir,
		Signals:        apiSignals,
	}
	resp, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("backtest run failed: %v", err)
	}
	return resp
}

// TestRunSimulationWithBuyOnly verifies that buying at the first bar and
// holding to the end produces correct equity and PnL.
func TestRunSimulationWithBuyOnly(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	signals := []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "test buy"},
	}

	resp := runTestBacktest(t, "TEST", dir, signals, 10_000.0)

	if resp.TotalTrades < 1 {
		t.Errorf("expected at least 1 trade, got %d", resp.TotalTrades)
	}

	// Through the public API layer, zero commission is overridden to a small
	// positive value, which reduces share count by floor rounding (99 vs 100).
	// Accept any result within 2% of the ideal no-commission final equity.
	finalEquity := resp.TotalPnL + 10000.0
	idealFinal := 100 * 110.0
	if finalEquity < idealFinal*0.98 {
		t.Errorf("final equity ≈ %.2f, want ≥ %.2f (−2%% tolerance)", finalEquity, idealFinal*0.98)
	}

	if resp.TotalPnL <= 0 {
		t.Errorf("expected positive PnL since prices rose, got %.2f", resp.TotalPnL)
	}

	if len(resp.EquityCurve) == 0 {
		t.Error("expected non-empty equity curve")
	}

	if len(resp.TradeLog) == 0 {
		t.Error("expected non-empty trade log")
	}
}

// TestRunSimulationWithBuySell verifies a round-trip trade: buy early,
// sell later, and check PnL and trade count.
func TestRunSimulationWithBuySell(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	signals := []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "buy signal"},
		{Date: mustParseDate("2025-01-06"), Action: "SELL", Reason: "sell signal"},
	}

	resp := runTestBacktest(t, "TEST", dir, signals, 10_000.0)

	if resp.TotalTrades < 2 {
		t.Errorf("expected at least 2 trades, got %d", resp.TotalTrades)
	}

	if resp.TotalPnL <= 0 {
		t.Errorf("expected positive PnL on up-move, got %.2f", resp.TotalPnL)
	}

	if resp.TotalTrades >= 2 && resp.WinningTrades == 0 && resp.LosingTrades == 0 {
		t.Error("expected win/loss tracking on sell trade")
	}
}

// TestRunSimulationEmptySignals verifies that an empty signal list produces
// a valid result with no trades and zero PnL.
func TestRunSimulationEmptySignals(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	resp := runTestBacktest(t, "TEST", dir, []TradeSignal{}, 50_000.0)

	if resp.TotalTrades != 0 {
		t.Errorf("expected 0 trades with empty signals, got %d", resp.TotalTrades)
	}

	if resp.TotalPnL < -0.01 || resp.TotalPnL > 0.01 {
		t.Errorf("expected ~0 PnL with no trades, got %.2f", resp.TotalPnL)
	}

	if len(resp.EquityCurve) != 5 {
		t.Errorf("expected 5 equity points (one per bar), got %d", len(resp.EquityCurve))
	}
}

// TestMetricsCalculation verifies Sharpe ratio, max drawdown, and win rate.
func TestMetricsCalculation(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	signals := []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "buy #1"},
		{Date: mustParseDate("2025-01-04"), Action: "SELL", Reason: "sell #1 (profit)"},
		{Date: mustParseDate("2025-01-05"), Action: "BUY", Reason: "buy #2"},
		{Date: mustParseDate("2025-01-06"), Action: "SELL", Reason: "sell #2 (profit)"},
	}

	resp := runTestBacktest(t, "TEST", dir, signals, 100_000.0)

	if resp.WinningTrades < 2 {
		t.Errorf("expected at least 2 winning trades in rising market, got %d", resp.WinningTrades)
	}

	totalClosed := resp.WinningTrades + resp.LosingTrades
	if totalClosed > 0 {
		expectedWR := float64(resp.WinningTrades) / float64(totalClosed)
		if resp.WinRate < expectedWR-0.001 || resp.WinRate > expectedWR+0.001 {
			t.Errorf("win rate = %.4f, want %.4f", resp.WinRate, expectedWR)
		}
	}

	if resp.MaxDrawdown > 0.5 {
		t.Errorf("max drawdown suspiciously high: %.2f%%", resp.MaxDrawdown*100)
	}

	if mathIsNaN(resp.SharpeRatio) {
		t.Error("SharpeRatio is NaN")
	}

	if resp.TotalReturn <= 0 {
		t.Errorf("expected positive total return, got %.2f%%", resp.TotalReturn)
	}

	if resp.Summary == "" {
		t.Error("summary should not be empty")
	}
	if !strings.Contains(resp.Summary, "TEST") {
		t.Error("summary should contain ticker name")
	}
}

// TestGenerateSignalsFromResearchBuy verifies BUY signal generation from ratings.
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

// TestSaveSimulationJSON verifies JSON serialization of BacktestResponse to file.
func TestSaveSimulationJSON(t *testing.T) {
	resp := &marketmaking.BacktestResponse{
		TotalPnL:      200.0,
		TotalReturn:   20.0,
		SharpeRatio:   1.5,
		MaxDrawdown:   0.05,
		WinRate:       0.6,
		TotalTrades:   10,
		WinningTrades: 6,
		LosingTrades:  4,
		Summary:       "test summary",
	}

	outPath := filepath.Join(t.TempDir(), "sim_result.json")
	if err := saveSimulationJSON(resp, outPath); err != nil {
		t.Fatalf("saveSimulationJSON failed: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}

	content := string(data)
	fields := []string{"total_pnl", "sharpe_ratio", "win_rate"}
	for _, f := range fields {
		if !strings.Contains(content, f) {
			t.Errorf("output JSON missing field %q", f)
		}
	}
}

// TestLoadPriceDataMissingFile verifies error handling for missing CSV.
func TestLoadPriceDataMissingFile(t *testing.T) {
	runner := marketmaking.NewDefaultBacktestRunnerWithDataDir("/tmp/no_such_dir_xyz")
	req := &marketmaking.BacktestRequest{
		Symbols:        []string{"NONEXISTENT"},
		InitialCapital: 10000.0,
		Signals:        []marketmaking.TradeSignal{},
	}
	_, err := runner.Run(context.Background(), req)
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

	runner := marketmaking.NewDefaultBacktestRunnerWithDataDir(dir)
	req := &marketmaking.BacktestRequest{
		Symbols:        []string{"TEST"},
		InitialCapital: 10000.0,
		Signals: []marketmaking.TradeSignal{
			{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "test"},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runner.Run(ctx, req)
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

func mathIsNaN(f float64) bool { return f != f }
