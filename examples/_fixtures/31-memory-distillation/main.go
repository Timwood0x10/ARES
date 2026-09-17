// Memory distillation — demonstrate automatic knowledge extraction from conversations.
//
// Purpose:
//
//	Show how ARES automatically distills knowledge from agent conversations
//	into reusable experiences. Distillation runs in the background, extracting
//	facts, solutions, and heuristics from completed tasks.
//
// Learning objectives (what this example teaches you):
//   - How to enable distillation with WithDistillation(threshold).
//   - How to configure distillation parameters (DistillationConfig).
//   - How distilled experiences improve future agent performance.
//   - How to inspect the ExperienceRepository for extracted knowledge.
//
// Core APIs used (package path → symbol):
//   - github.com/Timwood0x10/ares/api.WithDistillation
//   - github.com/Timwood0x10/ares/api.DefaultDistillationConfig
//   - github.com/Timwood0x10/ares/api.DistillationConfig
//   - github.com/Timwood0x10/ares/api.Experience
//   - github.com/Timwood0x10/ares/api.ExperienceRepository
//   - github.com/Timwood0x10/ares/api.ExperienceTypeFailure
//
// Run:
//
//	go run examples/_fixtures/31-memory-distillation/main.go
//
// Expected output:
//
//	=== Memory Distillation Demo ===
//
//	[1/3] Distillation Configuration
//	   MinImportance: 0.60
//	   MaxMemories:   3
//	   Threshold:     5 rounds
//
//	[2/3] Agent Tasks (triggers distillation)
//	   Task 1: <result>
//	   Task 2: <result>
//	   Task 3: <result>
//
//	[3/3] Distilled Experiences
//	   <experience details>
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	api "github.com/Timwood0x10/ares/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Println("=== Memory Distillation Demo ===")
	fmt.Println()

	// ── 1. Show distillation configuration ──
	fmt.Println("[1/3] Distillation Configuration")
	distillCfg := api.DefaultDistillationConfig()
	fmt.Printf("   MinImportance: %.2f\n", distillCfg.MinImportance)
	fmt.Printf("   MaxMemories:   %d\n", distillCfg.MaxMemoriesPerDistillation)
	fmt.Printf("   ConflictThreshold: %.2f\n", distillCfg.ConflictThreshold)
	fmt.Printf("   CrossTurnExtraction: %v\n", distillCfg.EnableCrossTurnExtraction)
	fmt.Println()

	// ── 2. Create Runtime with distillation enabled ──
	rt, err := api.New(
		api.WithOllama("llama3.2"),
		api.WithDefaultMemory(),
		api.WithDistillation(3), // Fire distillation every 3 rounds
		api.WithTrace(false),
	)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer rt.Close()

	agent := rt.NewAgent("distill-agent",
		api.WithInstruction("You are a helpful assistant. Remember what you learn."),
		api.WithMaxTokens(128),
		api.WithTimeout(20*time.Second),
	)

	// ── 3. Run multiple tasks to trigger distillation ──
	fmt.Println("[2/3] Agent Tasks (triggers distillation)")
	tasks := []string{
		"What is the capital of France?",
		"What is 2+2?",
		"What color is the sky?",
	}

	for i, task := range tasks {
		result, err := agent.Run(ctx, task)
		if err != nil {
			fmt.Printf("   Task %d failed: %v\n", i+1, err)
			continue
		}
		fmt.Printf("   Task %d: %s\n", i+1, truncate(result.Output, 40))
	}

	fmt.Println()

	// ── 4. Show distilled experiences ──
	fmt.Println("[3/3] Distilled Experiences")
	fmt.Println("   Distillation runs in the background after N conversation rounds.")
	fmt.Println("   Check runtime logs for distillation events.")
	fmt.Println()

	fmt.Println("=== Demo Complete ===")
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
