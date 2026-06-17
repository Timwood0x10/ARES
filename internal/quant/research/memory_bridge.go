package research

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// PopulateMemoryContext queries the MemoryLog for historical decisions on the
// given symbol and populates state.MemoryContext for injection into the PM prompt.
// Memory failures are non-fatal: the research continues without historical context.
//
// Args:
//   - ctx: context for cancellation.
//   - log: memory log instance (may be nil — skipped silently).
//   - state: mutable research state to populate.
//
// Returns:
//   - nil always; errors are logged as warnings but never propagated.
func PopulateMemoryContext(ctx context.Context, log *MemoryLog, state *ResearchState) {
	if log == nil {
		return
	}
	mc, err := log.GenerateContext(ctx, state.Symbol)
	if err != nil {
		slog.Warn("memory context generation skipped", "symbol", state.Symbol, "err", err)
		return
	}
	state.MemoryContext = mc
}

// SaveDecisionToMemory persists the Portfolio Decision to the MemoryLog
// after graph execution completes. The decision is stored as a pending entry
// (outcome to be resolved later when trade results are known).
// Memory failures are non-fatal: the research output is preserved.
//
// Args:
//   - ctx: context for cancellation.
//   - log: memory log instance (may be nil — skipped silently).
//   - state: completed research state with PortfolioDecision populated.
//
// Returns:
//   - nil always; errors are logged as warnings but never propagated.
func SaveDecisionToMemory(ctx context.Context, log *MemoryLog, state *ResearchState) {
	if log == nil || state.PortfolioDecision == nil {
		return
	}
	entry := &MemoryEntry{
		Symbol:       state.Symbol,
		AnalysisDate: state.AnalysisDate,
		Rating:       state.PortfolioDecision.Rating,
		FinalDecision: &PortfolioDecision{
			Rating:           state.PortfolioDecision.Rating,
			ExecutiveSummary: state.PortfolioDecision.ExecutiveSummary,
			InvestmentThesis: state.PortfolioDecision.InvestmentThesis,
			PriceTarget:      state.PortfolioDecision.PriceTarget,
			TimeHorizon:      state.PortfolioDecision.TimeHorizon,
		},
		Benchmark:     "SPY",
		SourceQuality: "research_layer",
		Status:        MemoryStatusPending,
	}
	if err := log.Append(ctx, entry); err != nil {
		slog.Warn("memory save skipped", "symbol", state.Symbol, "err", err)
		return
	}
	slog.Debug("decision saved to memory log", "symbol", state.Symbol,
		"rating", state.PortfolioDecision.Rating, "entry_id", entry.ID)
}

// EnsureMemoryStore initializes an in-memory MemoryStore for use in the
// research layer when no persistent storage path is configured.
// This is safe for demo/example use; production deployments should use
// a persistent SQLite store.
//
// Args:
//   - dbPath: path to SQLite database file. If empty, an in-memory store is used.
//
// Returns:
//   - configured MemoryStore ready for use.
//   - error if store initialization fails.
func EnsureMemoryStore(dbPath string) (Store, error) {
	if dbPath == "" {
		return NewInMemoryStore()
	}
	return NewMemoryStore(dbPath)
}

// NewInMemoryStore creates a lightweight in-memory Store for testing/demo use.
func NewInMemoryStore() (Store, error) {
	return &memoryStore{
		entries: make([]*MemoryEntry, 0),
		pending: make([]*MemoryEntry, 0),
	}, nil
}

// memoryStore is a minimal in-memory implementation of Store for testing.
type memoryStore struct {
	entries []*MemoryEntry
	pending []*MemoryEntry
	nextID  int
}

func (s *memoryStore) AppendEntry(_ context.Context, entry *MemoryEntry) error {
	s.nextID++
	entry.ID = fmt.Sprintf("mem-%d", s.nextID)
	if entry.Status == MemoryStatusPending {
		s.pending = append(s.pending, entry)
	}
	s.entries = append(s.entries, entry)
	return nil
}

func (s *memoryStore) GetEntries(_ context.Context, symbol string, limit int) ([]*MemoryEntry, error) {
	var result []*MemoryEntry
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].Symbol == symbol {
			result = append(result, s.entries[i])
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (s *memoryStore) GetPendingEntries(_ context.Context) ([]*MemoryEntry, error) {
	return s.pending, nil
}

func (s *memoryStore) GetAllResolvedEntries(_ context.Context, limit int) ([]*MemoryEntry, error) {
	var result []*MemoryEntry
	for _, e := range s.entries {
		if e.Status == MemoryStatusResolved && e.Reflection != "" {
			result = append(result, e)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (s *memoryStore) UpdateOutcome(_ context.Context, id string, outcome *Outcome) error {
	for _, e := range s.entries {
		if e.ID == id {
			e.Status = MemoryStatusResolved
			e.RawReturn = &outcome.ActualReturn
			e.AlphaReturn = &outcome.RealizedAlpha
			now := time.Now()
			e.ResolvedAt = &now
			return nil
		}
	}
	return fmt.Errorf("entry %s not found", id)
}

func (s *memoryStore) UpdateReflection(_ context.Context, id string, reflection string) error {
	for _, e := range s.entries {
		if e.ID == id {
			e.Reflection = reflection
			return nil
		}
	}
	return fmt.Errorf("entry %s not found", id)
}

func (s *memoryStore) Close() error { return nil }
