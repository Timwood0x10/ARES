// package graph - tests for graph execution.

package graph

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockNode is a simple mock node for testing.
type mockNode struct {
	id        string
	executeFn func(context.Context, *State) error
}

func (m *mockNode) Execute(ctx context.Context, state *State) error {
	return m.executeFn(ctx, state)
}

func (m *mockNode) ID() string {
	return m.id
}

func TestNewGraph(t *testing.T) {
	graph := NewGraph("test-graph")
	if graph.id != "test-graph" {
		t.Errorf("expected test-graph, got %s", graph.id)
	}
	if graph.scheduler == nil {
		t.Error("expected default scheduler")
	}
}

func TestGraphBuilder(t *testing.T) {
	graph := NewGraph("test").
		Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
			return nil
		}}).
		Node("node2", &mockNode{id: "node2", executeFn: func(ctx context.Context, state *State) error {
			return nil
		}}).
		Edge("node1", "node2").
		Start("node1")

	if graph.start != "node1" {
		t.Errorf("expected start node1, got %s", graph.start)
	}
	if len(graph.nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(graph.nodes))
	}
	if len(graph.edges["node1"]) != 1 {
		t.Errorf("expected 1 edge from node1, got %d", len(graph.edges["node1"]))
	}
}

func TestGraphExecution(t *testing.T) {
	executionOrder := []string{}

	graph := NewGraph("test").
		Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node1")
			state.Set("node.node1", "result1")
			return nil
		}}).
		Node("node2", &mockNode{id: "node2", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node2")
			state.Set("node.node2", "result2")
			return nil
		}}).
		Edge("node1", "node2").
		Start("node1")

	state := NewState()
	result, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.GraphID != "test" {
		t.Errorf("expected graph ID test, got %s", result.GraphID)
	}

	// Check execution order
	if len(executionOrder) != 2 {
		t.Errorf("expected 2 nodes executed, got %d", len(executionOrder))
	}
	if executionOrder[0] != "node1" {
		t.Errorf("expected node1 first, got %s", executionOrder[0])
	}
	if executionOrder[1] != "node2" {
		t.Errorf("expected node2 second, got %s", executionOrder[1])
	}

	// Check state
	val, ok := state.Get("node.node1")
	if !ok || val != "result1" {
		t.Error("expected node.node1 in state")
	}
	val, ok = state.Get("node.node2")
	if !ok || val != "result2" {
		t.Error("expected node.node2 in state")
	}
}

func TestGraphExecutionWithCondition(t *testing.T) {
	executionOrder := []string{}

	graph := NewGraph("test").
		Node("check", &mockNode{id: "check", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "check")
			state.Set("status", "ok")
			return nil
		}}).
		Node("success", &mockNode{id: "success", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "success")
			return nil
		}}).
		Node("failure", &mockNode{id: "failure", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "failure")
			return nil
		}}).
		Edge("check", "success", IfFunc(func(s *State) bool {
			val, _ := s.Get("status")
			status, ok := val.(string)
			return ok && status == "ok"
		})).
		Edge("check", "failure", IfFunc(func(s *State) bool {
			val, _ := s.Get("status")
			status, ok := val.(string)
			return !ok || status != "ok"
		})).
		Start("check")

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	// Check execution order - only check and success should execute
	if len(executionOrder) != 2 {
		t.Errorf("expected 2 nodes executed, got %d", len(executionOrder))
	}
	if executionOrder[0] != "check" {
		t.Errorf("expected check first, got %s", executionOrder[0])
	}
	if executionOrder[1] != "success" {
		t.Errorf("expected success second, got %s", executionOrder[1])
	}
}

func TestGraphExecutionWithMultipleParents(t *testing.T) {
	executionOrder := []string{}

	graph := NewGraph("test").
		Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node1")
			return nil
		}}).
		Node("node2", &mockNode{id: "node2", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node2")
			return nil
		}}).
		Node("node3", &mockNode{id: "node3", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node3")
			return nil
		}}).
		Edge("node1", "node3").
		Edge("node2", "node3").
		Start("node1")

	// This test ensures node3 is only executed once even though it has two parents
	// Note: Without proper dependency tracking, this test may fail
	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	// Count how many times node3 was executed
	node3Count := 0
	for _, node := range executionOrder {
		if node == "node3" {
			node3Count++
		}
	}

	if node3Count != 1 {
		t.Errorf("expected node3 to execute once, got %d times", node3Count)
	}
}

func TestGraphExecutionWithError(t *testing.T) {
	graph := NewGraph("test").
		Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
			return errors.New("node1 error")
		}}).
		Node("node2", &mockNode{id: "node2", executeFn: func(ctx context.Context, state *State) error {
			return nil
		}}).
		Edge("node1", "node2").
		Start("node1")

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestGraphWithPriorityScheduler(t *testing.T) {
	executionOrder := []string{}

	graph := NewGraph("test").
		Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node1")
			return nil
		}}).
		Node("node2", &mockNode{id: "node2", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node2")
			return nil
		}}).
		Node("node3", &mockNode{id: "node3", executeFn: func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node3")
			return nil
		}}).
		Edge("node1", "node3").
		Edge("node2", "node3").
		SetScheduler(NewPriorityScheduler(map[string]int{
			"node1": 1,
			"node2": 10,
			"node3": 5,
		})).
		Start("node1")

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	// With priority scheduler and in-degree tracking, both node1 and node2 have
	// in-degree 0 so both are ready at start. The priority scheduler picks
	// node2 (priority 10) before node1 (priority 1).
	if executionOrder[0] != "node2" {
		t.Errorf("expected node2 first (highest priority), got %s", executionOrder[0])
	}
}

func TestGraphValidation(t *testing.T) {
	t.Run("nil graph", func(t *testing.T) {
		state := NewState()
		_, err := (*Graph)(nil).Execute(context.Background(), state)
		if err == nil {
			t.Error("expected error for nil graph")
		}
	})

	t.Run("no start node", func(t *testing.T) {
		graph := NewGraph("test")
		state := NewState()
		_, err := graph.Execute(context.Background(), state)
		if err == nil {
			t.Error("expected error for missing start node")
		}
	})

	t.Run("start node not found", func(t *testing.T) {
		graph := NewGraph("test").Start("nonexistent")
		state := NewState()
		_, err := graph.Execute(context.Background(), state)
		if err == nil {
			t.Error("expected error for nonexistent start node")
		}
	})
}

func TestGraphExecutionTimeout(t *testing.T) {
	graph := NewGraph("test").
		Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return nil
			}
		}}).
		Start("node1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	state := NewState()
	_, err := graph.Execute(ctx, state)
	if err == nil {
		t.Error("expected timeout error")
	}
}
