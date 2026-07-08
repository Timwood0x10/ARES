package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// LazyNode wraps a KnowledgeObject with lazy-loading support.
// The object's data is only loaded when Expand is called.
type LazyNode struct {
	ID       string
	Summary  string // Always available (pre-loaded)
	Loaded   bool   // Whether full data has been loaded
	expanded bool   // Whether children have been expanded
	expandFn func(ctx context.Context, id string) (*knowledge.KnowledgeObject, error)
	mu       sync.RWMutex
}

var (
	// ErrNodeAlreadyExpanded is returned when Expand is called on an already-expanded node.
	ErrNodeAlreadyExpanded = fmt.Errorf("node already expanded")
)

// Expand loads the full KnowledgeObject data on demand.
// Returns ErrNodeAlreadyExpanded if already expanded.
func (n *LazyNode) Expand(ctx context.Context) (*knowledge.KnowledgeObject, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.expanded {
		return nil, ErrNodeAlreadyExpanded
	}

	if n.expandFn == nil {
		n.expanded = true
		n.Loaded = true
		return nil, nil // nolint: nilnil // expandFn not set means data already loaded
	}

	obj, err := n.expandFn(ctx, n.ID)
	if err != nil {
		return nil, fmt.Errorf("expand node %s: %w", n.ID, err)
	}

	n.expanded = true
	n.Loaded = true
	return obj, nil
}

// IsExpanded returns whether the node has been expanded.
func (n *LazyNode) IsExpanded() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.expanded
}

// LazyGraph is a working graph that supports lazy node expansion.
// Only Summary-level data is loaded initially; full objects are loaded
// on demand when the Compiler references them.
type LazyGraph struct {
	Nodes map[string]*LazyNode
	Edges []knowledge.Relation
}

// NewLazyGraph creates a LazyGraph from a regular WorkingGraph.
// The expandFn is called when a node is expanded; if nil, nodes are
// considered already fully loaded (not lazy). When expandFn is non-nil,
// nodes start unexpanded so that Expand() actually invokes expandFn;
// previously expanded was hard-coded to true, which made Expand() return
// ErrNodeAlreadyExpanded and the expandFn unreachable.
func NewLazyGraph(graph *knowledge.WorkingGraph, expandFn func(ctx context.Context, id string) (*knowledge.KnowledgeObject, error)) *LazyGraph {
	lg := &LazyGraph{
		Nodes: make(map[string]*LazyNode, len(graph.Nodes)),
		Edges: graph.Edges,
	}

	// When no expandFn is provided, nodes are considered already fully
	// loaded (not lazy). When expandFn is provided, nodes must start
	// unexpanded so Expand() actually calls expandFn.
	expanded := expandFn == nil
	for id, obj := range graph.Nodes {
		summary := obj.Summary
		if summary == "" {
			summary = obj.ID
		}
		lg.Nodes[id] = &LazyNode{
			ID:       id,
			Summary:  summary,
			Loaded:   true,
			expanded: expanded,
		}
	}

	// Set up lazy expandFn for each node.
	if expandFn != nil {
		for _, node := range lg.Nodes {
			node.expandFn = expandFn
		}
	}

	return lg
}

// NewLazyGraphFromSummaries creates a LazyGraph where only summaries are known.
// Full objects are loaded via expandFn when Expand() is called.
func NewLazyGraphFromSummaries(summaries map[string]string, edges []knowledge.Relation, expandFn func(ctx context.Context, id string) (*knowledge.KnowledgeObject, error)) *LazyGraph {
	lg := &LazyGraph{
		Nodes: make(map[string]*LazyNode, len(summaries)),
		Edges: edges,
	}

	for id, summary := range summaries {
		lg.Nodes[id] = &LazyNode{
			ID:       id,
			Summary:  summary,
			expandFn: expandFn,
		}
	}

	return lg
}

// GetNode returns a lazy node by ID. Returns nil if not found.
func (g *LazyGraph) GetNode(id string) *LazyNode {
	if g == nil {
		return nil
	}
	return g.Nodes[id]
}

// ExpandNode expands a specific node on demand.
// Returns the loaded KnowledgeObject, or ErrNodeAlreadyExpanded if already expanded.
func (g *LazyGraph) ExpandNode(ctx context.Context, id string) (*knowledge.KnowledgeObject, error) {
	if g == nil {
		return nil, fmt.Errorf("lazy graph is nil")
	}
	node, ok := g.Nodes[id]
	if !ok {
		return nil, fmt.Errorf("node %s not found", id)
	}
	obj, err := node.Expand(ctx)
	if err == ErrNodeAlreadyExpanded {
		return nil, nil // nolint: nilnil // already expanded is not an error
	}
	return obj, err
}

// NodeCount returns the number of nodes (expanded or not).
func (g *LazyGraph) NodeCount() int {
	if g == nil {
		return 0
	}
	return len(g.Nodes)
}

// LoadedCount returns the number of fully loaded (expanded) nodes.
func (g *LazyGraph) LoadedCount() int {
	if g == nil {
		return 0
	}
	count := 0
	for _, node := range g.Nodes {
		if node.IsExpanded() {
			count++
		}
	}
	return count
}
