package portfolio

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
)

// InvestmentSimulator executes a backtest by replaying trade signals over
// historical OHLCV bars loaded from CSV.
type InvestmentSimulator struct {
	InitialCapital float64
	PositionSize   float64 // fraction of capital per trade (e.g., 0.05 for 5%)
	Commission     float64 // commission rate per trade (e.g., 0.001)
}

// NewInvestmentSimulator creates a new InvestmentSimulator with validated fields.
//
// Args:
//   - initialCapital: starting cash balance, must be > 0.
//   - positionSize: fraction of capital per trade, must be in (0, 1].
//   - commission: per-trade fee rate, must be >= 0.
//
// Returns:
//   - configured simulator.
//   - error if any parameter is invalid.
func NewInvestmentSimulator(initialCapital, positionSize, commission float64) (*InvestmentSimulator, error) {
	if initialCapital <= 0 {
		return nil, fmt.Errorf("initial capital must be > 0, got %f", initialCapital)
	}
	if positionSize <= 0 || positionSize > 1 {
		return nil, fmt.Errorf("position size must be in (0, 1], got %f", positionSize)
	}
	if commission < 0 {
		return nil, fmt.Errorf("commission must be >= 0, got %f", commission)
	}
	return &InvestmentSimulator{
		InitialCapital: initialCapital,
		PositionSize:   positionSize,
		Commission:     commission,
	}, nil
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
//
// Args:
//   - ctx: context for cancellation.
//   - ticker: symbol being simulated.
//   - dataDir: directory containing {ticker}.csv.
//   - signals: trade signal sequence to replay.
//
// Returns:
//   - populated SimulationResult with equity curve and trade log.
//   - error if data loading or execution fails.
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
	costBasis := 0.0
	var equityCurve []EquityPoint
	var tradeLog []TradeRecord
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
					tradeLog = append(tradeLog, TradeRecord{
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
					pnl := proceeds - (shares * costBasis)
					cash += proceeds
					tradeID++
					tradeLog = append(tradeLog, TradeRecord{
						ID:        fmt.Sprintf("T%d", tradeID),
						Symbol:    ticker,
						Side:      "sell",
						Price:     bar.Close,
						Quantity:  shares,
						Timestamp: bar.Date,
						PnL:       pnl,
					})
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
		equityCurve = append(equityCurve, EquityPoint{
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
		Summary:        formatSummary(ticker, finalEquity, totalPnL, totalReturn, sharpe, maxDrawdown, winRate, tradeID, winningTrades, losingTrades),
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
		openPrice, errOpen := strconv.ParseFloat(row[1], 64)
		highPrice, errHigh := strconv.ParseFloat(row[2], 64)
		lowPrice, errLow := strconv.ParseFloat(row[3], 64)
		closePrice, errClose := strconv.ParseFloat(row[4], 64)
		vol, errVol := strconv.ParseInt(row[5], 10, 64)
		if errOpen != nil || errHigh != nil || errLow != nil || errClose != nil || errVol != nil {
			continue
		}

		bars = append(bars, priceBar{
			Date:   dt,
			Open:   openPrice,
			High:   highPrice,
			Low:    lowPrice,
			Close:  closePrice,
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

	return (mean / std) * math.Sqrt(252)
}

// formatSummary builds a human-readable summary string for the simulation result.
func formatSummary(
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

// SaveSimulationResult writes the SimulationResult as indented JSON to the
// given file path.
//
// Args:
//   - result: the simulation result to serialize.
//   - outPath: destination file path.
//
// Returns:
//   - error if directory creation or file write fails.
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
