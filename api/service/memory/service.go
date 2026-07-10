// Package memory provides the public API for memory management.
package memory

import (
	"context"
	"time"

	internal "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// Config holds configuration for the memory service.
// This is a public type that wraps the internal MemoryConfig to avoid
// leaking internal package types into the public API.
type Config struct {
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

// toInternal converts the public Config to the internal MemoryConfig type.
func (c *Config) toInternal() *internal.MemoryConfig {
	if c == nil {
		return nil
	}
	return &internal.MemoryConfig{
		Enabled:           c.Enabled,
		Storage:           c.Storage,
		MaxHistory:        c.MaxHistory,
		MaxSessions:       c.MaxSessions,
		MaxTasks:          c.MaxTasks,
		MaxDistilledTasks: c.MaxDistilledTasks,
		SessionTTL:        c.SessionTTL,
		TaskTTL:           c.TaskTTL,
		DistilledTaskTTL:  c.DistilledTaskTTL,
		VectorDim:         c.VectorDim,
		EnablePostgres:    c.EnablePostgres,
		PostgresDSN:       c.PostgresDSN,
	}
}

// Message represents a single message in a session.
type Message struct {
	// Role is the message role (user, assistant, system, tool).
	Role string
	// Content is the message content.
	Content string
	// Time is the timestamp when the message was created.
	Time time.Time
}

// toPublicMessages converts a slice of internal Messages to public Messages.
func toPublicMessages(msgs []internal.Message) []Message {
	if msgs == nil {
		return nil
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = Message{
			Role:    m.Role,
			Content: m.Content,
			Time:    m.Time,
		}
	}
	return out
}

// Service wraps internal/ares_memory.MemoryManager for public consumption.
type Service struct {
	inner internal.MemoryManager
}

// New creates a new memory service with the given config.
// When cfg is nil, default configuration is used.
func New(cfg *Config) (*Service, error) {
	internalCfg := cfg.toInternal()
	if internalCfg == nil {
		internalCfg = internal.DefaultMemoryConfig()
	}
	mgr, err := internal.NewMemoryManager(internalCfg)
	if err != nil {
		return nil, err
	}
	return &Service{inner: mgr}, nil
}

// CreateSession creates a new session and returns the session ID.
func (s *Service) CreateSession(ctx context.Context, userID string) (string, error) {
	return s.inner.CreateSession(ctx, userID)
}

// AddMessage adds a message to the session.
func (s *Service) AddMessage(ctx context.Context, sessionID, role, content string) error {
	return s.inner.AddMessage(ctx, sessionID, role, content)
}

// GetMessages retrieves all messages from the session.
func (s *Service) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	msgs, err := s.inner.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return toPublicMessages(msgs), nil
}

// DeleteSession deletes a session and all its messages immediately.
func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	return s.inner.DeleteSession(ctx, sessionID)
}

// BuildContext builds input with conversation history context.
func (s *Service) BuildContext(ctx context.Context, input string, sessionID string) (string, error) {
	return s.inner.BuildContext(ctx, input, sessionID)
}

// CreateTask creates a new task and returns the task ID.
func (s *Service) CreateTask(ctx context.Context, sessionID, userID, input string) (string, error) {
	return s.inner.CreateTask(ctx, sessionID, userID, input)
}

// UpdateTaskOutput updates the task output.
func (s *Service) UpdateTaskOutput(ctx context.Context, taskID, output string) error {
	return s.inner.UpdateTaskOutput(ctx, taskID, output)
}

// DistillTask extracts key information from task for future reference.
func (s *Service) DistillTask(ctx context.Context, taskID string) (*models.Task, error) {
	return s.inner.DistillTask(ctx, taskID)
}

// SearchSimilarTasks searches for similar tasks using local cosine similarity.
func (s *Service) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	return s.inner.SearchSimilarTasks(ctx, query, limit)
}

// Start starts the memory manager and background workers.
func (s *Service) Start(ctx context.Context) error {
	return s.inner.Start(ctx)
}

// Stop stops the memory manager and cleans up resources.
func (s *Service) Stop(ctx context.Context) error {
	return s.inner.Stop(ctx)
}
