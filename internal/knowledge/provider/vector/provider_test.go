package vector

import (
	"context"
	"sync"
	"testing"

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
