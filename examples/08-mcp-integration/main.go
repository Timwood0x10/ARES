// MCP integration — demonstrates connecting to an MCP server and using its tools.
//
// This example builds the embedded MCP null server, connects via WithMCP(),
// and uses its tools (echo) through the agent.
//
// Run:
//
//	go run examples/08-mcp-integration/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	// ── 1. Build MCP server binary ─────────────────────────────
	mcpBin := filepath.Join(os.TempDir(), "ares-mcp-null")
	build := exec.Command("go", "build", "-o", mcpBin, "./cmd/mcp-null/")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ build MCP server: %v\n", err)
		return
	}
	defer func() { _ = os.Remove(mcpBin) }()

	// ── 2. Create Runtime with MCP server ──────────────────────
	rt := sdk.MustNew(
		sdk.WithOllama("llama3.2"),
		sdk.WithMCP(sdk.MCPConn{
			Name:    "null-server",
			Command: mcpBin,
			Args:    []string{"serve"},
		}),
	)
	defer rt.Close()

	// ── 3. Create Agent ─────────────────────────────────────────
	agent := rt.NewAgent("assistant",
		sdk.WithInstruction(`You are a helpful assistant with access to MCP tools.
Use the echo tool when asked to echo something.`),
	)

	// ── 4. Run ──────────────────────────────────────────────────
	for _, task := range []string{
		"Use the echo tool to echo 'Hello from MCP!'",
		"What tools do you have available?",
	} {
		fmt.Printf("\n---\n📋 %s\n", task)
		result, err := agent.Run(ctx, task)
		if err != nil {
			if strings.Contains(err.Error(), "API key") || strings.Contains(err.Error(), "refused") {
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
				return
			}
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			continue
		}
		fmt.Printf("🤖 %s\n", result.Output)
		fmt.Printf("   tools: %d | tokens: %d | took: %v\n",
			result.ToolCalls, result.TokenUsage.Total, result.Duration)
	}

	fmt.Println("\n✅ MCP integration demo completed")
}
