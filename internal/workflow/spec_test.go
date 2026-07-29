// Package workflow_test — tests for WorkflowSpec IR, Compiler, and Validator.
//
// Phase: P1 — IR definition + Compiler + Validator.
// These tests validate the IR structural integrity, compiler correctness,
// and validator coverage. They do NOT execute any workflow.

package workflow_test

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/workflow"
	wfengine "github.com/Timwood0x10/ares/internal/workflow/engine"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

func echoNode(id string, recorder *[]string) *wfgraph.FuncNode {
	node, err := wfgraph.NewFuncNode(id, func(_ context.Context, state *wfgraph.State) error {
		if recorder != nil {
			*recorder = append(*recorder, id)
		}
		state.Set("node."+id, id+"_done")
		return nil
	})
	if err != nil {
		panic(err)
	}
	return node
}

// ──────────────────────────────────────────────────────────────────────
// IR spec tests
// ──────────────────────────────────────────────────────────────────────

func TestIR_NewWorkflow(t *testing.T) {
	spec := workflow.NewWorkflow("test-wf")
	if spec.ID != "test-wf" {
		t.Errorf("expected ID 'test-wf', got %q", spec.ID)
	}
	if spec.Schedule.MaxParallel != 1 {
		t.Errorf("expected MaxParallel default 1, got %d", spec.Schedule.MaxParallel)
	}
	if len(spec.Nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(spec.Nodes))
	}
}

func TestIR_BuilderChaining(t *testing.T) {
	spec := workflow.NewWorkflow("wf").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "b", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "b", Kind: workflow.EdgeDataDependency}).
		WithEntry("a").
		WithMaxParallel(5)

	if len(spec.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(spec.Nodes))
	}
	if len(spec.Edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(spec.Edges))
	}
	if len(spec.Entries) != 1 || spec.Entries[0] != "a" {
		t.Errorf("expected entry 'a', got %v", spec.Entries)
	}
	if spec.Schedule.MaxParallel != 5 {
		t.Errorf("expected MaxParallel 5, got %d", spec.Schedule.MaxParallel)
	}
}

func TestIR_WithLoop(t *testing.T) {
	spec := workflow.NewWorkflow("loop-wf").
		AddNode(workflow.NodeSpec{ID: "process", AgentType: "echo"}).
		WithLoop(&workflow.LoopSpec{MaxIterations: 3, LoopNodes: []workflow.NodeID{"process"}})

	if spec.Loop == nil {
		t.Fatal("expected Loop to be non-nil")
	}
	if spec.Loop.MaxIterations != 3 {
		t.Errorf("expected MaxIterations 3, got %d", spec.Loop.MaxIterations)
	}
	if len(spec.Loop.LoopNodes) != 1 || spec.Loop.LoopNodes[0] != "process" {
		t.Errorf("expected LoopNodes ['process'], got %v", spec.Loop.LoopNodes)
	}
}

func TestIR_NodeSpecDefaults(t *testing.T) {
	n := workflow.NodeSpec{ID: "n1", AgentType: "echo"}
	if n.Join != "" {
		t.Errorf("expected empty Join (default JoinAll), got %q", n.Join)
	}
	if n.Timeout != 0 {
		t.Errorf("expected zero Timeout, got %v", n.Timeout)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Compiler tests: engine.Workflow → IR
// ──────────────────────────────────────────────────────────────────────

func TestCompiler_Engine_NilWorkflow(t *testing.T) {
	_, err := workflow.CompileFromEngine(nil)
	if err == nil {
		t.Fatal("expected error for nil workflow")
	}
}

func TestCompiler_Engine_EmptyWorkflow(t *testing.T) {
	w := &wfengine.Workflow{ID: "empty"}
	spec, err := workflow.CompileFromEngine(w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.ID != "empty" {
		t.Errorf("expected ID 'empty', got %q", spec.ID)
	}
	if len(spec.Nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(spec.Nodes))
	}
}

func TestCompiler_Engine_SimpleLinear(t *testing.T) {
	w := &wfengine.Workflow{
		ID:   "linear",
		Name: "Linear Workflow",
		Steps: []*wfengine.Step{
			{ID: "a", Name: "Step A", AgentType: "echo"},
			{ID: "b", Name: "Step B", AgentType: "echo", DependsOn: []string{"a"}},
			{ID: "c", Name: "Step C", AgentType: "echo", DependsOn: []string{"b"}},
		},
	}

	spec, err := workflow.CompileFromEngine(w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(spec.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(spec.Nodes))
	}
	if len(spec.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(spec.Edges))
	}

	// Verify edges: a→b, b→c
	assertEdge(t, spec.Edges, "a", "b", workflow.EdgeDataDependency)
	assertEdge(t, spec.Edges, "b", "c", workflow.EdgeDataDependency)

	// Verify entries: a has in-degree 0
	if len(spec.Entries) != 1 || spec.Entries[0] != "a" {
		t.Errorf("expected single entry 'a', got %v", spec.Entries)
	}
}

func TestCompiler_Engine_WithRetryAndInterrupt(t *testing.T) {
	w := &wfengine.Workflow{
		ID: "rich",
		Steps: []*wfengine.Step{
			{
				ID: "collect", AgentType: "api",
				RetryPolicy: &wfengine.RetryPolicy{
					MaxAttempts:       3,
					InitialDelay:      1000000000,  // 1s
					MaxDelay:          30000000000, // 30s
					BackoffMultiplier: 2.0,
				},
			},
			{
				ID: "review", AgentType: "human", DependsOn: []string{"collect"},
				Interrupt: &wfengine.InterruptConfig{
					Message: "Approve this?",
					Payload: map[string]any{"reason": "compliance"},
				},
			},
		},
	}

	spec, err := workflow.CompileFromEngine(w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify retry
	if spec.Nodes[0].Retry == nil {
		t.Fatal("expected Retry on node 'collect'")
	}
	if spec.Nodes[0].Retry.MaxAttempts != 3 {
		t.Errorf("expected MaxAttempts 3, got %d", spec.Nodes[0].Retry.MaxAttempts)
	}

	// Verify interrupt
	if spec.Nodes[1].Interrupt == nil {
		t.Fatal("expected Interrupt on node 'review'")
	}
	if spec.Nodes[1].Interrupt.Message != "Approve this?" {
		t.Errorf("expected 'Approve this?', got %q", spec.Nodes[1].Interrupt.Message)
	}
}

func TestCompiler_Engine_WithLoop(t *testing.T) {
	until := func(map[string]any, int) bool { return false }
	w := &wfengine.Workflow{
		ID: "loop-wf",
		Steps: []*wfengine.Step{
			{ID: "collect", AgentType: "echo"},
			{ID: "process", AgentType: "echo", DependsOn: []string{"collect"}},
		},
		LoopConfig: &wfengine.LoopConfig{
			MaxIterations:  5,
			UntilCondition: until,
			LoopSteps:      []string{"collect", "process"},
		},
	}

	spec, err := workflow.CompileFromEngine(w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if spec.Loop == nil {
		t.Fatal("expected Loop to be non-nil")
	}
	if spec.Loop.MaxIterations != 5 {
		t.Errorf("expected MaxIterations 5, got %d", spec.Loop.MaxIterations)
	}
	if len(spec.Loop.LoopNodes) != 2 {
		t.Errorf("expected 2 loop nodes, got %d", len(spec.Loop.LoopNodes))
	}
}

func TestCompiler_Engine_DuplicateNodeID(t *testing.T) {
	w := &wfengine.Workflow{
		ID: "dup",
		Steps: []*wfengine.Step{
			{ID: "a", AgentType: "echo"},
			{ID: "a", AgentType: "echo"}, // duplicate
		},
	}

	_, err := workflow.CompileFromEngine(w)
	if err == nil {
		t.Fatal("expected error for duplicate node ID")
	}
}

func TestCompiler_Engine_SubWorkflow(t *testing.T) {
	sub := &wfengine.Workflow{
		ID: "sub",
		Steps: []*wfengine.Step{
			{ID: "validate", AgentType: "checker"},
		},
	}
	parent := &wfengine.Workflow{
		ID: "parent",
		Steps: []*wfengine.Step{
			{ID: "receive", AgentType: "echo"},
			{ID: "validate_step", AgentType: "nop", SubWorkflow: sub, DependsOn: []string{"receive"}},
		},
	}

	spec, err := workflow.CompileFromEngine(parent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(spec.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(spec.Nodes))
	}
	if spec.Nodes[1].SubWorkflow == nil {
		t.Fatal("expected SubWorkflow on 'validate_step'")
	}
	if spec.Nodes[1].SubWorkflow.ID != "sub" {
		t.Errorf("expected SubWorkflow ID 'sub', got %q", spec.Nodes[1].SubWorkflow.ID)
	}
}

func TestCompiler_Engine_ConditionAnnotatesNode(t *testing.T) {
	w := &wfengine.Workflow{
		ID: "cond",
		Steps: []*wfengine.Step{
			{ID: "a", AgentType: "echo"},
			{
				ID: "b", AgentType: "echo", DependsOn: []string{"a"},
				Condition: func(vars map[string]any) bool { return false },
			},
		},
	}

	spec, err := workflow.CompileFromEngine(w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(spec.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(spec.Edges))
	}
	if spec.Edges[0].Kind != workflow.EdgeDataDependency {
		t.Errorf("expected structural dependency edge, got %v", spec.Edges[0].Kind)
	}
	if spec.Nodes[1].Condition == nil {
		t.Fatal("expected node condition binding")
	}
	if spec.Nodes[1].Condition.Type != "bound" || spec.Nodes[1].Condition.Value != "b" {
		t.Fatalf("condition = %#v, want bound:b", spec.Nodes[1].Condition)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Compiler tests: graph.Graph → IR
// ──────────────────────────────────────────────────────────────────────

func TestCompiler_Graph_NilGraph(t *testing.T) {
	_, err := workflow.CompileFromGraph(nil)
	if err == nil {
		t.Fatal("expected error for nil graph")
	}
}

func TestCompiler_Graph_SimpleLinear(t *testing.T) {
	g, err := wfgraph.NewGraph("linear")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	_, err = g.Node("a", echoNode("a", nil))
	if err != nil {
		t.Fatalf("Node a: %v", err)
	}
	_, err = g.Node("b", echoNode("b", nil))
	if err != nil {
		t.Fatalf("Node b: %v", err)
	}
	_, err = g.Node("c", echoNode("c", nil))
	if err != nil {
		t.Fatalf("Node c: %v", err)
	}
	_, err = g.Edge("a", "b")
	if err != nil {
		t.Fatalf("Edge a→b: %v", err)
	}
	_, err = g.Edge("b", "c")
	if err != nil {
		t.Fatalf("Edge b→c: %v", err)
	}
	_, err = g.Start("a")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	spec, err := workflow.CompileFromGraph(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(spec.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(spec.Nodes))
	}
	if len(spec.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(spec.Edges))
	}
	assertEdge(t, spec.Edges, "a", "b", workflow.EdgeDataDependency)
	assertEdge(t, spec.Edges, "b", "c", workflow.EdgeDataDependency)
	if len(spec.Entries) != 1 || spec.Entries[0] != "a" {
		t.Errorf("expected entry 'a', got %v", spec.Entries)
	}
}

func TestCompiler_Graph_ConditionalEdge(t *testing.T) {
	g, err := wfgraph.NewGraph("cond-graph")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	_, _ = g.Node("eval", echoNode("eval", nil))
	_, _ = g.Node("pass", echoNode("pass", nil))
	_, _ = g.Node("fail", echoNode("fail", nil))
	_, _ = g.Edge("eval", "pass", func(s *wfgraph.State) bool { return true })
	_, _ = g.Edge("eval", "fail", func(s *wfgraph.State) bool { return false })
	_, _ = g.Start("eval")

	spec, err := workflow.CompileFromGraph(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(spec.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(spec.Nodes))
	}
	if len(spec.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(spec.Edges))
	}

	// Both edges should be ControlFlow (they have conditions)
	for _, e := range spec.Edges {
		if e.Kind != workflow.EdgeControlFlow {
			t.Errorf("expected ControlFlow edge %s→%s with condition", e.From, e.To)
		}
		if e.Cond == nil {
			t.Errorf("expected non-nil Cond on edge %s→%s", e.From, e.To)
		}
	}
}

func TestCompiler_Graph_EmptyGraph(t *testing.T) {
	g, err := wfgraph.NewGraph("empty")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	spec, err := workflow.CompileFromGraph(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.Nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(spec.Nodes))
	}
}

// ──────────────────────────────────────────────────────────────────────
// Validator tests
// ──────────────────────────────────────────────────────────────────────

func TestValidator_EmptySpec(t *testing.T) {
	spec := workflow.NewWorkflow("")
	r := workflow.Validate(spec)
	if r.Valid() {
		t.Error("expected validation errors for empty spec")
	}
	hasEmptyID := false
	for _, e := range r.Errors {
		if e.Field == "id" {
			hasEmptyID = true
			break
		}
	}
	if !hasEmptyID {
		t.Error("expected error about empty workflow ID")
	}
}

func TestValidator_NilSpec(t *testing.T) {
	r := workflow.Validate(nil)
	if r.Valid() {
		t.Error("expected validation errors for nil spec")
	}
}

func TestValidator_ValidLinear(t *testing.T) {
	spec := createLinearSpec("valid", 3)
	r := workflow.Validate(spec)
	if !r.Valid() {
		t.Errorf("expected no errors for valid linear spec, got: %v", r.Errors)
	}
}

func TestValidator_DuplicateNodeIDs(t *testing.T) {
	spec := workflow.NewWorkflow("dup").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"})

	r := workflow.Validate(spec)
	if r.Valid() {
		t.Fatal("expected errors for duplicate nodes")
	}
	found := false
	for _, e := range r.Errors {
		if e.NodeID == "a" && e.Field == "id" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected duplicate ID error for node 'a', got: %v", r.Errors)
	}
}

func TestValidator_DanglingEdge(t *testing.T) {
	spec := workflow.NewWorkflow("dangling").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "nonexistent", Kind: workflow.EdgeDataDependency})

	r := workflow.Validate(spec)
	if r.Valid() {
		t.Fatal("expected errors for dangling edge")
	}
}

func TestValidator_Cycle(t *testing.T) {
	spec := createCycleSpec()
	r := workflow.Validate(spec)
	if r.Valid() {
		t.Fatal("expected errors for cycle")
	}
	hasCycle := false
	for _, e := range r.Errors {
		if e.Field == "edges" && e.Message == "cycle detected in workflow graph" {
			hasCycle = true
			break
		}
	}
	if !hasCycle {
		t.Errorf("expected cycle detection error, got: %v", r.Errors)
	}
}

func TestValidator_BranchOneNoFallback(t *testing.T) {
	spec := workflow.NewWorkflow("branch-one").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "b", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "b", Branch: workflow.BranchOne})

	r := workflow.Validate(spec)
	// Only 1 edge in BranchOne group → warning about redundancy, not error
	if !r.Valid() {
		t.Errorf("expected no errors (redundant BranchOne is a warning), got: %v", r.Errors)
	}
}

func TestValidator_BranchOneMultipleUnconditional(t *testing.T) {
	spec := workflow.NewWorkflow("branch-one-dup").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "b", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "c", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "b", Branch: workflow.BranchOne}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "c", Branch: workflow.BranchOne}).
		AddEdge(workflow.EdgeSpec{
			From: "a", To: "c", Branch: workflow.BranchOne,
			Cond: &workflow.ConditionExpr{Type: "expr", Value: "true"},
		})

	r := workflow.Validate(spec)
	if r.Valid() {
		t.Fatal("expected error for multiple unconditional edges in BranchOne group")
	}
}

func TestValidator_JoinWarning(t *testing.T) {
	spec := workflow.NewWorkflow("join-warn").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "b", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "merge", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "merge", Kind: workflow.EdgeDataDependency}).
		AddEdge(workflow.EdgeSpec{From: "b", To: "merge", Kind: workflow.EdgeDataDependency})

	r := workflow.Validate(spec)
	// merge has 2 incoming data-dependency edges but no explicit Join → warning
	if len(r.Warnings) == 0 {
		t.Error("expected warning about missing Join policy on 'merge'")
	}
}

func TestValidator_LoopInvalidNode(t *testing.T) {
	spec := workflow.NewWorkflow("loop-bad").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		WithLoop(&workflow.LoopSpec{
			MaxIterations: 3,
			LoopNodes:     []workflow.NodeID{"nonexistent"},
		})

	r := workflow.Validate(spec)
	if r.Valid() {
		t.Fatal("expected error for loop referencing non-existent node")
	}
}

func TestValidator_UnreachableNode(t *testing.T) {
	spec := workflow.NewWorkflow("unreachable").
		AddNode(workflow.NodeSpec{ID: "entry", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "orphan", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "entry", To: "orphan", Kind: workflow.EdgeDataDependency}).
		WithEntry("entry").
		AddNode(workflow.NodeSpec{ID: "unreachable", AgentType: "echo"})

	r := workflow.Validate(spec)
	if len(r.Warnings) == 0 {
		t.Error("expected warning about unreachable node")
	}
	hasUnreachable := false
	for _, w := range r.Warnings {
		if w.NodeID == "unreachable" {
			hasUnreachable = true
			break
		}
	}
	if !hasUnreachable {
		t.Errorf("expected unreachable warning for 'unreachable', got warnings: %v", r.Warnings)
	}
}

func TestValidator_ValidateFromEngine(t *testing.T) {
	// Full pipeline: engine → compiler → validator
	w := &wfengine.Workflow{
		ID: "pipeline",
		Steps: []*wfengine.Step{
			{ID: "a", AgentType: "echo"},
			{ID: "b", AgentType: "echo", DependsOn: []string{"a"}},
			{ID: "c", AgentType: "echo", DependsOn: []string{"b"}},
		},
	}

	spec, err := workflow.CompileFromEngine(w)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	r := workflow.Validate(spec)
	if !r.Valid() {
		t.Errorf("expected valid spec from engine, got: %v", r.Errors)
	}
}

func TestValidator_ValidateFromGraph(t *testing.T) {
	g, err := wfgraph.NewGraph("pipeline")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	_, _ = g.Node("a", echoNode("a", nil))
	_, _ = g.Node("b", echoNode("b", nil))
	_, _ = g.Edge("a", "b")
	_, _ = g.Start("a")

	spec, err := workflow.CompileFromGraph(g)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	r := workflow.Validate(spec)
	if !r.Valid() {
		t.Errorf("expected valid spec from graph, got: %v", r.Errors)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Gold file conformance: engine ↔ IR equivalence
// ──────────────────────────────────────────────────────────────────────

// TestCompiler_EngineToGraphEquivalence verifies that compiling the same
// linear workflow topology from both engine and graph produces equivalent
// IR structures.
func TestCompiler_EngineToGraphEquivalence(t *testing.T) {
	// Build engine workflow
	engineWF := &wfengine.Workflow{
		ID:   "equiv",
		Name: "Equivalence Test",
		Steps: []*wfengine.Step{
			{ID: "a", Name: "Step A", AgentType: "echo", Input: "hello"},
			{ID: "b", Name: "Step B", AgentType: "echo", Input: "world", DependsOn: []string{"a"}},
		},
	}

	// Build equivalent graph
	g, err := wfgraph.NewGraph("equiv")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	_, _ = g.Node("a", echoNode("a", nil))
	_, _ = g.Node("b", echoNode("b", nil))
	_, _ = g.Edge("a", "b")
	_, _ = g.Start("a")

	engineSpec, err := workflow.CompileFromEngine(engineWF)
	if err != nil {
		t.Fatalf("compile engine: %v", err)
	}
	graphSpec, err := workflow.CompileFromGraph(g)
	if err != nil {
		t.Fatalf("compile graph: %v", err)
	}

	// Both should have same ID
	if engineSpec.ID != graphSpec.ID {
		t.Errorf("ID mismatch: engine=%q graph=%q", engineSpec.ID, graphSpec.ID)
	}

	// Both should have same number of nodes
	if len(engineSpec.Nodes) != len(graphSpec.Nodes) {
		t.Errorf("node count mismatch: engine=%d graph=%d",
			len(engineSpec.Nodes), len(graphSpec.Nodes))
	}

	// Both should have same number of edges
	if len(engineSpec.Edges) != len(graphSpec.Edges) {
		t.Errorf("edge count mismatch: engine=%d graph=%d",
			len(engineSpec.Edges), len(graphSpec.Edges))
	}

	// Both should have same entry count
	if len(engineSpec.Entries) != len(graphSpec.Entries) {
		t.Errorf("entry count mismatch: engine=%d graph=%d",
			len(engineSpec.Entries), len(graphSpec.Entries))
	}

	// Both should pass validation
	engR := workflow.Validate(engineSpec)
	graphR := workflow.Validate(graphSpec)
	if !engR.Valid() {
		t.Errorf("engine spec validation: %v", engR.Errors)
	}
	if !graphR.Valid() {
		t.Errorf("graph spec validation: %v", graphR.Errors)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────

// assertEdge checks that an edge exists in the spec.
func assertEdge(t *testing.T, edges []workflow.EdgeSpec, from, to string, kind workflow.EdgeKind) {
	t.Helper()
	for _, e := range edges {
		if e.From == workflow.NodeID(from) && e.To == workflow.NodeID(to) {
			if e.Kind != kind {
				t.Errorf("edge %s→%s: expected kind %v, got %v", from, to, kind, e.Kind)
			}
			return
		}
	}
	t.Errorf("edge %s→%s not found in spec", from, to)
}

// createLinearSpec creates a linear workflow spec with n nodes (0→1→...→n-1).
func createLinearSpec(id string, n int) *workflow.WorkflowSpec {
	spec := workflow.NewWorkflow(id)
	for i := 0; i < n; i++ {
		nodeID := workflow.NodeID(string(rune('a' + i)))
		spec.AddNode(workflow.NodeSpec{
			ID:        nodeID,
			Name:      string(rune('A' + i)),
			AgentType: "echo",
		})
		if i > 0 {
			prevID := workflow.NodeID(string(rune('a' + i - 1)))
			spec.AddEdge(workflow.EdgeSpec{
				From: prevID,
				To:   nodeID,
				Kind: workflow.EdgeDataDependency,
			})
		}
	}
	return spec
}

// createCycleSpec creates a spec with a cycle: a→b, b→a.
func createCycleSpec() *workflow.WorkflowSpec {
	return workflow.NewWorkflow("cycle").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "b", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "b", Kind: workflow.EdgeDataDependency}).
		AddEdge(workflow.EdgeSpec{From: "b", To: "a", Kind: workflow.EdgeDataDependency})
}

//nolint:staticcheck // test file — intentionally uses legacy types for backward compat verification
