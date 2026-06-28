package marketmakingapi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewDefaultPaperTrader tests constructor.
func TestNewDefaultPaperTrader(t *testing.T) {
	trader := NewDefaultPaperTrader()
	require.NotNil(t, trader)
}

// TestPaperTrader_Start_NilRequest tests nil request handling.
func TestPaperTrader_Start_NilRequest(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Start(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestPaperTrader_Start_NoSymbols tests empty symbols.
func TestPaperTrader_Start_NoSymbols(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{},
		InitialCapital: 100000.0,
		Duration:       time.Hour,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no symbols")
	require.Nil(t, resp)
}

// TestPaperTrader_Start_InvalidCapital tests zero capital.
func TestPaperTrader_Start_InvalidCapital(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Start(context.Background(), &PaperTradeRequest{
		Symbols:        []string{"BTCUSDT"},
		InitialCapital: 0,
		Duration:       time.Hour,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "capital")
	require.Nil(t, resp)
}

// TestPaperTrader_Start_ValidRequest tests that start creates a session and returns it.
func TestPaperTrader_Start_ValidRequest(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{"BTCUSDT", "ETHUSDT"},
		InitialCapital: 100000.0,
		Duration:       2 * time.Hour,
	}
	resp, err := trader.Start(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.SessionID)
	require.Equal(t, 100000.0, resp.Equity)
}

// TestPaperTrader_Status_EmptySessionID tests status with empty session ID.
func TestPaperTrader_Status_EmptySessionID(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Status(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID")
	require.Nil(t, resp)
}

// TestPaperTrader_Status_ValidSessionID tests that status returns session data for started session.
func TestPaperTrader_Status_ValidSessionID(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{"BTCUSDT"},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Status(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, created.SessionID, resp.SessionID)
}

// TestPaperTrader_Stop_EmptySessionID tests stop with empty session ID.
func TestPaperTrader_Stop_EmptySessionID(t *testing.T) {
	trader := NewDefaultPaperTrader()
	resp, err := trader.Stop(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID")
	require.Nil(t, resp)
}

// TestPaperTrader_Stop_ValidSessionID tests that stop returns session data and removes it.
func TestPaperTrader_Stop_ValidSessionID(t *testing.T) {
	trader := NewDefaultPaperTrader()
	req := &PaperTradeRequest{
		Symbols:        []string{"BTCUSDT"},
		InitialCapital: 50000.0,
		Duration:       time.Hour,
	}
	created, err := trader.Start(context.Background(), req)
	require.NoError(t, err)

	resp, err := trader.Stop(context.Background(), created.SessionID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, created.SessionID, resp.SessionID)

	// Verify session was removed
	_, err = trader.Status(context.Background(), created.SessionID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

// TestPaperTrader_CancelledContext tests context cancellation on Start.
func TestPaperTrader_CancelledContext(t *testing.T) {
	trader := NewDefaultPaperTrader()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := trader.Start(ctx, &PaperTradeRequest{
		Symbols:        []string{"BTCUSDT"},
		InitialCapital: 10000.0,
		Duration:       time.Minute,
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, resp)
}
