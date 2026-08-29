// ARES unified CLI — single entry point for all ARES commands.
//
// Usage:
//
//	ARES serve                         Start full agent runtime (LLM + MCP + introspection)
//	ARES agent list                    List all registered agents
//	ARES arena run <scenario>          Run chaos scenario
//	ARES arena validate <scenario>     Validate scenario
//	ARES arena list [dir]              List scenarios
//	ARES arena serve                   Start arena HTTP server
//	ARES arena survival                Run survival test
//	ARES arena inspect                 Inspect arena results
//	ARES evolution run                 Run one evolution cycle
//	ARES evolution status              Show evolution system status
//	ARES flight inspect <taskID>       Inspect flight data
//	ARES flight replay <taskID>        Replay flight data
//	ARES knowledge build <goal>        Build a knowledge graph (via HTTP API)
//	ARES recall query <text>           Recall archived rounds by text
//	ARES recall round <N>              Recall one archived round
//	ARES evolution run [flags]         Run the GA evolution loop
//	ARES status                        Show runtime status
//	ARES init / run / bench            Scaffold, start dev runtime, run benchmarks
//	ARES mcp-null serve                Start minimal MCP null server (stdio)
//	ARES db migrate                    Run full DB migration
//	ARES db create-table               Create distilled_memories table
//	ARES db check-rls                  Check RLS policies
//	ARES version                       Show version
//	ARES doctor                        Diagnose environment
//	ARES status                        Show runtime status at a glance
//	ARES dashboard                     Open the runtime introspection panel
//	ARES init                          Scaffold new project
//	ARES run                           Run agent from config file
//	ARES bench                         Run benchmark
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
