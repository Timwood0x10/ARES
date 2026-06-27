package graph

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestExecuteWithLoopPluginMaxIterations(t *testing.T) {
	g := buildTestGraph(t, "loop-max",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			return nil
		}),
		startDef("node1"),
	)

	var callCount atomic.Int32
	origNode := g.nodes["node1"]
	g.nodes["node1"] = &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
		callCount.Add(1)
		return origNode.Execute(ctx, state)
	}}

	bus := runtime.NewPluginBus()
	loop := runtime.NewLoopPlugin("loop", runtime.LoopConfig{MaxIterations: 3})
	require.NoError(t, bus.Register(loop))
	require.NoError(t, bus.Start(context.Background()))
	_, err := g.SetPluginBus(bus)
	require.NoError(t, err)

	state := NewState()
	state.Set("__loop_iteration", 0)
	_, err = g.Execute(context.Background(), state)
	require.NoError(t, err)

	if n := callCount.Load(); n != 3 {
		t.Errorf("expected node1 to execute 3 times, got %d", n)
	}
}

func TestExecuteWithLoopPluginUntilCondition(t *testing.T) {
	g := buildTestGraph(t, "loop-until",
		nodeDef("counter", func(ctx context.Context, state *State) error {
			v, _ := state.Get("count")
			state.Set("count", v.(int)+1)
			return nil
		}),
		startDef("counter"),
	)

	bus := runtime.NewPluginBus()
	loop := runtime.NewLoopPlugin("loop", runtime.LoopConfig{
		UntilCondition: func(vars map[string]any) bool {
			c, ok := vars["count"].(int)
			return ok && c >= 5
		},
	})
	require.NoError(t, bus.Register(loop))
	require.NoError(t, bus.Start(context.Background()))
	_, err := g.SetPluginBus(bus)
	require.NoError(t, err)

	state := NewState()
	state.Set("count", 0)
	_, err = g.Execute(context.Background(), state)
	require.NoError(t, err)

	v, _ := state.Get("count")
	if n := v.(int); n != 5 {
		t.Errorf("expected count to reach 5, got %d", n)
	}
}

func TestExecuteWithLoopPluginNoPlugin(t *testing.T) {
	// Without a LoopPlugin, graph should execute once as usual.
	var callCount atomic.Int32
	g := buildTestGraph(t, "loop-none",
		nodeDef("node1", func(ctx context.Context, state *State) error {
			callCount.Add(1)
			return nil
		}),
		startDef("node1"),
	)

	state := NewState()
	_, err := g.Execute(context.Background(), state)
	require.NoError(t, err)

	if n := callCount.Load(); n != 1 {
		t.Errorf("expected node1 to execute once, got %d", n)
	}
}

func TestExecuteLifecycleEvents(t *testing.T) {
	g := buildTestGraph(t, "lifecycle",
		nodeDef("step1", func(ctx context.Context, state *State) error {
			state.Set("seen", "step1")
			return nil
		}),
		nodeDef("step2", func(ctx context.Context, state *State) error {
			state.Set("seen", "step2")
			return nil
		}),
		edgeDef("step1", "step2"),
		startDef("step1"),
	)

	bus := runtime.NewPluginBus()
	require.NoError(t, bus.Start(context.Background()))
	_, err := g.SetPluginBus(bus)
	require.NoError(t, err)

	var mu sync.Mutex
	var gotEvents []string
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	sub, err := bus.Subscribe(subCtx, ares_events.EventFilter{
		Types: []ares_events.EventType{
			runtime.EventWorkflowStarted,
			runtime.EventWorkflowCompleted,
			runtime.EventStepStarted,
			runtime.EventStepCompleted,
		},
	})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range sub {
			mu.Lock()
			gotEvents = append(gotEvents, string(evt.Type))
			mu.Unlock()
		}
	}()

	_, err = g.Execute(context.Background(), NewState())
	require.NoError(t, err)

	subCancel() // close subscriber channel
	<-done      // wait for goroutine to finish

	mu.Lock()
	defer mu.Unlock()

	expected := []string{
		"workflow.started",
		"step.started", "step.completed", // step1
		"step.started", "step.completed", // step2
		"workflow.completed",
	}
	require.Equal(t, len(expected), len(gotEvents), "got: %v", gotEvents)
	for i, typ := range expected {
		if gotEvents[i] != typ {
			t.Errorf("event[%d]: expected %s, got %s", i, typ, gotEvents[i])
		}
	}
}

type memCheckpointStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (s *memCheckpointStore) Save(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	s.data[key] = data
	return nil
}

func (s *memCheckpointStore) Load(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.data[key]
	if !ok {
		return nil, nil
	}
	return data, nil
}

func TestGraphCheckpointPlugin(t *testing.T) {
	g := buildTestGraph(t, "ckpt-graph",
		nodeDef("n1", func(ctx context.Context, state *State) error {
			state.Set("visited", "n1")
			return nil
		}),
		nodeDef("n2", func(ctx context.Context, state *State) error {
			state.Set("visited", "n2")
			return nil
		}),
		edgeDef("n1", "n2"),
		startDef("n1"),
	)

	store := &memCheckpointStore{}
	bus := runtime.NewPluginBus()
	cp := runtime.NewCheckpointPlugin("ckpt", store)
	require.NoError(t, bus.Register(cp))
	require.NoError(t, bus.Start(context.Background()))
	_, err := g.SetPluginBus(bus)
	require.NoError(t, err)

	_, err = g.Execute(context.Background(), NewState())
	require.NoError(t, err)

	// Verify checkpoint was saved for the graph execution.
	data, err := store.Load(context.Background(), "checkpoint/ckpt-graph")
	require.NoError(t, err)
	require.NotNil(t, data)

	var ckpt runtime.ExperienceCheckpoint
	require.NoError(t, json.Unmarshal(data, &ckpt))
	require.Equal(t, "ckpt-graph", ckpt.ExecutionID)
	require.Len(t, ckpt.StepStates, 2)
	// Both steps should be completed.
	for _, ss := range ckpt.StepStates {
		require.Equal(t, runtime.StepStatusCompleted, ss.Status)
	}
}

func TestExecuteFromCheckpoint_SkipsCompletedNodes(t *testing.T) {
	g := buildTestGraph(t, "resume-graph",
		nodeDef("n1", func(ctx context.Context, state *State) error {
			v, _ := state.Get("order")
			state.Set("order", append(v.([]string), "n1"))
			return nil
		}),
		nodeDef("n2", func(ctx context.Context, state *State) error {
			v, _ := state.Get("order")
			state.Set("order", append(v.([]string), "n2"))
			return nil
		}),
		nodeDef("n3", func(ctx context.Context, state *State) error {
			v, _ := state.Get("order")
			state.Set("order", append(v.([]string), "n3"))
			return nil
		}),
		edgeDef("n1", "n2"),
		edgeDef("n2", "n3"),
		startDef("n1"),
	)

	// First execution.
	state := NewState()
	state.Set("order", []string{})
	_, err := g.Execute(context.Background(), state)
	require.NoError(t, err)

	order, _ := state.Get("order")
	require.Equal(t, []string{"n1", "n2", "n3"}, order)

	// Second execution resume from checkpoint: n1 and n2 already completed.
	state2 := NewState()
	state2.Set("order", []string{"n1", "n2"})
	_, err = g.ExecuteFromCheckpoint(context.Background(), state2, []string{"n1", "n2"})
	require.NoError(t, err)

	order2, _ := state2.Get("order")
	// Only n3 should execute (successors of n2 resume with decremented in-degree).
	require.Equal(t, []string{"n1", "n2", "n3"}, order2)
}

func TestExecuteFromCheckpoint_AllNodesCompleted(t *testing.T) {
	g := buildTestGraph(t, "resume-all-done",
		nodeDef("n1", func(ctx context.Context, state *State) error {
			return nil
		}),
		startDef("n1"),
	)

	state := NewState()
	_, err := g.ExecuteFromCheckpoint(context.Background(), state, []string{"n1"})
	require.NoError(t, err)
}

func TestGraphSetExecutionCollector_Nil(t *testing.T) {
	g, err := NewGraph("test-collector-nil")
	require.NoError(t, err)
	_, err = g.SetExecutionCollector(nil)
	require.Error(t, err)
}

func TestGraphSetExecutionCollector_NilGraph(t *testing.T) {
	_, err := (*Graph)(nil).SetExecutionCollector(runtime.NewExecutionCollector("exec-1"))
	require.Error(t, err)
}

func TestGraphRouterRecordsToCollector(t *testing.T) {
	// Verifies that when a NodeRouter is set and an ExecutionCollector is
	// attached, route decisions are recorded in the collector.
	g := buildTestGraph(t, "route-record",
		nodeDef("n1", func(ctx context.Context, state *State) error {
			return nil
		}),
		nodeDef("n2", func(ctx context.Context, state *State) error {
			return nil
		}),
		edgeDef("n1", "n2"),
		startDef("n1"),
	)

	collector := runtime.NewExecutionCollector("exec-route-record")
	_, err := g.SetExecutionCollector(collector)
	require.NoError(t, err)

	_, err = g.SetRouter(func(ctx context.Context, currentNodeID string, state *State) string {
		if currentNodeID == "n1" {
			return "n2"
		}
		return ""
	})
	require.NoError(t, err)

	state := NewState()
	_, err = g.Execute(context.Background(), state)
	require.NoError(t, err)

	routes := collector.RouteHistory()
	require.Len(t, routes, 1)
	assert.Equal(t, "n1", routes[0].StepID)
	assert.Equal(t, "n2", routes[0].Decision)
	assert.Equal(t, "node-router", routes[0].Source)
}

func TestGraphPluginBusRouterRecordsToCollector(t *testing.T) {
	// Verifies that when a PluginBus-based RouterPlugin routes and an
	// ExecutionCollector is attached, route decisions are recorded.
	g := buildTestGraph(t, "route-pb-record",
		nodeDef("n1", func(ctx context.Context, state *State) error {
			return nil
		}),
		nodeDef("n2", func(ctx context.Context, state *State) error {
			return nil
		}),
		edgeDef("n1", "n2"),
		startDef("n1"),
	)

	_, err := g.SetExecutionCollector(runtime.NewExecutionCollector("exec-pb-record"))
	require.NoError(t, err)

	bus := runtime.NewPluginBus()
	require.NoError(t, bus.Register(runtime.NewExpressionRouter("test-router", []runtime.RouteRule{
		{FromStepID: "n1", ToStepID: "n2", Reason: "test route"},
	})))
	require.NoError(t, bus.Start(context.Background()))
	_, err = g.SetPluginBus(bus)
	require.NoError(t, err)

	state := NewState()
	_, err = g.Execute(context.Background(), state)
	require.NoError(t, err)

	routes := g.collector.RouteHistory()
	require.Len(t, routes, 1)
	assert.Equal(t, "n1", routes[0].StepID)
	assert.Equal(t, "n2", routes[0].Decision)
	assert.Equal(t, "test route", routes[0].Reason)
	assert.Equal(t, "expression", routes[0].Source)
}

func TestExecuteFromCheckpoint_EmptyExecuted(t *testing.T) {
	// Empty executed list behaves like a fresh Execute.
	var callCount atomic.Int32
	g := buildTestGraph(t, "resume-empty",
		nodeDef("n1", func(ctx context.Context, state *State) error {
			callCount.Add(1)
			return nil
		}),
		startDef("n1"),
	)

	state := NewState()
	_, err := g.ExecuteFromCheckpoint(context.Background(), state, nil)
	require.NoError(t, err)
	require.Equal(t, int32(1), callCount.Load())
}
