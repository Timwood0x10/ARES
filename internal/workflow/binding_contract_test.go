package workflow_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Timwood0x10/ares/internal/workflow"
)

func TestRunner_Contract_BoundConditionUsesConfiguredBinding(t *testing.T) {
	t.Parallel()

	spec := workflow.NewWorkflow("bound-condition").
		AddNode(workflow.NodeSpec{ID: "source"}).
		AddNode(workflow.NodeSpec{ID: "target"}).
		AddEdge(workflow.EdgeSpec{
			From: "source",
			To:   "target",
			Kind: workflow.EdgeControlFlow,
			Cond: &workflow.ConditionExpr{Type: "bound", Value: "target"},
		}).
		WithEntry("source")

	var conditionCalls atomic.Int32
	var targetCalls atomic.Int32
	result, err := workflow.RunWorkflow(
		context.Background(),
		spec,
		map[workflow.NodeID]workflow.ExecutableFunc{
			"source": func(context.Context, workflow.StateView) (map[string]any, error) {
				return map[string]any{"allow": false}, nil
			},
			"target": func(context.Context, workflow.StateView) (map[string]any, error) {
				targetCalls.Add(1)
				return nil, nil
			},
		},
		workflow.WithBindings(map[workflow.NodeID]func(map[string]any) bool{
			"target": func(state map[string]any) bool {
				conditionCalls.Add(1)
				allow, _ := state["allow"].(bool)
				return allow
			},
		}),
	)
	if err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	if conditionCalls.Load() != 1 {
		t.Fatalf("condition calls = %d, want 1", conditionCalls.Load())
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("target calls = %d, want 0", targetCalls.Load())
	}
	if got := buildStatusMap(result)["target"]; got != workflow.NodeStatusNotSelected {
		t.Fatalf("target status = %q, want %q", got, workflow.NodeStatusNotSelected)
	}
}

func TestRunner_Contract_UntilConditionStopsLoop(t *testing.T) {
	t.Parallel()

	spec := workflow.NewWorkflow("until-condition").
		AddNode(workflow.NodeSpec{ID: "body"}).
		WithEntry("body").
		WithLoop(&workflow.LoopSpec{
			MaxIterations: 5,
			LoopNodes:     []workflow.NodeID{"body"},
		})

	var bodyCalls atomic.Int32
	var untilCalls atomic.Int32
	result, err := workflow.RunWorkflow(
		context.Background(),
		spec,
		map[workflow.NodeID]workflow.ExecutableFunc{
			"body": func(context.Context, workflow.StateView) (map[string]any, error) {
				count := bodyCalls.Add(1)
				return map[string]any{"count": count}, nil
			},
		},
		workflow.WithUntilCondition(func(state map[string]any, iteration int) bool {
			untilCalls.Add(1)
			count, _ := state["count"].(int32)
			return iteration == 2 && count == 2
		}),
	)
	if err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	if bodyCalls.Load() != 2 {
		t.Fatalf("body calls = %d, want 2", bodyCalls.Load())
	}
	if untilCalls.Load() != 2 {
		t.Fatalf("until calls = %d, want 2", untilCalls.Load())
	}
	if result.SpecID != spec.ID {
		t.Fatalf("result spec ID = %q, want %q", result.SpecID, spec.ID)
	}
	if result.ExecutionID == "" {
		t.Fatal("result execution ID is empty")
	}
	if len(result.LoopHistory) != 2 {
		t.Fatalf("loop history length = %d, want 2", len(result.LoopHistory))
	}
	for i, iteration := range result.LoopHistory {
		if iteration.Iteration != i+1 {
			t.Fatalf("loop history[%d].iteration = %d, want %d", i, iteration.Iteration, i+1)
		}
	}
}
