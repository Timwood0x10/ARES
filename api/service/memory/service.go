// Package memory provides the public API for memory management.
package memory

import (
	"context"

	internal "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// Config re-exports internal's memory config.
type Config = internal.MemoryConfig

// Message represents a single message in a session.
type Message = internal.Message

// Service wraps internal/ares_memory.MemoryManager for public consumption.
type Service struct {
	inner internal.MemoryManager
}

// New creates a new memory service with the given config.
// When cfg is nil, DefaultMemoryConfig() is used.
func New(cfg *Config) (*Service, error) {
	if cfg == nil {
		cfg = internal.DefaultMemoryConfig()
	}
	mgr, err := internal.NewMemoryManager(cfg)
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
	return s.inner.GetMessages(ctx, sessionID)
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
