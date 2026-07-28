// Package workflow — Scheduler for the single Runner.
//
// Phase: P2 — Single Runner.
// The Scheduler builds a ready queue from a WorkflowSpec, tracks in-degrees
// with conditional-edge awareness, and applies scheduling strategies.

package workflow

import (
	"fmt"
	"sort"
)

// ──────────────────────────────────────────────────────────────────────
// ScheduleStrategy — how ready nodes are prioritised
// ──────────────────────────────────────────────────────────────────────

// ScheduleStrategy selects the order in which ready nodes are executed.
type ScheduleStrategy int

const (
	// ScheduleFIFO executes nodes in the order they become ready (default).
	ScheduleFIFO ScheduleStrategy = iota
	// SchedulePriority executes nodes with higher Priority values first.
	SchedulePriority
)

// ──────────────────────────────────────────────────────────────────────
// Scheduler — ready-queue manager
// ──────────────────────────────────────────────────────────────────────

// Scheduler manages the ready queue for a workflow execution.
// It tracks in-degrees, evaluates conditional edges, and applies the
// configured scheduling strategy.
//
// The Scheduler is the unified replacement for:
//   - engine.Executor.runSteps() topological-order dispatch
//   - graph.Graph.executeReadyQueue() with scheduler.Select()
type Scheduler struct {
	spec     *WorkflowSpec
	strategy ScheduleStrategy

	// condEval evaluates a ConditionExpr against the current state.
	// Nil means all conditions are treated as unsatisfied.
	condEval func(expr *ConditionExpr) bool

	// inDegree tracks how many unsatisfied incoming edges each node has.
	inDegree map[NodeID]int

	// activated tracks how many incoming edges have been activated (for JoinAny/Merge).
	activated map[NodeID]int

	// completed tracks which nodes have reached a terminal status.
	completed map[NodeID]bool

	// readyQueue is the current set of nodes ready to execute.
	readyQueue []NodeID
	readySet   map[NodeID]bool

	// pending tracks nodes that will never become ready (unreachable).
	pending []NodeID

	// activeBranches tracks which BranchOne groups have had a branch taken.
	activeBranches map[branchGroupKey]bool
	// branchSkipped tracks nodes that were skipped by BranchOne.
	branchSkipped map[NodeID]bool
}

type branchGroupKey struct {
	from  NodeID
	group string
}

// NewScheduler creates a Scheduler from a WorkflowSpec.
func NewScheduler(spec *WorkflowSpec, strategy ScheduleStrategy) (*Scheduler, error) {
	if spec == nil {
		return nil, fmt.Errorf("spec must not be nil")
	}
	s := &Scheduler{
		spec:           spec,
		strategy:       strategy,
		inDegree:       make(map[NodeID]int),
		activated:      make(map[NodeID]int),
		completed:      make(map[NodeID]bool),
		readySet:       make(map[NodeID]bool),
		activeBranches: make(map[branchGroupKey]bool),
		branchSkipped:  make(map[NodeID]bool),
	}

	// Build initial in-degree map from data-dependency edges only.
	// Control-flow edges are evaluated at runtime when the source completes.
	for _, n := range spec.Nodes {
		s.inDegree[n.ID] = 0
	}
	for _, e := range spec.Edges {
		if e.Kind == EdgeDataDependency {
			s.inDegree[e.To]++
		}
	}

	// Seed the ready queue: entries (or zero-in-degree if entries empty)
	entries := spec.Entries
	if len(entries) == 0 {
		for id, deg := range s.inDegree {
			if deg == 0 {
				entries = append(entries, id)
			}
		}
	}

	for _, id := range entries {
		if _, ok := s.inDegree[id]; ok && !s.completed[id] {
			s.enqueue(id)
		}
	}

	return s, nil
}

// Strategy returns the scheduling strategy.
func (s *Scheduler) Strategy() ScheduleStrategy { return s.strategy }

// HasReady returns true if there are nodes ready to execute.
func (s *Scheduler) HasReady() bool { return len(s.readyQueue) > 0 }

// SetCondEval sets the condition evaluation callback.
// The callback receives a ConditionExpr and returns true if the condition
// is satisfied. When nil, all conditions are treated as unsatisfied.
func (s *Scheduler) SetCondEval(fn func(expr *ConditionExpr) bool) {
	s.condEval = fn
}

// Next returns the next node to execute, respecting the scheduling strategy.
// Returns "" if no nodes are ready.
func (s *Scheduler) Next() NodeID {
	if len(s.readyQueue) == 0 {
		return ""
	}

	var next NodeID
	switch s.strategy {
	case SchedulePriority:
		next = s.selectByPriority()
	default:
		next = s.readyQueue[0]
	}

	// Remove from queue
	s.removeFromQueue(next)
	return next
}

// selectByPriority picks the ready node with the highest Priority value.
// Edges with Priority set are preferred; nodes with no priority-defaults tie-break by ID.
func (s *Scheduler) selectByPriority() NodeID {
	if len(s.readyQueue) == 0 {
		return ""
	}

	// Build priority map from edges pointing to each node
	priority := make(map[NodeID]int)
	for _, e := range s.spec.Edges {
		if e.Priority > 0 {
			if p, ok := priority[e.To]; !ok || e.Priority > p {
				priority[e.To] = e.Priority
			}
		}
	}

	best := s.readyQueue[0]
	bestPrio := priority[best]
	for _, id := range s.readyQueue[1:] {
		p := priority[id]
		if p > bestPrio || (p == bestPrio && id < best) {
			best = id
			bestPrio = p
		}
	}
	return best
}

// removeFromQueue removes a node ID from the ready queue.
func (s *Scheduler) removeFromQueue(id NodeID) {
	for i, rid := range s.readyQueue {
		if rid == id {
			s.readyQueue = append(s.readyQueue[:i], s.readyQueue[i+1:]...)
			break
		}
	}
	delete(s.readySet, id)
}

// enqueue adds a node ID to the ready queue if not already there or completed.
func (s *Scheduler) enqueue(id NodeID) {
	if s.readySet[id] || s.completed[id] {
		return
	}
	s.readyQueue = append(s.readyQueue, id)
	s.readySet[id] = true
}

// ── Event handlers (called by Runner after each node completes) ──

// OnNodeCompleted is called when a node finishes successfully.
// It decrements in-degrees, evaluates control-flow edges, and enqueues
// newly ready nodes.
func (s *Scheduler) OnNodeCompleted(id NodeID) {
	s.completed[id] = true

	// Find all outgoing edges from this node
	outgoing := s.outgoingEdges(id)

	// Handle BranchOne groups: for each group, only one branch is taken.
	branches := s.groupByBranch(outgoing)

	for groupKey, groupEdges := range branches {
		if groupKey.group != "" && groupKey.from == id {
			if s.activeBranches[groupKey] {
				// This branch group already had a branch taken; skip.
				continue
			}
		}

		for _, e := range groupEdges {
			if e.Kind == EdgeControlFlow {
				// Evaluate condition
				condSatisfied := e.Cond == nil
				if !condSatisfied && s.condEval != nil {
					condSatisfied = s.condEval(e.Cond)
				}

				if e.Branch == BranchOne {
					// For BranchOne, only the first satisfied condition fires.
					if condSatisfied && !s.activeBranches[groupKey] {
						s.activeBranches[groupKey] = true
						s.evaluateTarget(e.To)
						// Mark all other targets in this group as skipped.
						for _, other := range groupEdges {
							if other.To != e.To {
								s.branchSkipped[other.To] = true
							}
						}
						break // only one branch per group
					}
					continue
				}

				// BranchMany: all satisfied conditions fire
				if condSatisfied {
					s.evaluateTarget(e.To)
				}
				continue
			}

			// DataDependency: always decrement in-degree
			s.inDegree[e.To]--
			if s.inDegree[e.To] <= 0 {
				s.evaluateTarget(e.To)
			}
		}
	}
}

// evaluateTarget checks whether a target node is ready to be enqueued.
// For JoinAll: all predecessors must be completed.
// For JoinAny: any predecessor completion triggers.
// For Merge: each arrival triggers.
func (s *Scheduler) evaluateTarget(target NodeID) {
	if s.completed[target] {
		return
	}

	// Find the node spec to check Join policy
	join := JoinAll // default
	for _, n := range s.spec.Nodes {
		if n.ID == target {
			join = n.Join
			break
		}
	}

	switch join {
	case JoinAll, "": // empty string = default JoinAll
		// Check if all data-dependency predecessors are completed
		allDone := true
		for _, e := range s.spec.Edges {
			if e.To == target && e.Kind == EdgeDataDependency {
				if !s.completed[e.From] {
					allDone = false
					break
				}
			}
		}
		if allDone && s.inDegree[target] <= 0 {
			s.enqueue(target)
		}

	case JoinAny:
		// First activated predecessor is enough
		if !s.completed[target] {
			s.enqueue(target)
		}

	case Merge:
		// Each activation triggers; enqueue even if already completed
		s.enqueue(target)
	}
}

// OnNodeFailed is called when a node fails. It marks downstream nodes as Blocked.
func (s *Scheduler) OnNodeFailed(id NodeID) {
	s.completed[id] = true

	for _, e := range s.spec.Edges {
		if e.From == id && e.Kind == EdgeDataDependency {
			// Mark downstream as blocked
			if !s.completed[e.To] {
				s.pending = append(s.pending, e.To)
			}
		}
	}
}

// Pending returns nodes that will never become ready (unreachable/blocked).
func (s *Scheduler) Pending() []NodeID { return s.pending }

// BranchSkipped returns true if the node was skipped by a BranchOne selection.
func (s *Scheduler) BranchSkipped(id NodeID) bool {
	return s.branchSkipped[id]
}

// ── Helpers ──

// outgoingEdges returns all edges originating from the given node.
func (s *Scheduler) outgoingEdges(from NodeID) []EdgeSpec {
	var out []EdgeSpec
	for _, e := range s.spec.Edges {
		if e.From == from {
			out = append(out, e)
		}
	}
	return out
}

// groupByBranch groups edges by (from, branch, group) for BranchOne handling.
func (s *Scheduler) groupByBranch(edges []EdgeSpec) map[branchGroupKey][]EdgeSpec {
	groups := make(map[branchGroupKey][]EdgeSpec)
	for _, e := range edges {
		key := branchGroupKey{from: e.From, group: e.Group}
		groups[key] = append(groups[key], e)
	}
	return groups
}

// PriorityQueue is reserved for future heap-based scheduling.
// The current SchedulePriority implementation uses a linear scan in
// selectByPriority(), which is optimal for the typical sub-100-node case.

// ──────────────────────────────────────────────────────────────────────
// Helper: topological sort (used by Runner for initialisation)
// ──────────────────────────────────────────────────────────────────────

// TopologicalSort returns the node IDs in topological order (BFS / Kahn).
// This is used by the Runner to determine a deterministic execution hint.
// It does NOT affect the actual execution order (which is driven by the
// Scheduler), but is used for progress reporting and deadlock detection.
func TopologicalSort(spec *WorkflowSpec) ([]NodeID, error) {
	if spec == nil {
		return nil, fmt.Errorf("spec must not be nil")
	}

	inDegree := make(map[NodeID]int)
	for _, n := range spec.Nodes {
		inDegree[n.ID] = 0
	}
	for _, e := range spec.Edges {
		if e.Kind == EdgeDataDependency {
			inDegree[e.To]++
		}
	}

	queue := make([]NodeID, 0)
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	// Sort for deterministic ordering
	sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })

	result := make([]NodeID, 0, len(spec.Nodes))
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		result = append(result, curr)

		for _, e := range spec.Edges {
			if e.From == curr && e.Kind == EdgeDataDependency {
				inDegree[e.To]--
				if inDegree[e.To] == 0 {
					queue = append(queue, e.To)
				}
			}
		}
		// Re-sort for deterministic ordering
		sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })
	}

	if len(result) != len(spec.Nodes) {
		return result, fmt.Errorf("cycle detected: sorted %d of %d nodes", len(result), len(spec.Nodes))
	}
	return result, nil
}
