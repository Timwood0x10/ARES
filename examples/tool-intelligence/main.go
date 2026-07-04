// Command tool-intelligence demonstrates the full Capability Planner pipeline.
//
// Part 1 — Public API (api/tools): simple, no internal imports needed.
// Part 2 — Internal details: tool registration, evidence, scoring, multi-step DAG.
//
// Run: go run ./examples/tool-intelligence
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/tools/planner"
	builtin_embedding "github.com/Timwood0x10/ares/internal/tools/resources/builtin/embedding"
	builtin_execution "github.com/Timwood0x10/ares/internal/tools/resources/builtin/execution"
	builtin_file "github.com/Timwood0x10/ares/internal/tools/resources/builtin/file"
	builtin_hash "github.com/Timwood0x10/ares/internal/tools/resources/builtin/hash"
	builtin_knowledge "github.com/Timwood0x10/ares/internal/tools/resources/builtin/knowledge"
	builtin_math "github.com/Timwood0x10/ares/internal/tools/resources/builtin/math"
	builtin_memory "github.com/Timwood0x10/ares/internal/tools/resources/builtin/memory"
	builtin_network "github.com/Timwood0x10/ares/internal/tools/resources/builtin/network"
	builtin_pdf "github.com/Timwood0x10/ares/internal/tools/resources/builtin/pdf"
	builtin_planning "github.com/Timwood0x10/ares/internal/tools/resources/builtin/planning"
	builtin_stringutils "github.com/Timwood0x10/ares/internal/tools/resources/builtin/stringutils"
	builtin_system "github.com/Timwood0x10/ares/internal/tools/resources/builtin/system"
	builtin_text "github.com/Timwood0x10/ares/internal/tools/resources/builtin/text"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func main() {
	ctx := context.Background()

	// ═══════════════════════════════════════════════════════════════
	// Part 1: Public API — no internal imports needed
	// ═══════════════════════════════════════════════════════════════
	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║  Part 1: Public API (api/tools)                ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()

	// ── 1a. Tools registry (builtins auto-loaded) ────────────
	fmt.Println("1a. Creating tool registry...")
	reg := tools.NewRegistry()
	fmt.Printf("    %d built-in tools registered\n", len(reg.List()))
	fmt.Println()

	// ── 1b. Planner from public API ──────────────────────────
	fmt.Println("1b. Creating planner (public API)...")
	p, err := tools.NewPlanner(reg)
	if err != nil {
		fmt.Printf("    FAIL: %v\n", err)
		return
	}
	fmt.Println("    ✓ Planner.Ready")
	fmt.Println()

	// ── 1c. Plan a request ───────────────────────────────────
	fmt.Println("1c. Plan '计算1+1'...")
	plan, err := p.Plan(ctx, "计算1+1")
	if err != nil {
		fmt.Printf("    FAIL: %v\n", err)
		return
	}
	fmt.Printf("    goal: %s, tool: %s, params: %v\n",
		plan.Intent.Goal, plan.Steps[0].ToolName, plan.Steps[0].Parameters)
	fmt.Println()

	// ── 1d. Bridge + Execute ─────────────────────────────────
	fmt.Println("1d. Creating bridge and executing...")
	bridge, err := tools.NewBridge(reg, p)
	if err != nil {
		fmt.Printf("    FAIL: %v\n", err)
		return
	}

	result, err := bridge.Execute(ctx, "", nil, "计算1+1")
	if err != nil {
		fmt.Printf("    FAIL: %v\n", err)
	} else {
		fmt.Printf("    ✓ %v\n", result.Data)
	}
	fmt.Println()

	// ═══════════════════════════════════════════════════════════════
	// Part 2: Internal Details — full planner pipeline
	// ═══════════════════════════════════════════════════════════════
	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║  Part 2: Internal Details                      ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()

	// ── 2a. Register all built-in tools ─────────────────────
	fmt.Println("2a. Registering all built-in tools...")

	coreReg := core.NewRegistry()
	allTools := []core.Tool{
		builtin_math.NewCalculator(),
		builtin_math.NewDateTime(),
		builtin_math.NewTextProcessor(),
		builtin_hash.NewHashTool(),
		builtin_stringutils.NewStringUtils(),
		builtin_text.NewJSONTools(),
		builtin_text.NewRegexTool(),
		builtin_text.NewDataValidation(),
		builtin_text.NewDataTransform(),
		builtin_text.NewLogAnalyzer(),
		builtin_network.NewHTTPRequest(),
		builtin_network.NewWebSearch(),
		builtin_file.NewFileTools(),
		builtin_knowledge.NewKnowledgeSearch(nil),
		builtin_memory.NewMemorySearch(nil),
		builtin_system.NewIDGenerator(),
		builtin_execution.NewCodeRunner(),
		builtin_pdf.NewPDFTool(),
		builtin_planning.NewTaskPlanner(nil),
		builtin_embedding.NewEmbeddingTool(""),
	}
	for _, t := range allTools {
		if err := coreReg.Register(t); err != nil {
			fmt.Printf("  register %s: %v\n", t.Name(), err)
			return
		}
		fmt.Printf("  ✓ %s\n", t.Name())
	}
	fmt.Printf("  → %d tools registered\n\n", coreReg.Count())

	// ── 2b. Wire planner manually ───────────────────────────
	fmt.Println("2b. Wiring planner manually...")
	provider := planner.NewRegistryProvider(coreReg)
	resolver, err := planner.NewToolResolver(provider)
	if err != nil {
		fmt.Printf("  resolver: %v\n", err)
		return
	}

	evStore := planner.NewMemoryEvidenceStore()

	internalP, err := planner.NewPlanner(
		planner.NewRuleBasedAnalyzer(),
		planner.NewCapabilityPlanner(),
		resolver,
		planner.NewEvidenceScorer(evStore),
		planner.NewExecutionPlanner(),
		evStore,
	)
	if err != nil {
		fmt.Printf("  planner: %v\n", err)
		return
	}
	fmt.Println("  ✓ Planner ready")
	fmt.Println()

	// ── 2c. Semantic Analysis ───────────────────────────────
	fmt.Println("2c. Semantic Analysis...")
	analyzer := planner.NewRuleBasedAnalyzer()
	requests := []string{
		"计算1到一百万的和",
		"累加从1到100",
		"parse this json file",
		"extract text from this pdf",
		"compute sha256 hash of hello",
		"generate a uuid",
		"search the web for go programming",
	}
	for _, req := range requests {
		intent, err := analyzer.Analyze(ctx, req)
		if err != nil {
			fmt.Printf("  ✗ %-40s → %v\n", req, err)
			continue
		}
		fmt.Printf("  ✓ %-40s → goal=%q  capa=%v\n",
			req, intent.Goal, intent.RequiredCapabilities)
	}
	fmt.Println()

	// ── 2d. Single-step Plan ────────────────────────────────
	fmt.Println("2d. Single-step Plan: Summation...")
	p2, err := internalP.Plan(ctx, "累加从1到100")
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return
	}
	fmt.Printf("  Intent:    %s / %s\n", p2.Intent.Goal, p2.Intent.Operation)
	fmt.Printf("  MultiStep: %v\n", p2.IsMultiStep)
	for _, step := range p2.Steps {
		fmt.Printf("  Step:      %s → %s (%s)\n", step.StepID, step.ToolName, step.CapabilityName)
		fmt.Printf("  Params:    %v\n", step.Parameters)
	}
	fmt.Println()

	// ── 2e. Evidence-Aware Scoring ──────────────────────────
	fmt.Println("2e. Evidence-Aware Scoring...")
	agg := planner.NewEvidenceAggregator(evStore)

	// Record success evidence for calculator.
	for i := 0; i < 20; i++ {
		_ = agg.Record(ctx, "calculator", "Arithmetic", true, 2*time.Millisecond, 0, "")
	}
	// Record failure evidence for web_search.
	for i := 0; i < 5; i++ {
		_ = agg.Record(ctx, "web_search", "WebSearch", false, 500*time.Millisecond, 2, "timeout")
	}
	_ = agg.Record(ctx, "web_search", "WebSearch", true, 300*time.Millisecond, 0, "")

	calcEvidence, _ := evStore.Query(ctx, "calculator", "", 50)
	searchEvidence, _ := evStore.Query(ctx, "web_search", "", 50)
	fmt.Printf("  Calculator evidence: %d records\n", len(calcEvidence))
	fmt.Printf("  Web search evidence: %d records (%d failures)\n\n", len(searchEvidence), 5)

	// Score candidates with evidence.
	scorer := planner.NewEvidenceScorer(evStore)
	candidates := []planner.ToolCandidate{
		{ToolName: "calculator", CapabilityName: "Arithmetic", Cost: 1,
			Deterministic: true, Composable: true, SuccessRate: 0.95},
		{ToolName: "web_search", CapabilityName: "WebSearch", Cost: 5,
			Deterministic: false, Composable: true, SideEffects: false, SuccessRate: 0.80},
	}
	scored, _ := scorer.Score(ctx, candidates, append(calcEvidence, searchEvidence...))
	for _, c := range scored {
		fmt.Printf("  %-15s score=%.1f  cost=%d  det=%v\n",
			c.ToolName, c.Score, c.Cost, c.Deterministic)
	}
	fmt.Println()

	// ── 2f. Bridge + Planner Fallback ───────────────────────
	fmt.Println("2f. ToolExecutionBridge: Planner Fallback...")
	internalBridge, err := planner.NewToolExecutionBridge(coreReg, internalP, evStore)
	if err != nil {
		fmt.Printf("  bridge: %v\n", err)
		return
	}

	// Direct execution.
	result, err = internalBridge.Execute(ctx, "calculator",
		map[string]interface{}{"expression": "100*(100+1)/2"}, "")
	if err != nil {
		fmt.Printf("  direct FAIL: %v\n", err)
	} else {
		fmt.Printf("  ✓ Direct: calculator(100*(100+1)/2) = %v\n", result.Data)
	}

	// Planner fallback with user params.
	result, err = internalBridge.Execute(ctx, "",
		map[string]interface{}{"expression": "100*(100+1)/2"}, "累加从1到100")
	if err != nil {
		fmt.Printf("  fallback FAIL: %v\n", err)
	} else {
		fmt.Printf("  ✓ Fallback: auto→calculator = %v\n", result.Data)
	}

	// Planner fallback without user params (extractor fills them).
	result, err = internalBridge.Execute(ctx, "", nil, "计算1+1")
	if err != nil {
		fmt.Printf("  fallback(no params) FAIL: %v\n", err)
	} else {
		fmt.Printf("  ✓ Fallback(no params): %v\n", result.Data)
	}

	// Unknown tool → planner resolves.
	result, err = internalBridge.Execute(ctx, "unknown_tool",
		map[string]interface{}{"expression": "2+2"}, "计算")
	if err != nil {
		fmt.Printf("  unknown tool FAIL: %v\n", err)
	} else {
		fmt.Printf("  ✓ Unknown→fallback: %v\n", result.Data)
	}
	fmt.Println()

	// ── 2g. Multi-step DAG ──────────────────────────────────
	fmt.Println("2g. Multi-step Plan (DAG)...")
	plan2 := &planner.ExecutionPlan{
		PlanID:      "demo-multi-step",
		IsMultiStep: true,
		Steps: []planner.ExecutionStep{
			{
				StepID: "extract", ToolName: "pdf_tool",
				CapabilityName: "PDFParsing",
				Parameters:     map[string]interface{}{"operation": "extract_text", "file_path": "doc.pdf"},
			},
			{
				StepID: "process", ToolName: "string_utils",
				CapabilityName: "StringManipulation",
				Parameters:     map[string]interface{}{"operation": "upper", "input": ""},
				DependsOn:      []string{"extract"},
			},
		},
	}

	validator := planner.NewDAGValidator()
	if errs := validator.Validate(plan2); len(errs) > 0 {
		fmt.Printf("  DAG errors:\n")
		for _, e := range errs {
			fmt.Printf("    %s\n", e.Error())
		}
	} else {
		fmt.Printf("  ✓ DAG valid: %d steps\n", len(plan2.Steps))
		for _, step := range plan2.Steps {
			deps := step.DependsOn
			if len(deps) == 0 {
				deps = []string{"(none)"}
			}
			fmt.Printf("    %s → %-15s deps=%v\n", step.StepID, step.ToolName, deps)
		}
	}
	fmt.Println()

	// ═══════════════════════════════════════════════════════════════
	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║  Done                                           ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("Public API path:    api/tools.NewPlanner(reg)")
	fmt.Println("Internal path:      planner.NewPlanner(...)")
	fmt.Println("Both are wired and operational.")
}
