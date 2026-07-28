// Package workflow — ExecutionScope and transactional State for the single Runner.
//
// Phase: P2 — Single Runner.
// ExecutionScope is the unified container for all runtime state during a single
// workflow execution. It replaces the scattered maps, mutexes, and channels
// previously shared between engine.Executor, engine.DynamicExecutor, and
// graph.Graph.

package workflow

import (
	"fmt"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────────────────
// NodeStatusValue — runtime execution status of a single node
// ──────────────────────────────────────────────────────────────────────

// NodeStatusValue tracks the runtime execution status of a single node.
type NodeStatusValue struct {
	// ID is the node ID.
	ID NodeID `json:"id"`
	// Status is the current execution status.
	Status NodeStatus `json:"status"`
	// Output is the node's output data after successful execution.
	Output map[string]any `json:"output,omitempty"`
	// Error is the error message if the node failed.
	Error string `json:"error,omitempty"`
	// StartedAt is when the node started executing.
	StartedAt time.Time `json:"started_at,omitempty"`
	// FinishedAt is when the node completed (or failed).
	FinishedAt time.Time `json:"finished_at,omitempty"`
	// Attempts counts how many times the node has been retried.
	Attempts int `json:"attempts"`
}

// ──────────────────────────────────────────────────────────────────────
// StateView — transactional read/write interface
// ──────────────────────────────────────────────────────────────────────

// StateView provides transactional read access to the execution state.
// Nodes read from this view during execution. All writes go through a
// write-set that is atomically committed after each node completes.
//
// This replaces the previous patterns:
//   - graph.State (shared mutable map, no isolation)
//   - engine.WorkflowExecution.Variables (manually locked)
//   - engine.OutputStore (separate store with string-only values)
type StateView interface {
	// Get retrieves a value by key. Returns false if the key does not exist.
	Get(key string) (any, bool)
	// GetNodeOutput retrieves the output of a completed node.
	GetNodeOutput(nodeID NodeID) (map[string]any, bool)
}

// StateWriter is the write-side of the transactional state.
// Only the Runner core holds a StateWriter; node implementations receive
// a read-only StateView.
type StateWriter interface {
	// Set writes a key-value pair into the current write-set.
	Set(key string, value any)
	// SetNodeOutput records a node's output.
	SetNodeOutput(nodeID NodeID, output map[string]any)
}

// executionState is the concrete transactional state implementation.
// It maintains a base map and a pending write-set. All reads see both.
// Writes are buffered until Commit() is called, at which point they are
// atomically merged into the base.
type executionState struct {
	mu       sync.RWMutex
	base     map[string]any
	pending  map[string]any // uncommitted writes (cleared on commit)
	nodeOuts map[NodeID]map[string]any
}

func newExecutionState() *executionState {
	return &executionState{
		base:     make(map[string]any),
		pending:  make(map[string]any),
		nodeOuts: make(map[NodeID]map[string]any),
	}
}

func (s *executionState) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.pending[key]; ok {
		return v, true
	}
	v, ok := s.base[key]
	return v, ok
}

func (s *executionState) GetNodeOutput(nodeID NodeID) (map[string]any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.nodeOuts[nodeID]
	return v, ok
}

func (s *executionState) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[key] = value
}

func (s *executionState) SetNodeOutput(nodeID NodeID, output map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeOuts[nodeID] = output
}

// commit atomically merges pending writes into the base.
func (s *executionState) commit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.pending {
		s.base[k] = v
	}
	s.pending = make(map[string]any)
}

// snapshot returns a copy of all committed state for checkpointing.
func (s *executionState) snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]any, len(s.base))
	for k, v := range s.base {
		cp[k] = v
	}
	return cp
}

// ──────────────────────────────────────────────────────────────────────
// ExecutionScope — unified runtime container for one workflow execution
// ──────────────────────────────────────────────────────────────────────

// ExecutionScope is the unified runtime context for a single workflow execution.
// It is created once per Execute() call and shared across all nodes in the
// workflow. After execution, the scope holds the final results and state.
//
// ExecutionScope replaces the disjoint state spread across:
//   - engine.WorkflowExecution + engine.OutputStore + engine.StepResult
//   - graph.State + graph.Result
type ExecutionScope struct {
	// ExecutionID is the unique identifier for this execution.
	ExecutionID string `json:"execution_id"`
	// Spec is the immutable workflow IR being executed.
	Spec *WorkflowSpec `json:"spec"`

	// state is the transactional execution state.
	state *executionState

	// nodeStates tracks each node's runtime status.
	nodeStates map[NodeID]*NodeStatusValue
	nsMu       sync.RWMutex

	// completed tracks which nodes have reached a terminal status.
	completed map[NodeID]bool
	compMu    sync.RWMutex

	// startedAt is when execution began.
	startedAt time.Time
	// finishedAt is when execution completed or failed.
	finishedAt time.Time

	// err holds the terminal execution error (if any).
	err   error
	errMu sync.RWMutex
}

// NewExecutionScope creates a new ExecutionScope for the given spec.
func NewExecutionScope(execID string, spec *WorkflowSpec) *ExecutionScope {
	if execID == "" {
		execID = fmt.Sprintf("exec-%d", time.Now().UnixNano())
	}
	return &ExecutionScope{
		ExecutionID: execID,
		Spec:        spec,
		state:       newExecutionState(),
		nodeStates:  make(map[NodeID]*NodeStatusValue),
		completed:   make(map[NodeID]bool),
		startedAt:   time.Now(),
	}
}

// State returns a read-only view of the execution state for nodes.
func (s *ExecutionScope) State() StateView {
	return s.state
}

// Writer returns a write-only view of the execution state for the Runner.
func (s *ExecutionScope) Writer() StateWriter {
	return s.state
}

// CommitState atomically commits pending writes into the base state.
func (s *ExecutionScope) CommitState() {
	s.state.commit()
}

// StateSnapshot returns a deep copy of committed state for checkpointing.
func (s *ExecutionScope) StateSnapshot() map[string]any {
	return s.state.snapshot()
}

// ── Node state tracking ──

// InitNodeStates initialises all node states to Pending.
func (s *ExecutionScope) InitNodeStates() {
	s.nsMu.Lock()
	defer s.nsMu.Unlock()
	for _, n := range s.Spec.Nodes {
		s.nodeStates[n.ID] = &NodeStatusValue{
			ID:     n.ID,
			Status: NodeStatusPending,
		}
	}
}

// SetNodeStatus transitions a node to a new status and records timestamps.
func (s *ExecutionScope) SetNodeStatus(id NodeID, status NodeStatus) {
	s.nsMu.Lock()
	defer s.nsMu.Unlock()
	ns, ok := s.nodeStates[id]
	if !ok {
		ns = &NodeStatusValue{ID: id}
		s.nodeStates[id] = ns
	}
	now := time.Now()
	if status == NodeStatusRunning && ns.Status == NodeStatusPending {
		ns.StartedAt = now
	}
	ns.Status = status
	switch status {
	case NodeStatusCompleted, NodeStatusFailed, NodeStatusCancelled:
		ns.FinishedAt = now
		s.compMu.Lock()
		s.completed[id] = true
		s.compMu.Unlock()
	case NodeStatusNotSelected, NodeStatusUnreachable, NodeStatusBlocked:
		ns.FinishedAt = now
		s.compMu.Lock()
		s.completed[id] = true
		s.compMu.Unlock()
	}
}

// NodeStatus returns the current status of a node.
func (s *ExecutionScope) NodeStatus(id NodeID) NodeStatus {
	s.nsMu.RLock()
	defer s.nsMu.RUnlock()
	ns, ok := s.nodeStates[id]
	if !ok {
		return NodeStatusPending
	}
	return ns.Status
}

// NodeStates returns a snapshot of all node statuses.
func (s *ExecutionScope) NodeStates() []*NodeStatusValue {
	s.nsMu.RLock()
	defer s.nsMu.RUnlock()
	out := make([]*NodeStatusValue, 0, len(s.nodeStates))
	for _, ns := range s.nodeStates {
		out = append(out, ns)
	}
	return out
}

// IsCompleted returns true if the given node has reached a terminal status.
func (s *ExecutionScope) IsCompleted(id NodeID) bool {
	s.compMu.RLock()
	defer s.compMu.RUnlock()
	return s.completed[id]
}

// SetNodeOutput records a node's output and marks it completed.
// The output key-value pairs are also written to the pending state so that
// downstream condition evaluators (via StateView.Get) can read them.
func (s *ExecutionScope) SetNodeOutput(id NodeID, output map[string]any) {
	s.state.SetNodeOutput(id, output)
	s.nsMu.Lock()
	if ns, ok := s.nodeStates[id]; ok {
		ns.Output = output
	}
	s.nsMu.Unlock()
	// Expose output values in the pending state for condition evaluators.
	for k, v := range output {
		s.state.Set(k, v)
	}
	s.SetNodeStatus(id, NodeStatusCompleted)
}

// SetNodeError records a node's failure.
func (s *ExecutionScope) SetNodeError(id NodeID, err error) {
	s.SetNodeStatus(id, NodeStatusFailed)
	if err != nil {
		s.errMu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.errMu.Unlock()
	}
}

// ── Execution lifecycle ──

// StartedAt returns when execution began.
func (s *ExecutionScope) StartedAt() time.Time { return s.startedAt }

// FinishedAt returns when execution completed.
func (s *ExecutionScope) FinishedAt() time.Time { return s.finishedAt }

// MarkFinished records the execution end time.
func (s *ExecutionScope) MarkFinished() { s.finishedAt = time.Now() }

// Err returns the terminal execution error, if any.
func (s *ExecutionScope) Err() error {
	s.errMu.RLock()
	defer s.errMu.RUnlock()
	return s.err
}

// ── Result ──

// Result holds the final outcome of a workflow execution.
type Result struct {
	ExecutionID string             `json:"execution_id"`
	SpecID      string             `json:"spec_id"`
	Status      NodeStatus         `json:"status"`
	State       map[string]any     `json:"state,omitempty"`
	NodeStates  []*NodeStatusValue `json:"node_states"`
	StartedAt   time.Time          `json:"started_at"`
	FinishedAt  time.Time          `json:"finished_at"`
	Error       string             `json:"error,omitempty"`
	Duration    time.Duration      `json:"duration"`
}

// ToResult converts the scope to a final Result.
func (s *ExecutionScope) ToResult() *Result {
	r := &Result{
		ExecutionID: s.ExecutionID,
		SpecID:      s.Spec.ID,
		StartedAt:   s.startedAt,
		FinishedAt:  s.finishedAt,
		Duration:    s.finishedAt.Sub(s.startedAt),
		NodeStates:  s.NodeStates(),
	}
	if s.err != nil {
		r.Status = NodeStatusFailed
		r.Error = s.err.Error()
	} else {
		r.Status = NodeStatusCompleted
	}
	// Build final state: merge committed base state with all completed node outputs.
	state := s.StateSnapshot()
	if state == nil {
		state = make(map[string]any)
	}
	s.nsMu.RLock()
	for id, nsv := range s.nodeStates {
		if nsv.Status == NodeStatusCompleted && nsv.Output != nil {
			state[string(id)+".output"] = nsv.Output
		}
	}
	s.nsMu.RUnlock()
	// Also include node outputs from the transactional state.
	s.state.mu.RLock()
	for id, out := range s.state.nodeOuts {
		state[string(id)+".node_output"] = out
	}
	s.state.mu.RUnlock()
	r.State = state
	return r
}
