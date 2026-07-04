package planner

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// BenchmarkPlanner_FullPipeline measures the end-to-end planning time.
func BenchmarkPlanner_FullPipeline(b *testing.B) {
	planner := newBenchPlanner()
	ctx := context.Background()
	requests := []string{
		"计算1+1",
		"从1累加到100",
		"extract text from this pdf",
		"compute sha256 hash of hello",
		"search the web for golang",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := requests[i%len(requests)]
		_, err := planner.Plan(ctx, req)
		if err != nil {
			b.Fatalf("Plan(%q): %v", req, err)
		}
	}
}

// BenchmarkPlanner_Parallel measures concurrent planning performance.
func BenchmarkPlanner_Parallel(b *testing.B) {
	planner := newBenchPlanner()
	ctx := context.Background()
	requests := []string{
		"计算1+1",
		"从1累加到100",
		"extract text from this pdf",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := requests[b.N%len(requests)]
			_, err := planner.Plan(ctx, req)
			if err != nil {
				b.Fatalf("Plan(%q): %v", req, err)
			}
		}
	})
}

// BenchmarkPlanner_UnknownRequest measures the error path performance.
func BenchmarkPlanner_UnknownRequest(b *testing.B) {
	planner := newBenchPlanner()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := planner.Plan(ctx, "xyznonexistent999")
		if err == nil {
			b.Fatal("expected error")
		}
	}
}

// newBenchPlanner creates a planner with all real components for benchmarking.
func newBenchPlanner() *Planner {
	store := NewMemoryEvidenceStore()
	resolver, _ := NewToolResolver(&mockToolProvider{})
	p, _ := NewPlanner(
		NewRuleBasedAnalyzer(),
		NewCapabilityPlanner(),
		resolver,
		NewEvidenceScorer(store),
		NewExecutionPlanner(),
		store,
	)
	return p
}

// BenchmarkBridge_DirectExecution measures direct tool execution overhead.
func BenchmarkBridge_DirectExecution(b *testing.B) {
	reg := benchRegistry()
	planner := newBenchPlanner()
	bridge, _ := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := bridge.Execute(ctx, "calculator", map[string]interface{}{
			"expression": "1+1",
		}, "")
		if err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}

// BenchmarkBridge_PlannerFallback measures planner fallback overhead.
func BenchmarkBridge_PlannerFallback(b *testing.B) {
	reg := benchRegistry()
	planner := newBenchPlanner()
	bridge, _ := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := bridge.Execute(ctx, "", nil, "计算1+1")
		if err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}

// benchRegistry creates a tool registry with a calculator for benchmarking.
func benchRegistry() *core.Registry {
	reg := core.NewRegistry()
	reg.Register(&realCalculator{})
	return reg
}
