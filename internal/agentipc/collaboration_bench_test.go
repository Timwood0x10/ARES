package agentipc

import (
	"context"
	"testing"
	"time"
)

// Benchmarks for the v0.4.0 M1 collaboration patterns (delegation / pipeline /
// orchestration). All handlers echo synchronously so the numbers isolate the
// composition-layer overhead on top of the underlying request/reply primitives.

// BenchmarkCollaborationDelegate measures one Leader→Specialist delegation
// round trip (M1-1).
func BenchmarkCollaborationDelegate(b *testing.B) {
	bus := NewBus()
	if err := bus.Register("specialist", echoHandler); err != nil {
		b.Fatalf("register: %v", err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bus.DelegateToSpecialist(ctx, "leader", "specialist", "t1", "code", "payload", time.Second)
	}
}

// BenchmarkCollaborationPipeline measures a 3-stage A→B→C pipeline run (M1-2).
func BenchmarkCollaborationPipeline(b *testing.B) {
	bus := NewBus()
	for _, stage := range []string{"a", "b", "c"} {
		if err := bus.Register(stage, echoHandler); err != nil {
			b.Fatalf("register %s: %v", stage, err)
		}
	}
	p, err := NewPipeline(bus, []string{"a", "b", "c"}, time.Second)
	if err != nil {
		b.Fatalf("NewPipeline: %v", err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Run(ctx, "leader", "input")
	}
}

// BenchmarkCollaborationOrchestrate measures a 3-worker fan-out with result
// aggregation (M1-3).
func BenchmarkCollaborationOrchestrate(b *testing.B) {
	bus := NewBus()
	for _, w := range []string{"w1", "w2", "w3"} {
		if err := bus.Register(w, echoHandler); err != nil {
			b.Fatalf("register %s: %v", w, err)
		}
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bus.Orchestrate(ctx, "coord", []string{"w1", "w2", "w3"}, "t1", "work", time.Second)
	}
}
