// Package main demonstrates different scheduling strategies.
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
	g := newSchedulerGraph("fifo-example", &executionOrder,
		[]timingNode{
			{"node1", 50 * time.Millisecond},
			{"node2", 30 * time.Millisecond},
			{"node3", 20 * time.Millisecond},
		},
		[]stringEdge{
			{"node1", "node2"},
			{"node2", "node3"},
		},
		"node1",
		nil, nil,
	)

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
	g := newSchedulerGraph("priority-example", &executionOrder,
		[]timingNode{
			{"low_priority", 50 * time.Millisecond},
			{"high_priority", 30 * time.Millisecond},
			{"medium_priority", 40 * time.Millisecond},
		},
		[]stringEdge{
			{"low_priority", "medium_priority"},
			{"high_priority", "medium_priority"},
		},
		"low_priority",
		wfgraph.NewPriorityScheduler(map[string]int{
			"low_priority":    1,
			"medium_priority": 5,
			"high_priority":   10,
		}),
		nil,
	)

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
	g := newSchedulerGraph("sjf-example", &executionOrder,
		[]timingNode{
			{"slow_job", 100 * time.Millisecond},
			{"fast_job", 20 * time.Millisecond},
			{"medium_job", 50 * time.Millisecond},
		},
		[]stringEdge{
			{"slow_job", "medium_job"},
			{"fast_job", "medium_job"},
		},
		"slow_job",
		wfgraph.NewShortJobScheduler(map[string]int{
			"slow_job":   100,
			"fast_job":   20,
			"medium_job": 50,
		}),
		nil,
	)

	response, err := service.Execute(context.Background(), g, &graph.ExecuteRequest{
		GraphID: "sjf-example",
	})
	if err != nil {
		log.Fatalf("execution failed: %v", err)
	}

	fmt.Printf("Execution order: %v\n", executionOrder)
	fmt.Printf("Duration: %v\n", response.Duration)
}

type timingNode struct {
	id       string
	duration time.Duration
}

type stringEdge struct {
	from string
	to   string
}

func newSchedulerGraph(id string, order *[]string, nodes []timingNode, edges []stringEdge, start string, scheduler wfgraph.Scheduler, limiter wfgraph.Scheduler) *wfgraph.Graph {
	g, err := wfgraph.NewGraph(id)
	if err != nil {
		log.Fatalf("failed to create graph: %v", err)
	}
	for _, n := range nodes {
		fn, err := wfgraph.NewFuncNode(n.id, func(ctx context.Context, state *wfgraph.State) error {
			fmt.Printf("  - Executing %s (estimated %v)\n", n.id, n.duration)
			time.Sleep(n.duration)
			*order = append(*order, n.id)
			return nil
		})
		if err != nil {
			log.Fatalf("failed to create node: %v", err)
		}
		if _, err = g.Node(n.id, fn); err != nil {
			log.Fatalf("failed to add node: %v", err)
		}
	}
	for _, e := range edges {
		if _, err = g.Edge(e.from, e.to); err != nil {
			log.Fatalf("failed to add edge: %v", err)
		}
	}
	if scheduler != nil {
		if _, err = g.SetScheduler(scheduler); err != nil {
			log.Fatalf("failed to set scheduler: %v", err)
		}
	}
	if _, err = g.Start(start); err != nil {
		log.Fatalf("failed to set start: %v", err)
	}
	return g
}
