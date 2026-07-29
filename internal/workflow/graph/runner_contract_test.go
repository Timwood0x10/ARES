package graph

import (
	"context"
	"testing"

	workflowcore "github.com/Timwood0x10/ares/internal/workflow"
)

func TestCompileBound_ExecutesNodesAndConditions(t *testing.T) {
	t.Parallel()

	graphDef, err := NewGraph("bound-conditions")
	if err != nil {
		t.Fatalf("NewGraph() error = %v", err)
	}
	calls := make(map[string]int)
	mustAddFuncNode(t, graphDef, "start", func(_ context.Context, state *State) error {
		calls["start"]++
		state.Set("selected", true)
		return nil
	})
	mustAddFuncNode(t, graphDef, "selected", func(_ context.Context, state *State) error {
		calls["selected"]++
		state.Set("selected_result", "done")
		return nil
	})
	mustAddFuncNode(t, graphDef, "rejected", func(_ context.Context, _ *State) error {
		calls["rejected"]++
		return nil
	})
	if _, err := graphDef.Edge("start", "selected", func(state *State) bool {
		value, _ := state.Get("selected")
		selected, _ := value.(bool)
		return selected
	}); err != nil {
		t.Fatalf("Edge(selected) error = %v", err)
	}
	if _, err := graphDef.Edge("start", "rejected", func(_ *State) bool { return false }); err != nil {
		t.Fatalf("Edge(rejected) error = %v", err)
	}
	if _, err := graphDef.Start("start"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	compiled, err := CompileBound(graphDef)
	if err != nil {
		t.Fatalf("CompileBound() error = %v", err)
	}
	result, err := workflowcore.NewRunner(compiled.Executor, compiled.Options...).ExecuteBound(
		context.Background(),
		compiled.Bound,
	)
	if err != nil {
		t.Fatalf("ExecuteBound() error = %v", err)
	}
	if result.Status != workflowcore.NodeStatusCompleted {
		t.Fatalf("result status = %q", result.Status)
	}
	if calls["selected"] != 1 || calls["rejected"] != 0 {
		t.Fatalf("calls = %#v", calls)
	}
	statuses := make(map[workflowcore.NodeID]workflowcore.NodeStatus)
	for _, state := range result.NodeStates {
		statuses[state.ID] = state.Status
	}
	if statuses["rejected"] != workflowcore.NodeStatusNotSelected {
		t.Fatalf("rejected status = %q", statuses["rejected"])
	}
}

func TestGraphExecute_UsesUnifiedRunnerState(t *testing.T) {
	t.Parallel()

	graphDef, err := NewGraph("public-execute")
	if err != nil {
		t.Fatalf("NewGraph() error = %v", err)
	}
	mustAddFuncNode(t, graphDef, "one", func(_ context.Context, state *State) error {
		input, _ := state.Get("input")
		state.Set("one", input)
		return nil
	})
	mustAddFuncNode(t, graphDef, "two", func(_ context.Context, state *State) error {
		value, _ := state.Get("one")
		state.Set("two", value)
		return nil
	})
	if _, err := graphDef.Edge("one", "two"); err != nil {
		t.Fatalf("Edge() error = %v", err)
	}
	if _, err := graphDef.Start("one"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	state := NewState()
	state.Set("input", "payload")

	result, err := graphDef.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result == nil || result.State == nil {
		t.Fatal("expected graph result state")
	}
	if value, exists := result.State.Get("two"); !exists || value != "payload" {
		t.Fatalf("two = %#v, exists = %v", value, exists)
	}
}

func mustAddFuncNode(
	t *testing.T,
	graphDef *Graph,
	id string,
	function func(context.Context, *State) error,
) {
	t.Helper()
	node, err := NewFuncNode(id, function)
	if err != nil {
		t.Fatalf("NewFuncNode(%q) error = %v", id, err)
	}
	if _, err := graphDef.Node(id, node); err != nil {
		t.Fatalf("Node(%q) error = %v", id, err)
	}
}
