package models

import "time"

// Task represents a recommendation task.
type Task struct {
	TaskID string `json:"task_id"`
	// SessionID scopes the task to a conversational session when the source
	// has one (REVIEW #61). Populated by callers that know the session
	// (e.g. DistillTask reading agent_checkpoints); empty for session-less
	// sources (experience search, collaboration tasks).
	SessionID string `json:"session_id,omitempty"`
	// TaskType and AgentType are MIRRORS: both carry the capability of the
	// agent that executes the task, kept as two fields only because
	// different consumers read different JSON keys ("task_type" is the
	// persistence/event key, "agent_type" the scheduler-facing one).
	// NewTask stamps both; writers that set one after construction must set
	// the other too, or the mirrors diverge (the known drift risk).
	// TODO(tech-debt): collapse to one field with both JSON keys served at
	// the serialization boundary.
	TaskType         AgentType      `json:"task_type"`
	AgentType        AgentType      `json:"agent_type"`
	UserProfile      *UserProfile   `json:"user_profile"`
	Context          *TaskContext   `json:"context"`
	Payload          map[string]any `json:"payload"`
	Priority         int            `json:"priority"`
	Deadline         time.Time      `json:"deadline"`
	UsedExperienceID string         `json:"used_experience_id,omitempty"` // Experience ID used for this task (bandit feedback).
	// StrategyID is the evolution strategy active when the task was submitted
	// (evolution loop closure). It is stamped once at submission and never
	// re-read, so the executor's task.completed/failed events attribute the
	// outcome to the strategy that actually chose the prompt/params.
	StrategyID string    `json:"strategy_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// TaskContext contains task dependencies and coordination data.
type TaskContext struct {
	Dependencies []string               `json:"dependencies"`
	DepResults   map[string]*TaskResult `json:"dep_results"`
	Coordination map[string]any         `json:"coordination"`
}

// NewTask creates a new Task.
func NewTask(taskID string, agentType AgentType, profile *UserProfile) *Task {
	return &Task{
		TaskID:      taskID,
		TaskType:    agentType,
		AgentType:   agentType,
		UserProfile: profile,
		Context:     &TaskContext{},
		Payload:     make(map[string]any),
		Priority:    0,
		CreatedAt:   time.Now(),
	}
}

// IsExpired checks if the task has expired.
func (t *Task) IsExpired() bool {
	return !t.Deadline.IsZero() && time.Now().After(t.Deadline)
}

// TaskResult represents the result of a task execution. The Items field
// retains []*RecommendItem for backward compatibility, but in general-purpose
// scenarios prefer using the Metadata field (or a dedicated Content field if
// added) as the primary data carrier. Items is only relevant when the task
// produces domain-specific structured results.
type TaskResult struct {
	TaskID    string           `json:"task_id"`
	AgentType AgentType        `json:"agent_type"`
	Success   bool             `json:"success"`
	Items     []*RecommendItem `json:"items"`
	Reason    string           `json:"reason"`
	Metadata  map[string]any   `json:"metadata"`
	Error     string           `json:"error"`
	Duration  time.Duration    `json:"duration"`
	CreatedAt time.Time        `json:"created_at"`
}

// NewTaskResult creates a new TaskResult.
func NewTaskResult(taskID string, agentType AgentType) *TaskResult {
	return &TaskResult{
		TaskID:    taskID,
		AgentType: agentType,
		Success:   false,
		Items:     make([]*RecommendItem, 0),
		Metadata:  make(map[string]any),
		CreatedAt: time.Now(),
	}
}

// SetSuccess marks the task as successful.
func (r *TaskResult) SetSuccess(items []*RecommendItem, reason string) {
	r.Success = true
	r.Items = items
	r.Reason = reason
}

// SetError marks the task as failed.
func (r *TaskResult) SetError(errMsg string) {
	r.Success = false
	r.Error = errMsg
}
