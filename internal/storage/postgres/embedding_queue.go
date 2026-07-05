// nolint: errcheck // Operations may ignore return values
// Package postgres provides PostgreSQL database operations for the storage system.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
)

// ErrDuplicateTask is returned when Enqueue or EnqueueTx detects a duplicate
// task (same dedupe_key already exists in the queue).
var ErrDuplicateTask = stderrors.New("duplicate embedding task")

// EmbeddingQueue manages async embedding tasks with idempotency and retry logic.
// This provides eventual consistency for embedding operations using a database-backed queue.
type EmbeddingQueue struct {
	db              *Pool
	embeddingConfig *EmbeddingConfig
}

// EmbeddingTask represents a single embedding task.
type EmbeddingTask struct {
	TaskID   string
	Table    string
	Content  string
	TenantID string
	Model    string
	Version  int
	Kind     string // EmbeddingSpec.Kind for canonical spec tracking
	Prefix   string // EmbeddingSpec.Prefix for canonical spec tracking
	Dim      int    // EmbeddingSpec.Dim for canonical spec tracking
	SpecHash string // EmbeddingSpec.Hash for deduplication
}

// NewEmbeddingQueue creates a new EmbeddingQueue instance.
// Args:
// pool - database connection pool.
// embeddingConfig - embedding configuration for retry settings.
// Returns new EmbeddingQueue instance.
func NewEmbeddingQueue(pool *Pool, embeddingConfig *EmbeddingConfig) *EmbeddingQueue {
	if embeddingConfig == nil {
		embeddingConfig = DefaultEmbeddingConfig()
	}
	return &EmbeddingQueue{
		db:              pool,
		embeddingConfig: embeddingConfig,
	}
}

// Enqueue adds an embedding task to the queue with idempotency protection.
// This uses dedupe_key to prevent duplicate tasks for the same content.
// Returns ErrDuplicateTask if the task already exists (same dedupe_key).
// Args:
// ctx - database operation context.
// task - embedding task to enqueue.
// Returns ErrDuplicateTask if duplicate, or other error if enqueue fails.
func (q *EmbeddingQueue) Enqueue(ctx context.Context, task *EmbeddingTask) error {
	if task == nil {
		return errors.ErrInvalidArgument
	}

	// Generate dedupe key for idempotency.
	dedupeKey := q.generateDedupeKey(task)

	result, err := q.db.Exec(ctx, `
		INSERT INTO embedding_queue
		(task_id, table_name, content, tenant_id, embedding_model, embedding_version, dedupe_key, status, queued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', NOW())
		ON CONFLICT (dedupe_key) DO NOTHING
	`, task.TaskID, task.Table, task.Content, task.TenantID, task.Model, task.Version, dedupeKey)

	if err != nil {
		return errors.Wrap(err, "enqueue embedding task")
	}

	// RowsAffected == 0 means the dedupe_key already existed (duplicate).
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "check rows affected")
	}
	if rows == 0 {
		return ErrDuplicateTask
	}

	return nil
}

// EnqueueTx adds an embedding task to the queue within an existing transaction.
// This ensures the enqueue is committed atomically with the caller's transaction,
// preventing orphaned tasks if the transaction rolls back.
// Returns ErrDuplicateTask if the task already exists (same dedupe_key).
// Args:
// ctx - database operation context.
// tx - active database transaction.
// task - embedding task to enqueue.
// Returns ErrDuplicateTask if duplicate, or other error if enqueue fails.
func (q *EmbeddingQueue) EnqueueTx(ctx context.Context, tx *sql.Tx, task *EmbeddingTask) error {
	if task == nil {
		return errors.ErrInvalidArgument
	}
	if tx == nil {
		return fmt.Errorf("transaction is nil: %w", errors.ErrInvalidArgument)
	}

	// Generate dedupe key for idempotency.
	dedupeKey := q.generateDedupeKey(task)

	result, err := tx.ExecContext(ctx, `
		INSERT INTO embedding_queue
		(task_id, table_name, content, tenant_id, embedding_model, embedding_version, dedupe_key, status, queued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', NOW())
		ON CONFLICT (dedupe_key) DO NOTHING
	`, task.TaskID, task.Table, task.Content, task.TenantID, task.Model, task.Version, dedupeKey)

	if err != nil {
		return errors.Wrap(err, "enqueue embedding task in transaction")
	}

	// RowsAffected == 0 means the dedupe_key already existed (duplicate).
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "check rows affected")
	}
	if rows == 0 {
		return ErrDuplicateTask
	}

	return nil
}

// generateDedupeKey generates a unique key for idempotency.
// When SpecHash is set (from canonical EmbeddingSpec), it is used directly.
// Otherwise falls back to content|model|version for backward compatibility.
func (q *EmbeddingQueue) generateDedupeKey(task *EmbeddingTask) string {
	if task.SpecHash != "" {
		return task.SpecHash
	}
	key := fmt.Sprintf("%s|%s|%d", task.Content, task.Model, task.Version)
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:16])
}

// FetchPendingTasks retrieves pending embedding tasks with locking.
// Uses FOR UPDATE SKIP LOCKED inside a transaction so the row-level lock
// is held until the transaction commits, preventing other workers from
// picking up the same tasks.
// Args:
// ctx - database operation context.
// limit - maximum number of tasks to fetch.
// Returns list of pending tasks or error if fetch fails.
func (q *EmbeddingQueue) FetchPendingTasks(ctx context.Context, limit int) ([]*EmbeddingTask, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive: %w", errors.ErrInvalidArgument)
	}

	tx, err := q.db.Begin(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "begin fetch transaction")
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := tx.Rollback(); rbErr != nil {
				log.Error("Failed to rollback fetch transaction", "error", rbErr)
			}
		}
	}()

	rows, err := tx.QueryContext(ctx, `
		SELECT task_id, table_name, content, tenant_id, embedding_model, embedding_version
		FROM embedding_queue
		WHERE status = 'pending'
		  AND queued_at <= NOW()
		ORDER BY queued_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, errors.Wrap(err, "fetch pending tasks")
	}
	defer rows.Close()

	tasks := make([]*EmbeddingTask, 0)
	for rows.Next() {
		task := &EmbeddingTask{}
		if err := rows.Scan(&task.TaskID, &task.Table, &task.Content, &task.TenantID, &task.Model, &task.Version); err != nil {
			log.Error("Failed to scan embedding task row", "error", err)
			continue
		}
		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate embedding tasks")
	}

	// Mark fetched tasks as processing within the same transaction.
	for _, task := range tasks {
		_, err := tx.ExecContext(ctx, `
			UPDATE embedding_queue
			SET status = 'processing', processing_at = NOW()
			WHERE task_id = $1
		`, task.TaskID)
		if err != nil {
			return nil, errors.Wrap(err, "mark task processing")
		}
	}

	// Commit to release the locks and persist the processing status.
	if err := tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit fetch transaction")
	}
	committed = true

	return tasks, nil
}

// MarkProcessing marks a task as being processed.
// Args:
// ctx - database operation context.
// taskID - task identifier.
// Returns error if update fails.
func (q *EmbeddingQueue) MarkProcessing(ctx context.Context, taskID string) error {
	_, err := q.db.Exec(ctx, `
		UPDATE embedding_queue
		SET status = 'processing', processing_at = NOW()
		WHERE task_id = $1
	`, taskID)

	if err != nil {
		return errors.Wrap(err, "mark task processing")
	}

	return nil
}

// MarkCompleted marks a task as successfully completed.
// Args:
// ctx - database operation context.
// taskID - task identifier.
// Returns error if update fails.
func (q *EmbeddingQueue) MarkCompleted(ctx context.Context, taskID string) error {
	_, err := q.db.Exec(ctx, `
		UPDATE embedding_queue
		SET status = 'completed', completed_at = NOW()
		WHERE task_id = $1
	`, taskID)

	if err != nil {
		return errors.Wrap(err, "mark task completed")
	}

	return nil
}

// MarkFailed marks a task as failed and updates retry count.
// This implements exponential backoff for retries.
// Args:
// ctx - database operation context.
// taskID - task identifier.
// errMessage - error message to store.
// Returns error if update fails or task exceeded max retries.
func (q *EmbeddingQueue) MarkFailed(ctx context.Context, taskID string, errMessage string) error {
	// Get current retry count
	var retryCount int
	err := q.db.QueryRow(ctx, `
		SELECT retry_count FROM embedding_queue WHERE task_id = $1
	`, taskID).Scan(&retryCount)

	if err != nil {
		return errors.Wrap(err, "get retry count")
	}

	// Use configured max retries
	maxRetries := q.embeddingConfig.MaxRetries
	if retryCount >= maxRetries {
		// Move to dead letter queue
		_, err := q.db.Exec(ctx, `
			INSERT INTO embedding_dead_letter 
			(task_id, table_name, content, tenant_id, embedding_model, embedding_version, error_message, retry_count, created_at)
			SELECT task_id, table_name, content, tenant_id, embedding_model, embedding_version, $1, retry_count, created_at
			FROM embedding_queue WHERE task_id = $2
		`, errMessage, taskID)

		if err != nil {
			return errors.Wrap(err, "move to dead letter")
		}

		// Delete from main queue
		_, err = q.db.Exec(ctx, `DELETE FROM embedding_queue WHERE task_id = $1`, taskID)
		return err
	}

	// Increment retry count
	_, err = q.db.Exec(ctx, `
		UPDATE embedding_queue
		SET status = 'pending', retry_count = retry_count + 1, error_message = $1
		WHERE task_id = $2
	`, errMessage, taskID)

	if err != nil {
		return errors.Wrap(err, "mark task failed")
	}

	return nil
}

// Reconcile finds orphaned tasks that were never processed and re-enqueues them.
// This provides eventual consistency for tasks that were lost between DB write and queue enqueue.
// Args:
// ctx - database operation context.
// threshold - time threshold to consider a task orphaned.
// Returns error if reconciliation fails.
func (q *EmbeddingQueue) Reconcile(ctx context.Context, threshold time.Duration) error {
	if threshold <= 0 {
		return fmt.Errorf("threshold must be positive: %w", errors.ErrInvalidArgument)
	}

	// Use configured default model and version.
	defaultModel := q.embeddingConfig.DefaultModel
	defaultVersion := q.embeddingConfig.DefaultVersion

	// Convert threshold to microseconds for PostgreSQL interval arithmetic.
	thresholdMicros := threshold.Microseconds()

	// Fetch orphaned chunks, compute dedupe key in Go (same logic as generateDedupeKey),
	// then insert into the queue. We use a transaction to ensure atomicity.
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "begin reconcile transaction")
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := tx.Rollback(); rbErr != nil {
				log.Error("Failed to rollback reconcile transaction", "error", rbErr)
			}
		}
	}()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, content, tenant_id
		FROM knowledge_chunks_1024
		WHERE embedding_status = 'pending'
		  AND embedding_queued_at < NOW() - ($1 * INTERVAL '1 microsecond')
		  AND embedding_processed_at IS NULL
	`, thresholdMicros)
	if err != nil {
		return errors.Wrap(err, "query orphaned embeddings")
	}
	defer rows.Close()

	type orphanedChunk struct {
		ID       string
		Content  string
		TenantID string
	}
	var chunks []orphanedChunk
	for rows.Next() {
		var chunk orphanedChunk
		if err := rows.Scan(&chunk.ID, &chunk.Content, &chunk.TenantID); err != nil {
			log.Error("Failed to scan orphaned chunk row", "error", err)
			continue
		}
		chunks = append(chunks, chunk)
	}
	if err := rows.Err(); err != nil {
		return errors.Wrap(err, "iterate orphaned chunks")
	}

	// Insert each orphaned chunk into the queue with Go-computed dedupe key.
	for _, chunk := range chunks {
		dedupeKey := q.generateDedupeKey(&EmbeddingTask{
			Content: chunk.Content,
			Model:   defaultModel,
			Version: defaultVersion,
		})
		_, err := tx.ExecContext(ctx, `
			INSERT INTO embedding_queue
			(task_id, table_name, content, tenant_id, embedding_model, embedding_version, dedupe_key, status, queued_at)
			VALUES ($1, 'knowledge_chunks_1024', $2, $3, $4, $5, $6, 'pending', NOW())
			ON CONFLICT (dedupe_key) DO NOTHING
		`, chunk.ID, chunk.Content, chunk.TenantID, defaultModel, defaultVersion, dedupeKey)
		if err != nil {
			return errors.Wrap(err, "insert orphaned task into queue")
		}
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "commit reconcile transaction")
	}
	committed = true

	return nil
}
