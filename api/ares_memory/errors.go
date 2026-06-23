// Package memory provides error definitions for memory operations.
package memory

import "errors"

var (
	// ErrInvalidUserID is returned when user ID is empty.
	ErrInvalidUserID = errors.New("invalid user ID")

	// ErrInvalidSessionID is returned when session ID is empty.
	ErrInvalidSessionID = errors.New("invalid session ID")

	// ErrInvalidRole is returned when role is empty.
	ErrInvalidRole = errors.New("invalid role")

	// ErrInvalidContent is returned when content is empty.
	ErrInvalidContent = errors.New("invalid content")

	// ErrInvalidTaskID is returned when task ID is empty.
	ErrInvalidTaskID = errors.New("invalid task ID")

	// ErrInvalidQuery is returned when query is empty.
	ErrInvalidQuery = errors.New("invalid query")

	// ErrInvalidLimit is returned when limit is less than or equal to zero.
	ErrInvalidLimit = errors.New("invalid limit")

	// ErrSessionNotFound is returned when session does not exist.
	ErrSessionNotFound = errors.New("session not found")

	// ErrTaskNotFound is returned when task does not exist.
	ErrTaskNotFound = errors.New("task not found")

	// ErrInvalidConversationID is returned when conversation ID is empty.
	ErrInvalidConversationID = errors.New("invalid conversation ID")

	// ErrNoMessages is returned when no messages are provided for distillation.
	ErrNoMessages = errors.New("no messages provided")

	// ErrInvalidTenantID is returned when tenant ID is empty.
	ErrInvalidTenantID = errors.New("invalid tenant ID")

	// ErrInvalidConfig is returned when configuration is invalid.
	ErrInvalidConfig = errors.New("invalid configuration")

	// ErrDistillationFailed is returned when distillation process fails.
	ErrDistillationFailed = errors.New("distillation failed")

	// ErrEmbeddingFailed is returned when embedding generation fails.
	ErrEmbeddingFailed = errors.New("embedding generation failed")

	// ErrVectorSearchFailed is returned when vector search fails.
	ErrVectorSearchFailed = errors.New("vector search failed")

	// ErrInvalidMemoryID is returned when memory ID is empty.
	ErrInvalidMemoryID = errors.New("invalid memory ID")

	// ErrMemoryNotFound is returned when memory does not exist.
	ErrMemoryNotFound = errors.New("memory not found")

	// ErrMemoryUpdateFailed is returned when memory update fails.
	ErrMemoryUpdateFailed = errors.New("memory update failed")

	// ErrMemoryDeleteFailed is returned when memory deletion fails.
	ErrMemoryDeleteFailed = errors.New("memory deletion failed")
)
