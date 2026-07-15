// Example 12 — YAML-driven flags.
//
// Demonstrates the "one yaml + one go file starts an agent" philosophy.
// Every internal component (LLM, memory, distillation, database, embedding,
// knowledge) is configurable via ares.yaml; fields left at zero fall back
// to the component default.
//
// Run:
//
//	make yaml-flags
//	# or
//	go run examples/12-yaml-driven-flags/main.go
//
// The example loads ares.yaml from the current directory, converts it to
// sdk Options, and runs one agent turn. Try editing ares.yaml to toggle
// memory.enable_distillation or distillation_threshold and observe the
// behaviour change.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

	yamlPath := defaultYAMLPath()
	cfg, err := sdk.LoadConfigFile(yamlPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", yamlPath, err)
	}

	opts, err := cfg.ToOptions()
	if err != nil {
		return fmt.Errorf("config to options: %w", err)
	}

	rt := sdk.MustNew(opts...)
	defer rt.Close()

	fmt.Printf("🔌 Loaded %s — LLM provider=%s model=%s memory=%v distillation=%v\n",
		yamlPath, cfg.LLM.Provider, cfg.LLM.Model,
		cfg.Memory.Enabled, cfg.Memory.EnableDistillation)

	agent := rt.NewAgent("assistant",
		sdk.WithInstruction("You are a helpful assistant. Answer briefly."),
	)

	result, err := agent.Run(ctx, "In one short sentence, what is memory distillation?")
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}

	fmt.Printf("\n✅ %s\n", result.Output)
	fmt.Printf("   tokens: %d | took: %v\n",
		result.TokenUsage.Total, result.Duration)
	return nil
}

// defaultYAMLPath returns the example directory's ares.yaml, allowing
// callers to override the path via the ARES_YAML env var when experimenting.
//
// Returns:
//
//	string - resolved YAML path, either ARES_YAML env value or ./ares.yaml.
func defaultYAMLPath() string {
	if p := os.Getenv("ARES_YAML"); p != "" {
		return p
	}
	return filepath.Join(".", "ares.yaml")
}
