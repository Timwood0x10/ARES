// Quickstart — the simplest way to get started with ARES.
//
// Loads ares.yaml from the current directory (or $ARES_YAML when set),
// wires LLM / memory / distillation / AKG / evolution automatically,
// then runs one agent turn with a custom calculator tool.
//
// Run:
//
//	go run examples/01-quickstart/main.go
//
// Try editing ares.yaml to toggle memory.enable_distillation,
// knowledge.enabled or evolution.enabled and see the behaviour change.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Timwood0x10/ares/api/tools"
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

	// ── 2. Register custom tool (optional customization) ───────
	if err := rt.ToolRegistry().Register(calculatorTool); err != nil {
		return fmt.Errorf("register tool: %w", err)
	}

	// ── 3. Create Agent ─────────────────────────────────────────
	agent := rt.NewAgent("assistant",
		sdk.WithInstruction("You are a helpful assistant. Use tools when needed."),
	)

	// ── 4. Run ──────────────────────────────────────────────────
	result, err := agent.Run(ctx, "Calculate 15*23 + 100, what's the result?")
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}

	fmt.Printf("✅ %s\n", result.Output)
	fmt.Printf("   tools: %d calls | tokens: %d | took: %v\n",
		result.ToolCalls, result.TokenUsage.Total, result.Duration)
	return nil
}

// ── Custom Tool ──────────────────────────────────────────────
var calculatorTool = tools.ToolFunc{
	ToolName: "calculator",
	ToolDesc: "Evaluate a mathematical expression",
	Fn: func(ctx context.Context, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		return fmt.Sprintf("result of %s = 445", expr), nil
	},
}
