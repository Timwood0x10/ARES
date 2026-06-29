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

	"github.com/Timwood0x10/ares/api/discovery"
	"github.com/Timwood0x10/ares/api/mcp"
	"github.com/Timwood0x10/ares/api/tools"
)

func main() {
	ctx := context.Background()

	// ── 1. Built-in tools ─────────────────────────────────
	registry := tools.NewRegistry()
	if err := tools.RegisterBuiltinTools(registry); err != nil {
		fmt.Printf("register builtin tools: %v\n", err)
		return
	}

	fmt.Println("=== Built-in Tools ===")
	for _, t := range registry.ListTools() {
		fmt.Printf("  %-20s %s\n", t.Name, t.Description)
	}

	// ── 2. Custom tools ───────────────────────────────────
	if err := registry.Register(tools.ToolFunc{
		ToolName: "sentiment",
		ToolDesc: "Analyze text sentiment",
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			text, _ := params["text"].(string)
			if strings.Contains(strings.ToLower(text), "good") {
				return map[string]any{"sentiment": "positive"}, nil
			}
			return map[string]any{"sentiment": "neutral"}, nil
		},
	}); err != nil {
		fmt.Printf("register sentiment: %v\n", err)
		return
	}

	if err := registry.Register(tools.ToolFunc{
		ToolName: "word_count",
		ToolDesc: "Count words in text",
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			text, _ := params["text"].(string)
			return map[string]any{"count": len(strings.Fields(text))}, nil
		},
	}); err != nil {
		fmt.Printf("register word_count: %v\n", err)
		return
	}

	// ── 3. Call tools ─────────────────────────────────────
	fmt.Println("\n=== Tool Calls ===")

	r1, _ := registry.Execute(ctx, "calculator", map[string]any{"expression": "2 + 3 * 4"})
	fmt.Printf("  calculator(2+3*4): %v\n", r1.Data)

	r2, _ := registry.Execute(ctx, "sentiment", map[string]any{"text": "This is good!"})
	fmt.Printf("  sentiment(good):   %v\n", r2.Data)

	r3, _ := registry.Execute(ctx, "word_count", map[string]any{"text": "hello world foo bar"})
	fmt.Printf("  word_count:        %v\n", r3.Data)

	// ── 4. MCP auto-discovery via api/discovery ────────────
	fmt.Println("\n=== MCP Auto-Discovery ===")
	engine := discovery.NewEngine(discovery.EngineConfig{})
	_ = engine.DiscoverNow(ctx)
	services, _ := engine.List(ctx)
	if len(services) == 0 {
		fmt.Println("  No MCP servers found")
	} else {
		fmt.Printf("  Found %d MCP server(s):\n", len(services))
		for _, svc := range services {
			fmt.Printf("    %s (confidence=%d%%)\n", svc.Identity.Name, bestConf(svc))
			// Connect via endpoint
			if len(svc.Records) > 0 {
				client, err := mcp.ConnectStdio(ctx, svc.Identity.Name, svc.Records[0].Endpoint, svc.Records[0].Args)
				if err != nil {
					fmt.Printf("      (connect failed: %v)\n", err)
					continue
				}
				defer func() { _ = client.Close() }()
				if err := client.RegisterTools(ctx, registry); err != nil {
					fmt.Printf("      (register failed: %v)\n", err)
					continue
				}
				fmt.Printf("      ✓ Registered tools\n")
			}
		}
	}

	// ── 5. List all tools ─────────────────────────────────
	fmt.Printf("\n=== All %d Tools ===\n", len(registry.List()))
	for _, name := range registry.List() {
		fmt.Printf("  %s\n", name)
	}
}

func bestConf(svc *discovery.DiscoveredService) int {
	best := 0
	for _, r := range svc.Records {
		if int(r.Confidence) > best {
			best = int(r.Confidence)
		}
	}
	return best
}
