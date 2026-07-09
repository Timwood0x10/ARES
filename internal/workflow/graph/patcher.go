// Package graph provides dynamic agent orchestration with pluggable scheduling.
//
// It also includes Runtime Patch executors for the Evolution system.
package graph

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// ── GraphPatchExecutor ──────────────────────

// GraphPatchExecutor handles DAG-related runtime patches.
// It wraps a *Graph and applies InsertNode/RemoveNode/ReplaceNode/AddEdge/RemoveEdge/ChangeScheduler.
type GraphPatchExecutor struct {
	graph *Graph
}

// NewGraphPatchExecutor creates a new GraphPatchExecutor.
func NewGraphPatchExecutor(g *Graph) *GraphPatchExecutor {
	return &GraphPatchExecutor{graph: g}
}

// Apply applies a runtime patch to the graph.
func (e *GraphPatchExecutor) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	switch p.Type {
	case patch.PatchInsertNode:
		return e.applyInsertNode(ctx, p)
	case patch.PatchRemoveNode:
		return e.applyRemoveNode(ctx, p)
	case patch.PatchReplaceNode:
		return e.applyReplaceNode(ctx, p)
	case patch.PatchAddEdge:
		return e.applyAddEdge(ctx, p)
	case patch.PatchRemoveEdge:
		return e.applyRemoveEdge(ctx, p)
	case patch.PatchChangeScheduler:
		return e.applyChangeScheduler(ctx, p)
	default:
		return nil, fmt.Errorf("graph executor: unsupported patch type %s", p.Type)
	}
}

// CanApply checks whether a patch can be applied.
func (e *GraphPatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	if e.graph == nil {
		return fmt.Errorf("graph executor: graph is nil")
	}
	switch p.Type {
	case patch.PatchInsertNode:
		if p.Target == "" {
			return fmt.Errorf("graph executor: insert node requires non-empty target")
		}
		return nil
	case patch.PatchRemoveNode:
		if p.Target == "" {
			return fmt.Errorf("graph executor: remove node requires non-empty target")
		}
		return nil
	case patch.PatchReplaceNode:
		if p.Target == "" {
			return fmt.Errorf("graph executor: replace node requires non-empty target")
		}
		return nil
	case patch.PatchAddEdge:
		if p.Target == "" {
			return fmt.Errorf("graph executor: add edge requires non-empty from")
		}
		to, ok := p.Value.(string)
		if !ok || to == "" {
			return fmt.Errorf("graph executor: add edge value must be non-empty string (to)")
		}
		return nil
	case patch.PatchRemoveEdge:
		if p.Target == "" {
			return fmt.Errorf("graph executor: remove edge requires non-empty from")
		}
		to, ok := p.Value.(string)
		if !ok || to == "" {
			return fmt.Errorf("graph executor: remove edge value must be non-empty string (to)")
		}
		return nil
	case patch.PatchChangeScheduler:
		return nil
	default:
		return fmt.Errorf("graph executor: unsupported patch type %s", p.Type)
	}
}

// ── Apply implementations ───────────────────

func (e *GraphPatchExecutor) applyInsertNode(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Determine the node to insert.
	var node Node
	if n, ok := p.Value.(Node); ok {
		node = n
	} else {
		// Create a FuncNode with the target ID.
		fn, err := NewFuncNode(p.Target, defaultNodeExecute)
		if err != nil {
			return nil, fmt.Errorf("graph executor: create func node: %w", err)
		}
		node = fn
	}

	// Capture the old node if it exists (for rollback).
	oldNode := e.graph.nodes[p.Target]

	_, err := e.graph.Node(p.Target, node)
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

func (e *GraphPatchExecutor) applyRemoveNode(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Capture the node before removing (for rollback).
	oldNode, exists := e.graph.nodes[p.Target]
	if !exists {
		return nil, fmt.Errorf("graph executor: node %q not found", p.Target)
	}

	_, err := e.graph.RemoveNode(p.Target)
	if err != nil {
		return nil, fmt.Errorf("graph executor: remove node %s: %w", p.Target, err)
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchInsertNode,
		Target: p.Target,
		Value:  oldNode,
		Reason: "rollback: re-insert removed node",
	}, nil
}

func (e *GraphPatchExecutor) applyReplaceNode(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	// Remove old node and insert new node in its place.
	oldNode, exists := e.graph.nodes[p.Target]
	if !exists {
		return nil, fmt.Errorf("graph executor: node %q not found for replace", p.Target)
	}

	var newNode Node
	if n, ok := p.Value.(Node); ok {
		newNode = n
	} else {
		fn, err := NewFuncNode(p.Target, defaultNodeExecute)
		if err != nil {
			return nil, fmt.Errorf("graph executor: create replacement func node: %w", err)
		}
		newNode = fn
	}

	e.graph.mu.Lock()
	e.graph.nodes[p.Target] = newNode
	e.graph.mu.Unlock()

	return &patch.RuntimePatch{
		Type:   patch.PatchReplaceNode,
		Target: p.Target,
		Value:  oldNode,
		Reason: "rollback: restore replaced node",
	}, nil
}

func (e *GraphPatchExecutor) applyAddEdge(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	to, ok := p.Value.(string)
	if !ok {
		return nil, fmt.Errorf("graph executor: add edge value must be string (to node ID)")
	}

	_, err := e.graph.Edge(p.Target, to)
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

func (e *GraphPatchExecutor) applyRemoveEdge(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	to, ok := p.Value.(string)
	if !ok {
		return nil, fmt.Errorf("graph executor: remove edge value must be string (to node ID)")
	}

	_, err := e.graph.RemoveEdge(p.Target, to)
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

func (e *GraphPatchExecutor) applyChangeScheduler(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	newSched, ok := p.Value.(Scheduler)
	if !ok {
		return nil, fmt.Errorf("graph executor: change scheduler value must be a Scheduler")
	}

	// Capture old scheduler for rollback.
	oldSched := e.graph.scheduler

	_, err := e.graph.SetScheduler(newSched)
	if err != nil {
		return nil, fmt.Errorf("graph executor: change scheduler: %w", err)
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchChangeScheduler,
		Value:  oldSched,
		Reason: "rollback: restore previous scheduler",
	}, nil
}

// ── defaultNodeExecute is a no-op execution function for evolved nodes. ──

func defaultNodeExecute(_ context.Context, state *State) error {
	// Evolved nodes are structural placeholders; real execution comes from
	// agent-backed nodes. This no-op ensures the DAG stays valid.
	return nil
}
