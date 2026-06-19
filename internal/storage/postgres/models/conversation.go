// Package models defines data structures for the storage system.
package models

import "time"

// Conversation represents a chat message in a conversation session.
// This stores short-term conversation context without vector embedding.
type Conversation struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"session_id"`
	TenantID  string                 `json:"tenant_id"`
	UserID    string                 `json:"user_id"`
	AgentID   string                 `json:"agent_id"`
	Role      string                 `json:"role"`
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata"`
	ExpiresAt time.Time              `json:"expires_at"`
	CreatedAt time.Time              `json:"created_at"`
}

// TableName returns the table name for this model.
func (c *Conversation) TableName() string {
	return "conversations"
}

// IsExpired checks if the conversation has expired based on TTL.
func (c *Conversation) IsExpired() bool {
	return !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt)
}

// ConversationRole constants.
const (
	RoleSystem     = "system"
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleTool       = "tool"
	RoleToolCall   = "tool_call"
	RoleToolResult = "tool_result"
)
