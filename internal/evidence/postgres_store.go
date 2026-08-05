// Package evidence — PostgreSQL-backed evidence store (persistence).
//
// PostgresStore implements the Store interface against the shared Postgres
// pool so evidence survives process restarts (T1, evidence persistence). It
// mirrors MemoryStore semantics: Query filters by source/kind/time window,
// orders by timestamp DESC, applies limit; Aggregate computes locally.
package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// PostgresStore persists evidence records in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a Postgres-backed evidence store and ensures the
// evidence_records table exists.
//
// Args:
//
//	pool - the shared Postgres pool; must be non-nil and healthy.
//
// Returns:
//
//	store - the Postgres evidence store.
//	err   - error when the pool is nil or table creation fails (fail-loud).
func NewPostgresStore(pool *postgres.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("evidence: postgres store requires a non-nil pool")
	}
	s := &PostgresStore{db: pool.GetDB()}
	if err := s.ensureTable(context.Background()); err != nil {
		return nil, fmt.Errorf("evidence: ensure table: %w", err)
	}
	return s, nil
}

// ensureTable creates the evidence_records table if it does not exist.
func (s *PostgresStore) ensureTable(ctx context.Context) error {
	const createTable = `
		CREATE TABLE IF NOT EXISTS evidence_records (
			id         uuid PRIMARY KEY,
			source     text NOT NULL,
			kind       text NOT NULL,
			payload    jsonb NOT NULL,
			metadata   jsonb NOT NULL DEFAULT '{}',
			ts         timestamptz NOT NULL
		)`
	_, err := s.db.ExecContext(ctx, createTable)
	return err
}

// Append inserts a single evidence record. A zero ID is generated client-side
// from the timestamp so retries do not duplicate rows.
func (s *PostgresStore) Append(ctx context.Context, e Evidence) error {
	id := e.ID
	if id == "" {
		id = fmt.Sprintf("%d-%s", e.Timestamp.UnixNano(), e.Source)
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("evidence: marshal payload: %w", err)
	}
	metadata, err := json.Marshal(e.Metadata)
	if err != nil {
		return fmt.Errorf("evidence: marshal metadata: %w", err)
	}
	const insert = `
		INSERT INTO evidence_records (id, source, kind, payload, metadata, ts)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err = s.db.ExecContext(ctx, insert,
		id, e.Source, string(e.Kind), payload, metadata, e.Timestamp)
	if err != nil {
		return fmt.Errorf("evidence: append: %w", err)
	}
	return nil
}

// Query returns evidence matching the filter, ordered by timestamp DESC.
// non-parameterized parts are hardcoded query fragments, and user-supplied
// filter values are passed via $1..$N parameterized placeholders.
//
//nolint:gosec // G202: SQL string concatenation is safe here — all
func (s *PostgresStore) Query(ctx context.Context, filter Filter) ([]Evidence, error) {
	query := `
		SELECT id, source, kind, payload, metadata, ts
		FROM evidence_records
		WHERE 1=1`
	args := make([]any, 0, 5)
	argID := 1
	if filter.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argID)
		args = append(args, filter.Source)
		argID++
	}
	if filter.Kind != "" {
		query += fmt.Sprintf(" AND kind = $%d", argID)
		args = append(args, string(filter.Kind))
		argID++
	}
	if !filter.Since.IsZero() {
		query += fmt.Sprintf(" AND ts >= $%d", argID)
		args = append(args, filter.Since)
		argID++
	}
	if !filter.Until.IsZero() {
		query += fmt.Sprintf(" AND ts <= $%d", argID)
		args = append(args, filter.Until)
		argID++
	}
	query += " ORDER BY ts DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argID)
		args = append(args, filter.Limit)
		// argID not incremented — no further parameter uses it.
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("evidence: query: %w", err)
	}
	defer rows.Close() //nolint:errcheck // deferred close; error is not actionable

	var result []Evidence
	for rows.Next() {
		var e Evidence
		var kind string
		var payload, metadata []byte
		if err := rows.Scan(&e.ID, &e.Source, &kind, &payload, &metadata, &e.Timestamp); err != nil {
			return nil, fmt.Errorf("evidence: scan: %w", err)
		}
		e.Kind = EvidenceKind(kind)
		e.Payload = payload
		if len(metadata) > 0 && string(metadata) != "{}" && string(metadata) != "null" {
			_ = json.Unmarshal(metadata, &e.Metadata)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evidence: rows: %w", err)
	}
	return result, nil
}

// Aggregate computes a metric over matching evidence using the caller's
// AggregateFn. Extraction of float64 from payloads mirrors MemoryStore.
func (s *PostgresStore) Aggregate(ctx context.Context, filter Filter, fn AggregateFn) (float64, error) {
	results, err := s.Query(ctx, filter)
	if err != nil {
		return 0, err
	}
	values := make([]float64, 0, len(results))
	for _, e := range results {
		var v float64
		if err := json.Unmarshal(e.Payload, &v); err == nil {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return 0, nil
	}
	return fn(values), nil
}

// Close closes the underlying database handle.
func (s *PostgresStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ensure PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)
