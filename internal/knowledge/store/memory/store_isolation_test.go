package memorystore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestStoreSaveInputIsolation locks REVIEW 2.5#27: Save must store a PRIVATE
// copy of the caller's object — mutating the object after Save (or the
// slices/maps inside it) must not be observable through the store. The old
// implementation stored the caller's pointer, so post-Save mutations raced
// with concurrent readers.
func TestStoreSaveInputIsolation(t *testing.T) {
	s := New()
	obj := &knowledge.KnowledgeObject{
		Namespace:  "default",
		ID:         "obj-1",
		Summary:    "original",
		Raw:        []byte("raw-original"),
		Metadata:   map[string]any{"k": "v"},
		Tags:       []string{"t1"},
		Confidence: 0.9,
	}
	if err := s.Save(context.Background(), obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Mutate every alias-bearing field after Save.
	obj.Summary = "mutated"
	obj.Raw[0] = 'X'
	obj.Metadata["k"] = "mutated"
	obj.Tags[0] = "mutated"

	got, err := s.Get(context.Background(), "default", "obj-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Summary != "original" || string(got.Raw) != "raw-original" ||
		got.Metadata["k"] != "v" || got.Tags[0] != "t1" {
		t.Errorf("post-Save caller mutations leaked into the store: %+v", got)
	}
}

// TestStoreReturnedObjectsAreCopies verifies the other boundary: objects
// returned by Get/Query/Search/ListByStatus must be detached from store
// state, so mutating a returned object cannot corrupt the stored copy.
func TestStoreReturnedObjectsAreCopies(t *testing.T) {
	s := New()
	obj := &knowledge.KnowledgeObject{
		Namespace:  "default",
		ID:         "obj-1",
		Type:       knowledge.ObjectMemory,
		Summary:    "searchable summary",
		Raw:        []byte("raw"),
		Metadata:   map[string]any{"k": "v"},
		Confidence: 0.9,
		Status:     knowledge.StatusActive,
	}
	if err := s.Save(context.Background(), obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Mutate through every read path.
	g, _ := s.Get(context.Background(), "default", "obj-1")
	g.Summary = "via-get"
	g.Metadata["k"] = "via-get"
	g.Raw[0] = 'X'

	q, _ := s.Query(context.Background(), knowledge.Query{})
	if len(q) != 1 {
		t.Fatalf("Query: expected 1 result, got %d", len(q))
	}
	q[0].Summary = "via-query"

	sr, _ := s.Search(context.Background(), "default", "searchable", "", 10)
	if len(sr) != 1 {
		t.Fatalf("Search: expected 1 result, got %d", len(sr))
	}
	sr[0].Summary = "via-search"

	lb, _ := s.ListByStatus(context.Background(), "", knowledge.StatusActive, 10)
	if len(lb) != 1 {
		t.Fatalf("ListByStatus: expected 1 result, got %d", len(lb))
	}
	lb[0].Summary = "via-list"

	final, _ := s.Get(context.Background(), "default", "obj-1")
	if final.Summary != "searchable summary" || final.Metadata["k"] != "v" || final.Raw[0] != 'r' {
		t.Errorf("mutations through returned objects corrupted the stored copy: %+v", final)
	}
	// Each read path also returned distinct objects.
	if g == final || q[0] == final {
		t.Error("read paths returned the stored pointer, not copies")
	}
}

// TestStoreHybridSearchResultsAreCopies verifies the HybridSearch boundary:
// the returned ScoredObjects embed store-owned pointers that must be cloned
// before escaping the RLock.
func TestStoreHybridSearchResultsAreCopies(t *testing.T) {
	s := New()
	obj := &knowledge.KnowledgeObject{Namespace: "default",
		ID:         "obj-1",
		Type:       knowledge.ObjectMemory,
		Summary:    "alpha beta",
		Normalized: "alpha beta",
		Status:     knowledge.StatusActive,
	}
	if err := s.Save(context.Background(), obj); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SaveRepresentation(context.Background(), &knowledge.Representation{
		ID: "rep-1", ObjectID: "obj-1", Model: "m", Vector: []float32{1, 0},
	}); err != nil {
		t.Fatalf("SaveRepresentation: %v", err)
	}

	res, err := s.HybridSearch(context.Background(), knowledge.HybridSearchRequest{
		Query: "alpha", Model: "m", QueryVector: []float32{1, 0},
	})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	res[0].Object.Summary = "mutated"
	res[0].Object.Raw = []byte("junk")

	final, _ := s.Get(context.Background(), "default", "obj-1")
	if final.Summary != "alpha beta" {
		t.Errorf("HybridSearch result mutation corrupted the stored object: %q", final.Summary)
	}
}

// TestStoreConcurrentSaveGetUnderRace hammers Save/Get/Query concurrently —
// meaningful under -race: with the old shared-pointer storage this is a
// guaranteed data race.
func TestStoreConcurrentSaveGetUnderRace(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				obj := &knowledge.KnowledgeObject{
					Namespace: "default",
					ID:        "obj-shared",
					Summary:   "summary",
					Raw:       []byte("payload"),
					Metadata:  map[string]any{"w": w, "i": i},
				}
				_ = s.Save(context.Background(), obj)
				obj.Metadata["mutated"] = true // post-Save mutation
				if got, err := s.Get(context.Background(), "default", "obj-shared"); err == nil {
					_ = got.Summary
				}
				_, _ = s.Query(context.Background(), knowledge.Query{})
			}
		}(w)
	}
	wg.Wait()
}

// TestCrossTenantAccessRefused locks the tenant predicate added to Get/Delete/
// Search. Before the fix these took no tenant at all — Get(ctx, id) and
// Delete(ctx, id) matched on the primary key alone, so any caller holding an
// object ID could read or erase another tenant's knowledge. Search ignored its
// tenant argument entirely in the postgres and sqlite backends (it was named
// `_ string`).
//
// A foreign-namespace row must be indistinguishable from a missing one, so the
// error is ErrObjectNotFound in both cases: distinguishing them would let a
// caller probe another tenant for valid object IDs.
func TestCrossTenantAccessRefused(t *testing.T) {
	s := New()
	ctx := context.Background()
	obj := &knowledge.KnowledgeObject{ID: "secret", Namespace: "tenant-a", Summary: "belongs to a"}
	if err := s.Save(ctx, obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := s.Get(ctx, "tenant-b", "secret"); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("cross-tenant Get = %v, want ErrObjectNotFound", err)
	}
	if err := s.Delete(ctx, "tenant-b", "secret"); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("cross-tenant Delete = %v, want ErrObjectNotFound", err)
	}
	if got, err := s.Search(ctx, "tenant-b", "belongs", "", 10); err != nil || len(got) != 0 {
		t.Errorf("cross-tenant Search = %d results (err %v), want none", len(got), err)
	}

	// The owning tenant still has full access.
	if _, err := s.Get(ctx, "tenant-a", "secret"); err != nil {
		t.Errorf("owning-tenant Get: %v", err)
	}
	// And the row survived the refused delete.
	if _, err := s.Get(ctx, "tenant-a", "secret"); err != nil {
		t.Errorf("row must survive a refused cross-tenant Delete: %v", err)
	}
}

// TestSaveRefusesCrossNamespaceOverwrite locks the Save ownership guard.
// Objects are keyed by ID alone, and a tenant-scoped Get reports a foreign row
// as absent — so without this guard an upsert-on-miss caller (knowledge_update
// via StoreAdapter) would overwrite another tenant's row and re-stamp it with
// the caller's namespace, silently migrating the victim's knowledge. The
// refused Save must answer ErrObjectNotFound (same as a foreign Get, so the
// caller cannot probe which IDs exist elsewhere) and leave the row untouched.
func TestSaveRefusesCrossNamespaceOverwrite(t *testing.T) {
	s := New()
	ctx := context.Background()
	victim := &knowledge.KnowledgeObject{ID: "obj-1", Namespace: "tenant-a", Normalized: "secret"}
	if err := s.Save(ctx, victim); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	attacker := &knowledge.KnowledgeObject{ID: "obj-1", Namespace: "tenant-b", Normalized: "attacker content"}
	if err := s.Save(ctx, attacker); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("cross-namespace Save = %v, want ErrObjectNotFound", err)
	}

	got, err := s.Get(ctx, "tenant-a", "obj-1")
	if err != nil {
		t.Fatalf("owning-tenant Get after refused Save: %v", err)
	}
	if got.Namespace != "tenant-a" || got.Normalized != "secret" {
		t.Fatalf("victim row mutated by refused Save: ns=%q normalized=%q", got.Namespace, got.Normalized)
	}
	// The caller's own namespace gained nothing either: the Save failed whole.
	if _, err := s.Get(ctx, "tenant-b", "obj-1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("refused Save must not create the row in tenant-b, got %v", err)
	}
}

// TestCrossTenantLifecycleWriteRefused locks the tenant predicate on
// UpdateStatus and Promote. Before the fix both took only an id and matched on
// it alone, so any caller holding an object ID could flip another tenant's
// lifecycle status or promote it to active — a cross-tenant WRITE, which is
// strictly worse than the read/delete exposure closed earlier.
//
// The row must be left untouched, and the error must be ErrObjectNotFound so
// the caller cannot tell "missing" from "foreign".
func TestCrossTenantLifecycleWriteRefused(t *testing.T) {
	s := New()
	ctx := context.Background()
	obj := &knowledge.KnowledgeObject{
		ID: "cand", Namespace: "tenant-a", Summary: "candidate",
		Status: knowledge.StatusCandidate,
	}
	if err := s.Save(ctx, obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.UpdateStatus(ctx, "tenant-b", "cand", knowledge.StatusActive); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("cross-tenant UpdateStatus = %v, want ErrObjectNotFound", err)
	}
	if err := s.Promote(ctx, "tenant-b", "cand", &knowledge.Quality{ExtractionScore: 0.9}); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("cross-tenant Promote = %v, want ErrObjectNotFound", err)
	}

	got, err := s.Get(ctx, "tenant-a", "cand")
	if err != nil {
		t.Fatalf("owning-tenant Get after refused writes: %v", err)
	}
	if got.Status != knowledge.StatusCandidate {
		t.Errorf("Status = %q, want %q (refused writes must not mutate)", got.Status, knowledge.StatusCandidate)
	}
	if got.Quality != nil {
		t.Errorf("Quality = %+v, want nil (refused Promote must not record)", got.Quality)
	}

	// The owning tenant can still transition.
	if err := s.UpdateStatus(ctx, "tenant-a", "cand", knowledge.StatusActive); err != nil {
		t.Errorf("owning-tenant UpdateStatus: %v", err)
	}
}

// TestSaveRejectsEmptyNamespace pins that Save refuses a row with no
// namespace. Every read path (Get/Delete/Search/UpdateStatus/Promote) filters
// on namespace, and StoreProvider.namespaceFor falls back through
// Scope.Namespaces -> tenantctx -> provider default and never yields empty —
// so a row saved without one is silently unreachable rather than merely
// unindexed. This is what distill_memory did before it stamped a namespace:
// the write succeeded and every later read missed it.
//
// An empty ID is still reported as the ID error, so the checks keep that
// order.
func TestSaveRejectsEmptyNamespace(t *testing.T) {
	s := New()
	err := s.Save(context.Background(), &knowledge.KnowledgeObject{ID: "x", Summary: "no namespace"})
	if err == nil || !strings.Contains(err.Error(), "namespace cannot be empty") {
		t.Fatalf("Save with empty namespace = %v, want a namespace error", err)
	}
	if len(s.objects) != 0 {
		t.Fatalf("nothing must be stored, got %d objects", len(s.objects))
	}

	// ID validation still wins when both are empty.
	err = s.Save(context.Background(), &knowledge.KnowledgeObject{})
	if err == nil || !strings.Contains(err.Error(), "ID cannot be empty") {
		t.Fatalf("Save with empty ID = %v, want the ID error", err)
	}
}

// TestGetRepresentationCrossTenantRefused locks F-05: a representation is
// reachable only through its owning object's tenant. A foreign tenant's
// vector read must be indistinguishable from a missing one
// (ErrObjectNotFound), matching the Get/Delete anti-probing contract.
func TestGetRepresentationCrossTenantRefused(t *testing.T) {
	s := New()
	ctx := context.Background()
	if err := s.Save(ctx, &knowledge.KnowledgeObject{
		Namespace: "tenant-a", ID: "vec-obj", Summary: "vectors", Confidence: 0.9,
	}); err != nil {
		t.Fatalf("Save owner: %v", err)
	}
	if err := s.SaveRepresentation(ctx, &knowledge.Representation{
		ID: "rep-1", ObjectID: "vec-obj", Model: "m", Dimension: 2, Vector: []float32{0.1, 0.2},
	}); err != nil {
		t.Fatalf("SaveRepresentation: %v", err)
	}

	// Owning tenant reads it.
	got, err := s.GetRepresentation(ctx, "tenant-a", "vec-obj", "m")
	if err != nil || got == nil {
		t.Fatalf("owning tenant must read its representation, got %v / %v", got, err)
	}
	// Foreign tenant gets not-found (not an error disclosing existence).
	if _, err := s.GetRepresentation(ctx, "tenant-b", "vec-obj", "m"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("cross-tenant GetRepresentation = %v, want ErrObjectNotFound", err)
	}
}
