package pipeline

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestDefaultNormalizer_Name(t *testing.T) {
	n := &DefaultNormalizer{}
	if n.Name() != "default-normalizer" {
		t.Errorf("unexpected name: %s", n.Name())
	}
}

func TestDefaultNormalizer_FromRaw(t *testing.T) {
	n := &DefaultNormalizer{}
	obj := &knowledge.KnowledgeObject{
		ID:  "obj1",
		Raw: []byte("  Hello   World!  \n\nThis\tis\traw.\x00"),
	}

	result, err := n.Normalize(context.Background(), obj)
	if err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	expected := "Hello World! This is raw."
	if result.Normalized != expected {
		t.Errorf("expected %q, got %q", expected, result.Normalized)
	}
}

func TestDefaultNormalizer_AlreadyNormalized(t *testing.T) {
	n := &DefaultNormalizer{}
	obj := &knowledge.KnowledgeObject{
		ID:         "obj2",
		Normalized: "already clean text",
	}

	result, err := n.Normalize(context.Background(), obj)
	if err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
	if result.Normalized != "already clean text" {
		t.Errorf("expected unchanged, got %q", result.Normalized)
	}
}

func TestDefaultNormalizer_FallbackToSummary(t *testing.T) {
	n := &DefaultNormalizer{}
	obj := &knowledge.KnowledgeObject{
		ID:      "obj3",
		Summary: "fallback summary",
	}

	result, err := n.Normalize(context.Background(), obj)
	if err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
	if result.Normalized != "fallback summary" {
		t.Errorf("expected fallback summary, got %q", result.Normalized)
	}
}

func TestDefaultNormalizer_Empty(t *testing.T) {
	n := &DefaultNormalizer{}
	result, err := n.Normalize(context.Background(), nil)
	if err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil object")
	}
}

func TestDefaultNormalizer_MaxBytes(t *testing.T) {
	n := &DefaultNormalizer{MaxRawBytes: 5}
	obj := &knowledge.KnowledgeObject{
		ID:  "obj4",
		Raw: []byte("Hello World!"),
	}

	result, err := n.Normalize(context.Background(), obj)
	if err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
	if len(result.Normalized) > 10 {
		t.Errorf("expected truncated normalized text, got %q (len=%d)", result.Normalized, len(result.Normalized))
	}
}

func TestDefaultSummarizer_Name(t *testing.T) {
	s := &DefaultSummarizer{}
	if s.Name() != "default-summarizer" {
		t.Errorf("unexpected name: %s", s.Name())
	}
}

func TestDefaultSummarizer_FromNormalized(t *testing.T) {
	s := &DefaultSummarizer{MaxSummaryLen: 20}
	obj := &knowledge.KnowledgeObject{
		ID:         "s1",
		Normalized: "This is a long text that should be summarized to just the first few words",
	}

	result, err := s.Summarize(context.Background(), obj)
	if err != nil {
		t.Fatalf("Summarize error: %v", err)
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(result.Summary) > 25 {
		t.Errorf("expected brief summary, got %q (len=%d)", result.Summary, len(result.Summary))
	}
}

func TestDefaultSummarizer_ShortText(t *testing.T) {
	s := &DefaultSummarizer{MaxSummaryLen: 200}
	obj := &knowledge.KnowledgeObject{
		ID:         "s2",
		Normalized: "Short text",
	}

	result, err := s.Summarize(context.Background(), obj)
	if err != nil {
		t.Fatalf("Summarize error: %v", err)
	}
	if result.Summary != "Short text" {
		t.Errorf("expected 'Short text', got %q", result.Summary)
	}
}

func TestDefaultSummarizer_Empty(t *testing.T) {
	s := &DefaultSummarizer{}
	result, err := s.Summarize(context.Background(), nil)
	if err != nil {
		t.Fatalf("Summarize error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for nil object")
	}
}

func TestDefaultEntityMatcher_Match(t *testing.T) {
	m := &DefaultEntityMatcher{MatchThreshold: 0.5}
	candidates := []*knowledge.KnowledgeObject{
		{ID: "c1", Normalized: "Redis cache performance caching solution design"},
		{ID: "c2", Normalized: "PostgreSQL database schema design query"},
	}

	// Object about Redis cache should match c1.
	obj := &knowledge.KnowledgeObject{Normalized: "Redis cache for performance caching improvement"}
	result, err := m.Match(context.Background(), obj, candidates)
	if err != nil {
		t.Fatalf("Match error: %v", err)
	}
	if result.IsNew {
		t.Errorf("expected match to c1 (Redis), got new entity")
	}
	if result.MatchedObjectID != "c1" {
		t.Errorf("expected match to c1 (Redis), got %s", result.MatchedObjectID)
	}
}

func TestDefaultEntityMatcher_NoMatch(t *testing.T) {
	m := &DefaultEntityMatcher{MatchThreshold: 0.9}
	candidates := []*knowledge.KnowledgeObject{
		{ID: "c1", Normalized: "Redis caching layer"},
	}

	// Completely unrelated topic.
	obj := &knowledge.KnowledgeObject{Normalized: "Quantum physics entanglement theory"}
	result, err := m.Match(context.Background(), obj, candidates)
	if err != nil {
		t.Fatalf("Match error: %v", err)
	}
	if !result.IsNew {
		t.Errorf("expected new entity for unrelated topic")
	}
}

func TestDefaultEntityMatcher_Empty(t *testing.T) {
	m := &DefaultEntityMatcher{}
	result, err := m.Match(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Match error: %v", err)
	}
	if !result.IsNew {
		t.Error("expected new entity for nil object")
	}
}

func TestDefaultValidator_Name(t *testing.T) {
	v := &DefaultValidator{}
	if v.Name() != "default-validator" {
		t.Errorf("unexpected name: %s", v.Name())
	}
}

func TestDefaultValidator_NoConflicts(t *testing.T) {
	v := &DefaultValidator{}
	obj := &knowledge.KnowledgeObject{ID: "v1", Confidence: 0.9}
	result, err := v.Validate(context.Background(), obj, nil)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if result.Confidence != 0.9 {
		t.Errorf("expected confidence 0.9, got %f", result.Confidence)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(result.Conflicts))
	}
}

func TestDefaultValidator_EmptyID(t *testing.T) {
	v := &DefaultValidator{}
	obj := &knowledge.KnowledgeObject{Confidence: 0.8}
	result, err := v.Validate(context.Background(), obj, nil)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if result.Confidence != 0 {
		t.Errorf("expected confidence 0 for empty ID, got %f", result.Confidence)
	}
	if len(result.Conflicts) == 0 {
		t.Error("expected at least one conflict for empty ID")
	}
}

func TestDefaultValidator_TypeConflict(t *testing.T) {
	v := &DefaultValidator{}
	merged := &knowledge.KnowledgeObject{ID: "v2", Confidence: 0.9}
	sources := []*knowledge.KnowledgeObject{
		{ID: "s1", Type: knowledge.ObjectDecision, Confidence: 0.9},
		{ID: "s2", Type: knowledge.ObjectArchitecture, Confidence: 0.7},
	}

	result, err := v.Validate(context.Background(), merged, sources)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if result.Confidence >= 0.9 {
		t.Errorf("expected reduced confidence due to type conflict, got %f", result.Confidence)
	}
	if len(result.Conflicts) == 0 {
		t.Error("expected conflicts for type mismatch")
	}
}
