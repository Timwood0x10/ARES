package memorystore

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestSaveAndGet(t *testing.T) {
	s := New()
	obj := &knowledge.KnowledgeObject{ID: "o1", Summary: "test", Confidence: 0.9}

	err := s.Save(context.Background(), obj)
	if err != nil {
		t.Fatalf("Save error: %v", err)
	}

	got, err := s.Get(context.Background(), "o1")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.Summary != "test" {
		t.Errorf("expected 'test', got '%s'", got.Summary)
	}
}

func TestSaveEmptyID(t *testing.T) {
	s := New()
	err := s.Save(context.Background(), &knowledge.KnowledgeObject{ID: ""})
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestGetNotFound(t *testing.T) {
	s := New()
	obj, err := s.Get(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if obj != nil {
		t.Error("expected nil for not found")
	}
}

func TestQueryByType(t *testing.T) {
	s := New()
	_ = s.Save(context.Background(),
		&knowledge.KnowledgeObject{ID: "d1", Type: knowledge.ObjectDecision, Confidence: 0.9},
		&knowledge.KnowledgeObject{ID: "m1", Type: knowledge.ObjectMemory, Confidence: 0.8},
		&knowledge.KnowledgeObject{ID: "d2", Type: knowledge.ObjectDecision, Confidence: 0.7},
	)

	results, err := s.Query(context.Background(), knowledge.Query{Types: []knowledge.ObjectType{knowledge.ObjectDecision}})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestQueryByTags(t *testing.T) {
	s := New()
	_ = s.Save(context.Background(),
		&knowledge.KnowledgeObject{ID: "a", Tags: []string{"redis", "cache"}},
		&knowledge.KnowledgeObject{ID: "b", Tags: []string{"postgres"}},
	)

	results, err := s.Query(context.Background(), knowledge.Query{Tags: []string{"redis"}})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestQueryWithLimit(t *testing.T) {
	s := New()
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("o%d", i)
		_ = s.Save(context.Background(), &knowledge.KnowledgeObject{ID: id, Confidence: float64(i) / 10})
	}

	results, err := s.Query(context.Background(), knowledge.Query{Limit: 3})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	// Should be sorted by confidence descending.
	if results[0].Confidence < results[1].Confidence {
		t.Error("expected descending confidence order")
	}
}

func TestDelete(t *testing.T) {
	s := New()
	_ = s.Save(context.Background(), &knowledge.KnowledgeObject{ID: "o1"})

	err := s.Delete(context.Background(), "o1")
	if err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	got, _ := s.Get(context.Background(), "o1")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestSearch(t *testing.T) {
	s := New()
	_ = s.Save(context.Background(),
		&knowledge.KnowledgeObject{ID: "o1", Summary: "Redis cache layer", Confidence: 0.9},
		&knowledge.KnowledgeObject{ID: "o2", Summary: "PostgreSQL storage", Confidence: 0.8},
	)

	results, err := s.Search(context.Background(), "redis", "", 10)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestRepresentation(t *testing.T) {
	s := New()
	rep := &knowledge.Representation{
		ID: "rep1", ObjectID: "o1", Model: "openai", Dimension: 1536,
	}

	err := s.SaveRepresentation(context.Background(), rep)
	if err != nil {
		t.Fatalf("SaveRepresentation error: %v", err)
	}

	got, err := s.GetRepresentation(context.Background(), "o1", "openai")
	if err != nil {
		t.Fatalf("GetRepresentation error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.Dimension != 1536 {
		t.Errorf("expected 1536, got %d", got.Dimension)
	}
}

func TestCount(t *testing.T) {
	s := New()
	_ = s.Save(context.Background(),
		&knowledge.KnowledgeObject{ID: "a"},
		&knowledge.KnowledgeObject{ID: "b"},
	)
	if s.Count() != 2 {
		t.Errorf("expected 2, got %d", s.Count())
	}
}
