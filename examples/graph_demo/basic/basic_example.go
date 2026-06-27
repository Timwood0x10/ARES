// Package main demonstrates basic graph usage.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/api/service/graph"
	"github.com/Timwood0x10/ares/internal/ares_observability"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

func main() {
	// Create graph service
	service, err := graph.NewService(&graph.Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         ares_observability.NewLogTracer(nil),
	})
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}

	// Build a simple graph
	g, err := wfgraph.NewGraph("basic-example")
	if err != nil {
		log.Fatalf("failed to create graph: %v", err)
	}
	n1, err := wfgraph.NewFuncNode("step1", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("Executing step1")
		state.Set("step1_result", "done")
		return nil
	})
	if err != nil {
		log.Fatalf("failed to create node: %v", err)
	}
	n2, err := wfgraph.NewFuncNode("step2", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("Executing step2")
		state.Set("step2_result", "done")
		return nil
	})
	if err != nil {
		log.Fatalf("failed to create node: %v", err)
	}
	n3, err := wfgraph.NewFuncNode("step3", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("Executing step3")
		state.Set("step3_result", "done")
		return nil
	})
	if err != nil {
		log.Fatalf("failed to create node: %v", err)
	}
	_, err = g.Node("step1", n1)
	if err != nil {
		log.Fatalf("failed to add node: %v", err)
	}
	_, err = g.Node("step2", n2)
	if err != nil {
		log.Fatalf("failed to add node: %v", err)
	}
	_, err = g.Node("step3", n3)
	if err != nil {
		log.Fatalf("failed to add node: %v", err)
	}
	_, err = g.Edge("step1", "step2")
	if err != nil {
		log.Fatalf("failed to add edge: %v", err)
	}
	_, err = g.Edge("step2", "step3")
	if err != nil {
		log.Fatalf("failed to add edge: %v", err)
	}
	_, err = g.Start("step1")
	if err != nil {
		log.Fatalf("failed to set start: %v", err)
	}

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
