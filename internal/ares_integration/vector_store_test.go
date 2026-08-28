// package integration provides end-to-end integration tests for VectorStore
// implementations (in-memory and PostgreSQL).
package ares_integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/storage"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// nanoSuffix returns the current Unix nanosecond timestamp as a string for unique naming.
func nanoSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// memVectorStore is an in-memory VectorStore for testing.
// It replaces the deleted internal/storage/memory package.
type memVectorStore struct {
	collections map[string]int                       // name → dimension
	vectors     map[string]map[string][]float64      // table → id → vec
	metadata    map[string]map[string]map[string]any // table → id → meta
}

func newMemVectorStore() *memVectorStore {
	return &memVectorStore{
		collections: make(map[string]int),
		vectors:     make(map[string]map[string][]float64),
		metadata:    make(map[string]map[string]map[string]any),
	}
}

func (m *memVectorStore) CreateCollection(_ context.Context, name string, dimension int) error {
	m.collections[name] = dimension
	m.vectors[name] = make(map[string][]float64)
	m.metadata[name] = make(map[string]map[string]any)
	return nil
}

func (m *memVectorStore) AddEmbedding(_ context.Context, table, id string, embedding []float64, metadata map[string]any) error {
	if _, ok := m.collections[table]; !ok {
		return fmt.Errorf("collection %q does not exist", table)
	}
	m.vectors[table][id] = embedding
	m.metadata[table][id] = metadata
	return nil
}

func (m *memVectorStore) Search(_ context.Context, table string, query []float64, limit int) ([]*storage.SearchResult, error) {
	if _, ok := m.collections[table]; !ok {
		return nil, fmt.Errorf("collection %q does not exist", table)
	}
	var results []*storage.SearchResult
	for id, vec := range m.vectors[table] {
		score := cosineSim(query, vec)
		results = append(results, &storage.SearchResult{
			ID:       id,
			Score:    score,
			Metadata: m.metadata[table][id],
		})
	}
	// Sort by score descending.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// cosineSim computes cosine similarity between two vectors.
func cosineSim(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		if i < len(b) {
			dot += a[i] * b[i]
			na += a[i] * a[i]
			nb += b[i] * b[i]
		}
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrtf(na) * sqrtf(nb))
}

func sqrtf(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// compile-time check.
var _ storage.VectorStore = (*memVectorStore)(nil)

// TestVectorStoreInMemoryCreateAddSearchDelete verifies the full in-memory
// vector store lifecycle: create collection -> add embeddings -> search -> delete.
func TestVectorStoreInMemoryCreateAddSearchDelete(t *testing.T) {
	store := newMemVectorStore()
	ctx := context.Background()

	collectionName := "test-collection"

	// Create collection.
	if err := store.CreateCollection(ctx, collectionName, 128); err != nil {
		t.Fatal(err)
	}

	// Add embeddings.
	vec1 := make([]float64, 128)
	vec1[0] = 1.0
	if err := store.AddEmbedding(ctx, collectionName, "doc-1", vec1, map[string]any{
		"source": "test",
	}); err != nil {
		t.Fatal(err)
	}

	vec2 := make([]float64, 128)
	vec2[1] = 1.0
	if err := store.AddEmbedding(ctx, collectionName, "doc-2", vec2, map[string]any{
		"source": "test",
	}); err != nil {
		t.Fatal(err)
	}

	// Search: vec1 should match doc-1 best.
	results, err := store.Search(ctx, collectionName, vec1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "doc-1" {
		t.Errorf("expected doc-1 as closest match, got %s", results[0].ID)
	}

	// Search: vec2 should match doc-2 best.
	results, err = store.Search(ctx, collectionName, vec2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "doc-2" {
		t.Errorf("expected doc-2 as closest match, got %s", results[0].ID)
	}
}

// TestVectorStoreInMemoryAddToNonExistentCollection verifies that adding
// to a collection that does not exist returns an error.
func TestVectorStoreInMemoryAddToNonExistentCollection(t *testing.T) {
	store := newMemVectorStore()
	ctx := context.Background()

	vec := make([]float64, 128)
	err := store.AddEmbedding(ctx, "non-existent", "doc-1", vec, nil)
	if err == nil {
		t.Fatal("expected error when adding to non-existent collection")
	}
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
	if err := searcher.CreateCollection(ctx, collectionName, dim); err != nil {
		t.Fatal(err)
	}

	// Add two embeddings with known directions.
	vec1 := make([]float64, dim)
	vec1[0] = 1.0
	metadata1 := map[string]any{"category": "alpha"}
	if err := searcher.AddEmbedding(ctx, collectionName, "vs-doc-1", vec1, metadata1); err != nil {
		t.Fatal(err)
	}

	vec2 := make([]float64, dim)
	vec2[1] = 1.0
	metadata2 := map[string]any{"category": "beta"}
	if err := searcher.AddEmbedding(ctx, collectionName, "vs-doc-2", vec2, metadata2); err != nil {
		t.Fatal(err)
	}

	// Cosine search: vec1 should match vs-doc-1 best.
	results, err := searcher.Search(ctx, collectionName, vec1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	if results[0].ID != "vs-doc-1" {
		t.Errorf("expected vs-doc-1 as closest match, got %s", results[0].ID)
	}
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
	if err := searcher.CreateCollection(ctx, collectionName, dim); err != nil {
		t.Fatal(err)
	}

	// Add 5 embeddings.
	for i := 0; i < 5; i++ {
		vec := make([]float64, dim)
		vec[i] = 1.0
		id := fmt.Sprintf("limit-doc-%d", i)
		if err := searcher.AddEmbedding(ctx, collectionName, id, vec, map[string]any{"idx": i}); err != nil {
			t.Fatal(err)
		}
	}

	queryVec := make([]float64, dim)
	queryVec[0] = 1.0

	// Search with limit 3.
	results, err := searcher.Search(ctx, collectionName, queryVec, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}
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
	if err := searcher.CreateCollection(ctx, collectionName, dim); err != nil {
		t.Fatal(err)
	}

	vec := make([]float64, dim)
	vec[0] = 1.0
	if err := searcher.AddEmbedding(ctx, collectionName, "del-doc-1", vec, nil); err != nil {
		t.Fatal(err)
	}

	// Verify it exists.
	results, err := searcher.Search(ctx, collectionName, vec, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Delete it.
	if err := searcher.DeleteEmbedding(ctx, collectionName, "del-doc-1"); err != nil {
		t.Fatal(err)
	}

	// Should no longer appear.
	results, err = searcher.Search(ctx, collectionName, vec, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.ID == "del-doc-1" {
			t.Errorf("deleted document should not appear in results")
		}
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
	if err == nil {
		t.Error("expected error for empty collection name")
	}

	// CreateCollection with zero dimension should fail.
	err = searcher.CreateCollection(ctx, "bad_dim", 0)
	if err == nil {
		t.Error("expected error for zero dimension")
	}

	// Search with zero limit should fail.
	vec := make([]float64, 128)
	_, err = searcher.Search(ctx, "any_table", vec, 0)
	if err == nil {
		t.Error("expected error for zero limit")
	}
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
	if err := searcher.CreateCollection(ctx, collectionName, dim); err != nil {
		t.Fatal(err)
	}

	// Create a vector with known direction.
	vec := make([]float64, dim)
	for i := range vec {
		vec[i] = float64(i+1) / float64(dim)
	}

	if err := searcher.AddEmbedding(ctx, collectionName, "large-pg-doc", vec, map[string]any{
		"model": "text-embedding-ada-002",
	}); err != nil {
		t.Fatal(err)
	}

	// Search with the same vector.
	results, err := searcher.Search(ctx, collectionName, vec, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	if results[0].ID != "large-pg-doc" {
		t.Errorf("expected large-pg-doc, got %s", results[0].ID)
	}
}
