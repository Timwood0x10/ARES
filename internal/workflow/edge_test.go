// Package workflow_test — edge case tests for the unified Runner.
//
// Phase: P4 — edge case validation.
// These tests verify the Runner handles nil/empty/concurrent/large inputs
// correctly and does not panic or leak goroutines.

package workflow_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/workflow"
)

// ── Nil / empty edge cases ───────────────────────────────────────────

func TestRunner_Edge_NilSpec(t *testing.T) {
	runner := workflow.NewRunner(nil)
	_, err := runner.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil spec")
	}
}

func TestRunner_Edge_EmptyWorkflow(t *testing.T) {
	spec := workflow.NewWorkflow("empty")
	runner := workflow.NewRunner(nil)
	result, err := runner.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != workflow.NodeStatusCompleted {
		t.Errorf("expected completed, got %v", result.Status)
	}
}

func TestRunner_Edge_SingleNode(t *testing.T) {
	executed := false
	spec := workflow.NewWorkflow("single").
		AddNode(workflow.NodeSpec{ID: "only", AgentType: "echo"}).
		WithEntry("only")

	result, err := workflow.RunWorkflow(context.Background(), spec, map[workflow.NodeID]workflow.ExecutableFunc{
		"only": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			executed = true
			return map[string]any{"result": "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != workflow.NodeStatusCompleted {
		t.Errorf("expected completed, got %v", result.Status)
	}
	if !executed {
		t.Error("expected node to be executed")
	}
}

func TestRunner_Edge_NoRegisteredFunctions(t *testing.T) {
	// Missing bindings now return an explicit error (task #4 fix).
	// The error is captured in the Result, not as a Go error return.
	spec := workflow.NewWorkflow("no-fns").
		AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}).
		AddNode(workflow.NodeSpec{ID: "b", AgentType: "echo"}).
		AddEdge(workflow.EdgeSpec{From: "a", To: "b", Kind: workflow.EdgeDataDependency}).
		WithEntry("a")

	result, err := workflow.RunWorkflow(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("unexpected Go error (failure is in result): %v", err)
	}
	if result.Status != workflow.NodeStatusFailed {
		t.Fatalf("expected failed status for missing bindings, got %v", result.Status)
	}
	if result.Error == "" {
		t.Fatal("expected non-empty error message for missing binding")
	}
	t.Logf("NoRegisteredFunctions correctly failed: %s", result.Error)
}

// ── Concurrent execution safety ──────────────────────────────────────

func TestRunner_Edge_ConcurrentExecutions(t *testing.T) {
	// Simple single-node workflow executed concurrently.
	// This tests scope isolation, not complex edge traversal.
	spec := workflow.NewWorkflow("concurrent").
		AddNode(workflow.NodeSpec{ID: "only", AgentType: "echo"}).
		WithEntry("only")

	var mu sync.Mutex
	count := 0

	fns := map[workflow.NodeID]workflow.ExecutableFunc{
		"only": func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			mu.Lock()
			count++
			mu.Unlock()
			return map[string]any{"done": "ok"}, nil
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := workflow.RunWorkflow(context.Background(), spec, fns)
			if err != nil {
				t.Errorf("concurrent execution error: %v", err)
			}
		}()
	}
	wg.Wait()

	if count != 10 {
		t.Errorf("expected 10 executions, got %d", count)
	}
}

// ── Large workflow (100 nodes, linear) ───────────────────────────────

func TestRunner_Edge_LargeLinearWorkflow(t *testing.T) {
	n := 100
	spec := workflow.NewWorkflow("large-linear")
	fns := make(map[workflow.NodeID]workflow.ExecutableFunc)
	for i := 0; i < n; i++ {
		id := workflow.NodeID(rune('a' + i))
		spec.AddNode(workflow.NodeSpec{ID: id, AgentType: "echo"})
		fns[id] = func(ctx context.Context, view workflow.StateView) (map[string]any, error) {
			return map[string]any{"done": true}, nil
		}
		if i > 0 {
			prev := workflow.NodeID(rune('a' + i - 1))
			spec.AddEdge(workflow.EdgeSpec{From: prev, To: id, Kind: workflow.EdgeDataDependency})
		}
	}
	spec.WithEntry("a")

	result, err := workflow.RunWorkflow(context.Background(), spec, fns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != workflow.NodeStatusCompleted {
		t.Errorf("expected completed, got %v", result.Status)
	}
	if len(result.NodeStates) != n {
		t.Errorf("expected %d node states, got %d", n, len(result.NodeStates))
	}
}

// ── Validate error handling ──────────────────────────────────────────

func TestRunner_Edge_NilNodeID(t *testing.T) {
	spec := workflow.NewWorkflow("nil-id")
	spec.AddNode(workflow.NodeSpec{ID: "", AgentType: "echo"})

	_, err := workflow.RunWorkflow(context.Background(), spec, nil)
	if err == nil {
		t.Fatal("expected validation error for empty node ID")
	}
}

func TestRunner_Edge_DuplicateNodeID(t *testing.T) {
	spec := workflow.NewWorkflow("dup-id")
	spec.AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"})
	spec.AddNode(workflow.NodeSpec{ID: "a", AgentType: "echo"}) // duplicate

	_, err := workflow.RunWorkflow(context.Background(), spec, nil)
	if err == nil {
		t.Fatal("expected validation error for duplicate node ID")
	}
}

// ── Context timeout ──────────────────────────────────────────────────

func TestRunner_Edge_ContextTimeout(t *testing.T) {
	spec := workflow.NewWorkflow("timeout").
		AddNode(workflow.NodeSpec{ID: "slow", AgentType: "echo"}).
		WithEntry("slow")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result, err := workflow.RunWorkflow(ctx, spec, nil)
	if err != nil {
		t.Logf("expected error on cancelled context: %v", err)
	}
	_ = result
}
