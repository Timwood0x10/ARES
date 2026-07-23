// Package compiler tests for AKGBuilder.
package compiler

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// fakeKnowledgeStore is a minimal KnowledgeStore mock recording Save calls.
type fakeKnowledgeStore struct {
	saved []*knowledge.KnowledgeObject
	err   error
}

func (f *fakeKnowledgeStore) Save(_ context.Context, objs ...*knowledge.KnowledgeObject) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, objs...)
	return nil
}

func (f *fakeKnowledgeStore) Get(context.Context, string) (*knowledge.KnowledgeObject, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) Query(context.Context, knowledge.Query) ([]*knowledge.KnowledgeObject, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) Delete(context.Context, string) error { return nil }

func (f *fakeKnowledgeStore) Search(context.Context, string, string, int) ([]*knowledge.KnowledgeObject, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) SaveRepresentation(context.Context, *knowledge.Representation) error {
	return nil
}

func (f *fakeKnowledgeStore) GetRepresentation(context.Context, string, string) (*knowledge.Representation, error) {
	return nil, nil
}

// ── AKGBuilder tests ────────────────────────────────────────────────────

func TestAKGBuilderBuildsObjectsAndRelations(t *testing.T) {
	store := &fakeKnowledgeStore{}
	builder := NewAKGBuilder(store)

	sub := &SubGraph{
		Nodes: []*Node{
			{ID: "e1", Type: NodeEntity, Confidence: 0.9, Version: 1, Source: "msg1",
				Attributes: map[string]any{"name": "ARES"}},
			{ID: "e2", Type: NodeEntity, Confidence: 0.8, Source: "msg1",
				Attributes: map[string]any{"name": "RuntimePatch"}},
			{ID: "f1", Type: NodeFact, Confidence: 0.7, Source: "msg2",
				Attributes: map[string]any{
					"subject": "ARES", "predicate": "uses", "object": "RuntimePatch",
				}},
		},
		Edges: []Edge{
			{ID: "ed1", Type: EdgeMentions, Source: "e1", Target: "f1", Weight: 0.5},
			{ID: "ed2", Type: EdgeDependsOn, Source: "e2", Target: "e1", Weight: 0.9},
		},
	}

	result, err := builder.Build(context.Background(), sub, "ns-1")
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if result == nil {
		t.Fatal("Build returned nil result")
	}
	if len(result.Objects) != 3 {
		t.Errorf("expected 3 objects, got %d", len(result.Objects))
	}
	if len(result.Relations) != 2 {
		t.Errorf("expected 2 relations, got %d", len(result.Relations))
	}
	if result.Saved != 3 {
		t.Errorf("expected 3 saved, got %d", result.Saved)
	}
	if len(store.saved) != 3 {
		t.Errorf("expected 3 objects passed to store, got %d", len(store.saved))
	}

	// Every object carries the namespace and a node-type tag.
	for _, obj := range result.Objects {
		if obj.Namespace != "ns-1" {
			t.Errorf("object %q namespace = %q, want ns-1", obj.ID, obj.Namespace)
		}
		if len(obj.Tags) != 1 {
			t.Errorf("object %q expected 1 tag, got %d", obj.ID, len(obj.Tags))
		}
		nt, _ := obj.Metadata["node_type"].(string)
		if nt == "" {
			t.Errorf("object %q missing node_type metadata", obj.ID)
		}
	}

	// The fact object should carry the assembled triple as its summary.
	var fact *knowledge.KnowledgeObject
	for _, o := range result.Objects {
		if o.ID == "f1" {
			fact = o
		}
	}
	if fact == nil {
		t.Fatal("fact object f1 not found")
	}
	if fact.Summary != "ARES uses RuntimePatch" {
		t.Errorf("fact summary = %q, want %q", fact.Summary, "ARES uses RuntimePatch")
	}

	// depends_on edge maps to the built-in relation name.
	var depRel *knowledge.Relation
	for i := range result.Relations {
		if result.Relations[i].Name == knowledge.RelDependsOn {
			depRel = &result.Relations[i]
		}
	}
	if depRel == nil {
		t.Error("expected a depends_on relation")
	} else if depRel.Score != 0.9 {
		t.Errorf("depends_on relation score = %v, want 0.9", depRel.Score)
	}
}

func TestAKGBuilderNilSub(t *testing.T) {
	builder := NewAKGBuilder(nil)
	result, err := builder.Build(context.Background(), nil, "ns")
	if err != nil {
		t.Fatalf("Build with nil sub failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Objects) != 0 {
		t.Errorf("expected 0 objects, got %d", len(result.Objects))
	}
	if len(result.Relations) != 0 {
		t.Errorf("expected 0 relations, got %d", len(result.Relations))
	}
	if result.Saved != 0 {
		t.Errorf("expected 0 saved, got %d", result.Saved)
	}
}

func TestAKGBuilderBuildOnlyNoStore(t *testing.T) {
	// A nil store means build-only: objects are produced but nothing is saved.
	builder := NewAKGBuilder(nil)
	sub := &SubGraph{
		Nodes: []*Node{{ID: "e1", Type: NodeEntity, Attributes: map[string]any{"name": "ARES"}}},
	}
	result, err := builder.Build(context.Background(), sub, "ns")
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if len(result.Objects) != 1 {
		t.Errorf("expected 1 object, got %d", len(result.Objects))
	}
	if result.Saved != 0 {
		t.Errorf("expected 0 saved without store, got %d", result.Saved)
	}
}

func TestAKGBuilderCancelledContext(t *testing.T) {
	builder := NewAKGBuilder(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := builder.Build(ctx,
		&SubGraph{Nodes: []*Node{{ID: "n1", Type: NodeEntity, Attributes: map[string]any{"name": "x"}}}},
		"ns")
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestAKGBuilderSaveError(t *testing.T) {
	store := &fakeKnowledgeStore{err: fmt.Errorf("disk full")}
	builder := NewAKGBuilder(store)
	sub := &SubGraph{
		Nodes: []*Node{{ID: "e1", Type: NodeEntity, Attributes: map[string]any{"name": "ARES"}}},
	}
	_, err := builder.Build(context.Background(), sub, "ns")
	if err == nil {
		t.Error("expected error from store Save failure")
	}
}
