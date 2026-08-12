// Multi-agent — demonstrates team-based leader/member orchestration with ARES.
//
// Purpose:
//
//	Show how to create a team of specialised agents (leader, researcher,
//	writer), wire them together with NewTeam, and run a collaborative task
//	where the leader plans and delegates, members execute concurrently, and
//	the leader synthesises the final output.
//
// Learning objectives (what this example teaches you):
//   - How to create multiple agents with distinct system instructions.
//   - How to assemble a Team from a leader agent and a slice of member agents.
//   - How to call team.Run() and interpret the TeamResult (Plan, Output,
//     SubResults, Duration).
//   - How YAML-driven config keeps Go code minimal — only agent definitions
//     and team orchestration need Go code.
//
// Core APIs used (with package paths):
//   - sdk.LoadConfigFile             — github.com/Timwood0x10/ares/sdk
//   - (*cfg.ConfigFile).ToOptions()  — github.com/Timwood0x10/ares/sdk
//   - sdk.NewRuntime                 — github.com/Timwood0x10/ares/sdk
//   - rt.NewAgent                    — github.com/Timwood0x10/ares/sdk
//   - sdk.WithInstruction            — github.com/Timwood0x10/ares/sdk
//   - rt.NewTeam                     — github.com/Timwood0x10/ares/sdk
//   - team.Run                       — github.com/Timwood0x10/ares/sdk
//   - sdk.TeamResult (struct)        — github.com/Timwood0x10/ares/sdk
//
// Run:
//
//	go run examples/04-multi-agent/main.go
//
// Expected output (when an LLM backend is configured):
//
//	📋 Task: Research and write a one-paragraph summary about the Go programming language
//
//	📋 Plan:
//	<the leader's plan for delegating the task>
//
//	📝 Result:
//	<the synthesised final output from the leader>
//
//	   sub-results: 2 | took: <duration>
//
// If the run fails with an "API key" error, set OPENAI_API_KEY or install
// Ollama. Try adding a fourth member agent or changing the leader's
// instruction to alter delegation behaviour.
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

	// ── Step 1: Load ares.yaml and wire everything ──
	// LoadConfigFile reads the YAML config; ToOptions converts it to Runtime
	// options that auto-wire LLM, memory, distillation, AKG, and evolution.
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
	// NewRuntime builds the runtime; LLM and all subsystems are auto-wired.
	rt := sdk.NewRuntime(opts...)
	// defer Close releases connections and background resources.
	defer rt.Close()

	// ── Step 2: Create team members ──
	// Each agent gets a specialised system instruction via WithInstruction.
	// The leader plans and delegates; members (researcher, writer) execute.
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

	// ── Step 3: Create the team ──
	// NewTeam assembles a named team from a leader agent and a slice of member
	// agents. The leader orchestrates; members execute concurrently.
	team := rt.NewTeam("project-alpha", leader, []*sdk.Agent{researcher, writer})

	// ── Step 4: Run the collaborative task ──
	// team.Run executes a four-phase orchestration:
	//   Phase 1 — Leader discovers and plans (runs with tools).
	//   Phase 2 — Members execute concurrently with their assigned work.
	//   Phase 3 — Verifier checks results (if configured).
	//   Phase 4 — Leader synthesises the final output.
	// The returned TeamResult contains Plan, Output, SubResults, and Duration.
	task := "Research and write a one-paragraph summary about the Go programming language"
	fmt.Printf("📋 Task: %s\n", task)

	result, err := team.Run(ctx, task)
	if err != nil {
		// Provide a hint if the error is about a missing API key.
		if strings.Contains(err.Error(), "API key") {
			fmt.Fprintf(os.Stderr, "❌ %v\n   → Set OPENAI_API_KEY or install Ollama\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "❌ team run: %v\n", err)
		return
	}

	// Print the leader's plan, the synthesised output, and sub-result count.
	fmt.Printf("\n📋 Plan:\n%s\n\n", result.Plan)
	fmt.Printf("📝 Result:\n%s\n", result.Output)
	fmt.Printf("\n   sub-results: %d | took: %v\n", len(result.SubResults), result.Duration)
}
