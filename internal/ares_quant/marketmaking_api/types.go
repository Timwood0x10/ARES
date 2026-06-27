// Package marketmaking provides a domain-level facade for market-making and
// quantitative trading operations. It exposes stable interfaces for quoting,
// backtesting, paper trading, risk management, and chaos testing — without
// requiring callers to depend on internal/quant or other implementation details.
package marketmakingapi

import (
	"fmt"
	"time"
)

// Mode represents the operating mode of the market-making system.
type Mode int

const (
	// ModeBacktest runs the strategy against historical data.
	ModeBacktest Mode = iota + 1
	// ModePaper executes trades in simulation (no real orders).
	ModePaper
	// ModeLive sends real orders to the execution gateway.
	ModeLive
)

// String returns a human-readable representation of the mode.
func (m Mode) String() string {
	switch m {
	case ModeBacktest:
		return "backtest"
	case ModePaper:
		return "paper"
	case ModeLive:
		return "live"
	default:
		return "unknown"
	}
}

// QuoteDecision represents a two-sided quote produced by the quote engine.
type QuoteDecision struct {
	// Symbol is the trading instrument identifier.
	Symbol string `json:"symbol"`
	// BidPrice is the quoted bid price.
	BidPrice float64 `json:"bid_price"`
	// AskPrice is the quoted ask price.
	AskPrice float64 `json:"ask_price"`
	// BidSize is the quoted bid quantity.
	BidSize float64 `json:"bid_size"`
	// AskSize is the quoted ask quantity.
	AskSize float64 `json:"ask_size"`
	// TTLMillis is the time-to-live of this quote in milliseconds.
	TTLMillis int64 `json:"ttl_millis"`
	// RiskState describes the current risk assessment for this symbol.
	RiskState string `json:"risk_state"`
	// Reason provides a human-readable explanation for the quote decision.
	Reason string `json:"reason,omitempty"`
}

// Validate checks that all fields in the quote decision are within acceptable bounds.
// FIX: add boundary validation for TTLMillis per code rule 9 (NEVER assume inputs are valid).
//
// Returns:
//
//	nil if valid, or an error describing which field is invalid.
func (q *QuoteDecision) Validate() error {
	if q == nil {
		return fmt.Errorf("quote decision is nil")
	}
	if q.Symbol == "" {
		return fmt.Errorf("symbol must not be empty")
	}
	if q.TTLMillis <= 0 {
		return fmt.Errorf("ttl_millis must be > 0, got %d", q.TTLMillis)
	}
	if q.BidPrice <= 0 || q.AskPrice <= 0 {
		return fmt.Errorf("bid_price and ask_price must be > 0")
	}
	if q.BidPrice >= q.AskPrice {
		return fmt.Errorf("bid_price (%f) must be < ask_price (%f)", q.BidPrice, q.AskPrice)
	}
	if q.BidSize <= 0 || q.AskSize <= 0 {
		return fmt.Errorf("bid_size and ask_size must be > 0")
	}
	return nil
}

// BacktestRequest defines the parameters for a backtest run.
type BacktestRequest struct {
	// Symbols is the list of instruments to trade.
	Symbols []string `json:"symbols"`
	// StartTime is the inclusive start of the backtest window.
	StartTime time.Time `json:"start_time"`
	// EndTime is the exclusive end of the backtest window.
	EndTime time.Time `json:"end_time"`
	// InitialCapital is the starting capital in base currency.
	InitialCapital float64 `json:"initial_capital"`
	// Strategy identifies the trading strategy to use.
	// Supported: "buy_hold", "research_signal", "csv_signal".
	Strategy string `json:"strategy,omitempty"`
	// DataDir is the directory containing CSV market data files.
	DataDir string `json:"data_dir,omitempty"`
	// DataSource identifies the data vendor (e.g., "yahoo", "csv").
	DataSource string `json:"data_source,omitempty"`
	// Commission is the per-trade fee rate (e.g., 0.001 for 0.1%).
	Commission float64 `json:"commission,omitempty"`
	// Slippage is the assumed slippage fraction (e.g., 0.0005 for 0.05%).
	Slippage float64 `json:"slippage,omitempty"`
	// AssetTypes maps symbol -> asset type: "us_stock", "cn_stock", "crypto", "custom".
	AssetTypes map[string]string `json:"asset_types,omitempty"`
	// ConfigPath points to a strategy configuration file (optional).
	ConfigPath string `json:"config_path,omitempty"`
	// PositionSize is the fraction of capital per trade (0-1).
	PositionSize float64 `json:"position_size,omitempty"`
	// Signals carries pre-generated trade signals from an external source
	// (e.g., the research layer). When non-empty, the runner uses these instead
	// of generating default signals internally.
	Signals []TradeSignal `json:"signals,omitempty"`
}

// TradeSignal represents a time-based trading instruction for a backtest.
type TradeSignal struct {
	// Date is when this signal was generated.
	Date time.Time `json:"date"`
	// Action is the trade direction: "BUY", "SELL", or "HOLD".
	Action string `json:"action"`
	// Reason explains why this signal was generated.
	Reason string `json:"reason,omitempty"`
	// Confidence is the signal confidence level between 0 and 1.
	Confidence float64 `json:"confidence,omitempty"`
}

// BacktestResponse contains the results of a completed backtest.
type BacktestResponse struct {
	// Request echoes the original request parameters.
	Request *BacktestRequest `json:"request"`
	// TotalPnL is the total profit and loss over the backtest period.
	TotalPnL float64 `json:"total_pnl"`
	// TotalReturn is the percentage return over the backtest period.
	TotalReturn float64 `json:"total_return"`
	// SharpeRatio is the annualized Sharpe ratio of the strategy.
	SharpeRatio float64 `json:"sharpe_ratio"`
	// MaxDrawdown is the maximum observed drawdown as a positive fraction (0-1).
	MaxDrawdown float64 `json:"max_drawdown"`
	// TotalTrades is the number of trades executed during the backtest.
	TotalTrades int `json:"total_trades"`
	// WinRate is the fraction of profitable trades between 0 and 1.
	WinRate float64 `json:"win_rate"`
	// EquityCurve is the time series of portfolio equity across the backtest period.
	EquityCurve []EquityPoint `json:"equity_curve,omitempty"`
	// TradeLog contains per-trade details (optional, may be large).
	TradeLog []TradeRecord `json:"trade_log,omitempty"`
	// Summary provides a human-readable summary of the backtest results.
	Summary string `json:"summary,omitempty"`
	// Warnings lists any issues encountered during backtest execution.
	Warnings []string `json:"warnings,omitempty"`
	// WinningTrades is the count of profitable trades.
	WinningTrades int `json:"winning_trades,omitempty"`
	// LosingTrades is the count of unprofitable trades.
	LosingTrades int `json:"losing_trades,omitempty"`
}

// EquityPoint represents a snapshot of portfolio equity at a point in time.
// Used to construct equity curves for performance visualization.
type EquityPoint struct {
	// Time is the timestamp of this snapshot.
	Time time.Time `json:"time"`
	// Equity is the total portfolio value (cash + position market value).
	Equity float64 `json:"equity"`
	// Cash is the cash balance at this point.
	Cash float64 `json:"cash"`
	// Exposure is the gross market exposure (sum of absolute position values).
	Exposure float64 `json:"exposure"`
	// Drawdown is the peak-to-trough decline as a positive fraction (0-1).
	Drawdown float64 `json:"drawdown"`
}

// TradeRecord represents a single executed trade within a backtest or paper session.
type TradeRecord struct {
	// ID is the unique trade identifier.
	ID string `json:"id"`
	// Symbol is the instrument traded.
	Symbol string `json:"symbol"`
	// Side is "buy" or "sell".
	Side string `json:"side"`
	// Price is the execution price.
	Price float64 `json:"price"`
	// Quantity is the executed quantity.
	Quantity float64 `json:"quantity"`
	// Timestamp records when the trade occurred.
	Timestamp time.Time `json:"timestamp"`
	// PnL is the realized profit and loss for this trade.
	PnL float64 `json:"pnl"`
}

// PaperTradeRequest defines the parameters for a paper trading session.
type PaperTradeRequest struct {
	// Symbols is the list of instruments to trade.
	Symbols []string `json:"symbols"`
	// InitialCapital is the starting capital in base currency.
	InitialCapital float64 `json:"initial_capital"`
	// Duration is how long the paper trading session should run.
	Duration time.Duration `json:"duration"`
}

// PaperTradeResponse reports the state of an active or completed paper trade session.
type PaperTradeResponse struct {
	// SessionID uniquely identifies this paper trading session.
	SessionID string `json:"session_id"`
	// CurrentPnL is the unrealized + realized PnL so far.
	CurrentPnL float64 `json:"current_pnl"`
	// Equity is the current account equity.
	Equity float64 `json:"equity"`
	// Trades lists all trades executed in this session.
	Trades []TradeRecord `json:"trades"`
	// StartedAt marks when the session began.
	StartedAt time.Time `json:"started_at"`
}

// RiskReport summarizes the current risk exposure across all positions.
type RiskReport struct {
	// Timestamp when this report was generated.
	Timestamp time.Time `json:"timestamp"`
	// TotalExposure is the net position value across all symbols.
	TotalExposure float64 `json:"total_exposure"`
	// Utilization is the fraction of risk limits used (0–1).
	Utilization float64 `json:"utilization"`
	// Breaches lists any active limit breaches.
	Breaches []RiskBreach `json:"breaches,omitempty"`
	// Health indicates overall risk system health ("healthy", "warning", "critical").
	Health string `json:"health"`
}

// RiskBreach describes a single risk-limit violation.
type RiskBreach struct {
	// LimitName identifies which limit was breached (e.g., "max_position").
	LimitName string `json:"limit_name"`
	// Symbol is the instrument that triggered the breach.
	Symbol string `json:"symbol"`
	// CurrentValue is the current measured value.
	CurrentValue float64 `json:"current_value"`
	// LimitValue is the maximum allowed value.
	LimitValue float64 `json:"limit_value"`
}

// InventoryReport describes current inventory positions.
type InventoryReport struct {
	// Timestamp when this report was generated.
	Timestamp time.Time `json:"timestamp"`
	// Positions lists current holdings by symbol.
	Positions []Position `json:"positions"`
	// NetDelta is the aggregate delta exposure.
	NetDelta float64 `json:"net_delta"`
	// CashBalance is the available cash balance.
	CashBalance float64 `json:"cash_balance"`
}

// Position represents a single instrument position.
type Position struct {
	// Symbol is the instrument identifier.
	Symbol string `json:"symbol"`
	// Quantity is the signed position size (positive = long, negative = short).
	Quantity float64 `json:"quantity"`
	// AvgEntryPrice is the volume-weighted average entry price.
	AvgEntryPrice float64 `json:"avg_entry_price"`
	// UnrealizedPnL is the mark-to-market PnL for this position.
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	// LastPrice is the last known market price for this symbol.
	LastPrice float64 `json:"last_price"`
}
