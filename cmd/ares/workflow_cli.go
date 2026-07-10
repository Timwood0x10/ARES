package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	workflowSvc "github.com/Timwood0x10/ares/api/service/workflow"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
	"github.com/spf13/cobra"
)

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Workflow management commands",
	Long:  `Register, run, and inspect workflows.`,
}

var workflowRunCmd = &cobra.Command{
	Use:   "run <workflow-id> <input>",
	Short: "Execute a workflow and wait for completion",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		workflowID := args[0]
		input := args[1]

		svc, err := workflowSvc.NewService(&workflowSvc.Config{
			AgentRegistry:  engine.NewAgentRegistry(),
			RequestTimeout: 5 * time.Minute,
			MaxParallel:    10,
		})
		if err != nil {
			return fmt.Errorf("create workflow service: %w", err)
		}

		ch, err := svc.ExecuteStream(context.Background(), &core.WorkflowRequest{
			WorkflowID: workflowID,
			Input:      input,
		})
		if err != nil {
			return fmt.Errorf("execute workflow: %w", err)
		}

		fmt.Printf("Executing workflow %s...\n", workflowID)
		for ev := range ch {
			fmt.Printf("  [%s] %d", ev.Timestamp.Format(time.RFC3339), ev.Type)
			if ev.StepName != "" {
				fmt.Printf(" step=%s", ev.StepName)
			}
			if ev.Error != "" {
				fmt.Printf(" error=%s", ev.Error)
			}
			fmt.Println()
		}
		fmt.Println("Done.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(workflowCmd)
	workflowCmd.AddCommand(workflowRunCmd)
}
