// Chaos resilience — demonstrates real failure handling and self-healing patterns.
// Covers multiple chaos modes: file system, tool timeout, network failure,
// graceful degradation, and fallback.
//
// Run:
//
//	go run examples/06-chaos-resilience/main.go
package main

import (
	"context"
	"encoding/json"
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

	rt := sdk.MustNew(sdk.WithOllama("llama3.2"), sdk.WithTrace(true))
	defer rt.Close()

	// Inject all chaos tools.
	dataDir := filepath.Join("examples", "06-chaos-resilience", "data")
	chaosTools := []tools.Tool{
		readFileTool(dataDir),
		slowTool,
		unreliableTool,
		echoTool,
		flakyNetworkTool,
		mcpDisconnectTool,
		llmFailureTool,
		memoryCorruptTool,
	}
	for _, t := range chaosTools {
		_ = rt.ToolRegistry().Register(t)
	}

	// Test each chaos scenario.
	scenarios := []struct {
		name  string
		tasks []string
	}{
		{
			name: "File system failures",
			tasks: []string{
				"Read data/languages.json and tell me which language has the most repos",
				"Read data/missing.json and explain what happened",
			},
		},
		{
			name: "Tool timeout",
			tasks: []string{
				"Call slow_tool with input 'test' and handle the result",
			},
		},
		{
			name: "Graceful degradation",
			tasks: []string{
				"Try unreliable_tool first, then fall back to echo_tool if it fails",
			},
		},
		{
			name: "Network failure simulation",
			tasks: []string{
				"Call flaky_network_api and handle any errors gracefully",
			},
		},
		{
			name: "MCP disconnect",
			tasks: []string{
				"Call mcp_disconnect_tool and explain what a disconnected MCP server means",
			},
		},
		{
			name: "LLM failure simulation",
			tasks: []string{
				"Call llm_failure_tool and handle the LLM service error gracefully",
			},
		},
		{
			name: "Memory corruption",
			tasks: []string{
				"Call memory_corrupt_tool with key 'user_data' and handle the corrupted data",
			},
		},
	}

	for _, sc := range scenarios {
		fmt.Printf("\n═══ %s ═══\n", sc.name)
		for _, task := range sc.tasks {
			fmt.Printf("  📋 %s\n", task)
			agent := rt.NewAgent("resilient-agent",
				sdk.WithInstruction("You are resilient. Handle tool failures gracefully. Always explain what happened."),
			)
			result, err := agent.Run(ctx, task)
			if err != nil {
				fmt.Printf("  ❌ %v\n", err)
				continue
			}
			fmt.Printf("  🤖 %s\n", result.Output)
			emoji := "✅"
			if strings.Contains(result.Output, "error") || strings.Contains(result.Output, "fail") ||
				strings.Contains(result.Output, "not found") || strings.Contains(result.Output, "sorry") {
				emoji = "⚠️"
			}
			fmt.Printf("  %s tools: %d | tokens: %d | took: %v\n",
				emoji, result.ToolCalls, result.TokenUsage.Total, result.Duration.Round(time.Millisecond))
		}
	}

	fmt.Println("\n✅ Chaos resilience demo completed")
}

// ── Chaos tools ────────────────────────────────────────────────

func readFileTool(dataDir string) tools.Tool {
	return tools.ToolFunc{
		ToolName: "read_file",
		ToolDesc: "Read a JSON data file from the data directory",
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			filename, _ := params["filename"].(string)
			if filename == "" {
				return nil, fmt.Errorf("filename is required")
			}
			filename = filepath.Base(filename)
			fullPath := filepath.Join(dataDir, filename)

			data, err := os.ReadFile(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("file %q not found", filename)
				}
				return nil, fmt.Errorf("read error: %w", err)
			}
			if !json.Valid(data) {
				return nil, fmt.Errorf("file %q contains invalid JSON", filename)
			}
			var parsed any
			_ = json.Unmarshal(data, &parsed)
			pretty, _ := json.MarshalIndent(parsed, "", "  ")
			return string(pretty), nil
		},
	}
}

var slowTool = tools.ToolFunc{
	ToolName: "slow_tool",
	ToolDesc: "A deliberately slow tool that takes 5 seconds",
	Fn: func(ctx context.Context, params map[string]any) (any, error) {
		input, _ := params["input"].(string)
		select {
		case <-time.After(5 * time.Second):
			return fmt.Sprintf("slow result for: %s", input), nil
		case <-ctx.Done():
			return nil, fmt.Errorf("tool timed out")
		}
	},
}

var unreliableTool = tools.ToolFunc{
	ToolName: "unreliable_tool",
	ToolDesc: "A tool that fails 80% of the time",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		input, _ := params["input"].(string)
		_ = input
		// Simulate 80% failure rate.
		return nil, fmt.Errorf("unreliable_tool: service temporarily unavailable (simulated)")
	},
}

var echoTool = tools.ToolFunc{
	ToolName: "echo_tool",
	ToolDesc: "Fallback tool that echoes input",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		input, _ := params["input"].(string)
		return fmt.Sprintf("echo: %s", input), nil
	},
}

var flakyNetworkTool = tools.ToolFunc{
	ToolName: "flaky_network_api",
	ToolDesc: "Simulates a flaky network API that sometimes times out",
	Fn: func(ctx context.Context, params map[string]any) (any, error) {
		endpoint, _ := params["endpoint"].(string)
		_ = endpoint
		select {
		case <-time.After(3 * time.Second):
			return nil, fmt.Errorf("flaky_network_api: connection timeout after 3s")
		case <-ctx.Done():
			return nil, fmt.Errorf("flaky_network_api: request cancelled")
		}
	},
}

// ── Additional chaos modes ─────────────────────────────────────

var mcpDisconnectTool = tools.ToolFunc{
	ToolName: "mcp_disconnect_tool",
	ToolDesc: "Simulates an MCP server disconnection",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		server, _ := params["server"].(string)
		_ = server
		return nil, fmt.Errorf("MCP server 'codegraph' disconnected: transport closed (simulated)")
	},
}

var llmFailureTool = tools.ToolFunc{
	ToolName: "llm_failure_tool",
	ToolDesc: "Simulates an LLM service failure",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		service, _ := params["service"].(string)
		_ = service
		return nil, fmt.Errorf("LLM provider returned 503 Service Unavailable: rate limit exceeded (simulated)")
	},
}

var memoryCorruptTool = tools.ToolFunc{
	ToolName: "memory_corrupt_tool",
	ToolDesc: "Simulates corrupted memory/data retrieval",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		key, _ := params["key"].(string)
		return nil, fmt.Errorf("memory corruption detected for key %q: checksum mismatch, data cannot be recovered (simulated)", key)
	},
}
