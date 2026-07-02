// Package main demonstrates ares Autonomous Evolution (Dream Mode v1) workflow.
// Showcases 7 core capabilities using mock implementations — no external services required.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

func setupLogger() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
}

func printHeader() {
	fmt.Println(`╔════════════════════════════════════════════════════╗`)
	fmt.Println(`║  ares Autonomous Evolution (Dream Mode v1) Demo ║`)
	fmt.Println(`╚════════════════════════════════════════════════════╝`)
}

func printFooter() {
	fmt.Println("\n✅ Demo Complete — all scenarios finished.")
}

func main() {
	ctx := context.Background()
	setupLogger()

	// Print header
	printHeader()

	// Scenario execution table — always show what's running
	fmt.Println("\n📋 Scenario Configuration:")
	fmt.Println("   ✓ ON   1-5: Core scenarios (Bandit, Callbacks, Mutation, Arena, Dream)")
	fmt.Println("   ✓ ON   Selection strategies: rank, SUS, roulette, tournament, truncation")
	fmt.Println("   ✓ ON   8: Real Data Evolution Pipeline (440 records, 5 strategies)")

	// Check if evolution is enabled via project-level config
	evoEnabled := false
	var evoCfg GACfg

	projectCfg, err := loadProjectEvolutionConfig()
	if err != nil {
		log.Info("evolution: no project config found, using defaults (OFF)")
	} else if projectCfg.Enabled {
		evoEnabled = true
		evoCfg = mergeGACfg(cfgGA, projectCfg)
		log.Info("evolution: enabled via project config")
	}

	fmt.Printf("   %s  6: Multi-Gen GA Evolution\n", statusStr(evoEnabled))
	fmt.Printf("   %s  7: Wired Evolution System\n", statusStr(evoEnabled))

	kit, err := NewDemoKit()
	if err != nil {
		log.Error("Failed to initialize demo kit", "error", err)
		return
	}

	// Run scenarios 1-5 (always on)
	runBandit(ctx, kit)
	runCallbacks(ctx, kit)
	runMutation(ctx, kit)
	runArena(ctx, kit)
	runDreamCycle(ctx, kit)

	// Scenario 8: Real Data Evolution Pipeline (always on, no LLM required).
	// Uses ~440 realistic tool call records from 15 conversation scenarios
	// to demonstrate the complete GA/Memory/Tool fusion pipeline end-to-end.
	runRealDataEvolution(ctx, kit, cfgWired)

	// Run control group comparison if evolution is enabled.
	// Scenario 6: Pure autonomous evolution (deterministic scoring, no LLM)
	// Scenario 7: LLM-guided evolution (LLM scoring in the loop)
	// Both use identical GA configurations — the only difference is the scorer.
	if evoEnabled {
		fmt.Println("\n  🧪 Control Group Experiment")
		fmt.Println("  ────────────────────────────────────────────────")
		fmt.Println("  A: Pure Autonomous — GA + DeterministicScore (no LLM)")
		fmt.Println("  B: LLM-Guided     — GA + LLMScorer (with deterministic fallback)")
		fmt.Println("  Same GA config, same seed — only the scorer differs.")
		fmt.Println("  ────────────────────────────────────────────────")
		fmt.Println()

		// Scenario 6: Pure autonomous (no LLM).
		resultA := runScenario6(ctx, kit, evoCfg)

		// Scenario 7: LLM-guided (LLM in the loop).
		resultB := runScenario7(ctx, kit, mergeGACfg(cfgWired, projectCfg))

		// Compare the two approaches.
		if resultA != nil && resultB != nil {
			compareResults(resultA, resultB,
				"Autonomous (no LLM)", "LLM-Guided")
		}
	} else {
		printInsight("Evolution Disabled", `
  🔒 The Genetic Algorithm evolution scenarios (6 & 7) are currently DISABLED.

  To enable them, add the following to your project's config.yaml:

    evolution:
      enabled: true
      population_size: 20
      elite_count: 2
      generations: 15

  Or set the environment variable: EVOLUTION_ENABLED=true

  Scenarios 1-5 demonstrate the individual building blocks (bandit feedback,
  callback events, strategy mutation, arena regression, and dream cycle) that
  compose into the full evolution pipeline. These always run regardless.`)
	}

	printFooter()
}
