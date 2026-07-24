package ares_bootstrap

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// enabledCompilerConfig returns a minimal valid enabled compiler config with
// the given AKG store mode, mirroring what setDefaults would normalize.
func enabledCompilerConfig(akgStore, sqlitePath string) *ares_config.Config {
	return &ares_config.Config{
		KnowledgeCompiler: ares_config.KnowledgeCompilerConfig{
			Enabled:          true,
			MaxNodes:         100,
			PromptMaxTokens:  1024,
			AKGMaxFacts:      50,
			MinConfidence:    0.3,
			AKGMinConfidence: 0.6,
			DistillMinScore:  0.4,
			WindowSize:       32000,
			Threshold:        0.7,
			AKGStore:         akgStore,
			AKGSQLitePath:    sqlitePath,
		},
	}
}

// TestNewSharedAKGStoreMemoryExplicit verifies the explicit "memory" mode
// returns an in-memory store with no cleanup (nothing to close).
func TestNewSharedAKGStoreMemoryExplicit(t *testing.T) {
	cfg := enabledCompilerConfig(ares_config.AKGStoreMemory, "")

	store, cleanup, err := newSharedAKGStore(context.Background(), cfg)

	require.NoError(t, err)
	require.NotNil(t, store)
	assert.Nil(t, cleanup, "in-memory store must not register a cleanup")
	assert.IsType(t, memorystore.New(), store)
}

// TestNewSharedAKGStoreAutoWithoutPostgres verifies the "auto" mode (and its
// empty-string equivalent) resolves to in-memory when no PostgreSQL storage is
// configured, preserving prior behavior for existing deployments.
func TestNewSharedAKGStoreAutoWithoutPostgres(t *testing.T) {
	for _, mode := range []string{ares_config.AKGStoreAuto, ""} {
		cfg := enabledCompilerConfig(mode, "")

		store, cleanup, err := newSharedAKGStore(context.Background(), cfg)

		require.NoError(t, err, "mode %q", mode)
		require.NotNil(t, store, "mode %q", mode)
		assert.Nil(t, cleanup, "mode %q: auto->memory must not register a cleanup", mode)
		assert.IsType(t, memorystore.New(), store, "mode %q", mode)
	}
}

// TestNewSharedAKGStoreSQLitePersistsAcrossReopen is the Phase 2 e2e check:
// objects written through the sqlite-backed store must survive a close/reopen
// cycle on the same database file (restart durability).
func TestNewSharedAKGStoreSQLitePersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "akg", "test.db")
	cfg := enabledCompilerConfig(ares_config.AKGStoreSQLite, dbPath)

	// First "process": open, write one object, close.
	store1, cleanup1, err := newSharedAKGStore(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, cleanup1, "sqlite store must register a cleanup")

	obj := &knowledge.KnowledgeObject{
		ID:         "akg-phase2-durability",
		Type:       knowledge.ObjectType("fact"),
		Namespace:  "conversation-compiler",
		Summary:    "ARES uses AKG graph",
		Normalized: "ares uses akg graph",
		Confidence: 0.9,
		Version:    1,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store1.Save(ctx, obj))
	cleanup1()

	// Second "process": reopen the same file and read the object back.
	store2, cleanup2, err := newSharedAKGStore(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, cleanup2)
	defer cleanup2()

	got, err := store2.Get(ctx, obj.ID)
	require.NoError(t, err, "object must survive a close/reopen cycle")
	assert.Equal(t, obj.Summary, got.Summary)
	assert.Equal(t, obj.Namespace, got.Namespace)
	assert.InDelta(t, obj.Confidence, got.Confidence, 1e-6)

	// Namespace query path must also see it (the StoreProvider read path).
	results, err := store2.Query(ctx, knowledge.Query{Namespace: obj.Namespace})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, obj.ID, results[0].ID)
}

// TestNewSharedAKGStoreSQLiteEmptyPath verifies the defensive empty-path guard
// returns an explicit error instead of opening an unnamed database.
func TestNewSharedAKGStoreSQLiteEmptyPath(t *testing.T) {
	cfg := enabledCompilerConfig(ares_config.AKGStoreSQLite, "")

	store, cleanup, err := newSharedAKGStore(context.Background(), cfg)

	require.Error(t, err)
	assert.Nil(t, store)
	assert.Nil(t, cleanup)
	assert.Contains(t, err.Error(), "path must not be empty")
}

// TestNewSharedAKGStorePostgresUnreachable verifies the postgres path fails
// fast with a wrapped error when the database is unreachable, so the caller
// can fall back to in-memory gracefully.
func TestNewSharedAKGStorePostgresUnreachable(t *testing.T) {
	cfg := enabledCompilerConfig(ares_config.AKGStorePostgres, "")
	cfg.Storage = ares_config.StorageConfig{
		Enabled:  true,
		Type:     "postgres",
		Host:     "127.0.0.1",
		Port:     1, // Reserved port: nothing listens here.
		Username: "nobody",
		Password: "nope",
		Database: "missing",
		SSLMode:  "disable",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, cleanup, err := newSharedAKGStore(ctx, cfg)

	require.Error(t, err)
	assert.Nil(t, store)
	assert.Nil(t, cleanup)
	assert.Contains(t, err.Error(), "akg postgres store")
}

// TestWireKnowledgeCompilerFallsBackToMemory verifies the wiring-level
// degradation contract: an unreachable persistence backend must not prevent
// the compiler pipeline from wiring; it falls back to an in-memory pool.
func TestWireKnowledgeCompilerFallsBackToMemory(t *testing.T) {
	comp := &Components{}
	cfg := enabledCompilerConfig(ares_config.AKGStorePostgres, "")
	cfg.Storage = ares_config.StorageConfig{
		Enabled:  true,
		Type:     "postgres",
		Host:     "127.0.0.1",
		Port:     1,
		Username: "nobody",
		Password: "nope",
		Database: "missing",
		SSLMode:  "disable",
	}

	var cleanups []func()
	wireKnowledgeCompiler(context.Background(), cfg, comp, &cleanups)

	require.NotNil(t, comp.KnowledgeCompiler, "pipeline must wire despite store failure")
	require.NotNil(t, comp.KnowledgeStore, "a fallback store must be present")
	assert.IsType(t, memorystore.New(), comp.KnowledgeStore, "fallback must be in-memory")
	assert.Empty(t, cleanups, "no DB handle must be registered on fallback")
}

// TestWireKnowledgeCompilerSQLiteRegistersCleanup verifies the durable path
// registers exactly one cleanup (the DB close) so bootstrap can shut the
// handle down without leaking connections.
func TestWireKnowledgeCompilerSQLiteRegistersCleanup(t *testing.T) {
	comp := &Components{}
	dbPath := filepath.Join(t.TempDir(), "akg.db")
	cfg := enabledCompilerConfig(ares_config.AKGStoreSQLite, dbPath)

	var cleanups []func()
	wireKnowledgeCompiler(context.Background(), cfg, comp, &cleanups)

	require.NotNil(t, comp.KnowledgeCompiler)
	require.NotNil(t, comp.KnowledgeStore)
	require.Len(t, cleanups, 1, "sqlite path must register the DB close cleanup")
	assert.NotPanics(t, func() { cleanups[0]() })
}
