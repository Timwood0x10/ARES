// Dev commands — ares init | run | bench | doctor | version
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Timwood0x10/ares/sdk"
)

var version = "dev" // set via ldflags at build time

func init() {
	// version
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show ARES version",
		Run: func(_ *cobra.Command, _ []string) {
			info, ok := debug.ReadBuildInfo()
			v := version
			if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
				v = info.Main.Version
			}
			fmt.Printf("ARES %s (%s/%s, %s)\n", v, runtime.GOOS, runtime.GOARCH, runtime.Version())
		},
	})

	// doctor
	rootCmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Diagnose ARES environment",
		RunE:  runDoctor,
	})

	// init
	var initDir string
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a new ARES project",
		RunE:  runInit,
	}
	initCmd.Flags().StringVarP(&initDir, "dir", "d", ".", "Project directory")
	rootCmd.AddCommand(initCmd)

	// run
	var configPath string
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run agent from config file",
		RunE:  runRun,
	}
	runCmd.Flags().StringVarP(&configPath, "config", "c", "ares.yaml", "Config file path")
	rootCmd.AddCommand(runCmd)

	// bench
	benchCmd := &cobra.Command{
		Use:   "bench",
		Short: "Run benchmark",
		RunE:  runBench,
	}
	benchCmd.Flags().StringP("format", "f", "markdown", "Output format: markdown | json")
	rootCmd.AddCommand(benchCmd)
}

// ── doctor ─────────────────────────────────────────────────────

func runDoctor(_ *cobra.Command, _ []string) error {
	ok := true

	fmt.Println("🔍 ARES Doctor")
	fmt.Println()

	// Go version
	fmt.Printf("  Go:       %s", runtime.Version())
	if v := runtime.Version(); strings.HasPrefix(v, "go1.26") || strings.HasPrefix(v, "go1.25") {
		fmt.Println(" ✅")
	} else {
		fmt.Println(" ⚠  Go 1.25+ recommended")
	}

	// LLM key check
	providers := []struct {
		name string
		env  string
	}{
		{"OpenAI", "OPENAI_API_KEY"},
		{"Anthropic", "ANTHROPIC_API_KEY"},
		{"OpenRouter", "OPENROUTER_API_KEY"},
	}
	for _, p := range providers {
		if v := os.Getenv(p.env); v != "" {
			fmt.Printf("  %-10s ✅ (%s...)\n", p.name, v[:min(8, len(v))]+"...")
		} else {
			fmt.Printf("  %-10s ❌ set %s\n", p.name, p.env)
			ok = false
		}
	}

	// Ollama check
	if err := exec.Command("ollama", "--version").Run(); err == nil {
		fmt.Println("  Ollama    ✅")
	} else {
		fmt.Println("  Ollama    ❌ not found (optional, install for local LLM)")
	}

	// Git check
	if err := exec.Command("git", "--version").Run(); err == nil {
		fmt.Println("  Git       ✅")
	} else {
		fmt.Println("  Git       ❌ not found")
	}

	fmt.Println()
	if ok {
		fmt.Println("✅ Environment looks good")
	} else {
		fmt.Println("⚠  Some checks failed — see above")
	}
	return nil
}

// ── init ───────────────────────────────────────────────────────

func runInit(cmd *cobra.Command, args []string) error {
	dir, _ := cmd.Flags().GetString("dir")
	if dir == "" {
		dir = "."
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	// Detect ARES module path for the replace directive.
	aresMod := "github.com/Timwood0x10/ares"
	aresRoot := findAresRoot()
	replaceLine := ""
	if aresRoot != "" {
		replaceLine = fmt.Sprintf("replace %s => %s\n", aresMod, aresRoot)
	}

	// go.mod template.
	goMod := fmt.Sprintf(`module myapp

go 1.26

require %s v0.0.0

%s`, aresMod, replaceLine)

	// main.go template.
	mainGo := `package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	rt := sdk.MustNew(
		sdk.WithOllama("llama3.2"),
		sdk.WithDefaultMemory(),
	)
	defer rt.Close()

	agent := rt.NewAgent("assistant",
		sdk.WithInstruction("You are a helpful assistant."),
	)

	input := "Hello!"
	if len(os.Args) > 1 {
		input = os.Args[1]
	}

	result, err := agent.Run(ctx, input)
	if err != nil {
		log.Fatalf("run: %v", err)
	}

	fmt.Println(result.Output)
	fmt.Printf("(tokens: %d, tools: %d, took: %v)\n",
		result.TokenUsage.Total, result.ToolCalls, result.Duration)
}
`

	// ares.yaml config template.
	aresYaml := `# ARES project configuration
llm:
  provider: ollama    # openai | anthropic | openrouter
  model: llama3.2

memory:
  enabled: true

tools:
  builtin: true

reflection:
  enabled: false

evolution:
  enabled: false
`

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0644); err != nil {
		return fmt.Errorf("write main.go: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ares.yaml"), []byte(aresYaml), 0644); err != nil {
		return fmt.Errorf("write ares.yaml: %w", err)
	}

	fmt.Printf("✅ Created ARES project in %s\n", dir)
	fmt.Println("   Files: go.mod, main.go, ares.yaml")
	fmt.Println("   Run:   cd", dir, "&& go run .")
	return nil
}

// findAresRoot walks up from the current directory looking for go.mod
// containing "github.com/Timwood0x10/ares".
func findAresRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := wd
	for {
		gm := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(gm)
		if err == nil && strings.Contains(string(data), "module github.com/Timwood0x10/ares") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ── run ────────────────────────────────────────────────────────

func runRun(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	// Auto-detect config if -c not provided.
	if configPath == "" || configPath == "ares.yaml" {
		if _, err := os.Stat("ares.yaml"); err == nil {
			configPath = "ares.yaml"
		} else if _, err := os.Stat("config/ares.yaml"); err == nil {
			configPath = "config/ares.yaml"
		}
	}
	if configPath == "" {
		return fmt.Errorf("no ares.yaml found; use -c to specify, or create one with 'ares init'")
	}

	// Load and parse config.
	cfg, err := sdk.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	opts, err := cfg.ToOptions()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	opts = append(opts, sdk.WithTrace(true))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	rt := sdk.MustNew(opts...)
	defer rt.Close()

	agent := rt.NewAgent("cli-agent",
		sdk.WithInstruction("You are a helpful assistant."),
	)

	// Read input from args (skip run subcommand and config flags).
	input := strings.Join(parseRunArgs(), " ")
	if input == "" {
		fmt.Print("Enter prompt: ")
		_, _ = fmt.Scanln(&input)
		input = strings.TrimSpace(input)
	}
	if input == "" {
		input = "Say hello"
	}

	result, err := agent.Run(ctx, input)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	fmt.Println(result.Output)
	fmt.Printf("(tokens: %d, tools: %d, took: %v)\n",
		result.TokenUsage.Total, result.ToolCalls, result.Duration)
	return nil
}

// parseRunArgs returns os.Args with the "run" subcommand and --config flag
// removed, so remaining words can be used as the input prompt.
func parseRunArgs() []string {
	var out []string
	skipNext := false
	for i, a := range os.Args {
		if i == 0 {
			continue // skip program name
		}
		if a == "run" {
			continue
		}
		if skipNext {
			skipNext = false
			continue
		}
		if a == "-c" || a == "--config" {
			skipNext = true
			continue
		}
		out = append(out, a)
	}
	return out
}

// ── bench ──────────────────────────────────────────────────────

type benchResult struct {
	Task      string        `json:"task"`
	Success   bool          `json:"success"`
	Output    string        `json:"output"`
	ToolCalls int           `json:"tool_calls"`
	Tokens    int           `json:"tokens"`
	Latency   time.Duration `json:"latency_ms"`
	MemoryHit bool          `json:"memory_hit"`
}

type benchReport struct {
	Date        string        `json:"date"`
	Model       string        `json:"model"`
	Provider    string        `json:"provider"`
	Results     []benchResult `json:"results"`
	AvgLatency  time.Duration `json:"avg_latency_ms"`
	TotalTokens int           `json:"total_tokens"`
	TotalTools  int           `json:"total_tool_calls"`
	PassRate    float64       `json:"pass_rate"`
}

func runBench(cmd *cobra.Command, _ []string) error {
	format, _ := cmd.Flags().GetString("format")
	if format == "" {
		format = "markdown"
	}

	ctx := context.Background()

	rt := sdk.MustNew(sdk.WithTrace(false))
	defer rt.Close()

	agent := rt.NewAgent("bench-agent",
		sdk.WithInstruction("Respond concisely in under 20 words."),
	)

	tasks := []string{
		"Say hello in English",
		"What is 2+2?",
		"Name three primary colors",
		"Convert 100 Celsius to Fahrenheit",
		"List the planets in order from the sun",
	}

	var results = make([]benchResult, 0, len(tasks))
	var totalDuration time.Duration
	var totalTokens, totalTools int
	passed := 0

	for i, task := range tasks {
		start := time.Now()
		result, err := agent.Run(ctx, task)
		d := time.Since(start)

		br := benchResult{
			Task:    task,
			Success: err == nil,
			Latency: d,
		}
		if err == nil {
			br.Output = truncateStr(result.Output, 60)
			br.ToolCalls = result.ToolCalls
			br.Tokens = result.TokenUsage.Total
			br.MemoryHit = result.MemoryUsed
			totalDuration += d
			totalTokens += result.TokenUsage.Total
			totalTools += result.ToolCalls
			passed++
		} else {
			br.Output = err.Error()
		}
		results = append(results, br)

		if format == "markdown" {
			status := "✅"
			if !br.Success {
				status = "❌"
			}
			fmt.Printf("| %d | %-40s | %s %s | %d tok | %v |\n",
				i+1, br.Task, status, truncateStr(br.Output, 30), br.Tokens, br.Latency.Round(time.Millisecond))
		}
	}

	report := benchReport{
		Date:        time.Now().Format(time.RFC3339),
		Model:       rt.GetModel(),
		Provider:    rt.GetProvider(),
		Results:     results,
		AvgLatency:  totalDuration / time.Duration(len(tasks)),
		TotalTokens: totalTokens,
		TotalTools:  totalTools,
		PassRate:    float64(passed) / float64(len(tasks)) * 100,
	}

	switch format {
	case "json":
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
	default:
		fmt.Println()
		fmt.Printf("**Summary** | **Value**\n")
		fmt.Printf("---|---\n")
		fmt.Printf("Model | %s\n", report.Model)
		fmt.Printf("Provider | %s\n", report.Provider)
		fmt.Printf("Tasks | %d/%d passed\n", passed, len(tasks))
		fmt.Printf("Pass Rate | %.0f%%\n", report.PassRate)
		fmt.Printf("Avg Latency | %v\n", report.AvgLatency.Round(time.Millisecond))
		fmt.Printf("Total Tokens | %d\n", report.TotalTokens)
		fmt.Printf("Total Tool Calls | %d\n", report.TotalTools)
	}
	return nil
}

func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
