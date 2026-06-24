// Package agent provides high-level APIs for agent management.
package agent

import (
	"context"
	"log/slog"
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

// CreateAgent creates a new agent with default configuration.
// Args:
// ctx - operation context.
// agentID - unique identifier for the agent.
// Returns new agent instance or error.
func (s *Service) CreateAgent(ctx context.Context, agentID string) (*Agent, error) {
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
			slog.Warn("Failed to delete associated session", "session_id", agent.SessionID, "error", err)
		}
	}

	// Remove agent from map
	delete(s.agents, agentID)

	return nil
}

// Agent represents an AI agent with session management.
type Agent struct {
	ID        string `json:"id"`
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
