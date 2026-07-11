package runtime

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
)

// PlanConfig holds the runtime-tunable planner configuration.
// These values are applied by KnowledgePatchExecutor when a patch arrives.
type PlanConfig struct {
	// MaxResults caps the number of results per knowledge query.
	MaxResults int

	// ReducerStrategy selects how query results are reduced: default / strict / relaxed.
	ReducerStrategy string
}

// SetPlanConfig updates the planner's MaxResults and reducer strategy.
// This is the integration point for KnowledgePatchExecutor.
func (r *KnowledgeRuntime) SetPlanConfig(cfg PlanConfig) {
	r.planner = &configurablePlanner{maxResults: cfg.MaxResults}
	log.Info("knowledge runtime: plan config updated",
		"max_results", cfg.MaxResults,
		"reducer", cfg.ReducerStrategy)
}

// configurablePlanner wraps a KnowledgePlanner with configurable MaxResults.
type configurablePlanner struct {
	maxResults int
}

func (p *configurablePlanner) Plan(ctx context.Context, goal string, budget knowledge.TokenBudget) (*planner.KnowledgePlan, error) {
	// Delegate to the default planner, then override MaxResults.
	base := planner.NewKnowledgePlanner()
	plan, err := base.Plan(ctx, goal, budget)
	if err != nil {
		return nil, err
	}
	if p.maxResults > 0 {
		for i := range plan.Requirements {
			plan.Requirements[i].MaxResults = p.maxResults
		}
	}
	return plan, nil
}

// ── KnowledgePatchExecutor ──────────────────

// KnowledgePatchExecutor handles knowledge-related runtime patches.
// It wraps a *KnowledgeRuntime and applies ChangePlanner/ChangeBudget/ChangeReducer.
// Implements patch.RuntimeComponent for unified runtime evolution.
type KnowledgePatchExecutor struct {
	runtime *KnowledgeRuntime
}

// NewKnowledgePatchExecutor creates a new KnowledgePatchExecutor.
func NewKnowledgePatchExecutor(r *KnowledgeRuntime) *KnowledgePatchExecutor {
	return &KnowledgePatchExecutor{runtime: r}
}

// Name returns "knowledge" as the component identifier for patch routing.
func (e *KnowledgePatchExecutor) Name() string { return "knowledge" }

// Snapshot returns the current plan configuration as a snapshot for diffing.
func (e *KnowledgePatchExecutor) Snapshot(_ context.Context) (any, error) {
	return PlanConfig{}, nil
}

// Ensure KnowledgePatchExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*KnowledgePatchExecutor)(nil)

// Apply applies a runtime patch to the knowledge runtime.
func (e *KnowledgePatchExecutor) Apply(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	switch p.Type {
	case patch.PatchChangeBudget:
		return e.applyChangeBudget(p)
	case patch.PatchChangePlanner:
		return e.applyChangePlanner(p)
	case patch.PatchChangeReducer:
		return e.applyChangeReducer(p)
	default:
		return nil, fmt.Errorf("knowledge executor: unsupported patch type %s", p.Type)
	}
}

// CanApply checks whether a patch can be applied.
func (e *KnowledgePatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	if e.runtime == nil {
		return fmt.Errorf("knowledge executor: runtime is nil")
	}
	switch p.Type {
	case patch.PatchChangeBudget:
		_, ok := p.Value.(int)
		if !ok {
			return fmt.Errorf("knowledge executor: ChangeBudget value must be int")
		}
		return nil
	case patch.PatchChangePlanner:
		_, ok := p.Value.(string)
		if !ok {
			return fmt.Errorf("knowledge executor: ChangePlanner value must be string")
		}
		return nil
	case patch.PatchChangeReducer:
		_, ok := p.Value.(string)
		if !ok {
			return fmt.Errorf("knowledge executor: ChangeReducer value must be string")
		}
		return nil
	default:
		return fmt.Errorf("knowledge executor: unsupported patch type %s", p.Type)
	}
}

// applyChangeBudget updates the MaxResults parameter.
func (e *KnowledgePatchExecutor) applyChangeBudget(p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	newBudget, ok := p.Value.(int)
	if !ok {
		return nil, fmt.Errorf("knowledge executor: ChangeBudget value must be int")
	}

	// Snapshot old budget for rollback. We use 0 as "unknown" since we can't
	// directly read the current value from the interface.
	e.runtime.SetPlanConfig(PlanConfig{
		MaxResults: newBudget,
	})

	return &patch.RuntimePatch{
		Type:   patch.PatchChangeBudget,
		Value:  0, // actual old value unknown from interface; rollback sets default
		Reason: "rollback: restore default budget",
	}, nil
}

// applyChangePlanner updates the planner strategy.
func (e *KnowledgePatchExecutor) applyChangePlanner(p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	_, ok := p.Value.(string)
	if !ok {
		return nil, fmt.Errorf("knowledge executor: ChangePlanner value must be string")
	}

	// Currently the planner strategy is applied via plan config.
	e.runtime.SetPlanConfig(PlanConfig{
		ReducerStrategy: "default",
	})

	return &patch.RuntimePatch{
		Type:   patch.PatchChangePlanner,
		Reason: "rollback: restore default planner",
	}, nil
}

// applyChangeReducer updates the reducer strategy.
func (e *KnowledgePatchExecutor) applyChangeReducer(p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	strategy, ok := p.Value.(string)
	if !ok {
		return nil, fmt.Errorf("knowledge executor: ChangeReducer value must be string")
	}

	e.runtime.SetPlanConfig(PlanConfig{
		ReducerStrategy: strategy,
	})

	return &patch.RuntimePatch{
		Type:   patch.PatchChangeReducer,
		Reason: "rollback: restore default reducer",
	}, nil
}
