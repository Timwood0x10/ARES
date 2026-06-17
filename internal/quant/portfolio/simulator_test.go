package portfolio

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

// TestRunSimulationBuyOnly verifies that buying at the first bar and
// holding to the end produces correct equity and PnL.
func TestRunSimulationBuyOnly(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim, err := NewInvestmentSimulator(10_000.0, 1.0, 0.0)
	if err != nil {
		t.Fatalf("NewInvestmentSimulator failed: %v", err)
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

// TestRunSimulationBuySell verifies a round-trip trade: buy early,
// sell later, and check PnL and trade count.
func TestRunSimulationBuySell(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim, err := NewInvestmentSimulator(10_000.0, 1.0, 0.001)
	if err != nil {
		t.Fatalf("NewInvestmentSimulator failed: %v", err)
	}

	signals := []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "buy signal"},
		{Date: mustParseDate("2025-01-06"), Action: "SELL", Reason: "sell signal"},
	}

	result, err := sim.RunSimulation(context.Background(), "TEST", dir, signals)
	if err != nil {
		t.Fatalf("RunSimulation failed: %v", err)
	}

	if result.TotalTrades < 2 {
		t.Errorf("expected at least 2 trades, got %d", result.TotalTrades)
	}

	if result.TotalPnL <= 0 {
		t.Errorf("expected positive PnL on up-move, got %.2f", result.TotalPnL)
	}

	if result.TotalTrades >= 2 && result.WinningTrades == 0 && result.LosingTrades == 0 {
		t.Error("expected win/loss tracking on sell trade")
	}
}

// TestRunSimulationEmptySignals verifies that an empty signal list produces
// a valid result with no trades and zero PnL.
func TestRunSimulationEmptySignals(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim, err := NewInvestmentSimulator(50_000.0, 0.1, 0.001)
	if err != nil {
		t.Fatalf("NewInvestmentSimulator failed: %v", err)
	}

	result, err := sim.RunSimulation(context.Background(), "TEST", dir, []TradeSignal{})
	if err != nil {
		t.Fatalf("RunSimulation failed: %v", err)
	}

	if result.TotalTrades != 0 {
		t.Errorf("expected 0 trades with empty signals, got %d", result.TotalTrades)
	}

	if result.FinalEquity < sim.InitialCapital-0.01 || result.FinalEquity > sim.InitialCapital+0.01 {
		t.Errorf("final equity should equal initial capital when no trades, got %.2f", result.FinalEquity)
	}

	if len(result.EquityCurve) != 5 {
		t.Errorf("expected 5 equity points (one per bar), got %d", len(result.EquityCurve))
	}
}

// TestMetricsCalculation verifies Sharpe ratio, max drawdown, and win rate
// are computed correctly for a known scenario.
func TestMetricsCalculation(t *testing.T) {
	dir := writeTestCSV(t, "TEST")

	sim, err := NewInvestmentSimulator(100_000.0, 0.5, 0.0)
	if err != nil {
		t.Fatalf("NewInvestmentSimulator failed: %v", err)
	}

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

	if result.WinningTrades < 2 {
		t.Errorf("expected at least 2 winning trades in rising market, got %d", result.WinningTrades)
	}

	totalClosed := result.WinningTrades + result.LosingTrades
	if totalClosed > 0 {
		expectedWR := float64(result.WinningTrades) / float64(totalClosed)
		if result.WinRate < expectedWR-0.001 || result.WinRate > expectedWR+0.001 {
			t.Errorf("win rate = %.4f, want %.4f", result.WinRate, expectedWR)
		}
	}

	if result.MaxDrawdown > 0.5 {
		t.Errorf("max drawdown suspiciously high: %.2f%%", result.MaxDrawdown*100)
	}

	if mathIsNaN(result.SharpeRatio) {
		t.Error("SharpeRatio is NaN")
	}

	if result.TotalReturn <= 0 {
		t.Errorf("expected positive total return, got %.2f%%", result.TotalReturn)
	}

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

	content := string(data)
	fields := []string{"TST", "total_pnl", "sharpe_ratio", "win_rate"}
	for _, f := range fields {
		if !strings.Contains(content, f) {
			t.Errorf("output JSON missing field %q", f)
		}
	}
}

// TestNewInvestmentSimulatorInvalid verifies constructor validation.
func TestNewInvestmentSimulatorInvalid(t *testing.T) {
	tests := []struct {
		name     string
		capital  float64
		size     float64
		comm     float64
		wantFail bool
	}{
		{"zero capital", 0, 0.1, 0.001, true},
		{"negative capital", -100, 0.1, 0.001, true},
		{"zero position size", 10000, 0, 0.001, true},
		{"negative position size", 10000, -0.1, 0.001, true},
		{"position size > 1", 10000, 1.5, 0.001, true},
		{"negative commission", 10000, 0.1, -0.01, true},
		{"valid", 10000, 0.1, 0.001, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewInvestmentSimulator(tt.capital, tt.size, tt.comm)
			if tt.wantFail && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantFail && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestValidateTradeSignal verifies signal validation.
func TestValidateTradeSignal(t *testing.T) {
	tests := []struct {
		name    string
		signal  TradeSignal
		wantErr bool
	}{
		{"valid buy", TradeSignal{Date: time.Now(), Action: "BUY", Confidence: 0.8}, false},
		{"valid sell", TradeSignal{Date: time.Now(), Action: "SELL", Confidence: 0.5}, false},
		{"valid hold", TradeSignal{Date: time.Now(), Action: "HOLD", Confidence: 0.0}, false},
		{"zero date", TradeSignal{Action: "BUY"}, true},
		{"invalid action", TradeSignal{Date: time.Now(), Action: "SHORT"}, true},
		{"negative confidence", TradeSignal{Date: time.Now(), Action: "BUY", Confidence: -0.1}, true},
		{"confidence > 1", TradeSignal{Date: time.Now(), Action: "BUY", Confidence: 1.5}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTradeSignal(tt.signal)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestLoadPriceDataMissingFile verifies error handling for missing CSV.
func TestLoadPriceDataMissingFile(t *testing.T) {
	sim, err := NewInvestmentSimulator(10000.0, 0.1, 0.001)
	if err != nil {
		t.Fatalf("NewInvestmentSimulator failed: %v", err)
	}

	_, err = sim.RunSimulation(context.Background(), "NONEXISTENT", "/tmp/no_such_dir_xyz", []TradeSignal{})
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

	sim, err := NewInvestmentSimulator(10000.0, 0.1, 0.001)
	if err != nil {
		t.Fatalf("NewInvestmentSimulator failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = sim.RunSimulation(ctx, "TEST", dir, []TradeSignal{
		{Date: mustParseDate("2025-01-02"), Action: "BUY", Reason: "test"},
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

// TestComputeSharpeRatio verifies Sharpe ratio calculation edge cases.
func TestComputeSharpeRatio(t *testing.T) {
	if sr := computeSharpeRatio(nil); sr != 0 {
		t.Errorf("nil returns %f, want 0", sr)
	}
	if sr := computeSharpeRatio([]float64{}); sr != 0 {
		t.Errorf("empty returns %f, want 0", sr)
	}
	if sr := computeSharpeRatio([]float64{0.01}); sr != 0 {
		t.Errorf("single return %f, want 0", sr)
	}

	constant := []float64{0.01, 0.01, 0.01, 0.01, 0.01}
	if sr := computeSharpeRatio(constant); sr != 0 {
		t.Errorf("constant returns %f, want 0", sr)
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
