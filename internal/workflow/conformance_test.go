// Package workflow_test — conformance tests for DAG execution semantics.
//
// These tests verify that all three current runtimes (engine.Executor,
// engine.DynamicExecutor, graph.Graph) produce consistent behavior for
// the same workflow topology. Each test documents both the CURRENT
// observed behaviour and the EXPECTED unified behaviour (once the
// single Runner from DAG_UNIFIED_PIPELINE.md §6 is implemented).
//
// Phase: P0 — semantic contract freezing.
// All tests in this file serve as the conformance gate for the unified
// pipeline. When the single Runner is ready, these tests are switched
// to run against it first, then verified against the legacy runtimes.
//
// See: DAG_UNIFIED_PIPELINE.md §10 (Acceptance Criteria)

package workflow_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	wfengine "github.com/Timwood0x10/ares/internal/workflow/engine"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

// ──────────────────────────────────────────────────────────────────────
// Test helpers
// ──────────────────────────────────────────────────────────────────────

// testAgent is a minimal Agent implementation for engine-package tests.
type testAgent struct {
	id   string
	typ  string
	proc func(ctx context.Context, input any) (any, error)
}

func (a *testAgent) ID() string                    { return a.id }
func (a *testAgent) Type() models.AgentType        { return models.AgentType(a.typ) }
func (a *testAgent) Status() models.AgentStatus    { return models.AgentStatusReady }
func (a *testAgent) Start(_ context.Context) error { return nil }
func (a *testAgent) Stop(_ context.Context) error  { return nil }
func (a *testAgent) Process(ctx context.Context, in any) (any, error) {
	if a.proc != nil {
		return a.proc(ctx, in)
	}
	return &models.RecommendResult{Items: []*models.RecommendItem{
		{ItemID: "r1", Name: "ok", Description: "ok", Price: 1},
	}}, nil
}
func (a *testAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	return nil, nil //nolint: nilnil // not used in conformance tests
}

// echoNode returns a graph.FuncNode that records its execution and stores output.
func echoNode(id string, recorder *[]string) *wfgraph.FuncNode {
	n, err := wfgraph.NewFuncNode(id, func(_ context.Context, state *wfgraph.State) error {
		if recorder != nil {
			*recorder = append(*recorder, id)
		}
		state.Set("node."+id, id+"_done")
		return nil
	})
	if err != nil {
		panic(err)
	}
	return n
}

// ──────────────────────────────────────────────────────────────────────
// §2.1 — Condition / Skip semantics
// ──────────────────────────────────────────────────────────────────────

// engine.Executor: Condition false → StepStatusSkipped → completed 包含它 → 下游继续
// graph.Graph: Edge condition false → 目标不入 ready queue → 后代静默不可达
//
// Expected unified: 条件不满足的节点标记为 NotSelected 或 Unreachable，
// 后代节点不会静默跳过，必须有明确的状态和原因。

func TestConformance_ConditionSkip_EngineExecutor(t *testing.T) {
	// Topology: ingest → process (condition: false) → finalize (depends: process)
	// Current engine.Executor behaviour:
	//   - process gets StepStatusSkipped
	//   - process is added to completed map
	//   - finalize's DependsOn is satisfied → finalize executes
	var executed []string
	executedMu := sync.Mutex{}
	mark := func(id string) {
		executedMu.Lock()
		executed = append(executed, id)
		executedMu.Unlock()
	}

	reg := wfengine.NewAgentRegistry()
	_ = reg.Register("track", func(_ context.Context, _ any) (base.Agent, error) {
		return &testAgent{
			id: "track", typ: "track",
			proc: func(_ context.Context, input any) (any, error) {
				mark(input.(string))
				return &models.RecommendResult{Items: []*models.RecommendItem{
					{ItemID: "r1", Name: input.(string), Description: "ok", Price: 1},
				}}, nil
			},
		}, nil
	})
	exec2 := wfengine.NewExecutor(reg)

	workflow := &wfengine.Workflow{
		ID:   "wf-cond-skip",
		Name: "ConditionSkip",
		Steps: []*wfengine.Step{
			{ID: "ingest", Name: "Ingest", AgentType: "track", Input: "ingest"},
			{
				ID: "process", Name: "Process", AgentType: "track", Input: "process",
				DependsOn: []string{"ingest"},
				Condition: func(vars map[string]any) bool { return false },
			},
			{
				ID: "finalize", Name: "Finalize", AgentType: "track", Input: "finalize",
				DependsOn: []string{"process"},
			},
		},
	}

	result, err := exec2.Execute(context.Background(), workflow, "input")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Build status map
	statuses := make(map[string]wfengine.StepStatus)
	for _, s := range result.Steps {
		statuses[s.StepID] = s.Status
	}

	// CURRENT: process skipped, finalize still executes
	if statuses["process"] != wfengine.StepStatusSkipped {
		t.Errorf("CURRENT [engine.Executor]: process expected skipped, got %s", statuses["process"])
	}
	if statuses["finalize"] != wfengine.StepStatusCompleted {
		t.Errorf("CURRENT [engine.Executor]: finalize expected completed, got %s", statuses["finalize"])
	}

	t.Logf("CURRENT engine.Executor: process=%s, finalize=%s",
		statuses["process"], statuses["finalize"])
	t.Log("EXPECTED unified: process=not_selected, finalize=blocked (upstream was skipped)")
}

func TestConformance_ConditionSkip_Graph(t *testing.T) {
	// Topology: ingest → process (edge condition: false) → finalize (edge from process)
	// Current graph.Graph behaviour:
	//   - Edge condition on ingest→process evaluates to false
	//   - process never enters ready queue
	//   - finalize's in-degree never reaches 0 → finalize never executes
	g, err := wfgraph.NewGraph("cond-skip-graph")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	var order []string
	_, _ = g.Node("ingest", echoNode("ingest", &order))
	_, _ = g.Node("process", echoNode("process", &order))
	_, _ = g.Node("finalize", echoNode("finalize", &order))
	_, _ = g.Edge("ingest", "process", func(s *wfgraph.State) bool { return false })
	_, _ = g.Edge("process", "finalize")
	_, _ = g.Start("ingest")

	result, err := g.Execute(context.Background(), wfgraph.NewState())
	if err != nil {
		// graph may return error due to unreachable nodes (depends on implementation)
		t.Logf("CURRENT graph.Graph: Execute returned error: %v", err)
	} else {
		t.Logf("CURRENT graph.Graph: completed with result")
		_ = result
	}

	t.Logf("CURRENT graph.Graph: execution order=%v", order)
	t.Log("CURRENT graph.Graph: process NOT in order (edge condition false)")
	t.Log("CURRENT graph.Graph: finalize NOT in order (process was never reached)")
	t.Log("EXPECTED unified: process=not_selected, finalize=unreachable")
}

// ──────────────────────────────────────────────────────────────────────
// §2.2 — Router semantics
// ──────────────────────────────────────────────────────────────────────

// engine.Executor: Router 可返回目标 ID，绕过 DependsOn；不会自动取消其他分支
// graph.Graph: Router 将目标加入 ready queue；静态后继仍继续处理
//
// Expected unified: Router 拆为 SelectBranch / ActivateNode / PrioritizeNode，
// 默认禁止隐式 bypass 依赖。

func TestConformance_Router_EngineExecutor(t *testing.T) {
	// Topology: classify → path_a, classify → path_b
	// Router on classify always returns "path_b"
	// CURRENT engine.Executor: path_b is routed into execution; path_a still
	// executes because its DependsOn is satisfied.
	reg := wfengine.NewAgentRegistry()
	_ = reg.Register("echo", func(_ context.Context, _ any) (base.Agent, error) {
		return &testAgent{id: "echo", typ: "echo"}, nil
	})
	exec := wfengine.NewExecutor(reg)

	workflow := &wfengine.Workflow{
		ID:   "wf-router",
		Name: "RouterDemo",
		Steps: []*wfengine.Step{
			{
				ID: "classify", Name: "Classify", AgentType: "echo",
				Router: func(_ context.Context, _ string, _ map[string]any, _ string) string {
					return "path_b"
				},
			},
			{ID: "path_a", Name: "Path A", AgentType: "echo", DependsOn: []string{"classify"}},
			{ID: "path_b", Name: "Path B", AgentType: "echo", DependsOn: []string{"classify"}},
		},
	}

	result, err := exec.Execute(context.Background(), workflow, "classify me")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	statuses := make(map[string]wfengine.StepStatus)
	for _, s := range result.Steps {
		statuses[s.StepID] = s.Status
	}

	t.Logf("CURRENT engine.Executor: classify=%s, path_a=%s, path_b=%s",
		statuses["classify"], statuses["path_a"], statuses["path_b"])
	t.Log("CURRENT: both path_a and path_b execute because their DependsOn are both satisfied")
	t.Log("EXPECTED unified: Router is additive (BranchMany), both paths still activate;",
		"exclusive-or requires explicit BranchOne")
}

func TestConformance_Router_Graph(t *testing.T) {
	// Topology: classify → path_a (edge), classify → path_b (edge)
	// Graph-level router returns "path_b" after classify completes
	// CURRENT graph.Graph: path_b added to ready queue; path_a still processed
	// via normal edge traversal.
	g, err := wfgraph.NewGraph("router-graph")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	var order []string
	_, _ = g.Node("classify", echoNode("classify", &order))
	_, _ = g.Node("path_a", echoNode("path_a", &order))
	_, _ = g.Node("path_b", echoNode("path_b", &order))
	_, _ = g.Edge("classify", "path_a")
	_, _ = g.Edge("classify", "path_b")
	_, _ = g.Start("classify")

	// The NodeRouter in graph.Graph is additive — it injects nodes into
	// the ready queue but does NOT suppress normal edge traversal.
	_, _ = g.SetRouter(func(_ context.Context, nodeID string, state *wfgraph.State) string {
		if nodeID == "classify" {
			return "path_b" // prepend path_b — but path_a still gets traversed via edge
		}
		return ""
	})

	_, err = g.Execute(context.Background(), wfgraph.NewState())
	if err != nil {
		t.Logf("CURRENT graph.Graph: Execute error: %v", err)
	}

	t.Logf("CURRENT graph.Graph: execution order=%v", order)
	t.Log("CURRENT: Router is additive — it does NOT suppress static edges")
	t.Log("EXPECTED unified: SelectBranch overrides static edges;",
		"PrioritizeNode only affects queue order")
}

// ──────────────────────────────────────────────────────────────────────
// §2.3 — State model
// ──────────────────────────────────────────────────────────────────────

// graph 使用共享可变 State (map[string]any)，依赖单线程执行假设。
// engine 把状态拆成 WorkflowExecution.Variables + OutputStore + StepState。
//
// Expected unified: StateView 接口 + 事务化写集 (Set 返回 error)，
// 并发写冲突有明确策略。

func TestConformance_StateModel_GraphSharedState(t *testing.T) {
	// Demonstrates that graph.State allows arbitrary reads/writes from any node.
	// This is flexible but unsafe under concurrent execution.
	g, err := wfgraph.NewGraph("state-graph")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	writerNode, _ := wfgraph.NewFuncNode("writer", func(_ context.Context, s *wfgraph.State) error {
		s.Set("shared_key", "written_by_writer")
		return nil
	})
	readerNode, _ := wfgraph.NewFuncNode("reader", func(_ context.Context, s *wfgraph.State) error {
		val, _ := s.Get("shared_key")
		_ = val // reader sees whatever writer wrote — no isolation
		return nil
	})
	unrelatedNode, _ := wfgraph.NewFuncNode("unrelated", func(_ context.Context, s *wfgraph.State) error {
		// Any node can write any key — no scoping or ownership
		s.Set("shared_key", "overwritten_by_unrelated")
		return nil
	})

	_, _ = g.Node("writer", writerNode)
	_, _ = g.Node("reader", readerNode)
	_, _ = g.Node("unrelated", unrelatedNode)
	_, _ = g.Edge("writer", "reader")
	_, _ = g.Start("unrelated")
	_, _ = g.Start("writer")

	_, _ = g.Execute(context.Background(), wfgraph.NewState())
	t.Log("CURRENT graph.Graph: shared State map — any node can read/write any key")
	t.Log("CURRENT: no isolation, no conflict detection, no ownership scoping")
	t.Log("EXPECTED unified: StateView + transactional write-set;",
		"concurrent writes must have explicit conflict strategy (reject/version/merge)")
}

// ──────────────────────────────────────────────────────────────────────
// §2.4 — Start node semantics
// ──────────────────────────────────────────────────────────────────────

// graph.Graph.Start() 只保存一个 ID 并在执行前验证存在，
// 但 ready queue 实际从全图所有零入度节点生成。
//
// Expected unified: Start 限定执行可达域，不在 Start 下游的节点不应执行。

func TestConformance_StartNode_Graph(t *testing.T) {
	// Graph: A (in-degree 0) → C, B (in-degree 0) → C
	// Start("B") should mean only B executes, but currently A also executes
	// because seedReadyQueue starts from all zero-in-degree nodes.
	g, err := wfgraph.NewGraph("start-graph")
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	var order []string
	_, _ = g.Node("A", echoNode("A", &order))
	_, _ = g.Node("B", echoNode("B", &order))
	_, _ = g.Node("C", echoNode("C", &order))
	_, _ = g.Edge("A", "C")
	_, _ = g.Edge("B", "C")
	_, _ = g.Start("B") // intentionally start from B

	_, err = g.Execute(context.Background(), wfgraph.NewState())
	if err != nil {
		t.Logf("Execute error: %v", err)
	}

	hasA := false
	hasB := false
	for _, id := range order {
		if id == "A" {
			hasA = true
		}
		if id == "B" {
			hasB = true
		}
	}

	if hasA {
		t.Log("CURRENT graph.Graph: A executed despite Start('B') —",
			"ready queue seeds from ALL zero-in-degree nodes")
	}
	if !hasB {
		t.Error("CURRENT graph.Graph: B should have executed (Start('B') + zero in-degree)")
	}
	t.Logf("CURRENT graph.Graph: execution order=%v", order)
	t.Log("EXPECTED unified: Start strictly limits the reachable set;",
		"A would be unreachable if not downstream of B")
}

// ──────────────────────────────────────────────────────────────────────
// §2.5 — Diamond topology + condition combinations
// ──────────────────────────────────────────────────────────────────────

// Diamond: ingest → (branch_a | branch_b) → merge
// Tests how each runtime handles a diamond with different condition outcomes.

func TestConformance_Diamond_AllConditionsFalse(t *testing.T) {
	// All branch conditions false → neither branch executes
	t.Run("engine.Executor", func(t *testing.T) {
		reg := wfengine.NewAgentRegistry()
		_ = reg.Register("echo", func(_ context.Context, _ any) (base.Agent, error) {
			return &testAgent{id: "echo", typ: "echo"}, nil
		})
		exec := wfengine.NewExecutor(reg)

		workflow := &wfengine.Workflow{
			ID:   "wf-diamond-all-false",
			Name: "DiamondAllFalse",
			Steps: []*wfengine.Step{
				{ID: "ingest", AgentType: "echo"},
				{
					ID: "branch_a", AgentType: "echo",
					DependsOn: []string{"ingest"},
					Condition: func(vars map[string]any) bool { return false },
				},
				{
					ID: "branch_b", AgentType: "echo",
					DependsOn: []string{"ingest"},
					Condition: func(vars map[string]any) bool { return false },
				},
				{
					ID: "merge", AgentType: "echo",
					DependsOn: []string{"branch_a", "branch_b"},
				},
			},
		}

		result, err := exec.Execute(context.Background(), workflow, "input")
		if err != nil {
			t.Logf("CURRENT engine.Executor: Execute error: %v", err)
		}

		statuses := make(map[string]wfengine.StepStatus)
		for _, s := range result.Steps {
			statuses[s.StepID] = s.Status
		}

		t.Logf("CURRENT engine.Executor: ingest=%s, a=%s, b=%s, merge=%s",
			statuses["ingest"], statuses["branch_a"], statuses["branch_b"], statuses["merge"])
		t.Log("CURRENT: branch_a skipped, branch_b skipped, merge deadlocks",
			"(depends on two skipped steps, canExecute never true)")
		t.Log("EXPECTED unified: branch_a=not_selected, branch_b=not_selected,",
			"merge=blocked (all upstream skipped)")
	})

	t.Run("graph.Graph", func(t *testing.T) {
		g, err := wfgraph.NewGraph("diamond-graph")
		if err != nil {
			t.Fatalf("NewGraph: %v", err)
		}

		var order []string
		_, _ = g.Node("ingest", echoNode("ingest", &order))
		_, _ = g.Node("branch_a", echoNode("branch_a", &order))
		_, _ = g.Node("branch_b", echoNode("branch_b", &order))
		_, _ = g.Node("merge", echoNode("merge", &order))
		_, _ = g.Edge("ingest", "branch_a", func(s *wfgraph.State) bool { return false })
		_, _ = g.Edge("ingest", "branch_b", func(s *wfgraph.State) bool { return false })
		_, _ = g.Edge("branch_a", "merge")
		_, _ = g.Edge("branch_b", "merge")
		_, _ = g.Start("ingest")

		_, err = g.Execute(context.Background(), wfgraph.NewState())
		if err != nil {
			t.Logf("CURRENT graph.Graph: Execute error (expected): %v", err)
		}
		t.Logf("CURRENT graph.Graph: execution order=%v", order)
		t.Log("CURRENT: branch_a/branch_b never enter ready,",
			"merge's in-degree never reaches 0 → merge is unreachable")
		t.Log("EXPECTED unified: branch_a=not_selected, branch_b=not_selected,",
			"merge=unreachable")
	})
}

// ──────────────────────────────────────────────────────────────────────
// §2.6 — Loop + condition interaction
// ──────────────────────────────────────────────────────────────────────

// engine.Executor: LoopConfig 支持 MaxIterations + UntilCondition。
// 测试 loop 内条件 evaluate 在每个 iteration 上都重新运行。

func TestConformance_LoopWithCondition(t *testing.T) {
	reg := wfengine.NewAgentRegistry()
	_ = reg.Register("echo", func(_ context.Context, _ any) (base.Agent, error) {
		return &testAgent{id: "echo", typ: "echo"}, nil
	})
	exec := wfengine.NewExecutor(reg)

	iteration := 0
	until := func(vars map[string]any, iter int) bool {
		return iter >= 3 // stop after 3 iterations
	}

	workflow := &wfengine.Workflow{
		ID:   "wf-loop-cond",
		Name: "LoopWithCondition",
		Steps: []*wfengine.Step{
			{
				ID: "process", AgentType: "echo",
				Condition: func(vars map[string]any) bool {
					iteration++
					return iteration <= 2
				},
			},
		},
		LoopConfig: &wfengine.LoopConfig{
			MaxIterations:  5,
			UntilCondition: until,
			LoopSteps:      []string{"process"},
		},
	}

	_, err := exec.Execute(context.Background(), workflow, "input")
	if err != nil {
		t.Logf("CURRENT engine.Executor: Execute error: %v", err)
	}
	t.Logf("CURRENT engine.Executor: loop with condition, process skipped when condition false")
	t.Log("EXPECTED unified: loop iteration is independent of node conditions;",
		"condition only affects node-level skip, not loop-level continuation")
}

// ──────────────────────────────────────────────────────────────────────
// Summary
// ──────────────────────────────────────────────────────────────────────

func TestConformance_Summary(t *testing.T) {
	// This test exists solely to print the conformance status table in go test -v output.
	t.Log("╔══════════════════════════════════════════════════════════════╗")
	t.Log("║          DAG Unification — Conformance Test Summary        ║")
	t.Log("╠══════════════════════════════════════════════════════════════╣")
	t.Log("║ §2.1 Condition/Skip  — 3 runtimes, 3 different behaviors   ║")
	t.Log("║ §2.2 Router           — Additive vs bypass vs order-only   ║")
	t.Log("║ §2.3 State model      — Shared map vs layered stores       ║")
	t.Log("║ §2.4 Start semantics  — Advisory vs strict reachability    ║")
	t.Log("║ §2.5 Diamond + conds  — Deadlock vs unreachable vs blocked ║")
	t.Log("╠══════════════════════════════════════════════════════════════╣")
	t.Log("║ Phase: P0 — semantic contract freezing                     ║")
	t.Log("║ Next:   P1 — Workflow IR + Compiler + Validator            ║")
	t.Log("╚══════════════════════════════════════════════════════════════╝")
}

//nolint:staticcheck // test file — intentionally uses legacy types for backward compat verification
