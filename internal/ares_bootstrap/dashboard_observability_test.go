package ares_bootstrap

import (
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/dashboard"
)

// TestEvolutionTrajectoryProvider verifies the tracer adapter renders recorded
// generations as JSON-friendly values, and nil input yields nil (endpoint
// disabled).
func TestEvolutionTrajectoryProvider(t *testing.T) {
	if got := NewEvolutionTrajectoryProvider(nil); got != nil {
		t.Fatalf("nil tracer must yield nil provider, got %v", got)
	}

	tracer := aresrecovery.NewEvolutionTracer()
	tracer.Record(1, 0.6, []string{"s1"}, []aresrecovery.GenerationChange{
		{StrategyID: "s1", Description: "temp 0.7→0.4", Impact: 0.2},
	})
	provider := NewEvolutionTrajectoryProvider(tracer)
	if provider == nil {
		t.Fatal("non-nil tracer must yield a provider")
	}
	traj := provider.EvolutionTrajectory()
	if len(traj) != 1 {
		t.Fatalf("want 1 generation, got %d", len(traj))
	}
	gen := traj[0]
	if gen["generation"] != 1 || gen["best_score"] != 0.6 {
		t.Fatalf("unexpected generation view %v", gen)
	}
	if gen["breakthrough"] != false || gen["regression"] != false {
		t.Fatalf("unexpected flags %v", gen)
	}
}

// TestEvolutionFeedbackSink verifies the feedback adapter records dashboard
// submissions into the aresrecovery store.
func TestEvolutionFeedbackSink(t *testing.T) {
	if got := NewEvolutionFeedbackSink(nil); got != nil {
		t.Fatalf("nil store must yield nil sink, got %v", got)
	}

	store := aresrecovery.NewFeedbackStore()
	sink := NewEvolutionFeedbackSink(store)
	if sink == nil {
		t.Fatal("non-nil store must yield a sink")
	}
	if err := sink.SubmitFeedback(dashboard.EvolutionFeedback{
		CandidateID: "c1", Rating: 4, Approved: true, Reason: "good", Comments: "nice",
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	fb := store.ForCandidate("c1")
	if fb == nil {
		t.Fatal("feedback must be recorded")
	}
	if fb.Rating != 4 || !fb.Approved || fb.Reason != "good" || fb.Comments != "nice" {
		t.Fatalf("unexpected feedback %+v", fb)
	}
}

// TestObservabilitySpansProvider verifies the GlobalTracer adapter renders
// spans as JSON-friendly values, and nil input yields nil (endpoint disabled).
func TestObservabilitySpansProvider(t *testing.T) {
	if got := NewObservabilitySpansProvider(nil); got != nil {
		t.Fatalf("nil tracer must yield nil provider, got %v", got)
	}

	tracer := aresrecovery.NewGlobalTracer().WithClock(func() time.Time {
		return time.Unix(1700000000, 0)
	})
	tracer.TraceTask("t1", "created", nil)
	tracer.TraceTask("t1", "acquired", map[string]any{"agent": "a1"})
	tracer.Close("t1", "completed")

	provider := NewObservabilitySpansProvider(tracer)
	if provider == nil {
		t.Fatal("non-nil tracer must yield a provider")
	}
	spans := provider.Spans()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	span := spans[0]
	if span["kind"] != "task" || span["id"] != "t1" || span["status"] != "completed" {
		t.Fatalf("unexpected span %v", span)
	}
	events, ok := span["events"].([]map[string]any)
	if !ok || len(events) != 2 {
		t.Fatalf("want 2 events, got %v", span["events"])
	}
	if events[1]["name"] != "acquired" {
		t.Fatalf("unexpected event %v", events[1])
	}
}

// TestAdaptersSatisfyContracts verifies the adapters implement the dashboard
// provider/sink contracts at compile time.
func TestAdaptersSatisfyContracts(t *testing.T) {
	var (
		_ dashboard.EvolutionTrajectoryProvider = (*evolutionTrajectoryAdapter)(nil)
		_ dashboard.EvolutionFeedbackSink       = (*evolutionFeedbackAdapter)(nil)
		_ dashboard.ObservabilitySpansProvider  = (*globalTracerAdapter)(nil)
	)
}
