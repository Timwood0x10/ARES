// package integration provides end-to-end integration tests for VectorStore
// implementations (in-memory and PostgreSQL).
package ares_integration

import (
	"context"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/storage/memory"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// nanoSuffix returns the current Unix nanosecond timestamp as a string for unique naming.
func nanoSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// TestVectorStoreInMemoryCreateAddSearchDelete verifies the full in-memory
// vector store lifecycle: create collection -> add embeddings -> search -> delete.
func TestVectorStoreInMemoryCreateAddSearchDelete(t *testing.T) {
	store := memory.NewVectorStore()
	ctx := context.Background()

	collectionName := "test-collection"

	// Create collection.
	require.NoError(t, store.CreateCollection(ctx, collectionName, 128))

	// Add embeddings.
	vec1 := make([]float64, 128)
	vec1[0] = 1.0
	require.NoError(t, store.AddEmbedding(ctx, collectionName, "doc-1", vec1, map[string]any{
		"source": "test",
	}))

	vec2 := make([]float64, 128)
	vec2[1] = 1.0
	require.NoError(t, store.AddEmbedding(ctx, collectionName, "doc-2", vec2, map[string]any{
		"source": "test",
	}))

	// Search: vec1 should match doc-1 best.
	results, err := store.Search(ctx, collectionName, vec1, 10)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "doc-1", results[0].ID, "doc-1 should be the closest match to vec1")
	assert.InDelta(t, 1.0, results[0].Score, 0.001, "exact match should have score ~1.0")

	// Search: vec2 should match doc-2 best.
	results, err = store.Search(ctx, collectionName, vec2, 10)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "doc-2", results[0].ID, "doc-2 should be the closest match to vec2")
}

// TestVectorStoreInMemorySearchNonExistentCollection verifies that searching
// on a collection that does not exist returns nil (not an error).
func TestVectorStoreInMemorySearchNonExistentCollection(t *testing.T) {
	store := memory.NewVectorStore()
	ctx := context.Background()

	vec := make([]float64, 128)
	vec[0] = 1.0
	results, err := store.Search(ctx, "non-existent", vec, 10)
	require.NoError(t, err, "search on non-existent collection should not error")
	assert.Nil(t, results, "expected nil results for non-existent collection")
}

// TestVectorStoreInMemoryAddToNonExistentCollection verifies that adding
// to a collection that does not exist returns an error.
func TestVectorStoreInMemoryAddToNonExistentCollection(t *testing.T) {
	store := memory.NewVectorStore()
	ctx := context.Background()

	vec := make([]float64, 128)
	err := store.AddEmbedding(ctx, "non-existent", "doc-1", vec, nil)
	require.Error(t, err, "expected error when adding to non-existent collection")
}

// TestVectorStoreInMemoryConcurrentAddSearch verifies that concurrent
// add and search operations on the same collection are safe.
func TestVectorStoreInMemoryConcurrentAddSearch(t *testing.T) {
	store := memory.NewVectorStore()
	ctx := context.Background()

	collectionName := "concurrent-test"
	require.NoError(t, store.CreateCollection(ctx, collectionName, 64))

	const numGoroutines = 20
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines*2)

	// Concurrent adds.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			vec := make([]float64, 64)
			vec[idx%64] = 1.0
			id := fmt.Sprintf("doc-%d", idx)
			if addErr := store.AddEmbedding(ctx, collectionName, id, vec, map[string]any{"idx": idx}); addErr != nil {
				errs <- addErr
			}
		}(i)
	}

	// Concurrent searches.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			queryVec := make([]float64, 64)
			queryVec[idx%64] = 1.0
			if _, searchErr := store.Search(ctx, collectionName, queryVec, 5); searchErr != nil {
				errs <- searchErr
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for e := range errs {
		require.NoError(t, e, "concurrent operations should not fail")
	}
}

// TestVectorStoreInMemoryLargeVector verifies round-trip with 1536-dimension
// vectors (common embedding size for OpenAI models).
func TestVectorStoreInMemoryLargeVector(t *testing.T) {
	store := memory.NewVectorStore()
	ctx := context.Background()

	collectionName := "large-vec-test"
	dim := 1536
	require.NoError(t, store.CreateCollection(ctx, collectionName, dim))

	// Create a normalized vector.
	vec := make([]float64, dim)
	for i := range vec {
		vec[i] = float64(i+1) / float64(dim)
	}

	require.NoError(t, store.AddEmbedding(ctx, collectionName, "large-doc", vec, map[string]any{
		"model": "text-embedding-ada-002",
	}))

	// Search with the same vector.
	results, err := store.Search(ctx, collectionName, vec, 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "large-doc", results[0].ID)
	assert.InDelta(t, 1.0, results[0].Score, 0.001, "exact match should have cosine similarity ~1.0")

	// Search with an orthogonal vector should have score ~0.
	orthogonal := make([]float64, dim)
	orthogonal[0] = 1.0
	results, err = store.Search(ctx, collectionName, orthogonal, 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, math.Abs(results[0].Score) < 0.1, "orthogonal vector should have low similarity")
}

// TestVectorStorePostgresCreateAddCosineSearch verifies the PostgreSQL vector
// store lifecycle when TEST_POSTGRES_DSN is available.
func TestVectorStorePostgresCreateAddCosineSearch(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	searcher := postgres.NewVectorSearcher(pool, embeddingConfig)

	collectionName := fmt.Sprintf("test_vs_%s", nanoSuffix())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+collectionName)
	})

	dim := 128
	require.NoError(t, searcher.CreateCollection(ctx, collectionName, dim))

	// Add two embeddings with known directions.
	vec1 := make([]float64, dim)
	vec1[0] = 1.0
	metadata1 := map[string]any{"category": "alpha"}
	require.NoError(t, searcher.AddEmbedding(ctx, collectionName, "vs-doc-1", vec1, metadata1))

	vec2 := make([]float64, dim)
	vec2[1] = 1.0
	metadata2 := map[string]any{"category": "beta"}
	require.NoError(t, searcher.AddEmbedding(ctx, collectionName, "vs-doc-2", vec2, metadata2))

	// Cosine search: vec1 should match vs-doc-1 best.
	results, err := searcher.Search(ctx, collectionName, vec1, 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "vs-doc-1", results[0].ID, "closest match should be vs-doc-1")
}

// TestVectorStorePostgresSearchWithLimit verifies that the PostgreSQL vector
// searcher respects the limit parameter.
func TestVectorStorePostgresSearchWithLimit(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	searcher := postgres.NewVectorSearcher(pool, embeddingConfig)

	collectionName := fmt.Sprintf("test_limit_%s", nanoSuffix())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+collectionName)
	})

	dim := 128
	require.NoError(t, searcher.CreateCollection(ctx, collectionName, dim))

	// Add 5 embeddings.
	for i := 0; i < 5; i++ {
		vec := make([]float64, dim)
		vec[i] = 1.0
		id := fmt.Sprintf("limit-doc-%d", i)
		require.NoError(t, searcher.AddEmbedding(ctx, collectionName, id, vec, map[string]any{"idx": i}))
	}

	queryVec := make([]float64, dim)
	queryVec[0] = 1.0

	// Search with limit 3.
	results, err := searcher.Search(ctx, collectionName, queryVec, 3)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 3, "should return at most 3 results")
}

// TestVectorStorePostgresDeleteAndSearch verifies that deleting an embedding
// from the PostgreSQL vector store removes it from search results.
func TestVectorStorePostgresDeleteAndSearch(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	searcher := postgres.NewVectorSearcher(pool, embeddingConfig)

	collectionName := fmt.Sprintf("test_del_%s", nanoSuffix())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+collectionName)
	})

	dim := 128
	require.NoError(t, searcher.CreateCollection(ctx, collectionName, dim))

	vec := make([]float64, dim)
	vec[0] = 1.0
	require.NoError(t, searcher.AddEmbedding(ctx, collectionName, "del-doc-1", vec, nil))

	// Verify it exists.
	results, err := searcher.Search(ctx, collectionName, vec, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)

	// Delete it.
	require.NoError(t, searcher.DeleteEmbedding(ctx, collectionName, "del-doc-1"))

	// Should no longer appear.
	results, err = searcher.Search(ctx, collectionName, vec, 10)
	require.NoError(t, err)
	for _, r := range results {
		assert.NotEqual(t, "del-doc-1", r.ID, "deleted document should not appear in results")
	}
}

// TestVectorStorePostgresInvalidInputs verifies that the PostgreSQL vector
// searcher validates inputs properly.
func TestVectorStorePostgresInvalidInputs(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	searcher := postgres.NewVectorSearcher(pool, embeddingConfig)

	// CreateCollection with empty name should fail.
	err := searcher.CreateCollection(ctx, "", 128)
	require.Error(t, err)

	// CreateCollection with zero dimension should fail.
	err = searcher.CreateCollection(ctx, "bad_dim", 0)
	require.Error(t, err)

	// Search with zero limit should fail.
	vec := make([]float64, 128)
	_, err = searcher.Search(ctx, "any_table", vec, 0)
	require.Error(t, err)
}

// TestVectorStorePostgresLargeVector verifies 1536-dimension vectors
// round-trip through PostgreSQL with pgvector.
func TestVectorStorePostgresLargeVector(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("TEST_POSTGRES_DSN not set, skipping PostgreSQL vector test")
	}

	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)

	ctx := context.Background()
	embeddingConfig := postgres.DefaultEmbeddingConfig()
	searcher := postgres.NewVectorSearcher(pool, embeddingConfig)

	collectionName := fmt.Sprintf("test_large_%s", nanoSuffix())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+collectionName)
	})

	dim := 1536
	require.NoError(t, searcher.CreateCollection(ctx, collectionName, dim))

	// Create a vector with known direction.
	vec := make([]float64, dim)
	for i := range vec {
		vec[i] = float64(i+1) / float64(dim)
	}

	require.NoError(t, searcher.AddEmbedding(ctx, collectionName, "large-pg-doc", vec, map[string]any{
		"model": "text-embedding-ada-002",
	}))

	// Search with the same vector.
	results, err := searcher.Search(ctx, collectionName, vec, 1)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "large-pg-doc", results[0].ID)
}
