// Package market provides market data types and internal data source clients
// for quantitative trading. Exposes data through MCP Tool wrappers in the
// parent quant package, not directly.
package market

import (
	"time"
)

// ─── Core Types ────────────────────────────────────────────

// Candle represents a single OHLCV data point.
type Candle struct {
	Ticker string    `json:"ticker"`
	Date   time.Time `json:"date"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume int64     `json:"volume"`
}

// Quote represents a real-time or latest quote for a security.
type Quote struct {
	Ticker    string    `json:"ticker"`
	Price     float64   `json:"price"`
	Change    float64   `json:"change"`
	ChangePct float64   `json:"change_pct"`
	Volume    int64     `json:"volume"`
	Time      time.Time `json:"time"`
}

// TimeSeries is a slice of Candle with query metadata.
type TimeSeries struct {
	Ticker string    `json:"ticker"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Bars   []Candle  `json:"bars"`
}

// Resolution defines the bar interval.
type Resolution string

const (
	Res1m  Resolution = "1m"
	Res5m  Resolution = "5m"
	Res15m Resolution = "15m"
	Res1h  Resolution = "1h"
	Res1d  Resolution = "1d"
	Res1w  Resolution = "1w"
)

// ─── Predicition Market ────────────────────────────────────

// Market represents a Polymarket prediction market.
type Market struct {
	ID         string  `json:"id"`
	Question   string  `json:"question"`
	YesPrice   float64 `json:"yes_price"` // 0.0 - 1.0
	NoPrice    float64 `json:"no_price"`  // 0.0 - 1.0
	Volume     float64 `json:"volume"`
	EndDate    string  `json:"end_date"`
	Resolution string  `json:"resolution,omitempty"` // "YES" / "NO" / ""
}

// ─── Feed Interface ────────────────────────────────────────

// Feed provides a unified interface for market data sources.
type Feed interface {
	// Name returns the data source identifier (e.g. "yahoo", "csv").
	Name() string

	// Candles fetches historical OHLCV data for a ticker.
	Candles(ticker string, start, end time.Time, res Resolution) (TimeSeries, error)

	// Quote fetches the latest quote for a ticker.
	Quote(ticker string) (Quote, error)

	// Markets returns prediction markets matching a query (Polymarket only).
	Markets(query string) ([]Market, error)
}
