package context

import (
	"context"
	"math"
	"sort"
	"sync"

	"goagentx/internal/storage/postgres"
)

// VectorSearcher defines the interface for vector storage operations.
type VectorSearcher interface {
	Search(ctx context.Context, table string, query []float64, limit int) ([]*postgres.SearchResult, error)
	AddEmbedding(ctx context.Context, table, id string, embedding []float64, metadata map[string]any) error
	DeleteEmbedding(ctx context.Context, table, id string) error
	CreateVectorTable(ctx context.Context, table string, metadataSchema string) error
}

// KnowledgeEntry represents a knowledge base entry.
type KnowledgeEntry struct {
	ID        string
	Content   string
	Embedding []float64
	Metadata  map[string]interface{}
}

// RAG provides retrieval-augmented generation capabilities.
type RAG struct {
	entries       map[string]*KnowledgeEntry
	index         *VectorIndex
	mu            sync.RWMutex
	maxSize       int
	usePersistent bool           // Use pgvector instead of in-memory
	vectorSearch  VectorSearcher // Persistent storage (optional)
	tableName     string         // Table name for persistent storage
}

// VectorIndex is a simple in-memory vector index.
type VectorIndex struct {
	entries []*KnowledgeEntry
}

// Option is a function that configures RAG.
type Option func(*RAG)

// WithPersistentStorage enables pgvector storage.
func WithPersistentStorage(searcher VectorSearcher, tableName string) Option {
	return func(r *RAG) {
		r.usePersistent = true
		r.vectorSearch = searcher
		r.tableName = tableName
	}
}

// NewRAG creates a new RAG instance with in-memory storage.
func NewRAG(maxSize int) *RAG {
	return &RAG{
		entries: make(map[string]*KnowledgeEntry),
		index:   NewVectorIndex(),
		maxSize: maxSize,
	}
}

// NewRAGWithOptions creates a new RAG instance with options.
func NewRAGWithOptions(maxSize int, opts ...Option) *RAG {
	r := &RAG{
		entries: make(map[string]*KnowledgeEntry),
		index:   NewVectorIndex(),
		maxSize: maxSize,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// NewVectorIndex creates a new VectorIndex.
func NewVectorIndex() *VectorIndex {
	return &VectorIndex{
		entries: make([]*KnowledgeEntry, 0),
	}
}

// Add adds a knowledge entry.
func (r *RAG) Add(ctx context.Context, entry *KnowledgeEntry) error {
	if len(r.entries) >= r.maxSize {
		r.evictOldest()
	}

	// Persist to pgvector first to ensure consistency
	if r.usePersistent && r.vectorSearch != nil {
		if err := r.vectorSearch.AddEmbedding(ctx, r.tableName, entry.ID, entry.Embedding, entry.Metadata); err != nil {
			return err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries[entry.ID] = entry
	r.index.entries = append(r.index.entries, entry)

	return nil
}

// Get retrieves a knowledge entry by ID.
func (r *RAG) Get(ctx context.Context, id string) (*KnowledgeEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, exists := r.entries[id]
	return entry, exists
}

// Search searches for similar entries using simple cosine similarity.
func (r *RAG) Search(ctx context.Context, query []float64, topK int) ([]*KnowledgeEntry, error) {
	// If using persistent storage, use pgvector search (outside lock)
	if r.usePersistent && r.vectorSearch != nil {
		results, err := r.vectorSearch.Search(ctx, r.tableName, query, topK)
		if err != nil {
			return nil, err
		}

		r.mu.RLock()
		entries := make([]*KnowledgeEntry, 0, len(results))
		for _, result := range results {
			if entry, exists := r.entries[result.ID]; exists {
				entries = append(entries, entry)
			}
		}
		r.mu.RUnlock()
		return entries, nil
	}

	// Fallback to in-memory search
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.index.entries) == 0 {
		return nil, nil
	}

	scores := make([]struct {
		entry *KnowledgeEntry
		score float64
	}, 0, len(r.index.entries))

	for _, entry := range r.index.entries {
		score := cosineSimilarity(query, entry.Embedding)
		scores = append(scores, struct {
			entry *KnowledgeEntry
			score float64
		}{entry, score})
	}

	// Sort by score descending
	sort.Slice(scores, func(i, j int) bool {
		return scores[j].score < scores[i].score
	})

	result := make([]*KnowledgeEntry, 0, topK)
	for i := 0; i < topK && i < len(scores); i++ {
		result = append(result, scores[i].entry)
	}

	return result, nil
}

// SearchByText searches for entries matching text content.
func (r *RAG) SearchByText(ctx context.Context, query string, topK int) ([]*KnowledgeEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matches := make([]*KnowledgeEntry, 0)

	for _, entry := range r.entries {
		if contains(entry.Content, query) {
			matches = append(matches, entry)
		}
	}

	if len(matches) > topK {
		matches = matches[:topK]
	}

	return matches, nil
}

// Delete removes a knowledge entry.
func (r *RAG) Delete(ctx context.Context, id string) error {
	// Delete from persistent storage first
	if r.usePersistent && r.vectorSearch != nil {
		if err := r.vectorSearch.DeleteEmbedding(ctx, r.tableName, id); err != nil {
			return err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.entries, id)

	r.index.entries = make([]*KnowledgeEntry, 0)
	for _, entry := range r.entries {
		r.index.entries = append(r.index.entries, entry)
	}

	return nil
}

// Size returns the number of entries.
func (r *RAG) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.entries)
}

// InitStorage initializes the persistent storage table.
func (r *RAG) InitStorage(ctx context.Context) error {
	if r.usePersistent && r.vectorSearch != nil {
		return r.vectorSearch.CreateVectorTable(ctx, r.tableName, "")
	}
	return nil
}

// IsPersistent returns true if using persistent storage.
func (r *RAG) IsPersistent() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usePersistent
}

// evictOldest removes entries when capacity is reached.
func (r *RAG) evictOldest() {
	if len(r.index.entries) == 0 {
		return
	}

	oldest := r.index.entries[0]
	delete(r.entries, oldest.ID)
	r.index.entries = r.index.entries[1:]
}

// cosineSimilarity calculates cosine similarity between two vectors.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	// Optimization: Use single sqrt instead of two for better performance
	// math.Sqrt(normA) * math.Sqrt(normB) == math.Sqrt(normA * normB)
	return dotProduct / math.Sqrt(normA*normB)
}

// contains checks if text contains substring (simple implementation).
func contains(text, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(text)-len(substr); i++ {
		if text[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
