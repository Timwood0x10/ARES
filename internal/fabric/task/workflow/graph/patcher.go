// Package graph provides dynamic agent orchestration with pluggable scheduling.
//
// It also includes Runtime Patch executors for the Evolution system.
package graph

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// ── GraphPatchExecutor ──────────────────────

// GraphPatchExecutor handles DAG-related runtime patches.
// It wraps a *Graph and applies InsertNode/RemoveNode/ReplaceNode/AddEdge/RemoveEdge/ChangeScheduler.
// Implements patch.RuntimeComponent for unified runtime evolution.
type GraphPatchExecutor struct {
	// mu guards the graph pointer: the executor is registered on the patch
	// registry BEFORE a live graph exists, and SetGraph rebinds it later —
	// possibly while patches from the evolution loop apply concurrently. An
	// unlocked pointer swap was a data race with every Apply/Snapshot read.
	mu    sync.RWMutex
	graph *Graph
}

// NewGraphPatchExecutor creates a new GraphPatchExecutor.
func NewGraphPatchExecutor(g *Graph) *GraphPatchExecutor {
	return &GraphPatchExecutor{graph: g}
}

// Name returns "workflow.graph" as the component identifier for patch routing.
func (e *GraphPatchExecutor) Name() string { return "workflow.graph" }

// SetGraph replaces the executor's underlying graph reference with a live one.
// Called after agents are created so workflow/scheduler patches mutate the
// agent's real graph rather than the bootstrap placeholder. This mirrors
// RecoveryPatchExecutor.SetDAG: the executor is already registered on the
// patch registry, so it must be updated in place (Register cannot overwrite an
// already-registered component key).
func (e *GraphPatchExecutor) SetGraph(g *Graph) {
	if g == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.graph = g
}

// currentGraph returns the bound graph under the read lock.
func (e *GraphPatchExecutor) currentGraph() *Graph {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.graph
}

// Snapshot returns the current graph structure as a serializable snapshot.
func (e *GraphPatchExecutor) Snapshot(_ context.Context) (any, error) {
	g := e.currentGraph()
	if g == nil {
		return nil, patch.ErrNoSnapshot
	}
	return g, nil
}

// Ensure GraphPatchExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*GraphPatchExecutor)(nil)

// Apply applies a runtime patch to the graph.
func (e *GraphPatchExecutor) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	g := e.currentGraph()
	if g == nil {
		return nil, errors.New("graph executor: graph is nil (call SetGraph first)")
	}
	switch p.Type {
	case patch.PatchInsertNode:
		return e.applyInsertNode(ctx, g, p)
	case patch.PatchRemoveNode:
		return e.applyRemoveNode(ctx, g, p)
	case patch.PatchReplaceNode:
		return e.applyReplaceNode(ctx, g, p)
	case patch.PatchAddEdge:
		return e.applyAddEdge(ctx, g, p)
	case patch.PatchRemoveEdge:
		return e.applyRemoveEdge(ctx, g, p)
	case patch.PatchChangeScheduler:
		return e.applyChangeScheduler(ctx, g, p)
	case patch.PatchRestoreNode:
		return e.applyRestoreNode(ctx, g, p)
	default:
		return nil, fmt.Errorf("graph executor: unsupported patch type %s", p.Type)
	}
}

// CanApply checks whether a patch can be applied.
func (e *GraphPatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	if e.currentGraph() == nil {
		return errors.New("graph executor: graph is nil")
	}
	switch p.Type {
	case patch.PatchInsertNode:
		if p.Target == "" {
			return errors.New("graph executor: insert node requires non-empty target")
		}
		return nil
	case patch.PatchRemoveNode:
		if p.Target == "" {
			return errors.New("graph executor: remove node requires non-empty target")
		}
		return nil
	case patch.PatchReplaceNode:
		if p.Target == "" {
			return errors.New("graph executor: replace node requires non-empty target")
		}
		return nil
	case patch.PatchAddEdge:
		if p.Target == "" {
			return errors.New("graph executor: add edge requires non-empty from")
		}
		to, ok := p.Value.(string)
		if !ok || to == "" {
			return errors.New("graph executor: add edge value must be non-empty string (to)")
		}
		return nil
	case patch.PatchRemoveEdge:
		if p.Target == "" {
			return errors.New("graph executor: remove edge requires non-empty from")
		}
		to, ok := p.Value.(string)
		if !ok || to == "" {
			return errors.New("graph executor: remove edge value must be non-empty string (to)")
		}
		return nil
	case patch.PatchChangeScheduler:
		return nil
	case patch.PatchRestoreNode:
		if _, ok := p.Value.(nodeRestore); !ok {
			return errors.New("graph executor: restore node value must be a nodeRestore payload")
		}
		return nil
	default:
		return fmt.Errorf("graph executor: unsupported patch type %s", p.Type)
	}
}

// ── Apply implementations ───────────────────

func (e *GraphPatchExecutor) applyInsertNode(_ context.Context, g *Graph, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Determine the node to insert.
	var node Node
	if n, ok := p.Value.(Node); ok {
		node = n
	} else {
		// Create a FuncNode with the target ID.
		fn, err := NewFuncNode(p.Target, placeholderNodeExecute(p.Target))
		if err != nil {
			return nil, fmt.Errorf("graph executor: create func node: %w", err)
		}
		node = fn
	}

	// Capture the old node if it exists (for rollback).
	g.mu.RLock()
	oldNode := g.nodes[p.Target]
	g.mu.RUnlock()

	_, err := g.Node(p.Target, node)
	if err != nil {
		return nil, fmt.Errorf("graph executor: insert node %s: %w", p.Target, err)
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchRemoveNode,
		Target: p.Target,
		Value:  oldNode,
		Reason: "rollback: remove inserted node",
	}, nil
}

// nodeRestore captures everything Graph.RemoveNode destroys so a failed
// removal can be rolled back completely: the node itself, every edge that
// touched it (in and out, with their conditions), and whether the removed
// node was the graph's start node.
type nodeRestore struct {
	Node     Node
	Edges    []RuntimeEdge
	WasStart bool
}

func (e *GraphPatchExecutor) applyRemoveNode(_ context.Context, g *Graph, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Capture the node AND its edges before removing (for rollback): the
	// plain insert-node rollback restored the node but silently dropped
	// every edge that touched it — a rolled-back hub node reappeared as an
	// isolated vertex, disconnecting the workflow.
	var snap nodeRestore
	g.mu.RLock()
	oldNode, exists := g.nodes[p.Target]
	if !exists {
		g.mu.RUnlock()
		return nil, fmt.Errorf("graph executor: node %q not found", p.Target)
	}
	snap.Node = oldNode
	snap.WasStart = g.start == p.Target
	for from, edges := range g.edges {
		for _, edge := range edges {
			if edge.from == p.Target || edge.to == p.Target {
				snap.Edges = append(snap.Edges, RuntimeEdge{
					From:      from,
					To:        edge.to,
					Condition: edge.cond,
				})
			}
		}
	}
	g.mu.RUnlock()

	_, err := g.RemoveNode(p.Target)
	if err != nil {
		return nil, fmt.Errorf("graph executor: remove node %s: %w", p.Target, err)
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchRestoreNode,
		Target: p.Target,
		Value:  snap,
		Reason: "rollback: restore removed node with its edges",
	}, nil
}

// applyRestoreNode re-inserts a previously removed node with every captured
// edge and, when it was the start node, the start pointer. Best-effort per
// edge: an edge whose OTHER endpoint was also removed by a later patch
// cannot be restored and is skipped — a partial rollback is strictly better
// than failing the whole rollback.
func (e *GraphPatchExecutor) applyRestoreNode(_ context.Context, g *Graph, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	snap, ok := p.Value.(nodeRestore)
	if !ok {
		return nil, fmt.Errorf("graph executor: restore node %q: value %T is not a nodeRestore payload", p.Target, p.Value)
	}
	if snap.Node == nil {
		return nil, fmt.Errorf("graph executor: restore node %q: payload carries no node", p.Target)
	}
	if _, err := g.Node(p.Target, snap.Node); err != nil {
		// The id is occupied again (a replacement node was inserted after
		// the removal) — nothing sensible to restore on top of it.
		return nil, fmt.Errorf("graph executor: restore node %s: %w", p.Target, err)
	}
	for _, re := range snap.Edges {
		// Edge() validates both endpoints exist and suppresses duplicates;
		// an edge whose other endpoint is gone is skipped (best-effort).
		if _, err := g.Edge(re.From, re.To, re.Condition); err != nil {
			continue
		}
	}
	if snap.WasStart && g.StartNode() == "" {
		if _, err := g.Start(p.Target); err != nil {
			return nil, fmt.Errorf("graph executor: restore start %s: %w", p.Target, err)
		}
	}
	return &patch.RuntimePatch{
		Type:   patch.PatchRemoveNode,
		Target: p.Target,
		Reason: "rollback: remove restored node",
	}, nil
}

func (e *GraphPatchExecutor) applyReplaceNode(_ context.Context, g *Graph, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Remove old node and insert new node in its place.
	g.mu.RLock()
	oldNode, exists := g.nodes[p.Target]
	g.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("graph executor: node %q not found for replace", p.Target)
	}

	var newNode Node
	if n, ok := p.Value.(Node); ok {
		newNode = n
	} else {
		fn, err := NewFuncNode(p.Target, placeholderNodeExecute(p.Target))
		if err != nil {
			return nil, fmt.Errorf("graph executor: create replacement func node: %w", err)
		}
		newNode = fn
	}

	g.mu.Lock()
	g.nodes[p.Target] = newNode
	g.mu.Unlock()

	return &patch.RuntimePatch{
		Type:   patch.PatchReplaceNode,
		Target: p.Target,
		Value:  oldNode,
		Reason: "rollback: restore replaced node",
	}, nil
}

func (e *GraphPatchExecutor) applyAddEdge(_ context.Context, g *Graph, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	to, ok := p.Value.(string)
	if !ok {
		return nil, errors.New("graph executor: add edge value must be string (to node ID)")
	}

	_, err := g.Edge(p.Target, to)
	if err != nil {
		return nil, fmt.Errorf("graph executor: add edge %s→%s: %w", p.Target, to, err)
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchRemoveEdge,
		Target: p.Target,
		Value:  to,
		Reason: "rollback: remove added edge",
	}, nil
}

func (e *GraphPatchExecutor) applyRemoveEdge(_ context.Context, g *Graph, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	to, ok := p.Value.(string)
	if !ok {
		return nil, errors.New("graph executor: remove edge value must be string (to node ID)")
	}

	_, err := g.RemoveEdge(p.Target, to)
	if err != nil {
		return nil, fmt.Errorf("graph executor: remove edge %s→%s: %w", p.Target, to, err)
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchAddEdge,
		Target: p.Target,
		Value:  to,
		Reason: "rollback: re-add removed edge",
	}, nil
}

func (e *GraphPatchExecutor) applyChangeScheduler(
	_ context.Context,
	g *Graph,
	p patch.RuntimePatch,
) (*patch.RuntimePatch, error) {
	newSched, ok := p.Value.(Scheduler)
	if !ok {
		return nil, errors.New("graph executor: change scheduler value must be a Scheduler")
	}

	// Capture old scheduler for rollback. g.scheduler is guarded by
	// g.mu (SetScheduler writes it under the write lock), so the read
	// must hold the read lock too — matching the pattern used by the sibling
	// apply* functions and avoiding a data race with concurrent SetScheduler.
	g.mu.RLock()
	oldSched := g.scheduler
	g.mu.RUnlock()

	_, err := g.SetScheduler(newSched)
	if err != nil {
		return nil, fmt.Errorf("graph executor: change scheduler: %w", err)
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchChangeScheduler,
		Value:  oldSched,
		Reason: "rollback: restore previous scheduler",
	}, nil
}

// PlaceholderResult is the output written to state by structural placeholder
// nodes — evolution-inserted or replaced nodes that have no real tool/agent
// executor backing them. It explicitly signals that no real work was performed
// so downstream nodes and observers can distinguish a genuine no-op placeholder
// from a node that produced real output. This is NOT a fabricated success: the
// Placeholder flag and Reason make the absence of real work observable, which
// is the honest counterpart to the previous silent no-op that returned success
// doing nothing.
type PlaceholderResult struct {
	Placeholder bool   `json:"placeholder"`
	NodeID      string `json:"node_id"`
	Reason      string `json:"reason"`
}

// placeholderNodeExecute returns a FuncNode executor for evolution-inserted
// structural nodes that have no real tool/agent backing. Rather than silently
// returning success (which would pretend work was done), it writes a
// PlaceholderResult into state under "node.<id>" so the absence of real work is
// explicitly observable by callers. It returns nil so the DAG stays
// topologically valid for topology-only evolution; callers that need real
// output must later replace the node with a real executor via PatchReplaceNode.
func placeholderNodeExecute(nodeID string) func(context.Context, *State) error {
	return func(_ context.Context, state *State) error {
		if state == nil {
			return nil
		}
		state.Set("node."+nodeID, PlaceholderResult{
			Placeholder: true,
			NodeID:      nodeID,
			Reason:      "structural placeholder: no executor configured",
		})
		return nil
	}
}
