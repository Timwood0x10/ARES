// Package main demonstrates basic graph usage.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"goagentx/api/service/graph"
	"goagentx/internal/observability"
	wfgraph "goagentx/internal/workflow/graph"
)

func main() {
	// Create graph service
	service, err := graph.NewService(&graph.Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         observability.NewLogTracer(nil),
	})
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}

	// Build a simple graph
	g := wfgraph.NewGraph("basic-example").
		Node("step1", wfgraph.NewFuncNode("step1", func(ctx context.Context, state *wfgraph.State) error {
			fmt.Println("Executing step1")
			state.Set("step1_result", "done")
			return nil
		})).
		Node("step2", wfgraph.NewFuncNode("step2", func(ctx context.Context, state *wfgraph.State) error {
			fmt.Println("Executing step2")
			state.Set("step2_result", "done")
			return nil
		})).
		Node("step3", wfgraph.NewFuncNode("step3", func(ctx context.Context, state *wfgraph.State) error {
			fmt.Println("Executing step3")
			state.Set("step3_result", "done")
			return nil
		})).
		Edge("step1", "step2").
		Edge("step2", "step3").
		Start("step1")

	// Execute graph
	request := &graph.ExecuteRequest{
		GraphID: "basic-example",
		State: map[string]any{
			"input": "hello world",
		},
	}

	response, err := service.Execute(context.Background(), g, request)
	if err != nil {
		log.Fatalf("execution failed: %v", err)
	}

	// Print results
	fmt.Printf("Graph ID: %s\n", response.GraphID)
	fmt.Printf("Duration: %v\n", response.Duration)
	fmt.Printf("Final State: %v\n", response.State)

	fmt.Println("Basic example completed successfully!")
}
