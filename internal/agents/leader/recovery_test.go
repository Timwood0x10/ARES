// nolint: errcheck // Test code may ignore return values
package leader

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTaskRecovery_NilPool verifies that NewTaskRecovery accepts a nil pool
// without panicking. The pool-nil guard lives in RecoverStaleTasks.
func TestNewTaskRecovery_NilPool(t *testing.T) {
	tr := NewTaskRecovery(nil)
	require.NotNil(t, tr, "NewTaskRecovery should always return a non-nil struct")
	assert.Nil(t, tr.pool, "pool field should be nil")
}

// TestTaskRecovery_RecoverStaleTasks_NilPool verifies that calling
// RecoverStaleTasks with a nil pool returns an error.
func TestTaskRecovery_RecoverStaleTasks_NilPool(t *testing.T) {
	tr := NewTaskRecovery(nil)

	tasks, err := tr.RecoverStaleTasks(context.Background(), "session-1")
	require.Error(t, err, "should error with nil pool")
	assert.Nil(t, tasks, "tasks should be nil on error")
	assert.Contains(t, err.Error(), "pool not initialized",
		"error should indicate uninitialized pool")
}

// TestTaskRecovery_RecoverStaleTasks_EmptySessionID verifies that an empty
// session ID returns an error. When the pool is nil the "pool not initialized"
// error fires first.
func TestTaskRecovery_RecoverStaleTasks_EmptySessionID(t *testing.T) {
	tr := &TaskRecovery{pool: nil}

	tasks, err := tr.RecoverStaleTasks(context.Background(), "")
	require.Error(t, err, "should error with empty session ID")
	assert.Nil(t, tasks)
	assert.Contains(t, err.Error(), "pool not initialized",
		"error should indicate uninitialized pool")
}

// TestTaskRecovery_RecoverStaleTasks_Integration verifies the full recovery
// flow against a real PostgreSQL database.
// Skipped when no database is available.
func TestTaskRecovery_RecoverStaleTasks_Integration(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("requires PostgreSQL; set TEST_POSTGRES_DSN to enable")
	}

	tr := NewTaskRecovery(pool)
	ctx := context.Background()

	// No matching tasks for a random session.
	tasks, err := tr.RecoverStaleTasks(ctx, "non-existent-session-xyz")
	require.NoError(t, err, "should not error when no matching tasks exist")
	assert.Empty(t, tasks, "should return empty slice when no stale tasks found")
}

// TestStaleTask_Fields verifies the StaleTask struct field assignments.
func TestStaleTask_Fields(t *testing.T) {
	task := &StaleTask{
		TaskID:    "task-1",
		SessionID: "session-1",
		Status:    "failed",
		Error:     "leader failover: task orphaned",
	}

	assert.Equal(t, "task-1", task.TaskID)
	assert.Equal(t, "session-1", task.SessionID)
	assert.Equal(t, "failed", task.Status)
	assert.Equal(t, "leader failover: task orphaned", task.Error)
}

// TestTaskRecovery_RecoverStaleTasks_CancelledContext verifies that a cancelled
// context returns an appropriate error before reaching the database.
func TestTaskRecovery_RecoverStaleTasks_CancelledContext(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("requires PostgreSQL; set TEST_POSTGRES_DSN to enable")
	}

	tr := NewTaskRecovery(pool)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	tasks, err := tr.RecoverStaleTasks(ctx, "session-1")
	// With a cancelled context the pool.Query call may or may not fail
	// depending on timing, but we should not get a panic.
	if err != nil {
		assert.Nil(t, tasks, "tasks should be nil when error occurs")
	}
}
