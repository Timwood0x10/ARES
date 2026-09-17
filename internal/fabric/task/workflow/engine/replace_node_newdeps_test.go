package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReplaceNode_DifferentID_NewDepsBecomeEdges pins
// CRITICAL 1.1: a different-ID ReplaceNode whose replacement step DECLARES
// dependencies the old step did not have must grow those edges. The
// regression: the different-ID branch only migrated existing edges, so the
// new DependsOn entries never became DAG edges — GetExecutionOrder
// under-counted prerequisites and the replacement could be scheduled before
// its declared dependencies.
func TestReplaceNode_DifferentID_NewDepsBecomeEdges(t *testing.T) {
	m, err := NewMutableDAG([]*Step{
		makeStep("plan"),
		makeStep("tool_a", "plan"),
		makeStep("tool_b", "plan"),
		makeStep("analyze", "tool_a", "tool_b"),
	})
	require.NoError(t, err)

	// tool_a depends only on plan; the replacement ALSO declares tool_b —
	// an edge that never existed before (old tool_a had no tool_b edge to
	// migrate).
	replacement := makeStep("tool_a_recovery", "plan", "tool_b")
	require.NoError(t, m.ReplaceNode(context.Background(), "tool_a", replacement))

	// The declared dependency must be a real edge: tool_b → tool_a_recovery.
	dag := m.Snapshot()
	targets := dag.Edges["tool_b"]
	found := false
	for _, tgt := range targets {
		if tgt == "tool_a_recovery" {
			found = true
			break
		}
	}
	assert.True(t, found,
		"newly declared DependsOn (tool_b) must become an edge to the replacement, got tool_b edges %v", targets)

	// And the topological order must respect it: tool_a_recovery strictly
	// after tool_b.
	order, err := m.GetExecutionOrder()
	require.NoError(t, err)
	posRecovery, posToolB := -1, -1
	for i, id := range order {
		switch id {
		case "tool_a_recovery":
			posRecovery = i
		case "tool_b":
			posToolB = i
		}
	}
	require.NotEqual(t, -1, posRecovery)
	require.NotEqual(t, -1, posToolB)
	assert.Greater(t, posRecovery, posToolB,
		"the replacement must be ordered after its declared dependency (order: %v)", order)
}

// TestReplaceNode_DifferentID_MergesOldDependsOn pins the other half of the
// different-ID contract (P1): the rename path rewired the old node's INCOMING
// edges onto the replacement but stored newStep wholesale, so a replacement
// that did not re-declare the old prerequisites ended up with Edges ⊋
// DependsOn. ReadDeps returns DependsOn and feeds fabric.SetDependencies, so
// the compiled task lost its prerequisites and went READY early. The old
// step's DependsOn must be merged into the replacement's (union, deduped).
func TestReplaceNode_DifferentID_MergesOldDependsOn(t *testing.T) {
	m, err := NewMutableDAG([]*Step{
		makeStep("plan"),
		makeStep("fetch", "plan"),
		makeStep("analyze", "fetch"),
	})
	require.NoError(t, err)

	// The recovery replacement declares NO dependencies of its own — the
	// common case for a respawned agent step.
	replacement := makeStep("fetch_recovery")
	require.NoError(t, m.ReplaceNode(context.Background(), "fetch", replacement))

	deps := m.ReadDeps("fetch_recovery")
	assert.Contains(t, deps, "plan",
		"the old step's prerequisite must survive the rename (ReadDeps feeds fabric.SetDependencies), got %v", deps)

	// Edges and DependsOn must agree — no edge the dependency list omits.
	snap := m.Snapshot()
	declared := make(map[string]bool, len(deps))
	for _, d := range deps {
		declared[d] = true
	}
	for src, targets := range snap.Edges {
		for _, tgt := range targets {
			if tgt != "fetch_recovery" {
				continue
			}
			assert.True(t, declared[src],
				"edge %s→fetch_recovery has no matching DependsOn entry (Edges ⊋ DependsOn)", src)
		}
	}

	// The topological order must still place the replacement after plan.
	order, err := m.GetExecutionOrder()
	require.NoError(t, err)
	posRecovery, posPlan := -1, -1
	for i, id := range order {
		switch id {
		case "fetch_recovery":
			posRecovery = i
		case "plan":
			posPlan = i
		}
	}
	require.NotEqual(t, -1, posRecovery)
	require.NotEqual(t, -1, posPlan)
	assert.Greater(t, posRecovery, posPlan,
		"replacement must stay after its inherited prerequisite (order %v)", order)
}
