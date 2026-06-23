package leader

import (
	"context"
	"database/sql"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// taskResultsTable is the table name for task results.
// Configurable to support different environments.
const taskResultsTable = "task_results_1024"

// StaleTask represents a task orphaned by leader failure.
type StaleTask struct {
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Error     string `json:"error"`
}

// TaskRecovery handles orphaned task recovery after leader failover.
type TaskRecovery struct {
	pool *postgres.Pool
}

// NewTaskRecovery creates a TaskRecovery.
func NewTaskRecovery(pool *postgres.Pool) *TaskRecovery {
	return &TaskRecovery{pool: pool}
}

// RecoverStaleTasks finds tasks with status pending/running and no output
// for the given session, marks them as failed.
//
// Args:
//
//	ctx - timeout and cancellation context.
//	sessionID - the recovered session identifier.
//
// Returns:
//
//	staleTasks - list of tasks marked as failed.
//	err - database error.
func (r *TaskRecovery) RecoverStaleTasks(ctx context.Context, sessionID string) ([]*StaleTask, error) {
	if r.pool == nil {
		return nil, errors.New("task recovery: pool not initialized")
	}
	if sessionID == "" {
		return nil, errors.New("task recovery: empty session ID")
	}

	// The Output column stores JSON, so IS NULL checks cover the empty-output case.
	query := `
		UPDATE ` + taskResultsTable + `
		SET status = 'failed',
		    error = 'leader failover: task orphaned'
		WHERE session_id = $1
		  AND status IN ('pending', 'running')
		  AND output IS NULL
		RETURNING id, session_id, status, error
	`

	rows, err := r.pool.Query(ctx, query, sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "recover stale tasks")
	}
	defer func() { _ = rows.Close() }()

	var staleTasks []*StaleTask
	for rows.Next() {
		var task StaleTask
		var errStr sql.NullString
		if scanErr := rows.Scan(&task.TaskID, &task.SessionID, &task.Status, &errStr); scanErr != nil {
			if ctx.Err() != nil {
				break
			}
			return nil, errors.Wrap(scanErr, "recover stale tasks: scan")
		}
		if errStr.Valid {
			task.Error = errStr.String
		}
		staleTasks = append(staleTasks, &task)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, errors.Wrap(iterErr, "recover stale tasks: iterate")
	}

	return staleTasks, nil
}
