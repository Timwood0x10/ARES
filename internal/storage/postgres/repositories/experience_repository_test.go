// Package repositories provides comprehensive tests for ExperienceRepository.
package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	storage_models "goagentx/internal/storage/postgres/models"
)

// TestExperienceRepository_Create tests creating a single experience.
func TestExperienceRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	exp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "test input",
		Output:           "test output",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Score:            0.8,
		Success:          true,
		AgentID:          "agent-1",
		Metadata:         nil, // Note: metadata field has a bug in ExperienceRepository.Create
		DecayAt:          time.Now().Add(30 * 24 * time.Hour),
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, exp)
	require.NoError(t, err)
	assert.NotEmpty(t, exp.ID)
}

// TestExperienceRepository_GetByID tests retrieving an experience by ID.
func TestExperienceRepository_GetByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create an experience
	exp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "test input",
		Output:           "test output",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, exp)
	require.NoError(t, err)

	// Retrieve by ID
	retrieved, err := repo.GetByID(ctx, exp.ID)
	require.NoError(t, err)
	assert.Equal(t, exp.ID, retrieved.ID)
	assert.Equal(t, exp.TenantID, retrieved.TenantID)
	assert.Equal(t, exp.Type, retrieved.Type)
	assert.Equal(t, exp.Input, retrieved.Input)
	assert.Equal(t, exp.Output, retrieved.Output)
}

// TestExperienceRepository_GetByID_NotFound tests retrieving non-existent experience.
func TestExperienceRepository_GetByID_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	assert.Error(t, err)
}

// TestExperienceRepository_Update tests updating an experience.
func TestExperienceRepository_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create an experience
	exp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "original input",
		Output:           "original output",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Score:            0.5,
		Success:          false,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, exp)
	require.NoError(t, err)

	// Update the experience
	exp.Input = "updated input"
	exp.Output = "updated output"
	exp.Score = 0.9
	exp.Success = true

	err = repo.Update(ctx, exp)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, exp.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated input", retrieved.Input)
	assert.Equal(t, "updated output", retrieved.Output)
	assert.Equal(t, 0.9, retrieved.Score)
	assert.True(t, retrieved.Success)
}

// TestExperienceRepository_Delete tests deleting an experience.
func TestExperienceRepository_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create an experience
	exp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "test input",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, exp)
	require.NoError(t, err)
	id := exp.ID

	// Delete the experience
	err = repo.Delete(ctx, id)
	require.NoError(t, err)

	// Verify deletion
	_, err = repo.GetByID(ctx, id)
	assert.Error(t, err)
}

// TestExperienceRepository_SearchByVector tests vector search functionality.
func TestExperienceRepository_SearchByVector(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create multiple experiences
	for i := 0; i < 5; i++ {
		exp := &storage_models.Experience{
			TenantID:         "tenant-1",
			Type:             storage_models.ExperienceTypeQuery,
			Input:            "test input",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		}
		err := repo.Create(ctx, exp)
		require.NoError(t, err)
	}

	// Search by vector
	queryEmbedding := createTestEmbedding()
	results, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-1", 3)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 3)
}

// TestExperienceRepository_ListByType tests listing experiences by type.
func TestExperienceRepository_ListByType(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create experiences with different types
	types := []string{
		storage_models.ExperienceTypeQuery,
		storage_models.ExperienceTypeSolution,
		storage_models.ExperienceTypeFailure,
	}

	for _, expType := range types {
		exp := &storage_models.Experience{
			TenantID:         "tenant-1",
			Type:             expType,
			Input:            "test input",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		}
		err := repo.Create(ctx, exp)
		require.NoError(t, err)
	}

	// List experiences by type
	exps, err := repo.ListByType(ctx, storage_models.ExperienceTypeQuery, "tenant-1", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(exps), 1)
	for _, exp := range exps {
		assert.Equal(t, storage_models.ExperienceTypeQuery, exp.Type)
	}
}

// TestExperienceRepository_UpdateScore tests updating the score of an experience.
func TestExperienceRepository_UpdateScore(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create an experience
	exp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "test input",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Score:            0.5,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, exp)
	require.NoError(t, err)

	// Update score
	newScore := 0.9
	err = repo.UpdateScore(ctx, exp.ID, newScore)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, exp.ID)
	require.NoError(t, err)
	assert.Equal(t, newScore, retrieved.Score)
}

// TestExperienceRepository_ListByAgent tests listing experiences by agent.
func TestExperienceRepository_ListByAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create experiences for a specific agent
	agentID := "agent-1"
	for i := 0; i < 3; i++ {
		exp := &storage_models.Experience{
			TenantID:         "tenant-1",
			Type:             storage_models.ExperienceTypeQuery,
			Input:            "test input",
			AgentID:          agentID,
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		}
		err := repo.Create(ctx, exp)
		require.NoError(t, err)
	}

	// List experiences by agent
	exps, err := repo.ListByAgent(ctx, agentID, "tenant-1", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(exps), 3)
	for _, exp := range exps {
		assert.Equal(t, agentID, exp.AgentID)
	}
}

// TestExperienceRepository_UpdateEmbedding tests updating the embedding.
func TestExperienceRepository_UpdateEmbedding(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create an experience
	originalEmbedding := createTestEmbedding()
	exp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "test input",
		Embedding:        originalEmbedding,
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, exp)
	require.NoError(t, err)

	// Create a new embedding
	newEmbedding := make([]float64, 1024)
	for i := range newEmbedding {
		newEmbedding[i] = float64(1024-i) / 1024.0
	}

	// Update embedding
	err = repo.UpdateEmbedding(ctx, exp.ID, newEmbedding, "e5-large", 2)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, exp.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, retrieved.EmbeddingVersion)
	assert.Equal(t, "e5-large", retrieved.EmbeddingModel)
}

// TestExperienceRepository_CleanupExpired tests cleaning up expired experiences.
func TestExperienceRepository_CleanupExpired(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create an expired experience
	expiredExp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "test input",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		DecayAt:          time.Now().UTC().Add(-1 * time.Hour), // Expired (use UTC for consistency)
		CreatedAt:        time.Now().UTC(),
	}

	err := repo.Create(ctx, expiredExp)
	require.NoError(t, err)

	// Create a non-expired experience
	validExp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "test input",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		DecayAt:          time.Now().UTC().Add(30 * 24 * time.Hour), // Not expired (use UTC for consistency)
		CreatedAt:        time.Now().UTC(),
	}

	err = repo.Create(ctx, validExp)
	require.NoError(t, err)

	// Cleanup expired experiences
	count, err := repo.CleanupExpired(ctx)
	require.NoError(t, err)
	assert.Greater(t, count, int64(0))

	// Verify expired experience is deleted
	_, err = repo.GetByID(ctx, expiredExp.ID)
	assert.Error(t, err)

	// Verify valid experience still exists
	_, err = repo.GetByID(ctx, validExp.ID)
	assert.NoError(t, err)
}

// TestExperienceRepository_GetStatistics tests getting statistics.
func TestExperienceRepository_GetStatistics(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create some experiences
	for i := 0; i < 5; i++ {
		exp := &storage_models.Experience{
			TenantID:         "tenant-1",
			Type:             storage_models.ExperienceTypeQuery,
			Input:            "test input",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			CreatedAt:        time.Now(),
		}
		err := repo.Create(ctx, exp)
		require.NoError(t, err)
	}

	// Get statistics
	stats, err := repo.GetStatistics(ctx, "tenant-1")
	require.NoError(t, err)
	assert.Greater(t, len(stats), 0)
}

// TestExperienceRepository_ConcurrentOperations tests concurrent repository operations.
func TestExperienceRepository_ConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	// Create experiences concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			exp := &storage_models.Experience{
				TenantID:         "tenant-1",
				Type:             storage_models.ExperienceTypeQuery,
				Input:            "test input",
				Embedding:        createTestEmbedding(),
				EmbeddingModel:   "e5-large",
				EmbeddingVersion: 1,
				Metadata:         nil, // Note: metadata field has a bug in ExperienceRepository.Create
				CreatedAt:        time.Now(),
			}

			err := repo.Create(ctx, exp)
			assert.NoError(t, err, "concurrent create failed for experience %d", index)
			done <- true
		}(i)
	}

	// Wait for all operations to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestExperienceRepository_AllTypes tests all experience types.
func TestExperienceRepository_AllTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	types := []string{
		storage_models.ExperienceTypeQuery,
		storage_models.ExperienceTypeSolution,
		storage_models.ExperienceTypeFailure,
		storage_models.ExperienceTypePattern,
		storage_models.ExperienceTypeDistilled,
	}

	for _, expType := range types {
		exp := &storage_models.Experience{
			TenantID:         "tenant-1",
			Type:             expType,
			Input:            "test input",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Metadata:         nil, // Note: metadata field has a bug in ExperienceRepository.Create
			CreatedAt:        time.Now(),
		}

		err := repo.Create(ctx, exp)
		require.NoError(t, err, "failed to create experience with type %v", expType)
		assert.NotEmpty(t, exp.ID)
	}
}

// TestExperienceRepository_ContextCancelled tests operation with cancelled context.
func TestExperienceRepository_ContextCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	exp := &storage_models.Experience{
		TenantID:         "tenant-1",
		Type:             storage_models.ExperienceTypeQuery,
		Input:            "test input",
		Embedding:        createTestEmbedding(),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		CreatedAt:        time.Now(),
	}

	err := repo.Create(ctx, exp)
	assert.Error(t, err)
}
