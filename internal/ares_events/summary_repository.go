package ares_events

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// PgSummaryRepository implements SummaryRepository using PostgreSQL.
// Event summaries are stored in the `event_summaries` relational table (not vector DB).
type PgSummaryRepository struct {
	pool *postgres.Pool
}

// NewPgSummaryRepository creates a new PostgreSQL-backed summary repository.
func NewPgSummaryRepository(pool *postgres.Pool) *PgSummaryRepository {
	return &PgSummaryRepository{pool: pool}
}

// Save persists an event summary to the database.
func (r *PgSummaryRepository) Save(ctx context.Context, summary *EventSummary) error {
	if r == nil || r.pool == nil {
		return ErrEventStoreClosed
	}
	if summary.ID == "" {
		summary.ID = NewEventSummaryID()
	}
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = time.Now()
	}

	eventTypeCountsJSON, _ := json.Marshal(summary.EventTypeCounts)
	tasksCreatedJSON, _ := json.Marshal(summary.TasksCreated)
	toolsCalledJSON, _ := json.Marshal(summary.ToolsCalled)
	errorsJSON, _ := json.Marshal(summary.Errors)

	query := `
		INSERT INTO event_summaries (
			id, stream_id, agent_id, task_id, session_id, user_id,
			summary_text, event_count, start_version, end_version,
			start_time, end_time, event_type_counts,
			tasks_created, tools_called, errors,
			request_summary, outcome, metadata, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT (id) DO UPDATE SET
			summary_text = EXCLUDED.summary_text,
			event_count = EXCLUDED.event_count,
			end_version = EXCLUDED.end_version,
			end_time = EXCLUDED.end_time,
			event_type_counts = EXCLUDED.event_type_counts,
			tasks_created = EXCLUDED.tasks_created,
			tools_called = EXCLUDED.tools_called,
			errors = EXCLUDED.errors,
			outcome = EXCLUDED.outcome,
			metadata = EXCLUDED.metadata
	`

	_, err := r.pool.Exec(ctx, query,
		summary.ID,
		summary.StreamID,
		summary.AgentID,
		summary.TaskID,
		summary.SessionID,
		summary.UserID,
		summary.SummaryText,
		summary.EventCount,
		summary.StartVersion,
		summary.EndVersion,
		summary.StartTime,
		summary.EndTime,
		eventTypeCountsJSON,
		tasksCreatedJSON,
		toolsCalledJSON,
		errorsJSON,
		summary.RequestSummary,
		summary.Outcome,
		summary.Metadata,
		summary.CreatedAt,
	)
	if err != nil {
		return apperrors.Wrap(err, "insert event summary")
	}
	return nil
}

// FindByStreamID returns all summaries for a stream, ordered by start_version ascending.
func (r *PgSummaryRepository) FindByStreamID(ctx context.Context, streamID string) ([]*EventSummary, error) {
	if r == nil || r.pool == nil {
		return nil, ErrEventStoreClosed
	}

	query := `
		SELECT id, stream_id, agent_id, task_id, session_id, user_id,
			summary_text, event_count, start_version, end_version,
			start_time, end_time, event_type_counts,
			tasks_created, tools_called, errors,
			request_summary, outcome, metadata, created_at
		FROM event_summaries
		WHERE stream_id = $1
		ORDER BY start_version ASC
	`

	rows, err := r.pool.Query(ctx, query, streamID)
	if err != nil {
		return nil, apperrors.Wrap(err, "query event summaries by stream")
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Error("close event summaries rows failed", "err", err)
		}
	}()

	var summaries []*EventSummary
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, apperrors.Wrap(err, "scan event summary")
		}
		summaries = append(summaries, s)
	}

	return summaries, nil
}

// FindByAgentAndTask returns summaries matching both agent and task.
func (r *PgSummaryRepository) FindByAgentAndTask(ctx context.Context, agentID, taskID string) ([]*EventSummary, error) {
	if r == nil || r.pool == nil {
		return nil, ErrEventStoreClosed
	}

	query := `
		SELECT id, stream_id, agent_id, task_id, session_id, user_id,
			summary_text, event_count, start_version, end_version,
			start_time, end_time, event_type_counts,
			tasks_created, tools_called, errors,
			request_summary, outcome, metadata, created_at
		FROM event_summaries
		WHERE agent_id = $1 AND task_id = $2
		ORDER BY start_version ASC
	`

	rows, err := r.pool.Query(ctx, query, agentID, taskID)
	if err != nil {
		return nil, apperrors.Wrap(err, "query event summaries by agent+task")
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Error("close event summaries rows failed", "err", err)
		}
	}()

	var summaries []*EventSummary
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, apperrors.Wrap(err, "scan event summary")
		}
		summaries = append(summaries, s)
	}

	return summaries, nil
}

// FindByAgentID returns all summaries for an agent across all streams/tasks.
func (r *PgSummaryRepository) FindByAgentID(ctx context.Context, agentID string) ([]*EventSummary, error) {
	if r == nil || r.pool == nil {
		return nil, ErrEventStoreClosed
	}

	query := `
		SELECT id, stream_id, agent_id, task_id, session_id, user_id,
			summary_text, event_count, start_version, end_version,
			start_time, end_time, event_type_counts,
			tasks_created, tools_called, errors,
			request_summary, outcome, metadata, created_at
		FROM event_summaries
		WHERE agent_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, agentID)
	if err != nil {
		return nil, apperrors.Wrap(err, "query event summaries by agent")
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Error("close event summaries rows failed", "err", err)
		}
	}()

	var summaries []*EventSummary
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, apperrors.Wrap(err, "scan event summary")
		}
		summaries = append(summaries, s)
	}

	return summaries, nil
}

// FindLatestByStreamID returns the most recent summary for a stream.
func (r *PgSummaryRepository) FindLatestByStreamID(ctx context.Context, streamID string) (*EventSummary, error) {
	if r == nil || r.pool == nil {
		return nil, ErrEventStoreClosed
	}

	query := `
		SELECT id, stream_id, agent_id, task_id, session_id, user_id,
			summary_text, event_count, start_version, end_version,
			start_time, end_time, event_type_counts,
			tasks_created, tools_called, errors,
			request_summary, outcome, metadata, created_at
		FROM event_summaries
		WHERE stream_id = $1
		ORDER BY end_version DESC
		LIMIT 1
	`

	row := r.pool.QueryRow(ctx, query, streamID)

	s, err := scanSummary(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSummaryNotFound
		}
		return nil, apperrors.Wrap(err, "scan event summary")
	}
	return s, nil
}

// Delete removes a summary by ID.
func (r *PgSummaryRepository) Delete(ctx context.Context, id string) error {
	if r == nil || r.pool == nil {
		return ErrEventStoreClosed
	}

	_, err := r.pool.Exec(ctx, `DELETE FROM event_summaries WHERE id = $1`, id)
	if err != nil {
		return apperrors.Wrap(err, "delete event summary")
	}
	return nil
}

// DeleteOlderThan removes summaries created before the given threshold.
// Returns the number of rows deleted.
func (r *PgSummaryRepository) DeleteOlderThan(ctx context.Context, threshold time.Time) (int64, error) {
	if r == nil || r.pool == nil {
		return 0, ErrEventStoreClosed
	}

	result, err := r.pool.Exec(ctx,
		`DELETE FROM event_summaries WHERE created_at < $1`, threshold,
	)
	if err != nil {
		return 0, apperrors.Wrap(err, "delete old event summaries")
	}
	return result.RowsAffected()
}

// --- Row scanning helpers ---

// summaryScanner is the common interface for both sql.Row and ManagedRows.
type summaryScanner interface {
	Scan(dest ...any) error
}

func scanSummary(s summaryScanner) (*EventSummary, error) {
	var summary EventSummary
	var (
		eventTypeCountsJSON []byte
		tasksCreatedJSON    []byte
		toolsCalledJSON     []byte
		errorsJSON          []byte
		metadataJSON        []byte
	)

	err := s.Scan(
		&summary.ID,
		&summary.StreamID,
		&summary.AgentID,
		&summary.TaskID,
		&summary.SessionID,
		&summary.UserID,
		&summary.SummaryText,
		&summary.EventCount,
		&summary.StartVersion,
		&summary.EndVersion,
		&summary.StartTime,
		&summary.EndTime,
		&eventTypeCountsJSON,
		&tasksCreatedJSON,
		&toolsCalledJSON,
		&errorsJSON,
		&summary.RequestSummary,
		&summary.Outcome,
		&metadataJSON,
		&summary.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if len(eventTypeCountsJSON) > 0 {
		if err := json.Unmarshal(eventTypeCountsJSON, &summary.EventTypeCounts); err != nil {
			return nil, apperrors.Wrap(err, "unmarshal event type counts")
		}
	}
	if len(tasksCreatedJSON) > 0 {
		if err := json.Unmarshal(tasksCreatedJSON, &summary.TasksCreated); err != nil {
			return nil, apperrors.Wrap(err, "unmarshal tasks created")
		}
	}
	if len(toolsCalledJSON) > 0 {
		if err := json.Unmarshal(toolsCalledJSON, &summary.ToolsCalled); err != nil {
			return nil, apperrors.Wrap(err, "unmarshal tools called")
		}
	}
	if len(errorsJSON) > 0 {
		if err := json.Unmarshal(errorsJSON, &summary.Errors); err != nil {
			return nil, apperrors.Wrap(err, "unmarshal errors")
		}
	}
	if len(metadataJSON) > 0 {
		summary.Metadata = metadataJSON
	}

	return &summary, nil
}
