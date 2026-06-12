// Package agent provides in-memory repository implementation for development/testing.
package agent

import (
	"context"
	"fmt"
	"sync"

	"goagentx/api/core"
)

// MemoryRepository provides an in-memory implementation of AgentRepository.
// This is useful for development and testing without a database.
type MemoryRepository struct {
	mu     sync.RWMutex
	agents map[string]*core.Agent
}

// NewMemoryRepository creates a new in-memory agent repository.
// Returns new memory repository instance.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		agents: make(map[string]*core.Agent),
	}
}

// Create creates a new agent.
// Args:
// ctx - operation context.
// agent - the agent to create.
// Returns error if creation fails.
func (r *MemoryRepository) Create(ctx context.Context, agent *core.Agent) error {
	if agent == nil {
		return fmt.Errorf("agent is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[agent.ID]; exists {
		return fmt.Errorf("agent already exists: %s", agent.ID)
	}

	r.agents[agent.ID] = agent
	return nil
}

// Get retrieves an agent by ID.
// Args:
// ctx - operation context.
// agentID - the agent identifier.
// Returns the agent or error if not found.
func (r *MemoryRepository) Get(ctx context.Context, agentID string) (*core.Agent, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent ID is empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return nil, nil
	}

	// Return a copy to avoid mutation
	agentCopy := *agent
	return &agentCopy, nil
}

// Update updates an existing agent.
// Args:
// ctx - operation context.
// agent - the agent to update.
// Returns error if update fails.
func (r *MemoryRepository) Update(ctx context.Context, agent *core.Agent) error {
	if agent == nil {
		return fmt.Errorf("agent is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[agent.ID]; !exists {
		return fmt.Errorf("agent not found: %s", agent.ID)
	}

	r.agents[agent.ID] = agent
	return nil
}

// Delete deletes an agent by ID.
// Args:
// ctx - operation context.
// agentID - the agent identifier.
// Returns error if deletion fails.
func (r *MemoryRepository) Delete(ctx context.Context, agentID string) error {
	if agentID == "" {
		return fmt.Errorf("agent ID is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[agentID]; !exists {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	delete(r.agents, agentID)
	return nil
}

// List lists agents with optional filtering.
// Args:
// ctx - operation context.
// filter - optional filter criteria.
// Returns list of agents or error.
func (r *MemoryRepository) List(ctx context.Context, filter *core.AgentFilter) ([]*core.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]*core.Agent, 0, len(r.agents))
	for _, agent := range r.agents {
		// Apply filters
		if filter != nil {
			if filter.Type != "" && agent.Type != filter.Type {
				continue
			}
			if filter.Status != "" && agent.Status != filter.Status {
				continue
			}
			if filter.SessionID != "" && agent.SessionID != filter.SessionID {
				continue
			}
		}

		// Return a copy to avoid mutation
		agentCopy := *agent
		agents = append(agents, &agentCopy)
	}

	// Apply pagination
	if filter != nil && filter.Pagination != nil {
		limit := filter.Pagination.Limit
		if limit <= 0 {
			limit = filter.Pagination.PageSize
		}
		if limit <= 0 {
			limit = 100 // default limit
		}

		offset := filter.Pagination.Offset
		if offset <= 0 && filter.Pagination.Page > 0 {
			offset = (filter.Pagination.Page - 1) * limit
		}

		if offset >= len(agents) {
			return []*core.Agent{}, nil
		}

		if offset+limit > len(agents) {
			limit = len(agents) - offset
		}

		agents = agents[offset : offset+limit]
	}

	return agents, nil
}
