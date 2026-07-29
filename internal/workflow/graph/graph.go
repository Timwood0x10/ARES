// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_observability"
	"github.com/Timwood0x10/ares/internal/ares_ratelimit"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	workflowcore "github.com/Timwood0x10/ares/internal/workflow"
)

// Edge represents a connection between two nodes with optional condition.
type Edge struct {
	from string
	to   string
	cond Condition
}

// Condition defines a predicate function for edge traversal.
type Condition func(state *State) bool

// IfFunc creates a condition from a function.
func IfFunc(fn func(state *State) bool) Condition {
	return fn
}

// NodeRouter is a callback for dynamic routing decisions during graph execution.
// After a node completes, the router is called with the just-executed node ID
// and current state. If it returns a non-empty node ID, that node is enqueued
// for execution next (bypassing the DAG's static edge traversal).
// Return "" to let the DAG decide the next node via in-degree BFS as usual.
type NodeRouter func(ctx context.Context, currentNodeID string, state *State) string

// Graph represents a DAG of nodes with conditional edges.
//
// Graph is safe for concurrent reads via Execute, but concurrent
// mutation (Node, Edge, Start, RemoveEdge, RemoveNode, Clear, etc.)
// from multiple goroutines requires external synchronization.
type Graph struct {
	mu              sync.RWMutex
	id              string
	nodes           map[string]Node
	edges           map[string][]*Edge
	start           string
	scheduler       Scheduler
	tracer          ares_observability.Tracer        // ares_observability tracer for execution tracking
	limiter         ares_ratelimit.Limiter           // rate limiter for execution throttling
	pluginBus       *ares_runtime.PluginBus          // optional plugin bus for BeforeStep/AfterStep hooks
	router          NodeRouter                       // optional dynamic routing callback
	collector       *ares_runtime.ExecutionCollector // optional collector for route recording
	checkpointStore ares_runtime.CheckpointStore     // optional checkpoint store for state persistence
}

// NewGraph creates a new graph with the given ID.
//
// Args:
// id - unique graph identifier, must not be empty.
// Returns new graph instance or error if id is empty.
func NewGraph(id string) (*Graph, error) {
	if id == "" {
		return nil, fmt.Errorf("graph ID cannot be empty")
	}
	return &Graph{
		id:        id,
		nodes:     make(map[string]Node),
		edges:     make(map[string][]*Edge),
		scheduler: NewDefaultScheduler(),
		tracer:    ares_observability.NewNoopTracer(), // default to no-op tracer
		limiter:   nil,                                // default to no rate limiting
	}, nil
}

// NewGraphWithTracer creates a new graph with a custom tracer.
//
// Args:
// id - unique graph identifier, must not be empty.
// tracer - ares_observability tracer, must not be nil.
// Returns new graph instance or error.
func NewGraphWithTracer(id string, tracer ares_observability.Tracer) (*Graph, error) {
	if id == "" {
		return nil, fmt.Errorf("graph ID cannot be empty")
	}
	if tracer == nil {
		return nil, fmt.Errorf("tracer cannot be nil")
	}
	return &Graph{
		id:        id,
		nodes:     make(map[string]Node),
		edges:     make(map[string][]*Edge),
		scheduler: NewDefaultScheduler(),
		tracer:    tracer,
		limiter:   nil, // default to no rate limiting
	}, nil
}

// NewGraphWithLimiter creates a new graph with a custom rate limiter.
//
// Args:
// id - unique graph identifier, must not be empty.
// limiter - rate limiter for execution throttling.
// Returns new graph instance or error.
func NewGraphWithLimiter(id string, limiter ares_ratelimit.Limiter) (*Graph, error) {
	if id == "" {
		return nil, fmt.Errorf("graph ID cannot be empty")
	}
	return &Graph{
		id:        id,
		nodes:     make(map[string]Node),
		edges:     make(map[string][]*Edge),
		scheduler: NewDefaultScheduler(),
		tracer:    ares_observability.NewNoopTracer(),
		limiter:   limiter,
	}, nil
}

// Node adds a node to the graph.
//
// Args:
// id - unique node identifier, must not be empty.
// node - node instance, must not be nil.
// Returns graph for chaining or error.
func (g *Graph) Node(id string, node Node) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == "" {
		return nil, fmt.Errorf("node ID cannot be empty")
	}
	if node == nil {
		return nil, fmt.Errorf("node cannot be nil")
	}
	if _, exists := g.nodes[id]; exists {
		return nil, fmt.Errorf("duplicate node ID %q", id)
	}
	g.nodes[id] = node
	return g, nil
}

// Edge adds an edge from one node to another with optional condition.
//
// Args:
// from - source node ID, must not be empty and must exist in the graph.
// to - target node ID, must not be empty and must exist in the graph.
// cond - optional edge traversal condition.
// Returns graph for chaining or error.
func (g *Graph) Edge(from, to string, cond ...Condition) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if from == "" {
		return nil, fmt.Errorf("from node ID cannot be empty")
	}
	if to == "" {
		return nil, fmt.Errorf("to node ID cannot be empty")
	}
	if _, ok := g.nodes[from]; !ok {
		return nil, fmt.Errorf("from node %q not found: node must be added via Node() before Edge()", from)
	}
	if _, ok := g.nodes[to]; !ok {
		return nil, fmt.Errorf("to node %q not found: node must be added via Node() before Edge()", to)
	}

	edge := &Edge{from: from, to: to}
	if len(cond) > 0 {
		edge.cond = cond[0]
	}

	// Check for duplicate edge: same from→to with the same condition (or both nil).
	for _, existing := range g.edges[from] {
		if existing.to == to {
			if edge.cond == nil && existing.cond == nil {
				return g, nil // silently allow duplicate no-cond edges
			}
			if edge.cond != nil && existing.cond != nil {
				// Both have conditions — function equality cannot be compared
				// reflectively; treat as duplicate to avoid infinite growth.
				return g, nil
			}
		}
	}

	g.edges[from] = append(g.edges[from], edge)
	return g, nil
}

// Start sets the starting node for the graph.
//
// Args:
// id - starting node ID, must not be empty.
// Returns graph for chaining or error.
func (g *Graph) Start(id string) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == "" {
		return nil, fmt.Errorf("start node ID cannot be empty")
	}
	g.start = id
	return g, nil
}

// RemoveEdge removes an edge from one node to another.
//
// Args:
// from - source node ID, must not be empty.
// to - target node ID, must not be empty.
// Returns graph for chaining or error.
func (g *Graph) RemoveEdge(from, to string) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if from == "" {
		return nil, fmt.Errorf("from node ID cannot be empty")
	}
	if to == "" {
		return nil, fmt.Errorf("to node ID cannot be empty")
	}

	if edges, ok := g.edges[from]; ok {
		newEdges := make([]*Edge, 0, len(edges))
		for _, edge := range edges {
			if edge.to != to {
				newEdges = append(newEdges, edge)
			}
		}
		g.edges[from] = newEdges
	}

	return g, nil
}

// RemoveNode removes a node and all its associated edges from the graph.
//
// Args:
// id - node identifier, must not be empty.
// Returns graph for chaining or error.
func (g *Graph) RemoveNode(id string) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == "" {
		return nil, fmt.Errorf("node ID cannot be empty")
	}

	delete(g.nodes, id)

	// Remove all edges pointing to the removed node.
	for from, edges := range g.edges {
		newEdges := make([]*Edge, 0, len(edges))
		for _, edge := range edges {
			if edge.to != id {
				newEdges = append(newEdges, edge)
			}
		}
		g.edges[from] = newEdges
	}

	// Remove edges originating from the removed node.
	delete(g.edges, id)

	// Clear start if it points to the removed node.
	if g.start == id {
		g.start = ""
	}

	return g, nil
}

// Clear removes all nodes and edges from the graph.
//
// Returns graph for chaining or error.
func (g *Graph) Clear() (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	g.nodes = make(map[string]Node)
	g.edges = make(map[string][]*Edge)
	g.start = ""

	return g, nil
}

// SetScheduler sets a custom scheduler for the graph.
//
// Args:
// scheduler - custom scheduler instance, must not be nil.
// Returns graph for chaining or error.
func (g *Graph) SetScheduler(scheduler Scheduler) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if scheduler == nil {
		return nil, fmt.Errorf("scheduler cannot be nil")
	}
	g.scheduler = scheduler
	return g, nil
}

// SetTracer sets a custom tracer for the graph.
//
// Args:
// tracer - custom tracer instance, must not be nil.
// Returns graph for chaining or error.
func (g *Graph) SetTracer(tracer ares_observability.Tracer) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if tracer == nil {
		return nil, fmt.Errorf("tracer cannot be nil")
	}
	g.tracer = tracer
	return g, nil
}

// SetPluginBus attaches a PluginBus for BeforeStep/AfterStep hooks and
// event emission. This aligns the graph execution path with the workflow
// engine's plugin system, enabling ares_observability and memory routing.
func (g *Graph) SetPluginBus(pb *ares_runtime.PluginBus) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pluginBus = pb
	return g, nil
}

// SetExecutionCollector attaches an ExecutionCollector for route recording
// and history tracking during graph execution.
func (g *Graph) SetExecutionCollector(c *ares_runtime.ExecutionCollector) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if c == nil {
		return nil, fmt.Errorf("execution collector cannot be nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.collector = c
	return g, nil
}

// SetRouter sets a dynamic routing callback that is invoked after each
// successfully completed node. The callback receives the just-executed node ID
// and the current state, and returns the ID of the node to route to next.
// Return "" to let the graph continue with normal BFS in-degree traversal.
// The target node must already exist in the graph.
func (g *Graph) SetRouter(router NodeRouter) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.router = router
	return g, nil
}

// SetLimiter sets a custom rate limiter for the graph.
//
// Args:
// limiter - custom rate limiter instance (can be nil for no limiting).
// Returns graph for chaining or error.
func (g *Graph) SetLimiter(limiter ares_ratelimit.Limiter) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	g.limiter = limiter
	return g, nil
}

// SetCheckpointStore attaches a checkpoint store for state persistence.
// When set, the graph saves a checkpoint after each node execution.
func (g *Graph) SetCheckpointStore(store ares_runtime.CheckpointStore) (*Graph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checkpointStore = store
	return g, nil
}

// ID returns the graph ID.
func (g *Graph) ID() string {
	if g == nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.id
}

// NodeIDs returns all node IDs in the graph. The order is non-deterministic.
func (g *Graph) NodeIDs() []string {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	return ids
}

// EdgeInfo carries the serializable parts of an edge for IR compilation.
type EdgeInfo struct {
	From    string
	To      string
	HasCond bool
}

// RuntimeEdge contains one executable edge and its optional predicate.
type RuntimeEdge struct {
	From      string
	To        string
	Condition Condition
}

// Edges returns all edges in the graph as serializable EdgeInfo values.
func (g *Graph) Edges() []EdgeInfo {
	runtimeEdges := g.RuntimeEdges()
	edges := make([]EdgeInfo, 0, len(runtimeEdges))
	for _, edge := range runtimeEdges {
		edges = append(edges, EdgeInfo{
			From:    edge.From,
			To:      edge.To,
			HasCond: edge.Condition != nil,
		})
	}
	return edges
}

// CompileEdges returns serializable topology for the unified compiler.
func (g *Graph) CompileEdges() []workflowcore.GraphCompileEdge {
	runtimeEdges := g.RuntimeEdges()
	edges := make([]workflowcore.GraphCompileEdge, 0, len(runtimeEdges))
	for _, edge := range runtimeEdges {
		edges = append(edges, workflowcore.GraphCompileEdge{
			From:       edge.From,
			To:         edge.To,
			HasCond:    edge.Condition != nil,
			BindingRef: graphConditionBindingID(edge.From, edge.To),
		})
	}
	return edges
}

// RuntimeEdges returns executable edge bindings for unified compilation.
func (g *Graph) RuntimeEdges() []RuntimeEdge {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	edges := make([]RuntimeEdge, 0, len(g.edges))
	for from, targets := range g.edges {
		for _, edge := range targets {
			edges = append(edges, RuntimeEdge{
				From:      from,
				To:        edge.to,
				Condition: edge.cond,
			})
		}
	}
	return edges
}

// RuntimeRouter returns the optional graph routing callback.
func (g *Graph) RuntimeRouter() NodeRouter {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.router
}

// RuntimeScheduler returns the configured ready-node selector.
func (g *Graph) RuntimeScheduler() Scheduler {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.scheduler
}

// RuntimePluginBus returns the configured lifecycle plugin bus.
func (g *Graph) RuntimePluginBus() *ares_runtime.PluginBus {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.pluginBus
}

// RuntimeCollector returns the configured execution collector.
func (g *Graph) RuntimeCollector() *ares_runtime.ExecutionCollector {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.collector
}

// RuntimeCheckpointStore returns the configured checkpoint store.
func (g *Graph) RuntimeCheckpointStore() ares_runtime.CheckpointStore {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.checkpointStore
}

// RuntimeNodes returns executable node bindings for unified compilation.
func (g *Graph) RuntimeNodes() map[string]Node {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	nodes := make(map[string]Node, len(g.nodes))
	for id, node := range g.nodes {
		nodes[id] = node
	}
	return nodes
}

// StartNode returns the configured start node ID, or "" if not set.
func (g *Graph) StartNode() string {
	if g == nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.start
}

// Result represents the result of graph execution.
type Result struct {
	GraphID  string
	State    *State
	Duration time.Duration
	Error    error
}
