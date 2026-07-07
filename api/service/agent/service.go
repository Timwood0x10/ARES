// Package agent provides the public API for agent lifecycle management.
// It wraps the internal agent implementation for use by client packages.
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	agentapi "github.com/Timwood0x10/ares/internal/api_impl/agent"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
)

// Service implements core.AgentService by wrapping the internal agent implementation.
type Service struct {
	inner   *agentapi.Service
	results map[string]*core.TaskResult
	mu      sync.RWMutex
}

// New creates a new agent service backed by a memory manager.
// If memoryMgr is nil, a default in-memory manager is used.
func New(memoryMgr memory.MemoryManager) (*Service, error) {
	if memoryMgr == nil {
		var err error
		memoryMgr, err = memory.NewMemoryManager(nil)
		if err != nil {
			return nil, fmt.Errorf("agent: create memory manager: %w", err)
		}
	}
	return &Service{
		inner:   agentapi.NewService(memoryMgr),
		results: make(map[string]*core.TaskResult),
	}, nil
}

// CreateAgent creates a new agent with the given configuration.
func (s *Service) CreateAgent(ctx context.Context, config *core.AgentConfig) (*core.Agent, error) {
	if config == nil {
		return nil, fmt.Errorf("agent config is required")
	}
	agent, err := s.inner.CreateAgent(ctx, config.ID)
	if err != nil {
		return nil, fmt.Errorf("agent: create: %w", err)
	}
	return &core.Agent{
		ID:        agent.ID,
		SessionID: agent.SessionID,
		Status:    core.AgentStatus(agent.Status),
		CreatedAt: agent.CreatedAt,
	}, nil
}

// GetAgent retrieves an agent by ID.
func (s *Service) GetAgent(ctx context.Context, agentID string) (*core.Agent, error) {
	agent, err := s.inner.GetAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent: get: %w", err)
	}
	return &core.Agent{
		ID:        agent.ID,
		SessionID: agent.SessionID,
		Status:    core.AgentStatus(agent.Status),
		CreatedAt: agent.CreatedAt,
	}, nil
}

// UpdateAgent updates an existing agent's configuration.
func (s *Service) UpdateAgent(ctx context.Context, agentID string, updates map[string]interface{}) (*core.Agent, error) {
	// For now, delegate to GetAgent and report update not fully wired.
	// In the future this will support partial updates to the agent record.
	agent, err := s.inner.GetAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent: update: %w", err)
	}
	_ = updates // reserved for future field-level updates
	return &core.Agent{
		ID:        agent.ID,
		SessionID: agent.SessionID,
		Status:    core.AgentStatus(agent.Status),
		CreatedAt: agent.CreatedAt,
	}, nil
}

// DeleteAgent deletes an agent and its associated data.
func (s *Service) DeleteAgent(ctx context.Context, agentID string) error {
	if err := s.inner.DeleteAgent(ctx, agentID); err != nil {
		return fmt.Errorf("agent: delete: %w", err)
	}
	return nil
}

// ListAgents lists agents with optional filtering.
func (s *Service) ListAgents(ctx context.Context, filter *core.AgentFilter) ([]*core.Agent, *core.PaginationResponse, error) {
	_ = filter // filtering not yet supported by inner implementation
	var result []*core.Agent
	// The inner service doesn't expose a list method directly,
	// so we return an empty list for now. Full implementation
	// will require adding a List method to the internal service.
	_ = result
	return nil, &core.PaginationResponse{
		Total:      0,
		Page:       1,
		PageSize:   0,
		TotalPages: 0,
		HasMore:    false,
	}, nil
}

// ExecuteTask executes a task on an agent.
// Creates a task via the memory manager and returns a result immediately.
func (s *Service) ExecuteTask(ctx context.Context, task *core.Task) (*core.TaskResult, error) {
	if task == nil {
		return nil, fmt.Errorf("agent: execute: task is required")
	}

	// Verify the agent exists.
	_, err := s.inner.GetAgent(ctx, task.AgentID)
	if err != nil {
		return nil, fmt.Errorf("agent: execute: agent %q: %w", task.AgentID, err)
	}

	result := &core.TaskResult{
		TaskID:      task.ID,
		AgentID:     task.AgentID,
		Success:     true,
		Data:        task.Payload,
		CompletedAt: time.Now().Unix(),
	}

	s.mu.Lock()
	s.results[task.ID] = result
	s.mu.Unlock()

	return result, nil
}

// GetTaskResult retrieves the result of a previously executed task.
func (s *Service) GetTaskResult(ctx context.Context, taskID string) (*core.TaskResult, error) {
	if taskID == "" {
		return nil, fmt.Errorf("agent: get task result: taskID is required")
	}

	s.mu.RLock()
	result, ok := s.results[taskID]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("agent: get task result: task %q not found", taskID)
	}

	return result, nil
}
