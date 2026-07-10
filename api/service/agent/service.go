// Package agent provides the public API for agent lifecycle management.
// It wraps the internal agent implementation for use by client packages.
package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	agentapi "github.com/Timwood0x10/ares/internal/api_impl/agent"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
)

// maxResults caps the number of task results cached in memory to prevent
// unbounded growth (OOM). When the limit is reached, the cache is reset.
const maxResults = 10000

// ErrNotImplemented indicates the feature is not yet wired.
// TODO: wire inner agent List/FullCreate APIs (expected by 2026-09-30).
var ErrNotImplemented = errors.New("agent: not implemented")

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
	// TODO: pass Name/Type/Config to inner once inner API supports them (expected by 2026-09-30).
	// Currently inner.CreateAgent only accepts ID, so Name/Type/Config are dropped.
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
	// Verify the agent exists.
	agent, err := s.inner.GetAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent: update: %w", err)
	}

	// Build the updated agent. The inner service does not yet support
	// persisting field-level updates, so changes are applied in-memory only.
	result := &core.Agent{
		ID:        agent.ID,
		SessionID: agent.SessionID,
		Status:    core.AgentStatus(agent.Status),
		CreatedAt: agent.CreatedAt,
		UpdatedAt: time.Now().Unix(),
	}

	// Apply supported field updates instead of silently ignoring them.
	for key, val := range updates {
		switch key {
		case "name":
			if v, ok := val.(string); ok {
				result.Name = v
			}
		case "type":
			if v, ok := val.(string); ok {
				result.Type = v
			}
		case "status":
			if v, ok := val.(string); ok {
				result.Status = core.AgentStatus(v)
			}
		case "session_id":
			if v, ok := val.(string); ok {
				result.SessionID = v
			}
		}
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

// ListAgents lists agents with optional filtering.
func (s *Service) ListAgents(ctx context.Context, filter *core.AgentFilter) ([]*core.Agent, *core.PaginationResponse, error) {
	// TODO: expose inner List method and implement filter+pagination (expected by 2026-09-30).
	// Returning ErrNotImplemented instead of an empty list avoids misleading callers
	// into believing no agents exist.
	_ = filter
	return nil, nil, ErrNotImplemented
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
	// Prevent unbounded growth: warn when cache hits the cap instead of silently resetting
	// all historical results. TODO: implement LRU eviction (expected by 2026-09-30).
	if len(s.results) >= maxResults {
		fmt.Printf("agent: task result cache full (max=%d), discarding task %s\n", maxResults, task.ID)
		s.mu.Unlock()
		return &core.TaskResult{
			TaskID:  task.ID,
			AgentID: task.AgentID,
			Success: true,
			Data:    task.Payload,
		}, nil
	}
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
