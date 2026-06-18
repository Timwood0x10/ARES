package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"goagentx/internal/core/models"
)

// Workflow errors.
var (
	ErrInvalidDependency   = errors.New("invalid dependency: step not found")
	ErrCycleDetected       = errors.New("cycle detected in workflow")
	ErrAgentTypeRegistered = errors.New("agent type already registered")
	ErrAgentTypeNotFound   = errors.New("agent type not found")
	ErrAgentResultNil      = errors.New("agent returned nil result")
	ErrWorkflowIncomplete  = errors.New("workflow incomplete")
	ErrInvalidLoader       = errors.New("invalid loader type")
	ErrDuplicateID         = errors.New("duplicate ID")
	ErrInterruptRejected   = errors.New("interrupt rejected by human")
	ErrInterruptStoreNil   = errors.New("interrupt store is nil")
	ErrInterruptHandlerNil = errors.New("interrupt handler is nil")
	ErrInterruptNotFound   = errors.New("interrupt result not found")
	ErrInterruptPointNil   = errors.New("interrupt point is nil")
)

// WorkflowStatus represents the execution status of a workflow.
type WorkflowStatus string

const (
	WorkflowStatusPending   WorkflowStatus = "pending"
	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusCompleted WorkflowStatus = "completed"
	WorkflowStatusFailed    WorkflowStatus = "failed"
	WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

// StepStatus represents the execution status of a workflow step.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
)

// Step represents a single step in a workflow.
type Step struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	AgentType   string            `json:"agent_type"`
	Input       string            `json:"input"`
	DependsOn   []string          `json:"depends_on"`
	Timeout     time.Duration     `json:"timeout"`
	RetryPolicy     *RetryPolicy      `json:"retry_policy,omitempty"`
	RecoveryPolicy  *RecoveryPolicy   `json:"recovery_policy,omitempty"`
	Interrupt       *InterruptConfig  `json:"interrupt,omitempty"`
	Status      StepStatus        `json:"status"`
	Output      string            `json:"output,omitempty"`
	Error       string            `json:"error,omitempty"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	FinishedAt  time.Time         `json:"finished_at,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// RecoveryStrategy classifies the recovery approach for a failed step.
type RecoveryStrategy string

const (
	RecoveryRetry       RecoveryStrategy = "retry"
	RecoveryReplaceNode RecoveryStrategy = "replace_node"
	RecoveryFailFast    RecoveryStrategy = "fail_fast"
)

// RecoveryPolicy defines how the engine should recover when a step fails.
type RecoveryPolicy struct {
	Strategy         RecoveryStrategy `json:"strategy"`
	MaxAttempts      int              `json:"max_attempts,omitempty"`
	ReplacementAgent string           `json:"replacement_agent,omitempty"`
}

// StepFailure contains the context for a step failure that may be recoverable.
type StepFailure struct {
	ExecutionID string
	WorkflowID  string
	StepID      string
	Error       string
	Input       string
}

// RecoveryDecision is the outcome of a recovery handler invocation.
type RecoveryDecision struct {
	Strategy RecoveryStrategy
	NewStep  *Step // populated for replace_node
}

// StepRecoveryHandler defines the interface for recovering failed workflow steps.
// Implementations receive the failure context and the mutable DAG, and return
// a decision specifying whether to retry, replace, or fail.
type StepRecoveryHandler interface {
	RecoverStep(ctx context.Context, failure StepFailure, dag *MutableDAG) (*RecoveryDecision, error)
}

// InterruptConfig marks a step as requiring human approval before execution.
type InterruptConfig struct {
	Message string         `json:"message"`
	Payload map[string]any `json:"payload,omitempty"`
}

// RetryPolicy defines retry behavior for a step.
type RetryPolicy struct {
	MaxAttempts       int           `json:"max_attempts"`
	InitialDelay      time.Duration `json:"initial_delay"`
	MaxDelay          time.Duration `json:"max_delay"`
	BackoffMultiplier float64       `json:"backoff_multiplier"`
}

// Workflow represents a workflow definition.
type Workflow struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Steps       []*Step           `json:"steps"`
	Variables   map[string]string `json:"variables,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// WorkflowExecution represents a running instance of a workflow.
type WorkflowExecution struct {
	ID         string                 `json:"id"`
	WorkflowID string                 `json:"workflow_id"`
	Status     WorkflowStatus         `json:"status"`
	StepStates map[string]*StepState  `json:"step_states"`
	Variables  map[string]interface{} `json:"variables"`
	Context    *models.TaskContext    `json:"context"`
	StartedAt  time.Time              `json:"started_at"`
	FinishedAt time.Time              `json:"finished_at,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

// StepState represents the runtime state of a step.
type StepState struct {
	StepID     string     `json:"step_id"`
	Status     StepStatus `json:"status"`
	Output     string     `json:"output,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at,omitempty"`
	FinishedAt time.Time  `json:"finished_at,omitempty"`
	Attempts   int        `json:"attempts"`
}

// WorkflowResult represents the final result of a workflow execution.
type WorkflowResult struct {
	ExecutionID string                 `json:"execution_id"`
	WorkflowID  string                 `json:"workflow_id"`
	Status      WorkflowStatus         `json:"status"`
	Output      map[string]interface{} `json:"output"`
	Error       string                 `json:"error,omitempty"`
	Duration    time.Duration          `json:"duration"`
	Steps       []*StepResult          `json:"steps"`
}

// StepResult represents the result of a step execution.
type StepResult struct {
	StepID   string            `json:"step_id"`
	Name     string            `json:"name"`
	Status   StepStatus        `json:"status"`
	Output   string            `json:"output,omitempty"`
	Error    string            `json:"error,omitempty"`
	Duration time.Duration     `json:"duration"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// DAG represents a directed acyclic graph of workflow steps.
type DAG struct {
	Nodes map[string]*DAGNode
	Edges map[string][]string
}

// DAGNode represents a node in the workflow DAG.
type DAGNode struct {
	StepID    string
	InDegree  int
	OutDegree int
}

// NewDAG creates a new DAG from workflow steps.
func NewDAG(steps []*Step) (*DAG, error) {
	dag := &DAG{
		Nodes: make(map[string]*DAGNode),
		Edges: make(map[string][]string),
	}

	for _, step := range steps {
		// H4 fix: check for duplicate step IDs instead of silently overwriting.
		if _, exists := dag.Nodes[step.ID]; exists {
			return nil, fmt.Errorf("duplicate step ID %q: %w", step.ID, ErrDuplicateID)
		}
		dag.Nodes[step.ID] = &DAGNode{
			StepID:    step.ID,
			InDegree:  0,
			OutDegree: 0,
		}
	}

	for _, step := range steps {
		for _, dep := range step.DependsOn {
			if _, ok := dag.Nodes[dep]; !ok {
				return nil, ErrInvalidDependency
			}
			dag.Edges[dep] = append(dag.Edges[dep], step.ID)
			dag.Nodes[step.ID].InDegree++
			dag.Nodes[dep].OutDegree++
		}
	}

	if dag.hasCycle() {
		return nil, ErrCycleDetected
	}

	return dag, nil
}

// hasCycle checks if the DAG contains a cycle.
func (d *DAG) hasCycle() bool {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(node string) bool
	dfs = func(node string) bool {
		visited[node] = true
		recStack[node] = true

		for _, neighbor := range d.Edges[node] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recStack[neighbor] {
				return true
			}
		}

		recStack[node] = false
		return false
	}

	for node := range d.Nodes {
		if !visited[node] {
			if dfs(node) {
				return true
			}
		}
	}

	return false
}

// GetExecutionOrder returns the topological sort order of steps.
func (d *DAG) GetExecutionOrder() ([]string, error) {
	inDegree := make(map[string]int)
	for node := range d.Nodes {
		inDegree[node] = d.Nodes[node].InDegree
	}

	queue := make([]string, 0)
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}

	result := make([]string, 0, len(d.Nodes))
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		for _, neighbor := range d.Edges[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	if len(result) != len(d.Nodes) {
		return nil, ErrCycleDetected
	}

	return result, nil
}
