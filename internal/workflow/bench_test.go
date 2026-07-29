// Package workflow_test benchmarks the unified Runner.

package workflow_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/workflow"
	wfengine "github.com/Timwood0x10/ares/internal/workflow/engine"
)

// ── Benchmark helpers ─────────────────────────────────────────────────

// benchLinearWorkflow creates a linear chain of N steps: step0 → step1 → ... → stepN-1.
func benchLinearWorkflow(n int) *wfengine.Workflow {
	steps := make([]*wfengine.Step, n)
	for i := 0; i < n; i++ {
		deps := []string{}
		if i > 0 {
			deps = []string{fmt.Sprintf("step%d", i-1)}
		}
		steps[i] = &wfengine.Step{
			ID:        fmt.Sprintf("step%d", i),
			Name:      fmt.Sprintf("Step %d", i),
			AgentType: "bench-agent",
			Input:     fmt.Sprintf("input-%d", i),
			DependsOn: deps,
		}
	}
	return &wfengine.Workflow{
		ID:    "bench-linear",
		Name:  "Benchmark Linear",
		Steps: steps,
	}
}

// benchRunnerWorkflow runs the same topology through the new Runner.
func benchRunnerWorkflow(ctx context.Context, spec *workflow.WorkflowSpec, b *testing.B) {
	fns := make(map[workflow.NodeID]workflow.ExecutableFunc)
	for _, n := range spec.Nodes {
		nid := n.ID
		fns[nid] = func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			return map[string]any{"output": "result"}, nil
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := workflow.RunWorkflow(ctx, spec, fns)
		if err != nil {
			b.Fatalf("Runner: %v", err)
		}
	}
}

// ── Benchmarks: linear chain, 3 nodes ────────────────────────────────

func BenchmarkRunner_Linear3(b *testing.B) {
	wf := benchLinearWorkflow(3)
	spec, err := workflow.CompileFromEngine(wf)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	benchRunnerWorkflow(context.Background(), spec, b)
}

// ── Benchmarks: linear chain, 10 nodes ───────────────────────────────

func BenchmarkRunner_Linear10(b *testing.B) {
	wf := benchLinearWorkflow(10)
	spec, err := workflow.CompileFromEngine(wf)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	fns := make(map[workflow.NodeID]workflow.ExecutableFunc)
	for _, n := range spec.Nodes {
		nid := n.ID
		fns[nid] = func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			return map[string]any{"output": "result"}, nil
		}
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := workflow.RunWorkflow(ctx, spec, fns)
		if err != nil {
			b.Fatalf("Runner: %v", err)
		}
	}
}
