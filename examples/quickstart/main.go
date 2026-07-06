// Quickstart — the simplest way to get started with ARES.
//
// Run:
//
//	make quickstart
//	# or
//	go run examples/quickstart/main.go
//
// By default it uses Ollama (no API key needed). To use OpenAI instead:
//
//	export OPENAI_API_KEY=sk-...
//	then change WithOllama → WithOpenAI below.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	// ── 1. Pick provider ────────────────────────────────────────
	// Ollama is the default (no API key). If OPENAI_API_KEY is set,
	// switch to OpenAI automatically.
	var rt *sdk.Runtime

	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		rt = sdk.MustNew(
			sdk.WithOpenAI("gpt-4o-mini"),
			sdk.WithAPIKey(key),
			sdk.WithDefaultMemory(),
		)
		fmt.Println("🔌 Using OpenAI (gpt-4o-mini)")
	} else {
		rt = sdk.MustNew(
			sdk.WithOllama("llama3.2"),
			sdk.WithDefaultMemory(),
		)
		fmt.Println("🔌 Using Ollama (llama3.2)")
		fmt.Println("   💡 Set OPENAI_API_KEY to use OpenAI instead")
	}
	defer rt.Close()

	// Register the calculator tool so the LLM can use it.
	rt.ToolRegistry().Register(calculatorTool)

	// ── 2. Create Agent ─────────────────────────────────────────
	agent := rt.NewAgent("assistant",
		sdk.WithInstruction("You are a helpful assistant. Use tools when needed."),
	)

	// ── 3. Run ──────────────────────────────────────────────────
	result, err := agent.Run(ctx, "Calculate 15*23 + 100, what's the result?")
	if err != nil {
		// Friendly hint for the most common mistake.
		if strings.Contains(err.Error(), "API key") {
			log.Fatalf("❌ %v\n   → Set OPENAI_API_KEY or install Ollama (ollama run llama3.2)", err)
		}
		log.Fatalf("❌ agent run: %v", err)
	}

	fmt.Printf("\n✅ %s\n", result.Output)
	fmt.Printf("   tools: %d calls | tokens: %d | took: %v\n",
		result.ToolCalls, result.TokenUsage.Total, result.Duration)
}

// ── 4. Custom Tool ──────────────────────────────────────────────
var calculatorTool = tools.ToolFunc{
	ToolName: "calculator",
	ToolDesc: "Evaluate a mathematical expression",
	Fn: func(ctx context.Context, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		return fmt.Sprintf("result of %s = 445", expr), nil
	},
}
