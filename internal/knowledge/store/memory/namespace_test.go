package memorystore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestNamespaceIsolation_Query verifies that the namespace field partitions
// tenants at the query layer: a Query scoped to one namespace must never
// return objects belonging to another namespace, even when they share tags or
// type. This is the multi-tenant isolation contract documented in the README.
func TestNamespaceIsolation_Query(t *testing.T) {
	s := New()
	ctx := context.Background()

	// Two tenants with semantically overlapping content but distinct IDs.
	tenantA := "tenant-a"
	tenantB := "tenant-b"
	now := time.Now()
	objs := []*knowledge.KnowledgeObject{
		{ID: "t1:dec", Namespace: tenantA, Type: knowledge.ObjectDecision, Summary: "Use Redis", Tags: []string{"cache"}, Confidence: 0.9, CreatedAt: now, UpdatedAt: now},
		{ID: "t1:arch", Namespace: tenantA, Type: knowledge.ObjectArchitecture, Summary: "Microservices", Tags: []string{"cache"}, Confidence: 0.8, CreatedAt: now, UpdatedAt: now},
		{ID: "t2:dec", Namespace: tenantB, Type: knowledge.ObjectDecision, Summary: "Use Redis", Tags: []string{"cache"}, Confidence: 0.9, CreatedAt: now, UpdatedAt: now},
	}
	if err := s.Save(ctx, objs...); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Tenant A queries decisions: must see only its own object, never tenant B's.
	got, err := s.Query(ctx, knowledge.Query{Namespace: tenantA, Types: []knowledge.ObjectType{knowledge.ObjectDecision}})
	if err != nil {
		t.Fatalf("Query tenantA: %v", err)
	}
	if len(got) != 1 || got[0].ID != "t1:dec" {
		t.Fatalf("expected only t1:dec in tenant-a, got %+v", got)
	}

	// Tenant A queries by the shared tag: must still be scoped to tenant A.
	got, err = s.Query(ctx, knowledge.Query{Namespace: tenantA, Tags: []string{"cache"}})
	if err != nil {
		t.Fatalf("Query tenantA tags: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tagged objects in tenant-a, got %d", len(got))
	}
	for _, o := range got {
		if o.Namespace != tenantA {
			t.Errorf("namespace leak: query for %q returned object in %q", tenantA, o.Namespace)
		}
	}

	// An unscoped query returns everything (no isolation by default).
	all, err := s.Query(ctx, knowledge.Query{})
	if err != nil {
		t.Fatalf("Query all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 objects unscoped, got %d", len(all))
	}
}

// TestNamespaceIsolation_HybridSearch verifies that HybridSearch honors the
// Namespace field: a vector query for one tenant cannot recall objects from
// another tenant, regardless of how similar their embeddings are.
func TestNamespaceIsolation_HybridSearch(t *testing.T) {
	s := New()
	ctx := context.Background()

	// Identical embeddings across tenants — the only thing separating them is
	// the namespace. Tenant A's search must not surface tenant B's object.
	vec := []float32{1.0, 0.0}
	objs := []*knowledge.KnowledgeObject{
		{ID: "t1:fact", Namespace: "tenant-a", Type: knowledge.ObjectDocument, Summary: "shared fact", Confidence: 0.9},
		{ID: "t2:fact", Namespace: "tenant-b", Type: knowledge.ObjectDocument, Summary: "shared fact", Confidence: 0.9},
	}
	if err := s.Save(ctx, objs...); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SaveRepresentation(ctx, &knowledge.Representation{ID: "r1", ObjectID: "t1:fact", Model: "e5", Vector: vec}); err != nil {
		t.Fatalf("SaveRepresentation t1: %v", err)
	}
	if err := s.SaveRepresentation(ctx, &knowledge.Representation{ID: "r2", ObjectID: "t2:fact", Model: "e5", Vector: vec}); err != nil {
		t.Fatalf("SaveRepresentation t2: %v", err)
	}

	hits, err := s.HybridSearch(ctx, knowledge.HybridSearchRequest{
		Query:       "shared fact",
		QueryVector: vec,
		Namespace:   "tenant-a",
		Model:       "e5",
		TopK:        10,
		FinalK:      10,
	})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit in tenant-a, got %d", len(hits))
	}
	if hits[0].Object.ID != "t1:fact" {
		t.Errorf("namespace leak: tenant-a search returned %q", hits[0].Object.ID)
	}
}

// TestNamespaceIsolation_ListByStatus verifies the lifecycle query is also
// namespace-scoped: promoting a candidate in one tenant must not make it
// visible to another tenant's ListByStatus(StatusActive).
func TestNamespaceIsolation_ListByStatus(t *testing.T) {
	s := New()
	ctx := context.Background()

	objs := []*knowledge.KnowledgeObject{
		{ID: "t1:cand", Namespace: "tenant-a", Type: knowledge.ObjectDecision, Status: knowledge.StatusActive, Confidence: 0.9},
		{ID: "t2:cand", Namespace: "tenant-b", Type: knowledge.ObjectDecision, Status: knowledge.StatusActive, Confidence: 0.9},
		{ID: "t1:inactive", Namespace: "tenant-a", Type: knowledge.ObjectDecision, Status: knowledge.StatusCandidate, Confidence: 0.5},
	}
	if err := s.Save(ctx, objs...); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Tenant A sees exactly one active object (its own); tenant B's active
	// object and tenant A's candidate must not appear.
	got, err := s.ListByStatus(ctx, "tenant-a", knowledge.StatusActive, 100)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(got) != 1 || got[0].ID != "t1:cand" {
		t.Fatalf("expected only t1:cand active in tenant-a, got %+v", got)
	}

	// Promote tenant A's candidate; tenant B's view is unaffected. Reuse the
	// outer err to avoid shadowing the ListByStatus declaration above.
	if err = s.Promote(ctx, "t1:inactive", &knowledge.Quality{ExtractionScore: 0.9}); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	gotB, err := s.ListByStatus(ctx, "tenant-b", knowledge.StatusActive, 100)
	if err != nil {
		t.Fatalf("ListByStatus tenant-b: %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != "t2:cand" {
		t.Fatalf("tenant-b view leaked tenant-a promotion: %+v", gotB)
	}
}

// TestNamespaceIsolation_GetIsGlobal confirms that Get is keyed by ID globally
// (not partitioned by namespace): the same ID resolves regardless of the
// namespace the caller has in mind. This documents the design — namespace is a
// query filter, not an ID partition — so tenants must use globally-unique IDs
// (e.g. "tenant:object") to avoid collisions.
func TestNamespaceIsolation_GetIsGlobal(t *testing.T) {
	s := New()
	ctx := context.Background()
	obj := &knowledge.KnowledgeObject{ID: "shared-id", Namespace: "tenant-a", Summary: "owner is tenant-a"}
	if err := s.Save(ctx, obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "shared-id")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Namespace != "tenant-a" {
		t.Errorf("expected owner tenant-a, got %q", got.Namespace)
	}
	// A missing ID is still not-found, regardless of namespace intent.
	if _, err := s.Get(ctx, "absent"); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("expected ErrObjectNotFound for absent id, got %v", err)
	}
}
