// Package core provides core abstractions for memory operations.
package core

import (
	"context"
	"time"
)

// MessageRole represents the role of a message sender.
type MessageRole string

const (
	// MessageRoleSystem represents a system message.
	MessageRoleSystem MessageRole = "system"
	// MessageRoleUser represents a user message.
	MessageRoleUser MessageRole = "user"
	// MessageRoleAssistant represents an assistant message.
	MessageRoleAssistant MessageRole = "assistant"
	// MessageRoleTool represents a tool/function call message.
	MessageRoleTool MessageRole = "tool"
)

// Message represents a conversation message.
type Message struct {
	// ID is the unique identifier for the message.
	ID string
	// SessionID is the session this message belongs to.
	SessionID string
	// Role is the role of the message sender.
	Role MessageRole
	// Content is the message content.
	Content string
	// Time is the timestamp when the message was created.
	Time time.Time
	// Metadata is optional metadata.
	Metadata Metadata

	// TurnID identifies the ReAct turn this message belongs to for causal tracing.
	TurnID string
	// EventKind categorises the message for event-sourcing reconstruction.
	EventKind string
	// ParentID references the tool_call_id or message this is a response to.
	ParentID string
	// ArtifactRefs lists external artifact references produced by this message.
	ArtifactRefs []string
}

// Session represents a conversation session.
type Session struct {
	// ID is the unique identifier for the session.
	ID string
	// UserID is the user this session belongs to.
	UserID string
	// TenantID is the tenant this session belongs to.
	TenantID string
	// Status is the session status.
	Status string
	// CreatedAt is the timestamp when the session was created.
	CreatedAt time.Time
	// UpdatedAt is the timestamp when the session was last updated.
	UpdatedAt time.Time
	// ExpiresAt is the timestamp when the session expires.
	ExpiresAt *time.Time
	// Metadata is optional metadata.
	Metadata Metadata
}

// SessionConfig represents configuration for creating a session.
type SessionConfig struct {
	// UserID is the user identifier.
	UserID string
	// TenantID is the tenant identifier.
	TenantID string
	// ExpiresIn is the session expiration duration.
	ExpiresIn time.Duration
	// Metadata is optional metadata.
	Metadata Metadata
}

// DistilledTask represents a distilled task with extracted key information.
type DistilledTask struct {
	// TaskID is the unique identifier for the task.
	TaskID string
	// Input is the original input.
	Input string
	// Output is the generated output.
	Output string
	// Context is the context information.
	Context string
	// Summary is the task summary.
	Summary string
	// Tags are tags associated with the task.
	Tags []string
	// Embedding is the vector embedding of the task.
	Embedding []float32
	// CreatedAt is the timestamp when the task was distilled.
	CreatedAt time.Time
}

// SearchQuery represents a search query for similar tasks.
type SearchQuery struct {
	// Query is the search query text.
	Query string
	// Limit is the maximum number of results to return.
	Limit int
	// MinScore is the minimum similarity score.
	MinScore float64
	// Tags filters by tags.
	Tags []string
}

// SearchResult represents a search result.
type SearchResult struct {
	// TaskID is the task identifier.
	TaskID string
	// Input is the original input.
	Input string
	// Output is the generated output.
	Output string
	// Context is the context information.
	Context string
	// Summary is the task summary.
	Summary string
	// Score is the similarity score.
	Score float64
	// Tags are tags associated with the task.
	Tags []string
}

// MemoryRepository defines the interface for memory data access operations.
type MemoryRepository interface {
	// CreateSession creates a new session.
	// Args:
	// ctx - operation context.
	// session - the session to create.
	// Returns error if creation fails.
	CreateSession(ctx context.Context, session *Session) error

	// GetSession retrieves a session by ID.
	// Args:
	// ctx - operation context.
	// sessionID - the session identifier.
	// Returns the session or error if not found.
	GetSession(ctx context.Context, sessionID string) (*Session, error)

	// UpdateSession updates an existing session.
	// Args:
	// ctx - operation context.
	// session - the session to update.
	// Returns error if update fails.
	UpdateSession(ctx context.Context, session *Session) error

	// DeleteSession deletes a session and all its messages.
	// Args:
	// ctx - operation context.
	// sessionID - the session identifier.
	// Returns error if deletion fails.
	DeleteSession(ctx context.Context, sessionID string) error

	// AddMessage adds a message to a session.
	// Args:
	// ctx - operation context.
	// message - the message to add.
	// Returns error if addition fails.
	AddMessage(ctx context.Context, message *Message) error

	// GetMessages retrieves messages from a session.
	// Args:
	// ctx - operation context.
	// sessionID - the session identifier.
	// pagination - pagination parameters.
	// Returns list of messages or error.
	GetMessages(ctx context.Context, sessionID string, pagination *PaginationRequest) ([]*Message, error)

	// StoreDistilledTask stores a distilled task.
	// Args:
	// ctx - operation context.
	// task - the distilled task to store.
	// Returns error if storage fails.
	StoreDistilledTask(ctx context.Context, task *DistilledTask) error

	// GetDistilledTask retrieves a distilled task by ID.
	// Args:
	// ctx - operation context.
	// taskID - the task identifier.
	// Returns the distilled task or error if not found.
	GetDistilledTask(ctx context.Context, taskID string) (*DistilledTask, error)

	// SearchSimilarTasks searches for similar tasks.
	// Args:
	// ctx - operation context.
	// query - the search query.
	// Returns list of search results or error.
	SearchSimilarTasks(ctx context.Context, query *SearchQuery) ([]*SearchResult, error)
}

// MemoryService defines the interface for memory business logic operations.
type MemoryService interface {
	// CreateSession creates a new session with the given configuration.
	// Args:
	// ctx - operation context.
	// config - the session configuration.
	// Returns the session ID or error.
	CreateSession(ctx context.Context, config *SessionConfig) (string, error)

	// GetSession retrieves a session by ID.
	// Args:
	// ctx - operation context.
	// sessionID - the session identifier.
	// Returns the session or error if not found.
	GetSession(ctx context.Context, sessionID string) (*Session, error)

	// UpdateSession updates an existing session.
	// Args:
	// ctx - operation context.
	// session - the session to update.
	// Returns error if update fails.
	UpdateSession(ctx context.Context, session *Session) error

	// DeleteSession deletes a session and all its messages.
	// Args:
	// ctx - operation context.
	// sessionID - the session identifier.
	// Returns error if deletion fails.
	DeleteSession(ctx context.Context, sessionID string) error

	// AddMessage adds a message to a session.
	// Args:
	// ctx - operation context.
	// sessionID - the session identifier.
	// role - the message role.
	// content - the message content.
	// Returns error if addition fails.
	AddMessage(ctx context.Context, sessionID string, role MessageRole, content string) error

	// GetMessages retrieves messages from a session.
	// Args:
	// ctx - operation context.
	// sessionID - the session identifier.
	// pagination - pagination parameters.
	// Returns list of messages or error.
	GetMessages(ctx context.Context, sessionID string, pagination *PaginationRequest) ([]*Message, error)

	// DistillTask distills a task for future reference.
	// Args:
	// ctx - operation context.
	// taskID - the task identifier.
	// Returns the distilled task or error.
	DistillTask(ctx context.Context, taskID string) (*DistilledTask, error)

	// SearchSimilarTasks searches for similar tasks.
	// Args:
	// ctx - operation context.
	// query - the search query.
	// Returns list of search results or error.
	SearchSimilarTasks(ctx context.Context, query *SearchQuery) ([]*SearchResult, error)
}
