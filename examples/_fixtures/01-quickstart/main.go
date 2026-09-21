// Quickstart — the golden path: one interface, the whole AgentOS kernel.
//
// Purpose:
//
//	Show the minimal external flow. ARES has two faces sharing ONE execution
//	kernel (scheduler / MutableDAG / GA / memory all invisible behind it),
//	and ALL configuration comes from ares.yaml — the single config entry
//	point; there are no config flags.
//
//	Face 1 — in-process (this example, zero HTTP):
//		rt := api.MustNew()                  // reads ./ares.yaml
//		agent := rt.NewAgent("assistant", api.WithInstruction("..."))
//		result, _ := agent.Run(ctx, "your task")
//
//	Face 2 — remote (ares serve, for non-Go callers / other hosts):
//		ares init myproj && cd myproj && ares serve
//		# ares init generates security.api_key into ares.yaml — required:
//		# with no credential layer the write gate answers 401 (loopback too)
//		curl -X POST localhost:8080/api/tasks \
//		  -H "Authorization: Bearer <security.api_key from ares.yaml>" \
//		  -d '{"query":"your task"}'        # capability defaults from yaml
//		curl localhost:8080/api/tasks/<task_id>   # result = session answer
//		# POST ?wait=60s blocks until the result resolves (cap 300s), else
//		# 202 + poll (timeout body carries the current state)
//
//	Or one command, same yaml, human output, zero flags:
//		ares run -c ares.yaml "your task"
//
// Learning objectives (what this example teaches you):
//   - ares.yaml is the ONLY config entry point (LLM, memory, kernel, defaults).
//   - api.MustNew / api.NewRuntime assemble the full kernel from that one file.
//   - Custom tools register in-process via rt.ToolRegistry().
//   - agent.Run executes one L2 session through the SAME scheduler serve uses.
//
// Core APIs used (with package paths):
//   - ares.LoadConfigFile             — github.com/Timwood0x10/ares/api
//   - (*cfg.ConfigFile).ToOptions()  — github.com/Timwood0x10/ares/api
//   - ares.NewRuntime                 — github.com/Timwood0x10/ares/api
//   - rt.ToolRegistry().Register     — github.com/Timwood0x10/ares/api
//   - rt.NewAgent / ares.WithInstruction / agent.Run — github.com/Timwood0x10/ares/api
//   - ares.ToolFunc                  — github.com/Timwood0x10/ares/api
//
// Run:
//
//	go run examples/_fixtures/01-quickstart/main.go
//
// Expected output (when an LLM backend is configured in ares.yaml):
//
//	✅ <the assistant's text answer, e.g. "result of 15*23 + 100 = 445">
//	   tools: 1 calls | tokens: <n> | took: <duration>
//
// If no API key / Ollama is available the run will fail with an LLM error.
//
// Try editing ares.yaml to toggle memory.enable_distillation,
// server.default_capability or evolution.enabled and see the behaviour
// change — every knob lives in that one file.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Timwood0x10/ares/api"
)

// main is the entry point; it delegates to run() so that error handling
// stays linear and clean.
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

// run contains the whole example so that error returns stay simple.
func run() error {
	// A root context controls the Run lifecycle and supports cancellation.
	ctx := context.Background()

	// ── Step 1: Load ares.yaml and assemble Runtime options ──
	// LoadConfigFile reads and parses the YAML config file, returning *ConfigFile.
	// Passing "ares.yaml" makes the Runtime look for it in the current directory
	// (the $ARES_YAML env var can also specify a path).
	cfg, err := ares.LoadConfigFile("ares.yaml")
	if err != nil {
		return fmt.Errorf("load ares.yaml: %w", err)
	}
	// ToOptions converts the ConfigFile into a slice of Runtime Option values,
	// covering every subsystem: LLM backend, memory, distillation, AKG, evolution.
	opts, err := cfg.ToOptions()
	if err != nil {
		return fmt.Errorf("config to options: %w", err)
	}
	// NewRuntime builds the runtime from the options; LLM, memory, AKG and
	// evolution are all auto-wired — no manual assembly required.
	rt := ares.NewRuntime(opts...)
	// defer Close releases the connections and background resources held by Runtime.
	defer rt.Close()

	// ── Step 2: Register a custom tool (optional customisation point) ──
	// Most projects only need to register custom tools in Go; everything else
	// is driven by YAML.
	// ToolRegistry() returns the global tool registry; Register adds a ares.Tool.
	if err := rt.ToolRegistry().Register(calculatorTool); err != nil {
		return fmt.Errorf("register tool: %w", err)
	}

	// ── Step 3: Create an Agent ──
	// NewAgent creates a named Agent on the current Runtime.
	// WithInstruction sets the system prompt (prepended to the conversation).
	agent := rt.NewAgent("assistant",
		ares.WithInstruction("You are a helpful assistant. Use tools when needed."),
	)

	// ── Step 4: Run one conversational turn ──
	// Run executes one L2 session: the submission is admitted as a session,
	// the planner cognition grows the session graph (tool nodes dispatch
	// through the shared tool binder), and the terminal answer is returned.
	// The argument is the user's natural-language input; the return is *Result
	// containing output text, tool-call count, token usage, and duration.
	result, err := agent.Run(ctx, "Calculate 15*23 + 100, what's the result?")
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}

	// Print the final answer and statistics: tool calls, total tokens, duration.
	fmt.Printf("✅ %s\n", result.Output)
	fmt.Printf("   tools: %d calls | tokens: %d | took: %v\n",
		result.ToolCalls, result.TokenUsage.Total, result.Duration)
	return nil
}

// ── Custom Tool ──────────────────────────────────────────────
// calculatorTool is a demo "calculator" tool. It implements ares.Tool via
// the ares.ToolFunc convenience struct:
//   - ToolName: the tool name the LLM sees to decide when to call it.
//   - ToolDesc: a description helping the LLM understand the tool's purpose.
//   - Fn:       the actual function, receiving context and params (map[string]any).
//
// For simplicity Fn returns a hard-coded result string and does no real math.
var calculatorTool = ares.ToolFunc{
	ToolName: "calculator",
	ToolDesc: "Evaluate a mathematical expression",
	Fn: func(ctx context.Context, params map[string]any) (any, error) {
		// Extract the "expression" field from the params map (the LLM generates these).
		expr, _ := params["expression"].(string)
		// Return a string as the tool result; the LLM incorporates it into subsequent reasoning.
		return fmt.Sprintf("result of %s = 445", expr), nil
	},
}
