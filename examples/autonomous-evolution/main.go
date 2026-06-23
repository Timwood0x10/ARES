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

	// Check if evolution is enabled via project-level config
	evoEnabled := false
	var evoCfg GACfg

	projectCfg, err := loadProjectEvolutionConfig()
	if err != nil {
		slog.Info("evolution: no project config found, using defaults (OFF)")
	} else if projectCfg.Enabled {
		evoEnabled = true
		evoCfg = mergeGACfg(cfgGA, projectCfg)
		slog.Info("evolution: enabled via project config")
	}

	fmt.Printf("   %s  6: Multi-Gen GA Evolution\n", statusStr(evoEnabled))
	fmt.Printf("   %s  7: Wired Evolution System\n", statusStr(evoEnabled))

	kit, err := NewDemoKit()
	if err != nil {
		slog.Error("Failed to initialize demo kit", "error", err)
		return
	}

	// Run scenarios 1-5 (always on)
	runBandit(ctx, kit)
	runCallbacks(ctx, kit)
	runMutation(ctx, kit)
	runArena(ctx, kit)
	runDreamCycle(ctx, kit)

	// Run scenarios 6-7 only if evolution is enabled.
	// LLM scorer (if configured) is initialized inside runMultiGenGA.
	if evoEnabled {
		runMultiGenGA(ctx, kit, evoCfg)
		runMultiGenGA(ctx, kit, mergeGACfg(cfgWired, projectCfg))
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
