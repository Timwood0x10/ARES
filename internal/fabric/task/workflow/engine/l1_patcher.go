package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// L1PatchExecutor applies PatchSetNodeMetadata to the L1 ToolClass capability
// graph (M6). The L1 graph is a *MutableDAG injected via peer_assembly.go:
// one node per toolName#argShape, whose enabled/budget/prior metadata
// constrains L2 growth. This executor is the "third SetNodeMetadata call
// site" the PLAN-0.3.6-scheduling §M6 requires — it closes the loop so GA
// patches reach the L1 graph the planner reads at the next quantum.
//
// Unlike DAGPatchExecutor, this executor ONLY accepts PatchSetNodeMetadata:
// the L1 graph's topology (nodes and edges) is static — it is built from the
// tool registry at assembly time and evolution only adjusts the metadata
// (enabled/budget/prior), never the shape.
//
// Snapshot/Restore implement patch.Restorable so deployment rollback can
// revert an L1 metadata patch in place.
type L1PatchExecutor struct {
	// mu guards the dag pointer: SetDAG rebinds the executor to a new L1
	// DAG after registration (bootstrap), possibly while patches from the
	// evolution loop apply concurrently.
	mu  sync.RWMutex
	dag *MutableDAG
}

// NewL1PatchExecutor creates an L1 metadata executor bound to the given L1
// ToolClass DAG. dag may be nil if no L1 graph is wired (the executor will
// return an error on Apply — backward compatible with deployments that have
// no L1 graph).
// TODO(tech-debt): nothing in production can reach this executor yet, for TWO reasons, and fixing
// only one of them would produce dead code:
//
//  1. No producer. The only differ is WorkflowDiffer, which diffs workflow DAGs; there is no
//     differ over the L1 tool-class graph, so no PatchSetNodeMetadata naming an L1 node is ever
//     emitted.
//  2. No routing. patch.Registry dispatches by component name and falls back to the executor bound
//     to the live workflow DAG, while an L1 patch names a NODE id — so even with a producer the
//     patch would land on the wrong graph and fail on "target not found".
//
// Both are M6 of the 0.3.6 scheduling plan. Do not read the passing tests here as evidence that
// L1 metadata has a production writer.
func NewL1PatchExecutor(dag *MutableDAG) *L1PatchExecutor {
	return &L1PatchExecutor{dag: dag}
}

// Name returns the component identifier for the patch registry.
func (e *L1PatchExecutor) Name() string { return "l1.toolclass" }

// SetDAG rebinds the executor to a (possibly new) L1 DAG.
func (e *L1PatchExecutor) SetDAG(dag *MutableDAG) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dag = dag
}

// DAG returns the currently bound L1 DAG.
func (e *L1PatchExecutor) DAG() *MutableDAG {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dag
}

// currentDAG is a single read point so Apply/Snapshot/Restore share the
// same snapshot semantics.
func (e *L1PatchExecutor) currentDAG() *MutableDAG {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dag
}

// Snapshot captures the entire L1 DAG state for rollback. Returns nil when
// no L1 DAG is wired (rollback is a no-op).
func (e *L1PatchExecutor) Snapshot() any {
	dag := e.currentDAG()
	if dag == nil {
		return nil
	}
	steps := dag.Steps()
	cloned := make([]*Step, len(steps))
	for i, s := range steps {
		cloned[i] = cloneStepForSnapshot(s)
	}
	return &DAGSnapshot{Steps: cloned}
}

// Restore reverts the L1 DAG to a previously captured snapshot.
func (e *L1PatchExecutor) Restore(snap any) error {
	if snap == nil {
		return nil
	}
	s, ok := snap.(*DAGSnapshot)
	if !ok {
		return fmt.Errorf("l1.toolclass restore: unsupported snapshot type %T", snap)
	}
	dag := e.currentDAG()
	if dag == nil {
		return errors.New("l1.toolclass restore: dag is nil")
	}
	return dag.ResetFromSteps(s.Steps)
}

// CanApply reports which patch types this executor accepts. The L1 executor
// ONLY accepts PatchSetNodeMetadata — the L1 graph's topology is static.
func (e *L1PatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	switch p.Type {
	case patch.PatchSetNodeMetadata:
		return nil
	default:
		return fmt.Errorf("l1.toolclass: unsupported patch type %v on target %q (L1 topology is static)", p.Type, p.Target)
	}
}

// Apply mutates the L1 DAG's node metadata and returns an inverse patch for
// rollback. The old metadata is captured BEFORE the mutation so the inverse
// is a PatchSetNodeMetadata carrying the previous values.
func (e *L1PatchExecutor) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	dag := e.currentDAG()
	if dag == nil {
		return nil, errors.New("l1.toolclass: dag is nil (no L1 graph wired)")
	}

	if p.Type != patch.PatchSetNodeMetadata {
		return nil, fmt.Errorf("l1.toolclass: unsupported patch type %v on target %q", p.Type, p.Target)
	}

	var md map[string]string
	switch v := p.Value.(type) {
	case map[string]string:
		md = v
	case *Step:
		md = v.Metadata
	case Step:
		md = v.Metadata
	default:
		return nil, fmt.Errorf("l1.toolclass set-node-metadata %q: value %T is not a metadata map", p.Target, p.Value)
	}
	// Capture the old metadata BEFORE the mutation for the inverse patch.
	old := dag.StepSnapshot(p.Target) // O(1) deep copy; nil when absent
	if err := dag.SetNodeMetadata(p.Target, md); err != nil {
		return nil, fmt.Errorf("l1.toolclass set-node-metadata %q: %w", p.Target, err)
	}
	// The inverse is a PatchSetNodeMetadata carrying the old metadata.
	// When the node did not exist before (old == nil), the inverse is
	// still a SetNodeMetadata with nil metadata — SetNodeMetadata handles
	// nil by clearing the node's metadata, which is the correct rollback
	// for a newly-created metadata entry.
	return &patch.RuntimePatch{Type: patch.PatchSetNodeMetadata, Target: p.Target, Value: old}, nil
}
