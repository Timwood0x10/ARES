// Package workflow — intermediate representation (IR) and unified Runner for
// DAG-based workflow execution. The IR is the single compilation target for
// all legacy APIs. The Runner is the single production execution path.
//
// The Runner consumes WorkflowSpec IR as the only production DAG execution
// path. Compatibility APIs compile into this IR before execution.

package workflow

import "time"

const (
	conditionTypeState  = "state"
	validationFieldJoin = "join"
)

// NodeID is a unique identifier for a node within a workflow.
type NodeID string

// WorkflowSpec is the intermediate representation of a workflow.
// It decouples workflow topology (nodes + edges) from execution semantics
// (scheduling, checkpointing, recovery), allowing the same IR to be
// compiled from engine.Workflow or graph.Graph and executed by the
// single Runner.
type WorkflowSpec struct {
	// ID is the unique workflow identifier.
	ID string `json:"id" yaml:"id"`
	// Nodes is the set of all nodes in the workflow.
	Nodes []NodeSpec `json:"nodes" yaml:"nodes"`
	// Edges is the set of all directed edges between nodes.
	Edges []EdgeSpec `json:"edges" yaml:"edges"`
	// Entries lists the entry-point node IDs. Only nodes reachable from
	// these entries are executed. If empty, all zero-in-degree nodes are
	// treated as entries (legacy behaviour).
	Entries []NodeID `json:"entries,omitempty" yaml:"entries,omitempty"`
	// Loop, when non-nil, wraps the workflow in a controlled loop.
	Loop *LoopSpec `json:"loop,omitempty" yaml:"loop,omitempty"`
	// Schedule defines execution constraints for the workflow.
	Schedule ScheduleSpec `json:"schedule" yaml:"schedule"`
}

// NodeSpec defines a single node in the workflow graph.
type NodeSpec struct {
	// ID is the unique node identifier within the workflow.
	ID NodeID `json:"id" yaml:"id"`
	// Name is a human-readable label for the node.
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// AgentType identifies the binding type ("echo", "writer", etc.).
	// At execution time, this is resolved through a Binding provider.
	AgentType string `json:"agent_type" yaml:"agent_type"`
	// Input is the node's input template or literal string. It may contain
	// template variables ({{.output.step_id}}) that are resolved at runtime.
	Input string `json:"input,omitempty" yaml:"input,omitempty"`
	// Timeout is the maximum duration for node execution. Zero means no timeout.
	Timeout time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	// Retry, when non-nil, defines the retry policy on transient failures.
	Retry *RetrySpec `json:"retry,omitempty" yaml:"retry,omitempty"`
	// Recovery, when non-nil, defines the recovery policy on hard failures.
	Recovery *RecoverySpec `json:"recovery,omitempty" yaml:"recovery,omitempty"`
	// Interrupt, when non-nil, marks this node as requiring human approval.
	Interrupt *InterruptSpec `json:"interrupt,omitempty" yaml:"interrupt,omitempty"`
	// Join defines how this node is activated when it has multiple incoming edges.
	// Default: JoinAll (AND-join for all activated incoming edges).
	Join JoinKind `json:"join,omitempty" yaml:"join,omitempty"`
	// Condition is evaluated after all required incoming edges have resolved.
	// A false condition marks the node not selected without executing it.
	Condition *ConditionExpr `json:"condition,omitempty" yaml:"condition,omitempty"`
	// SubWorkflow, when non-nil, nests another workflow as a sub-graph node.
	SubWorkflow *WorkflowSpec `json:"sub_workflow,omitempty" yaml:"sub_workflow,omitempty"`
	// Metadata carries opaque key-value pairs for tooling and provenance.
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// EdgeSpec defines a directed edge between two nodes.
type EdgeSpec struct {
	// From is the source node ID.
	From NodeID `json:"from" yaml:"from"`
	// To is the target node ID.
	To NodeID `json:"to" yaml:"to"`
	// Kind classifies the edge as a data dependency or control flow.
	// Default: DataDependency.
	Kind EdgeKind `json:"kind,omitempty" yaml:"kind,omitempty"`
	// Branch defines the branching strategy for the source node.
	// Used when the source has multiple outgoing control-flow edges.
	// Default: BranchMany (all satisfied edges are activated).
	Branch BranchKind `json:"branch,omitempty" yaml:"branch,omitempty"`
	// Group identifies a branch group. All outgoing edges with the same
	// BranchOne and Group belong to the same exclusive-or group.
	Group string `json:"group,omitempty" yaml:"group,omitempty"`
	// Priority influences the order of processing among ready nodes.
	// Higher values are processed first. Zero means default priority.
	Priority int `json:"priority,omitempty" yaml:"priority,omitempty"`
	// Cond, when non-nil, is the edge traversal condition.
	// A nil condition means the edge is always traversed.
	Cond *ConditionExpr `json:"cond,omitempty" yaml:"cond,omitempty"`
}

// ConditionExpr is a serializable condition expression.
// It replaces the current `json:"-"` pattern (engine.ConditionFunc, graph.Condition)
// that makes conditions invisible to serialization and checkpoint recovery.
type ConditionExpr struct {
	// Type identifies the expression language: "template", "expr", "cel".
	Type string `json:"type" yaml:"type"`
	// Value is the expression string (e.g. "{{.output}} == 'approved'").
	Value string `json:"value" yaml:"value"`
}

// RetrySpec defines retry behaviour on transient node failures.
type RetrySpec struct {
	MaxAttempts       int           `json:"max_attempts" yaml:"max_attempts"`
	InitialDelay      time.Duration `json:"initial_delay" yaml:"initial_delay"`
	MaxDelay          time.Duration `json:"max_delay" yaml:"max_delay"`
	BackoffMultiplier float64       `json:"backoff_multiplier" yaml:"backoff_multiplier"`
}

// RecoverySpec defines recovery behaviour on hard node failures.
type RecoverySpec struct {
	// Strategy is the recovery approach: "retry", "replace_node", "fail_fast".
	Strategy string `json:"strategy" yaml:"strategy"`
	// ReplacementAgent is the agent type to use as replacement (for replace_node).
	ReplacementAgent string `json:"replacement_agent,omitempty" yaml:"replacement_agent,omitempty"`
}

// InterruptSpec marks a node as requiring human approval before execution.
type InterruptSpec struct {
	// Message is the prompt shown to the human approver.
	Message string `json:"message" yaml:"message"`
	// TimeoutSec is the maximum wait time for human approval.
	// Zero means no timeout (wait indefinitely).
	TimeoutSec int `json:"timeout_sec,omitempty" yaml:"timeout_sec,omitempty"`
	// AutoAction is the default action when the interrupt times out:
	// "skip", "approve", "fallback".
	AutoAction string `json:"auto_action,omitempty" yaml:"auto_action,omitempty"`
}

// LoopSpec defines controlled loop behaviour for a workflow.
type LoopSpec struct {
	// MaxIterations is the maximum number of loop iterations.
	MaxIterations int `json:"max_iterations" yaml:"max_iterations"`
	// LoopNodes lists the node IDs that form the loop body, in execution order.
	LoopNodes []NodeID `json:"loop_nodes" yaml:"loop_nodes"`
}

// ScheduleSpec defines execution constraints for the workflow.
type ScheduleSpec struct {
	// MaxParallel is the maximum number of nodes that can execute concurrently.
	// Zero defaults to 1 (sequential execution).
	MaxParallel int `json:"max_parallel,omitempty" yaml:"max_parallel,omitempty"`
}

// ──────────────────────────────────────────────────────────────────────
// Builder API helper (for §8 usability goal)
// ──────────────────────────────────────────────────────────────────────

// NewWorkflow creates a new workflow spec builder.
func NewWorkflow(id string) *WorkflowSpec {
	return &WorkflowSpec{
		ID:       id,
		Nodes:    make([]NodeSpec, 0),
		Edges:    make([]EdgeSpec, 0),
		Entries:  make([]NodeID, 0),
		Schedule: ScheduleSpec{MaxParallel: 1},
	}
}

// AddNode appends a node and returns the builder.
// Duplicate detection is handled by the validator and the engine layer.
func (s *WorkflowSpec) AddNode(n NodeSpec) *WorkflowSpec {
	s.Nodes = append(s.Nodes, n)
	return s
}

// AddEdge appends an edge and returns the builder.
// Duplicate detection is handled by the validator and the engine layer.
func (s *WorkflowSpec) AddEdge(e EdgeSpec) *WorkflowSpec {
	s.Edges = append(s.Edges, e)
	return s
}

// WithEntry marks one or more node IDs as entry points.
func (s *WorkflowSpec) WithEntry(ids ...NodeID) *WorkflowSpec {
	s.Entries = append(s.Entries, ids...)
	return s
}

// WithLoop sets the loop configuration.
func (s *WorkflowSpec) WithLoop(loop *LoopSpec) *WorkflowSpec {
	s.Loop = loop
	return s
}

// WithMaxParallel sets the maximum parallel execution count.
func (s *WorkflowSpec) WithMaxParallel(n int) *WorkflowSpec {
	s.Schedule.MaxParallel = n
	return s
}
