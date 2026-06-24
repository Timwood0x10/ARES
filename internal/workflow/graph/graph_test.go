package graph

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
	graph, err := NewGraph("test-graph")
	if err != nil {
		t.Fatalf("NewGraph failed: %v", err)
	}
	if graph.id != "test-graph" {
		t.Errorf("expected test-graph, got %s", graph.id)
	}
	if graph.scheduler == nil {
		t.Error("expected default scheduler")
	}
}

func TestGraphBuilder(t *testing.T) {
	graph, err := NewGraph("test")
	if err != nil {
		t.Fatalf("NewGraph failed: %v", err)
	}
	_, err = graph.Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
		return nil
	}})
	if err != nil {
		t.Fatalf("Node failed: %v", err)
	}
	_, err = graph.Node("node2", &mockNode{id: "node2", executeFn: func(ctx context.Context, state *State) error {
		return nil
	}})
	if err != nil {
		t.Fatalf("Node failed: %v", err)
	}
	_, err = graph.Edge("node1", "node2")
	if err != nil {
		t.Fatalf("Edge failed: %v", err)
	}
	_, err = graph.Start("node1")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

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

	graph := buildTestGraph(t, "test",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node1")
			state.Set("node.node1", "result1")
			return nil
		}),
		nodeDef("node2", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node2")
			state.Set("node.node2", "result2")
			return nil
		}),
		edgeDef("node1", "node2"),
		startDef("node1"),
	)

	state := NewState()
	result, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.GraphID != "test" {
		t.Errorf("expected graph ID test, got %s", result.GraphID)
	}

	if len(executionOrder) != 2 {
		t.Errorf("expected 2 nodes executed, got %d", len(executionOrder))
	}
	if executionOrder[0] != "node1" {
		t.Errorf("expected node1 first, got %s", executionOrder[0])
	}
	if executionOrder[1] != "node2" {
		t.Errorf("expected node2 second, got %s", executionOrder[1])
	}

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

	graph := buildTestGraph(t, "test",
		nodeDef("check", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "check")
			state.Set("status", "ok")
			return nil
		}),
		nodeDef("success", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "success")
			return nil
		}),
		nodeDef("failure", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "failure")
			return nil
		}),
		condEdgeDef("check", "success", IfFunc(func(s *State) bool {
			val, _ := s.Get("status")
			status, ok := val.(string)
			return ok && status == "ok"
		})),
		condEdgeDef("check", "failure", IfFunc(func(s *State) bool {
			val, _ := s.Get("status")
			status, ok := val.(string)
			return !ok || status != "ok"
		})),
		startDef("check"),
	)

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

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

	graph := buildTestGraph(t, "test",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node1")
			return nil
		}),
		nodeDef("node2", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node2")
			return nil
		}),
		nodeDef("node3", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node3")
			return nil
		}),
		edgeDef("node1", "node3"),
		edgeDef("node2", "node3"),
		startDef("node1"),
	)

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

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
	graph := buildTestGraph(t, "test",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			return errors.New("node1 error")
		}),
		nodeDef("node2", func(ctx context.Context, state *State) error {
			return nil
		}),
		edgeDef("node1", "node2"),
		startDef("node1"),
	)

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestGraphWithPriorityScheduler(t *testing.T) {
	executionOrder := []string{}

	graph := buildTestGraph(t, "test",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node1")
			return nil
		}),
		nodeDef("node2", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node2")
			return nil
		}),
		nodeDef("node3", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "node3")
			return nil
		}),
		edgeDef("node1", "node3"),
		edgeDef("node2", "node3"),
	)

	_, err := graph.SetScheduler(NewPriorityScheduler(map[string]int{
		"node1": 1,
		"node2": 10,
		"node3": 5,
	}))
	if err != nil {
		t.Fatalf("SetScheduler failed: %v", err)
	}
	_, err = graph.Start("node1")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	state := NewState()
	_, err = graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

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
		graph, err := NewGraph("test")
		if err != nil {
			t.Fatalf("NewGraph failed: %v", err)
		}
		state := NewState()
		_, err = graph.Execute(context.Background(), state)
		if err == nil {
			t.Error("expected error for missing start node")
		}
	})

	t.Run("start node not found", func(t *testing.T) {
		graph, err := NewGraph("test")
		if err != nil {
			t.Fatalf("NewGraph failed: %v", err)
		}
		_, err = graph.Start("nonexistent")
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		state := NewState()
		_, err = graph.Execute(context.Background(), state)
		if err == nil {
			t.Error("expected error for nonexistent start node")
		}
	})
}

func TestGraphExecutionTimeout(t *testing.T) {
	graph := buildTestGraph(t, "test",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return nil
			}
		}),
		startDef("node1"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	state := NewState()
	_, err := graph.Execute(ctx, state)
	if err == nil {
		t.Error("expected timeout error")
	}
}

// Test helpers to replace method chaining with error handling.

type nodeDefn struct {
	id string
	fn func(context.Context, *State) error
}

type edgeDefn struct {
	from string
	to   string
	cond Condition
}

type startDefn struct {
	id string
}

func nodeDef(id string, fn func(context.Context, *State) error) nodeDefn {
	return nodeDefn{id: id, fn: fn}
}

func edgeDef(from, to string) edgeDefn {
	return edgeDefn{from: from, to: to}
}

func condEdgeDef(from, to string, cond Condition) edgeDefn {
	return edgeDefn{from: from, to: to, cond: cond}
}

func startDef(id string) startDefn {
	return startDefn{id: id}
}

func buildTestGraph(t *testing.T, id string, defs ...interface{}) *Graph {
	t.Helper()
	g, err := NewGraph(id)
	if err != nil {
		t.Fatalf("NewGraph failed: %v", err)
	}
	for _, d := range defs {
		switch v := d.(type) {
		case nodeDefn:
			_, err = g.Node(v.id, &mockNode{id: v.id, executeFn: v.fn})
		case edgeDefn:
			if v.cond != nil {
				_, err = g.Edge(v.from, v.to, v.cond)
			} else {
				_, err = g.Edge(v.from, v.to)
			}
		case startDefn:
			_, err = g.Start(v.id)
		}
		if err != nil {
			t.Fatalf("build failed at %T: %v", d, err)
		}
	}
	return g
}
