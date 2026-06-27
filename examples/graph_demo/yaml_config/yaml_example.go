// Package main demonstrates YAML-based graph configuration.

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_observability"
	"github.com/Timwood0x10/ares/internal/workflow/graphservice"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run yaml_example.go <config-file>")
		fmt.Println("Example: go run yaml_example.go simple_workflow.yaml")
		os.Exit(1)
	}

	configPath := os.Args[1]

	// Read YAML configuration.
	configData, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("failed to read config file: %v", err)
	}

	// Parse configuration.
	graphConfig, err := graphservice.ParseGraphConfig(configData)
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	fmt.Printf("=== Loading graph from YAML: %s ===\n", configPath)
	fmt.Printf("Graph ID: %s\n", graphConfig.Graph.ID)
	fmt.Printf("Start Node: %s\n", graphConfig.Graph.StartNode)
	fmt.Printf("Nodes: %d\n", len(graphConfig.Graph.Nodes))
	fmt.Printf("Edges: %d\n", len(graphConfig.Graph.Edges))
	fmt.Println()

	// Build graph from configuration.
	builder := graphservice.NewGraphBuilder()
	g, err := builder.Build(graphConfig)
	if err != nil {
		log.Fatalf("failed to build graph: %v", err)
	}

	// Create service.
	service, err := graphservice.NewService(&graphservice.Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         ares_observability.NewLogTracer(nil),
	})
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}

	// Execute graphservice.
	fmt.Println("=== Executing graph ===")
	request := &graphservice.ExecuteRequest{
		GraphID: graphConfig.Graph.ID,
		State: map[string]any{
			"input": "test data from YAML config",
		},
	}

	response, err := service.Execute(context.Background(), g, request)
	if err != nil {
		log.Fatalf("execution failed: %v", err)
	}

	// Print results.
	fmt.Println()
	fmt.Println("=== Execution Results ===")
	fmt.Printf("Graph ID: %s\n", response.GraphID)
	fmt.Printf("Duration: %v\n", response.Duration)
	fmt.Printf("Final State:\n")
	for key, value := range response.State {
		fmt.Printf("  %s: %v\n", key, value)
	}

	fmt.Println()
	fmt.Println("YAML configuration example completed successfully!")
}
