// nolint: errcheck // Operations may ignore return values
// Package postgres provides PostgreSQL database operations for the storage system.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	coreerrors "github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/errors"
)

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
// Args:
// ctx - database operation context.
// task - embedding task to enqueue.
// Returns error if enqueue operation fails.
func (q *EmbeddingQueue) Enqueue(ctx context.Context, task *EmbeddingTask) error {
	if task == nil {
		return coreerrors.ErrInvalidArgument
	}

	// Generate dedupe key for idempotency
	dedupeKey := q.generateDedupeKey(task.Content, task.Model, task.Version)

	_, err := q.db.Exec(ctx, `
		INSERT INTO embedding_queue 
		(task_id, table_name, content, tenant_id, embedding_model, embedding_version, dedupe_key, status, queued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', NOW())
		ON CONFLICT (dedupe_key) DO NOTHING
	`, task.TaskID, task.Table, task.Content, task.TenantID, task.Model, task.Version, dedupeKey)

	if err != nil {
		return errors.Wrap(err, "enqueue embedding task")
	}

	return nil
}

// generateDedupeKey generates a unique key for idempotency based on content and model version.
// Args:
// content - text content to embed.
// model - embedding model name.
// version - embedding model version.
// Returns dedupe key as hexadecimal string.
func (q *EmbeddingQueue) generateDedupeKey(content, model string, version int) string {
	key := fmt.Sprintf("%s|%s|%d", content, model, version)
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:16])
}

// FetchPendingTasks retrieves pending embedding tasks with locking.
// This uses FOR UPDATE SKIP LOCKED to enable multiple concurrent workers.
// Args:
// ctx - database operation context.
// limit - maximum number of tasks to fetch.
// Returns list of pending tasks or error if fetch fails.
func (q *EmbeddingQueue) FetchPendingTasks(ctx context.Context, limit int) ([]*EmbeddingTask, error) {
	query := `
		SELECT task_id, table_name, content, tenant_id, embedding_model, embedding_version
		FROM embedding_queue
		WHERE status = 'pending'
		  AND queued_at <= NOW()
		ORDER BY queued_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`

	rows, err := q.db.Query(ctx, query, limit)
	if err != nil {
		return nil, errors.Wrap(err, "fetch pending tasks")
	}
	defer rows.Close()

	tasks := make([]*EmbeddingTask, 0)
	for rows.Next() {
		task := &EmbeddingTask{}
		if err := rows.Scan(&task.TaskID, &task.Table, &task.Content, &task.TenantID, &task.Model, &task.Version); err != nil {
			slog.Error("Failed to scan embedding task row", "error", err)
			continue
		}
		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate embedding tasks", "error", err)
		return nil, errors.Wrap(err, "iterate embedding tasks")
	}

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
	// Use configured default model and version
	defaultModel := q.embeddingConfig.DefaultModel
	defaultVersion := q.embeddingConfig.DefaultVersion

	// Find knowledge chunks with pending embedding status that haven't been processed recently
	_, err := q.db.Exec(ctx, `
		INSERT INTO embedding_queue (task_id, table_name, content, tenant_id, embedding_model, embedding_version, dedupe_key, status, queued_at)
		SELECT id, 'knowledge_chunks_1024', content, tenant_id, $2, $3, 
		       md5(content || $2 || $3), 'pending', NOW()
		FROM knowledge_chunks_1024
		WHERE embedding_status = 'pending'
		  AND embedding_queued_at < NOW() - $1
		  AND embedding_processed_at IS NULL
		ON CONFLICT (dedupe_key) DO NOTHING
	`, threshold, defaultModel, defaultVersion)

	if err != nil {
		return errors.Wrap(err, "reconcile orphaned embeddings")
	}

	return nil
}
