package knowledge_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// knowledgeStoreOnly satisfies knowledge.KnowledgeStore by delegation while
// hiding BOTH optional capabilities (NamespaceLister, RepresentationLister):
// embedding the interface rather than the concrete store is what hides them.
type knowledgeStoreOnly struct {
	knowledge.KnowledgeStore
}

// TestExportImport_RoundTripCarriesVectors is the regression for the lossy
// backup: a snapshot used to carry objects only, so a round trip returned the
// facts without their embeddings and recall silently degraded to lexical — the
// exact defect an operator-facing `ares knowledge export|import` would have
// shipped.
func TestExportImport_RoundTripCarriesVectors(t *testing.T) {
	ctx := context.Background()

	src := memorystore.New()
	obj := &knowledge.KnowledgeObject{
		ID: "akg:1", Type: knowledge.ObjectMemory, Namespace: "tenant-a",
		Summary: "redis caching", Normalized: "redis caching",
		Status: knowledge.StatusActive, Confidence: 0.9, CreatedAt: time.Now(),
	}
	knowledge.StampWriteSchema(obj)
	require.NoError(t, src.Save(ctx, obj))
	require.NoError(t, src.SaveRepresentation(ctx, &knowledge.Representation{
		ID: "rep:1", ObjectID: obj.ID, Model: "bge-m3", Dimension: 3,
		Vector: []float32{0.1, 0.2, 0.3},
	}))

	snapshot, err := knowledge.ExportStore(ctx, src, "tenant-a")
	require.NoError(t, err)
	require.Len(t, snapshot.Objects, 1)
	require.Len(t, snapshot.Representations, 1,
		"the snapshot must carry the vectors, not only the facts")

	dst := memorystore.New()
	require.NoError(t, knowledge.ImportStore(ctx, dst, snapshot))

	restored, err := dst.GetRepresentation(ctx, "tenant-a", obj.ID, "bge-m3")
	require.NoError(t, err, "the restored store must still serve the vector")
	require.Equal(t, []float32{0.1, 0.2, 0.3}, restored.Vector,
		"a round trip must not silently degrade recall to lexical")
}

// TestExportStore_StoreWithoutVectorListingIsObjectOnly pins the degraded-but-
// valid path: a store that cannot enumerate representations still exports its
// objects and reports no error. Warning about the loss is the operator
// surface's job, not the library's.
func TestExportStore_StoreWithoutVectorListingIsObjectOnly(t *testing.T) {
	ctx := context.Background()
	inner := memorystore.New()
	require.NoError(t, inner.Save(ctx, &knowledge.KnowledgeObject{
		ID: "akg:1", Type: knowledge.ObjectMemory, Namespace: "tenant-a",
		Summary: "fact", Status: knowledge.StatusActive,
	}))

	var bare knowledge.KnowledgeStore = knowledgeStoreOnly{KnowledgeStore: inner}
	snapshot, err := knowledge.ExportStore(ctx, bare, "tenant-a")
	require.NoError(t, err)
	require.Len(t, snapshot.Objects, 1)
	require.Empty(t, snapshot.Representations,
		"a store without the capability exports objects only, without erroring")
}

// TestListRepresentations_RequiresNamespace pins the fail-closed contract: a
// vector is reachable only through its owning object, so an unscoped listing
// would cross tenants.
func TestListRepresentations_RequiresNamespace(t *testing.T) {
	store := memorystore.New()
	if _, err := knowledge.ListRepresentations(context.Background(), store, ""); err == nil {
		t.Fatal("expected an error for an empty namespace, got nil")
	}
}
