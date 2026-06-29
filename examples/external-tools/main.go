// external-tools demonstrates how external projects use the ares public API.
//
// Shows:
//  1. Built-in tools (calculator, web_search, regex, etc.)
//  2. Custom tool registration
//  3. MCP server connection and tool usage
//  4. Building a tool registry for an agent system
//
// Run: go run ./examples/external-tools
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/api/mcp"
	"github.com/Timwood0x10/ares/api/tools"
)

func main() {
	ctx := context.Background()

	// ── 1. Built-in tools ─────────────────────────────────
	registry := tools.NewRegistry()
	tools.RegisterBuiltinTools(registry)

	fmt.Println("=== Built-in Tools ===")
	for _, t := range registry.ListTools() {
		fmt.Printf("  %-20s %s\n", t.Name, t.Description)
	}

	// ── 2. Custom tools ───────────────────────────────────
	registry.Register(tools.ToolFunc{
		ToolName: "sentiment",
		ToolDesc: "Analyze text sentiment",
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			text, _ := params["text"].(string)
			if strings.Contains(strings.ToLower(text), "good") {
				return map[string]any{"sentiment": "positive"}, nil
			}
			return map[string]any{"sentiment": "neutral"}, nil
		},
	})

	registry.Register(tools.ToolFunc{
		ToolName: "word_count",
		ToolDesc: "Count words in text",
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			text, _ := params["text"].(string)
			return map[string]any{"count": len(strings.Fields(text))}, nil
		},
	})

	// ── 3. Call tools ─────────────────────────────────────
	fmt.Println("\n=== Tool Calls ===")

	r1, _ := registry.Execute(ctx, "calculator", map[string]any{"expression": "2 + 3 * 4"})
	fmt.Printf("  calculator(2+3*4): %v\n", r1.Data)

	r2, _ := registry.Execute(ctx, "sentiment", map[string]any{"text": "This is good!"})
	fmt.Printf("  sentiment(good):   %v\n", r2.Data)

	r3, _ := registry.Execute(ctx, "word_count", map[string]any{"text": "hello world foo bar"})
	fmt.Printf("  word_count:        %v\n", r3.Data)

	// ── 4. MCP auto-discovery ──────────────────────────────
	fmt.Println("\n=== MCP Auto-Discovery ===")
	servers := mcp.DiscoverServers("")
	if len(servers) == 0 {
		fmt.Println("  No MCP servers found in ~/.claude.json or .claude/settings.json")
	} else {
		fmt.Printf("  Found %d MCP server(s):\n", len(servers))
		for _, s := range servers {
			fmt.Printf("    %s: %s %v\n", s.Name, s.Command, s.Args)
		}
		// Connect and register
		for _, s := range servers {
			client, err := mcp.ConnectFromConfig(ctx, s)
			if err != nil {
				fmt.Printf("  (skip %s: %v)\n", s.Name, err)
				continue
			}
			defer client.Close()
			if err := client.RegisterTools(ctx, registry); err != nil {
				fmt.Printf("  (skip %s register: %v)\n", s.Name, err)
				continue
			}
			fmt.Printf("  ✓ Connected to %s\n", s.Name)
		}
	}

	// Also try manual connection
	tryMCP(ctx, registry)

	// ── 5. List all tools ─────────────────────────────────
	fmt.Printf("\n=== All %d Tools ===\n", len(registry.List()))
	for _, name := range registry.List() {
		fmt.Printf("  %s\n", name)
	}
}

func tryMCP(ctx context.Context, registry *tools.Registry) {
	client, err := mcp.ConnectStdio(ctx, "codebase-memory", "codebase-memory-mcp", nil)
	if err != nil {
		fmt.Println("  (MCP not available, skipping)")
		return
	}
	defer client.Close()

	if err := client.RegisterTools(ctx, registry); err != nil {
		fmt.Printf("  (MCP register error: %v)\n", err)
		return
	}

	// Call an MCP tool
	result, err := registry.Execute(ctx, "mcp.codebase-memory.list_projects", map[string]any{})
	if err != nil {
		fmt.Printf("  (MCP call error: %v)\n", err)
		return
	}
	fmt.Printf("  MCP list_projects: %v\n", result.Data)
}
