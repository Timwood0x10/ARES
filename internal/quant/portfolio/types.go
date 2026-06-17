// Package portfolio provides backtesting and portfolio simulation for
// quantitative trading strategies. It supports replaying trade signals over
// historical OHLCV data, computing performance metrics, and converting
// research decisions into executable signals.
package portfolio

import "time"

// EquityPoint is a timestamped portfolio value snapshot.
type EquityPoint struct {
	Time     time.Time `json:"time"`
	Equity   float64   `json:"equity"`
	Cash     float64   `json:"cash"`
	Exposure float64   `json:"exposure"`
	Drawdown float64   `json:"drawdown"`
}

// TradeRecord is a single executed trade with fill details.
type TradeRecord struct {
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"`
	Price     float64   `json:"price"`
	Quantity  float64   `json:"quantity"`
	Timestamp time.Time `json:"timestamp"`
	PnL       float64   `json:"pnl,omitempty"`
}

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
	Ticker         string        `json:"ticker"`
	InitialCapital float64       `json:"initial_capital"`
	FinalEquity    float64       `json:"final_equity"`
	TotalPnL       float64       `json:"total_pnl"`
	TotalReturn    float64       `json:"total_return"` // percentage
	SharpeRatio    float64       `json:"sharpe_ratio"`
	MaxDrawdown    float64       `json:"max_drawdown"` // positive fraction
	WinRate        float64       `json:"win_rate"`     // 0–1
	TotalTrades    int           `json:"total_trades"`
	WinningTrades  int           `json:"winning_trades"`
	LosingTrades   int           `json:"losing_trades"`
	EquityCurve    []EquityPoint `json:"equity_curve"`
	TradeLog       []TradeRecord `json:"trade_log"`
	Summary        string        `json:"summary"`
}

// priceBar is an internal representation of one row from the price CSV.
type priceBar struct {
	Date   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}
