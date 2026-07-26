// Example 12 — YAML-driven flags.
//
// Demonstrates the "one yaml + one go file starts an agent" philosophy.
// Every internal component (LLM, memory, distillation, database, embedding,
// knowledge) is configurable via ares.yaml; fields left at zero fall back
// to the component default.
//
// This is the reference example for the YAML config format.
// All other examples (01–11) follow the same pattern.
//
// Run:
//
//	go run examples/12-yaml-driven-flags/main.go
//
// Try editing ares.yaml to toggle memory.enable_distillation or
// distillation_threshold and observe the behaviour change.
//
// To use a different config file:
//
//	ARES_YAML=./my-config.yaml go run examples/12-yaml-driven-flags/main.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// ── 1. Load ares.yaml + wire everything ────────────────────
	cfg, err := sdk.LoadConfigFile("ares.yaml")
	if err != nil {
		return fmt.Errorf("load ares.yaml: %w", err)
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		return fmt.Errorf("config to options: %w", err)
	}
	rt := sdk.NewRuntime(opts...)
	defer rt.Close()

	// ── 2. Create Agent ─────────────────────────────────────────
	agent := rt.NewAgent("assistant",
		sdk.WithInstruction("You are a helpful assistant. Answer briefly."),
	)

	// ── 3. Run ──────────────────────────────────────────────────
	result, err := agent.Run(ctx, "In one short sentence, what is memory distillation?")
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}

	fmt.Printf("✅ %s\n", result.Output)
	fmt.Printf("   tokens: %d | took: %v\n",
		result.TokenUsage.Total, result.Duration)
	return nil
}
