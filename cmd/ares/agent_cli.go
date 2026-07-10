// Package main — ARES agent CLI commands.
package main

import (
	"fmt"

	"github.com/Timwood0x10/ares/api/service/agent"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Agent management commands",
	Long:  `Create, list, and manage agents.`,
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Create a memory manager and agent service.
		memMgr, err := memory.NewMemoryManager(nil)
		if err != nil {
			return fmt.Errorf("create memory manager: %w", err)
		}
		svc, err := agent.New(memMgr)
		if err != nil {
			return fmt.Errorf("create agent service: %w", err)
		}

		agents, pagination, err := svc.ListAgents(cmd.Context(), nil)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}

		fmt.Printf("Agents (total: %d):\n", pagination.Total)
		for _, a := range agents {
			fmt.Printf("  - ID: %s  Status: %s  Created: %d\n", a.ID, a.Status, a.CreatedAt)
		}
		if len(agents) == 0 {
			fmt.Println("  (no agents)")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.AddCommand(agentListCmd)
}
