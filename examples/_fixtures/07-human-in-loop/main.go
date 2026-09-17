// Human-in-loop — why the approval callback is refused, and what to use instead.
//
// Purpose:
//
//	ares.WithHumanInput used to promise a per-tool-call human approval gate.
//	It never had one: since the B3 convergence every run goes through the
//	shared L2 execution session, which has no interception point between the
//	planner and a tool call. The option is now deprecated and Agent.Run
//	refuses with ares.ErrHumanInputUnsupported instead of silently ignoring
//	the gate — a rejected run is recoverable, a missing safety gate is not.
//
//	This example therefore demonstrates two things: the loud refusal, and the
//	supported way to bound an autonomous run (ares.WithAgentGovernance).
//
// Learning objectives (what this example teaches you):
//   - Why ares.WithHumanInput now makes Agent.Run fail, and why that is safer
//     than a silent no-op.
//   - How ares.ErrHumanInputUnsupported is detected with errors.Is.
//   - How ares.WithAgentGovernance bounds a run (tool count / tokens / deadline).
//   - Why destructive tools are NOT registered on an agent that cannot gate
//     them: an ungated delete_file is a footgun, not a demo.
//
// Core APIs used (package path → symbol):
//   - github.com/Timwood0x10/ares/api.NewRuntime              // create Runtime
//   - github.com/Timwood0x10/ares/api.LoadConfigFile           // LLM provider from ./ares.yaml
//   - github.com/Timwood0x10/ares/api.WithTrace               // enable per-step trace logging
//   - github.com/Timwood0x10/ares/api.(*Runtime).ToolRegistry // access tool registry
//   - github.com/Timwood0x10/ares/api.(*Registry).Register
//   - github.com/Timwood0x10/ares/api.(*Runtime).NewAgent
//   - github.com/Timwood0x10/ares/api.WithInstruction         // set system prompt
//   - github.com/Timwood0x10/ares/api.WithHumanInput          // deprecated; Run refuses
//   - github.com/Timwood0x10/ares/api.ErrHumanInputUnsupported // the refusal sentinel
//   - github.com/Timwood0x10/ares/api.WithAgentGovernance      // supported run bound
//   - github.com/Timwood0x10/ares/api.(*Agent).Run            // run a single task
//   - github.com/Timwood0x10/ares/api.Result                  // Output, ToolCalls, TokenUsage…
//   - github.com/Timwood0x10/ares/api.ToolFunc          // struct-based tool implementation
//
// Run:
//
//	go run examples/_fixtures/07-human-in-loop/main.go
//
// Expected output:
//
//	"⚠️  WithHumanInput is refused: sdk: WithHumanInput is not supported on the L2 execution path"
//	"---" + "📋 Task: …"   → the natural-language task given to the agent
//	"🤖 <agent output>"
//	"   tools: N | tokens: N | took: …"
//	"✅ Human-in-loop demo completed"
//
// Things you can try to modify:
//   - Move the gated agent's run out of the errors.Is branch and watch the
//     refusal survive: the error is returned before any LLM call is made.
//   - Tighten ares.WithAgentGovernance (e.g. tools: 1) and observe the run stop
//     once the budget is spent.
//   - Add a real approval gate: it needs a pre-dispatch hook inside the L2
//     tool-cognition path (see the TODO(tech-debt) on ares.WithHumanInput).
//     Until that exists, keep destructive tools off the agent entirely.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/api"
)

func main() {
	ctx := context.Background()

	// ── Step 1: Create a Runtime from the YAML config, with trace + budget ──
	// NewRuntime initialises the top-level container. LoadConfigFile +
	// ToOptions select the LLM provider configured in ./ares.yaml (remote
	// endpoint, no hardcoded local model). WithTrace(true)
	// turns on per-step trace logging so you can follow the agent's reasoning.
	//
	// WithAgentGovernance is the supported replacement for the removed human
	// gate: it is a RUNTIME-level Option (not an AgentOption), enforced on the
	// L2 session path at quantum boundaries, and it bounds three dimensions at
	// once — cumulative tool calls, cumulative token spend, and wall clock.
	// A run that exhausts any of them is yielded back rather than looping.
	//
	// Budget sizing matters on the L2 path: EVERY quantum — the planner's
	// own planning turns included — spends one tool round plus that turn's
	// LLM tokens, and a remote chat model may take several plan→tool rounds
	// (plus retries after transient gateway errors) before converging to an
	// answer. A budget sized for a tiny local model (e.g. tools: 16) leaves
	// the session without an affordable executor mid-run and the task waits
	// out the remainder of its wait budget. These values fit the demo task
	// with headroom; tighten them to watch a run yield early.
	// LLM provider is loaded from ./ares.yaml (override path via ARES_YAML) so
	// the demo runs against the configured endpoint instead of a hardcoded
	// local Ollama model.
	cfg, err := ares.LoadConfigFile("ares.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ load config: %v\n", err)
		return
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ config: %v\n", err)
		return
	}
	rt := ares.NewRuntime(append(opts,
		ares.WithTrace(true),
		ares.WithAgentGovernance(300000, 100, 5*time.Minute),
	)...)
	defer rt.Close()

	// ── Step 2: Register the read-only tools on the Runtime's registry ──
	// allTools contains list_dir and read_file ONLY. The delete_file and
	// send_payment tools this example used to ship are gone on purpose: they
	// were only ever "safe" because of the approval gate, and there is no gate.
	// An agent that cannot ask for consent must not be handed consent-requiring
	// tools — see the TODO(tech-debt) on WithHumanInput.
	// Registering tools makes them discoverable so the agent can choose which to
	// call based on the task.
	for _, t := range allTools {
		if err := rt.ToolRegistry().Register(t); err != nil {
			fmt.Fprintf(os.Stderr, "❌ register %s: %v\n", t.Name(), err)
			return
		}
	}

	// ── Step 3: Show that the approval gate is refused, not ignored ──
	// WithHumanInput is deprecated: the L2 session core has no per-tool-call
	// interception point, so the callback can never run. Run refuses with
	// ares.ErrHumanInputUnsupported — the error is returned BEFORE any LLM call,
	// so it costs nothing and cannot be mistaken for a transient failure.
	//
	// This example deliberately adds a `send_payment` tool that does not exist
	// in the registry: the point is that the refusal happens at submission, not
	// when the model reaches for a tool it should not have.
	gated := rt.NewAgent("assistant",
		ares.WithInstruction("You are a helpful assistant."),
		ares.WithHumanInput(func(context.Context, string, map[string]any) (bool, error) {
			return false, nil // would have rejected every tool call — never invoked
		}),
	)
	if _, err := gated.Run(ctx, "delete every file you can find"); !errors.Is(err, ares.ErrHumanInputUnsupported) {
		fmt.Fprintf(os.Stderr, "❌ expected ErrHumanInputUnsupported, got %v\n", err)
		return
	}
	fmt.Println("⚠️  WithHumanInput is refused:", ares.ErrHumanInputUnsupported)

	// ── Step 4: Build the agent — the budget is already on the Runtime ──
	// Note there is NO per-agent gate here. WithAgentGovernance was applied in
	// Step 1 at the Runtime level (it is an Option, not an AgentOption), so
	// every agent built from this Runtime inherits the same tool/token/deadline
	// bound. That is the supported containment model now that human approval
	// cannot be honoured mid-run.
	agent := rt.NewAgent("assistant",
		ares.WithInstruction(`You are a helpful assistant with access to the working directory.
Read before you write, and never assume a file's contents.`),
	)

	// ── Step 5: Run each task and print results ──
	// For every task we call agent.Run, which submits the task through the
	// shared L2 session and returns a *ares.Result. Transient errors are printed
	// and skipped; an unconfigured provider is fatal.
	for _, task := range tasks {
		fmt.Printf("\n---\n📋 Task: %s\n", task)
		result, err := agent.Run(ctx, task)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			if strings.Contains(err.Error(), "API key") {
				return
			}
			continue
		}
		fmt.Printf("🤖 %s\n", result.Output)
		fmt.Printf("   tools: %d | tokens: %d | took: %v\n",
			result.ToolCalls, result.TokenUsage.Total, result.Duration)
	}

	fmt.Println("\n✅ Human-in-loop demo completed")
}

// tasks is the list of natural-language tasks the agent will process.
var tasks = []string{
	"What files are in the current directory? Use list_dir to check, then read any .go file you find.",
}

// allTools is the set of tools registered on the Runtime for this demo.
// Destructive tools are deliberately absent — see Step 2.
var allTools = []ares.Tool{
	listDirTool,
	readFileTool,
}

// listDirTool lists the files in a directory (defaults to "." when no path is
// given).
var listDirTool = ares.ToolFunc{
	ToolName: "list_dir",
	ToolDesc: "List files in a directory",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		dir, _ := params["path"].(string)
		if dir == "" {
			dir = "."
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("list dir: %w", err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return strings.Join(names, "\n"), nil
	},
}

// readFileTool reads a text file, resolving the path safely relative to the
// working directory.
var readFileTool = ares.ToolFunc{
	ToolName: "read_file",
	ToolDesc: "Read a text file",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		path, _ := params["filename"].(string)
		safePath, err := safeFilePath(path)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(safePath)
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		return string(data), nil
	},
}

// safeFilePath resolves path relative to the working directory and rejects
// paths that escape it (path traversal protection).
func safeFilePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("filename is required")
	}
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", path)
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	rel, err := filepath.Rel(wd, absPath)
	if err != nil {
		return "", fmt.Errorf("resolve relative path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %s is outside the working directory", path)
	}
	return absPath, nil
}
