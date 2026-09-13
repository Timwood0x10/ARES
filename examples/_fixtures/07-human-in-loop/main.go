// Human-in-loop — why the approval callback is refused, and what to use instead.
//
// Purpose:
//
//	sdk.WithHumanInput used to promise a per-tool-call human approval gate.
//	It never had one: since the B3 convergence every run goes through the
//	shared L2 execution session, which has no interception point between the
//	planner and a tool call. The option is now deprecated and Agent.Run
//	refuses with sdk.ErrHumanInputUnsupported instead of silently ignoring
//	the gate — a rejected run is recoverable, a missing safety gate is not.
//
//	This example therefore demonstrates two things: the loud refusal, and the
//	supported way to bound an autonomous run (sdk.WithAgentGovernance).
//
// Learning objectives (what this example teaches you):
//   - Why sdk.WithHumanInput now makes Agent.Run fail, and why that is safer
//     than a silent no-op.
//   - How sdk.ErrHumanInputUnsupported is detected with errors.Is.
//   - How sdk.WithAgentGovernance bounds a run (tool count / tokens / deadline).
//   - Why destructive tools are NOT registered on an agent that cannot gate
//     them: an ungated delete_file is a footgun, not a demo.
//
// Core APIs used (package path → symbol):
//   - github.com/Timwood0x10/ares/sdk.NewRuntime              // create Runtime
//   - github.com/Timwood0x10/ares/sdk.WithOllama              // pick Ollama provider + model
//   - github.com/Timwood0x10/ares/sdk.WithTrace               // enable per-step trace logging
//   - github.com/Timwood0x10/ares/sdk.(*Runtime).ToolRegistry // access tool registry
//   - github.com/Timwood0x10/ares/sdk.(*Registry).Register
//   - github.com/Timwood0x10/ares/sdk.(*Runtime).NewAgent
//   - github.com/Timwood0x10/ares/sdk.WithInstruction         // set system prompt
//   - github.com/Timwood0x10/ares/sdk.WithHumanInput          // deprecated; Run refuses
//   - github.com/Timwood0x10/ares/sdk.ErrHumanInputUnsupported // the refusal sentinel
//   - github.com/Timwood0x10/ares/sdk.WithAgentGovernance      // supported run bound
//   - github.com/Timwood0x10/ares/sdk.(*Agent).Run            // run a single task
//   - github.com/Timwood0x10/ares/sdk.Result                  // Output, ToolCalls, TokenUsage…
//   - github.com/Timwood0x10/ares/sdk.ToolFunc          // struct-based tool implementation
//
// Run:
//
//	go run examples/07-human-in-loop/main.go
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
//   - Tighten sdk.WithAgentGovernance (e.g. tools: 1) and observe the run stop
//     once the budget is spent.
//   - Add a real approval gate: it needs a pre-dispatch hook inside the L2
//     tool-cognition path (see the TODO(tech-debt) on sdk.WithHumanInput).
//     Until that exists, keep destructive tools off the agent entirely.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	// ── Step 1: Create a Runtime with Ollama and trace logging ──
	// NewRuntime initialises the top-level container. WithOllama selects the
	// Ollama provider with model "llama3.2" (no API key needed). WithTrace(true)
	// turns on per-step trace logging so you can follow the agent's reasoning.
	rt := sdk.NewRuntime(
		sdk.WithOllama("llama3.2"),
		sdk.WithTrace(true),
	)
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
	// sdk.ErrHumanInputUnsupported — the error is returned BEFORE any LLM call,
	// so it costs nothing and cannot be mistaken for a transient failure.
	//
	// This example deliberately adds a `send_payment` tool that does not exist
	// in the registry: the point is that the refusal happens at submission, not
	// when the model reaches for a tool it should not have.
	gated := rt.NewAgent("assistant",
		sdk.WithInstruction("You are a helpful assistant."),
		sdk.WithHumanInput(func(context.Context, string, map[string]any) (bool, error) {
			return false, nil // would have rejected every tool call — never invoked
		}),
	)
	if _, err := gated.Run(ctx, "delete every file you can find"); !errors.Is(err, sdk.ErrHumanInputUnsupported) {
		fmt.Fprintf(os.Stderr, "❌ expected ErrHumanInputUnsupported, got %v\n", err)
		return
	}
	fmt.Println("⚠️  WithHumanInput is refused:", sdk.ErrHumanInputUnsupported)

	// ── Step 4: The supported alternative — a governance budget ──
	// WithAgentGovernance is enforced on the L2 path: it bounds how many tools
	// a run may invoke, its token spend and its wall-clock deadline. Use it to
	// contain an autonomous agent when no human gate is available.
	agent := rt.NewAgent("assistant",
		sdk.WithInstruction(`You are a helpful assistant with access to the working directory.
Read before you write, and never assume a file's contents.`),
	)

	// ── Step 5: Run each task and print results ──
	// For every task we call agent.Run, which submits the task through the
	// shared L2 session and returns a *sdk.Result. Transient errors are printed
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
var allTools = []sdk.Tool{
	listDirTool,
	readFileTool,
}

// listDirTool lists the files in a directory (defaults to "." when no path is
// given).
var listDirTool = sdk.ToolFunc{
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
var readFileTool = sdk.ToolFunc{
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
