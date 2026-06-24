// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import (
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/observability"
	"github.com/Timwood0x10/ares/internal/ratelimit"
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

// Graph represents a DAG of nodes with conditional edges.
//
// Graph is safe for concurrent reads via Execute, but concurrent
// mutation (Node, Edge, Start, RemoveEdge, RemoveNode, Clear, etc.)
// from multiple goroutines requires external synchronization.
type Graph struct {
	mu        sync.RWMutex
	id        string
	nodes     map[string]Node
	edges     map[string][]*Edge
	start     string
	scheduler Scheduler
	tracer    observability.Tracer // observability tracer for execution tracking
	limiter   ratelimit.Limiter    // rate limiter for execution throttling
}

// NewGraph creates a new graph with the given ID.
//
// NOTE: This function will panic if id is empty. This is intentional as it
// indicates a programming error in the calling code. This constructor is
// used during workflow graph initialization (startup phase), and invalid
// parameters represent fatal startup failures that should prevent application
// launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// id - unique graph identifier, must not be empty.
// Returns new graph instance.
func NewGraph(id string) *Graph {
	if id == "" {
		panic("graph ID cannot be empty: empty id is a programming error")
	}
	return &Graph{
		id:        id,
		nodes:     make(map[string]Node),
		edges:     make(map[string][]*Edge),
		scheduler: NewDefaultScheduler(),
		tracer:    observability.NewNoopTracer(), // default to no-op tracer
		limiter:   nil,                           // default to no rate limiting
	}
}

// NewGraphWithTracer creates a new graph with a custom tracer.
//
// NOTE: This function will panic if id is empty or tracer is nil. This is intentional
// as it indicates a programming error in the calling code. This constructor is
// used during workflow graph initialization (startup phase), and invalid
// parameters represent fatal startup failures that should prevent application
// launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// id - unique graph identifier, must not be empty.
// tracer - observability tracer, must not be nil.
// Returns new graph instance.
func NewGraphWithTracer(id string, tracer observability.Tracer) *Graph {
	if id == "" {
		panic("graph ID cannot be empty: empty id is a programming error")
	}
	if tracer == nil {
		panic("tracer cannot be nil: nil tracer is a programming error")
	}
	return &Graph{
		id:        id,
		nodes:     make(map[string]Node),
		edges:     make(map[string][]*Edge),
		scheduler: NewDefaultScheduler(),
		tracer:    tracer,
		limiter:   nil, // default to no rate limiting
	}
}

// NewGraphWithLimiter creates a new graph with a custom rate limiter.
//
// NOTE: This function will panic if id is empty. This is intentional as it
// indicates a programming error in the calling code. This constructor is
// used during workflow graph initialization (startup phase), and invalid
// parameters represent fatal startup failures that should prevent application
// launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// id - unique graph identifier, must not be empty.
// limiter - rate limiter for execution throttling.
// Returns new graph instance.
func NewGraphWithLimiter(id string, limiter ratelimit.Limiter) *Graph {
	if id == "" {
		panic("graph ID cannot be empty: empty id is a programming error")
	}
	return &Graph{
		id:        id,
		nodes:     make(map[string]Node),
		edges:     make(map[string][]*Edge),
		scheduler: NewDefaultScheduler(),
		tracer:    observability.NewNoopTracer(),
		limiter:   limiter,
	}
}

// Node adds a node to the graph.
//
// NOTE: This method will panic if graph is nil, id is empty, or node is nil.
// This is intentional as it indicates a programming error in the calling code.
// These methods are used during workflow graph initialization (startup phase),
// and invalid parameters represent fatal startup failures that should prevent
// application launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// id - unique node identifier, must not be empty.
// node - node instance, must not be nil.
// Returns graph for method chaining.
func (g *Graph) Node(id string, node Node) *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == "" {
		panic("node ID cannot be empty: empty id is a programming error")
	}
	if node == nil {
		panic("node cannot be nil: nil node is a programming error")
	}
	g.nodes[id] = node
	return g
}

// Edge adds an edge from one node to another with optional condition.
//
// NOTE: This method will panic if graph is nil, from/to id is empty, or if
// the referenced nodes have not been added via Node(). This is intentional as
// it indicates a programming error in the calling code. These methods are used
// during workflow graph initialization (startup phase), and invalid parameters
// represent fatal startup failures that should prevent application launch. This
// follows the coding standard allowing panic for fatal startup errors.
//
// M8 fix: validate that both from and to nodes exist in the graph.
//
// Args:
// from - source node ID, must not be empty and must exist in the graph.
// to - target node ID, must not be empty and must exist in the graph.
// cond - optional edge traversal condition.
// Returns graph for method chaining.
func (g *Graph) Edge(from, to string, cond ...Condition) *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if from == "" {
		panic("from node ID cannot be empty: empty id is a programming error")
	}
	if to == "" {
		panic("to node ID cannot be empty: empty id is a programming error")
	}
	if _, ok := g.nodes[from]; !ok {
		panic(fmt.Sprintf("from node %q not found: node must be added via Node() before Edge()", from))
	}
	if _, ok := g.nodes[to]; !ok {
		panic(fmt.Sprintf("to node %q not found: node must be added via Node() before Edge()", to))
	}

	edge := &Edge{from: from, to: to}
	if len(cond) > 0 {
		edge.cond = cond[0]
	}

	g.edges[from] = append(g.edges[from], edge)
	return g
}

// Start sets the starting node for the graph.
//
// NOTE: This method will panic if graph is nil or id is empty. This is intentional
// as it indicates a programming error in the calling code. These methods are
// used during workflow graph initialization (startup phase), and invalid
// parameters represent fatal startup failures that should prevent application
// launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// id - starting node ID, must not be empty.
// Returns graph for method chaining.
func (g *Graph) Start(id string) *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == "" {
		panic("start node ID cannot be empty: empty id is a programming error")
	}
	g.start = id
	return g
}

// RemoveEdge removes an edge from one node to another.
//
// NOTE: This method will panic if graph is nil or if from/to is empty.
//
// Args:
// from - source node ID, must not be empty.
// to - target node ID, must not be empty.
// Returns graph for method chaining.
func (g *Graph) RemoveEdge(from, to string) *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if from == "" {
		panic("from node ID cannot be empty: empty id is a programming error")
	}
	if to == "" {
		panic("to node ID cannot be empty: empty id is a programming error")
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

	return g
}

// RemoveNode removes a node and all its associated edges from the graph.
//
// NOTE: This method will panic if graph is nil or id is empty.
//
// Args:
// id - node identifier, must not be empty.
// Returns graph for method chaining.
func (g *Graph) RemoveNode(id string) *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == "" {
		panic("node ID cannot be empty: empty id is a programming error")
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

	return g
}

// Clear removes all nodes and edges from the graph.
//
// NOTE: This method will panic if graph is nil.
//
// Returns graph for method chaining.
func (g *Graph) Clear() *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	g.nodes = make(map[string]Node)
	g.edges = make(map[string][]*Edge)
	g.start = ""

	return g
}

// SetScheduler sets a custom scheduler for the graph.
//
// NOTE: This method will panic if graph is nil or scheduler is nil. This is
// intentional as it indicates a programming error in the calling code.
// These methods are used during workflow graph initialization (startup phase),
// and invalid parameters represent fatal startup failures that should prevent
// application launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// scheduler - custom scheduler instance, must not be nil.
// Returns graph for method chaining.
func (g *Graph) SetScheduler(scheduler Scheduler) *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if scheduler == nil {
		panic("scheduler cannot be nil: nil scheduler is a programming error")
	}
	g.scheduler = scheduler
	return g
}

// SetTracer sets a custom tracer for the graph.
//
// NOTE: This method will panic if graph is nil or tracer is nil. This is
// intentional as it indicates a programming error in the calling code.
// These methods are used during workflow graph initialization (startup phase),
// and invalid parameters represent fatal startup failures that should prevent
// application launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// tracer - custom tracer instance, must not be nil.
// Returns graph for method chaining.
func (g *Graph) SetTracer(tracer observability.Tracer) *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if tracer == nil {
		panic("tracer cannot be nil: nil tracer is a programming error")
	}
	g.tracer = tracer
	return g
}

// SetLimiter sets a custom rate limiter for the graph.
//
// NOTE: This method will panic if graph is nil. This is intentional as it
// indicates a programming error in the calling code. These methods are
// used during workflow graph initialization (startup phase), and invalid
// parameters represent fatal startup failures that should prevent application
// launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// limiter - custom rate limiter instance (can be nil for no limiting).
// Returns graph for method chaining.
func (g *Graph) SetLimiter(limiter ratelimit.Limiter) *Graph {
	if g == nil {
		panic("graph is nil: nil receiver is a programming error")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	g.limiter = limiter
	return g
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

// Result represents the result of graph execution.
type Result struct {
	GraphID  string
	State    *State
	Duration time.Duration
	Error    error
}
