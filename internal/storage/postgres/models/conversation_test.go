// Package models provides comprehensive tests for conversation model.
package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestConversation_TableName tests table name returns correct value.
func TestConversation_TableName(t *testing.T) {
	conv := &Conversation{}
	assert.Equal(t, "conversations", conv.TableName())
}

// TestConversation_ValidFields tests valid field assignment.
func TestConversation_ValidFields(t *testing.T) {
	conv := &Conversation{
		ID:        "conv-id",
		TenantID:  "tenant-1",
		SessionID: "session-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      RoleUser,
		Content:   "test content",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	assert.Equal(t, "conv-id", conv.ID)
	assert.Equal(t, "tenant-1", conv.TenantID)
	assert.Equal(t, "session-1", conv.SessionID)
	assert.Equal(t, "user-1", conv.UserID)
	assert.Equal(t, "agent-1", conv.AgentID)
	assert.Equal(t, RoleUser, conv.Role)
	assert.Equal(t, "test content", conv.Content)
	assert.False(t, conv.IsExpired())
}

// TestConversation_EmptyFields tests handling of empty fields.
func TestConversation_EmptyFields(t *testing.T) {
	conv := &Conversation{}

	assert.Empty(t, conv.ID)
	assert.Empty(t, conv.TenantID)
	assert.Empty(t, conv.SessionID)
	assert.Empty(t, conv.UserID)
	assert.Empty(t, conv.AgentID)
	assert.Empty(t, conv.Role)
	assert.Empty(t, conv.Content)
	assert.True(t, conv.ExpiresAt.IsZero())
	assert.True(t, conv.CreatedAt.IsZero())
}

// TestConversation_RoleValues tests different role values.
func TestConversation_RoleValues(t *testing.T) {
	tests := []struct {
		name string
		role string
	}{
		{"user role", RoleUser},
		{"assistant role", RoleAssistant},
		{"system role", RoleSystem},
		{"tool role", RoleTool},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conv := &Conversation{
				ID:        "conv-id",
				Role:      tt.role,
				Content:   "test content",
				CreatedAt: time.Now(),
			}

			assert.Equal(t, tt.role, conv.Role)
		})
	}
}

// TestConversation_IsExpired tests expiration logic.
func TestConversation_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		expected  bool
	}{
		{
			name:      "expired conversation",
			expiresAt: time.Now().Add(-1 * time.Hour),
			expected:  true,
		},
		{
			name:      "not expired conversation",
			expiresAt: time.Now().Add(1 * time.Hour),
			expected:  false,
		},
		{
			name:      "zero expires time",
			expiresAt: time.Time{},
			expected:  false,
		},
		{
			name:      "exactly expired",
			expiresAt: time.Now(),
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conv := &Conversation{
				ID:        "conv-id",
				ExpiresAt: tt.expiresAt,
				CreatedAt: time.Now(),
			}

			assert.Equal(t, tt.expected, conv.IsExpired())
		})
	}
}
