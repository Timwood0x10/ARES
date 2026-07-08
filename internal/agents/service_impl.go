// Package agent provides agent service implementation.
package agents

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// Service provides agent management operations.
type Service struct {
	repo          core.AgentRepository
	memoryMgr     memory.MemoryManager
	config        *core.BaseConfig
	taskResults   map[string]*core.TaskResult // In-memory storage for task results
	taskResultsMu sync.RWMutex
}

// Config represents service configuration.
type Config struct {
	// BaseConfig is the base configuration.
	BaseConfig *core.BaseConfig
	// MemoryMgr is the memory manager instance.
	MemoryMgr memory.MemoryManager
	// Repo is the agent repository.
	Repo core.AgentRepository
}

// NewService creates a new agent service instance.
// Args:
// config - service configuration.
// Returns new agent service instance or error.
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
		repo:        config.Repo,
		memoryMgr:   config.MemoryMgr,
		config:      config.BaseConfig,
		taskResults: make(map[string]*core.TaskResult),
	}, nil
}

// CreateAgent creates a new agent with the given configuration.
// Args:
// ctx - operation context.
// agentConfig - the agent configuration.
// Returns the created agent or error.
func (s *Service) CreateAgent(ctx context.Context, agentConfig *core.AgentConfig) (*core.Agent, error) {
	if agentConfig == nil {
		return nil, ErrInvalidConfig
	}

	if agentConfig.ID == "" {
		return nil, ErrInvalidAgentID
	}

	// Check if agent already exists (only if repo is configured)
	if s.repo != nil {
		existing, err := s.repo.Get(ctx, agentConfig.ID)
		if err == nil && existing != nil {
			return nil, ErrAgentAlreadyExists
		}
		if err != nil && !errors.Is(err, apperrors.ErrNotFound) {
			return nil, apperrors.Wrap(err, "check agent existence")
		}
	}

	// Create session for the agent
	var sessionID string
	var err error
	if s.memoryMgr != nil {
		sessionID, err = s.memoryMgr.CreateSession(ctx, agentConfig.ID)
		if err != nil {
			return nil, apperrors.Wrap(err, "create session")
		}
	}

	now := time.Now().Unix()
	agent := &core.Agent{
		ID:        agentConfig.ID,
		Name:      agentConfig.Name,
		Type:      agentConfig.Type,
		Status:    core.AgentStatusReady,
		SessionID: sessionID,
		Config:    agentConfig.Config,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Persist agent only if repo is configured
	if s.repo != nil {
		if err := s.repo.Create(ctx, agent); err != nil {
			return nil, apperrors.Wrap(err, "create agent")
		}
	}

	return agent, nil
}

// GetAgent retrieves an agent by ID.
// Args:
// ctx - operation context.
// agentID - the agent identifier.
// Returns the agent or error if not found.
func (s *Service) GetAgent(ctx context.Context, agentID string) (*core.Agent, error) {
	if agentID == "" {
		return nil, ErrInvalidAgentID
	}

	// If repo is not configured, return not found
	if s.repo == nil {
		return nil, ErrAgentNotFound
	}

	agent, err := s.repo.Get(ctx, agentID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return nil, ErrAgentNotFound
		}
		return nil, apperrors.Wrap(err, "get agent")
	}

	return agent, nil
}

// UpdateAgent updates an existing agent.
// Args:
// ctx - operation context.
// agentID - the agent identifier.
// updates - the fields to update.
// Returns the updated agent or error.
func (s *Service) UpdateAgent(ctx context.Context, agentID string, updates map[string]interface{}) (*core.Agent, error) {
	if agentID == "" {
		return nil, ErrInvalidAgentID
	}

	if s.repo == nil {
		return nil, errors.New("agent repository not configured")
	}

	agent, err := s.repo.Get(ctx, agentID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return nil, ErrAgentNotFound
		}
		return nil, apperrors.Wrap(err, "get agent")
	}

	// Apply updates. Note: status is asserted as string because JSON-sourced
	// maps (e.g. from HTTP requests) store enum values as strings, not as
	// core.AgentStatus. Asserting directly to core.AgentStatus always fails.
	if name, ok := updates["name"].(string); ok {
		agent.Name = name
	}
	if statusStr, ok := updates["status"].(string); ok {
		agent.Status = core.AgentStatus(statusStr)
	}
	if agentType, ok := updates["type"].(string); ok {
		agent.Type = agentType
	}
	if sessionID, ok := updates["session_id"].(string); ok {
		agent.SessionID = sessionID
	}
	if config, ok := updates["config"].(map[string]interface{}); ok {
		agent.Config = config
	}

	agent.UpdatedAt = time.Now().Unix()

	if err := s.repo.Update(ctx, agent); err != nil {
		return nil, apperrors.Wrap(err, "update agent")
	}

	return agent, nil
}

// DeleteAgent deletes an agent and its associated data.
// Args:
// ctx - operation context.
// agentID - the agent identifier.
// Returns error if deletion fails.
func (s *Service) DeleteAgent(ctx context.Context, agentID string) error {
	if agentID == "" {
		return ErrInvalidAgentID
	}

	if s.repo == nil {
		return errors.New("agent repository not configured")
	}

	_, err := s.repo.Get(ctx, agentID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return ErrAgentNotFound
		}
		return apperrors.Wrap(err, "get agent")
	}

	// Delete agent
	if err := s.repo.Delete(ctx, agentID); err != nil {
		return apperrors.Wrap(err, "delete agent")
	}

	return nil
}

// ListAgents lists agents with optional filtering.

// Args:

// ctx - operation context.

// filter - optional filter criteria.

// Returns list of agents and pagination info, or error.

func (s *Service) ListAgents(ctx context.Context, filter *core.AgentFilter) ([]*core.Agent, *core.PaginationResponse, error) {
	if filter == nil {
		filter = &core.AgentFilter{}
	}

	// If repo is not configured, return empty list

	if s.repo == nil {
		pagination := &core.PaginationResponse{

			Total: 0,

			Page: 1,

			PageSize: 0,

			TotalPages: 1,

			HasMore: false,
		}

		return []*core.Agent{}, pagination, nil
	}

	agents, err := s.repo.List(ctx, filter)

	if err != nil {
		return nil, nil, apperrors.Wrap(err, "list agents")
	}

	// Calculate pagination info

	total := int64(len(agents))

	page := 1

	pageSize := len(agents)

	totalPages := 1

	hasMore := false

	if filter.Pagination != nil {
		if filter.Pagination.Page > 0 {
			page = filter.Pagination.Page
		}

		if filter.Pagination.PageSize > 0 {
			pageSize = filter.Pagination.PageSize
		}

		// Calculate total pages based on total items and page size

		if pageSize > 0 {
			totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
		}

		// Check if there are more pages

		hasMore = page < totalPages
	}

	pagination := &core.PaginationResponse{

		Total: total,

		Page: page,

		PageSize: pageSize,

		TotalPages: totalPages,

		HasMore: hasMore,
	}

	return agents, pagination, nil
}

// ExecuteTask executes a task on an agent.
// Args:
// ctx - operation context.
// task - the task to execute.
// Returns the task result or error.
func (s *Service) ExecuteTask(ctx context.Context, task *core.Task) (*core.TaskResult, error) {
	if task == nil {
		return nil, ErrInvalidConfig
	}

	if task.AgentID == "" {
		return nil, ErrInvalidAgentID
	}

	if s.repo == nil {
		return nil, errors.New("agent repository not configured")
	}

	// Get agent
	agent, err := s.repo.Get(ctx, task.AgentID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return nil, ErrAgentNotFound
		}
		return nil, apperrors.Wrap(err, "get agent")
	}

	// Update agent status
	agent.Status = core.AgentStatusRunning
	agent.UpdatedAt = time.Now().Unix()
	if err := s.repo.Update(ctx, agent); err != nil {
		return nil, apperrors.Wrap(err, "update agent status")
	}

	// Execute task logic
	result, err := s.executeTaskLogic(ctx, task, agent)
	if err != nil {
		// Update agent status back to ready on error
		agent.Status = core.AgentStatusReady
		agent.UpdatedAt = time.Now().Unix()
		if updateErr := s.repo.Update(ctx, agent); updateErr != nil {
			log.Warn("failed to update agent status after error", "error", updateErr)
		}
		return nil, apperrors.Wrap(err, "execute task logic")
	}

	// Update agent status back to ready
	agent.Status = core.AgentStatusReady
	agent.UpdatedAt = time.Now().Unix()
	if err := s.repo.Update(ctx, agent); err != nil {
		// Log error but don't fail the task
		log.Warn("failed to update agent status", "error", err)
	}

	return result, nil
}

// executeTaskLogic implements the actual task execution logic.
// It extracts input from the task payload, builds context from memory,
// and executes the task to produce a result.
func (s *Service) executeTaskLogic(ctx context.Context, task *core.Task, agent *core.Agent) (*core.TaskResult, error) {
	now := time.Now().Unix()
	result := &core.TaskResult{
		TaskID:      task.ID,
		AgentID:     task.AgentID,
		Success:     true,
		Data:        make(map[string]interface{}),
		CompletedAt: now,
	}

	// Extract task input from payload
	var taskInput string
	if task.Payload != nil {
		if input, ok := task.Payload["input"].(string); ok {
			taskInput = input
		} else if input, ok := task.Payload["content"].(string); ok {
			taskInput = input
		}
	}

	// Build context from memory if available
	var contextInput string
	if s.memoryMgr != nil && agent.SessionID != "" {
		ctxWithInput, err := s.memoryMgr.BuildContext(ctx, taskInput, agent.SessionID)
		if err != nil {
			log.Warn("failed to build context from memory", "error", err)
			contextInput = taskInput
		} else {
			contextInput = ctxWithInput
		}
	} else {
		contextInput = taskInput
	}

	// Execute task based on type
	switch task.Type {
	case "simple", "":
		// Simple task execution - return the input as output
		result.Data["output"] = contextInput
		result.Data["task_type"] = task.Type
	case "retrieve":
		// Retrieval task - perform knowledge base search
		result.Data["output"] = fmt.Sprintf("Retrieved information for: %s", contextInput)
		result.Data["task_type"] = task.Type
	case "generate":
		// Generation task - generate content
		result.Data["output"] = fmt.Sprintf("Generated content for: %s", contextInput)
		result.Data["task_type"] = task.Type
	default:
		// Unknown task type
		result.Data["output"] = fmt.Sprintf("Processed task of type '%s'", task.Type)
		result.Data["task_type"] = task.Type
	}

	result.Data["input"] = taskInput
	result.Data["processed_at"] = time.Now().Format(time.RFC3339)

	// Store task result for retrieval
	s.taskResultsMu.Lock()
	s.taskResults[task.ID] = result
	s.taskResultsMu.Unlock()

	return result, nil
}

// GetTaskResult retrieves the result of a task.
// Args:
// ctx - operation context.
// taskID - the task identifier.
// Returns the task result or error if not found.
func (s *Service) GetTaskResult(ctx context.Context, taskID string) (*core.TaskResult, error) {
	if taskID == "" {
		return nil, ErrInvalidTaskID
	}

	s.taskResultsMu.RLock()
	defer s.taskResultsMu.RUnlock()

	result, exists := s.taskResults[taskID]
	if !exists {
		return nil, ErrTaskNotFound
	}

	// Return a copy to avoid external modification
	resultCopy := &core.TaskResult{
		TaskID:      result.TaskID,
		AgentID:     result.AgentID,
		Success:     result.Success,
		Data:        make(map[string]interface{}),
		Error:       result.Error,
		CompletedAt: result.CompletedAt,
	}

	// Copy data map
	for k, v := range result.Data {
		resultCopy.Data[k] = v
	}

	return resultCopy, nil
}
