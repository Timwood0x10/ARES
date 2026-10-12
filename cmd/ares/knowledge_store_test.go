package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// seedSnapshotStore builds a store holding one object plus its vector.
func seedSnapshotStore(t *testing.T) knowledge.KnowledgeStore {
	t.Helper()
	ctx := context.Background()
	store := memorystore.New()
	obj := &knowledge.KnowledgeObject{
		ID: "akg:1", Type: knowledge.ObjectMemory, Namespace: "tenant-a",
		Summary: "redis caching", Normalized: "redis caching",
		Status: knowledge.StatusActive, Confidence: 0.9,
	}
	knowledge.StampWriteSchema(obj)
	require.NoError(t, store.Save(ctx, obj))
	require.NoError(t, store.SaveRepresentation(ctx, &knowledge.Representation{
		ID: "rep:1", ObjectID: obj.ID, Model: "bge-m3", Dimension: 3,
		Vector: []float32{0.1, 0.2, 0.3},
	}))
	return store
}

// TestKnowledgeSnapshot_ExportImportRoundTrip exercises the operator commands'
// core: an exported snapshot must restore into a fresh store with its vectors
// intact, otherwise the backup is silently lossy.
func TestKnowledgeSnapshot_ExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := seedSnapshotStore(t)

	var buf bytes.Buffer
	exported, err := exportSnapshotTo(ctx, &buf, src, "tenant-a")
	require.NoError(t, err)
	require.Equal(t, snapshotCounts{Objects: 1, Representations: 1, Namespace: "tenant-a"}, exported,
		"the export must carry the vectors, not only the objects")

	dst := memorystore.New()
	imported, err := importSnapshotFrom(ctx, bytes.NewReader(buf.Bytes()), dst)
	require.NoError(t, err)
	require.Equal(t, exported.Objects, imported.Objects)
	require.Equal(t, exported.Representations, imported.Representations)

	rep, err := dst.GetRepresentation(ctx, "tenant-a", "akg:1", "bge-m3")
	require.NoError(t, err)
	require.Equal(t, []float32{0.1, 0.2, 0.3}, rep.Vector)
}

// TestKnowledgeSnapshot_ImportRefusesEmpty pins that an empty snapshot is an
// error: importing it "successfully" would report a restore that restored
// nothing, which is how a wrong-file mistake hides.
func TestKnowledgeSnapshot_ImportRefusesEmpty(t *testing.T) {
	empty := knowledge.StoreSnapshot{SchemaVersion: knowledge.CurrentSchemaVersion}
	data, err := json.Marshal(empty)
	require.NoError(t, err)

	_, err = importSnapshotFrom(context.Background(), bytes.NewReader(data), memorystore.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no objects")
}

// TestKnowledgeSnapshot_ImportRejectsGarbage pins that a malformed file fails
// with a decode error rather than a partial import.
func TestKnowledgeSnapshot_ImportRejectsGarbage(t *testing.T) {
	_, err := importSnapshotFrom(context.Background(), strings.NewReader("not json"), memorystore.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode snapshot")
}

// TestOpenAKGStore_RefusesInMemoryConfig pins the loud failure that keeps an
// operator from "successfully" exporting an empty corpus: the default store is
// in-memory, and a separate process can never read it.
func TestOpenAKGStore_RefusesInMemoryConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ares.yaml")
	require.NoError(t, os.WriteFile(path, []byte("memory:\n  enabled: true\n"), 0o600))

	_, _, err := openAKGStore(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "persistent Postgres store",
		"the error must say what to configure, not just fail")
}

// TestOpenAKGStore_MissingConfigIsAnError pins that a missing config file is
// refused rather than silently defaulted: for a data operation, quietly aiming
// at the built-in defaults could hit the wrong database.
func TestOpenAKGStore_MissingConfigIsAnError(t *testing.T) {
	_, _, err := openAKGStore(filepath.Join(t.TempDir(), "absent.yaml"))
	require.Error(t, err)
}
