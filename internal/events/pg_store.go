// Package events defines event sourcing types and the event store interface.
// This file provides the PostgreSQL-backed implementation of EventStore.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	apperrors "goagent/internal/errors"
	"goagent/internal/storage/postgres"
)

// PostgresEventStore persists events in PostgreSQL with optimistic concurrency control.
type PostgresEventStore struct {
	pool *postgres.Pool
}

// Compile-time interface compliance check.
var _ EventStore = (*PostgresEventStore)(nil)

// NewPostgresEventStore creates a PostgresEventStore backed by the given pool.
func NewPostgresEventStore(pool *postgres.Pool) *PostgresEventStore {
	if pool == nil {
		return nil
	}
	return &PostgresEventStore{pool: pool}
}

// Append persists events to the given stream with optimistic concurrency control.
// expectedVersion semantics:
//   - 0: stream must be empty or not yet created.
//   - positive: must match the stream's current max version.
//
// Returns ErrVersionConflict on mismatch.
func (s *PostgresEventStore) Append(
	ctx context.Context,
	streamID string,
	events []*Event,
	expectedVersion int64,
) error {
	if s == nil || s.pool == nil {
		return ErrEventStoreClosed
	}
	if streamID == "" {
		return apperrors.New("streamID must not be empty")
	}
	if len(events) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apperrors.Wrap(err, "begin transaction")
	}

	// Ensure rollback on any error path.
	committed := false
	defer func() {
		if !committed {
			if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
				slog.Warn("failed to rollback event append transaction", "error", rbErr)
			}
		}
	}()

	// Read current max version under the transaction lock.
	var currentVersion int64
	err = tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(version), 0) FROM events WHERE stream_id = $1`,
		streamID,
	).Scan(&currentVersion)
	if err != nil {
		return apperrors.Wrap(err, "query current version")
	}

	// Optimistic concurrency check.
	// expectedVersion == 0: auto-detect, append after current version (no conflict).
	// expectedVersion > 0: must match current version, otherwise ErrVersionConflict.
	if expectedVersion > 0 && currentVersion != expectedVersion {
		return ErrVersionConflict
	}

	// Assign versions starting after the current max.
	nextVersion := currentVersion + 1

	const insertQuery = `
		INSERT INTO events (id, stream_id, type, payload, metadata, version, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	for i, evt := range events {
		if evt == nil {
			return fmt.Errorf("event at index %d is nil", i)
		}

		payloadJSON, err := json.Marshal(evt.Payload)
		if err != nil {
			return apperrors.Wrapf(err, "marshal payload for event %d", i)
		}

		metadataJSON, err := json.Marshal(evt.Metadata)
		if err != nil {
			return apperrors.Wrapf(err, "marshal metadata for event %d", i)
		}

		// Use provided ID or generate a new one.
		eventID := evt.ID
		if eventID == "" {
			eventID = NewEventID()
		}

		// Use provided timestamp or current time.
		ts := evt.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}

		assignedVersion := nextVersion + int64(i)

		_, err = tx.ExecContext(ctx, insertQuery,
			eventID,
			streamID,
			string(evt.Type),
			payloadJSON,
			metadataJSON,
			assignedVersion,
			ts,
		)
		if err != nil {
			// Check for PostgreSQL unique violation (error code 23505)
			// which indicates a concurrent version conflict.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrVersionConflict
			}
			return apperrors.Wrapf(err, "insert event %d", i)
		}
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(err, "commit transaction")
	}
	committed = true

	return nil
}

// Read returns events for a single stream, filtered and ordered per opts.
func (s *PostgresEventStore) Read(
	ctx context.Context,
	streamID string,
	opts ReadOptions,
) ([]*Event, error) {
	if s == nil || s.pool == nil {
		return nil, ErrEventStoreClosed
	}
	if streamID == "" {
		return nil, apperrors.New("streamID must not be empty")
	}

	query, args := buildStreamReadQuery(streamID, opts)

	return s.queryEvents(ctx, query, args...)
}

// ReadAll returns events across all streams, ordered by created_at per opts.
func (s *PostgresEventStore) ReadAll(
	ctx context.Context,
	opts ReadOptions,
) ([]*Event, error) {
	if s == nil || s.pool == nil {
		return nil, ErrEventStoreClosed
	}

	query, args := buildAllReadQuery(opts)

	return s.queryEvents(ctx, query, args...)
}

// Subscribe returns a channel that receives events matching the filter.
// Polling interval is 1 second. The channel is closed when ctx is cancelled.
func (s *PostgresEventStore) Subscribe(
	ctx context.Context,
	filter EventFilter,
) (<-chan *Event, error) {
	if s == nil || s.pool == nil {
		return nil, ErrEventStoreClosed
	}

	ch := make(chan *Event, 1)

	go s.pollEvents(ctx, filter, ch)

	return ch, nil
}

// StreamVersion returns the current version of a stream.
// Returns 0 if the stream has no events.
func (s *PostgresEventStore) StreamVersion(
	ctx context.Context,
	streamID string,
) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, ErrEventStoreClosed
	}
	if streamID == "" {
		return 0, apperrors.New("streamID must not be empty")
	}

	var version int64
	err := s.pool.QueryRow(
		ctx,
		`SELECT COALESCE(MAX(version), 0) FROM events WHERE stream_id = $1`,
		streamID,
	).Scan(&version)
	if err != nil {
		return 0, apperrors.Wrap(err, "query stream version")
	}

	return version, nil
}

// queryEvents executes a query and scans the results into []*Event.
func (s *PostgresEventStore) queryEvents(
	ctx context.Context,
	query string,
	args ...any,
) ([]*Event, error) {
	mr, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, apperrors.Wrap(err, "query events")
	}
	defer func() {
		if closeErr := mr.Close(); closeErr != nil {
			slog.Warn("failed to close event query rows", "error", closeErr)
		}
	}()

	var result []*Event
	for mr.Next() {
		evt, err := scanEvent(mr.Rows)
		if err != nil {
			return nil, apperrors.Wrap(err, "scan event")
		}
		result = append(result, evt)
	}

	if err := mr.Err(); err != nil {
		return nil, apperrors.Wrap(err, "iterate events")
	}

	return result, nil
}

// scanEvent scans a single row into an Event.
func scanEvent(rows *sql.Rows) (*Event, error) {
	var (
		evt          Event
		typeStr      string
		payloadJSON  []byte
		metadataJSON []byte
	)

	err := rows.Scan(
		&evt.ID,
		&evt.StreamID,
		&typeStr,
		&payloadJSON,
		&metadataJSON,
		&evt.Version,
		&evt.Timestamp,
	)
	if err != nil {
		return nil, err
	}

	evt.Type = EventType(typeStr)

	if len(payloadJSON) > 0 {
		if err := json.Unmarshal(payloadJSON, &evt.Payload); err != nil {
			return nil, apperrors.Wrap(err, "unmarshal payload")
		}
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &evt.Metadata); err != nil {
			return nil, apperrors.Wrap(err, "unmarshal metadata")
		}
	}

	return &evt, nil
}

// buildStreamReadQuery constructs a parameterized query for reading a single stream.
func buildStreamReadQuery(streamID string, opts ReadOptions) (string, []any) {
	query := `SELECT id, stream_id, type, payload, metadata, version, created_at
		FROM events WHERE stream_id = $1`

	args := []any{streamID}
	argIdx := 2

	if opts.FromVersion > 0 {
		query += fmt.Sprintf(" AND version >= $%d", argIdx)
		args = append(args, opts.FromVersion)
		argIdx++
	}

	if !opts.Since.IsZero() {
		query += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, opts.Since)
		argIdx++
	}

	// Default to ascending.
	direction := "ASC"
	if opts.Direction == ReadDescending {
		direction = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY version %s", direction)

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
	}

	return query, args
}

// buildAllReadQuery constructs a parameterized query for reading all streams.
func buildAllReadQuery(opts ReadOptions) (string, []any) {
	query := `SELECT id, stream_id, type, payload, metadata, version, created_at
		FROM events WHERE 1=1`

	var args []any
	argIdx := 1

	if !opts.Since.IsZero() {
		query += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, opts.Since)
		argIdx++
	}

	// Default to ascending by created_at for cross-stream reads.
	direction := "ASC"
	if opts.Direction == ReadDescending {
		direction = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY created_at %s", direction)

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
	}

	return query, args
}

// pollEvents periodically queries for new events matching the filter and sends them to ch.
// The goroutine exits when ctx is cancelled.
func (s *PostgresEventStore) pollEvents(
	ctx context.Context,
	filter EventFilter,
	ch chan<- *Event,
) {
	defer close(ch)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Track the last seen timestamp to avoid re-delivering events.
	cursor := filter.Since
	if cursor.IsZero() {
		cursor = time.Now()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newCursor, err := s.pollOnce(ctx, filter, cursor, ch)
			if err != nil {
				slog.Error("event subscription poll failed", "error", err)
				continue
			}
			if !newCursor.IsZero() {
				cursor = newCursor
			}
		}
	}
}

// pollOnce executes a single poll cycle, returning the updated cursor.
func (s *PostgresEventStore) pollOnce(
	ctx context.Context,
	filter EventFilter,
	cursor time.Time,
	ch chan<- *Event,
) (time.Time, error) {
	query, args := buildSubscribeQuery(filter, cursor)

	events, err := s.queryEvents(ctx, query, args...)
	if err != nil {
		return cursor, err
	}

	newCursor := cursor
	for _, evt := range events {
		select {
		case ch <- evt:
			if evt.Timestamp.After(newCursor) {
				newCursor = evt.Timestamp
			}
		case <-ctx.Done():
			return newCursor, ctx.Err()
		}
	}

	return newCursor, nil
}

// buildSubscribeQuery constructs a parameterized query for the subscription poll.
func buildSubscribeQuery(filter EventFilter, cursor time.Time) (string, []any) {
	query := `SELECT id, stream_id, type, payload, metadata, version, created_at
		FROM events WHERE created_at > $1`

	args := []any{cursor}
	argIdx := 2

	if len(filter.StreamIDs) > 0 {
		query += fmt.Sprintf(" AND stream_id = ANY($%d)", argIdx)
		args = append(args, filter.StreamIDs)
		argIdx++
	}

	if len(filter.Types) > 0 {
		typeStrs := make([]string, len(filter.Types))
		for i, t := range filter.Types {
			typeStrs[i] = string(t)
		}
		query += fmt.Sprintf(" AND type = ANY($%d)", argIdx)
		args = append(args, typeStrs)
		argIdx++
	}

	query += " ORDER BY created_at ASC LIMIT 100"

	return query, args
}
