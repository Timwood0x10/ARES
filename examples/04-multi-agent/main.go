// Multi-agent — demonstrates team-based leader/member orchestration with ARES.
//
// YAML-driven config: only agent definitions and team orchestration need Go code.
// The runtime, LLM, memory, distillation and evolution are all in ares.yaml.
//
// Run:
//
//	go run examples/04-multi-agent/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	// ── 1. Load ares.yaml + wire everything ────────────────────
	cfg, err := sdk.LoadConfigFile("ares.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ load config: %v\n", err)
		return
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ config: %v\n", err)
		return
	}
	rt := sdk.NewRuntime(opts...)
	defer rt.Close()

	// ── 2. Create team members ─────────────────────────────────
	leader := rt.NewAgent("coordinator",
		sdk.WithInstruction(`You are a team lead. You plan tasks, delegate to members, and synthesize results.
Be concise.`),
	)

	researcher := rt.NewAgent("researcher",
		sdk.WithInstruction(`You are a researcher. You find facts, analyze data, and provide insights.
Be factual and concise.`),
	)

	writer := rt.NewAgent("writer",
		sdk.WithInstruction(`You are a writer. You produce clear, well-structured content.
Be concise and engaging.`),
	)

	// ── 3. Create team ─────────────────────────────────────────
	team := rt.NewTeam("project-alpha", leader, []*sdk.Agent{researcher, writer})

	// ── 4. Run ─────────────────────────────────────────────────
	task := "Research and write a one-paragraph summary about the Go programming language"
	fmt.Printf("📋 Task: %s\n", task)

	result, err := team.Run(ctx, task)
	if err != nil {
		if strings.Contains(err.Error(), "API key") {
			fmt.Fprintf(os.Stderr, "❌ %v\n   → Set OPENAI_API_KEY or install Ollama\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "❌ team run: %v\n", err)
		return
	}

	fmt.Printf("\n📋 Plan:\n%s\n\n", result.Plan)
	fmt.Printf("📝 Result:\n%s\n", result.Output)
	fmt.Printf("\n   sub-results: %d | took: %v\n", len(result.SubResults), result.Duration)
}
