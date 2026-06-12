// Package repositories provides comprehensive tests for ConversationRepository.
package repositories

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/core/errors"
	storage_models "goagentx/internal/storage/postgres/models"
)

// TestConversationRepository_Create tests creating a conversation message.
func TestConversationRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	conv := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Hello, how are you?",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	err := repo.Create(ctx, conv)
	require.NoError(t, err)
	assert.NotEmpty(t, conv.ID)
}

// TestConversationRepository_Create_WithID tests creating a conversation with specified ID.
func TestConversationRepository_Create_WithID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	conv := &storage_models.Conversation{
		ID:        "123e4567-e89b-12d3-a456-426614174000",
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Test message",
		CreatedAt: time.Now(),
	}

	err := repo.Create(ctx, conv)
	require.NoError(t, err)
	assert.Equal(t, "123e4567-e89b-12d3-a456-426614174000", conv.ID)
}

// TestConversationRepository_GetByID tests retrieving a conversation by ID.
func TestConversationRepository_GetByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create a conversation
	conv := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Test message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv)
	require.NoError(t, err)

	// Retrieve by ID
	retrieved, err := repo.GetByID(ctx, conv.ID)
	require.NoError(t, err)
	assert.Equal(t, conv.ID, retrieved.ID)
	assert.Equal(t, conv.SessionID, retrieved.SessionID)
	assert.Equal(t, conv.TenantID, retrieved.TenantID)
	assert.Equal(t, conv.UserID, retrieved.UserID)
	assert.Equal(t, conv.AgentID, retrieved.AgentID)
	assert.Equal(t, conv.Role, retrieved.Role)
	assert.Equal(t, conv.Content, retrieved.Content)
}

// TestConversationRepository_GetByID_NotFound tests retrieving a non-existent conversation.
func TestConversationRepository_GetByID_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestConversationRepository_GetByID_InvalidID tests retrieving with invalid ID.
func TestConversationRepository_GetByID_InvalidID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrInvalidArgument, err)
}

// TestConversationRepository_GetBySession tests retrieving conversations by session.
func TestConversationRepository_GetBySession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create multiple conversations in the same session
	conversations := []*storage_models.Conversation{
		{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "First message",
			CreatedAt: time.Now().Add(-2 * time.Minute),
		},
		{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "assistant",
			Content:   "Second message",
			CreatedAt: time.Now().Add(-1 * time.Minute),
		},
		{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "Third message",
			CreatedAt: time.Now(),
		},
	}

	for _, conv := range conversations {
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Retrieve by session
	results, err := repo.GetBySession(ctx, "session-1", "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Verify ordering (ascending by created_at)
	assert.Equal(t, "First message", results[0].Content)
	assert.Equal(t, "Second message", results[1].Content)
	assert.Equal(t, "Third message", results[2].Content)
}

// TestConversationRepository_GetBySession_TenantIsolation tests tenant isolation.
func TestConversationRepository_GetBySession_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation for tenant-1
	conv1 := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Tenant 1 message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv1)
	require.NoError(t, err)

	// Try to retrieve with different tenant
	results, err := repo.GetBySession(ctx, "session-1", "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestConversationRepository_GetBySession_Limit tests limit parameter.
func TestConversationRepository_GetBySession_Limit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create 5 conversations
	for i := 0; i < 5; i++ {
		conv := &storage_models.Conversation{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Retrieve with limit
	results, err := repo.GetBySession(ctx, "session-1", "tenant-1", 3)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

// TestConversationRepository_Delete tests deleting a conversation.
func TestConversationRepository_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create a conversation
	conv := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Test message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv)
	require.NoError(t, err)

	// Delete the conversation
	err = repo.Delete(ctx, conv.ID)
	require.NoError(t, err)

	// Verify it's deleted
	_, err = repo.GetByID(ctx, conv.ID)
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestConversationRepository_Delete_NotFound tests deleting a non-existent conversation.
func TestConversationRepository_Delete_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	err := repo.Delete(ctx, "00000000-0000-0000-0000-000000000000")
	assert.Error(t, err)
	assert.Equal(t, errors.ErrRecordNotFound, err)
}

// TestConversationRepository_DeleteBySession tests deleting all conversations in a session.
func TestConversationRepository_DeleteBySession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create multiple conversations in the same session
	for i := 0; i < 3; i++ {
		conv := &storage_models.Conversation{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Delete all conversations in the session
	deleted, err := repo.DeleteBySession(ctx, "session-1", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted)

	// Verify all are deleted
	results, err := repo.GetBySession(ctx, "session-1", "tenant-1", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestConversationRepository_GetByUser tests retrieving conversations by user.
func TestConversationRepository_GetByUser(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversations for the same user
	for i := 0; i < 3; i++ {
		conv := &storage_models.Conversation{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Retrieve by user
	results, err := repo.GetByUser(ctx, "user-1", "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

// TestConversationRepository_GetByUser_TenantIsolation tests tenant isolation.
func TestConversationRepository_GetByUser_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation for tenant-1
	conv1 := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Tenant 1 message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv1)
	require.NoError(t, err)

	// Try to retrieve with different tenant
	results, err := repo.GetByUser(ctx, "user-1", "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestConversationRepository_GetByAgent tests retrieving conversations by agent.
func TestConversationRepository_GetByAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversations for the same agent
	for i := 0; i < 3; i++ {
		conv := &storage_models.Conversation{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "assistant",
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Retrieve by agent
	results, err := repo.GetByAgent(ctx, "agent-1", "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

// TestConversationRepository_GetByAgent_TenantIsolation tests tenant isolation.
func TestConversationRepository_GetByAgent_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation for tenant-1
	conv1 := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "assistant",
		Content:   "Tenant 1 message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv1)
	require.NoError(t, err)

	// Try to retrieve with different tenant
	results, err := repo.GetByAgent(ctx, "agent-1", "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestConversationRepository_CleanupExpired tests cleanup of expired conversations.
func TestConversationRepository_CleanupExpired(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create expired conversation - use UTC time to avoid timezone issues
	expiredTime := time.Now().UTC().Add(-1 * time.Hour)
	expiredConv := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Expired message",
		ExpiresAt: expiredTime,
		CreatedAt: time.Now().UTC(),
	}
	err := repo.Create(ctx, expiredConv)
	require.NoError(t, err)

	// Create non-expired conversation
	nonExpiredConv := &storage_models.Conversation{
		SessionID: "session-2",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Non-expired message",
		ExpiresAt: time.Now().UTC().Add(1 * time.Hour),
		CreatedAt: time.Now().UTC(),
	}
	err = repo.Create(ctx, nonExpiredConv)
	require.NoError(t, err)

	// Cleanup expired conversations
	deleted, err := repo.CleanupExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// Verify expired conversation is deleted
	_, err = repo.GetByID(ctx, expiredConv.ID)
	assert.Error(t, err)

	// Verify non-expired conversation still exists
	retrieved, err := repo.GetByID(ctx, nonExpiredConv.ID)
	require.NoError(t, err)
	assert.Equal(t, nonExpiredConv.ID, retrieved.ID)
}

// TestConversationRepository_CleanupExpired_NoExpiration tests cleanup when no expiration set.
func TestConversationRepository_CleanupExpired_NoExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation with future expiration (not expired)
	conv := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Test message",
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour), // Set future expiration
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv)
	require.NoError(t, err)

	// Cleanup expired conversations - should not delete non-expired conversation
	deleted, err := repo.CleanupExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)

	// Verify conversation still exists
	retrieved, err := repo.GetByID(ctx, conv.ID)
	require.NoError(t, err)
	assert.Equal(t, conv.ID, retrieved.ID)
}

// TestConversationRepository_UpdateExpiresAt tests updating expiration time.
func TestConversationRepository_UpdateExpiresAt(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversations in the same session
	for i := 0; i < 3; i++ {
		conv := &storage_models.Conversation{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Update expiration time (use UTC to match database timezone)
	newExpiresAt := time.Now().UTC().Add(24 * time.Hour)
	updated, err := repo.UpdateExpiresAt(ctx, "session-1", "tenant-1", newExpiresAt)
	require.NoError(t, err)
	assert.Equal(t, int64(3), updated)

	// Verify expiration was updated
	results, err := repo.GetBySession(ctx, "session-1", "tenant-1", 10)
	require.NoError(t, err)
	for _, result := range results {
		assert.WithinDuration(t, newExpiresAt, result.ExpiresAt, time.Second)
	}
}

// TestConversationRepository_UpdateExpiresAt_TenantIsolation tests tenant isolation.
func TestConversationRepository_UpdateExpiresAt_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation for tenant-1
	conv1 := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Tenant 1 message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv1)
	require.NoError(t, err)

	// Create conversation for tenant-2 with same session ID
	conv2 := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-2",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Tenant 2 message",
		CreatedAt: time.Now(),
	}
	err = repo.Create(ctx, conv2)
	require.NoError(t, err)

	// Update expiration for tenant-1 only (use UTC time)
	newExpiresAt := time.Now().UTC().Add(24 * time.Hour)
	updated, err := repo.UpdateExpiresAt(ctx, "session-1", "tenant-1", newExpiresAt)
	require.NoError(t, err)
	assert.Equal(t, int64(1), updated)

	// Verify tenant-1 conversation was updated
	retrieved1, err := repo.GetByID(ctx, conv1.ID)
	require.NoError(t, err)
	assert.WithinDuration(t, newExpiresAt, retrieved1.ExpiresAt, time.Second)

	// Verify tenant-2 conversation was not updated
	retrieved2, err := repo.GetByID(ctx, conv2.ID)
	require.NoError(t, err)
	assert.True(t, retrieved2.ExpiresAt.IsZero())
}

// TestConversationRepository_CountBySession tests counting messages in a session.
func TestConversationRepository_CountBySession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create 5 conversations in the same session
	for i := 0; i < 5; i++ {
		conv := &storage_models.Conversation{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Count messages
	count, err := repo.CountBySession(ctx, "session-1", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, int64(5), count)
}

// TestConversationRepository_CountBySession_Empty tests counting empty session.
func TestConversationRepository_CountBySession_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Count messages in empty session
	count, err := repo.CountBySession(ctx, "non-existent-session", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

// TestConversationRepository_CountBySession_TenantIsolation tests tenant isolation.
func TestConversationRepository_CountBySession_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation for tenant-1
	conv1 := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Tenant 1 message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv1)
	require.NoError(t, err)

	// Count for tenant-1
	count1, err := repo.CountBySession(ctx, "session-1", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count1)

	// Count for tenant-2
	count2, err := repo.CountBySession(ctx, "session-1", "tenant-2")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count2)
}

// TestConversationRepository_GetRecentSessions tests retrieving recent sessions.
func TestConversationRepository_GetRecentSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversations in different sessions
	sessions := []string{"session-1", "session-2", "session-3"}
	for _, sessionID := range sessions {
		conv := &storage_models.Conversation{
			SessionID: sessionID,
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Get recent sessions
	recentSessions, err := repo.GetRecentSessions(ctx, "tenant-1", 10)
	require.NoError(t, err)
	assert.Len(t, recentSessions, 3)

	// Verify all sessions are included
	for _, sessionID := range sessions {
		assert.Contains(t, recentSessions, sessionID)
	}
}

// TestConversationRepository_GetRecentSessions_Limit tests limit parameter.
func TestConversationRepository_GetRecentSessions_Limit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversations in 5 different sessions
	for i := 0; i < 5; i++ {
		conv := &storage_models.Conversation{
			SessionID: fmt.Sprintf("session-%d", i+1),
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      "user",
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)
	}

	// Get recent sessions with limit
	recentSessions, err := repo.GetRecentSessions(ctx, "tenant-1", 3)
	require.NoError(t, err)
	assert.Len(t, recentSessions, 3)
}

// TestConversationRepository_GetRecentSessions_TenantIsolation tests tenant isolation.
func TestConversationRepository_GetRecentSessions_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation for tenant-1
	conv1 := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "Tenant 1 message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv1)
	require.NoError(t, err)

	// Get recent sessions for tenant-2
	recentSessions, err := repo.GetRecentSessions(ctx, "tenant-2", 10)
	require.NoError(t, err)
	assert.Empty(t, recentSessions)
}

// TestConversationRepository_RoleHandling tests handling different roles.
func TestConversationRepository_RoleHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	roles := []string{"user", "assistant", "system"}
	for _, role := range roles {
		conv := &storage_models.Conversation{
			SessionID: "session-1",
			TenantID:  "tenant-1",
			UserID:    "user-1",
			AgentID:   "agent-1",
			Role:      role,
			Content:   "Test message",
			CreatedAt: time.Now(),
		}
		err := repo.Create(ctx, conv)
		require.NoError(t, err)

		// Retrieve and verify role
		retrieved, err := repo.GetByID(ctx, conv.ID)
		require.NoError(t, err)
		assert.Equal(t, role, retrieved.Role)
	}
}

// TestConversationRepository_ConcurrentOperations tests concurrent operations.
func TestConversationRepository_ConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	done := make(chan bool, 10)

	// Create 10 conversations concurrently
	for i := 0; i < 10; i++ {
		go func(index int) {
			conv := &storage_models.Conversation{
				SessionID: "session-1",
				TenantID:  "tenant-1",
				UserID:    "user-1",
				AgentID:   "agent-1",
				Role:      "user",
				Content:   "Test message",
				CreatedAt: time.Now(),
			}
			err := repo.Create(ctx, conv)
			require.NoError(t, err)
			done <- true
		}(i)
	}

	// Wait for all operations to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all conversations were created
	count, err := repo.CountBySession(ctx, "session-1", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, int64(10), count)
}

// TestConversationRepository_LongContent tests handling long content.
func TestConversationRepository_LongContent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation with very long content
	longContent := "This is a long message. "
	for i := 0; i < 100; i++ {
		longContent += "Repeat. "
	}

	conv := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   longContent,
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv)
	require.NoError(t, err)

	// Retrieve and verify content
	retrieved, err := repo.GetByID(ctx, conv.ID)
	require.NoError(t, err)
	assert.Equal(t, longContent, retrieved.Content)
}

// TestConversationRepository_NullFields tests handling null fields.
func TestConversationRepository_NullFields(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	// Create conversation with optional fields empty
	conv := &storage_models.Conversation{
		SessionID: "session-1",
		TenantID:  "tenant-1",
		UserID:    "", // Empty user ID
		AgentID:   "", // Empty agent ID
		Role:      "system",
		Content:   "System message",
		CreatedAt: time.Now(),
	}
	err := repo.Create(ctx, conv)
	require.NoError(t, err)

	// Retrieve and verify
	retrieved, err := repo.GetByID(ctx, conv.ID)
	require.NoError(t, err)
	assert.Equal(t, "", retrieved.UserID)
	assert.Equal(t, "", retrieved.AgentID)
	assert.Equal(t, "system", retrieved.Role)
}
