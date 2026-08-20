// Runtime scheduling demo — submit a real task, let the runtime schedule it,
// and watch the agent team split the work (leader plans → members execute →
// leader synthesises). This is the "Agent as Thread" showcase: the runtime
// owns dispatch, agents are disposable execution threads.
//
// LEGACY COMPATIBILITY: This example uses the Leader/Sub team orchestration
// model. ARES has evolved into a Peer Agent operating system (aresos-plan.md
// §1.1) where all agents are equal peers — no Leader/Worker hierarchy. The
// Leader/Sub path is retained as kernel.policy=legacy for compatibility. For
// the current model, see examples/aresos-demo.
//
// Purpose:
//
//	Show the full loop for a code-module analysis task: a leader agent plans
//	and splits the work, specialised members read the module files (via a
//	sandboxed read_file / list_files tool) and analyse their slice, and the
//	leader synthesises the final summary. Every phase is printed so you can
//	see the runtime schedule each agent's turn.
//
// Learning objectives:
//   - How sdk.LoadConfigFile + ToOptions wire the LLM/runtime from ares.yaml.
//   - How rt.ToolRegistry().Register adds a sandboxed read_file tool the
//     agents can actually call (the runtime dispatches the tool call to the
//     agent that asked for it).
//   - How rt.NewTeam assembles a leader + members and team.Run orchestrates
//     plan → delegate → execute → synthesise.
//   - How TeamResult exposes Plan, SubResults, Output and Duration so you can
//     observe the split work end to end.
//
// Core APIs used (with package paths):
//   - sdk.LoadConfigFile             — github.com/Timwood0x10/ares/sdk
//   - (*cfg.ConfigFile).ToOptions()  — github.com/Timwood0x10/ares/sdk
//   - sdk.NewRuntime                 — github.com/Timwood0x10/ares/sdk
//   - (*Runtime).ToolRegistry()      — github.com/Timwood0x10/ares/sdk
//   - api/tools.ToolFunc             — github.com/Timwood0x10/ares/api/tools
//   - rt.NewAgent / sdk.WithInstruction — github.com/Timwood0x10/ares/sdk
//   - rt.NewTeam / team.Run          — github.com/Timwood0x10/ares/sdk
//   - sdk.TeamResult / sdk.SubResult — github.com/Timwood0x10/ares/sdk
//
// Run (from the repo root):
//
//	go run examples/26-runtime-scheduling-demo/main.go
//
// Config: the demo reads ./ares.yaml (the root config with your real LLM
// endpoints). A version-safe template lives at
// examples/25-dual-endpoint-fallback/ares.yaml.
//
// Expected output:
//
//	📋 Task: Summarise the taskfabric module: its files, responsibilities and
//	        how the scheduler picks a capable agent for a task.
//	📋 Plan:   <leader's split plan>
//	📝 member code_reader: <per-member sub-result>
//	📝 Result: <synthesised module summary>
//	   sub-results: N | took: <duration>
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/api/tools"
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
	rt := sdk.NewRuntime(opts...)
	defer rt.Close()

	// ── Step 2: Build the sandboxed file tools ──
	// read_file / list_files let the agents actually inspect the module under
	// analysis. The paths are sandboxed to the repo root so the agents can
	// never read outside the project (defense in depth, not trust).
	//
	// NOTE: tools must ALSO be bound to each agent via sdk.WithTools. The
	// runtime ToolRegistry alone does NOT expose them to the LLM — the agent's
	// LLM tool definitions come from its own `WithTools` list
	// (sdk.resolveTools: a.toCoreTools(a.tools)); the registry is only the
	// executor the runtime uses when the agent calls a tool.
	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ getwd: %v\n", err)
		return
	}
	fileTools := buildFileTools(repoRoot)

	// Bind tools BOTH ways: sdk.WithTools exposes the tool definitions to the
	// LLM (resolveTools: toCoreTools(a.tools)), and ToolRegistry.Register wires
	// the executor the runtime uses when the agent actually calls a tool.
	// Missing either side fails: without WithTools the LLM never calls the
	// tool (0 tools); without Register the call errors with "tool not found".
	for _, t := range fileTools {
		if err := rt.ToolRegistry().Register(t); err != nil {
			fmt.Fprintf(os.Stderr, "❌ register %s: %v\n", t.Name(), err)
			return
		}
	}

	// ── Step 3: Create the agent team ──
	// The leader plans and splits the task; members analyse their slice using
	// the file tools; the leader synthesises the final summary. Every agent
	// that should call the tools gets them via WithTools.
	leader := rt.NewAgent("coordinator",
		sdk.WithInstruction(`You are a team lead for code-module analysis.
Plan the work, delegate to members, and synthesise their findings into a concise
module summary. Use list_files to find the module's files and read_file to
inspect them when you need context for the plan.`),
		sdk.WithTools(fileTools...),
	)
	analyst := rt.NewAgent("code_reader",
		sdk.WithInstruction(`You are a code analyst. Use list_files to enumerate the
module's files and read_file to inspect each one. Summarise the responsibilities
you find: what each file does and how the pieces fit together. Be factual and
reference file paths.`),
		sdk.WithTools(fileTools...),
	)

	team := rt.NewTeam("module-analysis", leader, []*sdk.Agent{analyst})

	// ── Step 4: Submit the task and let the runtime schedule it ──
	// team.Run drives plan → delegate → execute → synthesise. The runtime
	// dispatches each agent's turn; the members' tool calls are routed back
	// through the runtime's tool registry.
	task := "Summarise the internal/taskfabric module: enumerate its files, " +
		"explain each one's responsibility, and describe how the scheduler " +
		"picks a capable agent for a task."

	// ── 阶段记录 (Phase log) ──
	// 每一步都打印，完整还原"任务提交 → leader 规划 → 拆分 → 每成员执行
	// （含工具调用）→ 汇总"的全过程。
	logPhase("1/5 提交任务 (submit)", task)
	fmt.Printf("📋 Task: %s\n\n", task)

	start := time.Now()
	result, err := team.Run(ctx, task)
	elapsed := time.Since(start)
	if err != nil {
		if strings.Contains(err.Error(), "API key") {
			fmt.Fprintf(os.Stderr, "❌ %v\n   → Set your LLM key in ./ares.yaml (see examples/25-dual-endpoint-fallback)\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "❌ team run: %v\n", err)
		return
	}

	// ── Step 5: Print the full trace — plan, per-member results, synthesis ──
	// leader 规划（Phase 2）：leader 先用工具发现文件并产出计划。
	logPhase("2/5 leader 规划 (plan+discover)", "")
	fmt.Printf("📋 Plan:\n%s\n\n", result.Plan)

	// 拆分（Phase 3）：文件列表被分给各成员，并发执行。
	logPhase(fmt.Sprintf("3/5 任务拆分 (split) → %d 个成员子任务", len(result.SubResults)), "")
	for i, sub := range result.SubResults {
		// 每成员执行（Phase 4）：成员用工具读取文件并产出分析。
		logPhase(fmt.Sprintf("4/5 成员执行 (execute) #%d %s", i+1, sub.MemberName), "")
		if sub.Error != "" {
			fmt.Printf("📝 member #%d (%s) ❌ error: %s\n\n", i+1, sub.MemberName, sub.Error)
			continue
		}
		fmt.Printf("📝 member #%d (%s):\n%s\n", i+1, sub.MemberName, sub.Output)
		if sub.Duration != "" {
			fmt.Printf("   ⏱ member #%d took: %s\n", i+1, sub.Duration)
		}
		fmt.Println()
	}

	// 汇总（Phase 5）：leader 综合各成员结果，产出最终输出；若有验证器则给出验证结论。
	logPhase("5/5 leader 汇总 (synthesize)", "")
	fmt.Printf("✅ Result:\n%s\n\n", result.Output)
	if result.Verification != "" {
		fmt.Printf("🔎 Verification:\n%s\n\n", result.Verification)
	}
	fmt.Printf("   passed: %v | sub-results: %d | took: %v (runtime elapsed: %v)\n",
		result.Passed, len(result.SubResults), result.Duration.Round(time.Millisecond), elapsed.Round(time.Millisecond))
}

// logPhase prints a phase banner so the demo's stdout is a complete, greppable
// record of the whole task lifecycle (提交→规划→拆分→执行→汇总).
func logPhase(title, detail string) {
	if detail != "" {
		fmt.Printf("── %s: %s ──\n", title, detail)
		return
	}
	fmt.Printf("── %s ──\n", title)
}

// buildFileTools builds the sandboxed list_files / read_file tools. The tools
// are returned so callers bind them to agents via sdk.WithTools — binding is
// what makes the LLM aware of them (see Step 2 note in main).
//
// Args:
//   - repoRoot: the absolute repo root; tool paths are confined to it.
//
// Returns:
//   - []tools.Tool: list_files + read_file, ready for sdk.WithTools.
func buildFileTools(repoRoot string) []tools.Tool {
	// safeJoin confines a user-supplied relative path to the repo root.
	safeJoin := func(rel string) (string, error) {
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) {
			return "", fmt.Errorf("absolute paths are not allowed")
		}
		full := filepath.Join(repoRoot, clean)
		if !strings.HasPrefix(full, repoRoot) {
			return "", fmt.Errorf("path escapes the repo root")
		}
		return full, nil
	}

	listTool := tools.ToolFunc{
		ToolName: "list_files",
		ToolDesc: "List the files and directories under a repo-relative path (e.g. \"internal/taskfabric\"). Returns file names with sizes.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "repo-relative directory path"},
			},
			"required": []string{"path"},
		},
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			rel, _ := params["path"].(string)
			full, err := safeJoin(rel)
			if err != nil {
				return nil, err
			}
			entries, err := os.ReadDir(full)
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			for _, e := range entries {
				info, ierr := e.Info()
				size := int64(0)
				if ierr == nil {
					size = info.Size()
				}
				if e.IsDir() {
					fmt.Fprintf(&b, "%s/\n", e.Name())
				} else {
					fmt.Fprintf(&b, "%s (%d B)\n", e.Name(), size)
				}
			}
			return b.String(), nil
		},
	}

	readTool := tools.ToolFunc{
		ToolName: "read_file",
		ToolDesc: "Read a repo-relative file and return its contents (truncated to 1500 chars so long files never blow up the prompt budget).",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "repo-relative file path"},
			},
			"required": []string{"path"},
		},
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			rel, _ := params["path"].(string)
			full, err := safeJoin(rel)
			if err != nil {
				return nil, err
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return nil, err
			}
			const max = 1500
			if len(data) > max {
				return fmt.Sprintf("%s\n… [truncated %d more chars — read the next section via a targeted grep if needed]\n", string(data[:max]), len(data)-max), nil
			}
			return string(data), nil
		},
	}

	return []tools.Tool{listTool, readTool}
}
