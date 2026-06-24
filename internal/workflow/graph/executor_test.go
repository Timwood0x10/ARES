package graph

import (
	"context"
	"testing"
	"time"
)

func TestExecuteWithNilGraph(t *testing.T) {
	state := NewState()
	_, err := (*Graph)(nil).Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error for nil graph")
	}
}

func TestExecuteWithNoStartNode(t *testing.T) {
	graph, err := NewGraph("test")
	if err != nil {
		t.Fatalf("NewGraph failed: %v", err)
	}
	state := NewState()
	_, err = graph.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error for graph without start node")
	}
}

func TestExecuteWithInvalidStartNode(t *testing.T) {
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
		t.Error("expected error for invalid start node")
	}
}

func TestExecuteWithNilScheduler(t *testing.T) {
	graph, err := NewGraph("test")
	if err != nil {
		t.Fatalf("NewGraph failed: %v", err)
	}
	_, err = graph.SetScheduler(nil)
	if err == nil {
		t.Error("expected error for nil scheduler")
	}
}

func TestExecuteWithComplexDAG(t *testing.T) {
	executionOrder := []string{}

	graph := buildTestGraph(t, "complex",
		nodeDef("start", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "start")
			state.Set("stage", "1")
			return nil
		}),
		nodeDef("branch1", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "branch1")
			return nil
		}),
		nodeDef("branch2", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "branch2")
			return nil
		}),
		nodeDef("merge", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "merge")
			return nil
		}),
		nodeDef("end", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "end")
			return nil
		}),
		edgeDef("start", "branch1"),
		edgeDef("start", "branch2"),
		edgeDef("branch1", "merge"),
		edgeDef("branch2", "merge"),
		edgeDef("merge", "end"),
		startDef("start"),
	)

	state := NewState()
	result, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.State == nil {
		t.Error("expected non-nil state")
	}

	expectedNodes := []string{"start", "branch1", "branch2", "merge", "end"}
	if len(executionOrder) != len(expectedNodes) {
		t.Errorf("expected %d nodes, got %d", len(expectedNodes), len(executionOrder))
	}

	if executionOrder[0] != "start" {
		t.Errorf("expected start first, got %s", executionOrder[0])
	}

	if executionOrder[len(executionOrder)-1] != "end" {
		t.Errorf("expected end last, got %s", executionOrder[len(executionOrder)-1])
	}
}

func TestExecuteWithMultipleConditions(t *testing.T) {
	executionOrder := []string{}

	graph := buildTestGraph(t, "multi-condition",
		nodeDef("check", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "check")
			state.Set("value", 5)
			return nil
		}),
		nodeDef("path1", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "path1")
			return nil
		}),
		nodeDef("path2", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "path2")
			return nil
		}),
		nodeDef("path3", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "path3")
			return nil
		}),
		condEdgeDef("check", "path1", IfFunc(func(s *State) bool {
			val, _ := s.Get("value")
			intVal, ok := val.(int)
			return ok && intVal < 5
		})),
		condEdgeDef("check", "path2", IfFunc(func(s *State) bool {
			val, _ := s.Get("value")
			intVal, ok := val.(int)
			return ok && intVal == 5
		})),
		condEdgeDef("check", "path3", IfFunc(func(s *State) bool {
			val, _ := s.Get("value")
			intVal, ok := val.(int)
			return ok && intVal > 5
		})),
		startDef("check"),
	)

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if len(executionOrder) != 2 {
		t.Errorf("expected 2 nodes, got %d: %v", len(executionOrder), executionOrder)
	}
	if executionOrder[0] != "check" {
		t.Errorf("expected check first, got %s", executionOrder[0])
	}
	if executionOrder[1] != "path2" {
		t.Errorf("expected path2 second, got %s", executionOrder[1])
	}
}

func TestExecuteWithCycleDetection(t *testing.T) {
	executionOrder := []string{}

	graph := buildTestGraph(t, "diamond",
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
		edgeDef("node1", "node2"),
		edgeDef("node1", "node3"),
		edgeDef("node2", "node3"),
		startDef("node1"),
	)

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	node2Count := 0
	node3Count := 0
	for _, node := range executionOrder {
		if node == "node2" {
			node2Count++
		}
		if node == "node3" {
			node3Count++
		}
	}

	if node2Count != 1 {
		t.Errorf("expected node2 to execute once, got %d times", node2Count)
	}
	if node3Count != 1 {
		t.Errorf("expected node3 to execute once, got %d times", node3Count)
	}

	node3Idx := -1
	for i, node := range executionOrder {
		if node == "node3" {
			node3Idx = i
			break
		}
	}
	for i, node := range executionOrder {
		if (node == "node1" || node == "node2") && i > node3Idx {
			t.Errorf("expected node3 to execute after %s", node)
		}
	}
}

func TestExecuteWithEmptyReadyQueue(t *testing.T) {
	graph := buildTestGraph(t, "empty",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			return nil
		}),
		startDef("node1"),
	)

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecuteWithStateMutation(t *testing.T) {
	graph := buildTestGraph(t, "mutation",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			state.Set("key1", "value1")
			state.Set("key2", 42)
			return nil
		}),
		nodeDef("node2", func(ctx context.Context, state *State) error {
			val1, _ := state.Get("key1")
			val2, _ := state.Get("key2")

			if val1 != "value1" {
				t.Errorf("expected key1 to be value1, got %v", val1)
			}
			if val2 != 42 {
				t.Errorf("expected key2 to be 42, got %v", val2)
			}

			state.Set("key3", "value3")
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

	val1, ok := result.State.Get("key1")
	if !ok || val1 != "value1" {
		t.Error("expected key1 in final state")
	}

	val2, ok := result.State.Get("key2")
	if !ok || val2 != 42 {
		t.Error("expected key2 in final state")
	}

	val3, ok := result.State.Get("key3")
	if !ok || val3 != "value3" {
		t.Error("expected key3 in final state")
	}
}

func TestExecuteWithDuration(t *testing.T) {
	graph := buildTestGraph(t, "duration",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			time.Sleep(50 * time.Millisecond)
			return nil
		}),
		startDef("node1"),
	)

	state := NewState()
	result, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.Duration < 50*time.Millisecond {
		t.Errorf("expected duration >= 50ms, got %v", result.Duration)
	}

	if result.Duration > 200*time.Millisecond {
		t.Errorf("expected duration < 200ms, got %v", result.Duration)
	}
}

func TestExecuteWithNilState(t *testing.T) {
	graph := buildTestGraph(t, "test",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			if state == nil {
				return nil
			}
			return nil
		}),
	)
	if _, err := graph.Start("node1"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_, err := graph.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil state")
	}
}

func TestExecuteConditionalEdgeMultiPredecessor(t *testing.T) {
	executionOrder := []string{}

	graph := buildTestGraph(t, "cond-multi-pred",
		nodeDef("start", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "start")
			return nil
		}),
		nodeDef("X1", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "X1")
			return nil
		}),
		nodeDef("X2", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "X2")
			return nil
		}),
		nodeDef("Y", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "Y")
			return nil
		}),
		edgeDef("start", "X1"),
		edgeDef("start", "X2"),
		condEdgeDef("X1", "Y", IfFunc(func(s *State) bool { return false })),
		condEdgeDef("X2", "Y", IfFunc(func(s *State) bool { return true })),
		startDef("start"),
	)

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	expectedNodes := map[string]bool{"start": true, "X1": true, "X2": true, "Y": true}
	for _, node := range executionOrder {
		delete(expectedNodes, node)
	}
	if len(expectedNodes) > 0 {
		t.Errorf("missing nodes in execution order: %v, got %v", expectedNodes, executionOrder)
	}

	yCount := 0
	for _, node := range executionOrder {
		if node == "Y" {
			yCount++
		}
	}
	if yCount != 1 {
		t.Errorf("expected Y to execute once, got %d times, order: %v", yCount, executionOrder)
	}

	yIdx := -1
	x1Idx := -1
	x2Idx := -1
	for i, node := range executionOrder {
		switch node {
		case "Y":
			yIdx = i
		case "X1":
			x1Idx = i
		case "X2":
			x2Idx = i
		}
	}
	if yIdx < x1Idx || yIdx < x2Idx {
		t.Errorf("Y (idx=%d) must execute after X1 (idx=%d) and X2 (idx=%d), order: %v",
			yIdx, x1Idx, x2Idx, executionOrder)
	}
}

func TestExecuteAllConditionalEdgesFalse(t *testing.T) {
	executionOrder := []string{}

	graph := buildTestGraph(t, "all-cond-false",
		nodeDef("start", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "start")
			return nil
		}),
		nodeDef("X1", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "X1")
			return nil
		}),
		nodeDef("X2", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "X2")
			return nil
		}),
		nodeDef("Y", func(ctx context.Context, state *State) error {
			executionOrder = append(executionOrder, "Y")
			return nil
		}),
		edgeDef("start", "X1"),
		edgeDef("start", "X2"),
		condEdgeDef("X1", "Y", IfFunc(func(s *State) bool { return false })),
		condEdgeDef("X2", "Y", IfFunc(func(s *State) bool { return false })),
		startDef("start"),
	)

	state := NewState()
	_, err := graph.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	for _, node := range executionOrder {
		if node == "Y" {
			t.Errorf("Y should not execute when all conditional edges are false, order: %v", executionOrder)
		}
	}

	if len(executionOrder) != 3 {
		t.Errorf("expected 3 nodes, got %d: %v", len(executionOrder), executionOrder)
	}
}
