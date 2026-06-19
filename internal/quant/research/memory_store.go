package research

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// MemoryStore implements the Store interface using SQLite via modernc.org/sqlite.
// It provides persistent storage for memory entries without CGO dependencies.
type MemoryStore struct {
	db   *sql.DB
	path string
	mu   sync.RWMutex
}

// createMemoryTableSQL is the DDL for the memory_entries table.
const createMemoryTableSQL = `
CREATE TABLE IF NOT EXISTS memory_entries (
    id              TEXT PRIMARY KEY,
    symbol          TEXT NOT NULL,
    analysis_date   TEXT NOT NULL,
    rating          TEXT NOT NULL,
    trader_proposal TEXT,
    final_decision  TEXT,
    benchmark       TEXT,
    raw_return      REAL,
    alpha_return    REAL,
    holding_days    INTEGER,
    reflection      TEXT,
    source_quality  TEXT,
    cross_ticker_lesson TEXT,
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TEXT NOT NULL,
    resolved_at     TEXT,
    UNIQUE(symbol, analysis_date)
);
`

// NewMemoryStore creates a new SQLite-backed memory store at the given file path.
// If the database file does not exist, it will be created along with the schema.
//
// Args:
//   - path: filesystem path for the SQLite database file.
//
// Returns:
//   - initialized MemoryStore.
//   - error if database connection or schema creation fails.
func NewMemoryStore(path string) (*MemoryStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("memory store create dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory store open db: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close() // best-effort cleanup; primary error already captured above
		return nil, fmt.Errorf("memory store set wal mode: %w", err)
	}

	if _, err := db.Exec(createMemoryTableSQL); err != nil {
		_ = db.Close() // best-effort cleanup; primary error already captured above
		return nil, fmt.Errorf("memory store create table: %w", err)
	}

	return &MemoryStore{db: db, path: path}, nil
}

// AppendEntry inserts a new memory entry into the database.
// On conflict (same symbol + date), it skips insertion to maintain uniqueness.
//
// Args:
//   - ctx: context for cancellation.
//   - entry: the memory entry to insert.
//
// Returns:
//   - error if the insert operation fails.
func (s *MemoryStore) AppendEntry(ctx context.Context, entry *MemoryEntry) error {
	traderJSON := marshalOptionalJSON(entry.TraderProposal)
	decisionJSON := marshalOptionalJSON(entry.FinalDecision)
	resolvedAtStr := formatTimePtr(entry.ResolvedAt)

	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO memory_entries 
			(id, symbol, analysis_date, rating, trader_proposal, final_decision, 
			 benchmark, raw_return, alpha_return, holding_days, reflection, 
			 source_quality, cross_ticker_lesson, status, created_at, resolved_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.Symbol, entry.AnalysisDate.Format(time.RFC3339),
		string(entry.Rating), traderJSON, decisionJSON,
		entry.Benchmark, entry.RawReturn, entry.AlphaReturn, entry.HoldingDays,
		entry.Reflection, entry.SourceQuality, entry.CrossTickerLesson,
		entry.Status.String(), entry.CreatedAt.Format(time.RFC3339), resolvedAtStr,
	)
	if err != nil {
		return fmt.Errorf("memory store append entry: %w", err)
	}
	return nil
}

// GetEntries retrieves memory entries for a given symbol, ordered by date descending.
//
// Args:
//   - ctx: context for cancellation.
//   - symbol: ticker symbol to filter by.
//   - limit: maximum number of entries to return.
//
// Returns:
//   - slice of matching memory entries.
//   - error if the query fails.
func (s *MemoryStore) GetEntries(ctx context.Context, symbol string, limit int) ([]*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, symbol, analysis_date, rating, trader_proposal, final_decision,
		        benchmark, raw_return, alpha_return, holding_days, reflection,
		        source_quality, cross_ticker_lesson, status, created_at, resolved_at
		 FROM memory_entries 
		 WHERE symbol = ? 
		 ORDER BY analysis_date DESC 
		 LIMIT ?`,
		symbol, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory store get entries: %w", err)
	}
	defer func() { _ = rows.Close() /* best-effort */ }()

	var entries []*MemoryEntry
	for rows.Next() {
		entry, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("memory store scan row: %w", err)
		}
		entries = append(entries, entry)
	}
	// FIX: check rows.Err() to catch iteration errors (e.g., connection drops mid-scan).
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entries: %w", err)
	}
	return entries, nil
}

// GetPendingEntries returns all entries with status 'pending'.
//
// Args:
//   - ctx: context for cancellation.
//
// Returns:
//   - slice of pending memory entries.
//   - error if the query fails.
func (s *MemoryStore) GetPendingEntries(ctx context.Context) ([]*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, symbol, analysis_date, rating, trader_proposal, final_decision,
		        benchmark, raw_return, alpha_return, holding_days, reflection,
		        source_quality, cross_ticker_lesson, status, created_at, resolved_at
		 FROM memory_entries 
		 WHERE status = 'pending'
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("memory store get pending: %w", err)
	}
	defer func() { _ = rows.Close() /* best-effort */ }()

	var entries []*MemoryEntry
	for rows.Next() {
		entry, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("memory store scan row: %w", err)
		}
		entries = append(entries, entry)
	}
	// FIX: check rows.Err() to catch iteration errors (e.g., connection drops mid-scan).
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending entries: %w", err)
	}
	return entries, nil
}

// UpdateOutcome updates a pending entry's outcome data and marks it as resolved.
//
// Args:
//   - ctx: context for cancellation.
//   - id: the entry ID to update.
//   - outcome: the actual outcome data to record.
//
// Returns:
//   - error if the update fails.
func (s *MemoryStore) UpdateOutcome(ctx context.Context, id string, outcome *Outcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// FIX: wrap update in a transaction to ensure atomicity.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory store begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback() // best-effort cleanup; primary error already captured above
		}
	}()

	now := time.Now().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx,
		`UPDATE memory_entries 
		 SET raw_return = ?, alpha_return = ?, 
		     status = 'resolved', resolved_at = ?
		 WHERE id = ? AND status = 'pending'`,
		outcome.ActualReturn, outcome.RealizedAlpha, now, id,
	)
	if err != nil {
		return fmt.Errorf("memory store update outcome: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("memory store update: entry %s not found or already resolved", id)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory store commit outcome: %w", err)
	}
	return nil
}

// GetAllResolvedEntries returns all resolved entries across all symbols,
// ordered by resolved_at descending. Used for cross-ticker lesson extraction.
//
// Args:
//   - ctx: context for cancellation.
//   - limit: maximum number of entries to return.
//
// Returns:
//   - slice of resolved memory entries.
//   - error if the query fails.
func (s *MemoryStore) GetAllResolvedEntries(ctx context.Context, limit int) ([]*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, symbol, analysis_date, rating, trader_proposal, final_decision,
		        benchmark, raw_return, alpha_return, holding_days, reflection,
		        source_quality, cross_ticker_lesson, status, created_at, resolved_at
		 FROM memory_entries 
		 WHERE status = 'resolved' AND reflection != ''
		 ORDER BY resolved_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory store get all resolved: %w", err)
	}
	defer func() { _ = rows.Close() /* best-effort */ }()

	var entries []*MemoryEntry
	for rows.Next() {
		entry, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("memory store scan row: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resolved entries: %w", err)
	}
	return entries, nil
}

// UpdateReflection persists the reflection text for an existing entry.
//
// Args:
//   - ctx: context for cancellation.
//   - id: the entry ID to update.
//   - reflection: the reflection text to store.
//
// Returns:
//   - error if the update fails.
func (s *MemoryStore) UpdateReflection(ctx context.Context, id string, reflection string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.ExecContext(ctx,
		`UPDATE memory_entries SET reflection = ? WHERE id = ?`,
		reflection, id,
	)
	if err != nil {
		return fmt.Errorf("memory store update reflection: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("memory store update reflection: entry %s not found", id)
	}
	return nil
}

// Close releases the underlying database connection.
//
// Returns:
//   - error if closing the connection fails.
func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

// ─── Internal Helpers ──────────────────────────────────────

func scanMemoryRow(rows *sql.Rows) (*MemoryEntry, error) {
	var entry MemoryEntry
	var ratingStr, statusStr, createdAtStr string
	var resolvedAtStr *string
	var rawReturn, alphaReturn *float64
	var traderJSON, decisionJSON *string
	var analysisDateStr string

	err := rows.Scan(
		&entry.ID, &entry.Symbol, &analysisDateStr, &ratingStr,
		&traderJSON, &decisionJSON,
		&entry.Benchmark, &rawReturn, &alphaReturn,
		&entry.HoldingDays, &entry.Reflection,
		&entry.SourceQuality, &entry.CrossTickerLesson,
		&statusStr, &createdAtStr, &resolvedAtStr,
	)
	if err != nil {
		return nil, err
	}

	entry.Rating = PortfolioRating(ratingStr)
	entry.RawReturn = rawReturn
	entry.AlphaReturn = alphaReturn
	entry.Status = parseMemoryStatus(statusStr)

	analysisDate, err := time.Parse(time.RFC3339, analysisDateStr)
	if err == nil {
		entry.AnalysisDate = analysisDate
	}
	createdAt, err := time.Parse(time.RFC3339, createdAtStr)
	if err == nil {
		entry.CreatedAt = createdAt
	}
	if resolvedAtStr != nil && *resolvedAtStr != "" {
		resolvedAt, err := time.Parse(time.RFC3339, *resolvedAtStr)
		if err == nil {
			entry.ResolvedAt = &resolvedAt
		}
	}

	if traderJSON != nil && *traderJSON != "" {
		var tp TraderProposal
		if err := json.Unmarshal([]byte(*traderJSON), &tp); err == nil {
			entry.TraderProposal = &tp
		}
	}
	if decisionJSON != nil && *decisionJSON != "" {
		var fd PortfolioDecision
		if err := json.Unmarshal([]byte(*decisionJSON), &fd); err == nil {
			entry.FinalDecision = &fd
		}
	}

	return &entry, nil
}

func parseMemoryStatus(s string) MemoryStatus {
	switch s {
	case "pending":
		return MemoryStatusPending
	case "resolved":
		return MemoryStatusResolved
	default:
		return MemoryStatusPending
	}
}

func formatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func marshalOptionalJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	// Check for typed nil pointer (e.g., *TraderProposal(nil)).
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
