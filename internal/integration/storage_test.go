// package integration provides end-to-end integration tests with real PostgreSQL.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// TestWriteBufferBatchFlush verifies the full WriteBuffer pipeline:
// batch write -> flush -> verify rows in DB.
func TestWriteBufferBatchFlush(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "embedding_queue", "embedding_dead_letter")
	})

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	queue := postgres.NewEmbeddingQueue(pool, embeddingConfig)

	// Create a WriteBuffer with batch size 2 and short flush interval.
	buf := postgres.NewWriteBuffer(pool, queue, 2, 200*time.Millisecond, embeddingConfig)
	require.NoError(t, buf.Start(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, buf.Stop(stopCtx))
	}()

	// Write two items to trigger a batch flush.
	tenantID := fmt.Sprintf("test-wb-%d", time.Now().UnixNano())
	for i := 0; i < 2; i++ {
		item := &postgres.WriteItem{
			TenantID: tenantID,
			Table:    "knowledge_chunks_1024",
			Content:  fmt.Sprintf("write-buffer-test-content-%d-%d", i, time.Now().UnixNano()),
			Metadata: map[string]interface{}{"source": "integration-test"},
		}
		require.NoError(t, buf.Write(ctx, item))
	}

	// Wait for the batch to flush.
	time.Sleep(500 * time.Millisecond)

	// Verify that embedding tasks were enqueued.
	rows, err := pool.Query(ctx,
		"SELECT task_id, table_name, tenant_id, status FROM embedding_queue WHERE tenant_id = $1",
		tenantID,
	)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var taskCount int
	for rows.Next() {
		var taskID, tableName, tid, status string
		require.NoError(t, rows.Scan(&taskID, &tableName, &tid, &status))
		assert.Equal(t, "knowledge_chunks_1024", tableName)
		assert.Equal(t, tenantID, tid)
		assert.Equal(t, "pending", status)
		taskCount++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 2, taskCount, "expected 2 embedding tasks to be enqueued")
}

// TestWriteBufferNilItem verifies that writing a nil item returns an error.
func TestWriteBufferNilItem(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	queue := postgres.NewEmbeddingQueue(pool, embeddingConfig)
	buf := postgres.NewWriteBuffer(pool, queue, 10, 1*time.Second, embeddingConfig)

	err := buf.Write(ctx, nil)
	require.Error(t, err, "expected error when writing nil item")
}

// TestEmbeddingQueueEnqueueAndFetch verifies the EmbeddingQueue pipeline:
// EnqueueTx within transaction -> FetchPendingTasks -> MarkComplete.
func TestEmbeddingQueueEnqueueAndFetch(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "embedding_queue", "embedding_dead_letter")
	})

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	queue := postgres.NewEmbeddingQueue(pool, embeddingConfig)

	// Enqueue a task within a transaction.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	task := &postgres.EmbeddingTask{
		TaskID:   "",
		Table:    "knowledge_chunks_1024",
		Content:  fmt.Sprintf("queue-test-content-%d", time.Now().UnixNano()),
		TenantID: "test-tenant",
		Model:    embeddingConfig.DefaultModel,
		Version:  embeddingConfig.DefaultVersion,
	}
	require.NoError(t, queue.EnqueueTx(ctx, tx, task))
	require.NoError(t, tx.Commit())

	// Fetch pending tasks.
	tasks, err := queue.FetchPendingTasks(ctx, 10)
	require.NoError(t, err)
	require.NotEmpty(t, tasks, "expected at least one pending task")

	// Verify the fetched task.
	found := false
	for _, pending := range tasks {
		if pending.TenantID == "test-tenant" && pending.Table == "knowledge_chunks_1024" {
			found = true
			assert.Equal(t, embeddingConfig.DefaultModel, pending.Model)
			assert.Equal(t, embeddingConfig.DefaultVersion, pending.Version)

			// Mark the task as completed.
			require.NoError(t, queue.MarkCompleted(ctx, pending.TaskID))
			break
		}
	}
	assert.True(t, found, "expected to find the enqueued task in pending tasks")

	// Verify the task is no longer pending.
	tasks2, err := queue.FetchPendingTasks(ctx, 10)
	require.NoError(t, err)
	for _, pending := range tasks2 {
		if pending.TenantID == "test-tenant" {
			t.Errorf("task should have been marked completed, but was still pending: %s", pending.TaskID)
		}
	}
}

// TestEmbeddingQueueIdempotency verifies that duplicate enqueue with the same
// content/model/version is deduplicated via the dedupe_key.
func TestEmbeddingQueueIdempotency(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "embedding_queue", "embedding_dead_letter")
	})

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	queue := postgres.NewEmbeddingQueue(pool, embeddingConfig)

	content := fmt.Sprintf("idempotent-test-%d", time.Now().UnixNano())
	task := &postgres.EmbeddingTask{
		Table:    "knowledge_chunks_1024",
		Content:  content,
		TenantID: "test-tenant",
		Model:    embeddingConfig.DefaultModel,
		Version:  embeddingConfig.DefaultVersion,
	}

	// Enqueue the same task twice.
	require.NoError(t, queue.Enqueue(ctx, task))
	require.NoError(t, queue.Enqueue(ctx, task))

	// Fetch pending tasks - should only get one due to dedupe_key.
	tasks, err := queue.FetchPendingTasks(ctx, 10)
	require.NoError(t, err)

	count := 0
	for _, pending := range tasks {
		if pending.Content == content {
			count++
		}
	}
	assert.Equal(t, 1, count, "expected exactly one task after duplicate enqueue")
}

// TestEmbeddingQueueMarkFailed verifies that a failed task is retried
// and eventually moved to the dead letter queue after max retries.
func TestEmbeddingQueueMarkFailed(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "embedding_queue", "embedding_dead_letter")
	})

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	queue := postgres.NewEmbeddingQueue(pool, embeddingConfig)

	// Enqueue a task.
	task := &postgres.EmbeddingTask{
		Table:    "knowledge_chunks_1024",
		Content:  fmt.Sprintf("fail-test-%d", time.Now().UnixNano()),
		TenantID: "test-tenant",
		Model:    embeddingConfig.DefaultModel,
		Version:  embeddingConfig.DefaultVersion,
	}
	require.NoError(t, queue.Enqueue(ctx, task))

	// Fetch the task.
	tasks, err := queue.FetchPendingTasks(ctx, 10)
	require.NoError(t, err)
	require.NotEmpty(t, tasks)

	var targetTask *postgres.EmbeddingTask
	for _, pending := range tasks {
		if pending.TenantID == "test-tenant" {
			targetTask = pending
			break
		}
	}
	require.NotNil(t, targetTask, "expected to find the enqueued task")

	// Fail the task MaxRetries times to trigger dead letter.
	for i := 0; i < embeddingConfig.MaxRetries; i++ {
		// The task was marked as 'processing' by FetchPendingTasks, so we can mark it failed.
		// After each failure, the task is re-enqueued as 'pending' (unless max retries reached).
		// We need to fetch it again to get the updated state.
		if i > 0 {
			tasks, err = queue.FetchPendingTasks(ctx, 10)
			require.NoError(t, err)
			for _, pending := range tasks {
				if pending.TenantID == "test-tenant" {
					targetTask = pending
					break
				}
			}
		}
		require.NoError(t, queue.MarkFailed(ctx, targetTask.TaskID, "test error"))
	}

	// Verify the task was moved to dead letter queue.
	var dlqCount int
	err = pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM embedding_dead_letter WHERE tenant_id = $1",
		"test-tenant",
	).Scan(&dlqCount)
	require.NoError(t, err)
	assert.Equal(t, 1, dlqCount, "expected one task in dead letter queue after max retries")
}

// TestCheckpointRepositorySaveAndRetrieve verifies the full checkpoint lifecycle:
// Save -> GetLatest -> UPSERT -> Delete.
func TestCheckpointRepositorySaveAndRetrieve(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "leader_checkpoints")
	})

	ctx := context.Background()
	repo := leader.NewCheckpointRepository(pool)
	require.NotNil(t, repo, "expected non-nil checkpoint repository")

	leaderID := fmt.Sprintf("test-leader-%d", time.Now().UnixNano())

	// Save initial checkpoint.
	metadata := json.RawMessage(`{"step": 1, "status": "running"}`)
	cp := &leader.LeaderCheckpoint{
		LeaderID:  leaderID,
		SessionID: "session-001",
		Status:    "active",
		Metadata:  metadata,
	}
	require.NoError(t, repo.Save(ctx, cp))

	// GetLatest should return the saved checkpoint.
	latest, err := repo.GetLatest(ctx, leaderID)
	require.NoError(t, err)
	require.NotNil(t, latest, "expected non-nil checkpoint")
	assert.Equal(t, leaderID, latest.LeaderID)
	assert.Equal(t, "session-001", latest.SessionID)
	assert.Equal(t, "active", latest.Status)
	assert.JSONEq(t, `{"step": 1, "status": "running"}`, string(latest.Metadata))

	// UPSERT: save with updated session and metadata.
	updatedMetadata := json.RawMessage(`{"step": 2, "status": "completed"}`)
	cp2 := &leader.LeaderCheckpoint{
		LeaderID:  leaderID,
		SessionID: "session-002",
		Status:    "completed",
		Metadata:  updatedMetadata,
	}
	require.NoError(t, repo.Save(ctx, cp2))

	// GetLatest should return the updated checkpoint.
	latest2, err := repo.GetLatest(ctx, leaderID)
	require.NoError(t, err)
	require.NotNil(t, latest2)
	assert.Equal(t, "session-002", latest2.SessionID)
	assert.Equal(t, "completed", latest2.Status)
	assert.JSONEq(t, `{"step": 2, "status": "completed"}`, string(latest2.Metadata))

	// Delete the checkpoint.
	require.NoError(t, repo.Delete(ctx, leaderID))

	// GetLatest should return nil after deletion.
	latest3, err := repo.GetLatest(ctx, leaderID)
	require.NoError(t, err)
	assert.Nil(t, latest3, "expected nil checkpoint after deletion")
}

// TestCheckpointRepositoryValidation verifies error handling for invalid inputs.
func TestCheckpointRepositoryValidation(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	repo := leader.NewCheckpointRepository(pool)
	require.NotNil(t, repo)

	// Save with nil checkpoint should fail.
	err := repo.Save(ctx, nil)
	require.Error(t, err)

	// Save with empty leader ID should fail.
	err = repo.Save(ctx, &leader.LeaderCheckpoint{
		LeaderID:  "",
		SessionID: "session-001",
		Status:    "active",
	})
	require.Error(t, err)

	// GetLatest with empty leader ID should fail.
	_, err = repo.GetLatest(ctx, "")
	require.Error(t, err)

	// Delete with empty leader ID should fail.
	err = repo.Delete(ctx, "")
	require.Error(t, err)
}

// TestVectorSearcherCreateCollection verifies collection creation and basic operations.
func TestVectorSearcherCreateCollection(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	searcher := postgres.NewVectorSearcher(pool, embeddingConfig)

	collectionName := fmt.Sprintf("test_collection_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+collectionName)
	})

	// Create a collection with 1024 dimensions.
	require.NoError(t, searcher.CreateCollection(ctx, collectionName, 1024))

	// Creating the same collection again should not error (IF NOT EXISTS).
	require.NoError(t, searcher.CreateCollection(ctx, collectionName, 1024))

	// Invalid dimension should fail.
	err := searcher.CreateCollection(ctx, "bad_col", 0)
	require.Error(t, err)

	// Invalid name should fail.
	err = searcher.CreateCollection(ctx, "", 1024)
	require.Error(t, err)
}

// TestVectorSearcherAddAndDelete verifies embedding add and delete operations.
func TestVectorSearcherAddAndDelete(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	searcher := postgres.NewVectorSearcher(pool, embeddingConfig)

	collectionName := fmt.Sprintf("test_vec_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+collectionName)
	})

	require.NoError(t, searcher.CreateCollection(ctx, collectionName, 1536))

	// Add an embedding.
	embedding := make([]float64, 1536)
	embedding[0] = 1.0
	metadata := map[string]any{"source": "test", "category": "integration"}
	require.NoError(t, searcher.AddEmbedding(ctx, collectionName, "doc-1", embedding, metadata))

	// Add another embedding.
	embedding2 := make([]float64, 1536)
	embedding2[1] = 1.0
	metadata2 := map[string]any{"source": "test", "category": "unit"}
	require.NoError(t, searcher.AddEmbedding(ctx, collectionName, "doc-2", embedding2, metadata2))

	// Delete the first embedding.
	require.NoError(t, searcher.DeleteEmbedding(ctx, collectionName, "doc-1"))

	// Verify deletion by searching - should only find doc-2.
	results, err := searcher.Search(ctx, collectionName, embedding2, 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "doc-2", results[0].ID)
}

// TestVectorSearcherSearchWithLimit verifies search result limiting.
func TestVectorSearcherSearchWithLimit(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	searcher := postgres.NewVectorSearcher(pool, embeddingConfig)

	collectionName := fmt.Sprintf("test_limit_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+collectionName)
	})

	require.NoError(t, searcher.CreateCollection(ctx, collectionName, 1536))

	// Add 5 embeddings.
	for i := 0; i < 5; i++ {
		embedding := make([]float64, 1536)
		embedding[i] = 1.0
		id := fmt.Sprintf("doc-%d", i)
		metadata := map[string]any{"index": i}
		require.NoError(t, searcher.AddEmbedding(ctx, collectionName, id, embedding, metadata))
	}

	// Search with limit 3.
	queryEmbedding := make([]float64, 1536)
	queryEmbedding[0] = 1.0
	results, err := searcher.Search(ctx, collectionName, queryEmbedding, 3)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 3, "expected at most 3 results")

	// Invalid limit should fail.
	_, err = searcher.Search(ctx, collectionName, queryEmbedding, 0)
	require.Error(t, err)
}
