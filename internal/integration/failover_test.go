// package integration provides end-to-end integration tests with real PostgreSQL.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagent/internal/agents/leader"
	"goagent/internal/memory"
	"goagent/internal/storage/postgres"
	"goagent/internal/storage/postgres/embedding"
)

// createTestLeaderCheckpoint creates a checkpoint for the given leader and session.
func createTestLeaderCheckpoint(
	ctx context.Context,
	pool *postgres.Pool,
	leaderID, sessionID, status string,
) error {
	metadata := json.RawMessage(fmt.Sprintf(`{"created_at": "%s"}`, time.Now().Format(time.RFC3339)))
	cp := &leader.LeaderCheckpoint{
		LeaderID:  leaderID,
		SessionID: sessionID,
		Status:    status,
		Metadata:  metadata,
	}
	repo := leader.NewCheckpointRepository(pool)
	return repo.Save(ctx, cp)
}

// insertStaleTask inserts a task_result with pending/running status and no output,
// simulating a task that was orphaned by a leader crash.
func insertStaleTask(
	ctx context.Context,
	pool *postgres.Pool,
	taskID, sessionID, status string,
) error {
	query := `
		INSERT INTO task_results_1024
		(id, tenant_id, session_id, task_type, agent_id, input, status, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
	`
	_, err := pool.Exec(ctx, query,
		taskID,
		"test-tenant",
		sessionID,
		"user_request",
		"style-agent",
		`{"content": "test input"}`,
		status,
		`{}`,
	)
	return err
}

// TestCheckpointRecovery verifies the full checkpoint lifecycle for leader failover:
// save checkpoint -> simulate crash -> recover checkpoint with successor.
func TestCheckpointRecovery(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Logf("failed to close pool: %v", err)
		}
	}()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "leader_checkpoints")
	})

	ctx := context.Background()
	repo := leader.NewCheckpointRepository(pool)
	require.NotNil(t, repo)

	leaderID := fmt.Sprintf("leader-recovery-%d", time.Now().UnixNano())

	// Step 1: Leader saves a checkpoint with active status.
	require.NoError(t, createTestLeaderCheckpoint(ctx, pool, leaderID, "session-001", "active"))

	// Step 2: Simulate crash - the leader stops without cleanup.
	// The checkpoint remains in the database with "active" status.

	// Step 3: Successor leader retrieves the checkpoint.
	cp, err := repo.GetLatest(ctx, leaderID)
	require.NoError(t, err)
	require.NotNil(t, cp, "expected checkpoint to exist after simulated crash")
	assert.Equal(t, "session-001", cp.SessionID)
	assert.Equal(t, "active", cp.Status)

	// Step 4: Successor leader takes over - updates checkpoint with new session.
	require.NoError(t, createTestLeaderCheckpoint(ctx, pool, leaderID, "session-002", "active"))

	// Verify the checkpoint was updated.
	cp2, err := repo.GetLatest(ctx, leaderID)
	require.NoError(t, err)
	require.NotNil(t, cp2)
	assert.Equal(t, "session-002", cp2.SessionID)
}

// TestStaleTaskRecovery verifies that orphaned tasks are correctly identified
// and marked as failed during leader failover.
func TestStaleTaskRecovery(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Logf("failed to close pool: %v", err)
		}
	}()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "task_results_1024", "leader_checkpoints")
	})

	ctx := context.Background()
	sessionID := fmt.Sprintf("session-stale-%d", time.Now().UnixNano())

	// Insert stale tasks (pending and running with no output).
	require.NoError(t, insertStaleTask(ctx, pool, "task-pending-1", sessionID, "pending"))
	require.NoError(t, insertStaleTask(ctx, pool, "task-running-1", sessionID, "running"))
	require.NoError(t, insertStaleTask(ctx, pool, "task-completed-1", sessionID, "completed"))

	// Create TaskRecovery and recover stale tasks.
	recovery := leader.NewTaskRecovery(pool)
	staleTasks, err := recovery.RecoverStaleTasks(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, staleTasks, 2, "expected 2 stale tasks (pending and running)")

	// Verify the stale tasks were marked as failed.
	for _, task := range staleTasks {
		assert.Equal(t, "failed", task.Status)
		assert.Equal(t, "leader failover: task orphaned", task.Error)
		assert.Equal(t, sessionID, task.SessionID)
	}

	// Verify the completed task was not affected.
	var completedStatus string
	err = pool.QueryRow(ctx,
		"SELECT status FROM task_results_1024 WHERE id = $1",
		"task-completed-1",
	).Scan(&completedStatus)
	require.NoError(t, err)
	assert.Equal(t, "completed", completedStatus, "completed task should not be affected by recovery")
}

// TestStaleTaskRecoveryEmptySession verifies that empty session ID returns an error.
func TestStaleTaskRecoveryEmptySession(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Logf("failed to close pool: %v", err)
		}
	}()

	recovery := leader.NewTaskRecovery(pool)
	_, err := recovery.RecoverStaleTasks(context.Background(), "")
	require.Error(t, err, "expected error for empty session ID")
}

// TestStaleTaskRecoveryNoStaleTasks verifies that recovery returns empty list
// when there are no stale tasks.
func TestStaleTaskRecoveryNoStaleTasks(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Logf("failed to close pool: %v", err)
		}
	}()

	runMigrations(t, pool)

	recovery := leader.NewTaskRecovery(pool)
	staleTasks, err := recovery.RecoverStaleTasks(context.Background(), "non-existent-session")
	require.NoError(t, err)
	assert.Empty(t, staleTasks, "expected no stale tasks for non-existent session")
}

// TestLeaderCheckpointMultipleLeaders verifies that checkpoints are isolated
// between different leaders.
func TestLeaderCheckpointMultipleLeaders(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "leader_checkpoints")
	})

	ctx := context.Background()
	repo := leader.NewCheckpointRepository(pool)
	require.NotNil(t, repo)

	leader1 := fmt.Sprintf("leader-1-%d", time.Now().UnixNano())
	leader2 := fmt.Sprintf("leader-2-%d", time.Now().UnixNano())

	// Save checkpoints for two different leaders.
	require.NoError(t, createTestLeaderCheckpoint(ctx, pool, leader1, "session-L1", "active"))
	require.NoError(t, createTestLeaderCheckpoint(ctx, pool, leader2, "session-L2", "active"))

	// Verify each leader has its own checkpoint.
	cp1, err := repo.GetLatest(ctx, leader1)
	require.NoError(t, err)
	require.NotNil(t, cp1)
	assert.Equal(t, "session-L1", cp1.SessionID)

	cp2, err := repo.GetLatest(ctx, leader2)
	require.NoError(t, err)
	require.NotNil(t, cp2)
	assert.Equal(t, "session-L2", cp2.SessionID)

	// Delete leader1's checkpoint.
	require.NoError(t, repo.Delete(ctx, leader1))

	// Leader1's checkpoint should be gone.
	cp1, err = repo.GetLatest(ctx, leader1)
	require.NoError(t, err)
	assert.Nil(t, cp1)

	// Leader2's checkpoint should still exist.
	cp2, err = repo.GetLatest(ctx, leader2)
	require.NoError(t, err)
	require.NotNil(t, cp2)
	assert.Equal(t, "session-L2", cp2.SessionID)
}

// TestFullFailoverScenario simulates a complete leader failover scenario:
// 1. Leader A processes tasks and saves checkpoints
// 2. Leader A crashes (checkpoint remains active)
// 3. Leader B detects the failure and recovers
// 4. Leader B marks stale tasks as failed
// 5. Leader B takes over with a new session
func TestFullFailoverScenario(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		return
	}
	defer func() { _ = pool.Close() }()

	runMigrations(t, pool)
	t.Cleanup(func() {
		cleanupTables(t, pool, "task_results_1024", "leader_checkpoints", "conversations")
	})

	ctx := context.Background()
	repo := leader.NewCheckpointRepository(pool)
	require.NotNil(t, repo)

	leaderID := fmt.Sprintf("leader-failover-%d", time.Now().UnixNano())

	// Step 1: Leader A processes tasks and saves checkpoints.
	sessionA := fmt.Sprintf("session-a-%d", time.Now().UnixNano())
	require.NoError(t, createTestLeaderCheckpoint(ctx, pool, leaderID, sessionA, "active"))

	// Insert some tasks that Leader A was processing.
	require.NoError(t, insertStaleTask(ctx, pool, "task-1", sessionA, "pending"))
	require.NoError(t, insertStaleTask(ctx, pool, "task-2", sessionA, "running"))
	require.NoError(t, insertStaleTask(ctx, pool, "task-3", sessionA, "completed"))

	// Step 2: Leader A crashes. The checkpoint remains active.

	// Step 3: Leader B detects the failure and recovers the checkpoint.
	cp, err := repo.GetLatest(ctx, leaderID)
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, sessionA, cp.SessionID)
	assert.Equal(t, "active", cp.Status)

	// Step 4: Leader B marks stale tasks as failed.
	recovery := leader.NewTaskRecovery(pool)
	staleTasks, err := recovery.RecoverStaleTasks(ctx, sessionA)
	require.NoError(t, err)
	assert.Len(t, staleTasks, 2, "expected 2 stale tasks (pending and running)")

	// Step 5: Leader B takes over with a new session.
	sessionB := fmt.Sprintf("session-b-%d", time.Now().UnixNano())
	require.NoError(t, createTestLeaderCheckpoint(ctx, pool, leaderID, sessionB, "active"))

	// Verify Leader B's checkpoint is now the latest.
	cp2, err := repo.GetLatest(ctx, leaderID)
	require.NoError(t, err)
	require.NotNil(t, cp2)
	assert.Equal(t, sessionB, cp2.SessionID)
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
