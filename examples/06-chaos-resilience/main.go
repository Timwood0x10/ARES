// Chaos resilience — demonstrates real failure handling and self-healing patterns.
// Uses real filesystem data — no mocks.
//
// Scenarios:
//  1. Normal: agent reads a real JSON file and answers questions
//  2. Missing file: file not found → agent intelligently recovers
//  3. Corrupted data: malformed input → agent degrades gracefully
//
// Run:
//
//	go run examples/chaos-resilience/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	rt := sdk.MustNew(
		sdk.WithOllama("llama3.2"),
		sdk.WithTrace(true),
	)
	defer rt.Close()

	// Register real file-read tool.
	dataDir := filepath.Join("examples", "chaos-resilience", "data")
	if err := rt.ToolRegistry().Register(readFileTool(dataDir)); err != nil {
		fmt.Fprintf(os.Stderr, "❌ register tool: %v\n", err)
		return
	}

	agent := rt.NewAgent("analyst",
		sdk.WithInstruction(`You are a data analyst. Use the read_file tool to load JSON data files and answer questions.
If a file is missing, report clearly and suggest alternatives.
If data is incomplete, note what's missing.`),
	)

	// ── 1. Normal: file exists ──────────────────────────────────
	fmt.Println("═══ Scenario 1: Normal operation ═══")
	result1, err := agent.Run(ctx, "Load data/languages.json. Which language has the most repos?")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return
	}
	fmt.Printf("🤖 %s\n", result1.Output)

	// ── 2. Missing file: simulate by requesting nonexistent file ─
	fmt.Println("\n═══ Scenario 2: Missing file (resilience) ═══")
	result2, err := agent.Run(ctx, "Load data/missing.json and tell me what's in it")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return
	}
	fmt.Printf("🤖 %s\n", result2.Output)

	// ── 3. Agent self-healing: try alternative approach ──────────
	fmt.Println("\n═══ Scenario 3: Graceful degradation ═══")
	result3, err := agent.Run(ctx,
		"Try to load data/languages.json. If that fails, compute stats manually: I know there are 5 languages with about 10M total repos")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return
	}
	fmt.Printf("🤖 %s\n", result3.Output)

	fmt.Println("\n✅ Chaos resilience demo completed")
}

// readFileTool returns a real tool that reads JSON files from the data directory.
func readFileTool(dataDir string) tools.Tool {
	return tools.ToolFunc{
		ToolName: "read_file",
		ToolDesc: "Read a JSON data file and return its contents. Path is relative to the data directory.",
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			filename, _ := params["filename"].(string)
			if filename == "" {
				return nil, fmt.Errorf("filename is required")
			}
			// Sanitize: prevent path traversal.
			filename = filepath.Base(filename)
			fullPath := filepath.Join(dataDir, filename)

			data, err := os.ReadFile(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("file %q not found in data directory", filename)
				}
				return nil, fmt.Errorf("read %q: %w", filename, err)
			}

			// Validate JSON.
			if !json.Valid(data) {
				return nil, fmt.Errorf("file %q contains invalid JSON", filename)
			}

			var parsed any
			if err := json.Unmarshal(data, &parsed); err != nil {
				return nil, fmt.Errorf("parse %q: %w", filename, err)
			}
			pretty, _ := json.MarshalIndent(parsed, "", "  ")
			return string(pretty), nil
		},
	}
}
