package vector

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/storage"
)

// memVectorStore is an in-memory VectorStore for testing.
type memVectorStore struct {
	mu          sync.Mutex
	collections map[string]int // name → dimension
	vectors     map[string][]float64
	metadata    map[string]map[string]any
}

func newMemVectorStore() *memVectorStore {
	return &memVectorStore{
		collections: make(map[string]int),
		vectors:     make(map[string][]float64),
		metadata:    make(map[string]map[string]any),
	}
}

func (m *memVectorStore) Search(_ context.Context, table string, _ []float64, limit int) ([]*storage.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.collections[table]; !ok {
		return nil, nil
	}

	var results []*storage.SearchResult
	for id := range m.vectors {
		if len(results) >= limit {
			break
		}
		meta := m.metadata[id]
		if meta == nil {
			meta = make(map[string]any)
		}
		results = append(results, &storage.SearchResult{
			ID:       id,
			Score:    0.85,
			Metadata: meta,
		})
	}
	return results, nil
}

func (m *memVectorStore) AddEmbedding(_ context.Context, table, id string, embedding []float64, metadata map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vectors[id] = embedding
	m.metadata[id] = metadata
	return nil
}

func (m *memVectorStore) CreateCollection(_ context.Context, name string, dimension int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.collections[name] = dimension
	return nil
}

func TestVectorProvider_Name(t *testing.T) {
	store := newMemVectorStore()
	p, err := NewVectorProvider(store, Config{Name: "test-vec", Collection: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "test-vec" {
		t.Errorf("Name() = %q, want %q", p.Name(), "test-vec")
	}
}

func TestVectorProvider_IntentMatch(t *testing.T) {
	store := newMemVectorStore()
	p, err := NewVectorProvider(store, Config{
		Name:       "test-vec",
		Collection: "docs",
		IntentTags: []string{"knowledge", "doc", "guide"},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		goal  string
		want  float64
		label string
	}{
		{"how to write documentation", 0.0, "should match doc tag"},
		{"knowledge base query", 0.0, "should match knowledge tag"},
		{"unrelated query about cooking", 0.2, "weak match"},
		{"", 0.4, "empty goal -> generic"},
	}

	for _, tt := range tests {
		got := p.IntentMatch(knowledge.Intent{Goal: tt.goal})
		if got < tt.want {
			t.Errorf("IntentMatch(%q) = %.2f, want >= %.2f (%s)", tt.goal, got, tt.want, tt.label)
		}
	}
}

func TestVectorProvider_Stream(t *testing.T) {
	ctx := context.Background()
	store := newMemVectorStore()

	// Seed test data.
	_ = store.CreateCollection(ctx, "docs", 4)
	_ = store.AddEmbedding(ctx, "docs", "doc-1", []float64{0.1, 0.2, 0.3, 0.4}, map[string]any{
		"summary": "PostgreSQL connection pooling with pgx",
		"tags":    []string{"postgres", "pool"},
	})

	p, err := NewVectorProvider(store, Config{
		Name:       "vec-test",
		Namespace:  "test",
		Collection: "docs",
	})
	if err != nil {
		t.Fatal(err)
	}

	intent := knowledge.Intent{
		Goal: "how to configure connection pooling",
		Scope: knowledge.Scope{
			MaxObjects: 10,
		},
	}

	objCh, errCh := p.Stream(ctx, intent)

	var objects []*knowledge.KnowledgeObject
	for obj := range objCh {
		objects = append(objects, obj)
	}

	// Check error channel.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	default:
	}

	if len(objects) == 0 {
		t.Fatal("expected at least 1 object from stream, got 0")
	}

	obj := objects[0]
	if obj.ID != "test:doc-1" {
		t.Errorf("object ID = %q, want %q", obj.ID, "test:doc-1")
	}
	if obj.Summary != "PostgreSQL connection pooling with pgx" {
		t.Errorf("object Summary = %q, want %q", obj.Summary, "PostgreSQL connection pooling with pgx")
	}
	// Confidence is the reliability prior (not the vector score).
	if obj.Confidence != vectorReliability {
		t.Errorf("object Confidence = %v, want %v (reliability prior)", obj.Confidence, vectorReliability)
	}
	// Relevance carries the vector similarity score.
	if obj.Relevance != 0.85 {
		t.Errorf("object Relevance = %v, want 0.85 (vector score)", obj.Relevance)
	}
}

func TestVectorProvider_Stream_EmptyCollection(t *testing.T) {
	ctx := context.Background()
	store := newMemVectorStore()

	p, err := NewVectorProvider(store, Config{
		Name:       "empty-vec",
		Namespace:  "test",
		Collection: "nonexistent",
	})
	if err != nil {
		t.Fatal(err)
	}

	intent := knowledge.Intent{Goal: "anything", Scope: knowledge.Scope{MaxObjects: 5}}
	objCh, errCh := p.Stream(ctx, intent)

	count := 0
	for range objCh {
		count++
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("expected collection-missing error: %v", err)
		}
	default:
	}

	if count != 0 {
		t.Errorf("expected 0 objects from empty collection, got %d", count)
	}
}

func TestVectorProvider_Validation(t *testing.T) {
	store := newMemVectorStore()

	tests := []struct {
		name    string
		store   storage.VectorStore
		cfg     Config
		wantErr bool
	}{
		{"nil store", nil, Config{Name: "x", Collection: "c"}, true},
		{"empty name", store, Config{Name: "", Collection: "c"}, true},
		{"empty collection", store, Config{Name: "x", Collection: ""}, true},
		{"valid", store, Config{Name: "x", Collection: "c"}, false},
	}

	for _, tt := range tests {
		_, err := NewVectorProvider(tt.store, tt.cfg)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: NewVectorProvider() error = %v, wantErr = %v", tt.name, err, tt.wantErr)
		}
	}
}

// compile-time check.
var _ storage.VectorStore = (*memVectorStore)(nil)

// mockEmbedder is a deterministic EmbeddingService for tests: it maps text to
// a fixed-dimension vector derived from rune values, and can be told to fail.
type mockEmbedder struct {
	dim    int
	fail   bool
	called int
}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	return m.EmbedWithPrefix(context.Background(), text, "")
}

func (m *mockEmbedder) EmbedWithPrefix(_ context.Context, text, _ string) ([]float64, error) {
	m.called++
	if m.fail {
		return nil, fmt.Errorf("mock embedder: forced failure")
	}
	vec := make([]float64, m.dim)
	for i, c := range text {
		if i >= m.dim {
			break
		}
		vec[i] = float64(c%97)/97.0 + 0.01
	}
	return vec, nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, 0, len(texts))
	for _, t := range texts {
		v, err := m.Embed(context.Background(), t)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (m *mockEmbedder) HealthCheck(context.Context) error { return nil }
func (m *mockEmbedder) GetModel() string                  { return "mock" }
func (m *mockEmbedder) GetTimeout() time.Duration         { return time.Second }

func TestVectorProvider_HashQueryVector_Deterministic(t *testing.T) {
	store := newMemVectorStore()
	p, err := NewVectorProvider(store, Config{Name: "vec", Collection: "docs", VectorDimension: 8})
	if err != nil {
		t.Fatal(err)
	}

	intent := knowledge.Intent{Goal: "semantic search example"}
	v1, err := p.generateQueryVector(context.Background(), intent)
	if err != nil {
		t.Fatalf("generateQueryVector: %v", err)
	}
	v2, err := p.generateQueryVector(context.Background(), intent)
	if err != nil {
		t.Fatalf("generateQueryVector: %v", err)
	}

	if len(v1) != 8 {
		t.Fatalf("dim = %d, want 8", len(v1))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("deterministic hash should be identical across calls, got diff at %d", i)
		}
	}

	// Vector must be unit length (normalized for cosine similarity).
	var sum float64
	for _, v := range v1 {
		sum += v * v
	}
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("hash vector not unit length: sum(sq)=%f", sum)
	}
}

func TestVectorProvider_GenerateQueryVector_UsesEmbedder(t *testing.T) {
	store := newMemVectorStore()
	em := &mockEmbedder{dim: 8}
	p, err := NewVectorProvider(store, Config{
		Name: "vec", Collection: "docs", VectorDimension: 8, Embedder: em,
	})
	if err != nil {
		t.Fatal(err)
	}

	intent := knowledge.Intent{Goal: "semantic query"}
	vec, err := p.generateQueryVector(context.Background(), intent)
	if err != nil {
		t.Fatalf("generateQueryVector: %v", err)
	}
	if em.called == 0 {
		t.Fatal("expected embedder to be called when configured")
	}
	if len(vec) != 8 {
		t.Fatalf("dim = %d, want 8", len(vec))
	}
	// Embedder output must be normalized to unit length.
	var sum float64
	for _, v := range vec {
		sum += v * v
	}
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("embedder vector not unit length: sum(sq)=%f", sum)
	}
}

func TestVectorProvider_GenerateQueryVector_EmbedderError(t *testing.T) {
	store := newMemVectorStore()
	em := &mockEmbedder{dim: 8, fail: true}
	p, err := NewVectorProvider(store, Config{
		Name: "vec", Collection: "docs", VectorDimension: 8, Embedder: em,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.generateQueryVector(context.Background(), knowledge.Intent{Goal: "x"})
	if err == nil {
		t.Fatal("expected error when embedder fails (no silent hash fallback)")
	}
}

func TestVectorProvider_GenerateQueryVector_EmbedderEmptyVector(t *testing.T) {
	store := newMemVectorStore()
	em := &emptyVectorEmbedder{}
	p, err := NewVectorProvider(store, Config{
		Name: "vec", Collection: "docs", VectorDimension: 8, Embedder: em,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.generateQueryVector(context.Background(), knowledge.Intent{Goal: "x"})
	if err == nil {
		t.Fatal("expected error when embedder returns empty vector")
	}
}

// emptyVectorEmbedder returns an empty vector to exercise the empty-result path.
type emptyVectorEmbedder struct{}

func (e *emptyVectorEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return nil, nil
}
func (e *emptyVectorEmbedder) EmbedWithPrefix(_ context.Context, _, _ string) ([]float64, error) {
	return nil, nil
}
func (e *emptyVectorEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float64, error) {
	return nil, nil
}
func (e *emptyVectorEmbedder) HealthCheck(context.Context) error { return nil }
func (e *emptyVectorEmbedder) GetModel() string                  { return "empty" }
func (e *emptyVectorEmbedder) GetTimeout() time.Duration         { return time.Second }
