// Package main demonstrates conditional branching in graphs.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/goagent/api/service/graph"
	"github.com/Timwood0x10/goagent/internal/observability"
	wfgraph "github.com/Timwood0x10/goagent/internal/workflow/graph"
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

	// Build a graph with conditional branches
	g := wfgraph.NewGraph("conditional-example").
		Node("check_status", wfgraph.NewFuncNode("check_status", func(ctx context.Context, state *wfgraph.State) error {
			fmt.Println("Checking status...")
			// Simulate status check
			status := "ok"
			state.Set("status", status)
			return nil
		})).
		Node("success_handler", wfgraph.NewFuncNode("success_handler", func(ctx context.Context, state *wfgraph.State) error {
			fmt.Println("✓ Handling success case")
			state.Set("result", "success")
			return nil
		})).
		Node("error_handler", wfgraph.NewFuncNode("error_handler", func(ctx context.Context, state *wfgraph.State) error {
			fmt.Println("✗ Handling error case")
			state.Set("result", "error")
			return nil
		})).
		Node("fallback_handler", wfgraph.NewFuncNode("fallback_handler", func(ctx context.Context, state *wfgraph.State) error {
			fmt.Println("⚠ Using fallback handler")
			state.Set("result", "fallback")
			return nil
		})).
		// Conditional edges
		Edge("check_status", "success_handler", wfgraph.IfFunc(func(s *wfgraph.State) bool {
			val, _ := s.Get("status")
			status, ok := val.(string)
			return ok && status == "ok"
		})).
		Edge("check_status", "error_handler", wfgraph.IfFunc(func(s *wfgraph.State) bool {
			val, _ := s.Get("status")
			status, ok := val.(string)
			return ok && status == "error"
		})).
		Edge("check_status", "fallback_handler", wfgraph.IfFunc(func(s *wfgraph.State) bool {
			val, _ := s.Get("status")
			status, ok := val.(string)
			return !ok || (status != "ok" && status != "error")
		})).
		Start("check_status")

	// Execute graph
	request := &graph.ExecuteRequest{
		GraphID: "conditional-example",
	}

	response, err := service.Execute(context.Background(), g, request)
	if err != nil {
		log.Fatalf("execution failed: %v", err)
	}

	// Print results
	fmt.Printf("\nGraph ID: %s\n", response.GraphID)
	fmt.Printf("Duration: %v\n", response.Duration)
	fmt.Printf("Result: %v\n", response.State["result"])

	fmt.Println("\nConditional branching example completed successfully!")
}
