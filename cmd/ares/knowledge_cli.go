package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var knowledgeCmd = &cobra.Command{
	Use:   "knowledge",
	Short: "Knowledge graph management (via HTTP API)",
	Long: `Knowledge graph commands are available via the running ARES HTTP API.

Usage:
  ares serve                  Start the HTTP server first
  curl localhost:PORT/api/v1/knowledge/build -d '{"goal":"..."}'
  curl localhost:PORT/api/v1/knowledge/context -d '{"goal":"...", "formats":["prompt"]}'`,
}

var knowledgeBuildCmd = &cobra.Command{
	Use:   "build <goal>",
	Short: "Build a knowledge graph (requires running ares serve)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(`knowledge build is available via the HTTP API.
Start the server with 'ares serve' first, then send a POST request:

  curl -X POST http://localhost:PORT/api/v1/knowledge/build \
    -H "Content-Type: application/json" \
    -d '{"goal":%q, "max_tokens":5000, "for_graph":3000}'`, args[0])
	},
}

func init() {
	rootCmd.AddCommand(knowledgeCmd)
	knowledgeCmd.AddCommand(knowledgeBuildCmd)
}
