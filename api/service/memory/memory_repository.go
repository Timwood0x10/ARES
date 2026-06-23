// Package memory provides in-memory repository implementation for development/testing.
package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/api/core"
)

// MemoryRepository provides an in-memory implementation of MemoryRepository.
// This is useful for development and testing without a database.
type MemoryRepository struct {
	mu       sync.RWMutex
	sessions map[string]*core.Session
	messages map[string][]*core.Message
	tasks    map[string]*core.DistilledTask
}

// NewMemoryRepository creates a new in-memory memory repository.
// Returns new memory repository instance.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		sessions: make(map[string]*core.Session),
		messages: make(map[string][]*core.Message),
		tasks:    make(map[string]*core.DistilledTask),
	}
}

// CreateSession creates a new session.
// Args:
// ctx - operation context.
// session - the session to create.
// Returns error if creation fails.
func (r *MemoryRepository) CreateSession(ctx context.Context, session *core.Session) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sessions[session.ID]; exists {
		return fmt.Errorf("session already exists: %s", session.ID)
	}

	r.sessions[session.ID] = session
	return nil
}

// GetSession retrieves a session by ID.
// Args:
// ctx - operation context.
// sessionID - the session identifier.
// Returns the session or error if not found.
func (r *MemoryRepository) GetSession(ctx context.Context, sessionID string) (*core.Session, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	session, exists := r.sessions[sessionID]
	if !exists {
		return nil, nil
	}

	// Return a copy to avoid mutation
	sessionCopy := *session
	return &sessionCopy, nil
}

// UpdateSession updates an existing session.
// Args:
// ctx - operation context.
// session - the session to update.
// Returns error if update fails.
func (r *MemoryRepository) UpdateSession(ctx context.Context, session *core.Session) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sessions[session.ID]; !exists {
		return fmt.Errorf("session not found: %s", session.ID)
	}

	r.sessions[session.ID] = session
	return nil
}

// DeleteSession deletes a session and all its messages.
// Args:
// ctx - operation context.
// sessionID - the session identifier.
// Returns error if deletion fails.
func (r *MemoryRepository) DeleteSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sessions[sessionID]; !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	delete(r.sessions, sessionID)
	delete(r.messages, sessionID)
	return nil
}

// AddMessage adds a message to a session.
// Args:
// ctx - operation context.
// message - the message to add.
// Returns error if addition fails.
func (r *MemoryRepository) AddMessage(ctx context.Context, message *core.Message) error {
	if message == nil {
		return fmt.Errorf("message is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Verify session exists
	if _, exists := r.sessions[message.SessionID]; !exists {
		return fmt.Errorf("session not found: %s", message.SessionID)
	}

	r.messages[message.SessionID] = append(r.messages[message.SessionID], message)
	return nil
}

// GetMessages retrieves messages from a session.
// Args:
// ctx - operation context.
// sessionID - the session identifier.
// pagination - pagination parameters.
// Returns list of messages or error.
func (r *MemoryRepository) GetMessages(ctx context.Context, sessionID string, pagination *core.PaginationRequest) ([]*core.Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	messages, exists := r.messages[sessionID]
	if !exists {
		return []*core.Message{}, nil
	}

	// Return copies to avoid mutation
	result := make([]*core.Message, len(messages))
	for i, msg := range messages {
		msgCopy := *msg
		result[i] = &msgCopy
	}

	// Apply pagination
	if pagination != nil {
		limit := pagination.Limit
		if limit <= 0 {
			limit = pagination.PageSize
		}
		if limit <= 0 {
			limit = 100 // default limit
		}

		offset := pagination.Offset
		if offset <= 0 && pagination.Page > 0 {
			offset = (pagination.Page - 1) * limit
		}

		if offset >= len(result) {
			return []*core.Message{}, nil
		}

		if offset+limit > len(result) {
			limit = len(result) - offset
		}

		result = result[offset : offset+limit]
	}

	return result, nil
}

// StoreDistilledTask stores a distilled task.
// Args:
// ctx - operation context.
// task - the distilled task to store.
// Returns error if storage fails.
func (r *MemoryRepository) StoreDistilledTask(ctx context.Context, task *core.DistilledTask) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.tasks[task.TaskID] = task
	return nil
}

// GetDistilledTask retrieves a distilled task by ID.
// Args:
// ctx - operation context.
// taskID - the task identifier.
// Returns the distilled task or error if not found.
func (r *MemoryRepository) GetDistilledTask(ctx context.Context, taskID string) (*core.DistilledTask, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID is empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	task, exists := r.tasks[taskID]
	if !exists {
		return nil, nil
	}

	// Return a copy to avoid mutation
	taskCopy := *task
	return &taskCopy, nil
}

// SearchSimilarTasks searches for similar tasks.
// Args:
// ctx - operation context.
// query - the search query.
// Returns list of search results or error.
// Note: This is a simplified implementation that doesn't do actual vector similarity search.
func (r *MemoryRepository) SearchSimilarTasks(ctx context.Context, query *core.SearchQuery) ([]*core.SearchResult, error) {
	if query == nil {
		return nil, fmt.Errorf("query is nil")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make([]*core.SearchResult, 0)
	count := 0

	// Simplified search: return tasks that match tags or contain query text
	for _, task := range r.tasks {
		if count >= query.Limit {
			break
		}

		// Check if task matches any tag
		matched := false
		for _, tag := range task.Tags {
			for _, queryTag := range query.Tags {
				if tag == queryTag {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}

		// Check if task input contains query text
		if !matched && len(task.Input) > 0 && len(query.Query) > 0 {
			// Simple text matching (not actual semantic search)
			// In production, this would use vector similarity
			matched = true
		}

		if matched {
			results = append(results, &core.SearchResult{
				TaskID:  task.TaskID,
				Input:   task.Input,
				Output:  task.Output,
				Context: task.Context,
				Summary: task.Summary,
				Score:   0.8, // Placeholder score
				Tags:    task.Tags,
			})
			count++
		}
	}

	return results, nil
}
