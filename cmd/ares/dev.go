// Dev commands — ares init | run | bench | doctor | version
package main

import (
	"context"
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
		Short: "Run quick benchmark",
		RunE:  runBench,
	}
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
			ok = true
		} else {
			fmt.Printf("  %-10s ❌ set %s\n", p.name, p.env)
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

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0644); err != nil {
		return fmt.Errorf("write main.go: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ares.yaml"), []byte(aresYaml), 0644); err != nil {
		return fmt.Errorf("write ares.yaml: %w", err)
	}

	fmt.Printf("✅ Created ARES project in %s\n", dir)
	fmt.Println("   Files: main.go, ares.yaml")
	fmt.Println("   Run:   cd", dir, "&& go mod init myapp && go mod tidy && go run .")
	return nil
}

// ── run ────────────────────────────────────────────────────────

func runRun(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	// Try loading config.
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config %q: %w", configPath, err)
	}

	fmt.Printf("📋 Using config: %s\n", configPath)
	fmt.Println(string(data))

	// Simple run with SDK defaults for now.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rt := sdk.MustNew(sdk.WithTrace(true))
	defer rt.Close()

	agent := rt.NewAgent("cli-agent",
		sdk.WithInstruction("You are a helpful assistant."),
	)

	// Read input from args or stdin.
	args := os.Args[2:] // skip "run"
	input := strings.Join(args, " ")
	if input == "" {
		fmt.Print("Enter prompt: ")
		_, _ = fmt.Scanln(&input)
		input = strings.TrimSpace(input)
		if input == "" {
			input = "Say hello"
		}
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

// ── bench ──────────────────────────────────────────────────────

func runBench(_ *cobra.Command, _ []string) error {
	fmt.Println("📊 ARES Quick Benchmark")
	fmt.Println()

	ctx := context.Background()

	rt := sdk.MustNew(sdk.WithTrace(false))
	defer rt.Close()

	agent := rt.NewAgent("bench-agent",
		sdk.WithInstruction("Respond concisely."),
	)

	tasks := []string{
		"Say hello",
		"What is 2+2?",
		"Name three colors",
	}

	var totalDuration time.Duration
	var totalTokens int

	for i, task := range tasks {
		start := time.Now()
		result, err := agent.Run(ctx, task)
		d := time.Since(start)
		if err != nil {
			fmt.Printf("  %d. %-30s ❌ %v\n", i+1, task, err)
			continue
		}
		totalDuration += d
		totalTokens += result.TokenUsage.Total
		fmt.Printf("  %d. %-30s ✅ %s (%d tokens, %v)\n",
			i+1, task, truncateStr(result.Output, 40), result.TokenUsage.Total, d.Round(time.Millisecond))
	}

	fmt.Println()
	if len(tasks) > 0 {
		avg := totalDuration / time.Duration(len(tasks))
		fmt.Printf("  Average: %v | Total tokens: %d\n", avg.Round(time.Millisecond), totalTokens)
	}
	fmt.Println("✅ Benchmark complete")
	return nil
}

func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
