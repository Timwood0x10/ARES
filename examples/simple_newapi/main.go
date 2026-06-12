package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"strings"

	"goagentx/api/client"
)

func main() {
	log.Println("=== GoAgent Multi-Agent Workflow Example ===")

	// Step 1: Load config and create client
	goagentClient, err := client.NewClientFromDefaultPath()
	if err != nil {
		slog.Error("Failed to create client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := goagentClient.Close(context.Background()); err != nil {
			slog.Error("Failed to close GoAgent client", "error", err)
		}
	}()

	config := goagentClient.GetConfig()

	// Show configured agents
	log.Println("\n=== Configured Agents ===")
	for _, agent := range config.Agents.Sub {
		log.Printf("  - %s (%s): %s", agent.ID, agent.Type, agent.Name)
	}

	// User query
	userQuery := "Analyze the latest tech trends in AI and cloud computing, summarize key findings with priorities"
	log.Printf("\n=== User Query ===\n%s\n", userQuery)

	// Step 2: Create workflow client
	workflowClient, err := client.NewWorkflowClient(goagentClient)
	if err != nil {
		slog.Error("Failed to create workflow client", "error", err)
		os.Exit(1)
	}

	// Step 3: Load and execute workflow
	log.Println("\n=== Executing Workflow ===")
	workflowPath := "config/workflow.yaml"

	result, err := workflowClient.ExecuteFromFile(context.Background(), workflowPath, userQuery)
	if err != nil {
		slog.Error("Failed to execute workflow", "error", err)
		os.Exit(1)
	}

	// Step 4: Display results
	log.Println("\n=== Workflow Execution Result ===")
	log.Printf("Execution ID: %s", result.ExecutionID)
	log.Printf("Status: %s", result.Status)
	log.Printf("Duration: %v", result.Duration)
	log.Printf("Total Steps: %d", len(result.Steps))

	// Show each step result
	log.Println("\n=== Task Results ===")

	// Parse and display task results by category
	for _, step := range result.Steps {
		if step.Status != "completed" {
			continue
		}

		items := parseTaskResults(step.Output)
		if len(items) == 0 {
			continue
		}

		log.Printf("\n%s:", step.Name)

		for i, item := range items {
			if i >= 3 { // Only show top 3
				break
			}

			log.Printf("  %d. %s", i+1, item.Name)
			if item.Reason != "" {
				log.Printf("     Reason: %s", item.Reason)
			}
		}
	}

	// Count completed steps
	completedCount := 0
	for _, step := range result.Steps {
		if step.Status == "completed" {
			completedCount++
		}
	}

	// Show summary
	log.Printf("\nCompleted %d task steps in %.1f seconds", completedCount, result.Duration.Seconds())

	log.Println("\n=== Done! ===")
}

// parseTaskResults parses JSON output and extracts task result items.
func parseTaskResults(output string) []TaskResultItem {
	// Try to extract JSON from output
	jsonStart := strings.Index(output, "{")
	jsonEnd := strings.LastIndex(output, "}")

	if jsonStart == -1 || jsonEnd == -1 {
		return []TaskResultItem{}
	}

	jsonStr := output[jsonStart : jsonEnd+1]

	var result struct {
		Items []struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return []TaskResultItem{}
	}

	items := make([]TaskResultItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, TaskResultItem{
			Name:   item.Name,
			Reason: item.Reason,
		})
	}

	return items
}

// TaskResultItem represents a parsed task result item.
type TaskResultItem struct {
	Name   string
	Reason string
}
