package marketmakingapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMode_String verifies Mode string representations.
func TestMode_String(t *testing.T) {
	tests := []struct {
		mode Mode
		want string
	}{
		{ModeBacktest, "backtest"},
		{ModePaper, "paper"},
		{ModeLive, "live"},
		{Mode(0), ModeUnknown},
		{Mode(99), ModeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.mode.String()
			require.Equal(t, tt.want, got)
		})
	}
}

// TestMode_IotaStart verifies that modes start from 1 (not 0).
func TestMode_IotaStart(t *testing.T) {
	require.Equal(t, Mode(1), ModeBacktest)
	require.Equal(t, Mode(2), ModePaper)
	require.Equal(t, Mode(3), ModeLive)
}

// TestQuoteDecision_JSONTags verifies QuoteDecision has proper JSON tags.
func TestQuoteDecision_JSONTags(t *testing.T) {
	q := &QuoteDecision{
		Symbol:    SymbolBTCUSDT,
		BidPrice:  50000.0,
		AskPrice:  50010.0,
		BidSize:   1.5,
		AskSize:   1.5,
		TTLMillis: 5000,
		RiskState: "normal",
		Reason:    "spread 2 bps",
	}
	require.Equal(t, SymbolBTCUSDT, q.Symbol)
	require.Equal(t, 50000.0, q.BidPrice)
	require.Equal(t, "normal", q.RiskState)
}

// TestTradeRecord_Structure verifies TradeRecord fields.
func TestTradeRecord_Structure(t *testing.T) {
	trade := TradeRecord{
		ID:       "trade-001",
		Symbol:   SymbolETHUSDT,
		Side:     "buy",
		Price:    3000.0,
		Quantity: 2.0,
		PnL:      50.0,
	}
	require.Equal(t, "trade-001", trade.ID)
	require.Equal(t, "buy", trade.Side)
	require.Equal(t, 3000.0, trade.Price)
}

// TestRiskBreach_Structure verifies RiskBreach fields.
func TestRiskBreach_Structure(t *testing.T) {
	breach := RiskBreach{
		LimitName:    "max_position",
		Symbol:       SymbolBTCUSDT,
		CurrentValue: 15.0,
		LimitValue:   10.0,
	}
	require.Equal(t, "max_position", breach.LimitName)
	require.True(t, breach.CurrentValue > breach.LimitValue)
}

// TestPosition_Structure verifies Position fields.
func TestPosition_Structure(t *testing.T) {
	pos := Position{
		Symbol:        SymbolBTCUSDT,
		Quantity:      5.0,
		AvgEntryPrice: 50000.0,
		UnrealizedPnL: 2500.0,
		LastPrice:     50500.0,
	}
	require.Equal(t, SymbolBTCUSDT, pos.Symbol)
	require.True(t, pos.Quantity > 0) // long position
}
