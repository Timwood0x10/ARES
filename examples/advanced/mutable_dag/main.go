// Package main demonstrates MutableDAG with runtime node/edge mutations.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

func main() {
	ctx := context.Background()

	// Create initial DAG: A → B → C.
	initialSteps := []*engine.Step{
		{ID: "A", Name: "Step A"},
		{ID: "B", Name: "Step B", DependsOn: []string{"A"}},
		{ID: "C", Name: "Step C", DependsOn: []string{"B"}},
	}

	dag, err := engine.NewMutableDAG(initialSteps)
	if err != nil {
		log.Fatalf("failed to create mutable DAG: %v", err)
	}

	order, _ := dag.GetExecutionOrder()
	fmt.Printf("Initial order: %v\n", order)
	fmt.Printf("Version: %d\n\n", dag.Version())

	// Subscribe to graph events.
	events := dag.Subscribe()

	// Add node D with dependency on B: A → B → C, B → D.
	err = dag.AddNode(ctx, &engine.Step{
		ID:        "D",
		Name:      "Step D",
		DependsOn: []string{"B"},
	})
	if err != nil {
		log.Fatalf("failed to add node D: %v", err)
	}

	order, _ = dag.GetExecutionOrder()
	fmt.Printf("After adding D: %v\n", order)
	fmt.Printf("Version: %d\n", dag.Version())

	// Try to add a cycle: D → A (should fail).
	err = dag.AddEdge(ctx, "D", "A")
	if err != nil {
		fmt.Printf("AddEdge D→A rejected: %v\n", err)
	}

	// Remove node C (has no dependents, so removal succeeds).
	err = dag.RemoveNode(ctx, "C")
	if err != nil {
		fmt.Printf("RemoveNode C failed: %v\n", err)
	} else {
		order, _ = dag.GetExecutionOrder()
		fmt.Printf("After removing C: %v\n", order)
	}

	// Snapshot for safe concurrent reads.
	snapshot := dag.Snapshot()
	fmt.Printf("Snapshot nodes: %d, edges: %d\n", len(snapshot.Nodes), len(snapshot.Edges))

	// Drain events (in production, use GraphEventHub.Unsubscribe).
	for len(events) > 0 {
		<-events
	}

	fmt.Println("\nMutableDAG example completed successfully!")
}
