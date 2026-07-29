// Package workflow — ExecutionScope and transactional State for the single Runner.
//
// Phase: P2 — Single Runner.
// ExecutionScope is the unified container for all runtime state during a single
// workflow execution. It owns the state, scheduling recovery data, lifecycle
// collection, and ordered events used by the single Runner.

package workflow

import (
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
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
	// Spec is the effective workflow IR committed at Runner safe points.
	Spec *WorkflowSpec `json:"spec"`

	// baseSpec is the immutable public input used to validate resumed executions.
	baseSpec *WorkflowSpec

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

	// loopHistory stores immutable snapshots for each completed loop iteration.
	loopHistory []LoopIteration
	loopMu      sync.RWMutex

	// eventSequence is the last ordered Runner lifecycle event sequence.
	eventSequence uint64
	eventMu       sync.Mutex

	// pendingInterrupts records unresolved human approval points.
	pendingInterrupts map[NodeID]PendingInterrupt
	interruptMu       sync.RWMutex

	// mutationIDs records mutations atomically applied at Runner safe points.
	mutationIDs []string
	mutationMu  sync.RWMutex

	// collector owns lifecycle data for this execution only.
	collector *ares_runtime.ExecutionCollector

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
		ExecutionID:       execID,
		Spec:              spec,
		baseSpec:          spec,
		state:             newExecutionState(),
		nodeStates:        make(map[NodeID]*NodeStatusValue),
		completed:         make(map[NodeID]bool),
		pendingInterrupts: make(map[NodeID]PendingInterrupt),
		collector:         ares_runtime.NewExecutionCollector(execID),
		startedAt:         time.Now(),
	}
}

// Collector returns execution-scoped lifecycle data.
func (s *ExecutionScope) Collector() *ares_runtime.ExecutionCollector {
	return s.collector
}

// SetCollector replaces the default execution-scoped collector.
func (s *ExecutionScope) SetCollector(collector *ares_runtime.ExecutionCollector) {
	if collector != nil {
		s.collector = collector
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

// StateSnapshot returns a copy of committed state for checkpointing.
func (s *ExecutionScope) StateSnapshot() map[string]any {
	return s.state.snapshot()
}

// RestoreState replaces committed state during checkpoint or child-scope restoration.
func (s *ExecutionScope) RestoreState(state map[string]any) {
	s.state.mu.Lock()
	s.state.base = cloneAnyMap(state)
	if s.state.base == nil {
		s.state.base = make(map[string]any)
	}
	s.state.pending = make(map[string]any)
	s.state.mu.Unlock()
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

// SetNodeError records a node's failure and populates its Error field.
func (s *ExecutionScope) SetNodeError(id NodeID, err error) {
	s.nsMu.Lock()
	if ns, ok := s.nodeStates[id]; ok {
		if err != nil {
			ns.Error = err.Error()
		}
	}
	s.nsMu.Unlock()
	s.SetNodeStatus(id, NodeStatusFailed)
	if err != nil {
		s.errMu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.errMu.Unlock()
	}
}

// RecordAttempt increments the retry attempt counter for the given node.
func (s *ExecutionScope) RecordAttempt(id NodeID) {
	s.nsMu.Lock()
	defer s.nsMu.Unlock()
	if ns, ok := s.nodeStates[id]; ok {
		ns.Attempts++
	}
}

// SetInitialState injects the initial execution input and variables into the
// execution scope's state. This must be called before the scheduler runs.
func (s *ExecutionScope) SetInitialState(input string, variables map[string]string) {
	w := s.Writer()
	w.Set("input", input)
	for k, v := range variables {
		w.Set(k, v)
	}
	s.CommitState()
}

// ── Execution lifecycle ──

// StartedAt returns when execution began.
func (s *ExecutionScope) StartedAt() time.Time { return s.startedAt }

// FinishedAt returns when execution completed.
func (s *ExecutionScope) FinishedAt() time.Time { return s.finishedAt }

// MarkFinished records the execution end time.
func (s *ExecutionScope) MarkFinished() { s.finishedAt = time.Now() }

// RecordLoopIteration appends an immutable snapshot of one committed iteration.
func (s *ExecutionScope) RecordLoopIteration(iteration int, nodeIDs []NodeID) {
	nodes := make([]NodeStatusValue, 0, len(nodeIDs))
	s.nsMu.RLock()
	for _, id := range nodeIDs {
		if state, ok := s.nodeStates[id]; ok {
			copyValue := *state
			copyValue.Output = cloneAnyMap(state.Output)
			nodes = append(nodes, copyValue)
		}
	}
	s.nsMu.RUnlock()
	s.loopMu.Lock()
	s.loopHistory = append(s.loopHistory, LoopIteration{
		Iteration: iteration,
		State:     s.StateSnapshot(),
		Nodes:     nodes,
	})
	s.loopMu.Unlock()
}

// ResetNodesForIteration resets selected nodes without changing execution identity.
func (s *ExecutionScope) ResetNodesForIteration(nodeIDs []NodeID) {
	s.nsMu.Lock()
	s.compMu.Lock()
	for _, id := range nodeIDs {
		s.nodeStates[id] = &NodeStatusValue{ID: id, Status: NodeStatusPending}
		delete(s.completed, id)
	}
	s.compMu.Unlock()
	s.nsMu.Unlock()
}

// RestoreNodeStates replaces runtime node state from a validated checkpoint.
func (s *ExecutionScope) RestoreNodeStates(states []NodeStatusValue) {
	s.nsMu.Lock()
	s.compMu.Lock()
	for i := range states {
		state := states[i]
		state.Output = cloneAnyMap(state.Output)
		s.nodeStates[state.ID] = &state
		if terminalNodeStatus(state.Status) {
			s.completed[state.ID] = true
		} else {
			delete(s.completed, state.ID)
		}
		if state.Output != nil {
			s.state.SetNodeOutput(state.ID, cloneAnyMap(state.Output))
		}
	}
	s.compMu.Unlock()
	s.nsMu.Unlock()
}

// RestoreLoopHistory replaces loop history from a validated checkpoint.
func (s *ExecutionScope) RestoreLoopHistory(history []LoopIteration) {
	s.loopMu.Lock()
	s.loopHistory = append([]LoopIteration(nil), history...)
	s.loopMu.Unlock()
}

// LoopHistory returns an immutable snapshot of completed iterations.
func (s *ExecutionScope) LoopHistory() []LoopIteration {
	s.loopMu.RLock()
	defer s.loopMu.RUnlock()
	return append([]LoopIteration(nil), s.loopHistory...)
}

// PublishOrderedEvent serializes one event publication with sequence allocation.
func (s *ExecutionScope) PublishOrderedEvent(publish func(uint64) error) error {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	s.eventSequence++
	return publish(s.eventSequence)
}

// PersistOrderedEvent atomically reserves an event sequence after durable state commits.
func (s *ExecutionScope) PersistOrderedEvent(
	persist func(uint64) error,
	publish func(uint64) error,
) error {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	next := s.eventSequence + 1
	if err := persist(next); err != nil {
		return err
	}
	s.eventSequence = next
	return publish(next)
}

// PersistOrderedEvents reserves a durable sequence range and publishes it in order.
func (s *ExecutionScope) PersistOrderedEvents(
	count uint64,
	persist func(uint64, uint64) error,
	publish func(uint64) error,
) error {
	if count == 0 {
		return nil
	}
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	first := s.eventSequence + 1
	last := s.eventSequence + count
	if err := persist(first, last); err != nil {
		return err
	}
	s.eventSequence = last
	for sequence := first; sequence <= last; sequence++ {
		if err := publish(sequence); err != nil {
			return err
		}
	}
	return nil
}

// EventSequence returns the last emitted execution event sequence.
func (s *ExecutionScope) EventSequence() uint64 {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	return s.eventSequence
}

// RestoreEventSequence restores the last emitted execution event sequence.
func (s *ExecutionScope) RestoreEventSequence(sequence uint64) {
	s.eventMu.Lock()
	s.eventSequence = sequence
	s.eventMu.Unlock()
}

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
	LoopHistory []LoopIteration    `json:"loop_history,omitempty"`
	StartedAt   time.Time          `json:"started_at"`
	FinishedAt  time.Time          `json:"finished_at"`
	Error       string             `json:"error,omitempty"`
	Duration    time.Duration      `json:"duration"`
}

// LoopIteration captures the committed state and node statuses for one loop iteration.
type LoopIteration struct {
	Iteration int               `json:"iteration"`
	State     map[string]any    `json:"state"`
	Nodes     []NodeStatusValue `json:"nodes"`
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
	s.loopMu.RLock()
	r.LoopHistory = append([]LoopIteration(nil), s.loopHistory...)
	s.loopMu.RUnlock()
	if err := s.Err(); err != nil {
		r.Status = NodeStatusFailed
		r.Error = err.Error()
	} else {
		r.Status = overallNodeStatus(r.NodeStates)
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

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func terminalNodeStatus(status NodeStatus) bool {
	switch status {
	case NodeStatusCompleted, NodeStatusFailed, NodeStatusCancelled,
		NodeStatusNotSelected, NodeStatusUnreachable, NodeStatusBlocked:
		return true
	default:
		return false
	}
}

func overallNodeStatus(states []*NodeStatusValue) NodeStatus {
	result := NodeStatusCompleted
	for _, state := range states {
		switch state.Status {
		case NodeStatusFailed:
			return NodeStatusFailed
		case NodeStatusPending, NodeStatusReady, NodeStatusRunning, NodeStatusInterrupted:
			result = NodeStatusInterrupted
		case NodeStatusCancelled:
			if result == NodeStatusCompleted {
				result = NodeStatusCancelled
			}
		}
	}
	return result
}
