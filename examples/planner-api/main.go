// Command planner-api demonstrates the public API for the capability planner.
//
// This example shows:
//  1. Create a tool registry with built-in tools
//  2. Create a planner from the registry — no internal imports needed
//  3. Analyze user requests and generate execution plans
//  4. Execute plans through the bridge with evidence feedback
//  5. Custom EvidenceStore plugin for persistence
//
// Run: go run ./examples/planner-api
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/tools/planner"
)

func main() {
	ctx := context.Background()
	fmt.Println("=== Planner API Demo ===")
	fmt.Println()

	// ── 1. Create registry with built-in tools ─────────────
	fmt.Println("1. Creating tool registry...")
	reg := tools.NewRegistry()
	fmt.Printf("   %d tools registered\n", len(reg.List()))
	fmt.Println()

	// ── 2. Create planner ──────────────────────────────────
	fmt.Println("2. Creating planner...")
	p, err := tools.NewPlanner(reg)
	if err != nil {
		panic(err)
	}
	fmt.Println("   ✓ Planner ready")
	fmt.Println()

	// ── 3. Plan a user request ─────────────────────────────
	fmt.Println("3. Planning a user request...")
	requests := []string{
		"计算1+1",
		"从1累加到100",
		"计算sha256 hash of hello",
	}

	for _, req := range requests {
		plan, err := p.Plan(ctx, req)
		if err != nil {
			fmt.Printf("   ✗ %q: %v\n", req, err)
			continue
		}
		fmt.Printf("   ✓ %q\n", req)
		fmt.Printf("     goal: %s, steps: %d, multi: %v\n",
			plan.Intent.Goal, len(plan.Steps), plan.IsMultiStep)
		for _, step := range plan.Steps {
			fmt.Printf("       - %s → %s (params: %v)\n",
				step.CapabilityName, step.ToolName, step.Parameters)
		}
	}
	fmt.Println()

	// ── 4. Execute via bridge ──────────────────────────────
	fmt.Println("4. Executing via bridge...")
	bridge, err := tools.NewBridge(reg, p)
	if err != nil {
		panic(err)
	}
	fmt.Println("   ✓ Bridge ready")

	execRequests := []struct {
		toolName string
		params   map[string]interface{}
		request  string
	}{
		{toolName: "", params: nil, request: "计算1+1"},
		{toolName: "calculator", params: map[string]interface{}{"expression": "2+2"}, request: ""},
	}

	for _, ex := range execRequests {
		start := time.Now()
		result, err := bridge.Execute(ctx, ex.toolName, ex.params, ex.request)
		latency := time.Since(start)
		if err != nil {
			fmt.Printf("   ✗ %q: %v\n", ex.request, err)
		} else {
			fmt.Printf("   ✓ %q → success=%v [%dms]\n",
				ex.request, result.Success, latency.Milliseconds())
		}
	}
	fmt.Println()

	// ── 5. Evidence was saved automatically ────────────────
	fmt.Println("5. Bridge saved execution evidence automatically.")
	fmt.Println("   (Evidence drives future tool scoring —")
	fmt.Println("    successful tools rank higher over time.)")
	fmt.Println()

	// ── 6. Custom tool with capabilities ───────────────────
	fmt.Println("6. Registering a custom tool with capabilities...")
	_ = reg.Register(&customMathTool{})
	fmt.Println("   ✓ custom_math registered (capabilities: math)")
	fmt.Println()

	plan, err := p.Plan(ctx, "计算3的平方")
	if err != nil {
		fmt.Printf("   Plan result: %v\n", err)
	} else {
		fmt.Printf("   Planner selected: %s for %q\n",
			plan.Steps[0].ToolName, plan.Intent.Operation)
	}
	fmt.Println()

	fmt.Println("=== Demo Complete ===")
}

// customMathTool demonstrates declaring a capability for planner discovery.
type customMathTool struct{}

func (t *customMathTool) Name() string        { return "custom_math" }
func (t *customMathTool) Description() string { return "Custom math evaluator" }
func (t *customMathTool) Capabilities() []string {
	// Declare capabilities so the planner can discover this tool dynamically
	// via the broad→granular mapping: "math" → Arithmetic, Summation, ...
	return []string{"math"}
}
func (t *customMathTool) Execute(_ context.Context, params map[string]interface{}) (tools.Result, error) {
	return tools.Result{Success: true, Data: map[string]interface{}{
		"result": "custom math result",
	}}, nil
}

// ── EvidenceStore Plugin Example ────────────────────────────────────────
//
// loggingStore wraps an in-memory store with operation logging,
// demonstrating the plugin pattern: any backend can implement EvidenceStore.
type loggingStore struct {
	inner planner.EvidenceStore
	mu    sync.Mutex
	logs  []string
}

func (s *loggingStore) Save(ctx context.Context, ev *planner.ToolEvidence) error {
	s.mu.Lock()
	s.logs = append(s.logs, fmt.Sprintf("%s:%s success=%v", ev.ToolName, ev.CapabilityName, ev.Success))
	s.mu.Unlock()
	return s.inner.Save(ctx, ev)
}

func (s *loggingStore) Query(ctx context.Context, toolName, capaName string, limit int) ([]planner.ToolEvidence, error) {
	return s.inner.Query(ctx, toolName, capaName, limit)
}

func (s *loggingStore) Aggregate(ctx context.Context, toolName string) (map[string]planner.ToolScore, error) {
	return s.inner.Aggregate(ctx, toolName)
}

// init registers the logging store for demonstration.
func init() {
	_ = &loggingStore{inner: planner.NewMemoryEvidenceStore()}
}
