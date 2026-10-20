package knowledge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// seedMigrateStore builds a source corpus: two facts under the tenant-less
// "default" namespace, each with a vector.
func seedMigrateStore(t *testing.T) *memorystore.Store {
	t.Helper()
	ctx := context.Background()
	store := memorystore.New()
	for _, id := range []string{"akg:1", "akg:2"} {
		obj := &knowledge.KnowledgeObject{
			ID: id, Type: knowledge.ObjectMemory, Namespace: "default",
			Summary: "fact " + id, Normalized: "fact " + id,
			Status: knowledge.StatusActive, Confidence: 0.9,
		}
		knowledge.StampWriteSchema(obj)
		require.NoError(t, store.Save(ctx, obj))
		require.NoError(t, store.SaveRepresentation(ctx, &knowledge.Representation{
			ID: "rep:" + id, ObjectID: id, Model: "bge-m3", Dimension: 3,
			Vector: []float32{1, 0, 0},
		}))
	}
	return store
}

// TestMigrateNamespace_RehomesFactsAndVectors is the primitive behind enabling
// user-scoped recall on a corpus written before any user was declared: the facts
// must arrive in the user's graph WITH their vectors (deleting the object
// cascades them away otherwise) and must no longer be reachable from the tenant
// scope.
func TestMigrateNamespace_RehomesFactsAndVectors(t *testing.T) {
	ctx := context.Background()
	store := seedMigrateStore(t)
	target := knowledge.UserNamespace("acme", "alice")

	report, err := knowledge.MigrateNamespace(ctx, store, "default", target, knowledge.MigrateOptions{})
	require.NoError(t, err)
	require.Equal(t, 2, report.Considered)
	require.Equal(t, 2, report.Migrated)
	require.Equal(t, 2, report.Vectors, "every moved fact must keep its vector")

	// Gone from the source: a copy left behind would stay visible to exactly the
	// tenant-scoped recall this move exists to scope down.
	left, err := store.Query(ctx, knowledge.Query{Namespace: "default", Limit: 0})
	require.NoError(t, err)
	require.Empty(t, left)

	// Present in the target, vectors included.
	moved, err := store.Query(ctx, knowledge.Query{Namespace: target, Limit: 0})
	require.NoError(t, err)
	require.Len(t, moved, 2)
	rep, err := store.GetRepresentation(ctx, target, "akg:1", "bge-m3")
	require.NoError(t, err)
	require.Equal(t, []float32{1, 0, 0}, rep.Vector)
}

// TestMigrateNamespace_FailsClosed pins the caller mistakes that would otherwise
// become a silent no-op or an unscoped scan.
func TestMigrateNamespace_FailsClosed(t *testing.T) {
	ctx := context.Background()
	store := memorystore.New()

	for _, tc := range []struct{ name, from, to string }{
		{name: "empty source", from: "", to: "tu:x"},
		{name: "empty target", from: "default", to: ""},
		{name: "same namespace", from: "default", to: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := knowledge.MigrateNamespace(ctx, store, tc.from, tc.to, knowledge.MigrateOptions{}); err == nil {
				t.Fatalf("MigrateNamespace(%q, %q) must fail", tc.from, tc.to)
			}
		})
	}
}

// TestMigrateNamespace_DryRunChangesNothing pins that an operator can size the
// move before making it.
func TestMigrateNamespace_DryRunChangesNothing(t *testing.T) {
	ctx := context.Background()
	store := seedMigrateStore(t)

	report, err := knowledge.MigrateNamespace(ctx, store, "default", knowledge.UserNamespace("acme", "alice"),
		knowledge.MigrateOptions{DryRun: true})
	require.NoError(t, err)
	require.True(t, report.DryRun)
	require.Equal(t, 2, report.Migrated)

	still, err := store.Query(ctx, knowledge.Query{Namespace: "default", Limit: 0})
	require.NoError(t, err)
	require.Len(t, still, 2, "a dry run must not touch the store")
}

// TestMigrateNamespace_ResumesAfterLimit pins the property that makes a bounded
// or interrupted pass safe to re-run: a second call picks up what is left rather
// than repeating or skipping it.
func TestMigrateNamespace_ResumesAfterLimit(t *testing.T) {
	ctx := context.Background()
	store := seedMigrateStore(t)
	target := knowledge.UserNamespace("acme", "alice")

	first, err := knowledge.MigrateNamespace(ctx, store, "default", target, knowledge.MigrateOptions{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 1, first.Migrated)

	second, err := knowledge.MigrateNamespace(ctx, store, "default", target, knowledge.MigrateOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, second.Considered, "the re-run must see only what is left")

	all, err := store.Query(ctx, knowledge.Query{Namespace: target, Limit: 0})
	require.NoError(t, err)
	require.Len(t, all, 2)
}

// failingSaveStore fails Save for one namespace, simulating the save failure a
// migration must survive. It embeds the CONCRETE store so the optional
// capabilities stay exposed — embedding the interface would hide them and the
// vector path would go untested.
type failingSaveStore struct {
	*memorystore.Store
	failNamespace string
}

func (s *failingSaveStore) Save(ctx context.Context, objects ...*knowledge.KnowledgeObject) error {
	for _, obj := range objects {
		if obj != nil && obj.Namespace == s.failNamespace {
			return errors.New("simulated save failure")
		}
	}
	return s.Store.Save(ctx, objects...)
}

// TestMigrateNamespace_RollsBackAFailedMove pins the safety property: a move
// that cannot be saved must leave the fact where it was, vector included, rather
// than deleting it.
func TestMigrateNamespace_RollsBackAFailedMove(t *testing.T) {
	ctx := context.Background()
	inner := seedMigrateStore(t)
	target := knowledge.UserNamespace("acme", "alice")
	store := &failingSaveStore{Store: inner, failNamespace: target}

	report, err := knowledge.MigrateNamespace(ctx, store, "default", target, knowledge.MigrateOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "simulated save failure")
	require.Zero(t, report.Migrated, "a rolled-back move must not be counted as migrated")

	still, qErr := inner.Query(ctx, knowledge.Query{Namespace: "default", Limit: 0})
	require.NoError(t, qErr)
	require.Len(t, still, 2, "a failed move must not drop the fact")

	rep, rErr := inner.GetRepresentation(ctx, "default", "akg:1", "bge-m3")
	require.NoError(t, rErr, "the rolled-back fact must keep its vector")
	require.Equal(t, []float32{1, 0, 0}, rep.Vector)
}
