// Package dataflow provides anti-hallucination infrastructure for market data,
// including symbol normalization, vendor routing, data validation, and verified
// snapshot construction.
package dataflow

import (
	"fmt"
)

// ─── Sentinel Errors ────────────────────────────────────────

// ErrNoMarketData indicates no market data is available for the requested symbol.
var ErrNoMarketData = fmt.Errorf("no market data available")

// ErrSymbolEmpty indicates an empty symbol was provided.
var ErrSymbolEmpty = fmt.Errorf("symbol must not be empty")

// ErrStaleData indicates market data is too old relative to the analysis date.
var ErrStaleData = fmt.Errorf("market data is stale")

// ErrLookaheadData indicates future data was detected in backtest mode.
var ErrLookaheadData = fmt.Errorf("data from future detected in backtest mode")

// ErrAllVendorsFailed indicates every configured vendor returned an error.
var ErrAllVendorsFailed = fmt.Errorf("all data vendors failed")

// ErrInvalidSymbol indicates the symbol format is unrecognized.
var ErrInvalidSymbol = fmt.Errorf("invalid symbol format")

// NoMarketDataError is a typed error for no-data conditions that carries
// context about which symbol, vendor, and data type failed.
// Its Error() output includes "NO_DATA_AVAILABLE" and "Do not estimate"
// so that LLM prompts clearly signal that estimation or fabrication is forbidden.
type NoMarketDataError struct {
	Symbol   string
	Vendor   string
	DataType string
	Message  string
}

// Error implements the error interface with a machine-parseable sentinel message.
func (e *NoMarketDataError) Error() string {
	if e == nil {
		return ""
	}
	base := fmt.Sprintf(
		"NO_DATA_AVAILABLE: %s | Symbol: %s | Vendor: %s | DataType: %s",
		e.Message, e.Symbol, e.Vendor, e.DataType,
	)
	return base + ". Do not estimate or fabricate values."
}

// Unwrap supports errors.Is/As chaining.
func (e *NoMarketDataError) Unwrap() error {
	return ErrNoMarketData
}
