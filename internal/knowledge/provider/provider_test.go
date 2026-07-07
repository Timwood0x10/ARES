package provider

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// testProvider is a simple GraphProvider implementation for testing.
type testProvider struct {
	name        string
	intentMatch float64
	objects     []*knowledge.KnowledgeObject
}

func (p *testProvider) Name() string { return p.name }

func (p *testProvider) IntentMatch(_ knowledge.Intent) float64 { return p.intentMatch }

func (p *testProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	objCh := make(chan *knowledge.KnowledgeObject, len(p.objects))
	errCh := make(chan error, 1)
	go func() {
		defer close(objCh)
		defer close(errCh)
		for _, obj := range p.objects {
			objCh <- obj
		}
	}()
	return objCh, errCh
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewProviderRegistry()
	p := &testProvider{name: "test-pg", intentMatch: 0.8}

	err := r.Register(p)
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	got := r.Get("test-pg")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name() != "test-pg" {
		t.Errorf("expected 'test-pg', got '%s'", got.Name())
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	r := NewProviderRegistry()
	p1 := &testProvider{name: "dup", intentMatch: 0.5}
	p2 := &testProvider{name: "dup", intentMatch: 0.9}

	_ = r.Register(p1)
	err := r.Register(p2)
	if err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestRegistryRegisterNil(t *testing.T) {
	r := NewProviderRegistry()
	err := r.Register(nil)
	if err == nil {
		t.Error("expected error for nil provider")
	}
}

func TestRegistryUnregister(t *testing.T) {
	r := NewProviderRegistry()
	_ = r.Register(&testProvider{name: "p1"})

	err := r.Unregister("p1")
	if err != nil {
		t.Fatalf("Unregister error: %v", err)
	}

	got := r.Get("p1")
	if got != nil {
		t.Error("expected nil after unregister")
	}
}

func TestRegistryUnregisterNotFound(t *testing.T) {
	r := NewProviderRegistry()
	err := r.Unregister("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewProviderRegistry()
	_ = r.Register(&testProvider{name: "b"})
	_ = r.Register(&testProvider{name: "a"})
	_ = r.Register(&testProvider{name: "c"})

	names := r.List()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	// Should be sorted.
	if names[0] != "a" || names[1] != "b" || names[2] != "c" {
		t.Errorf("expected sorted [a b c], got %v", names)
	}
}

func TestRegistrySelectByIntent(t *testing.T) {
	r := NewProviderRegistry()
	_ = r.Register(&testProvider{name: "high-match", intentMatch: 0.9})
	_ = r.Register(&testProvider{name: "medium-match", intentMatch: 0.5})
	_ = r.Register(&testProvider{name: "low-match", intentMatch: 0.05})

	selected := r.Select(knowledge.Intent{Goal: "test"}, 0.1)
	if len(selected) != 2 {
		t.Fatalf("expected 2 providers above threshold, got %d", len(selected))
	}
	// Should be sorted by score descending.
	if selected[0].Name() != "high-match" {
		t.Errorf("expected 'high-match' first, got '%s'", selected[0].Name())
	}
	if selected[1].Name() != "medium-match" {
		t.Errorf("expected 'medium-match' second, got '%s'", selected[1].Name())
	}
}

func TestRegistrySelectEmpty(t *testing.T) {
	r := NewProviderRegistry()
	selected := r.Select(knowledge.Intent{Goal: "test"}, 0.5)
	if len(selected) != 0 {
		t.Errorf("expected empty selection, got %d", len(selected))
	}
}

func TestProviderStream(t *testing.T) {
	p := &testProvider{
		name:        "stream-test",
		intentMatch: 1.0,
		objects: []*knowledge.KnowledgeObject{
			{ID: "obj1", Summary: "first"},
			{ID: "obj2", Summary: "second"},
			{ID: "obj3", Summary: "third"},
		},
	}

	objCh, errCh := p.Stream(context.Background(), knowledge.Intent{})

	var count int
	for range objCh {
		count++
	}

	// Check for errors.
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	default:
	}

	if count != 3 {
		t.Errorf("expected 3 objects, got %d", count)
	}
}

func TestProviderStreamContextCancel(t *testing.T) {
	p := &testProvider{
		name:        "cancel-test",
		intentMatch: 1.0,
		objects: []*knowledge.KnowledgeObject{
			{ID: "obj1"}, {ID: "obj2"}, {ID: "obj3"},
			{ID: "obj4"}, {ID: "obj5"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	objCh, _ := p.Stream(ctx, knowledge.Intent{})

	// Read one object then cancel.
	_, ok := <-objCh
	if !ok {
		t.Fatal("expected at least one object")
	}
	cancel()

	// Remaining objects should not be delivered (or may be, depending on timing).
	// Just verify no panic.
	for range objCh {
		// Drain without counting.
	}
}

func TestColumnMapping(t *testing.T) {
	m := ColumnMapping{
		IDColumn:      "order_id",
		SummaryColumn: "title",
		ContentColumn: "body",
		TimeColumn:    "created_at",
	}

	if m.IDColumn != "order_id" {
		t.Errorf("unexpected ID column: %s", m.IDColumn)
	}
	if m.ContentColumn != "body" {
		t.Errorf("unexpected content column: %s", m.ContentColumn)
	}
}

func TestProviderConfig(t *testing.T) {
	cfg := ProviderConfig{
		Name:       "orders-db",
		Namespace:  "ecommerce",
		IntentTags: []string{"order", "customer"},
	}

	if cfg.Name != "orders-db" {
		t.Errorf("unexpected name: %s", cfg.Name)
	}
	if len(cfg.IntentTags) != 2 {
		t.Errorf("expected 2 intent tags, got %d", len(cfg.IntentTags))
	}
}
