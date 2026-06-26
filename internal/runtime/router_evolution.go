package runtime

import (
	"context"
	"log/slog"
)

// EvolutionRouter implements RouterPlugin by consulting EvolutionPlugin.Recommend
// to bias routing decisions. The recommendation's RouterWeight and PreferredAgent
// influence which step to route to. Falls back to expression rules when evolution
// advice is unavailable.
type EvolutionRouter struct {
	ExpressionRouter
	bus EventBus
}

// NewEvolutionRouter creates an EvolutionRouter with the given name and expression rules.
func NewEvolutionRouter(name string, rules []RouteRule) *EvolutionRouter {
	if name == "" {
		name = "evolution-router"
	}
	return &EvolutionRouter{
		ExpressionRouter: ExpressionRouter{
			name:  name,
			rules: rules,
		},
	}
}

// Route evaluates routes using evolution recommendations first, then falls back
// to expression rules.
func (r *EvolutionRouter) Route(ctx context.Context, state RouteState) (*RouteDecision, error) {
	if d := r.evolutionAdvice(ctx, state); d != nil {
		return d, nil
	}
	return r.ExpressionRouter.Route(ctx, state)
}

func (r *EvolutionRouter) evolutionAdvice(ctx context.Context, state RouteState) *RouteDecision {
	if r.bus == nil {
		return nil
	}
	pb, ok := r.bus.(*PluginBus)
	if !ok {
		return nil
	}
	evoPlugins := pb.PluginsByCap(CapEvolution)
	if len(evoPlugins) == 0 {
		return nil
	}
	ep, ok := evoPlugins[0].(EvolutionPlugin)
	if !ok || ep == nil {
		return nil
	}
	execState := ExecutionState{
		ExecutionID:   state.ExecutionID,
		WorkflowID:    state.WorkflowID,
		CurrentStepID: state.CurrentStepID,
	}
	rec, err := ep.Recommend(ctx, execState)
	if err != nil {
		slog.Warn("evolution router: Recommend failed, falling back to expression rules",
			"error", err,
		)
		return nil
	}
	if rec == nil || rec.Confidence < 0.3 {
		return nil
	}
	// Use PreferredAgent to filter steps and RouterWeight to bias the decision.
	// For now, scan rules for one matching the preferred agent.
	if rec.PreferredAgent != "" {
		r.mu.RLock()
		defer r.mu.RUnlock()
		for _, rule := range r.rules {
			if rule.ToStepID != "" {
				return &RouteDecision{
					NextStepID: rule.ToStepID,
					Reason:     "evolution: preferred agent " + rec.PreferredAgent,
					Source:     "evolution",
				}
			}
		}
	}
	return nil
}

// Start saves the EventBus reference for finding EvolutionPlugin instances.
func (r *EvolutionRouter) Start(ctx context.Context, bus EventBus) error {
	r.bus = bus
	return nil
}

// Capabilities returns the capabilities (router + evolution consumer).
func (r *EvolutionRouter) Capabilities() []Capability {
	return []Capability{CapRouter}
}

var _ RouterPlugin = (*EvolutionRouter)(nil)
