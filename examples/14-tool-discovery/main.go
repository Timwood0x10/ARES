// Tool discovery — MCP-style proactive discovery + the discover_tools meta-tool.
//
// Builds a MultiSource (runtime registry + one custom static tool), exercises
// discover_tools directly (deterministic, no LLM needed), then runs one agent
// turn with WithToolDiscovery so the LLM can search the tool pool at runtime.
//
// Run:
//
//	go run examples/14-tool-discovery/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Timwood0x10/ares/api/tools"
	rescore "github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/Timwood0x10/ares/internal/tools/toolsource"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── 1. Load ares.yaml; fall back to SDK defaults if missing so the
	// discovery demo still runs without a YAML on disk. ──
	var rtOpts []sdk.Option
	if cfg, err := sdk.LoadConfigFile("ares.yaml"); err == nil {
		if rtOpts, err = cfg.ToOptions(); err != nil {
			return fmt.Errorf("config to options: %w", err)
		}
	} else {
		fmt.Println("(ares.yaml not found; using SDK defaults)")
	}
	rt := sdk.NewRuntime(rtOpts...)
	defer rt.Close()

	// ── 2. Register custom tools so discover_tools has something to find ──
	for _, t := range registryTools {
		if err := rt.ToolRegistry().Register(t); err != nil {
			return fmt.Errorf("register %s: %w", t.Name(), err)
		}
	}

	// ── 3. Build a MultiSource: registry tools + one extra static tool ──
	coreReg, err := rt.ToolRegistry().CoreRegistry()
	if err != nil {
		return fmt.Errorf("core registry: %w", err)
	}
	src := toolsource.NewMultiSource(
		toolsource.NewRegistrySource(coreReg),
		toolsource.NewStaticSource([]rescore.Tool{reverseTool{}}),
	)

	// ── 4. Demonstrate discover_tools directly (no LLM required) ──
	fmt.Println("=== discover_tools (direct, no LLM) ===")
	meta := toolsource.NewDiscoverToolsTool(src)
	for _, q := range []string{"translate", "reverse"} {
		res, _ := meta.Execute(ctx, map[string]any{"query": q}) // best-effort demo
		fmt.Printf("  query=%q -> %s\n", q, res.Data)
	}

	// ── 5. Agent with discovery + one Run (LLM may fail; demo above succeeded) ──
	agent := rt.NewAgent("assistant",
		sdk.WithToolDiscovery(),
		sdk.WithToolSource(src),
		sdk.WithMaxIterations(3),
		sdk.WithInstruction("You are a helpful assistant. Use discover_tools to find tools."),
	)
	fmt.Println("\n=== agent.Run (LLM) ===")
	result, err := agent.Run(ctx, "Translate 'hello' to French.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ agent run: %v (discovery demo above succeeded)\n", err)
		return nil
	}
	fmt.Printf("🤖 %s\n", result.Output)
	return nil
}

// registryTools are registered in the runtime registry so discover_tools can
// search them by name/description.
var registryTools = []tools.Tool{
	tools.ToolFunc{ToolName: "translate", ToolDesc: "Translate text between languages",
		Fn: func(_ context.Context, p map[string]any) (any, error) {
			return fmt.Sprintf("[%s] %s", p["lang"], p["text"]), nil
		}},
	tools.ToolFunc{ToolName: "calculator", ToolDesc: "Evaluate a mathematical expression",
		Fn: func(_ context.Context, p map[string]any) (any, error) {
			return fmt.Sprintf("= %s", p["expression"]), nil
		}},
}

// reverseTool is a custom static tool (NOT in the runtime registry) showing
// MultiSource merging a source outside the registry. Execute reverses the
// input text rune-wise (real deterministic transform).
type reverseTool struct{}

func (reverseTool) Name() string                       { return "reverse_text" }
func (reverseTool) Description() string                { return "Reverse the characters of a string" }
func (reverseTool) Category() rescore.ToolCategory     { return rescore.CategoryCore }
func (reverseTool) Capabilities() []rescore.Capability { return nil }
func (reverseTool) Parameters() *rescore.ParameterSchema {
	return &rescore.ParameterSchema{
		Type: "object",
		Properties: map[string]*rescore.Parameter{
			"text": {Type: "string", Description: "text to reverse"},
		},
		Required: []string{"text"},
	}
}

func (reverseTool) Execute(_ context.Context, p map[string]interface{}) (rescore.Result, error) {
	text, _ := p["text"].(string)
	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return rescore.NewResult(true, string(runes)), nil
}
