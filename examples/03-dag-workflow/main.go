// DAG workflow — demonstrates graph-based workflow with linear and conditional execution,
// plus the unified Runner API: conditional branching, controlled loops, subgraph nesting.
//
// Run:
//
//	go run examples/03-dag-workflow/main.go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/workflow"
)

func main() {
	ctx := context.Background()

	// ── Unified Runner API ──
	condEdgeDemo(ctx)
	linearDemo(ctx)
	branchManyDemo(ctx)
	loopDemo(ctx)

	fmt.Println("\n✅ All DAG workflow demos completed")
}

// condEdgeDemo demonstrates conditional branching with ConditionExpr + WithConditionEvaluator.
// ingest → gate → (pass if score >= 70) / (fail otherwise)
func condEdgeDemo(ctx context.Context) {
	fmt.Println("\n═══ Conditional Edge (unified Runner) ═══")

	spec := workflow.NewWorkflow("wf-cond").
		AddNode(workflow.NodeSpec{ID: "ingest", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "gate", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "pass", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "fail", AgentType: "echo"}).
		// DataDependency: gate needs ingest's output
		AddEdge(workflow.EdgeSpec{From: "ingest", To: "gate", Kind: workflow.EdgeDataDependency}).
		// ControlFlow with condition: gate → pass if condition matches
		AddEdge(workflow.EdgeSpec{From: "gate", To: "pass",
			Kind: workflow.EdgeControlFlow,
			Cond: &workflow.ConditionExpr{Type: "state", Value: "score"},
		}).
		// ControlFlow unconditional fallback: gate → fail
		AddEdge(workflow.EdgeSpec{From: "gate", To: "fail",
			Kind: workflow.EdgeControlFlow}).
		WithEntry("ingest")

	// Ingest sets a score in the state.
	// Gate reads it; the condition evaluator decides which branch fires.
	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"ingest": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			return map[string]any{"score": 85}, nil
		},
		"gate": echoFn("gated"),
		"pass": echoFn("passed"),
		"fail": echoFn("failed"),
	}

	// Condition evaluator reads state["score"] and checks threshold.
	condEval := func(expr *workflow.ConditionExpr, view workflow.StateView) bool {
		if expr.Type == "state" {
			val, ok := view.Get(expr.Value)
			if !ok {
				return false
			}
			score, ok := val.(int)
			return ok && score >= 70
		}
		return false
	}

	// RunWorkflow accepts WithConditionEvaluator via variadic opts.
	result, err := workflow.RunWorkflow(ctx, spec, fns,
		workflow.WithConditionEvaluator(condEval))
	if err != nil {
		log.Fatalf("cond edge demo failed: %v", err)
	}

	for _, ns := range result.NodeStates {
		status := "✅"
		if ns.Status == workflow.NodeStatusUnreachable {
			status = "⛔"
		}
		fmt.Printf("  %s %s (%v)\n", status, ns.ID, ns.Status)
	}
	fmt.Printf("  → Conditional: gate→pass (score=85 >= 70)\n")
}

// linearDemo demonstrates workflow with linear DataDependency chain.
func linearDemo(ctx context.Context) {
	fmt.Println("\n═══ Linear DAG (unified Runner) ═══")

	spec := workflow.NewWorkflow("wf-linear").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "b", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "c", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "b", Kind: workflow.EdgeDataDependency}).
		AddEdge(workflow.EdgeSpec{From: "b", To: "c", Kind: workflow.EdgeDataDependency}).
		WithEntry("a")

	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"a": echoFn("done a"),
		"b": echoFn("done b"),
		"c": echoFn("done c"),
	}

	result, err := workflow.RunWorkflow(ctx, spec, fns)
	if err != nil {
		log.Fatalf("linear DAG demo failed: %v", err)
	}

	for _, ns := range result.NodeStates {
		status := "✅"
		if ns.Status == workflow.NodeStatusUnreachable {
			status = "⛔"
		}
		fmt.Printf("  %s %s (%v)\n", status, ns.ID, ns.Status)
	}
	fmt.Printf("  → Linear DAG: a→b→c, all %d nodes completed\n", len(result.NodeStates))
}

// branchManyDemo demonstrates BranchMany — both outgoing edges are activated.
func branchManyDemo(ctx context.Context) {
	fmt.Println("\n═══ BranchMany (unified Runner) ═══")

	spec := workflow.NewWorkflow("wf-branch").
		AddNode(workflow.NodeSpec{ID: "classify", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "path_a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "path_b", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "classify", To: "path_a",
			Kind: workflow.EdgeControlFlow, Branch: workflow.BranchMany}).
		AddEdge(workflow.EdgeSpec{From: "classify", To: "path_b",
			Kind: workflow.EdgeControlFlow, Branch: workflow.BranchMany}).
		WithEntry("classify")

	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"classify": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			return map[string]any{"decision": "both"}, nil
		},
		"path_a": echoFn("went to A"),
		"path_b": echoFn("went to B"),
	}

	result, err := workflow.RunWorkflow(ctx, spec, fns)
	if err != nil {
		log.Fatalf("branch demo failed: %v", err)
	}

	for _, ns := range result.NodeStates {
		status := "✅"
		if ns.Status == workflow.NodeStatusUnreachable {
			status = "⛔"
		}
		fmt.Printf("  %s %s (%v)\n", status, ns.ID, ns.Status)
	}
	fmt.Printf("  → BranchMany: classify completed with decision=both\n")
}

// loopDemo demonstrates controlled loops with LoopSpec.
func loopDemo(ctx context.Context) {
	fmt.Println("\n═══ Controlled Loop (unified Runner) ═══")

	spec := workflow.NewWorkflow("wf-loop").
		AddNode(workflow.NodeSpec{ID: "process", AgentType: "echo"}).
		WithEntry("process").
		WithLoop(&workflow.LoopSpec{
			MaxIterations: 3,
			LoopNodes:     []workflow.NodeID{"process"},
		})

	iteration := 0
	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"process": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			iteration++
			return map[string]any{"iteration": iteration}, nil
		},
	}

	result, err := workflow.RunWorkflow(ctx, spec, fns)
	if err != nil {
		log.Fatalf("loop demo failed: %v", err)
	}

	fmt.Printf("  Total iterations: %d (expected 3)\n", iteration)
	for _, ns := range result.NodeStates {
		fmt.Printf("  [%s] status=%v\n", ns.ID, ns.Status)
	}
}

// echoFn returns an ExecutableFunc that records a message and returns it.
func echoFn(msg string) workflow.ExecutableFunc {
	return func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
		return map[string]any{"output": msg}, nil
	}
}
