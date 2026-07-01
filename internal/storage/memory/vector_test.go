package memory

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage"
)

// ---------------------------------------------------------------------------
// Collection lifecycle
// ---------------------------------------------------------------------------

func TestCreateCollection(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()

	err := store.CreateCollection(ctx, "my-collection", 128)
	require.NoError(t, err)
}

func TestCreateCollectionIsIdempotent(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()

	require.NoError(t, store.CreateCollection(ctx, "dup", 64))
	require.NoError(t, store.CreateCollection(ctx, "dup", 128),
		"creating the same collection again should not error")
}

func TestAddEmbeddingToNonExistentCollection(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()

	err := store.AddEmbedding(ctx, "does-not-exist", "doc-1", []float64{1, 2, 3}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSearchNonExistentCollection(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()

	results, err := store.Search(ctx, "does-not-exist", []float64{1, 0}, 10)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
	assert.Nil(t, results)
}

func TestSearchEmptyCollection(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCollection(ctx, "empty", 4))

	results, err := store.Search(ctx, "empty", []float64{1, 0, 0, 0}, 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// ---------------------------------------------------------------------------
// Happy path: create → add → search
// ---------------------------------------------------------------------------

func TestCreateAddSearchBasic(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()

	dim := 4
	require.NoError(t, store.CreateCollection(ctx, "vectors", dim))

	v1 := []float64{1, 0, 0, 0}
	v2 := []float64{0, 1, 0, 0}
	v3 := []float64{0, 0, 1, 0}

	require.NoError(t, store.AddEmbedding(ctx, "vectors", "doc-1", v1, map[string]any{"idx": 1}))
	require.NoError(t, store.AddEmbedding(ctx, "vectors", "doc-2", v2, map[string]any{"idx": 2}))
	require.NoError(t, store.AddEmbedding(ctx, "vectors", "doc-3", v3, map[string]any{"idx": 3}))

	results, err := store.Search(ctx, "vectors", v1, 10)
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, "doc-1", results[0].ID, "closest to v1 should be doc-1")
	assert.InDelta(t, 1.0, results[0].Score, 0.001)
}

// ---------------------------------------------------------------------------
// Search limit
// ---------------------------------------------------------------------------

func TestSearchRespectsLimit(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCollection(ctx, "limit-test", 4))

	for i := 0; i < 10; i++ {
		v := []float64{float64(i), 0, 0, 0}
		require.NoError(t, store.AddEmbedding(ctx, "limit-test", fmtID(i), v, nil))
	}

	query := []float64{5, 0, 0, 0}
	results, err := store.Search(ctx, "limit-test", query, 3)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestSearchLimitGreaterThanResults(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCollection(ctx, "few", 2))

	require.NoError(t, store.AddEmbedding(ctx, "few", "a", []float64{1, 0}, nil))
	require.NoError(t, store.AddEmbedding(ctx, "few", "b", []float64{0, 1}, nil))

	results, err := store.Search(ctx, "few", []float64{1, 0}, 100)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

// ---------------------------------------------------------------------------
// Error: nil embedding
// ---------------------------------------------------------------------------

func TestSearchNilEmbedding(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCollection(ctx, "c", 2))
	require.NoError(t, store.AddEmbedding(ctx, "c", "x", []float64{1, 0}, nil))

	results, err := store.Search(ctx, "c", nil, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedding must not be nil")
	assert.Nil(t, results)
}

// ---------------------------------------------------------------------------
// Cosine similarity edge cases
// ---------------------------------------------------------------------------

func TestCosineSimilarityExactMatch(t *testing.T) {
	a := []float64{1, 2, 3, 4}
	b := []float64{1, 2, 3, 4}
	assert.InDelta(t, 1.0, cosineSimilarity(a, b), 0.001)
}

func TestCosineSimilarityOrthogonal(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{0, 1, 0}
	assert.InDelta(t, 0.0, cosineSimilarity(a, b), 0.001)
}

func TestCosineSimilarityOpposite(t *testing.T) {
	a := []float64{1, 0}
	b := []float64{-1, 0}
	assert.InDelta(t, -1.0, cosineSimilarity(a, b), 0.001)
}

func TestCosineSimilarityDifferentLengths(t *testing.T) {
	assert.Equal(t, 0.0, cosineSimilarity([]float64{1, 0}, []float64{1, 0, 0}),
		"different length vectors should return 0")
}

func TestCosineSimilarityEmptyVectors(t *testing.T) {
	assert.Equal(t, 0.0, cosineSimilarity([]float64{}, []float64{}),
		"empty vectors should return 0")
}

func TestCosineSimilarityNilVectors(t *testing.T) {
	assert.Equal(t, 0.0, cosineSimilarity(nil, nil),
		"nil vectors should return 0 (len mismatch shortcut)")
}

func TestCosineSimilarityOneNil(t *testing.T) {
	assert.Equal(t, 0.0, cosineSimilarity(nil, []float64{1, 2}),
		"nil vs non-nil should return 0")
}

func TestCosineSimilarityZeroVectors(t *testing.T) {
	a := []float64{0, 0, 0}
	b := []float64{0, 0, 0}
	assert.Equal(t, 0.0, cosineSimilarity(a, b),
		"all-zero vectors should return 0 (denominator is zero)")
}

func TestCosineSimilarityOneZeroVector(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{0, 0, 0}
	assert.Equal(t, 0.0, cosineSimilarity(a, b),
		"zero query vector should return 0")
}

func TestCosineSimilarityPartialOverlap(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{1, 1, 0}
	// dot=1, |a|=1, |b|=√2 ≈ 1.414, score = 1/1.414 ≈ 0.707
	assert.InDelta(t, 0.7071, cosineSimilarity(a, b), 0.001)
}

// ---------------------------------------------------------------------------
// Result ordering
// ---------------------------------------------------------------------------

func TestSearchResultsOrderedByScoreDescending(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCollection(ctx, "rank", 2))

	require.NoError(t, store.AddEmbedding(ctx, "rank", "far", []float64{0, 1}, nil))
	require.NoError(t, store.AddEmbedding(ctx, "rank", "close", []float64{0.9, 0.1}, nil))
	require.NoError(t, store.AddEmbedding(ctx, "rank", "exact", []float64{1, 0}, nil))

	results, err := store.Search(ctx, "rank", []float64{1, 0}, 10)
	require.NoError(t, err)
	require.Len(t, results, 3)

	assert.Equal(t, "exact", results[0].ID, "highest score first")
	assert.Equal(t, "close", results[1].ID, "middle")
	assert.Equal(t, "far", results[2].ID, "lowest score last")

	assert.True(t, results[0].Score >= results[1].Score,
		"scores must be non-increasing")
	assert.True(t, results[1].Score >= results[2].Score,
		"scores must be non-increasing")
}

// ---------------------------------------------------------------------------
// Metadata is preserved
// ---------------------------------------------------------------------------

func TestSearchPreservesMetadata(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCollection(ctx, "meta-test", 2))

	meta := map[string]any{"title": "hello", "tags": []string{"go", "test"}}
	require.NoError(t, store.AddEmbedding(ctx, "meta-test", "with-meta", []float64{1, 0}, meta))

	results, err := store.Search(ctx, "meta-test", []float64{1, 0}, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "hello", results[0].Metadata["title"])
	assert.Equal(t, []string{"go", "test"}, results[0].Metadata["tags"])
}

func TestSearchNilMetadata(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCollection(ctx, "nil-meta", 2))
	require.NoError(t, store.AddEmbedding(ctx, "nil-meta", "no-meta", []float64{1, 0}, nil))

	results, err := store.Search(ctx, "nil-meta", []float64{1, 0}, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Nil(t, results[0].Metadata)
}

// ---------------------------------------------------------------------------
// 1536-dimension large vectors
// ---------------------------------------------------------------------------

func TestLargeDimensionVectors(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()

	dim := 1536
	require.NoError(t, store.CreateCollection(ctx, "large", dim))

	vec := make([]float64, dim)
	for i := range vec {
		vec[i] = float64(i+1) / float64(dim)
	}

	require.NoError(t, store.AddEmbedding(ctx, "large", "big-doc", vec,
		map[string]any{"model": "text-embedding-ada-002"}))

	results, err := store.Search(ctx, "large", vec, 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "big-doc", results[0].ID)
	assert.InDelta(t, 1.0, results[0].Score, 0.001)

	orthogonal := make([]float64, dim)
	orthogonal[0] = 1.0
	results, err = store.Search(ctx, "large", orthogonal, 1)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.True(t, math.Abs(results[0].Score) < 0.1,
		"orthogonal vector should have near-zero similarity")
}

// ---------------------------------------------------------------------------
// Concurrent safety (racy detector)
// ---------------------------------------------------------------------------

func TestConcurrentAddAndSearch(t *testing.T) {
	store := NewVectorStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCollection(ctx, "concurrent", 8))

	const goroutines = 32
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*2)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			vec := make([]float64, 8)
			vec[i%8] = 1.0
			if e := store.AddEmbedding(ctx, "concurrent", fmtID(i), vec, nil); e != nil {
				errCh <- e
			}
		}()
	}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			vec := make([]float64, 8)
			vec[i%8] = 1.0
			if _, e := store.Search(ctx, "concurrent", vec, 5); e != nil {
				errCh <- e
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for e := range errCh {
		require.NoError(t, e, "concurrent operations must not error")
	}
}

// ---------------------------------------------------------------------------
// Compile-time interface compliance check
// ---------------------------------------------------------------------------

func TestImplementsVectorStoreInterface(t *testing.T) {
	var store storage.VectorStore = NewVectorStore()
	_ = store
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func fmtID(i int) string {
	return fmt.Sprintf("doc-%d", i)
}
