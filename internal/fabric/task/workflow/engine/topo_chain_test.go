package engine

import (
	"context"
	"fmt"
	"testing"
)

// TestExecutionOrderCoversFullChain pins the Reconcile prerequisite: a serial
// chain grown by AddToolNode/AddNode must be fully covered by
// GetExecutionOrder — no node may be left out by in-degree drift.
func TestExecutionOrderCoversFullChain(t *testing.T) {
	dag, err := NewMutableDAG([]*Step{{ID: "root", AgentType: "echo"}})
	if err != nil {
		t.Fatal(err)
	}
	prev := "root"
	for i := 0; i < 70; i++ {
		id := fmt.Sprintf("b%d", i)
		step := &Step{ID: id, AgentType: "tool/echo", DependsOn: []string{prev}}
		if err := dag.AddNode(context.Background(), step); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
		prev = id
	}
	order, err := dag.GetExecutionOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 71 {
		t.Fatalf("execution order covers %d/71 nodes; missing: %v", len(order), missingIDs(order))
	}
}

func missingIDs(order []string) []string {
	have := map[string]bool{}
	for _, id := range order {
		have[id] = true
	}
	var miss []string
	for i := 0; i < 70; i++ {
		if !have[fmt.Sprintf("b%d", i)] {
			miss = append(miss, fmt.Sprintf("b%d", i))
		}
	}
	if !have["root"] {
		miss = append(miss, "root")
	}
	return miss
}
