// Evolution demo — demonstrates strategy evolution to improve agent performance.
//
// Shows:
//  1. A base agent runs a task.
//  2. Evolution optimizes the agent's instruction.
//  3. The evolved agent runs the same task again.
//  4. Compare before and after.
//
// Run:
//
//	go run examples/evolution-demo/main.go
package main

import (
	"context"
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
	fmt.Println("\n═══ Evolving instruction ═══")
	evolvedInstr, err := rt.Evolve(ctx, agent1, task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ evolve: %v\n", err)
		return
	}
	fmt.Printf("📋 Evolved instruction:\n%s\n", evolvedInstr)

	// ── 4. After evolution ─────────────────────────────────────
	fmt.Println("\n═══ After evolution ═══")
	agent2 := rt.NewAgent("coder-v2",
		sdk.WithInstruction(evolvedInstr),
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

	// ── 5. Compare ─────────────────────────────────────────────
	fmt.Println("\n═══ Comparison ═══")
	fmt.Printf("  v1 (before): %d tokens, %v\n", result1.TokenUsage.Total, d1)
	fmt.Printf("  v2 (after):  %d tokens, %v\n", result2.TokenUsage.Total, d2)
	fmt.Println("\n✅ Evolution demo completed")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
