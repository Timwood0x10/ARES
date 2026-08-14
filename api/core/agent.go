// Package core provides core abstractions for agent operations.
package core

import "context"

// AgentStatus represents the current status of an agent.
type AgentStatus string

const (
	// AgentStatusReady indicates the agent is ready to accept tasks.
	AgentStatusReady AgentStatus = "ready"
	// AgentStatusRunning indicates the agent is currently executing a task.
	AgentStatusRunning AgentStatus = "running"
	// AgentStatusStopped indicates the agent has been stopped.
	AgentStatusStopped AgentStatus = "stopped"
	// AgentStatusError indicates the agent is in an error state.
	AgentStatusError AgentStatus = "error"
	// AgentStatusInitializing indicates the agent is being initialized.
	AgentStatusInitializing AgentStatus = "initializing"
)

// Agent represents an AI agent with its configuration and state.
type Agent struct {
	// ID is the unique identifier for the agent.
	ID string
	// Name is the display name of the agent.
	Name string
	// Type is the type of agent (e.g., "leader", "sub").
	Type string
	// Status is the current status of the agent.
	Status AgentStatus
	// SessionID is the associated session ID.
	SessionID string
	// Config is the agent configuration.
	Config map[string]interface{}
	// CreatedAt is the timestamp when the agent was created.
	CreatedAt int64
	// UpdatedAt is the timestamp when the agent was last updated.
	UpdatedAt int64
}

// AgentConfig represents configuration for creating an agent.
type AgentConfig struct {
	// ID is the unique identifier for the agent.
	ID string
	// Name is the display name of the agent.
	Name string
	// Type is the type of agent.
	Type string
	// Config is additional configuration parameters.
	Config map[string]interface{}
}

// Task represents a task to be executed by an agent.
type Task struct {
	// ID is the unique identifier for the task.
	ID string
	// AgentID is the agent that should execute this task.
	AgentID string
	// Type is the type of task.
	Type string
	// Payload is the task payload/data.
	Payload map[string]interface{}
	// Priority is the task priority (higher = more important).
	Priority int
	// Status is the task status.
	Status string
	// CreatedAt is the timestamp when the task was created.
	CreatedAt int64
	// StartedAt is the timestamp when the task was started.
	StartedAt int64
	// CompletedAt is the timestamp when the task was completed.
	CompletedAt int64
}

// TaskResult represents the result of a task execution.
type TaskResult struct {
	// TaskID is the ID of the task.
	TaskID string
	// AgentID is the ID of the agent that executed the task.
	AgentID string
	// Success indicates whether the task was successful.
	Success bool
	// Data is the result data.
	Data map[string]interface{}
	// Error is the error message if the task failed.
	Error string
	// CompletedAt is the timestamp when the task was completed.
	CompletedAt int64
}

// AgentRepository defines the interface for agent data access operations.
type AgentRepository interface {
	// Create creates a new agent.
	// Args:
	// ctx - operation context.
	// agent - the agent to create.
	// Returns error if creation fails.
	Create(ctx context.Context, agent *Agent) error

	// Get retrieves an agent by ID.
	// Args:
	// ctx - operation context.
	// agentID - the agent identifier.
	// Returns the agent or error if not found.
	Get(ctx context.Context, agentID string) (*Agent, error)

	// Update updates an existing agent.
	// Args:
	// ctx - operation context.
	// agent - the agent to update.
	// Returns error if update fails.
	Update(ctx context.Context, agent *Agent) error

	// Delete deletes an agent by ID.
	// Args:
	// ctx - operation context.
	// agentID - the agent identifier.
	// Returns error if deletion fails.
	Delete(ctx context.Context, agentID string) error

	// List lists agents with optional filtering.
	// Args:
	// ctx - operation context.
	// filter - optional filter criteria.
	// Returns list of agents or error.
	List(ctx context.Context, filter *AgentFilter) ([]*Agent, error)

	// Count returns the number of agents matching the filter, before
	// pagination is applied. Used to report accurate pagination totals.
	Count(ctx context.Context, filter *AgentFilter) (int, error)
}

// AgentFilter represents filter criteria for listing agents.
type AgentFilter struct {
	// Type filters by agent type.
	Type string
	// Status filters by agent status.
	Status AgentStatus
	// SessionID filters by session ID.
	SessionID string
	// Pagination represents pagination parameters.
	Pagination *PaginationRequest
}

// AgentService defines the interface for agent business logic operations.
type AgentService interface {
	// CreateAgent creates a new agent with the given configuration.
	// Args:
	// ctx - operation context.
	// config - the agent configuration.
	// Returns the created agent or error.
	CreateAgent(ctx context.Context, config *AgentConfig) (*Agent, error)

	// GetAgent retrieves an agent by ID.
	// Args:
	// ctx - operation context.
	// agentID - the agent identifier.
	// Returns the agent or error if not found.
	GetAgent(ctx context.Context, agentID string) (*Agent, error)

	// UpdateAgent updates an existing agent.
	// Args:
	// ctx - operation context.
	// agentID - the agent identifier.
	// updates - the fields to update.
	// Returns the updated agent or error.
	UpdateAgent(ctx context.Context, agentID string, updates map[string]interface{}) (*Agent, error)

	// DeleteAgent deletes an agent and its associated data.
	// Args:
	// ctx - operation context.
	// agentID - the agent identifier.
	// Returns error if deletion fails.
	DeleteAgent(ctx context.Context, agentID string) error

	// ListAgents lists agents with optional filtering.
	// Args:
	// ctx - operation context.
	// filter - optional filter criteria.
	// Returns list of agents and pagination info, or error.
	ListAgents(ctx context.Context, filter *AgentFilter) ([]*Agent, *PaginationResponse, error)

	// ExecuteTask executes a task on an agent.
	// Args:
	// ctx - operation context.
	// task - the task to execute.
	// Returns the task result or error.
	ExecuteTask(ctx context.Context, task *Task) (*TaskResult, error)

	// GetTaskResult retrieves the result of a task.
	// Args:
	// ctx - operation context.
	// taskID - the task identifier.
	// Returns the task result or error if not found.
	GetTaskResult(ctx context.Context, taskID string) (*TaskResult, error)
}
