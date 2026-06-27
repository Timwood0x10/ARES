package store

import "context"

// Store defines the persistence contract for quant trading data.
// Implementations: SQLiteStore (dev), PgStore (prod).
// All methods are safe for concurrent use.
type Store interface {
	// SaveDecision persists a trading decision. Replaces on conflict (ticker + date).
	SaveDecision(ctx context.Context, d *Decision) error

	// Decisions returns all decisions for a ticker, ordered by date descending.
	Decisions(ctx context.Context, ticker string, limit int) ([]Decision, error)

	// LatestDecision returns the most recent decision for a ticker.
	LatestDecision(ctx context.Context, ticker string) (*Decision, error)

	// SaveSignal caches a computed indicator value. Replaces on conflict.
	SaveSignal(ctx context.Context, s *SignalRecord) error

	// Signals returns cached indicator values for a ticker and date range.
	Signals(ctx context.Context, ticker, indicator string, limit int) ([]SignalRecord, error)

	// Close releases any underlying resources.
	Close() error
}
