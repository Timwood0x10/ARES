package ares_bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// bareKnowledgeStore satisfies knowledge.KnowledgeStore by delegation while
// hiding the optional NamespaceLister capability: embedding the interface (not
// the concrete store) is what removes it.
type bareKnowledgeStore struct {
	knowledge.KnowledgeStore
}

// TestAKGDecayCleaner_ArchivesPerNamespace verifies the M4 decay pass wired onto
// the maintenance sweep: it archives stale objects in EVERY namespace the store
// holds (an unscoped pass would cross tenants), leaves fresh ones active, and
// archives rather than deletes.
func TestAKGDecayCleaner_ArchivesPerNamespace(t *testing.T) {
	ctx := context.Background()
	store := memorystore.New()

	stale := time.Now().Add(-48 * time.Hour)
	fresh := time.Now()
	objs := []*knowledge.KnowledgeObject{
		{ID: "a:stale", Type: knowledge.ObjectMemory, Namespace: "tenant-a", Status: knowledge.StatusActive, CreatedAt: stale, UpdatedAt: stale},
		{ID: "a:fresh", Type: knowledge.ObjectMemory, Namespace: "tenant-a", Status: knowledge.StatusActive, CreatedAt: fresh, UpdatedAt: fresh},
		{ID: "b:stale", Type: knowledge.ObjectMemory, Namespace: "tenant-b", Status: knowledge.StatusActive, CreatedAt: stale, UpdatedAt: stale},
	}
	knowledge.StampWriteSchema(objs...)
	require.NoError(t, store.Save(ctx, objs...))

	cleaner := akgDecayCleaner{store: store, cfg: knowledge.DecayConfig{ArchiveAfter: 24 * time.Hour}}
	archived, err := cleaner.CleanupExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), archived,
		"every namespace with a stale object must be visited, not just one")

	for _, tc := range []struct {
		namespace string
		id        string
		want      knowledge.ObjectStatus
	}{
		{namespace: "tenant-a", id: "a:stale", want: knowledge.StatusSuperseded},
		{namespace: "tenant-b", id: "b:stale", want: knowledge.StatusSuperseded},
		{namespace: "tenant-a", id: "a:fresh", want: knowledge.StatusActive},
	} {
		obj, getErr := store.Get(ctx, tc.namespace, tc.id)
		require.NoError(t, getErr, "%s must still exist: decay archives, it never deletes", tc.id)
		require.Equal(t, tc.want, obj.Status, "%s in %s", tc.id, tc.namespace)
	}
}

// TestAKGDecayCleaner_SkipsStoreWithoutNamespaceListing pins the fail-safe: a
// store that cannot enumerate its namespaces is skipped, not guessed at —
// inventing a namespace would either miss objects or scan across tenants.
func TestAKGDecayCleaner_SkipsStoreWithoutNamespaceListing(t *testing.T) {
	ctx := context.Background()
	inner := memorystore.New()
	require.NoError(t, inner.Save(ctx, &knowledge.KnowledgeObject{
		ID: "a:stale", Type: knowledge.ObjectMemory, Namespace: "tenant-a",
		Status: knowledge.StatusActive, CreatedAt: time.Now().Add(-48 * time.Hour),
	}))

	var bare knowledge.KnowledgeStore = bareKnowledgeStore{KnowledgeStore: inner}
	cleaner := akgDecayCleaner{store: bare, cfg: knowledge.DecayConfig{ArchiveAfter: time.Hour}}

	archived, err := cleaner.CleanupExpired(ctx)
	require.NoError(t, err, "a missing optional capability must not be an error")
	require.Zero(t, archived)

	obj, getErr := inner.Get(ctx, "tenant-a", "a:stale")
	require.NoError(t, getErr)
	require.Equal(t, knowledge.StatusActive, obj.Status,
		"skipping is safe; guessing a namespace would not be")
}

// TestWireAKGDecay_OptIn pins that the pass is registered only when the operator
// opts in: an existing deployment must not start archiving knowledge merely
// because it upgraded.
func TestWireAKGDecay_OptIn(t *testing.T) {
	comp := &Components{KnowledgeStore: memorystore.New()}

	wireAKGDecay(comp, &ares_config.Config{})
	require.Empty(t, comp.ExpiryCleaners, "the default config must not wire the decay pass")

	wireAKGDecay(comp, &ares_config.Config{
		Knowledge: ares_config.KnowledgeConfig{DecayArchiveAfter: 24 * time.Hour},
	})
	require.Len(t, comp.ExpiryCleaners, 1)
	require.Equal(t, "akf_decay", comp.ExpiryCleaners[0].Name)
	require.Equal(t, "archived", comp.ExpiryCleaners[0].Action,
		"the pass log must not claim a deletion that never happens")
}

// TestWireAKGDecay_NoStore pins that a bootstrap without an AKG store (the
// default, since the loop is gated on knowledge.retrieval_enabled) registers
// nothing rather than a cleaner that would nil-panic on its first pass.
func TestWireAKGDecay_NoStore(t *testing.T) {
	comp := &Components{}
	require.NotPanics(t, func() {
		wireAKGDecay(comp, &ares_config.Config{
			Knowledge: ares_config.KnowledgeConfig{DecayArchiveAfter: time.Hour},
		})
	})
	require.Empty(t, comp.ExpiryCleaners)
}
