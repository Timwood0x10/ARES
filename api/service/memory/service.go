// Package memory provides the public API for memory management.
package memory

import (
	"context"

	"github.com/Timwood0x10/ares/api/core"
	internal "github.com/Timwood0x10/ares/internal/ares_memory"
)

// Config re-exports internal's memory config.
type Config = internal.MemoryConfig

// Service wraps internal/ares_memory to implement core.MemoryService.
type Service struct {
	inner internal.MemoryManager
}

// New creates a new memory service with the given config.
func New(cfg *internal.MemoryConfig) (*Service, error) {
	if cfg == nil {
		cfg = internal.DefaultMemoryConfig()
	}
	mgr, err := internal.NewMemoryManager(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{inner: mgr}, nil
}

// CreateSession creates a new session.
func (s *Service) CreateSession(ctx context.Context, config *core.SessionConfig) (string, error) {
	return "", errNotImplemented
}

// GetSession retrieves a session.
func (s *Service) GetSession(ctx context.Context, sessionID string) (*core.Session, error) {
	return nil, errNotImplemented
}

// DeleteSession deletes a session.
func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	return errNotImplemented
}

// AddMessage adds a message to a session.
func (s *Service) AddMessage(ctx context.Context, sessionID string, role core.MessageRole, content string) error {
	return errNotImplemented
}

// GetMessages retrieves messages from a session.
func (s *Service) GetMessages(ctx context.Context, sessionID string, pagination *core.PaginationRequest) ([]*core.Message, error) {
	return nil, errNotImplemented
}

// DistillTask distills a task for future reference.
func (s *Service) DistillTask(ctx context.Context, taskID string) (*core.DistilledTask, error) {
	return nil, errNotImplemented
}

// SearchSimilarTasks searches for similar tasks.
func (s *Service) SearchSimilarTasks(ctx context.Context, query *core.SearchQuery) ([]*core.SearchResult, error) {
	return nil, errNotImplemented
}

// SetEventStore attaches an event store to the memory manager.
func (s *Service) SetEventStore(store core.EventStore, component string) {
	// Event store integration requires bootstrap-level wiring.
}

// Internal returns the underlying MemoryManager for compatibility.
func (s *Service) Internal() internal.MemoryManager {
	return s.inner
}
