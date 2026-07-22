// Package patch provides the universal mutation language for ARES Runtime.
//
// Every subsystem (GA, Chaos, LLM, Human, K8s Operator) outputs RuntimePatch.
// Runtime applies them via the Apply function.
// If Apply fails, the automatic rollback undoes the change.
//
// Everything evolves by emitting Runtime Patches.
package patch

import (
	"context"
	"fmt"
)

// PatchType classifies a runtime mutation.
type PatchType int

const (
	// ── DAG mutations ──────────────────────────────────
	PatchInsertNode  PatchType = iota // Insert a new node into the DAG
	PatchRemoveNode                   // Remove a node from the DAG
	PatchReplaceNode                  // Replace a node with another
	PatchAddEdge                      // Add a directed edge between nodes
	PatchRemoveEdge                   // Remove a directed edge

	// ── Scheduler mutations ────────────────────────────
	PatchChangeScheduler // Replace the current scheduler

	// ── Knowledge/Planner mutations ────────────────────
	PatchChangePlanner // Change planner strategy
	PatchChangeReducer // Change reducer strategy
	PatchChangeBudget  // Change knowledge budget (e.g. TopK)

	// ── Recovery mutations ────────────────────────────
	PatchChangeRecoveryStrategy // Change recovery strategy (retry/replace/fail)
	PatchChangeMaxRetries       // Change max retry count
	PatchChangeBackoff          // Change backoff duration
)

// String returns a human-readable name for the patch type.
func (pt PatchType) String() string {
	switch pt {
	case PatchInsertNode:
		return "insert_node"
	case PatchRemoveNode:
		return "remove_node"
	case PatchReplaceNode:
		return "replace_node"
	case PatchAddEdge:
		return "add_edge"
	case PatchRemoveEdge:
		return "remove_edge"
	case PatchChangeScheduler:
		return "change_scheduler"
	case PatchChangePlanner:
		return "change_planner"
	case PatchChangeReducer:
		return "change_reducer"
	case PatchChangeBudget:
		return "change_budget"
	case PatchChangeRecoveryStrategy:
		return "change_recovery_strategy"
	case PatchChangeMaxRetries:
		return "change_max_retries"
	case PatchChangeBackoff:
		return "change_backoff"
	default:
		return fmt.Sprintf("unknown(%d)", int(pt))
	}
}

// RuntimePatch is the universal mutation unit.
// Source identifies who proposed it (genome / chaos / llm / human / k8s).
// If Rollback is non-nil, Runtime can undo the patch on failure.
type RuntimePatch struct {
	Type     PatchType     `json:"type"`               // what to change
	Target   string        `json:"target"`             // what to change (node ID / component name)
	Value    any           `json:"value,omitempty"`    // what to become (new Node / Scheduler / Config)
	Reason   string        `json:"reason,omitempty"`   // why this change was proposed
	Source   string        `json:"source,omitempty"`   // who proposed it
	Rollback *RuntimePatch `json:"rollback,omitempty"` // inverse patch for rollback
}

// PatchSet is an atomic batch of patches.
// All patches are applied in order; if any fails, all are rolled back.
type PatchSet struct {
	Patches []RuntimePatch `json:"patches"`
	Reason  string         `json:"reason"`           // why this batch was proposed
	Source  string         `json:"source,omitempty"` // batch source
}

// Executor applies a RuntimePatch to a specific subsystem.
// Each subsystem (DAG, Scheduler, Planner, Recovery) implements this interface.
type Executor interface {
	// Apply applies the patch and returns a rollback patch.
	// If the patch cannot be applied, Apply returns an error.
	// The rollback patch can be used to undo the change.
	Apply(ctx context.Context, patch RuntimePatch) (*RuntimePatch, error)

	// CanApply returns nil if the patch can be applied, or an error explaining why not.
	CanApply(ctx context.Context, patch RuntimePatch) error
}

// RuntimeComponent is the unified interface for all evolvable runtime subsystems.
// It extends Executor with Name and Snapshot, enabling the Coordinator to discover
// and snapshot any component without knowing its concrete type.
//
// Every subsystem (DAG, Scheduler, Planner, Knowledge, Recovery) implements this
// interface to participate in runtime evolution.
type RuntimeComponent interface {
	// Name returns the component identifier, used for registry lookup.
	Name() string

	// Snapshot returns a serializable representation of the component's current
	// state. Used by Diff Engine to compute changes between generations.
	Snapshot(ctx context.Context) (any, error)

	// Apply applies the patch and returns a rollback patch.
	// If the patch cannot be applied, Apply returns an error.
	// The rollback patch can be used to undo the change.
	Apply(ctx context.Context, patch RuntimePatch) (*RuntimePatch, error)

	// CanApply returns nil if the patch can be applied, or an error explaining why not.
	CanApply(ctx context.Context, patch RuntimePatch) error
}

// ExecutorComponent wraps an Executor as a RuntimeComponent.
// This adapter allows existing Executor implementations to participate in the
// RuntimeComponent ecosystem without immediate migration.
// Snapshot returns (nil, nil) by default — concrete implementations should
// override by implementing RuntimeComponent directly.
type ExecutorComponent struct {
	name     string
	executor Executor
}

// NewExecutorComponent creates a RuntimeComponent adapter from an Executor.
func NewExecutorComponent(name string, ex Executor) *ExecutorComponent {
	return &ExecutorComponent{name: name, executor: ex}
}

// Name returns the component name passed at construction time.
func (c *ExecutorComponent) Name() string { return c.name }

// Snapshot returns a nil snapshot. Components that support diffing should
// implement RuntimeComponent directly instead of using this adapter.
func (c *ExecutorComponent) Snapshot(_ context.Context) (any, error) { return nil, ErrNoSnapshot }

// Apply delegates to the wrapped Executor.
func (c *ExecutorComponent) Apply(ctx context.Context, patch RuntimePatch) (*RuntimePatch, error) {
	return c.executor.Apply(ctx, patch)
}

// CanApply delegates to the wrapped Executor.
func (c *ExecutorComponent) CanApply(ctx context.Context, patch RuntimePatch) error {
	return c.executor.CanApply(ctx, patch)
}

// sentinel errors for the patch package.
var (
	// ErrNoSnapshot is returned by Snapshot when no snapshot is available.
	ErrNoSnapshot = fmt.Errorf("patch: no snapshot available")
)

// Ensure ExecutorComponent implements RuntimeComponent.
var _ RuntimeComponent = (*ExecutorComponent)(nil)

// Registry manages patch executors and runtime components by target name.
type Registry struct {
	executors map[string]Executor
	// fallback is a component that handles patches for targets that have no
	// dedicated executor registered. This enables catch-all executors like
	// liveDAGPatchExecutor to handle all workflow structure patches (insert/
	// remove nodes/edges) whose targets are dynamic node IDs.
	fallback RuntimeComponent
}

// NewRegistry creates a new patch registry.
func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[string]Executor),
	}
}

// SetFallback sets a fallback component that handles patches for targets
// with no dedicated executor. When Apply cannot find an executor by target,
// it delegates to the fallback if one is set.
func (r *Registry) SetFallback(comp RuntimeComponent) {
	r.fallback = comp
}

// Register registers an executor for a target component.
func (r *Registry) Register(target string, ex Executor) error {
	if target == "" {
		return fmt.Errorf("patch: target must not be empty")
	}
	if ex == nil {
		return fmt.Errorf("patch: executor must not be nil")
	}
	if _, exists := r.executors[target]; exists {
		return fmt.Errorf("patch: executor for %q already registered", target)
	}
	r.executors[target] = ex
	return nil
}

// RegisterComponent registers a RuntimeComponent by its Name.
// This is the preferred registration method for new code.
func (r *Registry) RegisterComponent(comp RuntimeComponent) error {
	if comp == nil {
		return fmt.Errorf("patch: component must not be nil")
	}
	return r.Register(comp.Name(), comp)
}

// Apply dispatches a patch to the appropriate executor.
// First tries to find an executor by target name. If none is found and a
// fallback is set, delegates to the fallback. If no fallback exists, returns
// an error. If the patch has a Rollback, it is automatically applied on failure.
func (r *Registry) Apply(ctx context.Context, patch RuntimePatch) error {
	ex, ok := r.executors[patch.Target]
	if !ok {
		// No executor for this target — try the fallback if one is set.
		if r.fallback != nil {
			rollback, err := r.fallback.Apply(ctx, patch)
			if err != nil {
				return fmt.Errorf("patch %s on %s (fallback): %w", patch.Type, patch.Target, err)
			}
			_ = rollback
			return nil
		}
		return fmt.Errorf("patch: no executor registered for target %q", patch.Target)
	}
	rollback, err := ex.Apply(ctx, patch)
	if err != nil {
		// Attempt rollback if available.
		if rollback != nil {
			if rbEx, ok := r.executors[rollback.Target]; ok {
				if _, rbErr := rbEx.Apply(ctx, *rollback); rbErr != nil {
					return fmt.Errorf("patch %s failed (%w); rollback also failed: %v",
						patch.Type, err, rbErr)
				}
			}
		}
		return fmt.Errorf("patch %s on %s: %w", patch.Type, patch.Target, err)
	}
	return nil
}

// ApplySet applies a PatchSet atomically. If any patch in the set fails,
// all previously applied patches are rolled back in reverse order.
func (r *Registry) ApplySet(ctx context.Context, ps PatchSet) error {
	if len(ps.Patches) == 0 {
		return nil
	}

	// Track applied patches for rollback.
	type applied struct {
		patch    RuntimePatch
		rollback *RuntimePatch
	}
	var appliedPatches []applied

	for _, p := range ps.Patches {
		ex, ok := r.executors[p.Target]
		if !ok {
			// Try fallback if no dedicated executor.
			if r.fallback != nil {
				rollback, fbErr := r.fallback.Apply(ctx, p)
				if fbErr != nil {
					// Rollback all previously applied patches.
					for i := len(appliedPatches) - 1; i >= 0; i-- {
						ap := appliedPatches[i]
						if ap.rollback == nil {
							continue
						}
						if rbEx, ok := r.executors[ap.rollback.Target]; ok {
							_, _ = rbEx.Apply(ctx, *ap.rollback)
						}
					}
					return fmt.Errorf("patch set: no executor for target %q (fallback also failed: %w)", p.Target, fbErr)
				}
				appliedPatches = append(appliedPatches, applied{patch: p, rollback: rollback})
				continue
			}
			// Rollback all previously applied patches in reverse order.
			for i := len(appliedPatches) - 1; i >= 0; i-- {
				ap := appliedPatches[i]
				if ap.rollback == nil {
					continue
				}
				if rbEx, ok := r.executors[ap.rollback.Target]; ok {
					_, _ = rbEx.Apply(ctx, *ap.rollback)
				}
			}
			return fmt.Errorf("patch set: no executor for target %q", p.Target)
		}

		canErr := ex.CanApply(ctx, p)
		if canErr != nil {
			// Rollback all previously applied patches in reverse order.
			for i := len(appliedPatches) - 1; i >= 0; i-- {
				ap := appliedPatches[i]
				if ap.rollback == nil {
					continue
				}
				if rbEx, ok := r.executors[ap.rollback.Target]; ok {
					_, _ = rbEx.Apply(ctx, *ap.rollback)
				}
			}
			return fmt.Errorf("patch set: cannot apply %s on %s: %w", p.Type, p.Target, canErr)
		}

		rollback, err := ex.Apply(ctx, p)
		if err != nil {
			// Rollback all previously applied patches in reverse order.
			for i := len(appliedPatches) - 1; i >= 0; i-- {
				ap := appliedPatches[i]
				if ap.rollback == nil {
					continue
				}
				if rbEx, ok := r.executors[ap.rollback.Target]; ok {
					_, _ = rbEx.Apply(ctx, *ap.rollback)
				}
			}
			return fmt.Errorf("patch set: apply %s on %s failed: %w", p.Type, p.Target, err)
		}

		appliedPatches = append(appliedPatches, applied{patch: p, rollback: rollback})
	}

	return nil
}
