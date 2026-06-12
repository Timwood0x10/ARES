// Package postgres provides PostgreSQL database operations for the storage system.
package postgres

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"goagentx/internal/errors"
)

// EmbeddingReconciler provides eventual consistency for embedding operations.
// This scans for orphaned tasks where DB write succeeded but queue enqueue failed.
type EmbeddingReconciler struct {
	db               *Pool
	queue            *EmbeddingQueue
	embeddingConfig  *EmbeddingConfig
	interval         time.Duration
	missingThreshold time.Duration
	stopCh           chan struct{}
	stopOnce         sync.Once
}

// NewEmbeddingReconciler creates a new EmbeddingReconciler instance.
// Args:
// db - database connection pool.
// queue - embedding queue for re-enqueuing tasks.
// embeddingConfig - embedding configuration for model and version settings.
// interval - time between reconciliation scans.
// missingThreshold - time after which a task is considered orphaned.
// Returns new EmbeddingReconciler instance.
func NewEmbeddingReconciler(db *Pool, queue *EmbeddingQueue, embeddingConfig *EmbeddingConfig, interval, missingThreshold time.Duration) *EmbeddingReconciler {
	if embeddingConfig == nil {
		embeddingConfig = DefaultEmbeddingConfig()
	}
	return &EmbeddingReconciler{
		db:               db,
		queue:            queue,
		embeddingConfig:  embeddingConfig,
		interval:         interval,
		missingThreshold: missingThreshold,
		stopCh:           make(chan struct{}),
	}
}

// Start begins periodic reconciliation scanning.
// This runs until Stop is called or context is cancelled.
// Args:
// ctx - context for cancellation.
func (r *EmbeddingReconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping embedding reconciler due to context cancellation")
			return
		case <-r.stopCh:
			slog.Info("Stopping embedding reconciler due to Stop() call")
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			if err := r.Reconcile(ctx); err != nil {
				slog.Error("Embedding reconciliation failed", "error", err)
			}
		}
	}
}

// Reconcile scans for orphaned embedding tasks and re-enqueues them.
// This addresses the case where DB write succeeded but queue enqueue failed.
// Args:
// ctx - database operation context.
// Returns error if reconciliation fails.
func (r *EmbeddingReconciler) Reconcile(ctx context.Context) error {
	slog.Debug("Starting embedding reconciliation")

	// Use configured batch reconciliation limit
	batchLimit := r.embeddingConfig.ReconcileBatchSize

	// Find knowledge chunks with pending embedding status that haven't been processed recently
	query := `
		SELECT id, tenant_id, content
		FROM knowledge_chunks_1024
		WHERE embedding_status = 'pending'
		  AND embedding_queued_at < NOW() - $1
		  AND embedding_processed_at IS NULL
		LIMIT $2
	`

	rows, err := r.db.Query(ctx, query, r.missingThreshold, batchLimit)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	reconciledCount := 0
	for rows.Next() {
		var id, tenantID, content string
		if err := rows.Scan(&id, &tenantID, &content); err != nil {
			slog.Error("Failed to scan orphaned task", "error", err)
			continue
		}

		// Use configured default model and version
		// Re-enqueue the task
		task := &EmbeddingTask{
			TaskID:   id,
			Table:    "knowledge_chunks_1024",
			Content:  content,
			TenantID: tenantID,
			Model:    r.embeddingConfig.DefaultModel,
			Version:  r.embeddingConfig.DefaultVersion,
		}

		if err := r.queue.Enqueue(ctx, task); err != nil {
			if err == ErrDuplicateTask {
				slog.Debug("Task already queued, skipping duplicate", "task_id", id)
				continue
			}
			slog.Error("Failed to re-enqueue orphaned task", "task_id", id, "error", err)
			continue
		}

		// Update queued_at to prevent immediate re-scanning
		_, err = r.db.Exec(ctx, `
			UPDATE knowledge_chunks_1024
			SET embedding_queued_at = NOW()
			WHERE id = $1
		`, id)

		if err != nil {
			slog.Error("Failed to update queued_at for orphaned task", "task_id", id, "error", err)
			continue
		}

		reconciledCount++
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate orphaned tasks", "error", err)
		return errors.Wrap(err, "iterate orphaned tasks")
	}

	if reconciledCount > 0 {
		slog.Info("Reconciled orphaned embedding tasks", "count", reconciledCount)
	}

	return nil
}

// Stop gracefully shuts down the reconciler.
// This method is idempotent and safe to call multiple times.
func (r *EmbeddingReconciler) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
}
