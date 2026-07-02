// Package memory provides memory service implementation.
package memoryservice

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Timwood0x10/ares/api/core"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/truncate"
)

// Service provides memory management operations.
type Service struct {
	repo      core.MemoryRepository
	memoryMgr memory.MemoryManager
	config    *core.BaseConfig
}

// Config represents service configuration.
type Config struct {
	// BaseConfig is the base configuration.
	BaseConfig *core.BaseConfig
	// MemoryMgr is the internal memory manager instance.
	MemoryMgr memory.MemoryManager
	// Repo is the memory repository.
	Repo core.MemoryRepository
}

// NewService creates a new memory service instance.
// Args:
// config - service configuration.
// Returns new memory service instance or error.
func NewService(config *Config) (*Service, error) {
	if config == nil {
		return nil, ErrInvalidConfig
	}

	if config.BaseConfig == nil {
		config.BaseConfig = &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		}
	}

	return &Service{
		repo:      config.Repo,
		memoryMgr: config.MemoryMgr,
		config:    config.BaseConfig,
	}, nil
}

// CreateSession creates a new session with the given configuration.
// Args:
// ctx - operation context.
// sessionConfig - the session configuration.
// Returns the session ID or error.
func (s *Service) CreateSession(ctx context.Context, sessionConfig *core.SessionConfig) (string, error) {
	if sessionConfig == nil {
		return "", ErrInvalidConfig
	}

	if sessionConfig.UserID == "" {
		return "", ErrInvalidUserID
	}

	// Generate session ID
	sessionID := generateSessionID()

	now := time.Now()
	var expiresAt *time.Time
	if sessionConfig.ExpiresIn > 0 {
		expired := now.Add(sessionConfig.ExpiresIn)
		expiresAt = &expired
	}

	session := &core.Session{
		ID:        sessionID,
		UserID:    sessionConfig.UserID,
		TenantID:  sessionConfig.TenantID,
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: expiresAt,
		Metadata:  sessionConfig.Metadata,
	}

	if err := s.repo.CreateSession(ctx, session); err != nil {
		return "", errors.Wrap(err, "create session")
	}

	return sessionID, nil
}

// GetSession retrieves a session by ID.
// Args:
// ctx - operation context.
// sessionID - the session identifier.
// Returns the session or error if not found.
func (s *Service) GetSession(ctx context.Context, sessionID string) (*core.Session, error) {
	if sessionID == "" {
		return nil, ErrInvalidSessionID
	}

	session, err := s.repo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "get session")
	}

	if session == nil {
		return nil, ErrSessionNotFound
	}

	return session, nil
}

// DeleteSession deletes a session and all its messages.
// Args:
// ctx - operation context.
// sessionID - the session identifier.
// Returns error if deletion fails.
func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return ErrInvalidSessionID
	}

	// Verify session exists
	_, err := s.repo.GetSession(ctx, sessionID)
	if err != nil {
		return errors.Wrap(err, "get session")
	}

	if err := s.repo.DeleteSession(ctx, sessionID); err != nil {
		return errors.Wrap(err, "delete session")
	}

	return nil
}

// AddMessage adds a message to a session.
// Args:
// ctx - operation context.
// sessionID - the session identifier.
// role - the message role.
// content - the message content.
// Returns error if addition fails.
func (s *Service) AddMessage(ctx context.Context, sessionID string, role core.MessageRole, content string) error {
	if sessionID == "" {
		return ErrInvalidSessionID
	}

	if role == "" {
		return ErrInvalidRole
	}

	if content == "" {
		return ErrInvalidContent
	}

	// Verify session exists
	_, err := s.repo.GetSession(ctx, sessionID)
	if err != nil {
		return errors.Wrap(err, "get session")
	}

	message := &core.Message{
		ID:        generateMessageID(),
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Time:      time.Now(),
		Metadata:  make(core.Metadata),
	}

	if err := s.repo.AddMessage(ctx, message); err != nil {
		return errors.Wrap(err, "add message")
	}

	return nil
}

// GetMessages retrieves messages from a session.
// Args:
// ctx - operation context.
// sessionID - the session identifier.
// pagination - pagination parameters.
// Returns list of messages or error.
func (s *Service) GetMessages(ctx context.Context, sessionID string, pagination *core.PaginationRequest) ([]*core.Message, error) {
	if sessionID == "" {
		return nil, ErrInvalidSessionID
	}

	// Verify session exists
	_, err := s.repo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "get session")
	}

	if pagination == nil {
		pagination = &core.PaginationRequest{
			Page:     1,
			PageSize: 100,
		}
	}

	messages, err := s.repo.GetMessages(ctx, sessionID, pagination)
	if err != nil {
		return nil, errors.Wrap(err, "get messages")
	}

	return messages, nil
}

// DistillTask distills a task for future reference.
// Args:
// ctx - operation context.
// taskID - the task identifier.
// Returns the distilled task or error.
func (s *Service) DistillTask(ctx context.Context, taskID string) (*core.DistilledTask, error) {
	if taskID == "" {
		return nil, ErrInvalidTaskID
	}

	// Get task from memory manager if available
	var taskInput, taskOutput, taskContext string
	var taskTags []string

	if s.memoryMgr != nil {
		task, err := s.memoryMgr.DistillTask(ctx, taskID)
		if err != nil {
			return nil, errors.Wrap(err, "get task for distillation")
		}

		// Extract task information
		if task.Payload != nil {
			if input, ok := task.Payload["input"].(string); ok {
				taskInput = input
			}
			if output, ok := task.Payload["output"].(string); ok {
				taskOutput = output
			}
			if context, ok := task.Payload["context"].(string); ok {
				taskContext = context
			}
		}

		// Generate summary from input and output
		summary := s.generateSummary(taskInput, taskOutput)

		// Generate tags based on task type and content
		taskTags = s.generateTags(string(task.TaskType), taskInput, taskOutput)

		// Create distilled task
		distilledTask := &core.DistilledTask{
			TaskID:    taskID,
			Input:     taskInput,
			Output:    taskOutput,
			Context:   taskContext,
			Summary:   summary,
			Tags:      taskTags,
			Embedding: nil, // Embedding will be generated by storage layer
			CreatedAt: time.Now(),
		}

		if err := s.repo.StoreDistilledTask(ctx, distilledTask); err != nil {
			return nil, errors.Wrap(err, "store distilled task")
		}

		return distilledTask, nil
	}

	// Fallback if memory manager is not available
	distilledTask := &core.DistilledTask{
		TaskID:    taskID,
		Input:     taskInput,
		Output:    taskOutput,
		Context:   taskContext,
		Summary:   "Task distillation without memory manager",
		Tags:      taskTags,
		Embedding: nil,
		CreatedAt: time.Now(),
	}

	if err := s.repo.StoreDistilledTask(ctx, distilledTask); err != nil {
		return nil, errors.Wrap(err, "store distilled task")
	}

	return distilledTask, nil
}

// generateSummary generates a concise summary from input and output.
func (s *Service) generateSummary(input, output string) string {
	if input == "" && output == "" {
		return "Empty task"
	}

	maxLen := 200
	summary := ""
	if input != "" {
		summary = "Input: " + truncate.WithEllipsis(input, maxLen)
	}
	if output != "" {
		if summary != "" {
			summary += " | "
		}
		summary += "Output: " + truncate.WithEllipsis(output, maxLen)
	}

	return summary
}

// generateTags generates tags based on task type and content.
func (s *Service) generateTags(taskType, input, output string) []string {
	tags := []string{taskType}

	// Add content-based tags
	if len(input) > 100 {
		tags = append(tags, "long_input")
	}
	if len(output) > 100 {
		tags = append(tags, "long_output")
	}

	// Add common tags based on keywords
	combined := input + " " + output
	combinedLower := strings.ToLower(combined)

	if strings.Contains(combinedLower, "error") || strings.Contains(combinedLower, "fail") {
		tags = append(tags, "error_handling")
	}
	if strings.Contains(combinedLower, "search") || strings.Contains(combinedLower, "retrieve") {
		tags = append(tags, "retrieval")
	}
	if strings.Contains(combinedLower, "generate") || strings.Contains(combinedLower, "create") {
		tags = append(tags, "generation")
	}

	return tags
}

// SearchSimilarTasks searches for similar tasks.
// Args:
// ctx - operation context.
// query - the search query.
// Returns list of search results or error.
func (s *Service) SearchSimilarTasks(ctx context.Context, query *core.SearchQuery) ([]*core.SearchResult, error) {
	if query == nil {
		return nil, ErrInvalidConfig
	}

	if query.Query == "" {
		return nil, ErrInvalidQuery
	}

	if query.Limit <= 0 {
		query.Limit = 10
	}

	results, err := s.repo.SearchSimilarTasks(ctx, query)
	if err != nil {
		return nil, errors.Wrap(err, "search similar tasks")
	}

	return results, nil
}

// generateSessionID generates a unique session ID.
func generateSessionID() string {
	return "session_" + uuid.New().String()
}

// generateMessageID generates a unique message ID.
func generateMessageID() string {
	return "msg_" + uuid.New().String()
}
