package evalapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"goagentx/internal/errors"
	"goagentx/internal/storage/postgres"
)

// EvalResultRepository defines the data access interface for evaluation results.
type EvalResultRepository interface {
	// Store persists a single evaluation result.
	Store(ctx context.Context, result *EvalResult) error

	// StoreBatch persists multiple evaluation results in a single transaction.
	StoreBatch(ctx context.Context, results []*EvalResult) error

	// GetByRunID retrieves all results for a given run ID.
	GetByRunID(ctx context.Context, runID string) ([]*EvalResult, error)

	// GetLeaderboard returns ranked entries across all runs.
	GetLeaderboard(ctx context.Context, limit, offset int) ([]*LeaderboardEntry, int, error)

	// GetComparison retrieves side-by-side results for given run IDs.
	GetComparison(ctx context.Context, runIDs []string) ([]*ComparisonRow, error)
}

// pgEvalResultRepository implements EvalResultRepository using PostgreSQL.
type pgEvalResultRepository struct {
	db   postgres.DBTX
	pool *sql.DB // retained for transaction operations (BeginTx)
}

// NewPGEvalResultRepository creates a new PostgreSQL-backed eval result repository.
//
// Args:
//
//	db - database connection or transaction implementing postgres.DBTX.
//	pool - underlying *sql.DB pool for transaction BeginTx (may be nil when db is already a tx).
//
// Returns:
//
//	*pgEvalResultRepository - the repository instance.
func NewPGEvalResultRepository(db postgres.DBTX, pool *sql.DB) *pgEvalResultRepository {
	return &pgEvalResultRepository{db: db, pool: pool}
}

// Store inserts a single evaluation result into the database.
func (r *pgEvalResultRepository) Store(ctx context.Context, result *EvalResult) error {
	dimensionsJSON, err := json.Marshal(result.Dimensions)
	if err != nil {
		return errors.Wrap(err, "marshal dimensions")
	}

	query := `
		INSERT INTO eval_results
			(id, run_id, config_name, suite_name, test_case_id, test_case_name,
			 score, dimensions, status, error_message, duration_ms, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12, $13)
		ON CONFLICT (run_id, config_name, test_case_id)
		DO UPDATE SET
			score = EXCLUDED.score,
			dimensions = EXCLUDED.dimensions,
			status = EXCLUDED.status,
			error_message = EXCLUDED.error_message,
			duration_ms = EXCLUDED.duration_ms,
			updated_at = NOW()
	`

	errMsg := result.ErrorMessage
	if errMsg == nil {
		errMsg = new(string)
	}

	_, err = r.db.ExecContext(ctx, query,
		result.ID, result.RunID, result.ConfigName, result.SuiteName,
		result.TestCaseID, result.TestCaseName, result.Score,
		dimensionsJSON, result.Status, *errMsg, result.DurationMs,
		result.CreatedAt, result.UpdatedAt,
	)
	if err != nil {
		return errors.Wrap(err, "store eval result")
	}

	return nil
}

// StoreBatch persists multiple evaluation results atomically within a single
// transaction. If any individual insert fails, the entire batch is rolled back.
func (r *pgEvalResultRepository) StoreBatch(ctx context.Context, results []*EvalResult) error {
	if len(results) == 0 {
		return nil
	}

	if r.pool == nil {
		// No pool available (db may already be a transaction); fall back
		// to non-atomic loop. The caller should provide a pool for atomicity.
		for _, result := range results {
			if err := r.Store(ctx, result); err != nil {
				return fmt.Errorf("store batch: %w", err)
			}
		}
		return nil
	}

	tx, err := r.pool.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin batch transaction")
	}

	txRepo := NewPGEvalResultRepository(tx, nil)
	for _, result := range results {
		if err := txRepo.Store(ctx, result); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store batch (rolled back): %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "commit batch")
	}

	return nil
}

// GetByRunID retrieves all results for a given run ID, ordered by config_name
// and then by test_case_id for consistent ordering.
func (r *pgEvalResultRepository) GetByRunID(ctx context.Context, runID string) ([]*EvalResult, error) {
	if runID == "" {
		return nil, ErrInvalidRunID
	}

	query := `
		SELECT id, run_id, config_name, suite_name, test_case_id, test_case_name,
			   score, dimensions::text, status, error_message, duration_ms,
			   created_at, updated_at
		FROM eval_results
		WHERE run_id = $1
		ORDER BY config_name, test_case_id
	`

	rows, err := r.db.QueryContext(ctx, query, runID)
	if err != nil {
		if err == sql.ErrNoRows {
			return []*EvalResult{}, nil
		}
		return nil, errors.Wrap(err, "get results by run_id")
	}
	defer func() { _ = rows.Close() }()

	results := make([]*EvalResult, 0)
	for rows.Next() {
		result, err := scanEvalResult(rows)
		if err != nil {
			slog.Warn("failed to scan eval result row", "error", err)
			continue
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate eval results")
	}

	return results, nil
}

// GetLeaderboard returns ranked leaderboard entries across all evaluation runs.
// Entries are ordered by overall_score DESC (best first), then by most recent run.
// The returned count is the total number of distinct config entries before pagination.
func (r *pgEvalResultRepository) GetLeaderboard(ctx context.Context, limit, offset int) ([]*LeaderboardEntry, int, error) {
	// First get total count of distinct (run_id, config_name) groups.
	countQuery := `
		SELECT COUNT(DISTINCT (run_id, config_name))
		FROM eval_results
	`
	var totalCount int
	if err := r.db.QueryRowContext(ctx, countQuery).Scan(&totalCount); err != nil {
		return nil, 0, errors.Wrap(err, "count leaderboard entries")
	}

	// Query aggregated scores per (run_id, config_name), ordered by avg score desc.
	query := `
		SELECT run_id, config_name,
			   AVG(score) as overall_score,
			   COUNT(*) FILTER (WHERE status = 'pass')::float / NULLIF(COUNT(*), 0) as pass_rate,
			   COUNT(*) as total_tests,
			   AVG(duration_ms)::int as avg_duration_ms
		FROM eval_results
		GROUP BY run_id, config_name
		ORDER BY overall_score DESC, run_id DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, errors.Wrap(err, "query leaderboard")
	}
	defer func() { _ = rows.Close() }()

	entries := make([]*LeaderboardEntry, 0)
	rank := offset + 1
	for rows.Next() {
		entry := &LeaderboardEntry{}
		if err := rows.Scan(
			&entry.RunID, &entry.ConfigName, &entry.OverallScore,
			&entry.PassRate, &entry.TotalTests, &entry.AvgDurationMs,
		); err != nil {
			slog.Warn("failed to scan leaderboard row", "error", err)
			continue
		}
		entry.Rank = rank
		rank++
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, errors.Wrap(err, "iterate leaderboard")
	}

	return entries, totalCount, nil
}

// GetComparison retrieves side-by-side comparison data for the given run IDs.
// Each ComparisonRow represents one test case with per-run results.
func (r *pgEvalResultRepository) GetComparison(ctx context.Context, runIDs []string) ([]*ComparisonRow, error) {
	if len(runIDs) == 0 {
		return nil, ErrEmptyRunIDs
	}

	// Build placeholder list for IN clause.
	placeholders := make([]string, len(runIDs))
	args := make([]any, len(runIDs))
	for i, id := range runIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	inClause := strings.Join(placeholders, ",")

	query := fmt.Sprintf(`
		SELECT test_case_id, test_case_name, run_id, config_name,
			   score, status, duration_ms
		FROM eval_results
		WHERE run_id IN (%s)
		ORDER BY test_case_id, run_id, config_name
	`, inClause)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrap(err, "query comparison")
	}
	defer func() { _ = rows.Close() }()

	// Group results by test case ID.
	rowMap := make(map[string]*ComparisonRow)
	for rows.Next() {
		var testCaseID, testCaseName, runID, configName, status string
		var score float64
		var durationMs int

		if err := rows.Scan(&testCaseID, &testCaseName, &runID, &configName,
			&score, &status, &durationMs); err != nil {
			slog.Warn("failed to scan comparison row", "error", err)
			continue
		}

		row, ok := rowMap[testCaseID]
		if !ok {
			row = &ComparisonRow{
				TestCaseID:   testCaseID,
				TestCaseName: testCaseName,
				Results:      make(map[string]ComparisonCell),
			}
			rowMap[testCaseID] = row
		}

		cellKey := runID + ":" + configName
		row.Results[cellKey] = ComparisonCell{
			ConfigName: configName,
			Score:      score,
			Status:     status,
			DurationMs: durationMs,
		}
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate comparison rows")
	}

	// Convert map to ordered slice.
	result := make([]*ComparisonRow, 0, len(rowMap))
	for _, row := range rowMap {
		result = append(result, row)
	}

	return result, nil
}

// scanEvalResult scans a single eval result from a Scannable row.
func scanEvalResult(scanner interface {
	Scan(dest ...any) error
}) (*EvalResult, error) {
	result := &EvalResult{}
	var dimensionsStr, errMsg sql.NullString

	err := scanner.Scan(
		&result.ID, &result.RunID, &result.ConfigName, &result.SuiteName,
		&result.TestCaseID, &result.TestCaseName, &result.Score,
		&dimensionsStr, &result.Status, &errMsg, &result.DurationMs,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if dimensionsStr.Valid && dimensionsStr.String != "" {
		var dims map[string]float64
		if err := json.Unmarshal([]byte(dimensionsStr.String), &dims); err == nil {
			result.Dimensions = dims
		} else {
			result.Dimensions = make(map[string]float64)
		}
	} else {
		result.Dimensions = make(map[string]float64)
	}

	if errMsg.Valid && errMsg.String != "" {
		s := errMsg.String
		result.ErrorMessage = &s
	}

	return result, nil
}

// Ensure compile-time interface check.
var _ EvalResultRepository = (*pgEvalResultRepository)(nil)

// RepositoryConfig holds configuration for creating an eval repository.
type RepositoryConfig struct {
	// DB is the database connection pool.
	DB postgres.DBTX
}

// DefaultRepositoryConfig returns sensible defaults for repository configuration.
func DefaultRepositoryConfig() *RepositoryConfig {
	return &RepositoryConfig{}
}
