package builtin

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// Tenant-isolation regressions in StoreAdapter. The KnowledgeStore contract
// has no tenant column — Namespace carries the tenant — so isolation is
// enforced in two layers: tenant-scoped reads (Get/Delete/Search) in the
// backends, and the Save ownership guard that refuses to overwrite a row
// owned by a different namespace. The adapter sits on both, so these tests
// pin how its write paths behave against a store that honors that contract.

// errFakeNotFound is the fake backend's not-found sentinel, mirroring each
// real backend's own ErrObjectNotFound (which the adapter cannot import).
var errFakeNotFound = errors.New("object not found")

// fakeStore is a minimal KnowledgeStore recording the calls that matter to
// tenant isolation. Get can be told to answer (nil, nil) to model a backend
// that does not use an ErrObjectNotFound sentinel.
type fakeStore struct {
	objects map[string]*knowledge.KnowledgeObject
	// nilGet makes Get answer (nil, nil) instead of an error for a missing ID.
	nilGet  bool
	deleted []string
	saved   []*knowledge.KnowledgeObject
}

func newFakeStore(objs ...*knowledge.KnowledgeObject) *fakeStore {
	f := &fakeStore{objects: map[string]*knowledge.KnowledgeObject{}}
	for _, o := range objs {
		f.objects[o.ID] = o
	}
	return f
}

// Save honors the same contract as the real backends: an upsert whose ID
// already exists under a different namespace is refused with ErrObjectNotFound
// and the stored row is left untouched. Without this guard the fake would
// model a store that does not exist — one where a cross-tenant update
// migrates the victim row — and adapter tests would assert the wrong contract.
func (f *fakeStore) Save(_ context.Context, objects ...*knowledge.KnowledgeObject) error {
	for _, o := range objects {
		if prev, ok := f.objects[o.ID]; ok && prev.Namespace != o.Namespace {
			return errFakeNotFound
		}
		f.objects[o.ID] = o
		f.saved = append(f.saved, o)
	}
	return nil
}

// Get scopes by namespace exactly like the real backends: a foreign-namespace
// row answers the same as a missing one. Keeping the fake honest matters —
// a fake that ignores the tenant would let adapter code pass tests while
// relying on checks that real stores make unreachable.
func (f *fakeStore) Get(_ context.Context, tenantID, id string) (*knowledge.KnowledgeObject, error) {
	obj, ok := f.objects[id]
	if !ok || obj.Namespace != tenantID {
		if f.nilGet {
			return nil, nil
		}
		return nil, errFakeNotFound
	}
	return obj, nil
}

func (f *fakeStore) Delete(_ context.Context, tenantID, id string) error {
	obj, ok := f.objects[id]
	if !ok || obj.Namespace != tenantID {
		return errFakeNotFound
	}
	f.deleted = append(f.deleted, id)
	delete(f.objects, id)
	return nil
}

// Unused KnowledgeStore methods — the adapter never calls them.
func (f *fakeStore) Query(context.Context, knowledge.Query) ([]*knowledge.KnowledgeObject, error) {
	return nil, nil
}
func (f *fakeStore) Search(context.Context, string, string, string, int) ([]*knowledge.KnowledgeObject, error) {
	return nil, nil
}
func (f *fakeStore) SaveRepresentation(context.Context, *knowledge.Representation) error { return nil }
func (f *fakeStore) GetRepresentation(context.Context, string, string) (*knowledge.Representation, error) {
	return nil, nil
}
func (f *fakeStore) HybridSearch(context.Context, knowledge.HybridSearchRequest) ([]knowledge.ScoredObject, error) {
	return nil, nil
}
func (f *fakeStore) ListByStatus(context.Context, string, knowledge.ObjectStatus, int) ([]*knowledge.KnowledgeObject, error) {
	return nil, nil
}
func (f *fakeStore) UpdateStatus(context.Context, string, knowledge.ObjectStatus) error { return nil }
func (f *fakeStore) Promote(context.Context, string, *knowledge.Quality) error          { return nil }

// TestUpdateKnowledgeCrossTenantRefused pins the fix: updating an ID that
// belongs to another tenant must fail with the store's not-found answer and
// leave the victim row untouched.
//
// The attack path: akf_objects is keyed by `id` alone, and a tenant-scoped Get
// reports another tenant's row as absent, so the adapter cannot tell "missing"
// from "foreign" (and must not try — that would reopen ID enumeration). The
// pre-fix Save was a blind upsert, so the foreign row was overwritten and
// re-assigned to the caller's namespace. The ownership gate now lives in the
// store: Save refuses to overwrite a row owned by a different namespace.
func TestUpdateKnowledgeCrossTenantRefused(t *testing.T) {
	victim := &knowledge.KnowledgeObject{ID: "obj-1", Namespace: "tenant-a", Normalized: "secret"}
	store := newFakeStore(victim)
	a := NewStoreAdapter(store)

	_, err := a.UpdateKnowledge(context.Background(), "tenant-b", &KnowledgeItem{
		ID:      "obj-1",
		Content: "attacker content",
	})
	if err == nil {
		t.Fatal("cross-tenant UpdateKnowledge must fail, got nil error")
	}
	got := store.objects["obj-1"]
	if got == nil {
		t.Fatal("victim row must still exist")
	}
	if got.Namespace != "tenant-a" {
		t.Fatalf("victim namespace = %q, want tenant-a (must not be migrated)", got.Namespace)
	}
	if got.Normalized != "secret" {
		t.Fatalf("victim content = %q, want %q (must not be overwritten)", got.Normalized, "secret")
	}
}

// TestUpdateKnowledgeCrossTenantRefused_RealBackend is the end-to-end
// regression against the REAL memory backend (not fakeStore). The original
// defect passed every fake-based test because the fake's Get ignored the
// tenant parameter and answered with the foreign row — a contract no real
// backend implements. This test closes that gap: adapter + real store, the
// exact combination the vulnerability lived in.
func TestUpdateKnowledgeCrossTenantRefused_RealBackend(t *testing.T) {
	store := memorystore.New()
	ctx := context.Background()
	if err := store.Save(ctx, &knowledge.KnowledgeObject{
		ID: "obj-1", Namespace: "tenant-a", Normalized: "secret",
	}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	a := NewStoreAdapter(store)

	_, err := a.UpdateKnowledge(ctx, "tenant-b", &KnowledgeItem{
		ID:      "obj-1",
		Content: "attacker content",
	})
	if err == nil {
		t.Fatal("cross-tenant UpdateKnowledge must fail on a real backend, got nil error")
	}

	got, gerr := store.Get(ctx, "tenant-a", "obj-1")
	if gerr != nil {
		t.Fatalf("victim row must survive: %v", gerr)
	}
	if got.Namespace != "tenant-a" || got.Normalized != "secret" {
		t.Fatalf("victim migrated/overwritten: ns=%q normalized=%q", got.Namespace, got.Normalized)
	}
	if _, err := store.Get(ctx, "tenant-b", "obj-1"); !errors.Is(err, memorystore.ErrObjectNotFound) {
		t.Fatalf("refused update must not create the row in tenant-b, got %v", err)
	}
}

// TestUpdateKnowledgeSameTenantStillWorks pins that the ownership gate does
// not break the legitimate path: same-tenant update succeeds and still
// preserves the round-trip fields.
func TestUpdateKnowledgeSameTenantStillWorks(t *testing.T) {
	existing := &knowledge.KnowledgeObject{
		ID:             "obj-2",
		Namespace:      "tenant-a",
		Normalized:     "old",
		EmbeddingModel: "m1",
		Confidence:     0.9,
	}
	store := newFakeStore(existing)
	a := NewStoreAdapter(store)

	got, err := a.UpdateKnowledge(context.Background(), "tenant-a", &KnowledgeItem{
		ID:      "obj-2",
		Content: "new",
	})
	if err != nil {
		t.Fatalf("same-tenant UpdateKnowledge: %v", err)
	}
	if got == nil || got.Content != "new" {
		t.Fatalf("updated content = %+v, want content new", got)
	}
	if store.objects["obj-2"].EmbeddingModel != "m1" {
		t.Fatal("embedding model must be preserved across a same-tenant update")
	}
}

// TestDeleteKnowledgeRejectsNilObject pins the fix: when the store answers
// (nil, nil) for a missing ID, the delete must be refused. Pre-fix the check
// was `obj != nil && obj.Namespace != tenantID`, which skipped the tenant test
// on a nil object and deleted by ID — the write path was more permissive than
// the read path (GetKnowledge already rejected nil).
func TestDeleteKnowledgeRejectsNilObject(t *testing.T) {
	for _, nilGet := range []bool{true, false} {
		store := newFakeStore()
		store.nilGet = nilGet
		a := NewStoreAdapter(store)

		err := a.DeleteKnowledge(context.Background(), "tenant-a", "missing")
		if err == nil {
			t.Fatalf("DeleteKnowledge on a missing object (nilGet=%v) must fail", nilGet)
		}
		if len(store.deleted) != 0 {
			t.Fatalf("store.Delete must not be called for a missing object (nilGet=%v), got %v", nilGet, store.deleted)
		}
	}
}

// TestDeleteKnowledgeCrossTenantRefused pins that a foreign-namespace object
// is not deletable by another tenant.
func TestDeleteKnowledgeCrossTenantRefused(t *testing.T) {
	store := newFakeStore(&knowledge.KnowledgeObject{ID: "obj-3", Namespace: "tenant-a"})
	a := NewStoreAdapter(store)

	if err := a.DeleteKnowledge(context.Background(), "tenant-b", "obj-3"); err == nil {
		t.Fatal("cross-tenant DeleteKnowledge must fail")
	}
	if len(store.deleted) != 0 {
		t.Fatalf("store.Delete must not run cross-tenant, got %v", store.deleted)
	}
}

// TestDeleteKnowledgeSameTenantWorks pins the legitimate path still deletes.
func TestDeleteKnowledgeSameTenantWorks(t *testing.T) {
	store := newFakeStore(&knowledge.KnowledgeObject{ID: "obj-4", Namespace: "tenant-a"})
	a := NewStoreAdapter(store)

	if err := a.DeleteKnowledge(context.Background(), "tenant-a", "obj-4"); err != nil {
		t.Fatalf("same-tenant DeleteKnowledge: %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("expected one delete, got %v", store.deleted)
	}
}
