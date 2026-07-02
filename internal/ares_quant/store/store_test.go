package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
)

func TestMemoryStore_New(t *testing.T) {
	s := NewMemoryStore()
	require.NotNil(t, s)
	assert.NotNil(t, s.decisionsByTicker)
	assert.NotNil(t, s.signalsByKey)
}

func TestMemoryStore_SaveAndDecisions(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	d := &Decision{
		Ticker:       "AAPL",
		DecisionDate: "2026-06-15",
		Signal:       "buy",
		Confidence:   0.85,
		Quantity:     100,
		Price:        245.0,
		Reasoning:    "Bullish trend",
	}
	err := s.SaveDecision(ctx, d)
	require.NoError(t, err)

	d2 := &Decision{
		Ticker:       "AAPL",
		DecisionDate: "2026-06-14",
		Signal:       "hold",
		Confidence:   0.5,
	}
	err = s.SaveDecision(ctx, d2)
	require.NoError(t, err)

	decisions, err := s.Decisions(ctx, "AAPL", 10)
	require.NoError(t, err)
	assert.Len(t, decisions, 2)
	// Ordered by date descending.
	assert.Equal(t, "2026-06-15", decisions[0].DecisionDate)
	assert.Equal(t, "2026-06-14", decisions[1].DecisionDate)
}

func TestMemoryStore_Decisions_NoData(t *testing.T) {
	s := NewMemoryStore()
	decisions, err := s.Decisions(context.Background(), "NONEXISTENT", 10)
	assert.NoError(t, err)
	assert.Len(t, decisions, 0)
}

func TestMemoryStore_Decisions_Limit(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		err := s.SaveDecision(ctx, &Decision{
			Ticker: "AAPL", DecisionDate: "2026-06-01",
			Signal: "hold",
		})
		require.NoError(t, err)
	}
	// Limit to 3.
	decisions, err := s.Decisions(ctx, "AAPL", 3)
	require.NoError(t, err)
	assert.Len(t, decisions, 3)
}

func TestMemoryStore_LatestDecision(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	err := s.SaveDecision(ctx, &Decision{
		Ticker: "AAPL", DecisionDate: "2026-06-10", Signal: "buy",
	})
	require.NoError(t, err)
	err = s.SaveDecision(ctx, &Decision{
		Ticker: "AAPL", DecisionDate: "2026-06-15", Signal: "sell",
	})
	require.NoError(t, err)

	d, err := s.LatestDecision(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, "sell", d.Signal)
	assert.Equal(t, "2026-06-15", d.DecisionDate)
}

func TestMemoryStore_LatestDecision_NotFound(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.LatestDecision(context.Background(), "NONEXISTENT")
	assert.ErrorIs(t, err, coreerrors.ErrRecordNotFound)
}

func TestMemoryStore_SaveDecision_ReplacesExisting(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	d := &Decision{Ticker: "AAPL", DecisionDate: "2026-06-15", Signal: "buy", Confidence: 0.8}
	err := s.SaveDecision(ctx, d)
	require.NoError(t, err)

	// Different date: should add a new entry.
	d2 := &Decision{Ticker: "AAPL", DecisionDate: "2026-06-16", Signal: "sell", Confidence: 0.7}
	err = s.SaveDecision(ctx, d2)
	require.NoError(t, err)

	decisions, err := s.Decisions(ctx, "AAPL", 10)
	require.NoError(t, err)
	assert.Len(t, decisions, 2)
	assert.Equal(t, "sell", decisions[0].Signal) // most recent first
	assert.Equal(t, "buy", decisions[1].Signal)
}

func TestMemoryStore_SaveDecision_SetsCreatedAt(t *testing.T) {
	s := NewMemoryStore()
	d := &Decision{Ticker: "AAPL", DecisionDate: "2026-06-15", Signal: "buy"}
	err := s.SaveDecision(context.Background(), d)
	require.NoError(t, err)
	assert.NotEmpty(t, d.CreatedAt)
}

func TestMemoryStore_SaveAndSignals(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	err := s.SaveSignal(ctx, &SignalRecord{
		Ticker: "AAPL", Date: "2026-06-15", Indicator: "RSI", Value: 72.5,
	})
	require.NoError(t, err)
	err = s.SaveSignal(ctx, &SignalRecord{
		Ticker: "AAPL", Date: "2026-06-14", Indicator: "RSI", Value: 68.0,
	})
	require.NoError(t, err)
	// Different indicator.
	err = s.SaveSignal(ctx, &SignalRecord{
		Ticker: "AAPL", Date: "2026-06-15", Indicator: "MACD", Value: 1.2,
	})
	require.NoError(t, err)

	// Filter by ticker + indicator.
	signals, err := s.Signals(ctx, "AAPL", "RSI", 10)
	require.NoError(t, err)
	assert.Len(t, signals, 2)
	// Ordered by date descending.
	assert.Equal(t, "2026-06-15", signals[0].Date)
	assert.Equal(t, "2026-06-14", signals[1].Date)

	// Different indicator.
	signals, err = s.Signals(ctx, "AAPL", "MACD", 10)
	require.NoError(t, err)
	assert.Len(t, signals, 1)
	assert.Equal(t, 1.2, signals[0].Value)
}

func TestMemoryStore_Signals_NoData(t *testing.T) {
	s := NewMemoryStore()
	signals, err := s.Signals(context.Background(), "NONEXISTENT", "RSI", 10)
	assert.NoError(t, err)
	assert.Len(t, signals, 0)
}

func TestMemoryStore_Signals_Limit(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	dates := []string{
		"2026-06-01", "2026-06-02", "2026-06-03", "2026-06-04", "2026-06-05",
		"2026-06-06", "2026-06-07", "2026-06-08", "2026-06-09", "2026-06-10",
	}
	for i, date := range dates {
		err := s.SaveSignal(ctx, &SignalRecord{
			Ticker: "AAPL", Date: date, Indicator: "RSI", Value: float64(50 + i),
		})
		require.NoError(t, err)
	}
	signals, err := s.Signals(ctx, "AAPL", "RSI", 3)
	require.NoError(t, err)
	assert.Len(t, signals, 3)
}

func TestMemoryStore_SaveSignal_ReplacesExisting(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	err := s.SaveSignal(ctx, &SignalRecord{
		Ticker: "AAPL", Date: "2026-06-15", Indicator: "RSI", Value: 70.0,
	})
	require.NoError(t, err)
	err = s.SaveSignal(ctx, &SignalRecord{
		Ticker: "AAPL", Date: "2026-06-15", Indicator: "RSI", Value: 75.0,
	})
	require.NoError(t, err)

	signals, err := s.Signals(ctx, "AAPL", "RSI", 10)
	require.NoError(t, err)
	assert.Len(t, signals, 1)
	assert.Equal(t, 75.0, signals[0].Value)
}

func TestMemoryStore_Close(t *testing.T) {
	s := NewMemoryStore()
	assert.NoError(t, s.Close())
}

func TestNewStore_EmptyPath(t *testing.T) {
	s, err := NewStore("")
	require.NoError(t, err)
	_, ok := s.(*MemoryStore)
	assert.True(t, ok, "expected *MemoryStore for empty path")
	assert.NoError(t, s.Close())
}

func TestNewStore_InvalidPath(t *testing.T) {
	_, err := NewStore("/nonexistent/dir/store.db")
	assert.Error(t, err)
}

func TestDecisionDefaults(t *testing.T) {
	d := Decision{
		Ticker: "AAPL", DecisionDate: "2026-06-15", Signal: "buy",
	}
	// Unset fields should be zero-valued.
	assert.Equal(t, "", d.ID)
	assert.Equal(t, 0.0, d.Confidence)
	assert.Equal(t, 0, d.Quantity)
	assert.Equal(t, 0.0, d.Price)
}

func TestSignalRecordConstruction(t *testing.T) {
	s := SignalRecord{
		Ticker: "AAPL", Date: "2026-06-15", Indicator: "SMA_20",
		Value: 250.0, Metadata: `{"period":20}`,
	}
	assert.Equal(t, "SMA_20", s.Indicator)
	assert.Equal(t, `{"period":20}`, s.Metadata)
}

func TestMemoryStore_SaveDecision_MultipleTickers(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	err := s.SaveDecision(ctx, &Decision{Ticker: "AAPL", DecisionDate: "2026-06-15", Signal: "buy"})
	require.NoError(t, err)
	err = s.SaveDecision(ctx, &Decision{Ticker: "MSFT", DecisionDate: "2026-06-15", Signal: "hold"})
	require.NoError(t, err)

	aapl, err := s.Decisions(ctx, "AAPL", 10)
	require.NoError(t, err)
	assert.Len(t, aapl, 1)

	msft, err := s.Decisions(ctx, "MSFT", 10)
	require.NoError(t, err)
	assert.Len(t, msft, 1)
}
