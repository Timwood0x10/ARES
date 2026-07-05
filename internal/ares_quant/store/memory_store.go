package store

import (
	"context"
	"sort"
	"sync"
	"time"

	coreerrors "github.com/Timwood0x10/ares/internal/errors"
)

// MemoryStore implements Store with in-memory maps.
// Useful for testing and demo mode. Data is not persisted across restarts.
type MemoryStore struct {
	mu                sync.RWMutex
	decisions         []Decision // Sorted by ticker + date desc
	signals           []SignalRecord
	decisionsByTicker map[string][]Decision
	signalsByKey      map[string]*SignalRecord
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		decisionsByTicker: make(map[string][]Decision),
		signalsByKey:      make(map[string]*SignalRecord),
	}
}

func (s *MemoryStore) SaveDecision(_ context.Context, d *Decision) error {
	if d.CreatedAt == "" {
		d.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Replace existing entry for same ticker + date.
	existing := -1
	for i, dec := range s.decisions {
		if dec.Ticker == d.Ticker && dec.DecisionDate == d.DecisionDate {
			existing = i
			break
		}
	}
	if existing >= 0 {
		s.decisions[existing] = *d
	} else {
		s.decisions = append(s.decisions, *d)
	}

	// Update ticker index.
	s.decisionsByTicker[d.Ticker] = append(s.decisionsByTicker[d.Ticker], *d)
	sort.Slice(s.decisionsByTicker[d.Ticker], func(i, j int) bool {
		return s.decisionsByTicker[d.Ticker][i].DecisionDate >
			s.decisionsByTicker[d.Ticker][j].DecisionDate
	})

	return nil
}

func (s *MemoryStore) Decisions(_ context.Context, ticker string, limit int) ([]Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	decisions := s.decisionsByTicker[ticker]
	if limit <= 0 || limit > len(decisions) {
		limit = len(decisions)
	}
	result := make([]Decision, limit)
	copy(result, decisions[:limit])
	return result, nil
}

func (s *MemoryStore) LatestDecision(_ context.Context, ticker string) (*Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	decisions := s.decisionsByTicker[ticker]
	if len(decisions) == 0 {
		return nil, coreerrors.ErrRecordNotFound
	}
	d := decisions[0]
	return &d, nil
}

func (s *MemoryStore) SaveSignal(_ context.Context, sig *SignalRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sig.Ticker + "|" + sig.Date + "|" + sig.Indicator
	s.signalsByKey[key] = sig

	// Append to flat list for iteration, but deduplicate.
	found := false
	for i, existing := range s.signals {
		if existing.Ticker == sig.Ticker && existing.Date == sig.Date && existing.Indicator == sig.Indicator {
			s.signals[i] = *sig
			found = true
			break
		}
	}
	if !found {
		s.signals = append(s.signals, *sig)
	}

	return nil
}

func (s *MemoryStore) Signals(_ context.Context, ticker, indicator string, limit int) ([]SignalRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var filtered []SignalRecord
	for _, sig := range s.signals {
		if sig.Ticker == ticker && sig.Indicator == indicator {
			filtered = append(filtered, sig)
		}
	}
	// Sort by date descending.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Date > filtered[j].Date
	})

	if limit <= 0 || limit > len(filtered) {
		limit = len(filtered)
	}
	return filtered[:limit], nil
}

func (s *MemoryStore) Close() error { return nil }

// Ensure MemoryStore implements Store at compile time.
var _ Store = (*MemoryStore)(nil)

// NewStore creates the appropriate store implementation based on config.
// If path is empty, returns an in-memory store. Otherwise opens SQLite.
func NewStore(path string) (Store, error) {
	if path == "" {
		return NewMemoryStore(), nil
	}
	return NewSQLiteStore(path)
}
