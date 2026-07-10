// Package agent provides the public API for agent lifecycle management.
// It wraps the internal agent implementation for use by client packages.
package agent

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	agentapi "github.com/Timwood0x10/ares/internal/api_impl/agent"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
)

// maxResults caps the number of task results cached in memory to prevent
// unbounded growth (OOM). When the limit is reached, oldest entries are evicted.
const maxResults = 10000

// Service implements core.AgentService by wrapping the internal agent implementation.
type Service struct {
	inner   *agentapi.Service
	results map[string]*core.TaskResult
	order   *list.List // tracks insertion order for LRU eviction; values are task IDs
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
		order:   list.New(),
	}, nil
}

// CreateAgent creates a new agent with the given configuration.
func (s *Service) CreateAgent(ctx context.Context, config *core.AgentConfig) (*core.Agent, error) {
	if config == nil {
		return nil, fmt.Errorf("agent config is required")
	}
	agent, err := s.inner.CreateAgent(ctx, config.ID, config.Name, config.Type)
	if err != nil {
		return nil, fmt.Errorf("agent: create: %w", err)
	}
	return &core.Agent{
		ID:        agent.ID,
		Name:      agent.Name,
		Type:      agent.Type,
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
		Name:      agent.Name,
		Type:      agent.Type,
		SessionID: agent.SessionID,
		Status:    core.AgentStatus(agent.Status),
		CreatedAt: agent.CreatedAt,
	}, nil
}

// UpdateAgent updates an existing agent's configuration and persists changes.
// Supported update keys: "name", "type", "status".
func (s *Service) UpdateAgent(ctx context.Context, agentID string, updates map[string]interface{}) (*core.Agent, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent: update: agentID is required")
	}

	// Delegate to the inner service which persists the update and returns the
	// updated agent.
	agent, err := s.inner.UpdateAgent(ctx, agentID, updates)
	if err != nil {
		return nil, fmt.Errorf("agent: update: %w", err)
	}

	result := &core.Agent{
		ID:        agent.ID,
		Name:      agent.Name,
		Type:      agent.Type,
		SessionID: agent.SessionID,
		Status:    core.AgentStatus(agent.Status),
		CreatedAt: agent.CreatedAt,
		UpdatedAt: time.Now().Unix(),
	}

	return result, nil
}

// DeleteAgent deletes an agent and its associated data.
func (s *Service) DeleteAgent(ctx context.Context, agentID string) error {
	if err := s.inner.DeleteAgent(ctx, agentID); err != nil {
		return fmt.Errorf("agent: delete: %w", err)
	}
	return nil
}

// ListAgents lists agents with optional filtering and pagination.
func (s *Service) ListAgents(ctx context.Context, filter *core.AgentFilter) ([]*core.Agent, *core.PaginationResponse, error) {
	// Convert public filter to internal filter.
	innerFilter := &agentapi.AgentFilter{}
	if filter != nil {
		innerFilter.Type = filter.Type
		innerFilter.Status = agentapi.Status(filter.Status)
	}

	agents, err := s.inner.ListAgents(ctx, innerFilter)
	if err != nil {
		return nil, nil, fmt.Errorf("agent: list: %w", err)
	}

	// Convert internal agents to public core.Agent type.
	out := make([]*core.Agent, len(agents))
	for i, a := range agents {
		out[i] = &core.Agent{
			ID:        a.ID,
			Name:      a.Name,
			Type:      a.Type,
			SessionID: a.SessionID,
			Status:    core.AgentStatus(string(a.Status)),
			CreatedAt: a.CreatedAt,
		}
	}

	// Apply pagination.
	total := int64(len(out))
	page := 1
	pageSize := int(total)
	if pageSize < 1 {
		pageSize = 0
	}

	if filter != nil && filter.Pagination != nil {
		p := filter.Pagination
		// Calculate offset and limit.
		offset := p.Offset
		limit := p.Limit
		if limit <= 0 {
			limit = 20 // default page size
		}
		if p.Page > 0 && p.PageSize > 0 {
			offset = (p.Page - 1) * p.PageSize
			limit = p.PageSize
		}

		// Apply offset and limit.
		if offset > 0 && offset < len(out) {
			out = out[offset:]
		} else if offset >= len(out) {
			out = nil
		}
		if limit > 0 && limit < len(out) {
			out = out[:limit]
		}

		pageSize = limit
		if pageSize < 1 {
			pageSize = 0
		}
		if p.Page > 0 {
			page = p.Page
		}
	}

	totalPages := 1
	if pageSize > 0 {
		totalPages = int(total) / pageSize
		if int(total)%pageSize > 0 {
			totalPages++
		}
	}
	hasMore := page < totalPages

	return out, &core.PaginationResponse{
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
		HasMore:    hasMore,
	}, nil
}

// ExecuteTask executes a task on an agent.
// Creates a task via the memory manager and returns a result immediately.
func (s *Service) ExecuteTask(ctx context.Context, task *core.Task) (*core.TaskResult, error) {
	if task == nil {
		return nil, fmt.Errorf("agent: execute: task is required")
	}

	if task.ID == "" {
		return nil, fmt.Errorf("agent: execute: task ID is required")
	}

	// Delegate to the inner service which creates the task via the memory manager.
	createdTaskID, err := s.inner.ExecuteTask(ctx, task.AgentID, task.ID, task.Payload)
	if err != nil {
		return nil, fmt.Errorf("agent: execute: %w", err)
	}

	result := &core.TaskResult{
		TaskID:      createdTaskID,
		AgentID:     task.AgentID,
		Success:     true,
		CompletedAt: time.Now().Unix(),
	}

	s.mu.Lock()
	// LRU eviction: remove oldest entries when the cache exceeds the cap.
	if len(s.results) >= maxResults {
		if front := s.order.Front(); front != nil {
			oldestID := front.Value.(string)
			delete(s.results, oldestID)
			s.order.Remove(front)
		}
	}
	s.results[task.ID] = result
	s.order.PushBack(task.ID)
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
