package ares_bootstrap

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/stretchr/testify/require"
)

// TestBuildBootstrapKnowledgeStore_DefaultInMemory verifies the AKG store
// defaults to in-memory when no postgres storage is configured — the product
// decision (2026-08-03): no external DB required by default.
func TestBuildBootstrapKnowledgeStore_DefaultInMemory(t *testing.T) {
	store, err := buildBootstrapKnowledgeStore(&ares_config.Config{})
	require.NoError(t, err)
	require.NotNil(t, store)
}

// TestBuildBootstrapKnowledgeStore_PGUnreachable verifies that a postgres
// storage config pointing at an unreachable host returns an error (the
// caller degrades to no AKG loop rather than silently proceeding with a
// broken store).
func TestBuildBootstrapKnowledgeStore_PGUnreachable(t *testing.T) {
	cfg := &ares_config.Config{
		Storage: ares_config.StorageConfig{
			Enabled:  true,
			Type:     "postgres",
			Host:     "127.0.0.1",
			Port:     1, // nothing listens here
			Username: "nobody",
			Password: "nobody",
			Database: "nobody",
			SSLMode:  "disable",
		},
	}
	_, err := buildBootstrapKnowledgeStore(cfg)
	require.Error(t, err)
}

// TestWireAKGLoop_Disabled verifies the AKG loop is skipped entirely when
// knowledge retrieval is not enabled — minimal configs keep prior behavior.
func TestWireAKGLoop_Disabled(t *testing.T) {
	store, bridge := wireAKGLoop(&ares_config.Config{}, nil, nil)
	require.Nil(t, store)
	require.Nil(t, bridge)
}

// TestWireAKGLoop_StoreOnly verifies that enabling AKG with a working
// in-memory store but no write deps yields a read-side store and a nil
// bridge (write loop skipped, read loop active).
func TestWireAKGLoop_StoreOnly(t *testing.T) {
	cfg := &ares_config.Config{}
	cfg.Knowledge.RetrievalEnabled = true
	store, bridge := wireAKGLoop(cfg, nil, nil)
	require.NotNil(t, store)
	require.Nil(t, bridge)
}

// TestAKGModelName_NilEmb verifies the model helper returns "" for a nil
// embedding service (lexical-only HybridSearch).
func TestAKGModelName_NilEmb(t *testing.T) {
	require.Equal(t, "", akgModelName(nil))
}

// TestAKGModelName_WithEmb verifies the model helper returns the configured
// model name when an embedding service is present.
func TestAKGModelName_WithEmb(t *testing.T) {
	require.Equal(t, "test", akgModelName(&testEmbedder{}))
}
