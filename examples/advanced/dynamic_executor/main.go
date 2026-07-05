// Package main demonstrates DynamicExecutor API and ApplyMode configuration.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

const StepID1 = "step1"

func main() {
	ctx := context.Background()

	// Create initial DAG: step1 → step2 → step3.
	initialSteps := []*engine.Step{
		{ID: StepID1, Name: "Print Hello"},
		{ID: "step2", Name: "Print World", DependsOn: []string{StepID1}},
		{ID: "step3", Name: "Print Done", DependsOn: []string{"step2"}},
	}

	dag, err := engine.NewMutableDAG(initialSteps)
	if err != nil {
		log.Fatalf("failed to create DAG: %v", err)
	}

	// Demonstrate MutableDAG operations before execution.
	fmt.Println("=== MutableDAG Operations ===")
	fmt.Printf("Initial nodes: %d\n", dag.NodeCount())
	fmt.Printf("Initial edges: %d\n", dag.EdgeCount())

	order, _ := dag.GetExecutionOrder()
	fmt.Printf("Execution order: %v\n\n", order)

	// Add a parallel step: step1 → step4.
	err = dag.AddNode(ctx, &engine.Step{
		ID:        "step4",
		Name:      "Parallel Step",
		DependsOn: []string{StepID1},
	})
	if err != nil {
		log.Fatalf("failed to add step4: %v", err)
	}

	order, _ = dag.GetExecutionOrder()
	fmt.Printf("After adding step4: %v\n", order)
	fmt.Printf("Nodes: %d, Edges: %d\n\n", dag.NodeCount(), dag.EdgeCount())

	// Demonstrate DynamicExecutor creation with different modes.
	fmt.Println("=== DynamicExecutor Modes ===")

	executorCheckpoint := engine.NewDynamicExecutor(nil, engine.ApplyAtCheckpoint)
	fmt.Printf("Checkpoint mode executor: %v\n", executorCheckpoint != nil)

	executorImmediate := engine.NewDynamicExecutor(nil, engine.ApplyImmediate)
	fmt.Printf("Immediate mode executor: %v\n\n", executorImmediate != nil)

	// Demonstrate ExecutorOption pattern.
	executorWithOpts := engine.NewDynamicExecutor(
		nil,
		engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(5),
		engine.WithStepTimeout(60*time.Second),
	)
	fmt.Printf("Executor with options: %v\n\n", executorWithOpts != nil)

	// Demonstrate validation.
	fmt.Println("=== Validation ===")
	_, err = executorCheckpoint.ExecuteDynamic(ctx, nil, "input", dag)
	fmt.Printf("nil workflow: %v\n", err)

	_, err = executorCheckpoint.ExecuteDynamic(ctx, &engine.Workflow{ID: "test"}, "input", nil)
	fmt.Printf("nil DAG: %v\n", err)

	// Demonstrate cycle detection.
	fmt.Println("\n=== Cycle Detection ===")
	err = dag.AddEdge(ctx, "step3", "step1")
	if err != nil {
		fmt.Printf("Cycle detected (expected): %v\n", err)
	}

	fmt.Println("\nDynamicExecutor example completed successfully!")
}
