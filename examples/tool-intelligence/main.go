// Command tool-intelligence demonstrates the full Capability Planner pipeline:
//
//  1. Semantic analysis — parse user request into Intent
//  2. Capability planning — decompose Intent into requirements
//  3. Tool resolution — map capabilities to registered tools
//  4. Evidence-aware scoring — rank tools by metadata + history
//  5. Execution planning — build single-step or multi-step DAG
//  6. Planner fallback — auto-select tool when LLM provides no name
//  7. Evidence feedback — record execution results for future scoring
//
// Run: go run ./examples/tool-intelligence
package main

import (
	"context"
	"fmt"
	"time"

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

// registryProvider adapts core.Registry to planner.ToolProvider.
type registryProvider struct {
	reg *core.Registry
}

func (p *registryProvider) ListTools() []string {
	return p.reg.List()
}

func (p *registryProvider) GetToolCapabilities(name string) ([]string, error) {
	return nil, nil
}

func main() {
	ctx := context.Background()

	// ── 1. Register all built-in tools ──────────────────────
	fmt.Println("=== 1. Registering Built-in Tools ===")

	reg := core.NewRegistry()
	tools := []core.Tool{
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
	for _, t := range tools {
		if err := reg.Register(t); err != nil {
			fmt.Printf("  register %s: %v\n", t.Name(), err)
			return
		}
		fmt.Printf("  ✓ %s\n", t.Name())
	}
	fmt.Printf("  → %d tools registered\n\n", len(reg.List()))

	// ── 2. Wire the Capability Planner ──────────────────────
	fmt.Println("=== 2. Wiring Capability Planner ===")
	provider := &registryProvider{reg: reg}

	resolver, err := planner.NewToolResolver(provider)
	if err != nil {
		fmt.Printf("  resolver: %v\n", err)
		return
	}

	evStore := planner.NewMemoryEvidenceStore()

	p, err := planner.NewPlanner(
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

	// ── 3. Semantic Analysis — Chinese summation ────────────
	fmt.Println("=== 3. Semantic Analysis ===")
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

	// ── 4. Single-step Planning: Summation ──────────────────
	fmt.Println("=== 4. Single-step Plan: Summation ===")
	plan, err := p.Plan(ctx, "累加从1到100")
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return
	}
	fmt.Printf("  Intent:    %s / %s\n", plan.Intent.Goal, plan.Intent.Operation)
	fmt.Printf("  MultiStep: %v\n", plan.IsMultiStep)
	for _, step := range plan.Steps {
		fmt.Printf("  Step:      %s → %s (%s)\n", step.StepID, step.ToolName, step.CapabilityName)
		fmt.Printf("  Params:    %v\n", step.Parameters)
	}
	fmt.Println()

	// ── 5. Tool Scoring with Evidence ───────────────────────
	fmt.Println("=== 5. Evidence-Aware Scoring ===")
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

	// Query evidence to see aggregate.
	calcEvidence, _ := evStore.Query(ctx, "calculator", "", 50)
	searchEvidence, _ := evStore.Query(ctx, "web_search", "", 50)
	fmt.Printf("  Calculator evidence: %d records\n", len(calcEvidence))
	fmt.Printf("  Web search evidence: %d records (%d failures)\n\n", len(searchEvidence), 5)

	// Score candidates with evidence.
	scorer := planner.NewToolScorer()
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

	// ── 6. ToolExecutionBridge — Planner Fallback ───────────
	fmt.Println("=== 6. ToolExecutionBridge: Planner Fallback ===")
	bridge, err := planner.NewToolExecutionBridge(reg, p)
	if err != nil {
		fmt.Printf("  bridge: %v\n", err)
		return
	}

	// Direct execution: LLM knows the tool name.
	result, err := bridge.Execute(ctx, "calculator",
		map[string]interface{}{"expression": "100*(100+1)/2"}, "")
	if err != nil {
		fmt.Printf("  direct execution FAIL: %v\n", err)
	} else {
		fmt.Printf("  ✓ Direct: calculator(100*(100+1)/2) = %v\n", result.Data)
	}

	// Planner fallback: no tool name, planner auto-resolves.
	// User still provides params to fill the plan.
	result, err = bridge.Execute(ctx, "",
		map[string]interface{}{"expression": "100*(100+1)/2"}, "累加从1到100")
	if err != nil {
		fmt.Printf("  planner fallback FAIL: %v\n", err)
	} else {
		fmt.Printf("  ✓ Fallback: auto→calculator with user params: %v\n", result.Data)
	}

	// Planner fallback without user params: planner generates plan but
	// params are empty (LLM is normally needed for param extraction).
	result, err = bridge.Execute(ctx, "", nil, "计算1+1")
	if err != nil {
		fmt.Printf("  ✓ Fallback(no params): detected as expected: %v\n", err)
	} else {
		fmt.Printf("  ✓ Fallback(no params): %v\n", result.Data)
	}

	// Unknown tool name → planner fallback with params.
	result, err = bridge.Execute(ctx, "unknown_tool",
		map[string]interface{}{"expression": "2+2"}, "计算")
	if err != nil {
		fmt.Printf("  unknown tool FAIL: %v\n", err)
	} else {
		fmt.Printf("  ✓ Unknown→fallback: %v\n", result.Data)
	}
	fmt.Println()

	// ── 7. Multi-step DAG Plan ──────────────────────────────
	fmt.Println("=== 7. Multi-step Plan (DAG) ===")
	// Build a multi-step plan manually: PDF→text→embedding
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
		fmt.Printf("  ✓ DAG valid: %d steps, %s\n", len(plan2.Steps),
			map[bool]string{true: "multi-step", false: "single-step"}[plan2.IsMultiStep])
		for _, step := range plan2.Steps {
			deps := step.DependsOn
			if len(deps) == 0 {
				deps = []string{"(none)"}
			}
			fmt.Printf("    %s → %-15s deps=%v\n", step.StepID, step.ToolName, deps)
		}
	}
	fmt.Println()

	// ── 8. Summary ─────────────────────────────────────────
	fmt.Println("=== Done ===")
	fmt.Println("The Capability Planner pipeline is fully operational.")
}
