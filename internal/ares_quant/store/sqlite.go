package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
)

// SQLiteStore implements Store using SQLite (via modernc.org/sqlite, no CGO).
// Thread-safe via the sql.DB connection pool. WAL mode enabled for concurrent reads.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens or creates a SQLite database at the given path.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("sqlite: migrate: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS quant_decisions (
			id              TEXT PRIMARY KEY,
			ticker          TEXT NOT NULL,
			decision_date   TEXT NOT NULL,
			signal          TEXT NOT NULL,
			confidence      REAL DEFAULT 0,
			quantity        INTEGER DEFAULT 0,
			price           REAL DEFAULT 0,
			reasoning       TEXT DEFAULT '',
			analyst_reports TEXT DEFAULT '',
			debate_rounds   INTEGER DEFAULT 0,
			realized_return REAL DEFAULT 0,
			alpha_vs_spy    REAL DEFAULT 0,
			reflection      TEXT DEFAULT '',
			created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(ticker, decision_date)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_decisions_ticker ON quant_decisions(ticker)`,
		`CREATE INDEX IF NOT EXISTS idx_decisions_date ON quant_decisions(decision_date)`,

		`CREATE TABLE IF NOT EXISTS signals (
			ticker      TEXT NOT NULL,
			date        TEXT NOT NULL,
			indicator   TEXT NOT NULL,
			value       REAL DEFAULT 0,
			metadata    TEXT DEFAULT '',
			PRIMARY KEY (ticker, date, indicator)
		)`,
	}
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) SaveDecision(ctx context.Context, d *Decision) error {
	if d.CreatedAt == "" {
		d.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO quant_decisions (id, ticker, decision_date, signal, confidence, quantity, price,
			reasoning, analyst_reports, debate_rounds, realized_return, alpha_vs_spy, reflection, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(ticker, decision_date) DO UPDATE SET
			signal=excluded.signal, confidence=excluded.confidence, quantity=excluded.quantity,
			price=excluded.price, reasoning=excluded.reasoning, analyst_reports=excluded.analyst_reports,
			debate_rounds=excluded.debate_rounds, realized_return=excluded.realized_return,
			alpha_vs_spy=excluded.alpha_vs_spy, reflection=excluded.reflection`,
		d.ID, d.Ticker, d.DecisionDate, d.Signal, d.Confidence, d.Quantity, d.Price,
		d.Reasoning, d.AnalystReports, d.DebateRounds, d.RealizedReturn, d.AlphaVsSPY,
		d.Reflection, d.CreatedAt)
	return err
}

func (s *SQLiteStore) Decisions(ctx context.Context, ticker string, limit int) ([]Decision, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ticker, decision_date, signal, confidence, quantity, price,
			reasoning, analyst_reports, debate_rounds, realized_return, alpha_vs_spy, reflection, created_at
		FROM quant_decisions WHERE ticker = ? ORDER BY decision_date DESC LIMIT ?`, ticker, limit)
	if err != nil {
		return nil, fmt.Errorf("query decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanDecisions(rows)
}

func (s *SQLiteStore) LatestDecision(ctx context.Context, ticker string) (*Decision, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, ticker, decision_date, signal, confidence, quantity, price,
			reasoning, analyst_reports, debate_rounds, realized_return, alpha_vs_spy, reflection, created_at
		FROM quant_decisions WHERE ticker = ? ORDER BY decision_date DESC LIMIT 1`, ticker)
	d, err := scanDecisionRow(row)
	if err == sql.ErrNoRows {
		return nil, coreerrors.ErrRecordNotFound
	}
	return d, err
}

func (s *SQLiteStore) SaveSignal(ctx context.Context, sig *SignalRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO signals (ticker, date, indicator, value, metadata)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(ticker, date, indicator) DO UPDATE SET
			value=excluded.value, metadata=excluded.metadata`,
		sig.Ticker, sig.Date, sig.Indicator, sig.Value, sig.Metadata)
	return err
}

func (s *SQLiteStore) Signals(ctx context.Context, ticker, indicator string, limit int) ([]SignalRecord, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ticker, date, indicator, value, metadata FROM signals
		WHERE ticker = ? AND indicator = ? ORDER BY date DESC LIMIT ?`, ticker, indicator, limit)
	if err != nil {
		return nil, fmt.Errorf("query signals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSignals(rows)
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

// scan helpers.

func scanDecisions(rows *sql.Rows) ([]Decision, error) {
	var results []Decision
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.ID, &d.Ticker, &d.DecisionDate, &d.Signal,
			&d.Confidence, &d.Quantity, &d.Price, &d.Reasoning,
			&d.AnalystReports, &d.DebateRounds, &d.RealizedReturn,
			&d.AlphaVsSPY, &d.Reflection, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		results = append(results, d)
	}
	return results, rows.Err()
}

func scanDecisionRow(row *sql.Row) (*Decision, error) {
	var d Decision
	if err := row.Scan(&d.ID, &d.Ticker, &d.DecisionDate, &d.Signal,
		&d.Confidence, &d.Quantity, &d.Price, &d.Reasoning,
		&d.AnalystReports, &d.DebateRounds, &d.RealizedReturn,
		&d.AlphaVsSPY, &d.Reflection, &d.CreatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

func scanSignals(rows *sql.Rows) ([]SignalRecord, error) {
	var results []SignalRecord
	for rows.Next() {
		var s SignalRecord
		if err := rows.Scan(&s.Ticker, &s.Date, &s.Indicator, &s.Value, &s.Metadata); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}
