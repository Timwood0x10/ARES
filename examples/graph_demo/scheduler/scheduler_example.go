// Package main demonstrates different scheduling strategies.
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

	// Example 1: Default FIFO scheduler
	fmt.Println("=== Example 1: Default FIFO Scheduler ===")
	runWithDefaultScheduler(service)

	// Example 2: Priority scheduler
	fmt.Println("\n=== Example 2: Priority Scheduler ===")
	runWithPriorityScheduler(service)

	// Example 3: Short Job First scheduler
	fmt.Println("\n=== Example 3: Short Job First Scheduler ===")
	runWithShortJobScheduler(service)

	fmt.Println("\nAll scheduler examples completed successfully!")
}

func runWithDefaultScheduler(service *graph.Service) {
	executionOrder := []string{}

	g := wfgraph.NewGraph("fifo-example").
		Node("node1", createTimingNode("node1", 50*time.Millisecond, &executionOrder)).
		Node("node2", createTimingNode("node2", 30*time.Millisecond, &executionOrder)).
		Node("node3", createTimingNode("node3", 20*time.Millisecond, &executionOrder)).
		Edge("node1", "node2").
		Edge("node2", "node3").
		Start("node1")

	response, err := service.Execute(context.Background(), g, &graph.ExecuteRequest{
		GraphID: "fifo-example",
	})
	if err != nil {
		log.Fatalf("execution failed: %v", err)
	}

	fmt.Printf("Execution order: %v\n", executionOrder)
	fmt.Printf("Duration: %v\n", response.Duration)
}

func runWithPriorityScheduler(service *graph.Service) {
	executionOrder := []string{}

	g := wfgraph.NewGraph("priority-example").
		Node("low_priority", createTimingNode("low_priority", 50*time.Millisecond, &executionOrder)).
		Node("high_priority", createTimingNode("high_priority", 30*time.Millisecond, &executionOrder)).
		Node("medium_priority", createTimingNode("medium_priority", 40*time.Millisecond, &executionOrder)).
		Edge("low_priority", "medium_priority").
		Edge("high_priority", "medium_priority").
		SetScheduler(wfgraph.NewPriorityScheduler(map[string]int{
			"low_priority":    1,
			"medium_priority": 5,
			"high_priority":   10,
		})).
		Start("low_priority")

	response, err := service.Execute(context.Background(), g, &graph.ExecuteRequest{
		GraphID: "priority-example",
	})
	if err != nil {
		log.Fatalf("execution failed: %v", err)
	}

	fmt.Printf("Execution order: %v\n", executionOrder)
	fmt.Printf("Duration: %v\n", response.Duration)
}

func runWithShortJobScheduler(service *graph.Service) {
	executionOrder := []string{}

	g := wfgraph.NewGraph("sjf-example").
		Node("slow_job", createTimingNode("slow_job", 100*time.Millisecond, &executionOrder)).
		Node("fast_job", createTimingNode("fast_job", 20*time.Millisecond, &executionOrder)).
		Node("medium_job", createTimingNode("medium_job", 50*time.Millisecond, &executionOrder)).
		Edge("slow_job", "medium_job").
		Edge("fast_job", "medium_job").
		SetScheduler(wfgraph.NewShortJobScheduler(map[string]int{
			"slow_job":   100,
			"fast_job":   20,
			"medium_job": 50,
		})).
		Start("slow_job")

	response, err := service.Execute(context.Background(), g, &graph.ExecuteRequest{
		GraphID: "sjf-example",
	})
	if err != nil {
		log.Fatalf("execution failed: %v", err)
	}

	fmt.Printf("Execution order: %v\n", executionOrder)
	fmt.Printf("Duration: %v\n", response.Duration)
}

func createTimingNode(id string, duration time.Duration, order *[]string) *wfgraph.FuncNode {
	return wfgraph.NewFuncNode(id, func(ctx context.Context, state *wfgraph.State) error {
		fmt.Printf("  - Executing %s (estimated %v)\n", id, duration)
		time.Sleep(duration)
		*order = append(*order, id)
		return nil
	})
}
