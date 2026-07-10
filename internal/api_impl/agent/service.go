// Package agent provides high-level APIs for agent management.
package agentapi

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	memory "github.com/Timwood0x10/ares/internal/ares_memory"
)

// Service provides agent management operations.
type Service struct {
	memoryMgr memory.MemoryManager
	agents    map[string]*Agent
	agentsMu  sync.RWMutex
}

// NewService creates a new agent service instance.
// Args:
// memoryMgr - memory manager for session and task management.
// Returns new agent service instance.
func NewService(memoryMgr memory.MemoryManager) *Service {
	return &Service{
		memoryMgr: memoryMgr,
		agents:    make(map[string]*Agent),
	}
}

// CreateAgent creates a new agent with the given properties.
// Args:
// ctx - operation context.
// agentID - unique identifier for the agent.
// name - display name; may be empty.
// agentType - agent type; may be empty.
// Returns new agent instance or error.
func (s *Service) CreateAgent(ctx context.Context, agentID, name, agentType string) (*Agent, error) {
	if agentID == "" {
		return nil, ErrInvalidAgentID
	}

	// Create session for the agent
	sessionID, err := s.memoryMgr.CreateSession(ctx, agentID)
	if err != nil {
		return nil, err
	}

	agent := &Agent{
		ID:        agentID,
		Name:      name,
		Type:      agentType,
		SessionID: sessionID,
		Status:    StatusReady,
		CreatedAt: getCurrentTimestamp(),
	}

	// Store agent in map
	s.agentsMu.Lock()
	s.agents[agentID] = agent
	s.agentsMu.Unlock()

	return agent, nil
}

// GetAgent retrieves an agent by ID.
// Args:
// ctx - operation context.
// agentID - agent identifier.
// Returns agent instance or error if not found.
func (s *Service) GetAgent(ctx context.Context, agentID string) (*Agent, error) {
	if agentID == "" {
		return nil, ErrInvalidAgentID
	}

	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return nil, ErrAgentNotFound
	}

	// Return a copy to avoid external modification
	return &Agent{
		ID:        agent.ID,
		Name:      agent.Name,
		Type:      agent.Type,
		SessionID: agent.SessionID,
		Status:    agent.Status,
		CreatedAt: agent.CreatedAt,
	}, nil
}

// DeleteAgent deletes an agent and its associated data.
// Args:
// ctx - operation context.
// agentID - agent identifier.
// Returns error if deletion fails.
func (s *Service) DeleteAgent(ctx context.Context, agentID string) error {
	if agentID == "" {
		return ErrInvalidAgentID
	}

	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return ErrAgentNotFound
	}

	// Delete associated session if memory manager is available
	if s.memoryMgr != nil && agent.SessionID != "" {
		if err := s.memoryMgr.DeleteSession(ctx, agent.SessionID); err != nil {
			// Log error but don't fail the agent deletion
			// The session will eventually be cleaned up by TTL
			log.Warn("Failed to delete associated session", "session_id", agent.SessionID, "error", err)
		}
	}

	// Remove agent from map
	delete(s.agents, agentID)

	return nil
}

// ListAgents returns all agents, optionally filtered by type/status.
// Args:
// ctx - operation context.
// filter - optional filter (only Type and Status are supported; Pagination is handled by the caller).
// Returns list of agent copies or error.
func (s *Service) ListAgents(ctx context.Context, filter *AgentFilter) ([]*Agent, error) {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()

	out := make([]*Agent, 0, len(s.agents))
	for _, a := range s.agents {
		if filter != nil {
			if filter.Type != "" && string(a.Status) != filter.Type {
				continue
			}
			if filter.Status != "" && a.Status != filter.Status {
				continue
			}
		}
		// Return a copy to avoid external modification.
		out = append(out, &Agent{
			ID:        a.ID,
			SessionID: a.SessionID,
			Status:    a.Status,
			CreatedAt: a.CreatedAt,
		})
	}
	return out, nil
}

// UpdateAgent updates an existing agent's mutable fields (name, type, status).
// Returns a copy of the updated agent or error if the agent does not exist.
// Args:
// ctx - operation context.
// agentID - agent identifier.
// updates - map of field names to new values. Supported keys: "name", "type", "status".
// Returns updated agent copy or error.
func (s *Service) UpdateAgent(ctx context.Context, agentID string, updates map[string]interface{}) (*Agent, error) {
	if agentID == "" {
		return nil, ErrInvalidAgentID
	}

	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return nil, ErrAgentNotFound
	}

	// Apply supported field updates.
	for key, val := range updates {
		switch key {
		case "name":
			if v, ok := val.(string); ok {
				agent.Name = v
			}
		case "type":
			if v, ok := val.(string); ok {
				agent.Type = v
			}
		case "status":
			if v, ok := val.(string); ok {
				agent.Status = Status(v)
			}
		}
	}

	// Return a copy to avoid external modification.
	return &Agent{
		ID:        agent.ID,
		Name:      agent.Name,
		Type:      agent.Type,
		SessionID: agent.SessionID,
		Status:    agent.Status,
		CreatedAt: agent.CreatedAt,
	}, nil
}

// ExecuteTask creates a task for the agent via the memory manager and returns the
// created task ID. The agent's session ID is used as the task session context.
// Args:
// ctx - operation context.
// agentID - agent identifier.
// taskID - caller-assigned task identifier for tracking.
// payload - optional task data (serialized to JSON for storage).
// Returns created task ID or error.
func (s *Service) ExecuteTask(ctx context.Context, agentID, taskID string, payload map[string]interface{}) (string, error) {
	if agentID == "" {
		return "", ErrInvalidAgentID
	}

	s.agentsMu.RLock()
	agent, exists := s.agents[agentID]
	s.agentsMu.RUnlock()

	if !exists {
		return "", ErrAgentNotFound
	}

	// Serialize payload to string for storage.
	input := ""
	if len(payload) > 0 {
		data, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshal task payload: %w", err)
		}
		input = string(data)
	}

	// Create task via memory manager.
	createdTaskID, err := s.memoryMgr.CreateTask(ctx, agent.SessionID, agentID, input)
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	return createdTaskID, nil
}

// Agent filter fields.
type AgentFilter struct {
	Type   string
	Status Status
}

// Agent represents an AI agent with session management.
type Agent struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	SessionID string `json:"session_id"`
	Status    Status `json:"status"`
	CreatedAt int64  `json:"created_at"`
}

// Status represents the current status of an agent.
type Status string

const (
	StatusReady   Status = "ready"
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusError   Status = "error"
)

// getCurrentTimestamp returns the current Unix timestamp in seconds.
func getCurrentTimestamp() int64 {
	return time.Now().Unix()
}
