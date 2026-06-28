package marketmaking

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// MMBacktestResult holds market-making specific backtest results.
type MMBacktestResult struct {
	RealizedPnL      float64         `json:"realized_pnl"`
	UnrealizedPnL    float64         `json:"unrealized_pnl"`
	TotalPnL         float64         `json:"total_pnl"`
	FillRate         float64         `json:"fill_rate"`
	SpreadCapture    float64         `json:"spread_capture"`
	AdverseSelection float64         `json:"adverse_selection"`
	MaxDrawdown      float64         `json:"max_drawdown"`
	TotalQuotes      int             `json:"total_quotes"`
	TotalFills       int             `json:"total_fills"`
	EquityCurve      []MMEquityPoint `json:"equity_curve"`
	TradeLog         []MMTradeRecord `json:"trade_log"`
	Duration         time.Duration   `json:"duration"`
	Warnings         []string        `json:"warnings,omitempty"`
}

// MMEquityPoint represents a snapshot of portfolio equity during a market-making backtest.
type MMEquityPoint struct {
	Time     time.Time `json:"time"`
	Equity   float64   `json:"equity"`
	Cash     float64   `json:"cash"`
	Exposure float64   `json:"exposure"`
	Drawdown float64   `json:"drawdown"`
}

// MMTradeRecord represents a single executed trade within the market-making backtest.
type MMTradeRecord struct {
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"`
	Price     float64   `json:"price"`
	Quantity  float64   `json:"quantity"`
	Timestamp time.Time `json:"timestamp"`
	PnL       float64   `json:"pnl"`
}

// MMBacktestRunner executes event-driven market-making backtests.
// It replays MarketDataEvents through the QuoteEngine and simulates fills.
type MMBacktestRunner struct {
	Engine      *QuoteEngine
	InitialCash float64
	Commission  float64
	Slippage    float64
}

// NewMMBacktestRunner creates a new market-making backtest runner.
//
// Args:
//
//	engine - the quote engine to use for generating quotes.
//	initialCash - starting capital for the backtest.
//	commission - per-trade commission rate (e.g., 0.001 for 0.1%).
//	slippage - per-trade slippage fraction (e.g., 0.0005 for 0.05%).
//
// Returns:
//
//	a configured MMBacktestRunner, or an error if parameters are invalid.
func NewMMBacktestRunner(engine *QuoteEngine, initialCash, commission, slippage float64) (*MMBacktestRunner, error) {
	if engine == nil {
		return nil, fmt.Errorf("quote engine must not be nil")
	}
	if initialCash <= 0 {
		return nil, fmt.Errorf("initial cash must be > 0, got %f", initialCash)
	}
	if commission < 0 {
		return nil, fmt.Errorf("commission must be >= 0, got %f", commission)
	}
	if slippage < 0 {
		return nil, fmt.Errorf("slippage must be >= 0, got %f", slippage)
	}
	return &MMBacktestRunner{
		Engine:      engine,
		InitialCash: initialCash,
		Commission:  commission,
		Slippage:    slippage,
	}, nil
}

// Run executes the backtest by replaying market data events in order.
// The replay logic is deterministic — given the same input events and engine,
// it produces identical output.
//
// Replay algorithm:
//  1. Initialize Inventory with initial cash.
//  2. For each MarketDataEvent (sorted by time):
//     a. Skip if IsStale (record warning).
//     b. Update mark-to-market price in inventory.
//     c. Generate quote via QuoteEngine.GenerateQuote().
//     d. Simulate fill: if mid price crosses bid/ask, fill at quote price.
//     e. Update inventory on fill (deduct commission + slippage).
//     f. Record equity curve point.
//  3. Compute summary metrics.
//
// Args:
//
//	ctx - operation context supporting cancellation.
//	events - sorted slice of MarketDataEvents to replay.
//
// Returns:
//
//	the complete MMBacktestResult with all metrics, or an error.
func (r *MMBacktestRunner) Run(ctx context.Context, events []MarketDataEvent) (*MMBacktestResult, error) {
	result := &MMBacktestResult{
		TradeLog:    make([]MMTradeRecord, 0),
		EquityCurve: make([]MMEquityPoint, 0),
		Warnings:    make([]string, 0),
	}

	if len(events) == 0 {
		return result, nil
	}

	inv := NewInventory(r.InitialCash)

	var (
		mu               sync.Mutex
		totalQuotes      int
		totalFills       int
		spreadCapture    float64
		adverseSelection float64
		peakEquity       = r.InitialCash
		maxDrawdown      float64
		tradeID          int
		prevMid          float64
	)

	for _, evt := range events {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Step a: Check data freshness.
		if evt.IsStale {
			mu.Lock()
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("stale data at %s for symbol %s", evt.Timestamp, evt.Symbol))
			mu.Unlock()
			inv.UpdateMarkToMarket(map[string]float64{evt.Symbol: evt.MidPrice})
			r.recordEquity(inv, evt.Timestamp, peakEquity, &mu, result)
			continue
		}

		// Step b: Update mark-to-market.
		mid := evt.MidPrice
		inv.UpdateMarkToMarket(map[string]float64{evt.Symbol: mid})

		// Step c: Generate quote using the T4.1/T4.2 engine interface.
		risk := NewRiskSnapshot(inv.NetInventoryValue(), r.Engine.MaxInventory, nil)
		qd, err := r.Engine.GenerateQuote(ctx, &evt, inv, risk)
		if err != nil {
			r.recordEquity(inv, evt.Timestamp, peakEquity, &mu, result)
			continue
		}
		totalQuotes++

		// Step d: Simulate fill — mid crossing bid/ask triggers fill at quote price.
		fills := r.simulateFill(qd, mid, evt.Timestamp)

		// Step e: Process fills into inventory.
		for _, fill := range fills {
			processFill(inv, fill, r.Commission, r.Slippage)
			totalFills++

			fillSpreadIncome := (qd.AskPrice - qd.BidPrice) * fill.Quantity / 2
			spreadCapture += fillSpreadIncome

			if prevMid > 0 && fill.Side == "buy" && mid < prevMid {
				adverseSelection += math.Abs(prevMid-mid) * fill.Quantity
			} else if prevMid > 0 && fill.Side == "sell" && mid > prevMid {
				adverseSelection += math.Abs(mid-prevMid) * fill.Quantity
			}

			tradeID++
			tradePnL := 0.0
			if fill.Side == "sell" {
				pos := inv.GetPosition(evt.Symbol)
				if pos != nil && pos.AvgEntryPrice > 0 {
					tradePnL = (fill.Price - pos.AvgEntryPrice) * fill.Quantity
					tradePnL -= fill.Price * fill.Quantity * r.Commission
				}
			}
			mu.Lock()
			result.TradeLog = append(result.TradeLog, MMTradeRecord{
				ID:        fmt.Sprintf("mm-%d", tradeID),
				Symbol:    fill.Symbol,
				Side:      fill.Side,
				Price:     fill.Price,
				Quantity:  fill.Quantity,
				Timestamp: fill.Timestamp,
				PnL:       tradePnL,
			})
			mu.Unlock()
		}

		// Step f: Record equity curve point.
		equity := inv.TotalEquity()
		if equity > peakEquity {
			peakEquity = equity
		}
		dd := peakEquity - equity
		if dd < 0 {
			dd = 0
		}
		if dd > maxDrawdown {
			maxDrawdown = dd
		}

		r.recordEquityWithValues(inv, evt.Timestamp, equity, dd, &mu, result)
		prevMid = mid
	}

	fillRate := 0.0
	if totalQuotes > 0 {
		fillRate = float64(totalFills) / float64(totalQuotes)
	}

	result.RealizedPnL = inv.Cash - r.InitialCash
	result.UnrealizedPnL = inv.UnrealizedPnL
	result.TotalPnL = result.RealizedPnL + result.UnrealizedPnL
	result.FillRate = fillRate
	result.SpreadCapture = spreadCapture
	result.AdverseSelection = adverseSelection
	result.MaxDrawdown = maxDrawdown
	result.TotalQuotes = totalQuotes
	result.TotalFills = totalFills

	if len(events) > 0 {
		result.Duration = events[len(events)-1].Timestamp.Sub(events[0].Timestamp)
	}

	return result, nil
}

// simulateFill determines whether a fill occurs based on price movement.
func (r *MMBacktestRunner) simulateFill(qd *Quote, mid float64, ts time.Time) []*Fill {
	var fills []*Fill

	if mid <= qd.BidPrice {
		fills = append(fills, &Fill{
			Symbol: qd.Symbol, Side: "buy",
			Price: qd.BidPrice, Quantity: qd.BidSize, Timestamp: ts,
		})
	}
	if mid >= qd.AskPrice {
		fills = append(fills, &Fill{
			Symbol: qd.Symbol, Side: "sell",
			Price: qd.AskPrice, Quantity: qd.AskSize, Timestamp: ts,
		})
	}
	return fills
}

// processFill updates inventory state after a fill event (standalone helper).
func processFill(inv *Inventory, fill *Fill, commission, slippage float64) {
	if inv == nil || fill == nil {
		return
	}

	fillValue := fill.Price * fill.Quantity
	fee := fillValue * commission
	slippageCost := fillValue * slippage

	if fill.Side == "buy" {
		inv.Cash -= (fillValue + fee + slippageCost)
	} else {
		inv.Cash += (fillValue - fee - slippageCost)
	}

	pos, exists := inv.Positions[fill.Symbol]
	if !exists {
		pos = &Position{Symbol: fill.Symbol}
		inv.Positions[fill.Symbol] = pos
	}

	oldQty := pos.Quantity
	if fill.Side == "buy" {
		pos.Quantity += fill.Quantity
	} else {
		pos.Quantity -= fill.Quantity
	}

	if oldQty == 0 {
		pos.AvgEntryPrice = fill.Price
	} else if oldQty*pos.Quantity > 0 {
		totalValue := math.Abs(oldQty)*pos.AvgEntryPrice + fill.Quantity*fill.Price
		pos.AvgEntryPrice = totalValue / math.Abs(pos.Quantity)
	} else {
		pos.AvgEntryPrice = fill.Price
	}

	pos.CurrentPrice = fill.Price
	pos.UnrealizedPnL = (fill.Price - pos.AvgEntryPrice) * pos.Quantity
	inv.LastUpdated = fill.Timestamp
}

func (r *MMBacktestRunner) recordEquity(inv *Inventory, ts time.Time, peakEquity float64, mu *sync.Mutex, result *MMBacktestResult) {
	equity := inv.TotalEquity()
	dd := peakEquity - equity
	if dd < 0 {
		dd = 0
	}
	r.recordEquityWithValues(inv, ts, equity, dd, mu, result)
}

func (r *MMBacktestRunner) recordEquityWithValues(inv *Inventory, ts time.Time, equity, drawdown float64, mu *sync.Mutex, result *MMBacktestResult) {
	mu.Lock()
	defer mu.Unlock()

	exposure := 0.0
	for _, pos := range inv.Positions {
		exposure += math.Abs(pos.Quantity * pos.CurrentPrice)
	}

	result.EquityCurve = append(result.EquityCurve, MMEquityPoint{
		Time:     ts,
		Equity:   equity,
		Cash:     inv.Cash,
		Exposure: exposure,
		Drawdown: drawdown,
	})
}
