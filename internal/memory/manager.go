// Package memory provides unified memory management for the StyleAgent framework.
// It coordinates session memory, task memory, and distilled task storage through a single interface.
package memory

import (
	"context"
	"time"

	"goagentx/internal/core/models"
)

// MemoryManager provides unified memory management.
// It coordinates session memory, task memory, and distilled task storage.
type MemoryManager interface {
	// CreateSession creates a new session and returns the session ID.
	CreateSession(ctx context.Context, userID string) (string, error)

	// AddMessage adds a message to the session.
	AddMessage(ctx context.Context, sessionID, role, content string) error

	// GetMessages retrieves all messages from the session.
	GetMessages(ctx context.Context, sessionID string) ([]Message, error)

	// DeleteSession deletes a session and all its messages immediately.
	// This is different from TTL-based cleanup, which waits for expiration.
	DeleteSession(ctx context.Context, sessionID string) error

	// BuildContext builds input with conversation history context.
	BuildContext(ctx context.Context, input string, sessionID string) (string, error)

	// CreateTask creates a new task and returns the task ID.
	CreateTask(ctx context.Context, sessionID, userID, input string) (string, error)

	// UpdateTaskOutput updates the task output.
	UpdateTaskOutput(ctx context.Context, taskID, output string) error

	// DistillTask extracts key information from task for future reference.
	DistillTask(ctx context.Context, taskID string) (*models.Task, error)

	// StoreDistilledTask stores a distilled task with local vector embedding.
	// The vector is generated locally using simple hash-based algorithms.
	StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error

	// SearchSimilarTasks searches for similar tasks using local cosine similarity.
	SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error)

	// GetLatestSessionForLeader retrieves the most recent session ID for a leader from checkpoint.
	// Returns ("", nil) if no checkpoint exists.
	GetLatestSessionForLeader(ctx context.Context, leaderID string) (string, error)

	// Start starts the memory manager and background workers.
	Start(ctx context.Context) error

	// Stop stops the memory manager and cleans up resources.
	Stop(ctx context.Context) error
}

// MemoryConfig holds configuration for MemoryManager.
type MemoryConfig struct {
	// Enabled enables memory features.
	Enabled bool

	// Storage type: "memory" or "postgres".
	Storage string

	// MaxHistory is the maximum number of turns to keep in context.
	MaxHistory int

	// MaxSessions is the maximum number of sessions to store.
	MaxSessions int

	// MaxTasks is the maximum number of tasks to store.
	MaxTasks int

	// MaxDistilledTasks is the maximum number of distilled tasks to store.
	// Implements LRU eviction when limit is reached.
	MaxDistilledTasks int

	// SessionTTL is the time-to-live for sessions.
	SessionTTL time.Duration

	// TaskTTL is time-to-live for tasks.
	TaskTTL time.Duration

	// DistilledTaskTTL is time-to-live for distilled tasks.
	DistilledTaskTTL time.Duration

	// VectorDim is the dimension of the vector (for local embedding).
	VectorDim int

	// EnablePostgres enables PostgreSQL storage.
	EnablePostgres bool

	// PostgresDSN is the PostgreSQL connection string.
	PostgresDSN string
}

// Message represents a chat message.
type Message struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}

// DefaultMemoryConfig returns default configuration for MemoryManager.
func DefaultMemoryConfig() *MemoryConfig {
	return &MemoryConfig{
		Enabled:           true,
		Storage:           "memory",
		MaxHistory:        10,
		MaxSessions:       100,
		MaxTasks:          1000,
		MaxDistilledTasks: 5000, // Maximum 5000 distilled tasks with LRU eviction
		SessionTTL:        24 * time.Hour,
		TaskTTL:           7 * 24 * time.Hour,
		VectorDim:         128, // 128-dimensional vector for simple hash-based embedding
		EnablePostgres:    false,
	}
}
