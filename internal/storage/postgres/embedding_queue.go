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
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
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
//
// TaskID contract (REVIEW #13): TaskID MUST be the primary key of the source
// row in Table (e.g. the knowledge_chunks_1024.id or experiences_1024.id the
// vector will be written back to). The worker passes TaskID directly to
// UpdateEmbedding, and MarkCompleted/MarkFailed address rows by it. Producers
// that enqueue without knowing the row id yet must capture it (INSERT ...
// RETURNING id) before calling Enqueue. Empty TaskIDs are rejected.
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
	// SpecHash is the EmbeddingSpec.Hash, carried for traceability only. It is
	// deliberately NOT part of dedupe_key — see generateDedupeKey for why a
	// content-derived hash breaks the one-queue-row-per-source-row invariant.
	SpecHash string
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
//
// Idempotency is per source row (see generateDedupeKey): a row that already has
// a queue entry yields ErrDuplicateTask. The one exception is the revive branch
// in enqueueSQL — a *completed* entry whose content or embedding spec no longer
// matches the request is reset to pending in place, so re-embedding an edited
// row still works without ever creating a second entry for the same row.
// Args:
// ctx - database operation context.
// task - embedding task to enqueue.
// Returns ErrDuplicateTask if duplicate, or other error if enqueue fails.
func (q *EmbeddingQueue) Enqueue(ctx context.Context, task *EmbeddingTask) error {
	if task == nil {
		return errors.ErrInvalidArgument
	}
	if task.TaskID == "" {
		return fmt.Errorf("embedding task must carry the source row id in TaskID: %w", errors.ErrInvalidArgument)
	}

	// Generate dedupe key for idempotency.
	dedupeKey := q.generateDedupeKey(task)

	result, err := q.db.Exec(ctx, enqueueSQL,
		task.TaskID, task.Table, task.Content, task.TenantID, task.Model, task.Version, dedupeKey)

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

// enqueueSQL inserts a queue entry, or revives an already-completed entry for
// the same source row when the content or embedding spec changed.
//
// The DO UPDATE branch is guarded on status = 'completed' so it can never
// disturb an entry another worker is currently holding (pending/processing):
// those legitimately return ErrDuplicateTask. RowsAffected stays a correct
// duplicate signal because a DO UPDATE whose WHERE fails reports 0 rows.
const enqueueSQL = `
		INSERT INTO embedding_queue
		(task_id, table_name, content, tenant_id, embedding_model, embedding_version, dedupe_key, status, queued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', NOW())
		ON CONFLICT (dedupe_key) DO UPDATE SET
			content = EXCLUDED.content,
			embedding_model = EXCLUDED.embedding_model,
			embedding_version = EXCLUDED.embedding_version,
			status = 'pending',
			retry_count = 0,
			queued_at = NOW(),
			processing_at = NULL,
			completed_at = NULL,
			error_message = NULL
		WHERE embedding_queue.status = 'completed'
		  AND (embedding_queue.content <> EXCLUDED.content
		    OR embedding_queue.embedding_model <> EXCLUDED.embedding_model
		    OR embedding_queue.embedding_version <> EXCLUDED.embedding_version)
	`

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
	if task.TaskID == "" {
		return fmt.Errorf("embedding task must carry the source row id in TaskID: %w", errors.ErrInvalidArgument)
	}

	// Generate dedupe key for idempotency.
	dedupeKey := q.generateDedupeKey(task)

	result, err := tx.ExecContext(ctx, enqueueSQL,
		task.TaskID, task.Table, task.Content, task.TenantID, task.Model, task.Version, dedupeKey)

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
//
// The key is exactly (table, task_id, tenant_id): one source row owns at most
// one queue row, forever.
//
// Two earlier variants were both wrong:
//
//   - table|content|model|version made the semantics "this content is never
//     embedded twice, ever". Completed rows stay in embedding_queue, so a second
//     source row whose content happened to match an older one got
//     ErrDuplicateTask forever and its vector was never backfilled.
//   - adding content (or SpecHash, which is derived from content) on top of
//     task_id let one task_id own several queue rows. dedupe_key is the only
//     UNIQUE constraint here, while MarkProcessing/MarkCompleted/MarkFailed all
//     address rows by `WHERE task_id = $1` — with multiple rows per id a single
//     Mark* call rewrites all of them, and MarkFailed's SELECT ... FOR UPDATE
//     scans more than the row it locks.
//
// Anything that varies per attempt (content, model, version, spec) is therefore
// excluded. Re-embedding a row under a new model is handled by reviving the
// existing queue row in Enqueue/EnqueueTx, not by inserting a second one.
func (q *EmbeddingQueue) generateDedupeKey(task *EmbeddingTask) string {
	// Include the table name so identical ids in different tables never
	// collide on the same dedupe key (the queue is shared across tables).
	key := fmt.Sprintf("%s|%s|%s", task.Table, task.TaskID, task.TenantID)
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
	defer func() {
		if err := rows.Close(); err != nil {
			log.Warn("failed to close rows", "error", err)
		}
	}()

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
//
// task_id addresses exactly one queue row: dedupe_key is derived solely from
// (table, task_id, tenant_id), so a source row can never own two entries. All
// Mark* statements below rely on that invariant — see generateDedupeKey.
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
//
// The read-then-write sequence (SELECT retry_count then UPDATE) is wrapped in
// a single transaction with SELECT ... FOR UPDATE to prevent lost updates when
// two workers fail the same task concurrently. Without the row lock, both
// workers could read the same retry_count and the increment would be lost,
// causing infinite retries.
// Args:
// ctx - database operation context.
// taskID - task identifier.
// errMessage - error message to store.
// Returns error if update fails or task exceeded max retries.
func (q *EmbeddingQueue) MarkFailed(ctx context.Context, taskID string, errMessage string) error {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "begin mark failed transaction")
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := tx.Rollback(); rbErr != nil {
				log.Error("Failed to rollback mark failed transaction", "error", rbErr)
			}
		}
	}()

	// Lock the row for the duration of this transaction so concurrent
	// MarkFailed calls block until we commit, preventing lost updates.
	var retryCount int
	err = tx.QueryRowContext(ctx, `
		SELECT retry_count FROM embedding_queue WHERE task_id = $1 FOR UPDATE
	`, taskID).Scan(&retryCount)

	if err == sql.ErrNoRows {
		return errors.Wrap(errors.ErrRecordNotFound, "get retry count")
	}
	if err != nil {
		return errors.Wrap(err, "get retry count")
	}

	// Use configured max retries.
	maxRetries := q.embeddingConfig.MaxRetries
	if retryCount >= maxRetries {
		// Move to dead letter queue.
		//
		// created_at is taken from queued_at: embedding_queue has no created_at
		// column, so selecting one raised "column created_at does not exist",
		// which aborted the transaction and left the row pending with
		// retry_count already at the limit — the worker then picked it up,
		// failed, and hit this same broken statement forever. Reconcile's
		// dead-letter guard also depends on this INSERT actually landing.
		_, err = tx.ExecContext(ctx, `
			INSERT INTO embedding_dead_letter
			(task_id, table_name, content, tenant_id, embedding_model, embedding_version, error_message, retry_count, created_at)
			SELECT task_id, table_name, content, tenant_id, embedding_model, embedding_version, $1, retry_count, queued_at
			FROM embedding_queue WHERE task_id = $2
		`, errMessage, taskID)
		if err != nil {
			return errors.Wrap(err, "move to dead letter")
		}

		// Delete from main queue.
		_, err = tx.ExecContext(ctx, `DELETE FROM embedding_queue WHERE task_id = $1`, taskID)
		if err != nil {
			return errors.Wrap(err, "delete from queue")
		}
	} else {
		// Increment retry count and re-queue for processing.
		_, err = tx.ExecContext(ctx, `
			UPDATE embedding_queue
			SET status = 'pending', retry_count = retry_count + 1, error_message = $1
			WHERE task_id = $2
		`, errMessage, taskID)
		if err != nil {
			return errors.Wrap(err, "mark task failed")
		}
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "commit mark failed transaction")
	}
	committed = true

	return nil
}

// Reconcile finds orphaned rows whose vector was never written and re-enqueues
// them. This provides eventual consistency for tasks that were lost between the
// DB write and the queue enqueue.
//
// It covers both vector tables: knowledge_chunks_1024 (status-driven) and
// experiences_1024, whose embedding column is nullable so distillation can
// insert the row first. Without the experiences pass, a row whose enqueue and
// synchronous embed both failed would keep a NULL vector forever and stay
// invisible to vector search.
// Args:
// ctx - database operation context.
// threshold - time threshold to consider a task orphaned.
// Returns error if reconciliation fails.
func (q *EmbeddingQueue) Reconcile(ctx context.Context, threshold time.Duration) error {
	if threshold <= 0 {
		return fmt.Errorf("threshold must be positive: %w", errors.ErrInvalidArgument)
	}

	// Convert threshold to microseconds for PostgreSQL interval arithmetic.
	thresholdMicros := threshold.Microseconds()

	// Both passes share one transaction so a failure leaves the queue untouched.
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

	// Rows already given up on (max retries exceeded) are excluded from both
	// passes. MarkFailed deletes the queue entry when it dead-letters a task, so
	// without this guard the source row would look orphaned again on every
	// reconcile tick: re-enqueue -> fail -> dead-letter -> re-enqueue, burning
	// embedding quota forever on content that cannot be embedded.
	knowledgeQuery := `
		SELECT k.id, k.content, k.tenant_id
		FROM knowledge_chunks_1024 k
		WHERE k.embedding_status = 'pending'
		  AND k.embedding_queued_at < NOW() - ($1 * INTERVAL '1 microsecond')
		  AND k.embedding_processed_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM embedding_dead_letter d
			WHERE d.task_id = k.id::text
			  AND d.table_name = 'knowledge_chunks_1024'
		  )
	`
	if err := q.reconcileTable(ctx, tx, storage_models.KnowledgeChunksTable, knowledgeQuery, thresholdMicros); err != nil {
		return err
	}

	// An experience is orphaned when the row exists without a vector and no
	// live queue entry covers it. created_at is the only timestamp available
	// here, which is enough: the row is written immediately before the enqueue.
	//
	// 'processing' is excluded alongside 'pending': a worker holding the task
	// has not written the vector yet, so the row legitimately still has a NULL
	// embedding and must not be reset underneath that worker.
	experienceQuery := `
		SELECT e.id, e.input, e.tenant_id
		FROM experiences_1024 e
		WHERE e.embedding IS NULL
		  AND e.created_at < NOW() - ($1 * INTERVAL '1 microsecond')
		  AND NOT EXISTS (
			SELECT 1 FROM embedding_queue q
			WHERE q.task_id = e.id::text
			  AND q.table_name = 'experiences_1024'
			  AND q.status IN ('pending', 'processing')
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM embedding_dead_letter d
			WHERE d.task_id = e.id::text
			  AND d.table_name = 'experiences_1024'
		  )
	`
	if err := q.reconcileTable(ctx, tx, storage_models.ExperiencesTable, experienceQuery, thresholdMicros); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "commit reconcile transaction")
	}
	committed = true

	return nil
}

// reconcileTable re-enqueues the rows selected by query into the queue for
// table. query must select (id, content, tenant_id) and take the orphan
// threshold in microseconds as $1.
func (q *EmbeddingQueue) reconcileTable(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	query string,
	thresholdMicros int64,
) error {
	defaultModel := q.embeddingConfig.DefaultModel
	defaultVersion := q.embeddingConfig.DefaultVersion

	rows, err := tx.QueryContext(ctx, query, thresholdMicros)
	if err != nil {
		return errors.Wrap(err, "query orphaned embeddings for "+table)
	}

	type orphanedRow struct {
		ID       string
		Content  string
		TenantID string
	}
	// Rows are collected before inserting: reusing the same transaction for
	// writes while a cursor is open would interleave on one connection.
	var orphans []orphanedRow
	for rows.Next() {
		var row orphanedRow
		if err := rows.Scan(&row.ID, &row.Content, &row.TenantID); err != nil {
			log.Error("Failed to scan orphaned row", "table", table, "error", err)
			continue
		}
		orphans = append(orphans, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close() // best-effort: the iteration error is the one to report
		return errors.Wrap(err, "iterate orphaned rows for "+table)
	}
	if err := rows.Close(); err != nil {
		return errors.Wrap(err, "close orphaned rows for "+table)
	}

	for _, row := range orphans {
		// Dedupe key must be built exactly like Enqueue does, otherwise a
		// reconciled task would not deduplicate against a producer-side task.
		dedupeKey := q.generateDedupeKey(&EmbeddingTask{
			TaskID:   row.ID,
			Table:    table,
			Content:  row.Content,
			TenantID: row.TenantID,
			Model:    defaultModel,
			Version:  defaultVersion,
		})
		// Same revive-on-completed conflict handling as enqueueSQL, minus the
		// content/spec comparison: the orphan query already proved this source
		// row has no vector, so a 'completed' queue entry for it is stale
		// regardless of what content it holds. Plain DO NOTHING would strand
		// such a row forever. Entries in pending/processing are left alone —
		// they are either waiting or held by a worker.
		_, err := tx.ExecContext(ctx, `
			INSERT INTO embedding_queue
			(task_id, table_name, content, tenant_id, embedding_model, embedding_version, dedupe_key, status, queued_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', NOW())
			ON CONFLICT (dedupe_key) DO UPDATE SET
				content = EXCLUDED.content,
				embedding_model = EXCLUDED.embedding_model,
				embedding_version = EXCLUDED.embedding_version,
				status = 'pending',
				retry_count = 0,
				queued_at = NOW(),
				processing_at = NULL,
				completed_at = NULL,
				error_message = NULL
			WHERE embedding_queue.status = 'completed'
		`, row.ID, table, row.Content, row.TenantID, defaultModel, defaultVersion, dedupeKey)
		if err != nil {
			return errors.Wrap(err, "insert orphaned task into queue")
		}
	}

	return nil
}
