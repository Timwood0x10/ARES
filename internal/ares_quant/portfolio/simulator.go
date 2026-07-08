package portfolio

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
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

// MultiAssetSimulator executes multi-symbol backtests with a unified capital pool.
// It aligns dates across all symbols, forward-fills missing prices, and
// produces combined equity curves.
type MultiAssetSimulator struct {
	InitialCapital float64
	PositionSize   float64
	Commission     float64
	AssetTypes     map[string]AssetType // symbol -> asset type
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

// NewMultiAssetSimulator creates a new MultiAssetSimulator with validated fields.
//
// Args:
//   - initialCapital: starting cash balance, must be > 0.
//   - positionSize: fraction of capital per trade, must be in (0, 1].
//   - commission: per-trade fee rate, must be >= 0.
//
// Returns:
//   - configured multi-asset simulator.
//   - error if any parameter is invalid.
func NewMultiAssetSimulator(initialCapital, positionSize, commission float64) (*MultiAssetSimulator, error) {
	if initialCapital <= 0 {
		return nil, fmt.Errorf("initial capital must be > 0, got %f", initialCapital)
	}
	if positionSize <= 0 || positionSize > 1 {
		return nil, fmt.Errorf("position size must be in (0, 1], got %f", positionSize)
	}
	if commission < 0 {
		return nil, fmt.Errorf("commission must be >= 0, got %f", commission)
	}
	return &MultiAssetSimulator{
		InitialCapital: initialCapital,
		PositionSize:   positionSize,
		Commission:     commission,
		AssetTypes:     make(map[string]AssetType),
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
	bars, err := loadPriceData(ticker, dataDir)
	if err != nil {
		return nil, fmt.Errorf("load price data: %w", err)
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("no price data found for %s in %s", ticker, dataDir)
	}

	signalIndex := indexSignalsByDate(signals)

	state := &simState{
		cash:       s.InitialCapital,
		peakEquity: s.InitialCapital,
	}
	var tradeLog []TradeRecord
	var equityCurve []EquityPoint
	var dailyReturns []float64

	for _, bar := range bars {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		dateKey := bar.Date.Format("2006-01-02")
		if sigs, ok := signalIndex[dateKey]; ok {
			tradeLog = append(tradeLog, s.executeSignals(state, sigs, bar, ticker)...)
		}

		equityCurve = append(equityCurve, recordEquityPoint(
			bar, state, &dailyReturns,
		))
	}

	return s.assembleResult(ticker, bars, state, equityCurve, dailyReturns, tradeLog), nil
}

// ─── Sub-functions extracted from RunSimulation ────────────

// simState holds mutable state during a single-ticker backtest run.
type simState struct {
	cash          float64
	shares        float64
	costBasis     float64
	tradeID       int
	winningTrades int
	losingTrades  int
	totalClosed   int
	peakEquity    float64
	maxDrawdown   float64
	prevEquity    float64
}

// indexSignalsByDate groups signals by their date string for O(1) lookup.
func indexSignalsByDate(signals []TradeSignal) map[string][]TradeSignal {
	idx := make(map[string][]TradeSignal, len(signals))
	for _, sig := range signals {
		key := sig.Date.Format("2006-01-02")
		idx[key] = append(idx[key], sig)
	}
	return idx
}

// executeSignals processes all signals matching the current bar date,
// mutating the simState and returning non-empty TradeRecords.
func (s *InvestmentSimulator) executeSignals(
	st *simState, sigs []TradeSignal, bar priceBar, ticker string,
) []TradeRecord {
	var records []TradeRecord
	for _, sig := range sigs {
		var rec TradeRecord
		switch sig.Action {
		case "BUY":
			rec = s.execBuy(st, bar, ticker)
		case "SELL":
			rec = s.execSell(st, bar, ticker)
		case "HOLD":
			continue
		}
		if rec.ID != "" { // filter out empty records (no-op signals)
			records = append(records, rec)
		}
	}
	return records
}

// execBuy handles a BUY signal against the current bar, updating state.
func (s *InvestmentSimulator) execBuy(st *simState, bar priceBar, ticker string) TradeRecord {
	investment := st.cash * s.PositionSize
	if investment <= 0 || bar.Close <= 0 {
		return TradeRecord{}
	}
	costPerShare := bar.Close * (1 + s.Commission)
	buyShares := math.Floor(investment / costPerShare)
	if buyShares <= 0 {
		return TradeRecord{}
	}
	cost := buyShares * costPerShare
	totalCost := st.costBasis*st.shares + cost
	st.cash -= cost
	st.shares += buyShares
	if st.shares > 0 {
		st.costBasis = totalCost / st.shares
	}
	st.tradeID++
	return TradeRecord{
		ID:        fmt.Sprintf("T%d", st.tradeID),
		Symbol:    ticker,
		Side:      "buy",
		Price:     bar.Close,
		Quantity:  buyShares,
		Timestamp: bar.Date,
	}
}

// execSell handles a SELL signal against the current bar, updating state.
func (s *InvestmentSimulator) execSell(st *simState, bar priceBar, ticker string) TradeRecord {
	if st.shares <= 0 {
		return TradeRecord{}
	}
	sellPrice := bar.Close * (1 - s.Commission)
	proceeds := st.shares * sellPrice
	pnl := proceeds - (st.shares * st.costBasis)
	st.cash += proceeds
	st.tradeID++
	if pnl >= 0 {
		st.winningTrades++
	} else {
		st.losingTrades++
	}
	st.totalClosed++
	st.shares = 0
	st.costBasis = 0
	return TradeRecord{
		ID:        fmt.Sprintf("T%d", st.tradeID),
		Symbol:    ticker,
		Side:      "sell",
		Price:     bar.Close,
		Quantity:  0, // already liquidated
		Timestamp: bar.Date,
		PnL:       pnl,
	}
}

// recordEquityPoint captures the current portfolio snapshot for the equity curve.
// It also updates peak equity, max drawdown, and daily return tracking.
func recordEquityPoint(bar priceBar, st *simState, dailyReturns *[]float64) EquityPoint {
	equity := st.cash + st.shares*bar.Close

	if len(*dailyReturns) > 0 || st.prevEquity != 0 {
		if st.prevEquity > 0 {
			*dailyReturns = append(*dailyReturns, (equity-st.prevEquity)/st.prevEquity)
		}
	}
	st.prevEquity = equity

	if equity > st.peakEquity {
		st.peakEquity = equity
	}
	dd := 0.0
	if st.peakEquity > 0 {
		dd = (st.peakEquity - equity) / st.peakEquity
	}
	if dd > st.maxDrawdown {
		st.maxDrawdown = dd
	}

	return EquityPoint{
		Time:     bar.Date,
		Equity:   equity,
		Cash:     st.cash,
		Exposure: st.shares * bar.Close,
		Drawdown: dd,
	}
}

// assembleResult builds the final SimulationResult from completed state.
func (s *InvestmentSimulator) assembleResult(
	ticker string,
	bars []priceBar,
	st *simState,
	equityCurve []EquityPoint,
	dailyReturns []float64,
	tradeLog []TradeRecord,
) *SimulationResult {
	finalEquity := st.cash + st.shares*bars[len(bars)-1].Close
	totalPnL := finalEquity - s.InitialCapital
	totalReturn := 0.0
	if s.InitialCapital > 0 {
		totalReturn = totalPnL / s.InitialCapital * 100
	}
	sharpe := computeSharpeRatio(dailyReturns)
	winRate := 0.0
	if st.totalClosed > 0 {
		winRate = float64(st.winningTrades) / float64(st.totalClosed)
	}

	return &SimulationResult{
		Ticker:         ticker,
		InitialCapital: s.InitialCapital,
		FinalEquity:    finalEquity,
		TotalPnL:       totalPnL,
		TotalReturn:    totalReturn,
		SharpeRatio:    sharpe,
		MaxDrawdown:    st.maxDrawdown,
		WinRate:        winRate,
		TotalTrades:    st.tradeID,
		WinningTrades:  st.winningTrades,
		LosingTrades:   st.losingTrades,
		EquityCurve:    equityCurve,
		TradeLog:       tradeLog,
		Summary:        formatSummary(ticker, finalEquity, totalPnL, totalReturn, sharpe, st.maxDrawdown, winRate, st.tradeID, st.winningTrades, st.losingTrades),
	}
}

// loadPriceData reads {dataDir}/{ticker}.csv and returns ordered price bars.
func loadPriceData(ticker string, dataDir string) ([]priceBar, error) {
	path := filepath.Join(dataDir, ticker+".csv")
	f, err := os.Open(path) // #nosec G304
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
		return nil, fmt.Errorf("load price data: CSV has no data rows (header only or empty)")
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

// RunMultiAssetSimulation executes a multi-symbol backtest with a unified
// capital pool. It loads price data for all symbols, aligns dates across
// assets (forward-filling missing prices), replays signals, and returns
// a combined equity curve reflecting total portfolio value.
//
// Execution model:
//   - Load CSV data for each symbol into map[string][]priceBar.
//   - Compute aligned date set (union of all symbol dates).
//   - For each aligned date: execute matching signals → mark-to-market all positions → record equity.
//   - If a symbol lacks data on a given date, use last valid price + "stale_price" quality flag.
//
// Args:
//   - ctx: context for cancellation.
//   - symbols: list of symbols to include in the portfolio.
//   - dataDir: directory containing {symbol}.csv files.
//   - signals: trade signals (date-matched; Symbol field is optional — applies to first symbol if empty).
//
// Returns:
//   - populated MultiAssetResult with combined metrics and per-symbol positions.
//   - error if data loading or execution fails.
//
//nolint:gocyclo // Complex multi-asset simulation with portfolio management
func (s *MultiAssetSimulator) RunMultiAssetSimulation(
	ctx context.Context,
	symbols []string,
	dataDir string,
	signals []TradeSignal,
) (*MultiAssetResult, error) {
	// Load price data for all symbols.
	allBars := make(map[string][]priceBar, len(symbols))
	for _, sym := range symbols {
		bars, err := loadPriceData(sym, dataDir)
		if err != nil {
			return nil, fmt.Errorf("load %s data: %w", sym, err)
		}
		allBars[sym] = bars
	}

	// Build aligned date set (union of all dates).
	dateSet := make(map[time.Time]bool)
	for _, bars := range allBars {
		for _, b := range bars {
			dateSet[b.Date] = true
		}
	}
	alignedDates := make([]time.Time, 0, len(dateSet))
	for d := range dateSet {
		alignedDates = append(alignedDates, d)
	}
	sort.Slice(alignedDates, func(i, j int) bool {
		return alignedDates[i].Before(alignedDates[j])
	})

	// Index signals by date.
	signalIndex := make(map[string][]TradeSignal, len(signals))
	for _, sig := range signals {
		key := sig.Date.Format("2006-01-02")
		signalIndex[key] = append(signalIndex[key], sig)
	}

	// Track per-symbol state and last-known prices.
	states := make(map[string]*multiAssetSymState, len(symbols))
	lastPrices := make(map[string]float64, len(symbols))
	for _, sym := range symbols {
		states[sym] = &multiAssetSymState{}
	}

	cash := s.InitialCapital
	var equityCurve []EquityPoint
	var tradeLog []TradeRecord
	tradeID := 0
	winningTrades := 0
	losingTrades := 0
	totalClosed := 0
	var warnings []string
	peakEquity := s.InitialCapital
	maxDrawdown := 0.0
	var dailyReturns []float64
	prevEquity := s.InitialCapital

	for barIdx, dt := range alignedDates {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		dateKey := dt.Format("2006-01-02")

		// Update each symbol's current price from its bar data.
		currentPrices := make(map[string]float64)
		for _, sym := range symbols {
			bars := allBars[sym]
			price := lastPrices[sym] // default: forward-fill
			for _, b := range bars {
				if b.Date.Equal(dt) {
					price = b.Close
					break
				}
			}
			// Check if this is a forward-filled stale price.
			hasDataOnDate := false
			for _, b := range bars {
				if b.Date.Equal(dt) {
					hasDataOnDate = true
					break
				}
			}
			if !hasDataOnDate && lastPrices[sym] > 0 {
				warnings = append(warnings, fmt.Sprintf("%s on %s: using stale forward-filled price %.2f", sym, dateKey, price))
			}
			currentPrices[sym] = price
			lastPrices[sym] = price
		}

		// Execute matching signals.
		if sigs, ok := signalIndex[dateKey]; ok {
			for _, sig := range sigs {
				// Resolve the target symbol(s) for this signal. A non-empty
				// Symbol routes to that instrument only; an empty Symbol
				// broadcasts the signal to every symbol with a valid price.
				targets := resolveSignalTargets(sig.Symbol, symbols, currentPrices)
				for _, targetSym := range targets {
					price := currentPrices[targetSym]
					if price <= 0 {
						continue
					}
					ss := states[targetSym]
					exec, ok := s.executeMultiAssetSignal(sig, ss, price, cash)
					if !ok {
						continue
					}
					cash = exec.cash
					if exec.side != "" {
						tradeID++
						rec := TradeRecord{
							ID:        fmt.Sprintf("T%d", tradeID),
							Symbol:    targetSym,
							Side:      exec.side,
							Price:     price,
							Quantity:  exec.quantity,
							Timestamp: dt,
						}
						if exec.side == "sell" {
							rec.PnL = exec.pnl
							if exec.pnl >= 0 {
								winningTrades++
							} else {
								losingTrades++
							}
							totalClosed++
						}
						tradeLog = append(tradeLog, rec)
					}
				}
			}
		}

		// Mark-to-market: compute total exposure across all positions.
		exposure := 0.0
		positions := make(map[string]PositionInfo)
		for _, sym := range symbols {
			ss := states[sym]
			price := currentPrices[sym]
			posValue := ss.shares * price
			exposure += posValue

			at := s.AssetTypes[sym]
			if at == "" {
				at = AssetUSStock
			}
			qf := ""
			positions[sym] = PositionInfo{
				Symbol:      sym,
				AssetType:   at,
				Shares:      ss.shares,
				CostBasis:   ss.costBasis,
				LastPrice:   price,
				QualityFlag: qf,
			}
		}

		equity := cash + exposure

		if barIdx > 0 && prevEquity > 0 {
			dailyRet := (equity - prevEquity) / prevEquity
			dailyReturns = append(dailyReturns, dailyRet)
		}
		prevEquity = equity

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

		equityCurve = append(equityCurve, EquityPoint{
			Time:     dt,
			Equity:   equity,
			Cash:     cash,
			Exposure: exposure,
			Drawdown: dd,
		})
	}

	finalEquity := cash
	for _, sym := range symbols {
		ss := states[sym]
		finalEquity += ss.shares * lastPrices[sym]
	}
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

	return &MultiAssetResult{
		Symbols:        symbols,
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
		Positions: func() map[string]PositionInfo {
			// Rebuild final positions for output.
			out := make(map[string]PositionInfo)
			for _, sym := range symbols {
				ss := states[sym]
				at := s.AssetTypes[sym]
				if at == "" {
					at = AssetUSStock
				}
				out[sym] = PositionInfo{
					Symbol:    sym,
					AssetType: at,
					Shares:    ss.shares,
					CostBasis: ss.costBasis,
					LastPrice: lastPrices[sym],
				}
			}
			return out
		}(),
		EquityCurve: equityCurve,
		TradeLog:    tradeLog,
		Summary:     formatMultiSummary(symbols, finalEquity, totalPnL, totalReturn, sharpe, maxDrawdown, winRate, tradeID, winningTrades, losingTrades),
		Warnings:    warnings,
	}, nil
}

// signalExecResult captures the side effects of executing one signal against a
// single symbol's position state.
type signalExecResult struct {
	side     string // "buy", "sell", or "" for no trade
	quantity float64
	pnl      float64
	cash     float64
}

// multiAssetSymState tracks per-symbol holdings during a multi-asset backtest.
type multiAssetSymState struct {
	shares    float64
	costBasis float64
}

// resolveSignalTargets returns the ordered list of symbols a signal should be
// applied to. When sym is non-empty and matches a known symbol, only that
// symbol is returned. When sym is empty, the signal broadcasts to every symbol
// that currently has a positive price, preserving the input symbols order.
func resolveSignalTargets(sym string, symbols []string, currentPrices map[string]float64) []string {
	if sym != "" {
		for _, s := range symbols {
			if s == sym {
				return []string{s}
			}
		}
		return nil
	}
	out := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if currentPrices[s] > 0 {
			out = append(out, s)
		}
	}
	return out
}

// executeMultiAssetSignal applies one trade signal to a single symbol's state,
// returning the updated cash balance and trade-record details. Returns ok=false
// when the signal produces no trade (e.g. insufficient cash or shares).
func (s *MultiAssetSimulator) executeMultiAssetSignal(
	sig TradeSignal,
	ss *multiAssetSymState,
	price, cash float64,
) (signalExecResult, bool) {
	res := signalExecResult{cash: cash}
	switch sig.Action {
	case "BUY":
		investment := cash * s.PositionSize
		if investment <= 0 {
			return res, false
		}
		costPerShare := price * (1 + s.Commission)
		buyShares := math.Floor(investment / costPerShare)
		if buyShares <= 0 {
			return res, false
		}
		cost := buyShares * costPerShare
		totalCost := ss.costBasis*ss.shares + cost
		res.cash = cash - cost
		res.side = "buy"
		res.quantity = buyShares
		ss.shares += buyShares
		if ss.shares > 0 {
			ss.costBasis = totalCost / ss.shares
		}
		return res, true
	case "SELL":
		if ss.shares <= 0 {
			return res, false
		}
		sellPrice := price * (1 - s.Commission)
		proceeds := ss.shares * sellPrice
		res.pnl = proceeds - (ss.shares * ss.costBasis)
		res.cash = cash + proceeds
		res.side = "sell"
		res.quantity = ss.shares
		ss.shares = 0
		ss.costBasis = 0
		return res, true
	case "HOLD":
		// No action.
		return res, true
	default:
		return res, false
	}
}

// formatMultiSummary builds a human-readable summary string for multi-asset results.
func formatMultiSummary(
	symbols []string,
	finalEquity, totalPnL, totalReturn, sharpe, maxDD, winRate float64,
	totalTrades, wins, losses int,
) string {
	return fmt.Sprintf(
		"Multi-asset Backtest [%v]: PnL=%.2f (%.2f%%), Sharpe=%.2f, MaxDD=%.2f%%, WinRate=%.1f%%, Trades=%d (W:%d L:%d)",
		symbols, totalPnL, totalReturn, sharpe, maxDD*100, winRate*100,
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
	if err := os.MkdirAll(dir, 0o750); err != nil { // nosec G301
		return fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o600); err != nil { // nosec G306
		return fmt.Errorf("write result: %w", err)
	}
	return nil
}
