package knowledge_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

func importFixture() (*knowledge.StoreSnapshot, *knowledge.KnowledgeObject) {
	obj := &knowledge.KnowledgeObject{
		ID: "akg:import-1", Type: knowledge.ObjectMemory, Namespace: "tenant-a",
		Summary: "redis caching", Normalized: "redis caching",
		Status: knowledge.StatusActive, Confidence: 0.9, CreatedAt: time.Now(),
	}
	knowledge.StampWriteSchema(obj)
	return &knowledge.StoreSnapshot{
		SchemaVersion: knowledge.CurrentSchemaVersion,
		ExportedAt:    time.Now(),
		Objects:       []*knowledge.KnowledgeObject{obj},
	}, obj
}

// TestImportStore_RefusesANewerSchema is the guard for the one downgrade that looks like
// success: a snapshot written by a newer build would be re-stamped with this build's older
// version, dropping whatever the newer fields meant — and the import would report OK.
func TestImportStore_RefusesANewerSchema(t *testing.T) {
	snapshot, _ := importFixture()
	snapshot.SchemaVersion = knowledge.CurrentSchemaVersion + 1

	store := memorystore.New()
	err := knowledge.ImportStore(context.Background(), store, snapshot)
	require.Error(t, err)
	assert.True(t, errors.Is(err, knowledge.ErrSchemaVersionTooNew),
		"callers key on the sentinel, so the wrapped identity must survive: %v", err)

	// Nothing may be written: a half-imported newer snapshot is the worst outcome.
	_, getErr := store.Get(context.Background(), "tenant-a", "akg:import-1")
	assert.Error(t, getErr, "a refused import must not persist anything")
}

// TestImportStore_AcceptsCurrentAndLegacySchema is the POSITIVE path: the guard must not
// have become a wall. The current version imports, and the legacy zero version imports too
// (per-object MigrateObject upgrades those records).
func TestImportStore_AcceptsCurrentAndLegacySchema(t *testing.T) {
	for name, version := range map[string]int{
		"current":     knowledge.CurrentSchemaVersion,
		"legacy zero": 0,
	} {
		t.Run(name, func(t *testing.T) {
			snapshot, obj := importFixture()
			snapshot.SchemaVersion = version
			store := memorystore.New()

			require.NoError(t, knowledge.ImportStore(context.Background(), store, snapshot))
			got, err := store.Get(context.Background(), "tenant-a", obj.ID)
			require.NoError(t, err, "an accepted snapshot must be readable back")
			assert.Equal(t, obj.ID, got.ID)
		})
	}
}

// recordingImporter exposes the StoreExporter capability (by embedding the CONCRETE store,
// so the optional interface is visible) and records whether the fast path ran.
type recordingImporter struct {
	*memorystore.Store
	called *bool
}

// Compile-time proof that the fake really satisfies the optional interface. Without it the
// type silently fails the assertion inside ImportStore, the test falls through to Save and
// passes — while never touching the fast path it claims to cover. (That is exactly what
// happened here first time: the fake implemented Import but not Export.)
var _ knowledge.StoreExporter = recordingImporter{}

// Export is here because StoreExporter requires BOTH methods: a type that implements only
// Import does not satisfy the interface, silently falls back to Save, and would make this
// test pass while never exercising the fast path it claims to cover.
func (r recordingImporter) Export(_ context.Context, _ string) (*knowledge.StoreSnapshot, error) {
	return &knowledge.StoreSnapshot{}, nil
}

func (r recordingImporter) Import(_ context.Context, _ *knowledge.StoreSnapshot) error {
	*r.called = true
	return nil
}

// TestImportStore_GuardCoversTheStoreExporterFastPath: the validation must not be bypassable
// by a store that implements its own Import — otherwise the guard holds for the stores that
// do not need it and misses the ones that do.
func TestImportStore_GuardCoversTheStoreExporterFastPath(t *testing.T) {
	called := false
	store := recordingImporter{Store: memorystore.New(), called: &called}

	snapshot, _ := importFixture()
	snapshot.SchemaVersion = knowledge.CurrentSchemaVersion + 1
	require.Error(t, knowledge.ImportStore(context.Background(), store, snapshot))
	assert.False(t, called, "the per-store Import must not run behind the guard's back")

	// And it does run for an acceptable snapshot — the guard narrows nothing else.
	snapshot.SchemaVersion = knowledge.CurrentSchemaVersion
	require.NoError(t, knowledge.ImportStore(context.Background(), store, snapshot))
	assert.True(t, called)
}
