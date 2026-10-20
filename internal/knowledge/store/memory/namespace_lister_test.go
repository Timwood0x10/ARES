package memorystore

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// interfaceOnlyStore satisfies knowledge.KnowledgeStore by delegation but
// deliberately does NOT expose the optional NamespaceLister capability:
// embedding the INTERFACE (not the concrete store) is what hides it, so the
// type stays a valid KnowledgeStore that simply cannot enumerate.
type interfaceOnlyStore struct {
	knowledge.KnowledgeStore
}

// TestNamespaceIsolation_ListNamespaces verifies the NamespaceLister capability:
// the store reports the namespaces it holds — deduplicated and sorted, so a
// sweep over them is reproducible — and the package-level helper resolves the
// same capability without requiring it.
func TestNamespaceIsolation_ListNamespaces(t *testing.T) {
	ctx := context.Background()

	// An empty store holds no namespaces, and that is not an error.
	empty := New()
	got, err := empty.ListNamespaces(ctx)
	if err != nil {
		t.Fatalf("ListNamespaces on empty store: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no namespaces, got %v", got)
	}

	s := New()
	objs := []*knowledge.KnowledgeObject{
		{ID: "a:1", Namespace: "tenant-b", Type: knowledge.ObjectDecision},
		{ID: "a:2", Namespace: "tenant-a", Type: knowledge.ObjectDecision},
		{ID: "a:3", Namespace: "tenant-b", Type: knowledge.ObjectDecision},
	}
	if err := s.Save(ctx, objs...); err != nil {
		t.Fatalf("Save: %v", err)
	}

	want := []string{"tenant-a", "tenant-b"}
	got, err = s.ListNamespaces(ctx)
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ListNamespaces = %v, want %v", got, want)
	}

	viaHelper, err := knowledge.ListNamespaces(ctx, s)
	if err != nil {
		t.Fatalf("knowledge.ListNamespaces: %v", err)
	}
	if len(viaHelper) != len(want) {
		t.Fatalf("helper returned %v, want %v", viaHelper, want)
	}

	// A store without the optional capability must report it as UNSUPPORTED.
	// Returning an empty list instead would read to a sweep as "nothing to
	// visit" — the failure mode this sentinel exists to prevent.
	var bare knowledge.KnowledgeStore = interfaceOnlyStore{KnowledgeStore: s}
	if _, err := knowledge.ListNamespaces(ctx, bare); !errors.Is(err, knowledge.ErrNamespaceListingNotSupported) {
		t.Fatalf("expected ErrNamespaceListingNotSupported, got %v", err)
	}
}
