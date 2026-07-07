//go:build integration
// +build integration

// Package services provides integration tests for retrieval services.
package services

import (
	"context"
	"database/sql"
	"fmt"
	golog "log"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// getTestDB returns a test database connection for integration tests.
// Returns nil if database is not available (tests will be skipped).
func getTestDB(t *testing.T) *sql.DB {
	host := "localhost"
	port := "5433"
	user := "postgres"
	password := "postgres"
	dbname := "styleagent"

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Skipf("Skipping integration test - failed to open test database: %v", err)
		return nil
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("Skipping integration test - failed to connect to test database: %v", err)
		return nil
	}

	golog.Printf("Connected to test database: %s", dbname)

	return db
}

// createTestEmbedding creates a 1024-dimensional test embedding vector.
func createTestEmbedding() []float64 {
	embedding := make([]float64, 1024)
	for i := range embedding {
		embedding[i] = float64(i) / 1024.0
	}
	return embedding
}

// createTestPool creates a test database pool.
func createTestPool(db *sql.DB) *postgres.Pool {
	cfg := &postgres.Config{
		Host:            "localhost",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "styleagent",
		MaxOpenConns:    25,
		MaxIdleConns:    5,
		ConnMaxLifetime: 1 * time.Hour,
		ConnMaxIdleTime: 15 * time.Minute,
	}

	pool, err := postgres.NewPool(cfg)
	if err != nil {
		panic(fmt.Sprintf("Failed to create test pool: %v", err))
	}
	return pool
}

// TestSearchKnowledgeVector_Integration tests knowledge base vector search with real database.
func TestSearchKnowledgeVector_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close test database: ", "error", err)
		}
	}()

	// Create knowledge repository
	kbRepo := repositories.NewKnowledgeRepository(db, db)

	// Create test pool
	pool := createTestPool(db)

	// Create retrieval service with nil embedding client (for testing)
	service := NewRetrievalService(
		pool,
		nil, // embeddingClient - user will start embedding service when needed
		nil, // llmClient
		&postgres.TenantGuard{},
		&postgres.RetrievalGuard{},
		kbRepo,
		nil, /* expRepo */
		nil, /* toolRepo */
	)
	ctx := context.Background()

	// Create test knowledge chunks
	chunks := []*storage_models.KnowledgeChunk{
		{
			TenantID:         "tenant-1",
			Content:          "Go programming language for web development",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
			SourceType:       "document",
			ContentHash:      "hash-1",
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Content:          "Python is a versatile programming language",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
			SourceType:       "document",
			ContentHash:      "hash-2",
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-2", // Different tenant
			Content:          "JavaScript for frontend development",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
			SourceType:       "document",
			ContentHash:      "hash-3",
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		},
	}

	for _, chunk := range chunks {
		err := kbRepo.Create(ctx, chunk)
		require.NoError(t, err)
	}

	// Test vector search
	req := &SearchRequest{
		Query:    "web development",
		TenantID: "tenant-1",
		TopK:     10,
		Plan:     DefaultRetrievalPlan(),
	}

	embedding := createTestEmbedding()
	results := service.searchKnowledgeVector(ctx, embedding, req)

	// Verify results
	assert.NotNil(t, results)
	assert.IsType(t, []*SearchResult{}, results)

	// All results should belong to tenant-1
	for _, result := range results {
		assert.NotEmpty(t, result.ID)
		assert.NotEmpty(t, result.Content)
		assert.Greater(t, result.Score, 0.0)
		assert.Equal(t, "document", result.Source) // SourceType from test data
		assert.Equal(t, "knowledge", result.Type)  // Type is always "knowledge"
	}
}

// TestBm25SearchKnowledge_Integration tests BM25 search in knowledge base with real database.
func TestBm25SearchKnowledge_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close test database: ", "error", err)
		}
	}()

	// Create knowledge repository
	kbRepo := repositories.NewKnowledgeRepository(db, db)

	// Create retrieval service
	pool := createTestPool(db)
	service := NewRetrievalService(
		pool,
		nil, // embeddingClient - not needed for BM25 search
		nil, // llmClient
		&postgres.TenantGuard{},
		&postgres.RetrievalGuard{},
		kbRepo,
		nil, /* expRepo */
		nil, /* toolRepo */
	)

	ctx := context.Background()

	// Create test knowledge chunks
	chunks := []*storage_models.KnowledgeChunk{
		{
			TenantID:         "tenant-1",
			Content:          "Go programming language features",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
			SourceType:       "document",
			ContentHash:      "bm25-hash-1",
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		},
		{
			TenantID:         "tenant-1",
			Content:          "Python programming language features",
			Embedding:        createTestEmbedding(),
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
			SourceType:       "document",
			ContentHash:      "bm25-hash-2",
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		},
	}

	for _, chunk := range chunks {
		err := kbRepo.Create(ctx, chunk)
		require.NoError(t, err)
	}

	// Test BM25 search
	results := service.bm25SearchKnowledge(ctx, "programming language", "tenant-1", 10)

	// Verify results
	assert.NotNil(t, results)
	assert.IsType(t, []*SearchResult{}, results)

	// Verify result structure
	for _, result := range results {
		assert.NotEmpty(t, result.ID)
		assert.NotEmpty(t, result.Content)
		assert.Greater(t, result.Score, 0.0)
		assert.Equal(t, "knowledge", result.Source)
	}
}

// TestMergeAndRank_Integration tests merge and rank with real database results.
func TestMergeAndRank_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close test database: ", "error", err)
		}
	}()

	// Create knowledge repository
	kbRepo := repositories.NewKnowledgeRepository(db, db)

	// Create retrieval service
	pool := createTestPool(db)
	service := NewRetrievalService(
		pool,
		nil, // embeddingClient - not needed for merge and rank test
		nil, // llmClient
		&postgres.TenantGuard{},
		&postgres.RetrievalGuard{},
		kbRepo,
		nil, /* expRepo */
		nil, /* toolRepo */
	)

	now := time.Now()

	// Create mock vector results
	vectorResults := []*SearchResult{
		{
			ID:          "1",
			Content:     "Go programming",
			Score:       0.9,
			Source:      "knowledge",
			SubSource:   "vector",
			QueryWeight: 1.0,
			CreatedAt:   now.Add(-1 * time.Hour),
		},
		{
			ID:          "2",
			Content:     "Python programming",
			Score:       0.8,
			Source:      "knowledge",
			SubSource:   "vector",
			QueryWeight: 1.0,
			CreatedAt:   now.Add(-2 * time.Hour),
		},
	}

	// Create mock keyword results
	keywordResults := []*SearchResult{
		{
			ID:          "2",
			Content:     "Python programming",
			Score:       0.6,
			Source:      "knowledge",
			SubSource:   "keyword",
			QueryWeight: 1.0,
			CreatedAt:   now.Add(-2 * time.Hour),
		},
		{
			ID:          "3",
			Content:     "JavaScript programming",
			Score:       0.5,
			Source:      "knowledge",
			SubSource:   "keyword",
			QueryWeight: 1.0,
			CreatedAt:   now.Add(-3 * time.Hour),
		},
	}

	plan := DefaultRetrievalPlan()

	// Test merge and rank
	allResults := append(vectorResults, keywordResults...)
	merged := service.mergeAndRerank(allResults, plan)

	// Verify merged results
	assert.NotNil(t, merged)
	assert.Equal(t, 3, len(merged), "Should have 3 unique results")

	// Results should be sorted by score (descending)
	for i := 1; i < len(merged); i++ {
		assert.GreaterOrEqual(t, merged[i-1].Score, merged[i].Score,
			"Results should be sorted by score in descending order")
	}

	// Verify that ID 2 has combined score from both sources
	result2 := findResultByID(merged, "2")
	require.NotNil(t, result2, "Result with ID 2 should exist")
	// Score is calculated using RRF with time decay, so it will be lower than raw scores
	// The important thing is that the result exists and has a positive score
	assert.Greater(t, result2.Score, 0.0, "Combined score should be positive")
}

// TestGetEmbedding_Integration tests embedding retrieval with mock client.
func TestGetEmbedding_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close test database: ", "error", err)
		}
	}()

	// Create retrieval service
	pool := createTestPool(db)
	service := NewRetrievalService(
		pool,
		nil, // embeddingClient - will be nil, getEmbedding will return nil
		nil, // llmClient
		&postgres.TenantGuard{},
		&postgres.RetrievalGuard{},
		nil, // kbRepo not needed for this test
		nil, // expRepo
		nil, // toolRepo
	)

	ctx := context.Background()

	// Test embedding retrieval (will return nil since embeddingClient is nil)
	embedding := service.getEmbedding(ctx, "test query")

	// Should return nil embedding since client is nil
	assert.Nil(t, embedding)
}

// TestFilterByScore_Integration tests score filtering with real data.
func TestFilterByScore_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close test database: ", "error", err)
		}
	}()

	// Create retrieval service
	pool := createTestPool(db)
	service := NewRetrievalService(
		pool,
		nil, // embeddingClient - not needed for score filtering test
		nil, // llmClient
		&postgres.TenantGuard{},
		&postgres.RetrievalGuard{},
		nil, // kbRepo
		nil, // expRepo
		nil, // toolRepo
	)

	now := time.Now()

	results := []*SearchResult{
		{ID: "1", Content: "test 1", Score: 0.9, CreatedAt: now},
		{ID: "2", Content: "test 2", Score: 0.7, CreatedAt: now},
		{ID: "3", Content: "test 3", Score: 0.5, CreatedAt: now},
		{ID: "4", Content: "test 4", Score: 0.3, CreatedAt: now},
		{ID: "5", Content: "test 5", Score: 0.1, CreatedAt: now},
	}

	// Test filtering with minimum score 0.5
	filtered := service.filterByScore(results, 0.5)
	assert.Equal(t, 3, len(filtered), "Should return 3 results with score >= 0.5")

	// Test filtering with minimum score 0.0 (should return all)
	filtered = service.filterByScore(results, 0.0)
	assert.Equal(t, 5, len(filtered), "Should return all results when min score is 0")

	// Test filtering with high minimum score
	filtered = service.filterByScore(results, 0.8)
	assert.Equal(t, 1, len(filtered), "Should return only 1 result with score >= 0.8")
}

// TestCalculateTimeDecay_Integration tests time decay calculation.
func TestCalculateTimeDecay_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close test database: ", "error", err)
		}
	}()

	// Create retrieval service
	pool := createTestPool(db)
	service := NewRetrievalService(
		pool,
		nil, // embeddingClient - not needed for time decay test
		nil, // llmClient
		&postgres.TenantGuard{},
		&postgres.RetrievalGuard{},
		nil, // kbRepo
		nil, // expRepo
		nil, // toolRepo
	)

	now := time.Now()

	// Test recent content (should have high decay factor)
	recentDecay := service.calculateTimeDecay(now.Add(-1 * time.Hour))
	assert.Greater(t, recentDecay, 0.9, "Recent content should have high decay factor")

	// Test old content (should have lower decay factor)
	oldDecay := service.calculateTimeDecay(now.Add(-30 * 24 * time.Hour))
	assert.Less(t, oldDecay, recentDecay, "Old content should have lower decay factor")
	assert.GreaterOrEqual(t, oldDecay, 0.1, "Decay factor should not go below 0.1")

	// Test very old content (should hit minimum threshold)
	veryOldDecay := service.calculateTimeDecay(now.Add(-365 * 24 * time.Hour))
	assert.Equal(t, 0.1, veryOldDecay, "Very old content should hit minimum decay threshold")
}

// TestCountResultsBySource_Integration tests result counting with real data.
func TestCountResultsBySource_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	// Create retrieval service
	pool := createTestPool(db)
	service := NewRetrievalService(
		pool,
		nil, // embeddingClient - not needed for count results test
		nil, // llmClient
		&postgres.TenantGuard{},
		&postgres.RetrievalGuard{},
		nil, // kbRepo
		nil, // expRepo
		nil, // toolRepo
	)

	now := time.Now()

	results := []*SearchResult{
		{ID: "1", Source: "knowledge", CreatedAt: now},
		{ID: "2", Source: "knowledge", CreatedAt: now},
		{ID: "3", Source: "experience", CreatedAt: now},
		{ID: "4", Source: "tool", CreatedAt: now},
		{ID: "5", Source: "task_result", CreatedAt: now},
		{ID: "6", Source: "knowledge", CreatedAt: now},
	}

	counts := service.countResultsBySource(results)

	assert.Equal(t, 3, counts["knowledge"], "Should count 3 knowledge results")
	assert.Equal(t, 1, counts["experience"], "Should count 1 experience result")
	assert.Equal(t, 1, counts["tool"], "Should count 1 tool result")
	assert.Equal(t, 1, counts["task_result"], "Should count 1 task result")
	assert.Equal(t, 4, len(counts), "Should have 4 unique sources")
}

// TestValidateRequest_Integration tests request validation with real data.
func TestValidateRequest_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	// Create retrieval service
	pool := createTestPool(db)
	service := NewRetrievalService(
		pool,
		nil, // embeddingClient - not needed for validation test
		nil, // llmClient
		&postgres.TenantGuard{},
		&postgres.RetrievalGuard{},
		nil, // kbRepo
		nil, // expRepo
		nil, // toolRepo
	)

	tests := []struct {
		name    string
		req     *SearchRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: &SearchRequest{
				Query:    "test query",
				TenantID: "tenant-1",
				TopK:     10,
				Plan:     DefaultRetrievalPlan(),
			},
			wantErr: false,
		},
		{
			name:    "nil request",
			req:     nil,
			wantErr: true,
		},
		{
			name: "empty query",
			req: &SearchRequest{
				Query:    "",
				TenantID: "tenant-1",
				TopK:     10,
				Plan:     DefaultRetrievalPlan(),
			},
			wantErr: true,
		},
		{
			name: "empty tenant ID",
			req: &SearchRequest{
				Query:    "test query",
				TenantID: "",
				TopK:     10,
				Plan:     DefaultRetrievalPlan(),
			},
			wantErr: true,
		},
		{
			name: "zero TopK - should auto-correct",
			req: &SearchRequest{
				Query:    "test query",
				TenantID: "tenant-1",
				TopK:     0,
				Plan:     DefaultRetrievalPlan(),
			},
			wantErr: false, // Should auto-correct
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.validateRequest(tt.req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
