package dataflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"goagentx/internal/quant/market"
)

// Vendor is the interface that data source implementations must satisfy.
// All methods are safe for concurrent use.
type Vendor interface {
	// Name returns the vendor identifier (e.g. "yahoo", "alpha_vantage").
	Name() string

	// Candles fetches historical OHLCV data for a symbol over the given number of days.
	Candles(ctx context.Context, symbol string, days int) ([]market.Candle, error)

	// Quote fetches the latest quote for a symbol.
	Quote(ctx context.Context, symbol string) (*market.Quote, error)

	// Available reports whether the vendor is currently operational.
	Available() bool
}

// RouterConfig defines which vendors to use for each data category.
type RouterConfig struct {
	CoreStockAPIs       []string // e.g., ["yahoo", "alpha_vantage"]
	TechnicalIndicators []string // e.g., ["yahoo"]
	Fundamentals        []string // e.g., ["alpha_vantage", "yahoo"]
	News                []string // e.g., ["yahoo", "alpha_vantage"]
	Macro               []string // e.g., ["fred"]
	PredictionMarkets   []string // e.g., ["polymarket"]
}

// VendorResult holds the result of a single vendor call, including timing metadata.
type VendorResult struct {
	Data     interface{}
	Vendor   string
	Duration time.Duration
	Error    error
}

// VendorArgs encapsulates arguments for a routed vendor call.
type VendorArgs struct {
	Symbol string
	Days   int
	Method string // "candles" | "quote"
}

// VendorRouter routes data requests to configured vendors only.
// It does NOT silently fall back to unconfigured sources — if all configured
// vendors fail, it returns ErrAllVendorsFailed with a NoMarketDataError.
type VendorRouter struct {
	vendors map[string]Vendor
	config  *RouterConfig
	mu      sync.RWMutex
}

// NewVendorRouter creates a new VendorRouter with the given configuration.
func NewVendorRouter(cfg *RouterConfig) *VendorRouter {
	return &VendorRouter{
		vendors: make(map[string]Vendor),
		config:  cfg,
	}
}

// Register adds a vendor to the router. If a vendor with the same name already
// exists, it is replaced.
func (r *VendorRouter) Register(vendor Vendor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vendors[vendor.Name()] = vendor
}

// Route executes a data request through the configured vendor chain for the
// given method category. It tries each vendor in order; the first successful
// result is returned. If all vendors fail, it wraps errors into
// ErrAllVendorsFailed with a NoMarketDataError containing "Do not estimate".
func (r *VendorRouter) Route(ctx context.Context, method string, args VendorArgs) (VendorResult, error) {
	validMethods := map[string]bool{
		"candles": true, "quote": true, "technical_indicators": true,
		"fundamentals": true, "news": true, "macro": true, "prediction_markets": true,
	}
	if !validMethods[method] && method != "" {
		return VendorResult{}, fmt.Errorf("unknown route method: %q", method)
	}

	chain := r.vendorChain(method)
	if len(chain) == 0 {
		return VendorResult{}, fmt.Errorf("%w: no vendors configured for method %q", ErrAllVendorsFailed, method)
	}

	var lastErr error
	for _, name := range chain {
		r.mu.RLock()
		vendor, ok := r.vendors[name]
		r.mu.RUnlock()

		if !ok || !vendor.Available() {
			lastErr = fmt.Errorf("vendor %q not registered or unavailable", name)
			continue
		}

		start := time.Now()
		var result VendorResult

		switch args.Method {
		case "candles":
			data, err := vendor.Candles(ctx, args.Symbol, args.Days)
			result = VendorResult{Data: data, Vendor: name, Duration: time.Since(start), Error: err}
		case "quote":
			data, err := vendor.Quote(ctx, args.Symbol)
			result = VendorResult{Data: data, Vendor: name, Duration: time.Since(start), Error: err}
		default:
			return VendorResult{}, fmt.Errorf("unknown route method: %q", args.Method)
		}

		if result.Error == nil {
			return result, nil
		}
		lastErr = result.Error
	}

	noDataErr := &NoMarketDataError{
		Symbol:   args.Symbol,
		Vendor:   "all",
		DataType: args.Method,
		Message:  fmt.Sprintf("all %d vendors failed", len(chain)),
	}
	if lastErr != nil {
		noDataErr.Message = fmt.Sprintf("%s: last error: %v", noDataErr.Message, lastErr)
	}
	return VendorResult{Error: noDataErr}, fmt.Errorf("%w: %w", ErrAllVendorsFailed, noDataErr)
}

// vendorChain returns the ordered list of vendor names for a given method category.
func (r *VendorRouter) vendorChain(method string) []string {
	if r.config == nil {
		return nil
	}
	switch method {
	case "candles", "quote":
		return r.config.CoreStockAPIs
	case "technical_indicators":
		return r.config.TechnicalIndicators
	case "fundamentals":
		return r.config.Fundamentals
	case "news":
		return r.config.News
	case "macro":
		return r.config.Macro
	case "prediction_markets":
		return r.config.PredictionMarkets
	default:
		return nil
	}
}
