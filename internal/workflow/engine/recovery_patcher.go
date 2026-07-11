// Package engine provides the workflow execution engine with recoverable step execution.
package engine

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// RecoveryPatchExecutor handles recovery-related runtime patches.
// It wraps a MutableDAG and applies ChangeRecoveryStrategy/ChangeMaxRetries.
// Implements patch.RuntimeComponent for unified runtime evolution.
type RecoveryPatchExecutor struct {
	dag *MutableDAG
}

// NewRecoveryPatchExecutor creates a new RecoveryPatchExecutor.
func NewRecoveryPatchExecutor(dag *MutableDAG) *RecoveryPatchExecutor {
	return &RecoveryPatchExecutor{dag: dag}
}

// Name returns "recovery" as the component identifier for patch routing.
func (e *RecoveryPatchExecutor) Name() string { return "recovery" }

// Snapshot returns the current recovery configuration as a snapshot.
func (e *RecoveryPatchExecutor) Snapshot(_ context.Context) (any, error) {
	if e.dag == nil {
		return nil, patch.ErrNoSnapshot
	}
	return e.dag, nil
}

// Ensure RecoveryPatchExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*RecoveryPatchExecutor)(nil)

// Apply applies a runtime patch to the DAG's recovery configuration.
func (e *RecoveryPatchExecutor) Apply(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	switch p.Type {
	case patch.PatchChangeRecoveryStrategy:
		return e.applyChangeStrategy(p)
	case patch.PatchChangeMaxRetries:
		return e.applyChangeMaxRetries(p)
	case patch.PatchChangeBackoff:
		return e.applyChangeBackoff(p)
	default:
		return nil, fmt.Errorf("recovery executor: unsupported patch type %s", p.Type)
	}
}

// CanApply checks whether a patch can be applied.
func (e *RecoveryPatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	if e.dag == nil {
		return fmt.Errorf("recovery executor: dag is nil")
	}
	switch p.Type {
	case patch.PatchChangeRecoveryStrategy:
		strategy, ok := p.Value.(string)
		if !ok {
			return fmt.Errorf("recovery executor: ChangeRecoveryStrategy value must be string")
		}
		switch RecoveryStrategy(strategy) {
		case RecoveryRetry, RecoveryReplaceNode, RecoveryFailFast:
			return nil
		default:
			return fmt.Errorf("recovery executor: unknown strategy %q", strategy)
		}
	case patch.PatchChangeMaxRetries:
		_, ok := p.Value.(int)
		if !ok {
			return fmt.Errorf("recovery executor: ChangeMaxRetries value must be int")
		}
		return nil
	case patch.PatchChangeBackoff:
		return nil
	default:
		return fmt.Errorf("recovery executor: unsupported patch type %s", p.Type)
	}
}

func (e *RecoveryPatchExecutor) applyChangeStrategy(p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	strategy, ok := p.Value.(string)
	if !ok {
		return nil, fmt.Errorf("recovery executor: ChangeRecoveryStrategy value must be string")
	}

	newStrategy := RecoveryStrategy(strategy)

	// Apply to all steps in the DAG.
	steps := e.dag.Steps()
	if len(steps) == 0 {
		return nil, fmt.Errorf("recovery executor: no steps in DAG to apply strategy")
	}

	var oldStrategy RecoveryStrategy
	for _, step := range steps {
		if step.RecoveryPolicy != nil {
			oldStrategy = step.RecoveryPolicy.Strategy
			step.RecoveryPolicy.Strategy = newStrategy
		} else {
			// Create a recovery policy for steps that don't have one.
			step.RecoveryPolicy = &RecoveryPolicy{
				Strategy: newStrategy,
			}
		}
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchChangeRecoveryStrategy,
		Value:  string(oldStrategy),
		Reason: "rollback: restore previous recovery strategy",
	}, nil
}

func (e *RecoveryPatchExecutor) applyChangeMaxRetries(p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	newMax, ok := p.Value.(int)
	if !ok {
		return nil, fmt.Errorf("recovery executor: ChangeMaxRetries value must be int")
	}

	steps := e.dag.Steps()
	if len(steps) == 0 {
		return nil, fmt.Errorf("recovery executor: no steps in DAG to apply max retries")
	}

	var oldMax int
	for _, step := range steps {
		if step.RecoveryPolicy != nil {
			oldMax = step.RecoveryPolicy.MaxAttempts
			step.RecoveryPolicy.MaxAttempts = newMax
		}
	}

	return &patch.RuntimePatch{
		Type:   patch.PatchChangeMaxRetries,
		Value:  oldMax,
		Reason: "rollback: restore previous max retries",
	}, nil
}

func (e *RecoveryPatchExecutor) applyChangeBackoff(p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	return &patch.RuntimePatch{
		Type:   patch.PatchChangeBackoff,
		Reason: "rollback: restore previous backoff",
	}, nil
}
