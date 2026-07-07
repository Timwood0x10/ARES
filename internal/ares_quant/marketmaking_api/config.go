package marketmakingapi

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidConfig is returned when the configuration is nil or has invalid fields.
	ErrInvalidConfig = errors.New("invalid market making config")
	// ErrNoSymbols is returned when no trading symbols are specified.
	ErrNoSymbols = errors.New("no symbols configured")
	// ErrNotInitialized is returned when a required engine/manager has not been injected.
	ErrNotInitialized = errors.New("required component not initialized")
	// ErrNotImplemented is returned by skeleton/stub implementations to indicate
	// the feature has not been wired to a real backend yet. Callers can use
	// errors.Is(err, ErrNotImplemented) to detect this condition instead of
	// checking for zero-value results (code rule 4.4, 9).
	ErrNotImplemented = errors.New("feature not implemented")
)

// MarketMakingConfig holds all configuration for the market-making system.
type MarketMakingConfig struct {
	// Symbols is the list of instruments to trade.
	Symbols []string `json:"symbols"`
	// Mode determines the operating mode (backtest / paper / live).
	Mode Mode `json:"mode"`
	// RiskLimits defines position and order size constraints.
	RiskLimits RiskLimitConfig `json:"risk_limits"`
	// DataSource specifies where to read market data.
	DataSource DataSourceConfig `json:"data_source"`
	// ExecutionGateway defines how orders are sent (ignored in backtest mode).
	ExecutionGateway ExecutionGatewayConfig `json:"execution_gateway"`
	// ChaosFlags controls chaos engineering features for testing.
	ChaosFlags ChaosFlagConfig `json:"chaos_flags"`
}

// RiskLimitConfig defines risk management boundaries.
type RiskLimitConfig struct {
	// MaxPosition is the maximum absolute position size per symbol.
	MaxPosition float64 `json:"max_position"`
	// MaxOrderSize is the maximum quantity per single order.
	MaxOrderSize float64 `json:"max_order_size"`
	// MaxInventorySkew is the maximum allowed imbalance between long and short sides.
	MaxInventorySkew float64 `json:"max_inventory_skew"`
}

// DataSourceConfig describes the market data provider.
type DataSourceConfig struct {
	// Vendor identifies the data vendor name (e.g., "binance", "alphavantage").
	Vendor string `json:"vendor"`
	// SymbolFormat specifies how symbols are formatted for this vendor (e.g., "BTCUSDT").
	SymbolFormat string `json:"symbol_format"`
}

// ExecutionGatewayConfig describes the order execution endpoint.
type ExecutionGatewayConfig struct {
	// Type is the gateway type (e.g., "rest", "fix", "websocket").
	Type string `json:"type"`
	// Endpoint is the base URL or connection address for the gateway.
	Endpoint string `json:"endpoint"`
	// APIKey is the authentication key for the gateway.
	APIKey string `json:"api_key"`
}

// ChaosFlagConfig enables fault-injection scenarios for testing.
type ChaosFlagConfig struct {
	// EnableLatency injects random latency into quote generation.
	EnableLatency bool `json:"enable_latency"`
	// EnableReject randomly rejects a fraction of quotes to simulate failures.
	EnableReject bool `json:"enable_reject"`
	// EnableStaleData feeds stale market data to test data freshness handling.
	EnableStaleData bool `json:"enable_stale_data"`
}

// Validate checks that all required configuration fields are present and sane.
//
// Returns:
//
//	nil if valid, or an error describing which field is invalid.
func (c *MarketMakingConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: config is nil", ErrInvalidConfig)
	}
	if len(c.Symbols) == 0 {
		return fmt.Errorf("%w: %w", ErrInvalidConfig, ErrNoSymbols)
	}
	// Validate mode using enum comparison.
	switch c.Mode {
	case ModeBacktest, ModePaper, ModeLive:
		// valid
	default:
		return fmt.Errorf("%w: invalid mode %d", ErrInvalidConfig, c.Mode)
	}
	if c.RiskLimits.MaxPosition <= 0 {
		return fmt.Errorf("%w: risk_limits.max_position must be > 0", ErrInvalidConfig)
	}
	if c.RiskLimits.MaxOrderSize <= 0 {
		return fmt.Errorf("%w: risk_limits.max_order_size must be > 0", ErrInvalidConfig)
	}
	if c.Mode == ModeLive {
		if c.ExecutionGateway.Endpoint == "" {
			return fmt.Errorf("%w: execution_gateway.endpoint required in live mode", ErrInvalidConfig)
		}
	}
	return nil
}

// DefaultConfig returns a MarketMakingConfig with sensible defaults for paper
// trading on a single symbol. Callers should customize Symbols, Mode, and other
// fields before use.
//
// Returns:
//
//	config - a pre-populated configuration that passes Validate.
func DefaultConfig() *MarketMakingConfig {
	return &MarketMakingConfig{
		Symbols: []string{SymbolBTCUSDT},
		Mode:    ModePaper,
		RiskLimits: RiskLimitConfig{
			MaxPosition:      10.0,
			MaxOrderSize:     1.0,
			MaxInventorySkew: 5.0,
		},
		DataSource: DataSourceConfig{
			Vendor:       "binance",
			SymbolFormat: "{BASE}{QUOTE}",
		},
		ExecutionGateway: ExecutionGatewayConfig{
			Type:     ModeREST,
			Endpoint: "",
			APIKey:   "",
		},
		ChaosFlags: ChaosFlagConfig{
			EnableLatency:   false,
			EnableReject:    false,
			EnableStaleData: false,
		},
	}
}
