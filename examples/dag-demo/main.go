// Command dag-demo demonstrates multi-step DAG planning and execution.
//
// The capability planner can decompose a complex request into multiple
// dependent steps (a DAG), execute them in topological order, chain
// outputs between steps, and recover via fallback tools on failure.
//
// Run: go run ./examples/dag-demo
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/planner"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func main() {
	ctx := context.Background()
	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║  Multi-Step DAG Execution Demo                 ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()

	// ── 1. Register tools ───────────────────────────────────
	fmt.Println("1. Registering tools...")
	reg := core.NewRegistry()
	reg.Register(&echoTool{name: "fetcher"})
	reg.Register(&echoTool{name: "extractor"})
	reg.Register(&echoTool{name: "formatter"})
	reg.Register(&failingTool{name: "bad_extractor"}) // will fail → tests fallback
	fmt.Println("   4 tools registered (fetcher, extractor, formatter, bad_extractor)")
	fmt.Println()

	// ── 2. Build a multi-step execution plan ────────────────
	fmt.Println("2. Building multi-step DAG plan...")
	plan := &planner.ExecutionPlan{
		PlanID:      "dag-demo",
		IsMultiStep: true,
		Intent: planner.Intent{
			Goal:      "document processing",
			Operation: "fetch_extract_format",
		},
		Steps: []planner.ExecutionStep{
			{
				StepID: "fetch", ToolName: "fetcher",
				CapabilityName: "WebFetch",
				Parameters:     map[string]interface{}{"url": "https://example.com/doc"},
			},
			{
				StepID: "extract", ToolName: "bad_extractor",
				CapabilityName: "TextExtraction",
				Parameters:     map[string]interface{}{"input": ""},
				DependsOn:      []string{"fetch"},
				// Fallback: if bad_extractor fails, try extractor.
				FallbackToolNames: []string{"extractor"},
			},
			{
				StepID: "format", ToolName: "formatter",
				CapabilityName: "StringManipulation",
				Parameters:     map[string]interface{}{"input": ""},
				DependsOn:      []string{"extract"},
			},
		},
	}

	fmt.Printf("   Plan: %s (%d steps)\n", plan.PlanID, len(plan.Steps))
	for _, s := range plan.Steps {
		deps := s.DependsOn
		if len(deps) == 0 {
			deps = []string{"(none)"}
		}
		fmt.Printf("     %s → %-14s deps=%v  fallback=%v\n",
			s.StepID, s.ToolName, deps, s.FallbackToolNames)
	}
	fmt.Println()

	// ── 3. Validate DAG ────────────────────────────────────
	fmt.Println("3. Validating DAG...")
	validator := planner.NewDAGValidator()
	if errs := validator.Validate(plan); len(errs) > 0 {
		fmt.Println("   DAG INVALID:")
		for _, e := range errs {
			fmt.Printf("     ✗ %s\n", e.Error())
		}
		return
	}
	fmt.Println("   ✅ DAG is valid")
	fmt.Println()

	// ── 4. Build planner + bridge ───────────────────────────
	fmt.Println("4. Creating planner and bridge...")
	store := planner.NewMemoryEvidenceStore()
	provider := planner.NewRegistryProvider(reg)
	resolver, _ := planner.NewToolResolver(provider)

	p, err := planner.NewPlanner(
		planner.NewRuleBasedAnalyzer(),
		planner.NewCapabilityPlanner(),
		resolver,
		planner.NewEvidenceScorer(store),
		planner.NewExecutionPlanner(),
		store,
	)
	if err != nil {
		panic(err)
	}

	bridge, err := planner.NewToolExecutionBridge(reg, p, store)
	if err != nil {
		panic(err)
	}
	fmt.Println("   ✅ Planner + Bridge ready")
	fmt.Println()

	// ── 5. Execute multi-step DAG ───────────────────────────
	fmt.Println("5. Executing multi-step DAG...")
	fmt.Println("   (bad_extractor will fail → fallback to extractor)")
	fmt.Println()

	start := time.Now()
	result, err := bridge.ExecutePlan(ctx, plan, nil)
	latency := time.Since(start)

	if err != nil {
		fmt.Printf("   ❌ DAG execution failed: %v\n", err)
	} else {
		fmt.Printf("   ✅ DAG execution succeeded in %v\n", latency.Round(time.Millisecond))
		fmt.Printf("   Final result: %v\n", result.Data)
	}
	fmt.Println()

	// ── 6. Evidence collected ───────────────────────────────
	fmt.Println("6. Execution evidence collected:")
	allEvidence, _ := store.Query(ctx, "", "", 10)
	for _, ev := range allEvidence {
		status := "✅"
		if !ev.Success {
			status = "❌"
		}
		fmt.Printf("   %s %s/%s  latency=%v\n",
			status, ev.ToolName, ev.CapabilityName, ev.Latency.Round(time.Millisecond))
	}
	fmt.Println()

	// ── 7. Aggregate scores ────────────────────────────────
	fmt.Println("7. Aggregate scores (evidence-driven):")
	agg, _ := store.Aggregate(ctx, "")
	for key, score := range agg {
		fmt.Printf("   %-25s  base=%.1f  evidence=%.1f  penalty=%.1f  final=%.1f\n",
			key, score.BaseScore, score.EvidenceScore, score.Penalty, score.Final)
	}
	fmt.Println()

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║  Demo Complete                                  ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// ── Mock tools ────────────────────────────────────────────

// echoTool returns its input as output (pass-through for demo).
type echoTool struct{ name string }

func (t *echoTool) Name() string                      { return t.name }
func (t *echoTool) Description() string               { return "echo " + t.name }
func (t *echoTool) Category() core.ToolCategory       { return core.CategoryCore }
func (t *echoTool) Capabilities() []core.Capability   { return nil }
func (t *echoTool) Parameters() *core.ParameterSchema { return nil }
func (t *echoTool) Execute(_ context.Context, params map[string]interface{}) (core.Result, error) {
	input, _ := params["input"].(string)
	if input == "" {
		input = params["url"].(string)
	}
	return core.Result{
		Success: true,
		Data: map[string]interface{}{
			"output": fmt.Sprintf("[%s processed: %s]", t.name, input),
			"text":   fmt.Sprintf("content from %s", t.name),
		},
	}, nil
}

// failingTool always fails, used to test fallback chains.
type failingTool struct{ name string }

func (t *failingTool) Name() string                      { return t.name }
func (t *failingTool) Description() string               { return "always fails" }
func (t *failingTool) Category() core.ToolCategory       { return core.CategoryCore }
func (t *failingTool) Capabilities() []core.Capability   { return nil }
func (t *failingTool) Parameters() *core.ParameterSchema { return nil }
func (t *failingTool) Execute(_ context.Context, _ map[string]interface{}) (core.Result, error) {
	return core.Result{}, fmt.Errorf("intentional failure")
}
