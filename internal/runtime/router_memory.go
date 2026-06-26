package runtime

import (
	"context"
	"log/slog"
)

// MemoryRouter implements RouterPlugin by consulting MemoryPlugin.AdviseRoute
// and falling back to expression rules when memory advice is unavailable or
// below the confidence threshold.
type MemoryRouter struct {
	ExpressionRouter
	bus           EventBus
	confidenceMin float64 // minimum confidence to accept memory advice (default 0.5)
}

// NewMemoryRouter creates a MemoryRouter with the given name, expression rules,
// and minimum confidence threshold. If confidenceMin is 0, the default of 0.5 is used.
func NewMemoryRouter(name string, rules []RouteRule, confidenceMin float64) *MemoryRouter {
	if name == "" {
		name = "memory-router"
	}
	if confidenceMin <= 0 {
		confidenceMin = 0.5
	}
	return &MemoryRouter{
		ExpressionRouter: ExpressionRouter{
			name:  name,
			rules: rules,
		},
		confidenceMin: confidenceMin,
	}
}

// Route evaluates routes using memory advice first, then falls back to expression rules.
func (r *MemoryRouter) Route(ctx context.Context, state RouteState) (*RouteDecision, error) {
	// Try memory advice first.
	if d := r.memoryAdvice(ctx, state); d != nil {
		return d, nil
	}
	// Fall through to expression rules.
	return r.ExpressionRouter.Route(ctx, state)
}

func (r *MemoryRouter) memoryAdvice(ctx context.Context, state RouteState) *RouteDecision {
	if r.bus == nil {
		return nil
	}
	pb, ok := r.bus.(*PluginBus)
	if !ok {
		return nil
	}
	memPlugins := pb.PluginsByCap(CapMemory)
	if len(memPlugins) == 0 {
		return nil
	}
	mp, ok := memPlugins[0].(MemoryPlugin)
	if !ok || mp == nil {
		return nil
	}
	advice, err := mp.AdviseRoute(ctx, state)
	if err != nil {
		slog.Warn("memory router: AdviseRoute failed, falling back to expression rules",
			"error", err,
		)
		return nil
	}
	if len(advice) == 0 {
		return nil
	}
	// Pick the advice with highest confidence above threshold.
	best := advice[0]
	for _, a := range advice[1:] {
		if a.Confidence > best.Confidence {
			best = a
		}
	}
	if best.Confidence < r.confidenceMin {
		slog.Debug("memory router: best advice below confidence threshold, falling back",
			"confidence", best.Confidence,
			"threshold", r.confidenceMin,
		)
		return nil
	}
	return &RouteDecision{
		NextStepID: best.NextStepID,
		Reason:     best.Reason,
		Source:     "memory",
	}
}

// Start saves the EventBus reference for finding MemoryPlugin instances.
func (r *MemoryRouter) Start(ctx context.Context, bus EventBus) error {
	r.bus = bus
	return nil
}

// Capabilities returns the capabilities (router + memory consumer).
func (r *MemoryRouter) Capabilities() []Capability {
	return []Capability{CapRouter}
}

var _ RouterPlugin = (*MemoryRouter)(nil)
