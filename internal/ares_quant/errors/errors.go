// Package errors provides shared sentinel errors for ares_quant sub-packages.
package errors

import "fmt"

// ErrNoMarketData indicates no market data is available for the requested symbol.
var ErrNoMarketData = fmt.Errorf("no market data available")
