// DAG workflow — demonstrates graph-based workflow with linear and conditional
// execution, plus the unified Runner API: conditional branching, controlled
// loops, and fan-out branching.
//
// Purpose:
//
//	Show four increasingly complex workflow patterns built with the
//	internal/workflow package: conditional edges, linear data-dependency
//	chains, BranchMany fan-out, and controlled loops.
//
// Learning objectives (what this example teaches you):
//   - How to define a WorkflowSpec using the builder pattern (NewWorkflow,
//     AddNode, AddEdge, WithEntry).
//   - The difference between EdgeDataDependency and EdgeControlFlow edges.
//   - How to use BranchOne + Group for exclusive-or branching and BranchMany
//     for fan-out.
//   - How to supply a condition evaluator via WithConditionEvaluator.
//   - How to define a LoopSpec to repeat a set of nodes up to MaxIterations.
//   - How to provide per-node ExecutableFunc implementations and call
//     RunWorkflow to get a Result.
//
// Core APIs used (with package paths):
//   - workflow.NewWorkflow           — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.NodeSpec              — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.EdgeSpec              — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.EdgeDataDependency    — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.EdgeControlFlow       — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.BranchOne             — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.BranchMany            — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.ConditionExpr         — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.LoopSpec              — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.ExecutableFunc        — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.StateView             — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.RunWorkflow           — github.com/Timwood0x10/ares/internal/workflow
//   - workflow.WithConditionEvaluator — github.com/Timwood0x10/ares/internal/workflow
//
// Run:
//
//	go run examples/03-dag-workflow/main.go
//
// Expected output:
//
//	═══ Conditional Edge (unified Runner) ═══
//	  ✅ ingest (completed)
//	  ✅ gate (completed)
//	  ✅ pass (completed)
//	  ⛔ fail (unreachable)
//	  → Conditional: gate→pass (score=85 >= 70)
//
//	═══ Linear DAG (unified Runner) ═══
//	  ✅ a (completed)
//	  ✅ b (completed)
//	  ✅ c (completed)
//	  → Linear DAG: a→b→c, all 3 nodes completed
//
//	═══ BranchMany (unified Runner) ═══
//	  ✅ classify (completed)
//	  ✅ path_a (completed)
//	  ✅ path_b (completed)
//	  → BranchMany: classify completed with decision=both
//
//	═══ Controlled Loop (unified Runner) ═══
//	  Total iterations: 3 (expected 3)
//	  [process] status=completed
//
//	✅ All DAG workflow demos completed
//
// Try changing the score in condEdgeDemo to below 70 to see the "fail" branch
// fire instead. Adjust MaxIterations in loopDemo to change loop count.
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
	// Each demo builds a different WorkflowSpec and runs it via RunWorkflow.
	condEdgeDemo(ctx)
	linearDemo(ctx)
	branchManyDemo(ctx)
	loopDemo(ctx)

	fmt.Println("\n✅ All DAG workflow demos completed")
}

// condEdgeDemo demonstrates conditional branching with ConditionExpr +
// WithConditionEvaluator.
// Flow: ingest → gate → (pass if score >= 70) / (fail otherwise)
func condEdgeDemo(ctx context.Context) {
	fmt.Println("\n═══ Conditional Edge (unified Runner) ═══")

	// ── Step 1: Build the workflow spec ──
	// NewWorkflow starts a builder chain. AddNode adds nodes; AddEdge connects
	// them. EdgeDataDependency means the target needs the source's output.
	// EdgeControlFlow means the source's result determines whether the edge fires.
	spec := workflow.NewWorkflow("wf-cond").
		AddNode(workflow.NodeSpec{ID: "ingest", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "gate", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "pass", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "fail", AgentType: "echo"}).
		// DataDependency: gate needs ingest's output.
		AddEdge(workflow.EdgeSpec{From: "ingest", To: "gate", Kind: workflow.EdgeDataDependency}).
		// ControlFlow with condition: gate → pass if condition matches.
		AddEdge(workflow.EdgeSpec{From: "gate", To: "pass",
			Kind: workflow.EdgeControlFlow, Branch: workflow.BranchOne, Group: "score",
			Cond: &workflow.ConditionExpr{Type: "state", Value: "score"},
		}).
		// ControlFlow unconditional fallback: gate → fail.
		AddEdge(workflow.EdgeSpec{From: "gate", To: "fail",
			Kind: workflow.EdgeControlFlow, Branch: workflow.BranchOne, Group: "score"}).
		WithEntry("ingest")

	// ── Step 2: Provide per-node executable functions ──
	// Each ExecutableFunc receives a context and a read-only StateView, and
	// returns a map that is merged into the workflow state.
	// Ingest sets a score in the state; gate reads it; the condition evaluator
	// decides which branch fires.
	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"ingest": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			return map[string]any{"score": 85}, nil
		},
		"gate": echoFn("gated"),
		"pass": echoFn("passed"),
		"fail": echoFn("failed"),
	}

	// ── Step 3: Define a custom condition evaluator ──
	// The evaluator receives the ConditionExpr from the edge and the current
	// StateView. It returns true to fire the edge, false to skip it.
	// Here we read state["score"] and check the threshold.
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

	// ── Step 4: Run the workflow ──
	// RunWorkflow accepts WithConditionEvaluator via variadic opts.
	// The returned Result contains NodeStates with per-node status.
	result, err := workflow.RunWorkflow(ctx, spec, fns,
		workflow.WithConditionEvaluator(condEval))
	if err != nil {
		log.Fatalf("cond edge demo failed: %v", err)
	}

	// ── Step 5: Print per-node status ──
	// NodeStatusUnreachable means the node was never activated (a branch not taken).
	for _, ns := range result.NodeStates {
		status := "✅"
		if ns.Status == workflow.NodeStatusUnreachable {
			status = "⛔"
		}
		fmt.Printf("  %s %s (%v)\n", status, ns.ID, ns.Status)
	}
	fmt.Printf("  → Conditional: gate→pass (score=85 >= 70)\n")
}

// linearDemo demonstrates a workflow with a linear data-dependency chain.
// Flow: a → b → c
func linearDemo(ctx context.Context) {
	fmt.Println("\n═══ Linear DAG (unified Runner) ═══")

	// ── Step 1: Build a three-node linear chain ──
	// Each EdgeDataDependency means the target waits for the source to complete.
	spec := workflow.NewWorkflow("wf-linear").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "b", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "c", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "b", Kind: workflow.EdgeDataDependency}).
		AddEdge(workflow.EdgeSpec{From: "b", To: "c", Kind: workflow.EdgeDataDependency}).
		WithEntry("a")

	// ── Step 2: Provide executable functions for each node ──
	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"a": echoFn("done a"),
		"b": echoFn("done b"),
		"c": echoFn("done c"),
	}

	// ── Step 3: Run and print results ──
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

// branchManyDemo demonstrates BranchMany — both outgoing control-flow edges
// are activated simultaneously.
// Flow: classify → path_a + path_b
func branchManyDemo(ctx context.Context) {
	fmt.Println("\n═══ BranchMany (unified Runner) ═══")

	// ── Step 1: Build a fan-out spec ──
	// BranchMany (the default) means all outgoing control-flow edges with
	// satisfied conditions are activated. Here both edges fire.
	spec := workflow.NewWorkflow("wf-branch").
		AddNode(workflow.NodeSpec{ID: "classify", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "path_a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "path_b", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "classify", To: "path_a",
			Kind: workflow.EdgeControlFlow, Branch: workflow.BranchMany}).
		AddEdge(workflow.EdgeSpec{From: "classify", To: "path_b",
			Kind: workflow.EdgeControlFlow, Branch: workflow.BranchMany}).
		WithEntry("classify")

	// ── Step 2: Provide executable functions ──
	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"classify": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			return map[string]any{"decision": "both"}, nil
		},
		"path_a": echoFn("went to A"),
		"path_b": echoFn("went to B"),
	}

	// ── Step 3: Run and print results ──
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

// loopDemo demonstrates a controlled loop with LoopSpec.
// The "process" node repeats up to MaxIterations times.
func loopDemo(ctx context.Context) {
	fmt.Println("\n═══ Controlled Loop (unified Runner) ═══")

	// ── Step 1: Build a single-node workflow with a loop spec ──
	// WithLoop attaches a LoopSpec that tells the Runner to repeat the listed
	// nodes up to MaxIterations times.
	spec := workflow.NewWorkflow("wf-loop").
		AddNode(workflow.NodeSpec{ID: "process", AgentType: "echo"}).
		WithEntry("process").
		WithLoop(&workflow.LoopSpec{
			MaxIterations: 3,
			LoopNodes:     []workflow.NodeID{"process"},
		})

	// ── Step 2: Provide an executable that increments a counter ──
	// The closure captures the iteration variable to track how many times
	// the node actually executed.
	iteration := 0
	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"process": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			iteration++
			return map[string]any{"iteration": iteration}, nil
		},
	}

	// ── Step 3: Run and verify the loop count ──
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
