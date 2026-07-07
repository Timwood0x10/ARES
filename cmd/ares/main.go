// ARES unified CLI — single entry point for all ARES commands.
//
// Usage:
//
//	ARES serve                         Start full agent monitoring (LLM + MCP + dashboard)
//	ARES demo                          Start console demo with simulated workload
//	ARES arena run <scenario>          Run chaos scenario
//	ARES arena validate <scenario>     Validate scenario
//	ARES arena list [dir]              List scenarios
//	ARES arena serve                   Start arena HTTP server
//	ARES arena survival                Run survival test
//	ARES arena inspect                 Inspect arena results
//	ARES flight inspect <taskID>       Inspect flight data
//	ARES flight replay <taskID>        Replay flight data
//	ARES mcp-null serve                Start minimal MCP null server (stdio)
//	ARES db migrate                    Run full DB migration
//	ARES db setup-test                 Setup test database
//	ARES db create-table               Create distilled_memories table
//	ARES db check-rls                  Check RLS policies
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ares",
	Short: "ARES — Agent Runtime & Evolution System",
	Long: `ARES is the unified CLI for the Agent Runtime & Evolution System.

It provides commands for running agents, managing databases,
inspecting flight data, running chaos engineering scenarios,
and debugging MCP servers.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
