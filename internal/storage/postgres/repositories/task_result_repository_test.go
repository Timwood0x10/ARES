// Package repositories provides comprehensive tests for TaskResultRepository.
package repositories

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/errors"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// TestTaskResultRepository_Create tests creating a task result.
func TestTaskResultRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	result := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "completed",
		LatencyMs:        100,
		Metadata:         map[string]interface{}{"key": "value"},
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, result)
	require.NoError(t, err)
	assert.NotEmpty(t, result.ID)
}

// TestTaskResultRepository_Create_WithID tests creating a task result with specified ID.
func TestTaskResultRepository_Create_WithID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	result := &storage_models.TaskResult{
		ID:               "123e4567-e89b-12d3-a456-426614174000",
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "pending",
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, result)
	require.NoError(t, err)
	assert.Equal(t, "123e4567-e89b-12d3-a456-426614174000", result.ID)
}

// TestTaskResultRepository_Create_WithOutput tests creating a task result with output.
func TestTaskResultRepository_Create_WithOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	result := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Output:           map[string]interface{}{"result": "success"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "completed",
		LatencyMs:        100,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, result)
	require.NoError(t, err)
	assert.NotEmpty(t, result.ID)

	// Retrieve and verify output
	retrieved, err := repo.GetByID(ctx, result.ID)
	require.NoError(t, err)
	assert.Equal(t, "success", retrieved.Output["result"])
}

// TestTaskResultRepository_GetByID tests retrieving a task result by ID.
func TestTaskResultRepository_GetByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create a task result
	result := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Output:           map[string]interface{}{"result": "success"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "completed",
		LatencyMs:        100,
		Metadata:         map[string]interface{}{"key": "value"},
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Retrieve by ID
	retrieved, err := repo.GetByID(ctx, result.ID)
	require.NoError(t, err)
	assert.Equal(t, result.ID, retrieved.ID)
	assert.Equal(t, result.TenantID, retrieved.TenantID)
	assert.Equal(t, result.SessionID, retrieved.SessionID)
	assert.Equal(t, result.TaskType, retrieved.TaskType)
	assert.Equal(t, result.AgentID, retrieved.AgentID)
	assert.Equal(t, result.Input["query"], retrieved.Input["query"])
	assert.Equal(t, result.Output["result"], retrieved.Output["result"])
	assert.Equal(t, result.Status, retrieved.Status)
	assert.Equal(t, result.LatencyMs, retrieved.LatencyMs)
	assert.Equal(t, result.Metadata["key"], retrieved.Metadata["key"])
	assert.Len(t, retrieved.Embedding, 1024)
}

// TestTaskResultRepository_GetByID_NotFound tests retrieving a non-existent task result.
func TestTaskResultRepository_GetByID_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestTaskResultRepository_GetByID_InvalidID tests retrieving with invalid ID.
func TestTaskResultRepository_GetByID_InvalidID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrInvalidArgument, err)
}

// TestTaskResultRepository_Update tests updating a task result.
func TestTaskResultRepository_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create a task result
	result := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Output:           map[string]interface{}{"result": "old"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "pending",
		LatencyMs:        0,
		Metadata:         map[string]interface{}{"version": 1},
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Update the task result
	result.Output = map[string]interface{}{"result": "new"}
	result.Status = "completed"
	result.LatencyMs = 200
	result.Metadata = map[string]interface{}{"version": 2}

	err = repo.Update(ctx, result)
	require.NoError(t, err)

	// Verify the update
	retrieved, err := repo.GetByID(ctx, result.ID)
	require.NoError(t, err)
	assert.Equal(t, "new", retrieved.Output["result"])
	assert.Equal(t, "completed", retrieved.Status)
	assert.Equal(t, 200, retrieved.LatencyMs)
	assert.Equal(t, float64(2), retrieved.Metadata["version"])
}

// TestTaskResultRepository_Update_NotFound tests updating a non-existent task result.
func TestTaskResultRepository_Update_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	result := &storage_models.TaskResult{
		ID:        "00000000-0000-0000-0000-000000000000",
		TenantID:  "tenant-1",
		SessionID: "session-1",
		TaskType:  "analysis",
		AgentID:   "agent-1",
		Input:     map[string]interface{}{"query": "test"},
		Embedding: createTestEmbedding(),
	}

	err := repo.Update(ctx, result)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestTaskResultRepository_Delete tests deleting a task result.
func TestTaskResultRepository_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create a task result
	result := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "completed",
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Delete the task result
	err = repo.Delete(ctx, result.ID)
	require.NoError(t, err)

	// Verify it's deleted
	_, err = repo.GetByID(ctx, result.ID)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestTaskResultRepository_Delete_NotFound tests deleting a non-existent task result.
func TestTaskResultRepository_Delete_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	err := repo.Delete(ctx, "00000000-0000-0000-0000-000000000000")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestTaskResultRepository_SearchByVector tests vector similarity search.
func TestTaskResultRepository_SearchByVector(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create multiple task results with different embeddings
	results := []*storage_models.TaskResult{
		{
			TenantID:         "tenant-1",
			SessionID:        "session-1",
			TaskType:         "analysis",
			AgentID:          "agent-1",
			Input:            map[string]interface{}{"query": "test1"},
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Status:           "completed",
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			SessionID:        "session-1",
			TaskType:         "analysis",
			AgentID:          "agent-1",
			Input:            map[string]interface{}{"query": "test2"},
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Status:           "completed",
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			SessionID:        "session-1",
			TaskType:         "analysis",
			AgentID:          "agent-1",
			Input:            map[string]interface{}{"query": "test3"},
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Status:           "completed",
			CreatedAt:        time.Now(),
		},
	}

	for _, result := range results {
		err := repo.Create(ctx, result)
		require.NoError(t, err)
	}

	// Search by vector
	queryEmbedding := createTestEmbedding()
	searchResults, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, searchResults, 3)

	// Verify similarity metadata is added
	for _, result := range searchResults {
		assert.Contains(t, result.Metadata, "similarity")
		assert.Greater(t, result.Metadata["similarity"].(float64), 0.0)
	}
}

// TestTaskResultRepository_SearchByVector_EmptyEmbedding tests search with empty embedding.
func TestTaskResultRepository_SearchByVector_EmptyEmbedding(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create a task result
	result := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "completed",
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Search with empty embedding
	searchResults, err := repo.SearchByVector(ctx, []float64{}, "tenant-1", 10)
	require.NoError(t, err)
	// Empty embedding should return no results
	assert.Empty(t, searchResults)
}

// TestTaskResultRepository_SearchByVector_TenantIsolation tests tenant isolation in vector search.
func TestTaskResultRepository_SearchByVector_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task result for tenant-1
	result1 := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "completed",
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, result1)
	require.NoError(t, err)

	// Search for tenant-2 should return no results
	queryEmbedding := createTestEmbedding()
	searchResults, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, searchResults)
}

// TestTaskResultRepository_SearchByVector_Limit tests limit parameter.
func TestTaskResultRepository_SearchByVector_Limit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create 5 task results
	for i := 0; i < 5; i++ {
		result := &storage_models.TaskResult{
			TenantID:         "tenant-1",
			SessionID:        "session-1",
			TaskType:         "analysis",
			AgentID:          "agent-1",
			Input:            map[string]interface{}{"query": "test"},
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Status:           "completed",
			CreatedAt:        time.Now(),
		}
		err := repo.Create(ctx, result)
		require.NoError(t, err)
	}

	// Search with limit
	queryEmbedding := createTestEmbedding()
	searchResults, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-1", 2)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(searchResults), 2)
}

// TestTaskResultRepository_SearchByVector_StatusFilter tests status filter.
func TestTaskResultRepository_SearchByVector_StatusFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task results with different statuses
	completedResult := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "completed",
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, completedResult)
	require.NoError(t, err)

	pendingResult := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "pending",
		CreatedAt:        time.Now(),
	}
	err = repo.Create(ctx, pendingResult)
	require.NoError(t, err)

	// Search should only return completed results
	queryEmbedding := createTestEmbedding()
	searchResults, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, searchResults, 1)
	assert.Equal(t, "completed", searchResults[0].Status)
}

// TestTaskResultRepository_ListByType tests listing task results by type.
func TestTaskResultRepository_ListByType(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task results with different types
	results := []*storage_models.TaskResult{
		{
			TenantID:  "tenant-1",
			SessionID: "session-1",
			TaskType:  "analysis",
			AgentID:   "agent-1",
			Input:     map[string]interface{}{"query": "test"},
			Embedding: createTestEmbedding(),
			Status:    "completed",
			CreatedAt: time.Now(),
		},
		{
			TenantID:  "tenant-1",
			SessionID: "session-1",
			TaskType:  "analysis",
			AgentID:   "agent-1",
			Input:     map[string]interface{}{"query": "test"},
			Embedding: createTestEmbedding(),
			Status:    "completed",
			CreatedAt: time.Now(),
		},
		{
			TenantID:  "tenant-1",
			SessionID: "session-1",
			TaskType:  "retrieval",
			AgentID:   "agent-1",
			Input:     map[string]interface{}{"query": "test"},
			Embedding: createTestEmbedding(),
			Status:    "completed",
			CreatedAt: time.Now(),
		},
	}

	for _, result := range results {
		err := repo.Create(ctx, result)
		require.NoError(t, err)
	}

	// List by type
	listResults, err := repo.ListByType(ctx, "analysis", "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, listResults, 2)
}

// TestTaskResultRepository_ListByType_TenantIsolation tests tenant isolation.
func TestTaskResultRepository_ListByType_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task result for tenant-1
	result1 := &storage_models.TaskResult{
		TenantID:  "tenant-1",
		SessionID: "session-1",
		TaskType:  "analysis",
		AgentID:   "agent-1",
		Input:     map[string]interface{}{"query": "test"},
		Embedding: createTestEmbedding(),
		Status:    "completed",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, result1)
	require.NoError(t, err)

	// List for tenant-2 should return no results
	listResults, err := repo.ListByType(ctx, "analysis", "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, listResults)
}

// TestTaskResultRepository_ListBySession tests listing task results by session.
func TestTaskResultRepository_ListBySession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task results in the same session
	for i := 0; i < 3; i++ {
		result := &storage_models.TaskResult{
			TenantID:  "tenant-1",
			SessionID: "session-1",
			TaskType:  "analysis",
			AgentID:   "agent-1",
			Input:     map[string]interface{}{"query": "test"},
			Embedding: createTestEmbedding(),
			Status:    "completed",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, result)
		require.NoError(t, err)
	}

	// List by session
	listResults, err := repo.ListBySession(ctx, "session-1", "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, listResults, 3)
}

// TestTaskResultRepository_ListBySession_TenantIsolation tests tenant isolation.
func TestTaskResultRepository_ListBySession_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task result for tenant-1
	result1 := &storage_models.TaskResult{
		TenantID:  "tenant-1",
		SessionID: "session-1",
		TaskType:  "analysis",
		AgentID:   "agent-1",
		Input:     map[string]interface{}{"query": "test"},
		Embedding: createTestEmbedding(),
		Status:    "completed",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, result1)
	require.NoError(t, err)

	// List for tenant-2 should return no results
	listResults, err := repo.ListBySession(ctx, "session-1", "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, listResults)
}

// TestTaskResultRepository_UpdateEmbedding tests updating task result embedding.
func TestTaskResultRepository_UpdateEmbedding(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create a task result with initial embedding
	result := &storage_models.TaskResult{
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "analysis",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"query": "test"},
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           "completed",
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Update embedding
	newEmbedding := createTestEmbedding()
	newEmbedding[0] = 1.0 // Modify first element
	err = repo.UpdateEmbedding(ctx, result.ID, newEmbedding, "e5-large-v2", 2)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, result.ID)
	require.NoError(t, err)
	assert.Equal(t, newEmbedding[0], retrieved.Embedding[0])
	assert.Equal(t, "e5-large-v2", retrieved.EmbeddingModel)
	assert.Equal(t, 2, retrieved.EmbeddingVersion)
}

// TestTaskResultRepository_UpdateEmbedding_NotFound tests updating embedding for non-existent task result.
func TestTaskResultRepository_UpdateEmbedding_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	newEmbedding := createTestEmbedding()
	err := repo.UpdateEmbedding(ctx, "00000000-0000-0000-0000-000000000000", newEmbedding, "e5-large", 1)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestTaskResultRepository_UpdateStatus tests updating task result status.
func TestTaskResultRepository_UpdateStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create a task result
	result := &storage_models.TaskResult{
		TenantID:  "tenant-1",
		SessionID: "session-1",
		TaskType:  "analysis",
		AgentID:   "agent-1",
		Input:     map[string]interface{}{"query": "test"},
		Embedding: createTestEmbedding(),
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Update status
	err = repo.UpdateStatus(ctx, result.ID, "completed", "", 200)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, result.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", retrieved.Status)
	assert.Equal(t, 200, retrieved.LatencyMs)
}

// TestTaskResultRepository_UpdateStatus_WithError tests updating status with error message.
func TestTaskResultRepository_UpdateStatus_WithError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create a task result
	result := &storage_models.TaskResult{
		TenantID:  "tenant-1",
		SessionID: "session-1",
		TaskType:  "analysis",
		AgentID:   "agent-1",
		Input:     map[string]interface{}{"query": "test"},
		Embedding: createTestEmbedding(),
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Update status with error
	err = repo.UpdateStatus(ctx, result.ID, "failed", "timeout error", 0)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, result.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", retrieved.Status)
	assert.Equal(t, "timeout error", retrieved.Error)
}

// TestTaskResultRepository_UpdateStatus_NotFound tests updating status for non-existent task result.
func TestTaskResultRepository_UpdateStatus_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	err := repo.UpdateStatus(ctx, "00000000-0000-0000-0000-000000000000", "completed", "", 0)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestTaskResultRepository_GetStatistics tests getting task result statistics.
func TestTaskResultRepository_GetStatistics(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task results with different types and statuses
	results := []*storage_models.TaskResult{
		{
			TenantID:  "tenant-1",
			SessionID: "session-1",
			TaskType:  "analysis",
			AgentID:   "agent-1",
			Input:     map[string]interface{}{"query": "test"},
			Embedding: createTestEmbedding(),
			Status:    "completed",
			CreatedAt: time.Now(),
		},
		{
			TenantID:  "tenant-1",
			SessionID: "session-1",
			TaskType:  "analysis",
			AgentID:   "agent-1",
			Input:     map[string]interface{}{"query": "test"},
			Embedding: createTestEmbedding(),
			Status:    "failed",
			CreatedAt: time.Now(),
		},
		{
			TenantID:  "tenant-1",
			SessionID: "session-1",
			TaskType:  "retrieval",
			AgentID:   "agent-1",
			Input:     map[string]interface{}{"query": "test"},
			Embedding: createTestEmbedding(),
			Status:    "completed",
			CreatedAt: time.Now(),
		},
	}

	for _, result := range results {
		err := repo.Create(ctx, result)
		require.NoError(t, err)
	}

	// Get statistics
	stats, err := repo.GetStatistics(ctx, "tenant-1")
	require.NoError(t, err)
	assert.NotEmpty(t, stats)

	// Verify statistics
	assert.Equal(t, int64(1), stats["analysis:completed"])
	assert.Equal(t, int64(1), stats["analysis:failed"])
	assert.Equal(t, int64(1), stats["retrieval:completed"])
}

// TestTaskResultRepository_GetStatistics_Empty tests getting statistics with no data.
func TestTaskResultRepository_GetStatistics_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Get statistics with no data
	stats, err := repo.GetStatistics(ctx, "tenant-1")
	require.NoError(t, err)
	assert.Empty(t, stats)
}

// TestTaskResultRepository_GetStatistics_TenantIsolation tests tenant isolation in statistics.
func TestTaskResultRepository_GetStatistics_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task result for tenant-1
	result1 := &storage_models.TaskResult{
		TenantID:  "tenant-1",
		SessionID: "session-1",
		TaskType:  "analysis",
		AgentID:   "agent-1",
		Input:     map[string]interface{}{"query": "test"},
		Embedding: createTestEmbedding(),
		Status:    "completed",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, result1)
	require.NoError(t, err)

	// Get statistics for tenant-1
	stats1, err := repo.GetStatistics(ctx, "tenant-1")
	require.NoError(t, err)
	assert.NotEmpty(t, stats1)

	// Get statistics for tenant-2
	stats2, err := repo.GetStatistics(ctx, "tenant-2")
	require.NoError(t, err)
	assert.Empty(t, stats2)
}

// TestTaskResultRepository_ComplexInputOutput tests handling complex input/output structures.
func TestTaskResultRepository_ComplexInputOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task result with complex input/output
	complexInput := map[string]interface{}{
		"query": "test",
		"options": map[string]interface{}{
			"param1": "value1",
			"param2": 123,
		},
		"items": []interface{}{"item1", "item2", "item3"},
	}

	complexOutput := map[string]interface{}{
		"result": "success",
		"data": map[string]interface{}{
			"count": 42,
			"list":  []interface{}{1, 2, 3},
		},
	}

	result := &storage_models.TaskResult{
		TenantID:  "tenant-1",
		SessionID: "session-1",
		TaskType:  "analysis",
		AgentID:   "agent-1",
		Input:     complexInput,
		Output:    complexOutput,
		Embedding: createTestEmbedding(),
		Status:    "completed",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Retrieve and verify complex structures
	retrieved, err := repo.GetByID(ctx, result.ID)
	require.NoError(t, err)
	assert.Equal(t, "test", retrieved.Input["query"])
	assert.Equal(t, "value1", retrieved.Input["options"].(map[string]interface{})["param1"])
	assert.Equal(t, "success", retrieved.Output["result"])
	assert.Equal(t, float64(42), retrieved.Output["data"].(map[string]interface{})["count"])
}

// TestTaskResultRepository_ConcurrentOperations tests concurrent operations.
func TestTaskResultRepository_ConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	done := make(chan bool, 10)

	// Create 10 task results concurrently
	for i := 0; i < 10; i++ {
		go func(index int) {
			result := &storage_models.TaskResult{
				TenantID:  "tenant-1",
				SessionID: "session-1",
				TaskType:  "analysis",
				AgentID:   "agent-1",
				Input:     map[string]interface{}{"query": "test"},
				Embedding: createTestEmbedding(),
				Status:    "pending",
				CreatedAt: time.Now(),
			}
			err := repo.Create(ctx, result)
			require.NoError(t, err)
			done <- true
		}(i)
	}

	// Wait for all operations to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all task results were created
	listResults, err := repo.ListBySession(ctx, "session-1", "tenant-1", 20)
	require.NoError(t, err)
	assert.Len(t, listResults, 10)
}

// TestTaskResultRepository_LongMetadata tests handling long metadata.
func TestTaskResultRepository_LongMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewTaskResultRepository(db)
	ctx := context.Background()

	// Create task result with very long metadata
	longMetadata := map[string]interface{}{}
	for i := 0; i < 100; i++ {
		longMetadata[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}

	result := &storage_models.TaskResult{
		TenantID:  "tenant-1",
		SessionID: "session-1",
		TaskType:  "analysis",
		AgentID:   "agent-1",
		Input:     map[string]interface{}{"query": "test"},
		Embedding: createTestEmbedding(),
		Status:    "completed",
		Metadata:  longMetadata,
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, result)
	require.NoError(t, err)

	// Retrieve and verify metadata
	retrieved, err := repo.GetByID(ctx, result.ID)
	require.NoError(t, err)
	assert.Len(t, retrieved.Metadata, 100)
	assert.Equal(t, "value0", retrieved.Metadata["key0"])
	assert.Equal(t, "value99", retrieved.Metadata["key99"])
}
