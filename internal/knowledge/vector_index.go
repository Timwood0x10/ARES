package knowledge

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// VectorIndex abstracts vector storage and approximate nearest-neighbor (ANN)
// search so that specialized vector databases — pgvector, Milvus, Weaviate,
// Qdrant, etc. — can be plugged in WITHOUT changing the KnowledgeStore
// interface.
//
// Rationale: the built-in HybridSearch implementations recall vectors from
// each store's own table (JSON-serialized for SQLite/MySQL, REAL[] for
// PostgreSQL). That is enough to prove the loop, but a production deployment
// usually wants a dedicated ANN index for scale and latency. VectorIndex is
// the seam: a store (or the runtime) may delegate vector recall to a
// VectorIndex while keeping KnowledgeStore.HybridSearch's contract identical.
// The in-memory default (InMemoryVectorIndex) covers tests and single-node use.
//
// The interface is intentionally tiny (Upsert / Search / Delete) and keyed by
// (objectID, model) to mirror Representation, so an adapter can be written in
// either direction without leaking store internals.
type VectorIndex interface {
	// Upsert stores or replaces the vector for (objectID, model). An empty or
	// nil vector is treated as a deletion.
	Upsert(ctx context.Context, objectID, model string, vec []float32) error

	// Search returns up to topK entries for model whose vectors are most
	// similar to queryVec by cosine similarity, ordered by descending score.
	// Implementations must handle topK <= 0 by returning an empty slice.
	Search(ctx context.Context, model string, queryVec []float32, topK int) ([]VectorHit, error)

	// Delete removes the vector for (objectID, model). A missing entry is a
	// no-op and never an error.
	Delete(ctx context.Context, objectID, model string) error
}

// VectorHit is a single nearest-neighbor result from VectorIndex.Search.
type VectorHit struct {
	ObjectID string
	// Score is the cosine similarity in [-1, 1]; higher is more similar.
	Score float64
}

// InMemoryVectorIndex is the default VectorIndex: a thread-safe, brute-force
// index suitable for tests and single-node deployments. Recall is O(n) per
// query, which is fine up to a few tens of thousands of vectors; beyond that,
// swap in a remote ANN index — the contract is unchanged.
type InMemoryVectorIndex struct {
	// vecs is keyed by model, then by objectID. The outer map is guarded by mu
	// so concurrent Upsert/Search/Delete are race-free.
	mu   sync.RWMutex
	vecs map[string]map[string][]float32
}

// NewInMemoryVectorIndex creates an empty in-memory vector index.
func NewInMemoryVectorIndex() *InMemoryVectorIndex {
	return &InMemoryVectorIndex{
		vecs: make(map[string]map[string][]float32),
	}
}

// Upsert stores or replaces the vector for (objectID, model). An empty vector
// deletes the entry, keeping the index free of zero-length placeholders that
// would always score 0 in cosine similarity.
func (i *InMemoryVectorIndex) Upsert(_ context.Context, objectID, model string, vec []float32) error {
	if objectID == "" || model == "" {
		return errors.New("vector index: objectID and model are required")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(vec) == 0 {
		if objs, ok := i.vecs[model]; ok {
			delete(objs, objectID)
		}
		return nil
	}
	// Copy so the caller cannot mutate stored state after hand-off.
	cp := make([]float32, len(vec))
	copy(cp, vec)
	objs, ok := i.vecs[model]
	if !ok {
		objs = make(map[string][]float32)
		i.vecs[model] = objs
	}
	objs[objectID] = cp
	return nil
}

// Search returns the topK most similar vectors for model via brute-force
// cosine similarity. An empty model, empty query vector, or topK <= 0 yields
// an empty (nil) result.
func (i *InMemoryVectorIndex) Search(_ context.Context, model string, queryVec []float32, topK int) ([]VectorHit, error) {
	if topK <= 0 || len(queryVec) == 0 || model == "" {
		return nil, nil
	}
	i.mu.RLock()
	objs := i.vecs[model]
	if len(objs) == 0 {
		i.mu.RUnlock()
		return nil, nil
	}
	hits := make([]VectorHit, 0, len(objs))
	for id, vec := range objs {
		hits = append(hits, VectorHit{ObjectID: id, Score: CosineSimilarity(queryVec, vec)})
	}
	i.mu.RUnlock()

	sort.SliceStable(hits, func(a, b int) bool {
		return hits[a].Score > hits[b].Score
	})
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// Delete removes the vector for (objectID, model). Missing entries are a no-op.
func (i *InMemoryVectorIndex) Delete(_ context.Context, objectID, model string) error {
	if objectID == "" || model == "" {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if objs, ok := i.vecs[model]; ok {
		delete(objs, objectID)
	}
	return nil
}

// compile-time guard: InMemoryVectorIndex satisfies VectorIndex.
var _ VectorIndex = (*InMemoryVectorIndex)(nil)
