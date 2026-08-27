// Package ares_bootstrap — runtime observability providers (monitoring.md
// Phase 4: dashboard :8090 service deleted, M3/M4 providers migrated into the
// introspection control plane).
//
// Previously ProvideDashboard assembled a standalone :8090 gin server that
// historically was never started by serve (its endpoints fed a server no one
// could reach). Under the Phase 4 consolidation the three surfaces with real
// data — evolution trajectory (M3-1), human feedback (M3-2), cross-Fabric
// spans (M4-1) — are wired straight into introspect.ControlServer via
// introspect options, and the :8090 server itself is gone.
package ares_bootstrap

import (
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/introspect"
)

// ObservabilityProviders bundles the M3/M4 provider adapters handed to the
// introspection control plane. Fields are nil when the backing component is
// nil (endpoint disabled), matching the old dashboard behavior.
type ObservabilityProviders struct {
	// Trajectory backs /api/evolution/trajectory (v0.3.0 M3-1).
	Trajectory introspect.EvolutionTrajectoryProvider
	// Feedback backs POST /api/evolution/feedback (v0.3.0 M3-2).
	Feedback introspect.EvolutionFeedbackSink
	// Spans backs /api/observability/spans (v0.3.0 M4-1).
	Spans introspect.ObservabilitySpansProvider
}

// ProvideObservability wraps the SHARED aresrecovery components (created once
// in Bootstrap, not per-call) as introspection control-plane providers, so the
// endpoints read the same tracer / feedback store the runtime writes.
func ProvideObservability(
	evolutionTracer *aresrecovery.EvolutionTracer,
	feedbackStore *aresrecovery.FeedbackStore,
	globalTracer *aresrecovery.GlobalTracer,
) *ObservabilityProviders {
	return &ObservabilityProviders{
		Trajectory: NewEvolutionTrajectoryProvider(evolutionTracer, feedbackStore),
		Feedback:   NewEvolutionFeedbackSink(feedbackStore),
		Spans:      NewObservabilitySpansProvider(globalTracer),
	}
}

// IntrospectOptions converts the providers into introspect.ControlServerOption
// values. The returned slice is always length 2 (evolution + observability);
// nil providers are ignored inside the server (endpoint disabled).
func (p *ObservabilityProviders) IntrospectOptions() []introspect.ControlServerOption {
	return []introspect.ControlServerOption{
		introspect.WithEvolution(p.Trajectory, p.Feedback),
		introspect.WithObservability(p.Spans),
	}
}
