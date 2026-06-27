// Package main demonstrates conditional branching in graphs.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_observability"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
	"github.com/Timwood0x10/ares/internal/workflow/graphservice"
)

func main() {
	// Create graph service
	service, err := graphservice.NewService(&graphservice.Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         ares_observability.NewLogTracer(nil),
	})
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}

	// Build a graph with conditional branches
	g, err := wfgraph.NewGraph("conditional-example")
	if err != nil {
		log.Fatalf("failed to create graph: %v", err)
	}

	addNodeFn := func(id string, fn func(context.Context, *wfgraph.State) error) {
		n, err := wfgraph.NewFuncNode(id, fn)
		if err != nil {
			log.Fatalf("failed to create node: %v", err)
		}
		if _, err = g.Node(id, n); err != nil {
			log.Fatalf("failed to add node: %v", err)
		}
	}

	addNodeFn("check_status", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("Checking status...")
		state.Set("status", "ok")
		return nil
	})
	addNodeFn("success_handler", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("✓ Handling success case")
		state.Set("result", "success")
		return nil
	})
	addNodeFn("error_handler", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("✗ Handling error case")
		state.Set("result", "error")
		return nil
	})
	addNodeFn("fallback_handler", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("⚠ Using fallback handler")
		state.Set("result", "fallback")
		return nil
	})

	mustOp := func(_ *wfgraph.Graph, err error) {
		if err != nil {
			log.Fatalf("graph operation failed: %v", err)
		}
	}
	// Conditional edges
	mustOp(g.Edge("check_status", "success_handler", wfgraph.IfFunc(func(s *wfgraph.State) bool {
		val, _ := s.Get("status")
		status, ok := val.(string)
		return ok && status == "ok"
	})))
	mustOp(g.Edge("check_status", "error_handler", wfgraph.IfFunc(func(s *wfgraph.State) bool {
		val, _ := s.Get("status")
		status, ok := val.(string)
		return ok && status == "error"
	})))
	mustOp(g.Edge("check_status", "fallback_handler", wfgraph.IfFunc(func(s *wfgraph.State) bool {
		val, _ := s.Get("status")
		status, ok := val.(string)
		return !ok || (status != "ok" && status != "error")
	})))
	mustOp(g.Start("check_status"))

	// Execute graph
	request := &graphservice.ExecuteRequest{
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
