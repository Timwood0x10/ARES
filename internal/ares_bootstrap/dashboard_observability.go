// Package ares_bootstrap — dashboard observability adapters (v0.4.0 M3/M4).
//
// Bridges aresrecovery's recording surfaces (EvolutionTracer / FeedbackStore
// / GlobalTracer) to the dashboard's provider contracts so the existing
// /evolution/trajectory, /evolution/feedback and /observability/spans
// endpoints are backed by real components instead of returning empty lists.
package ares_bootstrap

import (
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/dashboard"
)

// evolutionTrajectoryAdapter adapts *aresrecovery.EvolutionTracer to
// dashboard.EvolutionTrajectoryProvider. TrajectoryViews already produces
// JSON-friendly generation maps, so the adapter is a thin pass-through.
type evolutionTrajectoryAdapter struct {
	tracer *aresrecovery.EvolutionTracer
}

// NewEvolutionTrajectoryProvider wraps a tracer as the dashboard trajectory
// provider. Returns nil when the tracer is nil (endpoint disabled).
func NewEvolutionTrajectoryProvider(tracer *aresrecovery.EvolutionTracer) dashboard.EvolutionTrajectoryProvider {
	if tracer == nil {
		return nil
	}
	return &evolutionTrajectoryAdapter{tracer: tracer}
}

var _ dashboard.EvolutionTrajectoryProvider = (*evolutionTrajectoryAdapter)(nil)

// EvolutionTrajectory returns the recorded generations as JSON-friendly
// values (oldest first), or nil when nothing is recorded.
func (a *evolutionTrajectoryAdapter) EvolutionTrajectory() []map[string]any {
	return a.tracer.TrajectoryViews()
}

// evolutionFeedbackAdapter adapts *aresrecovery.FeedbackStore to
// dashboard.EvolutionFeedbackSink. The dashboard's EvolutionFeedback payload
// maps directly onto aresrecovery.HumanFeedback.
type evolutionFeedbackAdapter struct {
	store *aresrecovery.FeedbackStore
}

// NewEvolutionFeedbackSink wraps a feedback store as the dashboard feedback
// sink. Returns nil when the store is nil (endpoint disabled).
func NewEvolutionFeedbackSink(store *aresrecovery.FeedbackStore) dashboard.EvolutionFeedbackSink {
	if store == nil {
		return nil
	}
	return &evolutionFeedbackAdapter{store: store}
}

var _ dashboard.EvolutionFeedbackSink = (*evolutionFeedbackAdapter)(nil)

// SubmitFeedback records one human feedback entry.
func (a *evolutionFeedbackAdapter) SubmitFeedback(fb dashboard.EvolutionFeedback) error {
	a.store.Add(aresrecovery.HumanFeedback{
		CandidateID: fb.CandidateID,
		Rating:      fb.Rating,
		Comments:    fb.Comments,
		Approved:    fb.Approved,
		Reason:      fb.Reason,
	})
	return nil
}

// globalTracerAdapter adapts *aresrecovery.GlobalTracer to
// dashboard.ObservabilitySpansProvider.
type globalTracerAdapter struct {
	tracer *aresrecovery.GlobalTracer
}

// NewObservabilitySpansProvider wraps a global tracer as the dashboard
// observability provider. Returns nil when the tracer is nil (endpoint
// disabled).
func NewObservabilitySpansProvider(tracer *aresrecovery.GlobalTracer) dashboard.ObservabilitySpansProvider {
	if tracer == nil {
		return nil
	}
	return &globalTracerAdapter{tracer: tracer}
}

var _ dashboard.ObservabilitySpansProvider = (*globalTracerAdapter)(nil)

// Spans returns a snapshot of the recorded spans (insertion order) as
// JSON-friendly values, or nil when nothing is recorded.
func (a *globalTracerAdapter) Spans() []map[string]any {
	spans := a.tracer.Spans()
	if len(spans) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		events := make([]map[string]any, 0, len(s.Events))
		for _, e := range s.Events {
			events = append(events, map[string]any{
				"at":     e.At,
				"name":   e.Name,
				"detail": e.Detail,
			})
		}
		out = append(out, map[string]any{
			"kind":       string(s.Kind),
			"id":         s.ID,
			"started_at": s.StartedAt,
			"ended_at":   s.EndedAt,
			"status":     s.Status,
			"parent_id":  s.ParentID,
			"events":     events,
		})
	}
	return out
}
