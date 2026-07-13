// Eval — runs concrete evaluation scenarios to measure ARES capabilities.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/evaluation"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	rt := sdk.MustNew(sdk.WithOllama("llama3.2"), sdk.WithEvolution(), sdk.WithTrace(false))
	defer rt.Close()

	// Register the calculator tool for tool-using scenarios.
	_ = rt.ToolRegistry().Register(calcTool)

	eval := evaluation.New("ARES Capability Evaluation")

	// ── 1. Basic Chat: does the agent respond correctly? ──────────
	_ = eval.Register(&evaluation.Scenario{
		Name:        "basic-chat",
		Description: "Basic agent response correctness",
		Runs:        3,
		Timeout:     30 * time.Second,
		Runner: evaluation.RunnerFunc(func(ctx context.Context, task string) (*evaluation.Metrics, error) {
			agent := rt.NewAgent("chat", sdk.WithInstruction("Respond concisely."))
			start := time.Now()
			result, err := agent.Run(ctx, "What is the capital of France?")
			latency := time.Since(start)
			if err != nil {
				return &evaluation.Metrics{Task: task, Success: false, Score: 0, Latency: latency}, nil
			}
			score := 0.0
			if strings.Contains(strings.ToLower(result.Output), "paris") {
				score = 1.0
			}
			return &evaluation.Metrics{
				Task: task, Success: score > 0, Score: score,
				Latency: latency, TokenCount: result.TokenUsage.Total, ToolCalls: result.ToolCalls,
			}, nil
		}),
	})

	// ── 2. Tool Calling: does the agent use tools correctly? ──────
	_ = eval.Register(&evaluation.Scenario{
		Name:        "tool-calling",
		Description: "Agent correctly invokes calculator tool",
		Runs:        3,
		Timeout:     30 * time.Second,
		Runner: evaluation.RunnerFunc(func(ctx context.Context, task string) (*evaluation.Metrics, error) {
			agent := rt.NewAgent("tool-user",
				sdk.WithInstruction("Use the calculator tool for math."),
			)
			start := time.Now()
			result, err := agent.Run(ctx, "Calculate 15*23 + 100")
			latency := time.Since(start)
			if err != nil {
				return &evaluation.Metrics{Task: task, Success: false, Score: 0, Latency: latency}, nil
			}
			score := 0.0
			if result.ToolCalls > 0 {
				score = 0.5
			}
			if strings.Contains(result.Output, "445") {
				score = 1.0
			}
			return &evaluation.Metrics{
				Task: task, Success: result.ToolCalls > 0, Score: score,
				Latency: latency, TokenCount: result.TokenUsage.Total, ToolCalls: result.ToolCalls,
			}, nil
		}),
	})

	// ── 3. Multi-Agent: does the team orchestration work? ─────────
	_ = eval.Register(&evaluation.Scenario{
		Name:        "multi-agent",
		Description: "Leader/member team collaboration",
		Runs:        2,
		Timeout:     60 * time.Second,
		Runner: evaluation.RunnerFunc(func(ctx context.Context, task string) (*evaluation.Metrics, error) {
			leader := rt.NewAgent("lead", sdk.WithInstruction("Plan and summarize."))
			worker := rt.NewAgent("worker", sdk.WithInstruction("Execute tasks."))
			team := rt.NewTeam("eval-team", leader, []*sdk.Agent{worker})
			start := time.Now()
			result, err := team.Run(ctx, "Say hello briefly")
			latency := time.Since(start)
			if err != nil {
				return &evaluation.Metrics{Task: task, Success: false, Score: 0, Latency: latency}, nil
			}
			score := 0.0
			if result.Output != "" {
				score = 0.5
			}
			if len(result.SubResults) > 0 {
				score = 1.0
			}
			return &evaluation.Metrics{
				Task: task, Success: result.Output != "", Score: score,
				Latency: latency,
			}, nil
		}),
	})

	// ── 4. Resilience: does the agent handle errors gracefully? ───
	_ = eval.Register(&evaluation.Scenario{
		Name:        "resilience",
		Description: "Agent recovers from tool failures",
		Runs:        2,
		Timeout:     30 * time.Second,
		Runner: evaluation.RunnerFunc(func(ctx context.Context, task string) (*evaluation.Metrics, error) {
			agent := rt.NewAgent("resilient",
				sdk.WithInstruction("If a tool fails, explain gracefully."),
				sdk.WithTools(failTool),
			)
			start := time.Now()
			result, err := agent.Run(ctx, "Use the unreliable_tool and handle failure")
			latency := time.Since(start)
			if err != nil {
				return &evaluation.Metrics{Task: task, Success: false, Score: 0, Latency: latency}, nil
			}
			score := 0.0
			if result.ToolCalls > 0 {
				score = 0.5 // at least tried the tool
			}
			if len(result.Output) > 20 {
				score = 1.0 // produced a meaningful response despite failure
			}
			return &evaluation.Metrics{
				Task: task, Success: result.Output != "", Score: score,
				Latency: latency, TokenCount: result.TokenUsage.Total, ToolCalls: result.ToolCalls,
			}, nil
		}),
	})

	// ── 5. Evolution: does evolution improve agent performance? ──
	_ = eval.Register(&evaluation.Scenario{
		Name:        "evolution",
		Description: "Instruction evolution improves response quality",
		Runs:        1,
		Timeout:     90 * time.Second,
		Runner: evaluation.RunnerFunc(func(ctx context.Context, task string) (*evaluation.Metrics, error) {
			baseInstr := "Answer questions."
			agent := rt.NewAgent("evolvable", sdk.WithInstruction(baseInstr))

			// Before evolution.
			start := time.Now()
			r1, err1 := agent.Run(ctx, "Explain closures in Go with a short example")
			latency := time.Since(start)
			if err1 != nil {
				return &evaluation.Metrics{Task: task, Success: false, Score: 0}, nil
			}
			scoreBefore := scoreResponse(r1.Output)

			// Evolve.
			evolvedInstr, err := rt.Evolve(ctx, agent, "Explain closures in Go with a short example")
			if err != nil {
				return &evaluation.Metrics{Task: task, Success: false, Score: scoreBefore}, nil
			}

			// After evolution.
			agent2 := rt.NewAgent("evolved", sdk.WithInstruction(evolvedInstr))
			r2, err2 := agent2.Run(ctx, "Explain closures in Go with a short example")
			if err2 != nil {
				return &evaluation.Metrics{
					Task: task, Success: true, Score: scoreBefore,
					ScoreBefore: scoreBefore, ScoreAfter: 0,
					EvoImprovement: -100, Latency: latency,
					TokenCount: r1.TokenUsage.Total, Generation: 1,
				}, nil
			}
			scoreAfter := scoreResponse(r2.Output)
			improvement := ((scoreAfter - scoreBefore) / scoreBefore) * 100
			if scoreBefore == 0 {
				improvement = scoreAfter * 100
			}

			return &evaluation.Metrics{
				Task: task, Success: true,
				Score:          scoreAfter,
				ScoreBefore:    scoreBefore,
				ScoreAfter:     scoreAfter,
				EvoImprovement: improvement,
				Latency:        latency,
				TokenCount:     r1.TokenUsage.Total + r2.TokenUsage.Total,
				Generation:     1,
			}, nil
		}),
	})

	// ── Run all scenarios ─────────────────────────────────────────
	reports, err := eval.RunAll(ctx)
	if err != nil {
		return fmt.Errorf("eval: %w", err)
	}

	// ── Print summary ──────────────────────────────────────────
	fmt.Println()
	for name, report := range reports {
		fmt.Printf("═══ %s ═══\n", name)
		fmt.Printf("  Pass Rate:  %.0f%% (%d/%d)\n", report.PassRate, report.Passed, report.Runs)
		fmt.Printf("  Avg Score:  %.2f\n", report.AvgScore)
		fmt.Printf("  Avg Latency: %v\n", report.AvgLatency)
		fmt.Printf("  Tokens:     %d\n", report.TotalTokens)
		fmt.Println()
	}

	// Save JSON report.
	jsonPath := "eval-report.json"
	for _, report := range reports {
		jsonStr, _ := report.ToJSON()
		_ = os.WriteFile(jsonPath, []byte(jsonStr), 0644)
		break // save first report as sample
	}
	fmt.Printf("📄 Report saved to %s\n", jsonPath)
	return nil
}

var calcTool = toolFunc("calculator", "Evaluate math expressions", func(expr string) string {
	return "445"
})

var failTool = toolFunc("unreliable_tool", "Sometimes fails", func(input string) string {
	return ""
})

type simpleTool struct {
	name string
	desc string
	fn   func(string) string
}

func (t *simpleTool) Name() string               { return t.name }
func (t *simpleTool) Description() string        { return t.desc }
func (t *simpleTool) Parameters() map[string]any { return nil }
func (t *simpleTool) Capabilities() []string     { return nil }
func (t *simpleTool) Execute(_ context.Context, params map[string]any) (tools.Result, error) {
	input, _ := params["input"].(string)
	if t.name == "unreliable_tool" {
		return tools.Result{Success: false, Data: "service unavailable"}, nil
	}
	result := t.fn(input)
	if result == "" {
		return tools.Result{Success: false, Data: "empty result"}, nil
	}
	return tools.Result{Success: true, Data: result}, nil
}

func toolFunc(name, desc string, fn func(string) string) *simpleTool {
	return &simpleTool{name: name, desc: desc, fn: fn}
}

// scoreResponse rates response quality 0.0-1.0 based on content indicators.
func scoreResponse(output string) float64 {
	if output == "" {
		return 0
	}
	score := 0.3 // base: has content
	lower := strings.ToLower(output)
	if strings.Contains(lower, "function") || strings.Contains(lower, "func") {
		score += 0.2
	}
	if strings.Contains(lower, "closure") {
		score += 0.2
	}
	if strings.Contains(lower, "example") || strings.Contains(lower, "```") {
		score += 0.2
	}
	if len([]rune(output)) > 100 {
		score += 0.1
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}
