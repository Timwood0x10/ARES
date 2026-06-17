// Package main — investment simulator for backtesting trade signals against
// historical market data. Supports BUY/SELL/HOLD signal execution with position
// sizing, commission, and performance metrics (Sharpe, drawdown, win rate).
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"goagentx/api/marketmaking"
	"goagentx/internal/quant/research"
)

// TradeSignal represents a time-based trading instruction produced by the
// research layer or an external strategy.
type TradeSignal struct {
	Date       time.Time `json:"date"`
	Action     string    `json:"action"` // "BUY", "SELL", or "HOLD"
	Reason     string    `json:"reason,omitempty"`
	Confidence float64   `json:"confidence,omitempty"` // 0–1
}

// SimulationResult holds the complete output of a backtest run, including
// performance metrics, equity curve, and per-trade log.
type SimulationResult struct {
	Ticker         string                     `json:"ticker"`
	InitialCapital float64                    `json:"initial_capital"`
	FinalEquity    float64                    `json:"final_equity"`
	TotalPnL       float64                    `json:"total_pnl"`
	TotalReturn    float64                    `json:"total_return"` // percentage
	SharpeRatio    float64                    `json:"sharpe_ratio"`
	MaxDrawdown    float64                    `json:"max_drawdown"` // positive fraction
	WinRate        float64                    `json:"win_rate"`     // 0–1
	TotalTrades    int                        `json:"total_trades"`
	WinningTrades  int                        `json:"winning_trades"`
	LosingTrades   int                        `json:"losing_trades"`
	EquityCurve    []marketmaking.EquityPoint `json:"equity_curve"`
	TradeLog       []marketmaking.TradeRecord `json:"trade_log"`
	Summary        string                     `json:"summary"`
}

// InvestmentSimulator executes a backtest by replaying trade signals over
// historical OHLCV bars loaded from CSV.
type InvestmentSimulator struct {
	InitialCapital float64
	PositionSize   float64 // fraction of capital per trade (e.g., 0.05 for 5%)
	Commission     float64 // commission rate per trade (e.g., 0.001)
}

// priceBar is an internal representation of one row from the CSV.
type priceBar struct {
	Date   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}

// RunSimulation loads historical price data from CSV, replays trade signals
// day-by-day, and returns a populated SimulationResult.
//
// Execution model:
//   - On each bar, if a signal's date matches (same calendar day), execute it.
//   - BUY: spend capital * position_size at close; round down to whole shares.
//   - SELL: liquidate entire position at close.
//   - HOLD: no action.
//   - Commission is deducted on every fill.
func (s *InvestmentSimulator) RunSimulation(
	ctx context.Context,
	ticker string,
	dataDir string,
	signals []TradeSignal,
) (*SimulationResult, error) {

	bars, err := s.loadPriceData(ticker, dataDir)
	if err != nil {
		return nil, fmt.Errorf("load price data: %w", err)
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("no price data found for %s in %s", ticker, dataDir)
	}

	// Index signals by date for O(1) lookup during bar iteration.
	signalIndex := make(map[string][]TradeSignal, len(signals))
	for _, sig := range signals {
		key := sig.Date.Format("2006-01-02")
		signalIndex[key] = append(signalIndex[key], sig)
	}

	cash := s.InitialCapital
	shares := 0.0
	costBasis := 0.0 // weighted average cost per share for PnL calculation
	var equityCurve []marketmaking.EquityPoint
	var tradeLog []marketmaking.TradeRecord
	tradeID := 0
	winningTrades := 0
	losingTrades := 0
	totalClosed := 0
	peakEquity := s.InitialCapital
	maxDrawdown := 0.0
	var dailyReturns []float64
	prevEquity := s.InitialCapital

	for barIdx, bar := range bars {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Execute any signals that match this bar date.
		dateKey := bar.Date.Format("2006-01-02")
		if sigs, ok := signalIndex[dateKey]; ok {
			for _, sig := range sigs {
				switch sig.Action {
				case "BUY":
					investment := cash * s.PositionSize
					if investment <= 0 || bar.Close <= 0 {
						continue
					}
					costPerShare := bar.Close * (1 + s.Commission)
					buyShares := math.Floor(investment / costPerShare)
					if buyShares <= 0 {
						continue
					}
					cost := buyShares * costPerShare
					totalCost := costBasis*shares + cost
					cash -= cost
					shares += buyShares
					if shares > 0 {
						costBasis = totalCost / shares
					}
					tradeID++
					tradeLog = append(tradeLog, marketmaking.TradeRecord{
						ID:        fmt.Sprintf("T%d", tradeID),
						Symbol:    ticker,
						Side:      "buy",
						Price:     bar.Close,
						Quantity:  buyShares,
						Timestamp: bar.Date,
					})
				case "SELL":
					if shares <= 0 {
						continue
					}
					sellPrice := bar.Close * (1 - s.Commission)
					proceeds := shares * sellPrice
					pnl := proceeds - (shares * costBasis) // realized PnL vs weighted-average cost basis
					cash += proceeds
					tradeID++
					tradeLog = append(tradeLog, marketmaking.TradeRecord{
						ID:        fmt.Sprintf("T%d", tradeID),
						Symbol:    ticker,
						Side:      "sell",
						Price:     bar.Close,
						Quantity:  shares,
						Timestamp: bar.Date,
						PnL:       pnl,
					})
					// Track win/loss on closed trades.
					if pnl >= 0 {
						winningTrades++
					} else {
						losingTrades++
					}
					totalClosed++
					shares = 0
					costBasis = 0
				case "HOLD":
					// No action.
				}
			}
		}

		// Compute current equity (cash + mark-to-market position).
		equity := cash + shares*bar.Close

		// Track daily return for Sharpe ratio.
		if barIdx > 0 && prevEquity > 0 {
			dailyRet := (equity - prevEquity) / prevEquity
			dailyReturns = append(dailyReturns, dailyRet)
		}
		prevEquity = equity

		// Track peak and drawdown.
		if equity > peakEquity {
			peakEquity = equity
		}
		dd := 0.0
		if peakEquity > 0 {
			dd = (peakEquity - equity) / peakEquity
		}
		if dd > maxDrawdown {
			maxDrawdown = dd
		}

		// Record equity point at each bar.
		equityCurve = append(equityCurve, marketmaking.EquityPoint{
			Time:     bar.Date,
			Equity:   equity,
			Cash:     cash,
			Exposure: shares * bar.Close,
			Drawdown: dd,
		})
	}

	finalEquity := cash + shares*bars[len(bars)-1].Close
	totalPnL := finalEquity - s.InitialCapital
	totalReturn := 0.0
	if s.InitialCapital > 0 {
		totalReturn = totalPnL / s.InitialCapital * 100
	}
	sharpe := computeSharpeRatio(dailyReturns)
	winRate := 0.0
	if totalClosed > 0 {
		winRate = float64(winningTrades) / float64(totalClosed)
	}

	result := &SimulationResult{
		Ticker:         ticker,
		InitialCapital: s.InitialCapital,
		FinalEquity:    finalEquity,
		TotalPnL:       totalPnL,
		TotalReturn:    totalReturn,
		SharpeRatio:    sharpe,
		MaxDrawdown:    maxDrawdown,
		WinRate:        winRate,
		TotalTrades:    tradeID,
		WinningTrades:  winningTrades,
		LosingTrades:   losingTrades,
		EquityCurve:    equityCurve,
		TradeLog:       tradeLog,
		Summary: s.formatSummary(ticker, finalEquity, totalPnL, totalReturn,
			sharpe, maxDrawdown, winRate, tradeID, winningTrades, losingTrades),
	}

	return result, nil
}

// loadPriceData reads {dataDir}/{ticker}.csv and returns ordered price bars.
func (s *InvestmentSimulator) loadPriceData(ticker string, dataDir string) ([]priceBar, error) {
	path := filepath.Join(dataDir, ticker+".csv")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open CSV %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read CSV %s: %w", path, err)
	}

	if len(records) < 2 {
		return nil, nil // header only or empty
	}

	var bars []priceBar
	for _, row := range records[1:] { // skip header
		if len(row) < 6 {
			continue
		}
		dt, err := time.Parse("2006-01-02", row[0])
		if err != nil {
			continue
		}
		open, errOpen := strconv.ParseFloat(row[1], 64)
		high, errHigh := strconv.ParseFloat(row[2], 64)
		low, errLow := strconv.ParseFloat(row[3], 64)
		close, errClose := strconv.ParseFloat(row[4], 64)
		vol, errVol := strconv.ParseInt(row[5], 10, 64)
		if errOpen != nil || errHigh != nil || errLow != nil || errClose != nil || errVol != nil {
			continue
		}

		bars = append(bars, priceBar{
			Date:   dt,
			Open:   open,
			High:   high,
			Low:    low,
			Close:  close,
			Volume: vol,
		})
	}
	return bars, nil
}

// computeSharpeRatio calculates the annualized Sharpe ratio from daily returns.
// Risk-free rate assumed to be 0. Returns 0 if insufficient data.
func computeSharpeRatio(dailyReturns []float64) float64 {
	n := len(dailyReturns)
	if n < 2 {
		return 0
	}

	mean := 0.0
	for _, r := range dailyReturns {
		mean += r
	}
	mean /= float64(n)

	variance := 0.0
	for _, r := range dailyReturns {
		diff := r - mean
		variance += diff * diff
	}
	if n > 1 {
		variance /= float64(n - 1)
	}

	std := math.Sqrt(variance)
	if std == 0 {
		return 0
	}

	// Annualize: multiply daily Sharpe by sqrt(252).
	return (mean / std) * math.Sqrt(252)
}

// formatSummary builds a human-readable summary string for the simulation result.
func (s *InvestmentSimulator) formatSummary(
	ticker string,
	finalEquity, totalPnL, totalReturn, sharpe, maxDD, winRate float64,
	totalTrades, wins, losses int,
) string {
	return fmt.Sprintf(
		"Backtest [%s]: PnL=%.2f (%.2f%%), Sharpe=%.2f, MaxDD=%.2f%%, WinRate=%.1f%%, Trades=%d (W:%d L:%d)",
		ticker, totalPnL, totalReturn, sharpe, maxDD*100, winRate*100,
		totalTrades, wins, losses,
	)
}

// GenerateSignalsFromResearch converts a research PortfolioDecision into
// a slice of TradeSignals. It creates:
//   - A BUY signal when rating is Buy or Overweight.
//   - A SELL signal when rating is Underweight or Sell.
//   - A HOLD signal otherwise (Hold rating).
//
// The signal date is set to time.Now(); callers should adjust if they need
// a specific simulation date.
func GenerateSignalsFromResearch(decision *research.PortfolioDecision) []TradeSignal {
	if decision == nil {
		return []TradeSignal{
			{Date: time.Now(), Action: "HOLD", Reason: "no decision available"},
		}
	}
	if decision.Rating == "" {
		return []TradeSignal{
			{Date: time.Now(), Action: "HOLD", Reason: "invalid decision: empty rating"},
		}
	}

	action := "HOLD"
	reason := fmt.Sprintf("Rating=%s: %s", decision.Rating, decision.ExecutiveSummary)

	switch decision.Rating {
	case research.RatingBuy, research.RatingOverweight:
		action = "BUY"
	case research.RatingUnderweight, research.RatingSell:
		action = "SELL"
	}

	confidence := 0.7
	if decision.PriceTarget != nil && *decision.PriceTarget > 0 {
		confidence = 0.8
	}

	return []TradeSignal{
		{
			Date:       time.Now(),
			Action:     action,
			Reason:     reason,
			Confidence: confidence,
		},
	}
}

// SaveSimulationResult writes the SimulationResult as indented JSON to the
// given file path.
func SaveSimulationResult(result *SimulationResult, outPath string) error {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	return nil
}
