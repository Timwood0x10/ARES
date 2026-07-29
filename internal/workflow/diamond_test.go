// Package workflow_test — diamond fan-in conformance test for Runner.
//
// Verifies that A/B → C (JoinAll) correctly executes C when both A and B
// complete. This is the specific scenario the DAG_UNIFIED_PIPELINE_COMPLETION_REVIEW
// identified as failing (C executed 0 times).

package workflow_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Timwood0x10/ares/internal/workflow"
)

func TestRunner_DiamondFanIn_JoinAll(t *testing.T) {
	// Topology: A (entry), B (entry) → C (depends on A and B via DataDependency)
	// Expected: all three nodes execute exactly 1 time.
	spec := workflow.NewWorkflow("diamond-ci").
		AddNode(workflow.NodeSpec{ID: "A", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "B", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "C", AgentType: "echo", Join: workflow.JoinAll}).
		AddEdge(workflow.EdgeSpec{From: "A", To: "C", Kind: workflow.EdgeDataDependency}).
		AddEdge(workflow.EdgeSpec{From: "B", To: "C", Kind: workflow.EdgeDataDependency}).
		WithEntry("A", "B")

	var execA, execB, execC int32

	_, err := workflow.RunWorkflow(context.Background(), spec, map[workflow.NodeID]workflow.ExecutableFunc{
		"A": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			atomic.AddInt32(&execA, 1)
			return map[string]any{"done": "A"}, nil
		},
		"B": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			atomic.AddInt32(&execB, 1)
			return map[string]any{"done": "B"}, nil
		},
		"C": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			atomic.AddInt32(&execC, 1)
			return map[string]any{"done": "C"}, nil
		},
	})

	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}

	// Verify each node executed exactly once.
	if n := atomic.LoadInt32(&execA); n != 1 {
		t.Errorf("A executed %d times, want 1", n)
	}
	if n := atomic.LoadInt32(&execB); n != 1 {
		t.Errorf("B executed %d times, want 1", n)
	}
	if n := atomic.LoadInt32(&execC); n != 1 {
		t.Errorf("C executed %d times, want 1 — diamond fan-in broken", n)
	}

	t.Logf("Diamond fan-in: A=%d B=%d C=%d ✓", execA, execB, execC)
}
