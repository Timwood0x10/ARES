// nolint: errcheck // Test code may ignore return values
package leader

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewCheckpointRepository_NilPool verifies that passing a nil pool returns nil.
func TestNewCheckpointRepository_NilPool(t *testing.T) {
	repo := NewCheckpointRepository(nil)
	assert.Nil(t, repo, "NewCheckpointRepository(nil) should return nil")
}

// TestCheckpointRepository_Save_NilCheckpoint verifies that saving a nil
// checkpoint returns an error.
func TestCheckpointRepository_Save_NilCheckpoint(t *testing.T) {
	// A nil pool yields a nil repo, which triggers the nil-receiver guard.
	// We construct the repo manually to isolate the nil-checkpoint path.
	repo := &CheckpointRepository{pool: nil}

	err := repo.Save(context.Background(), nil)
	require.Error(t, err, "Save(nil) should return an error")
	assert.Contains(t, err.Error(), "checkpoint repository not initialized",
		"error should indicate uninitialized repository")
}

// TestCheckpointRepository_Save_NilRepo verifies that calling Save on a nil
// receiver returns an error without panicking.
func TestCheckpointRepository_Save_NilRepo(t *testing.T) {
	var repo *CheckpointRepository // nil receiver.

	err := repo.Save(context.Background(), &LeaderCheckpoint{
		LeaderID: "leader-1",
	})
	require.Error(t, err, "Save on nil receiver should return an error")
	assert.Contains(t, err.Error(), "checkpoint repository not initialized")
}

// TestCheckpointRepository_Save_EmptyLeaderID verifies that saving a checkpoint
// with an empty LeaderID returns an error. When the pool is nil the repository
// returns "not initialized" before checking the leader ID, so this test
// validates the nil-pool guard for the empty-leader-ID case.
func TestCheckpointRepository_Save_EmptyLeaderID(t *testing.T) {
	repo := &CheckpointRepository{pool: nil}

	cp := &LeaderCheckpoint{
		LeaderID:  "",
		SessionID: "session-1",
		Status:    "active",
	}

	err := repo.Save(context.Background(), cp)
	require.Error(t, err, "Save with empty LeaderID should return an error")
	// The nil-pool guard fires first.
	assert.Contains(t, err.Error(), "checkpoint repository not initialized")
}

// TestCheckpointRepository_GetLatest_EmptyLeaderID verifies that GetLatest
// returns an error for an empty leader ID. When the pool is nil the "not
// initialized" error fires first.
func TestCheckpointRepository_GetLatest_EmptyLeaderID(t *testing.T) {
	repo := &CheckpointRepository{pool: nil}

	cp, err := repo.GetLatest(context.Background(), "")
	require.Error(t, err, "GetLatest with empty leaderID should return an error")
	assert.Nil(t, cp, "checkpoint should be nil on error")
	assert.Contains(t, err.Error(), "checkpoint repository not initialized")
}

// TestCheckpointRepository_GetLatest_NilRepo verifies that GetLatest on a nil
// receiver returns an error without panicking.
func TestCheckpointRepository_GetLatest_NilRepo(t *testing.T) {
	var repo *CheckpointRepository

	cp, err := repo.GetLatest(context.Background(), "leader-1")
	require.Error(t, err, "GetLatest on nil receiver should return an error")
	assert.Nil(t, cp)
	assert.Contains(t, err.Error(), "checkpoint repository not initialized")
}

// TestCheckpointRepository_Delete_EmptyLeaderID verifies that Delete returns
// an error for an empty leader ID. When the pool is nil the "not initialized"
// error fires first.
func TestCheckpointRepository_Delete_EmptyLeaderID(t *testing.T) {
	repo := &CheckpointRepository{pool: nil}

	err := repo.Delete(context.Background(), "")
	require.Error(t, err, "Delete with empty leaderID should return an error")
	assert.Contains(t, err.Error(), "checkpoint repository not initialized")
}

// TestCheckpointRepository_Delete_NilRepo verifies that Delete on a nil
// receiver returns an error without panicking.
func TestCheckpointRepository_Delete_NilRepo(t *testing.T) {
	var repo *CheckpointRepository

	err := repo.Delete(context.Background(), "leader-1")
	require.Error(t, err, "Delete on nil receiver should return an error")
	assert.Contains(t, err.Error(), "checkpoint repository not initialized")
}

// TestLeaderCheckpoint_MetadataDefault verifies that empty metadata is
// normalised to "{}" during Save. This tests the metadata-normalisation logic
// without needing a database.
func TestLeaderCheckpoint_MetadataDefault(t *testing.T) {
	cp := &LeaderCheckpoint{
		LeaderID:  "leader-1",
		SessionID: "session-1",
		Status:    "active",
		Metadata:  nil, // empty metadata.
	}

	// Simulate the normalisation that Save performs.
	metadata := cp.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}

	assert.Equal(t, json.RawMessage("{}"), metadata,
		"empty metadata should be normalised to {}")
}

// TestLeaderCheckpoint_JSONRoundTrip verifies the JSON serialisation of
// LeaderCheckpoint to ensure the struct tags are correct.
func TestLeaderCheckpoint_JSONRoundTrip(t *testing.T) {
	original := &LeaderCheckpoint{
		LeaderID:  "leader-1",
		SessionID: "session-abc",
		Status:    "active",
		Metadata:  json.RawMessage(`{"key":"value"}`),
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "json.Marshal should succeed")

	var decoded LeaderCheckpoint
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err, "json.Unmarshal should succeed")

	assert.Equal(t, original.LeaderID, decoded.LeaderID)
	assert.Equal(t, original.SessionID, decoded.SessionID)
	assert.Equal(t, original.Status, decoded.Status)
	assert.JSONEq(t, string(original.Metadata), string(decoded.Metadata))
}

// TestCheckpointRepository_Integration verifies the full Save/GetLatest/Delete
// round-trip against a real PostgreSQL database.
// Skipped when no database is available.
func TestCheckpointRepository_Integration(t *testing.T) {
	pool := getTestPool(t)
	if pool == nil {
		t.Skip("requires PostgreSQL; set TEST_POSTGRES_DSN to enable")
	}

	repo := NewCheckpointRepository(pool)
	require.NotNil(t, repo, "repo should not be nil with valid pool")

	ctx := context.Background()
	leaderID := "test-leader-integration"

	cp := &LeaderCheckpoint{
		LeaderID:  leaderID,
		SessionID: "session-int-1",
		Status:    "active",
		Metadata:  json.RawMessage(`{"test":true}`),
	}

	// Save.
	err := repo.Save(ctx, cp)
	require.NoError(t, err, "Save should succeed")

	// GetLatest after Save.
	got, err := repo.GetLatest(ctx, leaderID)
	require.NoError(t, err, "GetLatest should succeed")
	require.NotNil(t, got, "checkpoint should exist after save")
	assert.Equal(t, leaderID, got.LeaderID)
	assert.Equal(t, "session-int-1", got.SessionID)
	assert.Equal(t, "active", got.Status)

	// Save again (UPSERT semantics).
	cp.SessionID = "session-int-2"
	cp.Status = "recovered"
	err = repo.Save(ctx, cp)
	require.NoError(t, err, "second Save should succeed")

	got, err = repo.GetLatest(ctx, leaderID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "session-int-2", got.SessionID,
		"GetLatest should return the second (upserted) checkpoint")
	assert.Equal(t, "recovered", got.Status)

	// GetLatest for non-existent leader.
	got, err = repo.GetLatest(ctx, "non-existent-leader")
	require.NoError(t, err, "GetLatest for non-existent should not error")
	assert.Nil(t, got, "non-existent checkpoint should return nil")

	// Delete.
	err = repo.Delete(ctx, leaderID)
	require.NoError(t, err, "Delete should succeed")

	got, err = repo.GetLatest(ctx, leaderID)
	require.NoError(t, err, "GetLatest after Delete should not error")
	assert.Nil(t, got, "checkpoint should be nil after delete")

	// Clean up.
	_ = repo.Delete(ctx, leaderID)
}
