// Package memory provides high-level APIs for memory management.
package memory

import (
	"context"
	"fmt"
	"log/slog"

	"goagentx/internal/errors"
	"goagentx/internal/memory"
)

// Service provides memory management operations.
type Service struct {
	memoryMgr memory.MemoryManager
}

// NewService creates a new memory service instance.
// Args:
// memoryMgr - internal memory manager instance.
// Returns new memory service instance.
func NewService(memoryMgr memory.MemoryManager) *Service {
	return &Service{
		memoryMgr: memoryMgr,
	}
}

// CreateSession creates a new conversation session.
// Args:
// ctx - operation context.
// userID - user identifier for the session.
// Returns session ID or error if creation fails.
func (s *Service) CreateSession(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", ErrInvalidUserID
	}

	sessionID, err := s.memoryMgr.CreateSession(ctx, userID)
	if err != nil {
		return "", errors.Wrap(err, "create session")
	}

	return sessionID, nil
}

// AddMessage adds a message to the session.
// Args:
// ctx - operation context.
// sessionID - session identifier.
// role - message role (user/assistant/system).
// content - message content.
// Returns error if operation fails.
func (s *Service) AddMessage(ctx context.Context, sessionID, role, content string) error {
	if sessionID == "" {
		return ErrInvalidSessionID
	}
	if role == "" {
		return ErrInvalidRole
	}
	if content == "" {
		return ErrInvalidContent
	}

	if err := s.memoryMgr.AddMessage(ctx, sessionID, role, content); err != nil {
		return errors.Wrap(err, "add message")
	}

	return nil
}

// GetMessages retrieves all messages from the session.
// Args:
// ctx - operation context.
// sessionID - session identifier.
// Returns list of messages or error if retrieval fails.
func (s *Service) GetMessages(ctx context.Context, sessionID string) ([]*Message, error) {
	if sessionID == "" {
		return nil, ErrInvalidSessionID
	}

	messages, err := s.memoryMgr.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "get messages")
	}

	// Convert internal messages to API messages
	apiMessages := make([]*Message, 0, len(messages))
	for _, msg := range messages {
		apiMessages = append(apiMessages, &Message{
			Role:    msg.Role,
			Content: msg.Content,
			Time:    msg.Time.Format("2006-01-02 15:04:05"),
		})
	}

	return apiMessages, nil
}

// DeleteSession deletes a session and all its messages.
// Args:
// ctx - operation context.
// sessionID - session identifier.
// Returns error if deletion fails.
func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return ErrInvalidSessionID
	}

	if s.memoryMgr == nil {
		return fmt.Errorf("memory manager not configured")
	}

	// Delete session and all associated messages
	if err := s.memoryMgr.DeleteSession(ctx, sessionID); err != nil {
		return errors.Wrap(err, "delete session")
	}

	slog.Info("Session deleted successfully", "session_id", sessionID)
	return nil
}

// DistillTask extracts key information from a task for future reference.
// Args:
// ctx - operation context.
// taskID - task identifier.
// Returns error if distillation fails.
func (s *Service) DistillTask(ctx context.Context, taskID string) error {
	if taskID == "" {
		return ErrInvalidTaskID
	}

	task, err := s.memoryMgr.DistillTask(ctx, taskID)
	if err != nil {
		return errors.Wrap(err, "distill task")
	}

	if err := s.memoryMgr.StoreDistilledTask(ctx, taskID, task); err != nil {
		return errors.Wrap(err, "store distilled task")
	}

	return nil
}

// SearchSimilarTasks searches for similar tasks using vector similarity.
// Args:
// ctx - operation context.
// query - search query text.
// limit - maximum number of results to return.
// Returns list of similar tasks or error if search fails.
func (s *Service) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*Task, error) {
	if query == "" {
		return nil, ErrInvalidQuery
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}

	tasks, err := s.memoryMgr.SearchSimilarTasks(ctx, query, limit)
	if err != nil {
		return nil, errors.Wrap(err, "search similar tasks")
	}

	// Convert internal tasks to API tasks
	apiTasks := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		apiTask := &Task{
			TaskID: task.TaskID,
		}

		if task.Payload != nil {
			apiTask.Input = getPayloadString(task.Payload, "input")
			apiTask.Output = getPayloadString(task.Payload, "output")
			apiTask.Context = getPayloadString(task.Payload, "context")
		}

		apiTasks = append(apiTasks, apiTask)
	}

	return apiTasks, nil
}

// getPayloadString safely extracts a string from payload.
func getPayloadString(payload map[string]any, key string) string {
	if val, ok := payload[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// Message represents a conversation message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Time    string `json:"time"`
}

// Task represents a distilled task with its extracted information.
type Task struct {
	TaskID  string `json:"task_id"`
	Input   string `json:"input"`
	Output  string `json:"output"`
	Context string `json:"context"`
}
