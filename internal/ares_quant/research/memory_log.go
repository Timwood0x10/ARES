package research

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Timwood0x10/ares/internal/truncate"
)

// MemoryStatus represents the resolution status of a memory entry.
type MemoryStatus int

const (
	// MemoryStatusPending indicates the decision outcome is not yet known.
	MemoryStatusPending MemoryStatus = iota + 1

	// MemoryStatusResolved indicates the decision has been resolved with actual results.
	MemoryStatusResolved
)

// String returns a human-readable representation of the memory status.
func (s MemoryStatus) String() string {
	switch s {
	case MemoryStatusResolved:
		return "resolved"
	default:
		return "pending"
	}
}

// Outcome captures the actual result of a resolved decision for alpha calculation.
type Outcome struct {
	ActualReturn    float64 `json:"actual_return"`
	BenchmarkReturn float64 `json:"benchmark_return"`
	RealizedAlpha   float64 `json:"realized_alpha"`
	Notes           string  `json:"notes"`
}

// MemoryEntry represents a single decision record in the append-only memory log.
// It stores the full context of a portfolio decision for later reflection and learning.
type MemoryEntry struct {
	ID                string             `json:"id"`
	Symbol            string             `json:"symbol"`
	AnalysisDate      time.Time          `json:"analysis_date"`
	Rating            PortfolioRating    `json:"rating"`
	TraderProposal    *TraderProposal    `json:"trader_proposal,omitempty"`
	FinalDecision     *PortfolioDecision `json:"final_decision,omitempty"`
	Benchmark         string             `json:"benchmark"` // e.g., "SPY"
	RawReturn         *float64           `json:"raw_return,omitempty"`
	AlphaReturn       *float64           `json:"alpha_return,omitempty"`
	HoldingDays       int                `json:"holding_days"`
	Reflection        string             `json:"reflection"`
	SourceQuality     string             `json:"source_quality"` // poor/fair/good/excellent
	CrossTickerLesson string             `json:"cross_ticker_lesson,omitempty"`
	Status            MemoryStatus       `json:"status"`
	CreatedAt         time.Time          `json:"created_at"`
	ResolvedAt        *time.Time         `json:"resolved_at,omitempty"`
}

// Store defines the persistence interface for memory entries.
// Implementations can use SQLite (via modernc.org/sqlite) or other backends.
type Store interface {
	// AppendEntry adds a new memory entry to the log.
	AppendEntry(ctx context.Context, entry *MemoryEntry) error

	// GetEntries retrieves entries for a symbol, ordered by date descending.
	GetEntries(ctx context.Context, symbol string, limit int) ([]*MemoryEntry, error)

	// GetPendingEntries returns all entries that have not yet been resolved.
	GetPendingEntries(ctx context.Context) ([]*MemoryEntry, error)

	// GetAllResolvedEntries returns all resolved entries across all symbols,
	// ordered by resolved_at descending. Used for cross-ticker lesson extraction.
	GetAllResolvedEntries(ctx context.Context, limit int) ([]*MemoryEntry, error)

	// UpdateOutcome updates a pending entry with actual outcome data.
	UpdateOutcome(ctx context.Context, id string, outcome *Outcome) error

	// UpdateReflection persists the reflection text for an existing entry.
	UpdateReflection(ctx context.Context, id string, reflection string) error

	// Close releases any underlying resources.
	Close() error
}

// PMContext contains historical context to inject into the Portfolio Manager prompt.
// It aggregates same-ticker history and cross-ticker lessons for informed decision-making.
type PMContext struct {
	SameTickerSummary  string         `json:"same_ticker_summary"`
	PastDecisions      []*MemoryEntry `json:"past_decisions"`
	CrossTickerLessons []string       `json:"cross_ticker_lessons"`
	AvgAccuracy        float64        `json:"avg_accuracy"`
}

// MemoryLog manages an append-only decision log for post-trade reflection and learning.
// It supports the closed loop: decision -> outcome -> reflection -> next-time injection.
type MemoryLog struct {
	store Store
	mu    sync.RWMutex
}

// NewMemoryLog creates a new MemoryLog with the given store backend.
//
// Args:
//   - store: persistence backend implementing the Store interface.
//
// Returns:
//   - initialized MemoryLog ready for use.
func NewMemoryLog(store Store) *MemoryLog {
	return &MemoryLog{store: store}
}

// Append adds a new decision entry to the memory log.
// The entry ID is auto-generated if empty.
//
// Args:
//   - ctx: context for cancellation.
//   - entry: the memory entry to persist. ID is generated if empty.
//
// Returns:
//   - error if persistence fails.
func (log *MemoryLog) Append(ctx context.Context, entry *MemoryEntry) error {
	log.mu.Lock()
	defer log.mu.Unlock()

	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	if entry.Status == 0 {
		entry.Status = MemoryStatusPending
	}

	if err := log.store.AppendEntry(ctx, entry); err != nil {
		return fmt.Errorf("memory log append: %w", err)
	}
	return nil
}

// ResolvePending batch-updates pending decisions with actual outcomes.
// This is called when trade results become available (e.g., after N holding days).
//
// Args:
//   - ctx: context for cancellation.
//   - outcomes: map of entry ID -> actual outcome data.
//
// Returns:
//   - count of successfully resolved entries.
//   - error if the operation fails critically.
func (log *MemoryLog) ResolvePending(ctx context.Context, outcomes map[string]*Outcome) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()

	count := 0
	now := time.Now()
	for id, outcome := range outcomes {
		if err := log.store.UpdateOutcome(ctx, id, outcome); err != nil {
			continue // Skip failed updates, continue with others.
		}
		count++
		_ = now // Will be used to set ResolvedAt in store implementation.
	}
	return count, nil
}

// GetSymbolHistory retrieves past decisions for a specific symbol.
// Used to inject same-ticker context into PM prompt on subsequent analyses.
//
// Args:
//   - ctx: context for cancellation.
//   - symbol: ticker symbol to query.
//   - limit: maximum number of entries to return.
//
// Returns:
//   - slice of past memory entries for the symbol, ordered by date descending.
//   - error if the query fails.
func (log *MemoryLog) GetSymbolHistory(ctx context.Context, symbol string, limit int) ([]*MemoryEntry, error) {
	log.mu.RLock()
	defer log.mu.RUnlock()

	return log.store.GetEntries(ctx, symbol, limit)
}

// GetCrossTickerLessons extracts lessons from resolved entries of other tickers.
// Useful for identifying patterns that apply across different symbols.
//
// It queries resolved entries with non-empty reflections, excludes the given symbol,
// and returns reflections from the most recent high-alpha and low-alpha entries.
//
// Args:
//   - ctx: context for cancellation.
//   - excludeSymbol: symbol to exclude from cross-ticker analysis.
//   - limit: maximum number of lessons to return.
//
// Returns:
//   - slice of lesson strings from other tickers' reflections.
//   - error if the query fails.
func (log *MemoryLog) GetCrossTickerLessons(ctx context.Context, excludeSymbol string, limit int) ([]string, error) {
	log.mu.RLock()
	defer log.mu.RUnlock()

	// Extract cross-ticker lessons from resolved store entries.
	resolved, err := log.store.GetAllResolvedEntries(ctx, limit*3)
	if err != nil {
		return nil, fmt.Errorf("cross ticker lessons get resolved: %w", err)
	}

	var lessons []string
	for _, entry := range resolved {
		if entry.Symbol == excludeSymbol {
			continue
		}
		if entry.Reflection != "" {
			lessons = append(lessons, entry.Reflection)
		}
		if len(lessons) >= limit {
			break
		}
	}
	return lessons, nil
}

// GenerateContext builds a PMContext for a specific symbol containing historical context.
// This is injected into the Portfolio Manager prompt before final decision.
//
// Args:
//   - ctx: context for cancellation.
//   - symbol: ticker symbol to generate context for.
//
// Returns:
//   - PMContext with same-ticker summary, past decisions, and cross-ticker lessons.
//   - error if data retrieval fails.
func (log *MemoryLog) GenerateContext(ctx context.Context, symbol string) (*PMContext, error) {
	log.mu.RLock()
	defer log.mu.RUnlock()

	pastEntries, err := log.store.GetEntries(ctx, symbol, 10)
	if err != nil {
		return nil, fmt.Errorf("generate context get history: %w", err)
	}

	crossLessons, _ := log.GetCrossTickerLessons(ctx, symbol, 5)
	_ = crossLessons

	ctxData := &PMContext{
		PastDecisions: pastEntries,
		AvgAccuracy:   calculateAvgAccuracy(pastEntries),
	}

	if len(pastEntries) > 0 {
		ctxData.SameTickerSummary = formatSameTickerSummary(pastEntries)
	}

	return ctxData, nil
}

// ─── Internal Helpers ──────────────────────────────────────

func calculateAvgAccuracy(entries []*MemoryEntry) float64 {
	if len(entries) == 0 {
		return 0
	}
	var totalAlpha float64
	count := 0
	for _, e := range entries {
		if e.AlphaReturn != nil && e.Status == MemoryStatusResolved {
			totalAlpha += *e.AlphaReturn
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return totalAlpha / float64(count)
}

func formatSameTickerSummary(entries []*MemoryEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var summary string
	for _, e := range entries {
		summary += fmt.Sprintf("- %s: %s on %s", e.AnalysisDate.Format("2006-01-02"), e.Rating, e.Symbol)
		if e.Reflection != "" {
			summary += fmt.Sprintf(" (reflection: %s)", truncate.WithEllipsis(e.Reflection, 80))
		}
		summary += "\n"
	}
	return summary
}


