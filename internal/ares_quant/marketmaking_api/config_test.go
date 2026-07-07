package marketmakingapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDefaultConfig verifies that DefaultConfig produces a valid configuration.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	require.NotNil(t, cfg)
	require.NoError(t, cfg.Validate())
	require.Equal(t, []string{SymbolBTCUSDT}, cfg.Symbols)
	require.Equal(t, ModePaper, cfg.Mode)
	require.Equal(t, 10.0, cfg.RiskLimits.MaxPosition)
	require.Equal(t, "binance", cfg.DataSource.Vendor)
}

// TestValidate_NilConfig tests validation of nil config.
func TestValidate_NilConfig(t *testing.T) {
	var cfg *MarketMakingConfig
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil")
}

// TestValidate_NoSymbols tests validation when no symbols are configured.
func TestValidate_NoSymbols(t *testing.T) {
	cfg := &MarketMakingConfig{
		Symbols: []string{},
		Mode:    ModePaper,
		RiskLimits: RiskLimitConfig{
			MaxPosition:  10.0,
			MaxOrderSize: 1.0,
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no symbols")
}

// TestValidate_InvalidMode tests validation with an invalid mode value.
func TestValidate_InvalidMode(t *testing.T) {
	cfg := &MarketMakingConfig{
		Symbols: []string{SymbolBTCUSDT},
		Mode:    Mode(99),
		RiskLimits: RiskLimitConfig{
			MaxPosition:  10.0,
			MaxOrderSize: 1.0,
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid mode")
}

// TestValidate_ZeroMaxPosition tests validation with zero max position.
func TestValidate_ZeroMaxPosition(t *testing.T) {
	cfg := &MarketMakingConfig{
		Symbols: []string{SymbolBTCUSDT},
		Mode:    ModePaper,
		RiskLimits: RiskLimitConfig{
			MaxPosition:  0,
			MaxOrderSize: 1.0,
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "max_position")
}

// TestValidate_LiveModeNoEndpoint tests that live mode requires an endpoint.
func TestValidate_LiveModeNoEndpoint(t *testing.T) {
	cfg := &MarketMakingConfig{
		Symbols: []string{SymbolBTCUSDT},
		Mode:    ModeLive,
		RiskLimits: RiskLimitConfig{
			MaxPosition:  10.0,
			MaxOrderSize: 1.0,
		},
		ExecutionGateway: ExecutionGatewayConfig{
			Type:     ModeREST,
			Endpoint: "",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "endpoint required")
}

// TestValidate_LiveModeWithEndpoint tests that live mode passes with endpoint.
func TestValidate_LiveModeWithEndpoint(t *testing.T) {
	cfg := &MarketMakingConfig{
		Symbols: []string{SymbolBTCUSDT},
		Mode:    ModeLive,
		RiskLimits: RiskLimitConfig{
			MaxPosition:      10.0,
			MaxOrderSize:     1.0,
			MaxInventorySkew: 5.0,
		},
		ExecutionGateway: ExecutionGatewayConfig{
			Type:     ModeREST,
			Endpoint: "https://api.example.com",
		},
	}
	err := cfg.Validate()
	require.NoError(t, err)
}

// TestValidate_PaperModeNoEndpoint tests that paper mode does not require endpoint.
func TestValidate_PaperModeNoEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	err := cfg.Validate()
	require.NoError(t, err)
	require.Empty(t, cfg.ExecutionGateway.Endpoint)
}

// TestChaosFlagDefaults verifies chaos flags default to disabled.
func TestChaosFlagDefaults(t *testing.T) {
	cfg := DefaultConfig()
	require.False(t, cfg.ChaosFlags.EnableLatency)
	require.False(t, cfg.ChaosFlags.EnableReject)
	require.False(t, cfg.ChaosFlags.EnableStaleData)
}
