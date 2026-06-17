package marketmaking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"goagentx/internal/quant/portfolio"
)

type BacktestRunner interface {
	Run(ctx context.Context, req *BacktestRequest) (*BacktestResponse, error)
}

type DefaultBacktestRunner struct {
	dataDir string
}

func NewDefaultBacktestRunner() *DefaultBacktestRunner {
	return &DefaultBacktestRunner{}
}

func NewDefaultBacktestRunnerWithDataDir(dataDir string) *DefaultBacktestRunner {
	return &DefaultBacktestRunner{dataDir: dataDir}
}

func (r *DefaultBacktestRunner) Run(ctx context.Context, req *BacktestRequest) (*BacktestResponse, error) {
	if req == nil {
		return nil, errNilRequest
	}
	if len(req.Symbols) == 0 {
		return nil, errNoSymbols
	}
	if req.InitialCapital <= 0 {
		return nil, errInvalidCapital
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	dataDir := req.DataDir
	if dataDir == "" {
		dataDir = r.dataDir
	}

	var aggregatedResponse *BacktestResponse

	for _, symbol := range req.Symbols {
		resp, err := r.runSingleSymbol(ctx, symbol, req, dataDir)
		if err != nil {
			return nil, fmt.Errorf("backtest %s: %w", symbol, err)
		}
		if aggregatedResponse == nil {
			aggregatedResponse = resp
		} else {
			aggregatedResponse = mergeResponses(aggregatedResponse, resp)
		}
	}

	return aggregatedResponse, nil
}

func (r *DefaultBacktestRunner) runSingleSymbol(ctx context.Context, symbol string, req *BacktestRequest, dataDir string) (*BacktestResponse, error) {
	commission := req.Commission
	if commission == 0 {
		commission = 0.001
	}
	positionSize := req.PositionSize
	if positionSize == 0 {
		positionSize = 0.25
	}

	sim, err := portfolio.NewInvestmentSimulator(req.InitialCapital, positionSize, commission)
	if err != nil {
		return nil, fmt.Errorf("create simulator: %w", err)
	}

	signals := toPortfolioSignals(req.Signals)
	if len(signals) == 0 {
		signals = generateDefaultSignals(req.StartTime, req.EndTime)
	}
	result, err := sim.RunSimulation(ctx, symbol, dataDir, signals)
	if err != nil {
		return nil, fmt.Errorf("run simulation: %w", err)
	}

	return resultToResponse(result, req), nil
}

func resultToResponse(result *portfolio.SimulationResult, req *BacktestRequest) *BacktestResponse {
	equityCurve := make([]EquityPoint, len(result.EquityCurve))
	for i, ep := range result.EquityCurve {
		equityCurve[i] = EquityPoint{
			Time:     ep.Time,
			Equity:   ep.Equity,
			Cash:     ep.Cash,
			Exposure: ep.Exposure,
			Drawdown: ep.Drawdown,
		}
	}

	tradeLog := make([]TradeRecord, len(result.TradeLog))
	for i, tr := range result.TradeLog {
		tradeLog[i] = TradeRecord{
			ID:        tr.ID,
			Symbol:    tr.Symbol,
			Side:      tr.Side,
			Price:     tr.Price,
			Quantity:  tr.Quantity,
			Timestamp: tr.Timestamp,
			PnL:       tr.PnL,
		}
	}

	return &BacktestResponse{
		Request:       req,
		TotalPnL:      result.TotalPnL,
		TotalReturn:   result.TotalReturn,
		SharpeRatio:   result.SharpeRatio,
		MaxDrawdown:   result.MaxDrawdown,
		TotalTrades:   result.TotalTrades,
		WinRate:       result.WinRate,
		EquityCurve:   equityCurve,
		TradeLog:      tradeLog,
		Summary:       result.Summary,
		WinningTrades: result.WinningTrades,
		LosingTrades:  result.LosingTrades,
	}
}

func generateDefaultSignals(from, to time.Time) []portfolio.TradeSignal {
	start := from
	if start.IsZero() {
		start = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	end := to
	if end.IsZero() {
		end = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	var signals []portfolio.TradeSignal
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		signals = append(signals, portfolio.TradeSignal{
			Date:       d,
			Action:     "BUY",
			Reason:     "Daily buy signal",
			Confidence: 0.5,
		})
	}
	return signals
}

func mergeResponses(a, b *BacktestResponse) *BacktestResponse {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &BacktestResponse{
		Request:       a.Request,
		TotalPnL:      a.TotalPnL + b.TotalPnL,
		TotalReturn:   (a.TotalReturn + b.TotalReturn) / 2,
		SharpeRatio:   (a.SharpeRatio + b.SharpeRatio) / 2,
		MaxDrawdown:   max(a.MaxDrawdown, b.MaxDrawdown),
		TotalTrades:   a.TotalTrades + b.TotalTrades,
		WinRate:       (a.WinRate + b.WinRate) / 2,
		EquityCurve:   append(a.EquityCurve, b.EquityCurve...),
		TradeLog:      append(a.TradeLog, b.TradeLog...),
		Summary:       fmt.Sprintf("%s; %s", a.Summary, b.Summary),
		WinningTrades: a.WinningTrades + b.WinningTrades,
		LosingTrades:  a.LosingTrades + b.LosingTrades,
	}
}

var (
	errNilRequest     = errors.New("backtest request is nil")
	errNoSymbols      = errors.New("no symbols in backtest request")
	errInvalidCapital = errors.New("initial capital must be > 0")
)

// toPortfolioSignals converts public API TradeSignal slices to internal
// portfolio.TradeSignal slices for use by the simulator.
func toPortfolioSignals(signals []TradeSignal) []portfolio.TradeSignal {
	if len(signals) == 0 {
		return nil
	}
	result := make([]portfolio.TradeSignal, len(signals))
	for i, s := range signals {
		result[i] = portfolio.TradeSignal{
			Date:       s.Date,
			Action:     s.Action,
			Reason:     s.Reason,
			Confidence: s.Confidence,
		}
	}
	return result
}
