// Package repositories provides comprehensive tests for ToolRepository.
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

// TestToolRepository_Create tests creating a tool.
func TestToolRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "test-tool",
		Description:      "Test tool description",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		AgentType:        "test-agent",
		Tags:             []string{"test", "tool"},
		UsageCount:       0,
		SuccessRate:      0.0,
		Metadata:         map[string]interface{}{"key": "value"},
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, tool)
	require.NoError(t, err)
	assert.NotEmpty(t, tool.ID)
}

// TestToolRepository_Create_WithID tests creating a tool with specified ID.
func TestToolRepository_Create_WithID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	tool := &storage_models.Tool{
		ID:               "123e4567-e89b-12d3-a456-426614174000",
		TenantID:         "tenant-1",
		Name:             "tool-with-id",
		Description:      "Tool with specified ID",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		AgentType:        "test-agent",
		Tags:             []string{"test"},
		UsageCount:       0,
		SuccessRate:      0.0,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, tool)
	require.NoError(t, err)
	assert.Equal(t, "123e4567-e89b-12d3-a456-426614174000", tool.ID)
}

// TestToolRepository_Create_WithEmptyFields tests creating a tool with minimal fields.
func TestToolRepository_Create_WithEmptyFields(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "minimal-tool",
		Description:      "Minimal tool",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, tool)
	require.NoError(t, err)
	assert.NotEmpty(t, tool.ID)
}

// TestToolRepository_GetByID tests retrieving a tool by ID.
func TestToolRepository_GetByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "get-by-id-tool",
		Description:      "Tool for GetByID test",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		AgentType:        "test-agent",
		Tags:             []string{"test"},
		UsageCount:       5,
		SuccessRate:      0.85,
		Metadata:         map[string]interface{}{"key": "value"},
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Retrieve by ID
	retrieved, err := repo.GetByID(ctx, tool.ID)
	require.NoError(t, err)
	assert.Equal(t, tool.ID, retrieved.ID)
	assert.Equal(t, tool.TenantID, retrieved.TenantID)
	assert.Equal(t, tool.Name, retrieved.Name)
	assert.Equal(t, tool.Description, retrieved.Description)
	assert.Equal(t, tool.AgentType, retrieved.AgentType)
	assert.Equal(t, tool.Tags, retrieved.Tags)
	assert.Equal(t, tool.UsageCount, retrieved.UsageCount)
	assert.InDelta(t, tool.SuccessRate, retrieved.SuccessRate, 0.01)
	assert.Equal(t, tool.Metadata["key"], retrieved.Metadata["key"])
	assert.Len(t, retrieved.Embedding, 1024)
}

// TestToolRepository_GetByID_NotFound tests retrieving a non-existent tool.
func TestToolRepository_GetByID_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestToolRepository_GetByID_InvalidID tests retrieving with invalid ID.
func TestToolRepository_GetByID_InvalidID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrInvalidArgument, err)
}

// TestToolRepository_GetByName tests retrieving a tool by name.
func TestToolRepository_GetByName(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "get-by-name-tool",
		Description:      "Tool for GetByName test",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Retrieve by name
	retrieved, err := repo.GetByName(ctx, "get-by-name-tool", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, tool.ID, retrieved.ID)
	assert.Equal(t, tool.Name, retrieved.Name)
}

// TestToolRepository_GetByName_NotFound tests retrieving a tool with non-existent name.
func TestToolRepository_GetByName_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	_, err := repo.GetByName(ctx, "non-existent-tool", "tenant-1")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestToolRepository_GetByName_TenantIsolation tests tenant isolation.
func TestToolRepository_GetByName_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tool for tenant-1
	tool1 := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "isolation-tool",
		Description:      "Tool for tenant 1",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool1)
	require.NoError(t, err)

	// Try to retrieve with different tenant
	_, err = repo.GetByName(ctx, "isolation-tool", "tenant-2")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestToolRepository_Update tests updating a tool.
func TestToolRepository_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "update-tool",
		Description:      "Original description",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		AgentType:        "original-agent",
		Tags:             []string{"original"},
		Metadata:         map[string]interface{}{"version": 1},
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Update the tool
	tool.Description = "Updated description"
	tool.AgentType = "updated-agent"
	tool.Tags = []string{"updated", "tool"}
	tool.Metadata = map[string]interface{}{"version": 2, "updated": true}

	err = repo.Update(ctx, tool)
	require.NoError(t, err)

	// Verify the update
	retrieved, err := repo.GetByID(ctx, tool.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated description", retrieved.Description)
	assert.Equal(t, "updated-agent", retrieved.AgentType)
	assert.Equal(t, []string{"updated", "tool"}, retrieved.Tags)
	assert.Equal(t, float64(2), retrieved.Metadata["version"])
	assert.True(t, retrieved.Metadata["updated"].(bool))
}

// TestToolRepository_Update_NotFound tests updating a non-existent tool.
func TestToolRepository_Update_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	tool := &storage_models.Tool{
		ID:        "00000000-0000-0000-0000-000000000000",
		TenantID:  "tenant-1",
		Name:      "non-existent-tool",
		Embedding: createTestEmbedding(),
	}

	err := repo.Update(ctx, tool)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestToolRepository_Delete tests deleting a tool.
func TestToolRepository_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "delete-tool",
		Description:      "Tool for Delete test",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Delete the tool
	err = repo.Delete(ctx, tool.ID)
	require.NoError(t, err)

	// Verify it's deleted
	_, err = repo.GetByID(ctx, tool.ID)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestToolRepository_Delete_NotFound tests deleting a non-existent tool.
func TestToolRepository_Delete_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	err := repo.Delete(ctx, "00000000-0000-0000-0000-000000000000")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestToolRepository_SearchByVector tests vector similarity search.
func TestToolRepository_SearchByVector(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create multiple tools with different embeddings
	tools := []*storage_models.Tool{
		{
			TenantID:         "tenant-1",
			Name:             "tool-1",
			Description:      "First tool",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Name:             "tool-2",
			Description:      "Second tool",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Name:             "tool-3",
			Description:      "Third tool",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		},
	}

	for _, tool := range tools {
		err := repo.Create(ctx, tool)
		require.NoError(t, err)
	}

	// Search by vector
	queryEmbedding := createTestEmbedding()
	results, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Verify similarity metadata is added
	for _, result := range results {
		assert.Contains(t, result.Metadata, "similarity")
		assert.Greater(t, result.Metadata["similarity"].(float64), 0.0)
	}
}

// TestToolRepository_SearchByVector_EmptyEmbedding tests search with empty embedding.
func TestToolRepository_SearchByVector_EmptyEmbedding(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "tool-1",
		Description:      "Test tool",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Search with empty embedding - this should return an error because pgvector requires at least 1 dimension
	_, err = repo.SearchByVector(ctx, []float64{}, "tenant-1", 10)
	assert.Error(t, err)
}

// TestToolRepository_SearchByVector_TenantIsolation tests tenant isolation in vector search.
func TestToolRepository_SearchByVector_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tool for tenant-1
	tool1 := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "isolation-tool-1",
		Description:      "Tool for tenant 1",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool1)
	require.NoError(t, err)

	// Search for tenant-2 should return no results
	queryEmbedding := createTestEmbedding()
	results, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestToolRepository_SearchByVector_Limit tests limit parameter.
func TestToolRepository_SearchByVector_Limit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create 5 tools
	for i := 0; i < 5; i++ {
		tool := &storage_models.Tool{
			TenantID:         "tenant-1",
			Name:             fmt.Sprintf("tool-%d", i),
			Description:      fmt.Sprintf("Tool number %d", i),
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		}
		err := repo.Create(ctx, tool)
		require.NoError(t, err)
	}

	// Search with limit
	queryEmbedding := createTestEmbedding()
	results, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-1", 2)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 2)
}

// TestToolRepository_SearchByKeyword tests keyword search.
func TestToolRepository_SearchByKeyword(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tools with specific keywords
	tools := []*storage_models.Tool{
		{
			TenantID:         "tenant-1",
			Name:             "database-tool",
			Description:      "Tool for database operations",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Name:             "file-tool",
			Description:      "Tool for file operations",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Name:             "network-tool",
			Description:      "Tool for network operations",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		},
	}

	for _, tool := range tools {
		err := repo.Create(ctx, tool)
		require.NoError(t, err)
	}

	// Search by keyword
	results, err := repo.SearchByKeyword(ctx, "database", "tenant-1", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	assert.Contains(t, results[0].Name, "database")
}

// TestToolRepository_SearchByKeyword_EmptyQuery tests search with empty query.
func TestToolRepository_SearchByKeyword_EmptyQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "test-tool",
		Description:      "Test tool",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Search with empty query
	results, err := repo.SearchByKeyword(ctx, "", "tenant-1", 10)
	require.NoError(t, err)
	// Empty query should match everything
	assert.NotEmpty(t, results)
}

// TestToolRepository_SearchByKeyword_TenantIsolation tests tenant isolation in keyword search.
func TestToolRepository_SearchByKeyword_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tool for tenant-1
	tool1 := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "isolation-tool",
		Description:      "Tool for tenant 1",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool1)
	require.NoError(t, err)

	// Search for tenant-2 should return no results
	results, err := repo.SearchByKeyword(ctx, "isolation", "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestToolRepository_ListAll tests listing all tools.
func TestToolRepository_ListAll(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tools with different usage counts
	tools := []*storage_models.Tool{
		{
			TenantID:         "tenant-1",
			Name:             "low-usage-tool",
			Description:      "Tool with low usage",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			UsageCount:       1,
			SuccessRate:      0.9,
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Name:             "high-usage-tool",
			Description:      "Tool with high usage",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			UsageCount:       10,
			SuccessRate:      0.95,
			CreatedAt:        time.Now(),
		},
	}

	for _, tool := range tools {
		err := repo.Create(ctx, tool)
		require.NoError(t, err)
	}

	// List all tools
	results, err := repo.ListAll(ctx, "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Verify ordering by usage count (descending)
	assert.Equal(t, "high-usage-tool", results[0].Name)
	assert.Equal(t, "low-usage-tool", results[1].Name)
}

// TestToolRepository_ListAll_TenantIsolation tests tenant isolation in list all.
func TestToolRepository_ListAll_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tool for tenant-1
	tool1 := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "isolation-tool",
		Description:      "Tool for tenant 1",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool1)
	require.NoError(t, err)

	// List for tenant-2 should return no results
	results, err := repo.ListAll(ctx, "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestToolRepository_ListByAgentType tests listing tools by agent type.
func TestToolRepository_ListByAgentType(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tools with different agent types
	tools := []*storage_models.Tool{
		{
			TenantID:         "tenant-1",
			Name:             "data-agent-tool",
			Description:      "Tool for data agent",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			AgentType:        "data-agent",
			UsageCount:       5,
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Name:             "analysis-agent-tool",
			Description:      "Tool for analysis agent",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			AgentType:        "analysis-agent",
			UsageCount:       3,
			CreatedAt:        time.Now(),
		},
	}

	for _, tool := range tools {
		err := repo.Create(ctx, tool)
		require.NoError(t, err)
	}

	// List by agent type
	results, err := repo.ListByAgentType(ctx, "data-agent", "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "data-agent-tool", results[0].Name)
}

// TestToolRepository_ListByAgentType_NoResults tests listing tools with non-existent agent type.
func TestToolRepository_ListByAgentType_NoResults(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// List with non-existent agent type
	results, err := repo.ListByAgentType(ctx, "non-existent-agent", "tenant-1", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestToolRepository_UpdateUsage_Success tests updating usage with success.
func TestToolRepository_UpdateUsage_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "usage-tool",
		Description:      "Tool for usage test",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		UsageCount:       0,
		SuccessRate:      0.0,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Update usage with success
	err = repo.UpdateUsage(ctx, tool.ID, true)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, tool.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, retrieved.UsageCount)
	assert.Greater(t, retrieved.SuccessRate, 0.0)
	assert.NotNil(t, retrieved.LastUsedAt)
}

// TestToolRepository_UpdateUsage_Failure tests updating usage with failure.
func TestToolRepository_UpdateUsage_Failure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "usage-fail-tool",
		Description:      "Tool for usage fail test",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		UsageCount:       0,
		SuccessRate:      1.0,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Update usage with failure
	err = repo.UpdateUsage(ctx, tool.ID, false)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, tool.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, retrieved.UsageCount)
	assert.Less(t, retrieved.SuccessRate, 1.0)
}

// TestToolRepository_UpdateUsage_NotFound tests updating usage for non-existent tool.
func TestToolRepository_UpdateUsage_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	err := repo.UpdateUsage(ctx, "00000000-0000-0000-0000-000000000000", true)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestToolRepository_UpdateUsage_MultipleUpdates tests consecutive usage updates.
func TestToolRepository_UpdateUsage_MultipleUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "multi-usage-tool",
		Description:      "Tool for multiple usage updates",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		UsageCount:       0,
		SuccessRate:      0.0,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Update usage multiple times
	for i := 0; i < 5; i++ {
		success := i < 4 // 4 successes, 1 failure
		err = repo.UpdateUsage(ctx, tool.ID, success)
		require.NoError(t, err)
	}

	// Verify cumulative update
	retrieved, err := repo.GetByID(ctx, tool.ID)
	require.NoError(t, err)
	assert.Equal(t, 5, retrieved.UsageCount)
	// UpdateUsage uses moving average: 4 successes, 1 failure
	// Formula: new_rate = old_rate * 0.9 + (success ? 1.0 : 0.0) * 0.1
	// Sequence: 0.0 -> 0.1 -> 0.19 -> 0.271 -> 0.3439 -> 0.30951
	assert.InDelta(t, 0.31, retrieved.SuccessRate, 0.01)
}

// TestToolRepository_UpdateEmbedding tests updating tool embedding.
func TestToolRepository_UpdateEmbedding(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool with initial embedding
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "embedding-tool",
		Description:      "Tool for embedding update",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Update embedding
	newEmbedding := createTestEmbedding()
	newEmbedding[0] = 1.0 // Modify first element
	err = repo.UpdateEmbedding(ctx, tool.ID, newEmbedding, "e5-large-v2", 2)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, tool.ID)
	require.NoError(t, err)
	assert.Equal(t, newEmbedding[0], retrieved.Embedding[0])
	assert.Equal(t, "e5-large-v2", retrieved.EmbeddingModel)
	assert.Equal(t, 2, retrieved.EmbeddingVersion)
}

// TestToolRepository_UpdateEmbedding_NotFound tests updating embedding for non-existent tool.
func TestToolRepository_UpdateEmbedding_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	newEmbedding := createTestEmbedding()
	err := repo.UpdateEmbedding(ctx, "00000000-0000-0000-0000-000000000000", newEmbedding, "e5-large", 1)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestToolRepository_ListByTags tests listing tools by tags.
func TestToolRepository_ListByTags(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tools with different tags
	tools := []*storage_models.Tool{
		{
			TenantID:         "tenant-1",
			Name:             "api-tool",
			Description:      "API tool",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Tags:             []string{"api", "http"},
			UsageCount:       5,
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Name:             "database-tool",
			Description:      "Database tool",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Tags:             []string{"database", "sql"},
			UsageCount:       3,
			CreatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Name:             "http-tool",
			Description:      "HTTP tool",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Tags:             []string{"http", "network"},
			UsageCount:       4,
			CreatedAt:        time.Now(),
		},
	}

	for _, tool := range tools {
		err := repo.Create(ctx, tool)
		require.NoError(t, err)
	}

	// List by single tag
	results, err := repo.ListByTags(ctx, []string{"http"}, "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// List by multiple tags
	results, err = repo.ListByTags(ctx, []string{"api", "http"}, "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2) // Should match tools with either "api" or "http" tag
}

// TestToolRepository_ListByTags_EmptyTags tests listing tools with empty tags.
func TestToolRepository_ListByTags_EmptyTags(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "no-tags-tool",
		Description:      "Tool with no tags",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Tags:             []string{},
		UsageCount:       1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// List with empty tags should return no results
	results, err := repo.ListByTags(ctx, []string{}, "tenant-1", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestToolRepository_ListByTags_TenantIsolation tests tenant isolation in tag listing.
func TestToolRepository_ListByTags_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tool for tenant-1
	tool1 := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "isolation-tool",
		Description:      "Tool for tenant 1",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Tags:             []string{"isolation"},
		UsageCount:       1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool1)
	require.NoError(t, err)

	// List for tenant-2 should return no results
	results, err := repo.ListByTags(ctx, []string{"isolation"}, "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestToolRepository_MetadataHandling tests metadata field handling.
func TestToolRepository_MetadataHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tool with complex metadata
	metadata := map[string]interface{}{
		"version":    2,
		"enabled":    true,
		"config":     map[string]interface{}{"timeout": 30},
		"parameters": []string{"param1", "param2"},
		"count":      42,
	}

	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "metadata-tool",
		Description:      "Tool with complex metadata",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Metadata:         metadata,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Retrieve and verify metadata
	retrieved, err := repo.GetByID(ctx, tool.ID)
	require.NoError(t, err)
	assert.Equal(t, float64(2), retrieved.Metadata["version"])
	assert.True(t, retrieved.Metadata["enabled"].(bool))
	assert.Equal(t, float64(30), retrieved.Metadata["config"].(map[string]interface{})["timeout"])
	assert.Equal(t, float64(42), retrieved.Metadata["count"])
}

// TestToolRepository_EmbeddingHandling tests embedding field handling.
func TestToolRepository_EmbeddingHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tool with specific embedding
	embedding := make([]float64, 1024)
	for i := 0; i < 1024; i++ {
		embedding[i] = float64(i) / 1024.0
	}

	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "embedding-tool",
		Description:      "Tool with specific embedding",
		Embedding:        embedding,
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Retrieve and verify embedding
	retrieved, err := repo.GetByID(ctx, tool.ID)
	require.NoError(t, err)
	assert.Len(t, retrieved.Embedding, 1024)
	for i := 0; i < 1024; i++ {
		assert.InDelta(t, embedding[i], retrieved.Embedding[i], 0.0001)
	}
}

// TestToolRepository_ConcurrentOperations tests concurrent operations.
func TestToolRepository_ConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "concurrent-tool",
		Description:      "Tool for concurrent operations",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		UsageCount:       0,
		SuccessRate:      0.0,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	require.NoError(t, err)

	// Perform concurrent operations
	errCh := make(chan error, 10)

	// Concurrent reads
	for i := 0; i < 3; i++ {
		go func() {
			_, err := repo.GetByID(ctx, tool.ID)
			errCh <- err
		}()
	}

	// Concurrent usage updates
	for i := 0; i < 3; i++ {
		go func() {
			err := repo.UpdateUsage(ctx, tool.ID, true)
			errCh <- err
		}()
	}

	// Concurrent updates
	for i := 0; i < 2; i++ {
		go func(idx int) {
			toolCopy := &storage_models.Tool{
				ID:               tool.ID,
				TenantID:         tool.TenantID,
				Name:             fmt.Sprintf("concurrent-tool-%d", idx),
				Description:      tool.Description,
				Embedding:        tool.Embedding,
				EmbeddingModel:   tool.EmbeddingModel,
				EmbeddingVersion: tool.EmbeddingVersion,
				UsageCount:       tool.UsageCount,
				SuccessRate:      tool.SuccessRate,
				CreatedAt:        tool.CreatedAt,
			}
			err := repo.Update(ctx, toolCopy)
			errCh <- err
		}(i)
	}

	// Concurrent vector searches
	for i := 0; i < 2; i++ {
		go func() {
			_, err := repo.SearchByVector(ctx, createTestEmbedding(), "tenant-1", 10)
			errCh <- err
		}()
	}

	// Collect errors
	for i := 0; i < 10; i++ {
		err := <-errCh
		if err != nil {
			t.Errorf("Concurrent operation failed: %v", err)
		}
	}
}

// TestToolRepository_ContextCancelled tests context cancellation.
func TestToolRepository_ContextCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Create a tool
	tool := &storage_models.Tool{
		TenantID:         "tenant-1",
		Name:             "context-tool",
		Description:      "Tool for context test",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}
	err := repo.Create(ctx, tool)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

// TestToolRepository_AllTagsTypes tests various tag configurations.
func TestToolRepository_AllTagsTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	// Create tools with different tag configurations
	testCases := []struct {
		name     string
		tags     []string
		expected []string
	}{
		{"single-tag", []string{"api"}, []string{"api"}},
		{"multiple-tags", []string{"api", "http", "rest"}, []string{"api", "http", "rest"}},
		{"empty-tags", []string{}, []string{}},
	}

	for _, tc := range testCases {
		tool := &storage_models.Tool{
			TenantID:         "tenant-1",
			Name:             tc.name,
			Description:      "Tool with tags: " + tc.name,
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Tags:             tc.tags,
			UsageCount:       1,
			CreatedAt:        time.Now(),
		}
		err := repo.Create(ctx, tool)
		require.NoError(t, err, "Failed to create tool: %s", tc.name)

		// Retrieve and verify tags
		retrieved, err := repo.GetByID(ctx, tool.ID)
		require.NoError(t, err, "Failed to retrieve tool: %s", tc.name)
		assert.Equal(t, tc.expected, retrieved.Tags, "Tags mismatch for: %s", tc.name)
	}
}
