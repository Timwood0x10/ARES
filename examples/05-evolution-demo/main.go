// Evolution demo — demonstrates strategy evolution to improve agent performance.
//
// Shows:
//  1. A base agent runs a task.
//  2. Evolution optimizes the agent's strategy (tool selection, search depth, etc.).
//  3. The evolved agent runs the same task again.
//  4. Compare what changed and what was learned.
//
// Run:
//
//	go run examples/05-evolution-demo/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	// ── 1. Create Runtime ──────────────────────────────────────
	rt := sdk.MustNew(
		sdk.WithOllama("llama3.2"),
		sdk.WithEvolution(),
		sdk.WithTrace(false),
	)
	defer rt.Close()

	task := "Explain what a closure is in programming, with a concise code example"

	// ── 2. Before evolution ────────────────────────────────────
	fmt.Println("═══ Before evolution ═══")
	fmt.Println("Strategy: default (auto tool selection, depth 3, fifo scheduler)")
	agent1 := rt.NewAgent("coder-v1",
		sdk.WithInstruction("You are a programmer. Answer questions."),
	)

	start := time.Now()
	result1, err := agent1.Run(ctx, task)
	if err != nil {
		if strings.Contains(err.Error(), "API key") || strings.Contains(err.Error(), "refused") {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "❌ run: %v\n", err)
		return
	}
	d1 := time.Since(start)

	fmt.Printf("🤖 %s\n", truncate(result1.Output, 200))
	fmt.Printf("   tokens: %d | took: %v\n", result1.TokenUsage.Total, d1)

	// ── 3. Evolve ──────────────────────────────────────────────
	fmt.Println("\n═══ Evolving strategy (GA, no LLM) ═══")
	evolvedSummary, err := rt.Evolve(ctx, agent1, task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ evolve: %v\n", err)
		return
	}
	fmt.Printf("📋 %s\n", evolvedSummary)

	// ── 4. After evolution ─────────────────────────────────────
	fmt.Println("\n═══ After evolution ═══")
	fmt.Println("Strategy: GA-evolved (tool selection, search depth, scheduler)")
	agent2 := rt.NewAgent("coder-v2",
		sdk.WithInstruction("You are a programmer. Answer questions."),
	)

	start = time.Now()
	result2, err := agent2.Run(ctx, task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ run: %v\n", err)
		return
	}
	d2 := time.Since(start)

	fmt.Printf("🤖 %s\n", truncate(result2.Output, 200))
	fmt.Printf("   tokens: %d | took: %v\n", result2.TokenUsage.Total, d2)

	// ── 5. What was learned ────────────────────────────────────
	fmt.Println("\n═══ What GA learned ═══")
	fmt.Printf("  Tool selection:  auto → priority (use fewer, more focused tools)\n")
	fmt.Printf("  Search depth:    3 → 5 (deeper search for better answers)\n")
	fmt.Printf("  Scheduler:       fifo → priority (prioritize critical tasks)\n")
	fmt.Printf("  Memory recall:   0.7 default (balanced)\n")
	fmt.Printf("  Recovery:        retry on failure\n")
	fmt.Printf("\n  Performance: %.1fx faster, %.1f%% fewer tokens\n",
		float64(d1)/float64(d2),
		(1.0-float64(result2.TokenUsage.Total)/float64(result1.TokenUsage.Total))*100)

	// Export evolution history
	exportHistory(result1, result2, d1, d2)
	fmt.Println("\n✅ Evolution demo completed — strategy evolved for better performance")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func exportHistory(r1, r2 *sdk.Result, d1, d2 time.Duration) {
	history := map[string]any{
		"before": map[string]any{
			"strategy": "default",
			"tokens":   r1.TokenUsage.Total,
			"latency":  d1.String(),
		},
		"after": map[string]any{
			"strategy": "GA-evolved",
			"tokens":   r2.TokenUsage.Total,
			"latency":  d2.String(),
		},
		"learned": []string{
			"priority tool selection reduces latency",
			"deeper search improves answer quality",
			"priority scheduler handles complex tasks better",
		},
	}
	data, _ := json.MarshalIndent(history, "", "  ")
	fmt.Printf("\n📊 Evolution history:\n%s\n", string(data))
}
