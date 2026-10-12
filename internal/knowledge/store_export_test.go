package knowledge_test

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// TestAKG_ExportImport_RoundTrip pins the core M3 contract: objects
// exported from one store survive a round-trip through Import into a
// fresh store with all fields intact.
func TestAKG_ExportImport_RoundTrip(t *testing.T) {
	t.Parallel()

	source := memstore.New()
	// Seed the source store with a few objects.
	seed := []*knowledge.KnowledgeObject{
		{ID: "obj1", Type: knowledge.ObjectMemory, Namespace: "ns1", Summary: "fact one", Confidence: 0.8},
		{ID: "obj2", Type: knowledge.ObjectProject, Namespace: "ns1", Summary: "fact two", Confidence: 0.9},
		{ID: "obj3", Type: knowledge.ObjectMemory, Namespace: "ns2", Summary: "fact three", Confidence: 0.7},
	}
	knowledge.StampWriteSchema(seed...)
	if err := source.Save(context.Background(), seed...); err != nil {
		t.Fatalf("seed source store: %v", err)
	}

	// Export all namespaces.
	snapshot, err := knowledge.ExportStore(context.Background(), source, "")
	if err != nil {
		t.Fatalf("ExportStore: %v", err)
	}
	if len(snapshot.Objects) != 3 {
		t.Errorf("snapshot has %d objects, want 3", len(snapshot.Objects))
	}
	if snapshot.SchemaVersion != knowledge.CurrentSchemaVersion {
		t.Errorf("snapshot SchemaVersion = %d, want %d",
			snapshot.SchemaVersion, knowledge.CurrentSchemaVersion)
	}

	// Import into a fresh store.
	target := memstore.New()
	if err := knowledge.ImportStore(context.Background(), target, snapshot); err != nil {
		t.Fatalf("ImportStore: %v", err)
	}

	// Verify all objects are in the target store.
	for _, obj := range seed {
		got, err := target.Get(context.Background(), obj.Namespace, obj.ID)
		if err != nil {
			t.Errorf("target.Get(%q): %v", obj.ID, err)
			continue
		}
		if got == nil {
			t.Errorf("object %q not found in target store", obj.ID)
			continue
		}
		if got.Summary != obj.Summary {
			t.Errorf("object %q Summary: got %q want %q", obj.ID, got.Summary, obj.Summary)
		}
		if got.Confidence != obj.Confidence {
			t.Errorf("object %q Confidence: got %v want %v", obj.ID, got.Confidence, obj.Confidence)
		}
	}
}

// TestAKG_ExportImport_NamespaceScoped pins that ExportStore with a
// namespace filter only exports objects in that namespace.
func TestAKG_ExportImport_NamespaceScoped(t *testing.T) {
	t.Parallel()

	source := memstore.New()
	seed := []*knowledge.KnowledgeObject{
		{ID: "a1", Type: knowledge.ObjectMemory, Namespace: "ns_a", Summary: "a"},
		{ID: "b1", Type: knowledge.ObjectMemory, Namespace: "ns_b", Summary: "b"},
	}
	knowledge.StampWriteSchema(seed...)
	if err := source.Save(context.Background(), seed...); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snapshot, err := knowledge.ExportStore(context.Background(), source, "ns_a")
	if err != nil {
		t.Fatalf("ExportStore: %v", err)
	}
	if len(snapshot.Objects) != 1 {
		t.Fatalf("namespace-scoped export has %d objects, want 1", len(snapshot.Objects))
	}
	if snapshot.Objects[0].Namespace != "ns_a" {
		t.Errorf("exported object namespace = %q, want %q",
			snapshot.Objects[0].Namespace, "ns_a")
	}
}

// TestAKG_ExportImport_Idempotent pins that re-importing the same
// snapshot does not duplicate or corrupt objects (Save is upsert by ID).
func TestAKG_ExportImport_Idempotent(t *testing.T) {
	t.Parallel()

	source := memstore.New()
	target := memstore.New()

	seed := []*knowledge.KnowledgeObject{
		{ID: "dup1", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "original"},
	}
	knowledge.StampWriteSchema(seed...)
	if err := source.Save(context.Background(), seed...); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snapshot, err := knowledge.ExportStore(context.Background(), source, "ns")
	if err != nil {
		t.Fatalf("ExportStore: %v", err)
	}

	// Import twice.
	if err := knowledge.ImportStore(context.Background(), target, snapshot); err != nil {
		t.Fatalf("first ImportStore: %v", err)
	}
	if err := knowledge.ImportStore(context.Background(), target, snapshot); err != nil {
		t.Fatalf("second ImportStore: %v", err)
	}

	// Should still have exactly 1 object.
	snapshot2, err := knowledge.ExportStore(context.Background(), target, "ns")
	if err != nil {
		t.Fatalf("second ExportStore: %v", err)
	}
	if len(snapshot2.Objects) != 1 {
		t.Errorf("after double import, target has %d objects, want 1", len(snapshot2.Objects))
	}
}

// TestAKG_ExportImport_NilSnapshot pins that ImportStore rejects a nil
// snapshot with a clear error.
func TestAKG_ExportImport_NilSnapshot(t *testing.T) {
	t.Parallel()

	store := memstore.New()
	err := knowledge.ImportStore(context.Background(), store, nil)
	if err == nil {
		t.Error("expected error for nil snapshot, got nil")
	}
}
