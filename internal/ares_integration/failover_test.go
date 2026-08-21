// package integration provides end-to-end integration tests with real PostgreSQL.
package ares_integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// createTestLeaderCheckpoint upserts a row into the leader_checkpoints table
// via raw SQL. The leader state package (state.CheckpointRepository) was
// removed with the leader runtime (aresos-agentos-plan C1); the table remains
// because the memory manager's GetLatestSessionForLeader reads it directly.
func createTestLeaderCheckpoint(
	ctx context.Context,
	pool *postgres.Pool,
	leaderID, sessionID, status string,
) error {
	metadata := json.RawMessage(fmt.Sprintf(`{"created_at": "%s"}`, time.Now().Format(time.RFC3339)))
	query := `
		INSERT INTO leader_checkpoints (leader_id, session_id, status, metadata, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (leader_id) DO UPDATE
		SET session_id = EXCLUDED.session_id, status = EXCLUDED.status,
			metadata = EXCLUDED.metadata, updated_at = NOW()
	`
	_, err := pool.Exec(ctx, query, leaderID, sessionID, status, metadata)
	return err
}

// TestProductionMemoryManagerGetLatestSessionForLeaderWithFailover verifies
// that GetLatestSessionForLeader works correctly after a simulated failover.
func TestProductionMemoryManagerGetLatestSessionForLeaderWithFailover(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "leader_checkpoints", "conversations")
	})

	ctx := context.Background()

	// Create a ProductionMemoryManager for testing GetLatestSessionForLeader.
	embeddingClient := embedding.NewEmbeddingClient(
		"http://localhost:9999",
		"intfloat/e5-large",
		nil,
		5*time.Second,
	)
	config := &memory.MemoryConfig{
		Enabled:        true,
		Storage:        "postgres",
		MaxHistory:     10,
		MaxSessions:    100,
		MaxTasks:       1000,
		SessionTTL:     24 * time.Hour,
		TaskTTL:        7 * 24 * time.Hour,
		VectorDim:      128,
		EnablePostgres: true,
	}
	mgr, err := memory.NewProductionMemoryManager(pool, embeddingClient, config)
	require.NoError(t, err)

	require.NoError(t, mgr.Start(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, mgr.Stop(stopCtx))
	}()

	require.NoError(t, mgr.SetTenantID("test-tenant"))

	leaderID := fmt.Sprintf("leader-mgr-%d", time.Now().UnixNano())

	// Before any checkpoint, should return empty.
	sessionID, err := mgr.GetLatestSessionForLeader(ctx, leaderID)
	require.NoError(t, err)
	assert.Empty(t, sessionID)

	// Insert a checkpoint.
	require.NoError(t, createTestLeaderCheckpoint(ctx, pool, leaderID, "session-old", "active"))

	// Should return the old session.
	sessionID, err = mgr.GetLatestSessionForLeader(ctx, leaderID)
	require.NoError(t, err)
	assert.Equal(t, "session-old", sessionID)

	// Simulate failover: update checkpoint with new session.
	require.NoError(t, createTestLeaderCheckpoint(ctx, pool, leaderID, "session-new", "active"))

	// Should return the new session.
	sessionID, err = mgr.GetLatestSessionForLeader(ctx, leaderID)
	require.NoError(t, err)
	assert.Equal(t, "session-new", sessionID)
}
