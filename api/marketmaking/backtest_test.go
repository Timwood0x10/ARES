package marketmaking

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewDefaultBacktestRunner tests constructor.
func TestNewDefaultBacktestRunner(t *testing.T) {
	runner := NewDefaultBacktestRunner()
	require.NotNil(t, runner)
}

// TestBacktestRunner_Run_NilRequest tests nil request handling.
func TestBacktestRunner_Run_NilRequest(t *testing.T) {
	runner := NewDefaultBacktestRunner()
	resp, err := runner.Run(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestBacktestRunner_Run_NoSymbols tests empty symbols handling.
func TestBacktestRunner_Run_NoSymbols(t *testing.T) {
	runner := NewDefaultBacktestRunner()
	resp, err := runner.Run(context.Background(), &BacktestRequest{
		Symbols:        []string{},
		StartTime:      time.Now().Add(-24 * time.Hour),
		EndTime:        time.Now(),
		InitialCapital: 100000.0,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no symbols")
	require.Nil(t, resp)
}

// TestBacktestRunner_Run_InvalidCapital tests zero/negative capital.
func TestBacktestRunner_Run_InvalidCapital(t *testing.T) {
	runner := NewDefaultBacktestRunner()
	resp, err := runner.Run(context.Background(), &BacktestRequest{
		Symbols:        []string{"BTCUSDT"},
		StartTime:      time.Now().Add(-24 * time.Hour),
		EndTime:        time.Now(),
		InitialCapital: 0,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "capital")
	require.Nil(t, resp)
}

// TestBacktestRunner_Run_ValidRequest tests that valid request returns ErrNotImplemented (skeleton).
func TestBacktestRunner_Run_ValidRequest(t *testing.T) {
	runner := NewDefaultBacktestRunner()
	req := &BacktestRequest{
		Symbols:        []string{"BTCUSDT", "ETHUSDT"},
		StartTime:      time.Now().Add(-168 * time.Hour), // 7 days
		EndTime:        time.Now(),
		InitialCapital: 100000.0,
		ConfigPath:     "/tmp/strategy.yaml",
	}
	resp, err := runner.Run(context.Background(), req)
	// FIX: Skeleton implementation returns ErrNotImplemented, not zero-value + nil.
	require.ErrorIs(t, err, ErrNotImplemented)
	require.Nil(t, resp)
}

// TestBacktestRunner_Run_CancelledContext tests context cancellation.
func TestBacktestRunner_Run_CancelledContext(t *testing.T) {
	runner := NewDefaultBacktestRunner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req := &BacktestRequest{
		Symbols:        []string{"BTCUSDT"},
		StartTime:      time.Now().Add(-24 * time.Hour),
		EndTime:        time.Now(),
		InitialCapital: 10000.0,
	}
	resp, err := runner.Run(ctx, req)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, resp)
}
