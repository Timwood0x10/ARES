package engine

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Sentinel errors for MutableDAG operations.
var (
	ErrNodeNotFound      = errors.New("node not found")
	ErrNodeHasDependents = errors.New("node has dependents")
	ErrDuplicateEdge     = errors.New("duplicate edge")
	ErrEdgeNotFound      = errors.New("edge not found")
)

// MutableDAG extends DAG with thread-safe mutation operations.
type MutableDAG struct {
	mu      sync.RWMutex
	dag     *DAG
	steps   map[string]*Step
	version uint64
	hub     *GraphEventHub
}

// NewMutableDAG creates a MutableDAG from initial steps.
func NewMutableDAG(steps []*Step) (*MutableDAG, error) {
	dag, err := NewDAG(steps)
	if err != nil {
		return nil, err
	}

	stepsMap := make(map[string]*Step, len(steps))
	for _, s := range steps {
		stepsMap[s.ID] = s
	}

	return &MutableDAG{
		dag:   dag,
		steps: stepsMap,
		hub:   NewGraphEventHub(),
	}, nil
}

// AddNode adds a step as a new node. Validates dependencies exist, checks for cycles.
func (m *MutableDAG) AddNode(ctx context.Context, step *Step) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if step == nil {
		return errors.New("step must not be nil")
	}
	if step.ID == "" {
		return errors.New("step ID must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.dag.Nodes[step.ID]; exists {
		return ErrDuplicateID
	}

	// Track added edges for rollback on cycle detection.
	type addedEdge struct {
		from string
		to   string
	}
	var addedEdges []addedEdge

	// Add the node.
	m.dag.Nodes[step.ID] = &DAGNode{
		StepID:    step.ID,
		InDegree:  0,
		OutDegree: 0,
	}

	// Process dependencies.
	for _, dep := range step.DependsOn {
		if _, exists := m.dag.Nodes[dep]; !exists {
			// Rollback: remove the node and any edges added so far.
			delete(m.dag.Nodes, step.ID)
			for _, e := range addedEdges {
				m.removeEdgeFromSlice(e.from, e.to)
				m.dag.Nodes[e.from].OutDegree--
				m.dag.Nodes[e.to].InDegree--
			}
			return ErrInvalidDependency
		}

		// Check for cycle before adding edge.
		if m.wouldCreateCycle(dep, step.ID) {
			// Rollback.
			delete(m.dag.Nodes, step.ID)
			for _, e := range addedEdges {
				m.removeEdgeFromSlice(e.from, e.to)
				m.dag.Nodes[e.from].OutDegree--
				m.dag.Nodes[e.to].InDegree--
			}
			return ErrCycleDetected
		}

		m.dag.Edges[dep] = append(m.dag.Edges[dep], step.ID)
		m.dag.Nodes[step.ID].InDegree++
		m.dag.Nodes[dep].OutDegree++
		addedEdges = append(addedEdges, addedEdge{from: dep, to: step.ID})
	}

	m.steps[step.ID] = step
	m.version++

	m.hub.Publish(GraphEvent{
		Change: GraphChange{
			Type:      ChangeAddNode,
			NodeID:    step.ID,
			Step:      step,
			Timestamp: time.Now(),
		},
		Success: true,
	})

	return nil
}

// RemoveNode removes a node and its edges. Fails if other nodes depend on it.
func (m *MutableDAG) RemoveNode(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.dag.Nodes[id]; !exists {
		return ErrNodeNotFound
	}

	// Check if any other node depends on this node.
	for _, step := range m.steps {
		if step.ID == id {
			continue
		}
		for _, dep := range step.DependsOn {
			if dep == id {
				m.hub.Publish(GraphEvent{
					Change: GraphChange{
						Type:      ChangeRemoveNode,
						NodeID:    id,
						Timestamp: time.Now(),
					},
					Success: false,
					Error:   ErrNodeHasDependents,
				})
				return ErrNodeHasDependents
			}
		}
	}

	// Remove all edges where this node is source.
	for _, target := range m.dag.Edges[id] {
		m.dag.Nodes[target].InDegree--
	}
	delete(m.dag.Edges, id)

	// Remove all edges where this node is target.
	for src, targets := range m.dag.Edges {
		newTargets := make([]string, 0, len(targets))
		for _, t := range targets {
			if t != id {
				newTargets = append(newTargets, t)
			} else {
				m.dag.Nodes[src].OutDegree--
			}
		}
		m.dag.Edges[src] = newTargets
	}

	delete(m.dag.Nodes, id)
	delete(m.steps, id)
	m.version++

	m.hub.Publish(GraphEvent{
		Change: GraphChange{
			Type:      ChangeRemoveNode,
			NodeID:    id,
			Timestamp: time.Now(),
		},
		Success: true,
	})

	return nil
}

// AddEdge adds a directed edge. Checks for cycles incrementally.
func (m *MutableDAG) AddEdge(ctx context.Context, from, to string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.dag.Nodes[from]; !exists {
		return ErrNodeNotFound
	}
	if _, exists := m.dag.Nodes[to]; !exists {
		return ErrNodeNotFound
	}

	// Check for duplicate edge.
	for _, target := range m.dag.Edges[from] {
		if target == to {
			return ErrDuplicateEdge
		}
	}

	if m.wouldCreateCycle(from, to) {
		m.hub.Publish(GraphEvent{
			Change: GraphChange{
				Type:      ChangeAddEdge,
				FromID:    from,
				ToID:      to,
				Timestamp: time.Now(),
			},
			Success: false,
			Error:   ErrCycleDetected,
		})
		return ErrCycleDetected
	}

	m.dag.Edges[from] = append(m.dag.Edges[from], to)
	m.dag.Nodes[to].InDegree++
	m.dag.Nodes[from].OutDegree++
	m.version++

	// Update the step's DependsOn list.
	if step, exists := m.steps[to]; exists {
		step.DependsOn = append(step.DependsOn, from)
	}

	m.hub.Publish(GraphEvent{
		Change: GraphChange{
			Type:      ChangeAddEdge,
			FromID:    from,
			ToID:      to,
			Timestamp: time.Now(),
		},
		Success: true,
	})

	return nil
}

// RemoveEdge removes a directed edge.
func (m *MutableDAG) RemoveEdge(ctx context.Context, from, to string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.dag.Nodes[from]; !exists {
		return ErrNodeNotFound
	}
	if _, exists := m.dag.Nodes[to]; !exists {
		return ErrNodeNotFound
	}

	found := m.removeEdgeFromSlice(from, to)
	if !found {
		return ErrEdgeNotFound
	}

	m.dag.Nodes[to].InDegree--
	m.dag.Nodes[from].OutDegree--
	m.version++

	// Update the step's DependsOn list.
	if step, exists := m.steps[to]; exists {
		newDeps := make([]string, 0, len(step.DependsOn))
		for _, dep := range step.DependsOn {
			if dep != from {
				newDeps = append(newDeps, dep)
			}
		}
		step.DependsOn = newDeps
	}

	m.hub.Publish(GraphEvent{
		Change: GraphChange{
			Type:      ChangeRemoveEdge,
			FromID:    from,
			ToID:      to,
			Timestamp: time.Now(),
		},
		Success: true,
	})

	return nil
}

// GetExecutionOrder returns topological sort under read lock.
func (m *MutableDAG) GetExecutionOrder() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.dag.GetExecutionOrder()
}

// Snapshot returns a deep copy of the current DAG.
func (m *MutableDAG) Snapshot() *DAG {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nodesCopy := make(map[string]*DAGNode, len(m.dag.Nodes))
	for id, node := range m.dag.Nodes {
		nodeCopy := *node
		nodesCopy[id] = &nodeCopy
	}

	edgesCopy := make(map[string][]string, len(m.dag.Edges))
	for src, targets := range m.dag.Edges {
		targetsCopy := make([]string, len(targets))
		copy(targetsCopy, targets)
		edgesCopy[src] = targetsCopy
	}

	return &DAG{
		Nodes: nodesCopy,
		Edges: edgesCopy,
	}
}

// Version returns the current mutation counter.
func (m *MutableDAG) Version() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.version
}

// Steps returns the current step list under read lock.
func (m *MutableDAG) Steps() []*Step {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Step, 0, len(m.steps))
	for _, s := range m.steps {
		result = append(result, s)
	}
	return result
}

// Subscribe returns a channel for graph change events.
func (m *MutableDAG) Subscribe() <-chan GraphEvent {
	_, ch := m.hub.Subscribe()
	return ch
}

// NodeCount returns the number of nodes.
func (m *MutableDAG) NodeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.dag.Nodes)
}

// EdgeCount returns the total number of edges.
func (m *MutableDAG) EdgeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, targets := range m.dag.Edges {
		count += len(targets)
	}
	return count
}

// wouldCreateCycle checks if adding an edge from->to would create a cycle.
// BFS from `to` following outgoing edges. If `from` is reachable, it creates a cycle.
// Must be called with m.mu held.
func (m *MutableDAG) wouldCreateCycle(from, to string) bool {
	visited := make(map[string]bool)
	queue := []string{to}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current == from {
			return true
		}

		if visited[current] {
			continue
		}
		visited[current] = true

		for _, neighbor := range m.dag.Edges[current] {
			if !visited[neighbor] {
				queue = append(queue, neighbor)
			}
		}
	}

	return false
}

// removeEdgeFromSlice removes the edge from->to from the Edges slice.
// Returns true if the edge was found and removed.
// Must be called with m.mu held.
func (m *MutableDAG) removeEdgeFromSlice(from, to string) bool {
	targets := m.dag.Edges[from]
	for i, t := range targets {
		if t == to {
			m.dag.Edges[from] = append(targets[:i], targets[i+1:]...)
			return true
		}
	}
	return false
}
