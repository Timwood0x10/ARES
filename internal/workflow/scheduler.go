// Package workflow provides scheduling for the unified workflow Runner.
package workflow

import (
	"fmt"
	"sort"
)

// ScheduleStrategy selects the order in which ready nodes execute.
type ScheduleStrategy int

const (
	// ScheduleFIFO executes nodes in activation order.
	ScheduleFIFO ScheduleStrategy = iota
	// SchedulePriority executes nodes with higher incoming edge priority first.
	SchedulePriority
)

type edgeActivation string

const (
	edgePending  edgeActivation = "pending"
	edgeActive   edgeActivation = "active"
	edgeArrived  edgeActivation = "arrived"
	edgeInactive edgeActivation = "inactive"
)

// SchedulerSnapshot is the durable scheduler state used by checkpoint recovery.
type SchedulerSnapshot struct {
	EdgeStates     []string        `json:"edge_states"`
	Completed      map[NodeID]bool `json:"completed"`
	ReadyQueue     []NodeID        `json:"ready_queue"`
	Pending        []NodeID        `json:"pending"`
	BranchSkipped  map[NodeID]bool `json:"branch_skipped"`
	ActiveBranches map[string]bool `json:"active_branches"`
}

// Scheduler manages edge activation and the ready queue for one execution.
type Scheduler struct {
	spec           *WorkflowSpec
	strategy       ScheduleStrategy
	condEval       func(expr *ConditionExpr) bool
	edgeStates     []edgeActivation
	incoming       map[NodeID][]int
	outgoing       map[NodeID][]int
	completed      map[NodeID]bool
	readyQueue     []NodeID
	readySet       map[NodeID]bool
	pending        []NodeID
	activeBranches map[branchGroupKey]bool
	branchSkipped  map[NodeID]bool
}

type branchGroupKey struct {
	from  NodeID
	group string
}

// NewScheduler creates a scheduler from a workflow specification.
func NewScheduler(spec *WorkflowSpec, strategy ScheduleStrategy) (*Scheduler, error) {
	if spec == nil {
		return nil, fmt.Errorf("spec must not be nil")
	}
	scheduler := &Scheduler{
		spec:           spec,
		strategy:       strategy,
		edgeStates:     make([]edgeActivation, len(spec.Edges)),
		incoming:       make(map[NodeID][]int),
		outgoing:       make(map[NodeID][]int),
		completed:      make(map[NodeID]bool),
		readySet:       make(map[NodeID]bool),
		activeBranches: make(map[branchGroupKey]bool),
		branchSkipped:  make(map[NodeID]bool),
	}
	for i, edge := range spec.Edges {
		scheduler.incoming[edge.To] = append(scheduler.incoming[edge.To], i)
		scheduler.outgoing[edge.From] = append(scheduler.outgoing[edge.From], i)
		if edge.Kind == EdgeDataDependency {
			scheduler.edgeStates[i] = edgeActive
		} else {
			scheduler.edgeStates[i] = edgePending
		}
	}
	entries := append([]NodeID(nil), spec.Entries...)
	if len(entries) == 0 {
		entries = scheduler.zeroInDegreeNodes()
	}
	for _, id := range entries {
		if scheduler.hasNode(id) {
			scheduler.enqueue(id)
		}
	}
	return scheduler, nil
}

// Strategy returns the scheduling strategy.
func (s *Scheduler) Strategy() ScheduleStrategy { return s.strategy }

// HasReady reports whether at least one activation token is ready.
func (s *Scheduler) HasReady() bool { return len(s.readyQueue) > 0 }

// SetCondEval sets the condition evaluation callback.
func (s *Scheduler) SetCondEval(fn func(expr *ConditionExpr) bool) { s.condEval = fn }

// Next returns and removes the next ready node.
func (s *Scheduler) Next() NodeID {
	if len(s.readyQueue) == 0 {
		return ""
	}
	next := s.readyQueue[0]
	if s.strategy == SchedulePriority {
		next = s.selectByPriority()
	}
	s.removeFromQueue(next)
	return next
}

// NextWithSelector returns the custom-selected ready node or an error.
func (s *Scheduler) NextWithSelector(selector func([]NodeID) NodeID) (NodeID, error) {
	if len(s.readyQueue) == 0 {
		return "", nil
	}
	if selector == nil {
		return s.Next(), nil
	}
	ready := append([]NodeID(nil), s.readyQueue...)
	next := selector(ready)
	if next == "" {
		return "", fmt.Errorf("ready selector returned an empty node for %v", ready)
	}
	if !s.readySet[next] {
		return "", fmt.Errorf("ready selector returned unavailable node %q", next)
	}
	s.removeFromQueue(next)
	return next, nil
}

// OnNodeCompleted advances every outgoing edge after successful execution.
func (s *Scheduler) OnNodeCompleted(id NodeID) {
	s.OnNodeCompletedWithRoute(id, "")
}

// OnNodeCompletedWithRoute advances outgoing edges and optionally selects one route.
func (s *Scheduler) OnNodeCompletedWithRoute(id, route NodeID) {
	s.completed[id] = true
	indices := s.outgoing[id]
	groups := s.controlGroups(indices)
	for _, index := range indices {
		if s.spec.Edges[index].Kind == EdgeDataDependency {
			s.arrive(index)
		}
	}
	for key, group := range groups {
		s.resolveControlGroup(key, group, route)
	}
}

// OnNodeNotSelected deactivates every outgoing path from a rejected node.
func (s *Scheduler) OnNodeNotSelected(id NodeID) {
	s.completed[id] = true
	for _, index := range s.outgoing[id] {
		s.markInactive(index)
	}
}

// OnNodeFailed records failure and prevents required downstream execution.
func (s *Scheduler) OnNodeFailed(id NodeID) {
	s.completed[id] = true
	for _, index := range s.outgoing[id] {
		edge := s.spec.Edges[index]
		s.markInactive(index)
		if edge.Kind == EdgeDataDependency {
			s.pending = appendUniqueNode(s.pending, edge.To)
		}
	}
}

// Pending returns nodes that cannot become ready after a required failure.
func (s *Scheduler) Pending() []NodeID { return append([]NodeID(nil), s.pending...) }

// BranchSkipped reports whether all routes to a node were explicitly rejected.
func (s *Scheduler) BranchSkipped(id NodeID) bool { return s.branchSkipped[id] }

// Snapshot returns a durable copy of the scheduler state.
func (s *Scheduler) Snapshot() SchedulerSnapshot {
	edges := make([]string, len(s.edgeStates))
	for i, state := range s.edgeStates {
		edges[i] = string(state)
	}
	branches := make(map[string]bool, len(s.activeBranches))
	for key, active := range s.activeBranches {
		branches[branchKeyString(key)] = active
	}
	return SchedulerSnapshot{
		EdgeStates:     edges,
		Completed:      cloneNodeBoolMap(s.completed),
		ReadyQueue:     append([]NodeID(nil), s.readyQueue...),
		Pending:        append([]NodeID(nil), s.pending...),
		BranchSkipped:  cloneNodeBoolMap(s.branchSkipped),
		ActiveBranches: branches,
	}
}

// Restore replaces scheduler state from a validated checkpoint snapshot.
func (s *Scheduler) Restore(snapshot SchedulerSnapshot) error {
	if len(snapshot.EdgeStates) != len(s.edgeStates) {
		return fmt.Errorf("scheduler edge state count %d does not match spec edge count %d", len(snapshot.EdgeStates), len(s.edgeStates))
	}
	for i, raw := range snapshot.EdgeStates {
		state := edgeActivation(raw)
		if !validEdgeActivation(state) {
			return fmt.Errorf("scheduler edge %d has invalid activation state %q", i, raw)
		}
		s.edgeStates[i] = state
	}
	s.completed = cloneNodeBoolMap(snapshot.Completed)
	s.readyQueue = append([]NodeID(nil), snapshot.ReadyQueue...)
	s.readySet = make(map[NodeID]bool)
	for _, id := range s.readyQueue {
		if s.joinKind(id) != Merge {
			s.readySet[id] = true
		}
	}
	s.pending = append([]NodeID(nil), snapshot.Pending...)
	s.branchSkipped = cloneNodeBoolMap(snapshot.BranchSkipped)
	s.activeBranches = make(map[branchGroupKey]bool)
	for raw, active := range snapshot.ActiveBranches {
		key, err := parseBranchKey(raw)
		if err != nil {
			return err
		}
		s.activeBranches[key] = active
	}
	return nil
}

func (s *Scheduler) resolveControlGroup(key branchGroupKey, indices []int, route NodeID) {
	branchOne := false
	for _, index := range indices {
		if s.spec.Edges[index].Branch == BranchOne {
			branchOne = true
			break
		}
	}
	if branchOne {
		s.resolveBranchOne(key, indices, route)
		return
	}
	for _, index := range indices {
		edge := s.spec.Edges[index]
		if (route != "" && edge.To == route) || (route == "" && s.conditionSatisfied(edge.Cond)) {
			s.arrive(index)
		} else {
			s.branchSkipped[edge.To] = true
			s.markInactive(index)
		}
	}
}

func (s *Scheduler) resolveBranchOne(key branchGroupKey, indices []int, route NodeID) {
	selected := -1
	for _, index := range indices {
		edge := s.spec.Edges[index]
		if (route != "" && edge.To == route) || (route == "" && s.conditionSatisfied(edge.Cond)) {
			selected = index
			break
		}
	}
	if selected >= 0 {
		s.activeBranches[key] = true
	}
	for _, index := range indices {
		if index == selected {
			s.arrive(index)
		} else {
			s.branchSkipped[s.spec.Edges[index].To] = true
			s.markInactive(index)
		}
	}
}

func (s *Scheduler) arrive(index int) {
	edge := s.spec.Edges[index]
	repeatable := s.joinKind(edge.From) == Merge
	if s.edgeStates[index] == edgeInactive || (s.edgeStates[index] == edgeArrived && !repeatable) {
		return
	}
	s.edgeStates[index] = edgeArrived
	target := edge.To
	if s.joinKind(target) == Merge {
		s.enqueueMerge(target)
		return
	}
	s.evaluateTarget(target)
}

func (s *Scheduler) markInactive(index int) {
	if s.edgeStates[index] == edgeArrived || s.edgeStates[index] == edgeInactive {
		return
	}
	s.edgeStates[index] = edgeInactive
	target := s.spec.Edges[index].To
	if s.allIncomingInactive(target) {
		for _, outgoing := range s.outgoing[target] {
			s.markInactive(outgoing)
		}
		return
	}
	s.evaluateTarget(target)
}

func (s *Scheduler) evaluateTarget(target NodeID) {
	if s.completed[target] || s.readySet[target] {
		return
	}
	incoming := s.incoming[target]
	switch s.joinKind(target) {
	case JoinAny:
		for _, index := range incoming {
			if s.edgeStates[index] == edgeArrived {
				s.enqueue(target)
				return
			}
		}
	case Merge:
		return
	default:
		hasArrival := len(incoming) == 0
		for _, index := range incoming {
			switch s.edgeStates[index] {
			case edgeArrived:
				hasArrival = true
			case edgeInactive:
				continue
			default:
				return
			}
		}
		if hasArrival {
			s.enqueue(target)
		}
	}
}

func (s *Scheduler) conditionSatisfied(expr *ConditionExpr) bool {
	if expr == nil {
		return true
	}
	return s.condEval != nil && s.condEval(expr)
}

func (s *Scheduler) controlGroups(indices []int) map[branchGroupKey][]int {
	groups := make(map[branchGroupKey][]int)
	for _, index := range indices {
		edge := s.spec.Edges[index]
		if edge.Kind != EdgeControlFlow {
			continue
		}
		key := branchGroupKey{from: edge.From, group: edge.Group}
		groups[key] = append(groups[key], index)
	}
	return groups
}

func (s *Scheduler) enqueue(id NodeID) {
	if s.completed[id] || s.readySet[id] {
		return
	}
	s.readyQueue = append(s.readyQueue, id)
	s.readySet[id] = true
}

func (s *Scheduler) enqueueMerge(id NodeID) {
	s.readyQueue = append(s.readyQueue, id)
	s.readySet[id] = true
}

func (s *Scheduler) removeFromQueue(id NodeID) {
	for i, ready := range s.readyQueue {
		if ready == id {
			s.readyQueue = append(s.readyQueue[:i], s.readyQueue[i+1:]...)
			break
		}
	}
	if s.joinKind(id) != Merge {
		delete(s.readySet, id)
	}
}

func (s *Scheduler) selectByPriority() NodeID {
	best := s.readyQueue[0]
	bestPriority := s.priority(best)
	for _, id := range s.readyQueue[1:] {
		priority := s.priority(id)
		if priority > bestPriority || (priority == bestPriority && id < best) {
			best = id
			bestPriority = priority
		}
	}
	return best
}

func (s *Scheduler) priority(id NodeID) int {
	priority := 0
	for _, index := range s.incoming[id] {
		if value := s.spec.Edges[index].Priority; value > priority {
			priority = value
		}
	}
	return priority
}

func (s *Scheduler) joinKind(id NodeID) JoinKind {
	for _, node := range s.spec.Nodes {
		if node.ID == id {
			if node.Join == "" {
				return JoinAll
			}
			return node.Join
		}
	}
	return JoinAll
}

func (s *Scheduler) allIncomingInactive(id NodeID) bool {
	incoming := s.incoming[id]
	if len(incoming) == 0 {
		return false
	}
	for _, index := range incoming {
		if s.edgeStates[index] != edgeInactive {
			return false
		}
	}
	return true
}

func (s *Scheduler) zeroInDegreeNodes() []NodeID {
	entries := make([]NodeID, 0)
	for _, node := range s.spec.Nodes {
		if len(s.incoming[node.ID]) == 0 {
			entries = append(entries, node.ID)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i] < entries[j] })
	return entries
}

func (s *Scheduler) hasNode(id NodeID) bool {
	for _, node := range s.spec.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func appendUniqueNode(nodes []NodeID, id NodeID) []NodeID {
	for _, existing := range nodes {
		if existing == id {
			return nodes
		}
	}
	return append(nodes, id)
}

func cloneNodeBoolMap(source map[NodeID]bool) map[NodeID]bool {
	result := make(map[NodeID]bool, len(source))
	for id, value := range source {
		result[id] = value
	}
	return result
}

func validEdgeActivation(state edgeActivation) bool {
	switch state {
	case edgePending, edgeActive, edgeArrived, edgeInactive:
		return true
	default:
		return false
	}
}

func branchKeyString(key branchGroupKey) string {
	return string(key.from) + "\x00" + key.group
}

func parseBranchKey(raw string) (branchGroupKey, error) {
	for i := range raw {
		if raw[i] == 0 {
			return branchGroupKey{from: NodeID(raw[:i]), group: raw[i+1:]}, nil
		}
	}
	return branchGroupKey{}, fmt.Errorf("invalid scheduler branch key %q", raw)
}

// TopologicalSort returns node IDs in deterministic data-dependency order.
func TopologicalSort(spec *WorkflowSpec) ([]NodeID, error) {
	if spec == nil {
		return nil, fmt.Errorf("spec must not be nil")
	}
	inDegree := make(map[NodeID]int)
	for _, node := range spec.Nodes {
		inDegree[node.ID] = 0
	}
	for _, edge := range spec.Edges {
		if edge.Kind == EdgeDataDependency {
			inDegree[edge.To]++
		}
	}
	queue := make([]NodeID, 0)
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })
	result := make([]NodeID, 0, len(spec.Nodes))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)
		for _, edge := range spec.Edges {
			if edge.From != current || edge.Kind != EdgeDataDependency {
				continue
			}
			inDegree[edge.To]--
			if inDegree[edge.To] == 0 {
				queue = append(queue, edge.To)
			}
		}
		sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })
	}
	if len(result) != len(spec.Nodes) {
		return result, fmt.Errorf("cycle detected: sorted %d of %d nodes", len(result), len(spec.Nodes))
	}
	return result, nil
}
