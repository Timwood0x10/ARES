// package memory provides an in-memory vector store implementation.
// Use this for development, testing, or when no external vector DB is available.
package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/Timwood0x10/ares/internal/storage"
)

// VectorStore is an in-memory implementation of storage.VectorStore.
type VectorStore struct {
	mu          sync.RWMutex
	collections map[string]*collection
}

type collection struct {
	dimension int
	vectors   map[string]*vectorEntry
}

type vectorEntry struct {
	id       string
	vector   []float64
	metadata map[string]any
}

// NewVectorStore creates a new in-memory vector store.
func NewVectorStore() *VectorStore {
	return &VectorStore{
		collections: make(map[string]*collection),
	}
}

// Search performs brute-force cosine similarity search.
func (v *VectorStore) Search(_ context.Context, table string, embedding []float64, limit int) ([]*storage.SearchResult, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	col, ok := v.collections[table]
	if !ok {
		return nil, nil
	}

	type scored struct {
		id       string
		score    float64
		metadata map[string]any
	}

	results := make([]scored, 0, len(col.vectors))
	for _, entry := range col.vectors {
		score := cosineSimilarity(embedding, entry.vector)
		results = append(results, scored{
			id:       entry.id,
			score:    score,
			metadata: entry.metadata,
		})
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if limit > len(results) {
		limit = len(results)
	}

	out := make([]*storage.SearchResult, limit)
	for i := 0; i < limit; i++ {
		out[i] = &storage.SearchResult{
			ID:       results[i].id,
			Score:    results[i].score,
			Metadata: results[i].metadata,
		}
	}
	return out, nil
}

// AddEmbedding stores a vector in the specified collection.
func (v *VectorStore) AddEmbedding(_ context.Context, table, id string, embedding []float64, metadata map[string]any) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	col, ok := v.collections[table]
	if !ok {
		return fmt.Errorf("collection %q not found", table)
	}

	col.vectors[id] = &vectorEntry{
		id:       id,
		vector:   embedding,
		metadata: metadata,
	}
	return nil
}

// CreateCollection creates a new vector collection.
func (v *VectorStore) CreateCollection(_ context.Context, name string, dimension int) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, exists := v.collections[name]; exists {
		return nil // already exists
	}

	v.collections[name] = &collection{
		dimension: dimension,
		vectors:   make(map[string]*vectorEntry),
	}
	return nil
}

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
