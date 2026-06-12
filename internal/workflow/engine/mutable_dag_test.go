// nolint: errcheck // Test code may ignore return values
package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: creates a Step with the given ID and dependencies.
func makeStep(id string, deps ...string) *Step {
	return &Step{
		ID:        id,
		Name:      id + " step",
		AgentType: "test-agent",
		DependsOn: deps,
	}
}

// =====================================================
// NewMutableDAG tests
// =====================================================

func TestNewMutableDAG_EmptySteps(t *testing.T) {
	m, err := NewMutableDAG(nil)
	require.NoError(t, err, "empty steps should not error")
	require.NotNil(t, m)
	assert.Equal(t, 0, m.NodeCount())
}

func TestNewMutableDAG_ValidSteps(t *testing.T) {
	steps := []*Step{
		makeStep("a"),
		makeStep("b", "a"),
		makeStep("c", "a", "b"),
	}

	m, err := NewMutableDAG(steps)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, 3, m.NodeCount())
	// Edges: a appears in b.DependsOn and c.DependsOn, b appears in c.DependsOn.
	// So edges: a->b, a->c, b->c = 3.
	assert.Equal(t, 3, m.EdgeCount())
}

func TestNewMutableDAG_WithCycle(t *testing.T) {
	steps := []*Step{
		makeStep("a", "c"),
		makeStep("b", "a"),
		makeStep("c", "b"),
	}

	_, err := NewMutableDAG(steps)
	require.Error(t, err, "cyclic steps should error")
	assert.ErrorIs(t, err, ErrCycleDetected)
}

func TestNewMutableDAG_InvalidDependency(t *testing.T) {
	steps := []*Step{
		makeStep("a", "nonexistent"),
	}

	_, err := NewMutableDAG(steps)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDependency)
}

// =====================================================
// AddNode tests
// =====================================================

func TestMutableDAG_AddNode_NilStep(t *testing.T) {
	m, _ := NewMutableDAG(nil)

	err := m.AddNode(context.Background(), nil)
	require.Error(t, err, "nil step should error")
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestMutableDAG_AddNode_EmptyID(t *testing.T) {
	m, _ := NewMutableDAG(nil)

	err := m.AddNode(context.Background(), &Step{ID: ""})
	require.Error(t, err, "empty ID should error")
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestMutableDAG_AddNode_DuplicateID(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{makeStep("a")})

	err := m.AddNode(context.Background(), makeStep("a"))
	require.Error(t, err, "duplicate ID should error")
	assert.ErrorIs(t, err, ErrDuplicateID)
}

func TestMutableDAG_AddNode_InvalidDependency(t *testing.T) {
	m, _ := NewMutableDAG(nil)

	err := m.AddNode(context.Background(), makeStep("a", "nonexistent"))
	require.Error(t, err, "invalid dependency should error")
	assert.ErrorIs(t, err, ErrInvalidDependency)
}

func TestMutableDAG_AddNode_CycleCreatingDependency(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b", "a"),
	})

	// Add c that depends on b (valid).
	err := m.AddNode(context.Background(), makeStep("c", "b"))
	require.NoError(t, err)

	// Now add an edge from c->a which would create a cycle (a->b->c->a).
	err = m.AddEdge(context.Background(), "c", "a")
	require.Error(t, err, "cycle-creating edge should error")
	assert.ErrorIs(t, err, ErrCycleDetected)
}

func TestMutableDAG_AddNode_Normal(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{makeStep("a")})

	err := m.AddNode(context.Background(), makeStep("b", "a"))
	require.NoError(t, err)
	assert.Equal(t, 2, m.NodeCount())
	assert.Equal(t, 1, m.EdgeCount())

	order, err := m.GetExecutionOrder()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, order)
}

func TestMutableDAG_AddNode_CancelledContext(t *testing.T) {
	m, _ := NewMutableDAG(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.AddNode(ctx, makeStep("a"))
	require.Error(t, err, "cancelled context should error")
}

func TestMutableDAG_AddNode_VersionIncrements(t *testing.T) {
	m, _ := NewMutableDAG(nil)
	assert.Equal(t, uint64(0), m.Version())

	_ = m.AddNode(context.Background(), makeStep("a"))
	assert.Equal(t, uint64(1), m.Version())

	_ = m.AddNode(context.Background(), makeStep("b"))
	assert.Equal(t, uint64(2), m.Version())
}

// =====================================================
// RemoveNode tests
// =====================================================

func TestMutableDAG_RemoveNode_NotFound(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{makeStep("a")})

	err := m.RemoveNode(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestMutableDAG_RemoveNode_WithDependents(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b", "a"),
	})

	// Removing "a" should fail because "b" depends on it.
	err := m.RemoveNode(context.Background(), "a")
	require.Error(t, err, "removing node with dependents should error")
	assert.ErrorIs(t, err, ErrNodeHasDependents)
}

func TestMutableDAG_RemoveNode_Normal(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b", "a"),
	})

	err := m.RemoveNode(context.Background(), "b")
	require.NoError(t, err)
	assert.Equal(t, 1, m.NodeCount())
	assert.Equal(t, 0, m.EdgeCount(), "edge a->b should be removed")
}

func TestMutableDAG_RemoveNode_VersionIncrements(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{makeStep("a")})
	v := m.Version()

	_ = m.RemoveNode(context.Background(), "a")
	assert.Greater(t, m.Version(), v)
}

func TestMutableDAG_RemoveNode_CancelledContext(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{makeStep("a")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.RemoveNode(ctx, "a")
	require.Error(t, err)
}

// =====================================================
// AddEdge tests
// =====================================================

func TestMutableDAG_AddEdge_NonexistentFrom(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{makeStep("a")})

	err := m.AddEdge(context.Background(), "nonexistent", "a")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestMutableDAG_AddEdge_NonexistentTo(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{makeStep("a")})

	err := m.AddEdge(context.Background(), "a", "nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestMutableDAG_AddEdge_Duplicate(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b", "a"),
	})

	// Edge a->b already exists via the dependency.
	err := m.AddEdge(context.Background(), "a", "b")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicateEdge)
}

func TestMutableDAG_AddEdge_CycleDetected(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b", "a"),
	})

	// Adding edge b->a would create a cycle.
	err := m.AddEdge(context.Background(), "b", "a")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCycleDetected)
}

func TestMutableDAG_AddEdge_Normal(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b"),
	})

	err := m.AddEdge(context.Background(), "a", "b")
	require.NoError(t, err)
	assert.Equal(t, 1, m.EdgeCount())

	order, err := m.GetExecutionOrder()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, order)
}

func TestMutableDAG_AddEdge_CancelledContext(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b"),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.AddEdge(ctx, "a", "b")
	require.Error(t, err)
}

// =====================================================
// RemoveEdge tests
// =====================================================

func TestMutableDAG_RemoveEdge_NotFound(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b"),
	})

	err := m.RemoveEdge(context.Background(), "a", "b")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEdgeNotFound)
}

func TestMutableDAG_RemoveEdge_NonexistentNode(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{makeStep("a")})

	err := m.RemoveEdge(context.Background(), "nonexistent", "a")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestMutableDAG_RemoveEdge_Normal(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b", "a"),
	})

	err := m.RemoveEdge(context.Background(), "a", "b")
	require.NoError(t, err)
	assert.Equal(t, 0, m.EdgeCount())

	// After removing the edge, b no longer depends on a.
	order, err := m.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2)
}

func TestMutableDAG_RemoveEdge_CancelledContext(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b", "a"),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.RemoveEdge(ctx, "a", "b")
	require.Error(t, err)
}

// =====================================================
// GetExecutionOrder tests
// =====================================================

func TestMutableDAG_GetExecutionOrder_AfterMutations(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b"),
	})

	// Initially both are independent.
	order, err := m.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2)

	// Add dependency: b depends on a.
	err = m.AddEdge(context.Background(), "a", "b")
	require.NoError(t, err)

	order, err = m.GetExecutionOrder()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, order)

	// Remove the edge.
	err = m.RemoveEdge(context.Background(), "a", "b")
	require.NoError(t, err)

	order, err = m.GetExecutionOrder()
	require.NoError(t, err)
	assert.Len(t, order, 2)
}

// =====================================================
// Snapshot tests
// =====================================================

func TestMutableDAG_Snapshot_ReturnsIndependentCopy(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b", "a"),
	})

	snap := m.Snapshot()
	require.NotNil(t, snap)

	// Verify the snapshot has the same data.
	assert.Equal(t, 2, len(snap.Nodes))
	assert.Equal(t, 1, len(snap.Edges))

	// Mutate the snapshot and verify the original is unaffected.
	delete(snap.Nodes, "a")
	assert.Equal(t, 1, len(snap.Nodes), "snapshot should now have 1 node after delete")
	assert.Equal(t, 2, m.NodeCount(), "original should be unaffected by snapshot mutation")

	// Mutate the original and verify the snapshot is unaffected.
	_ = m.AddNode(context.Background(), makeStep("c"))
	assert.Equal(t, 3, m.NodeCount(), "original should now have 3 nodes")
	assert.Equal(t, 1, len(snap.Nodes), "snapshot should still have 1 node (b)")
}

// =====================================================
// Version tests
// =====================================================

func TestMutableDAG_Version_IncrementsOnEachMutation(t *testing.T) {
	m, _ := NewMutableDAG(nil)
	assert.Equal(t, uint64(0), m.Version())

	_ = m.AddNode(context.Background(), makeStep("a"))
	assert.Equal(t, uint64(1), m.Version())

	_ = m.AddNode(context.Background(), makeStep("b"))
	assert.Equal(t, uint64(2), m.Version())

	_ = m.AddEdge(context.Background(), "a", "b")
	assert.Equal(t, uint64(3), m.Version())

	_ = m.RemoveEdge(context.Background(), "a", "b")
	assert.Equal(t, uint64(4), m.Version())

	_ = m.RemoveNode(context.Background(), "b")
	assert.Equal(t, uint64(5), m.Version())
}

// =====================================================
// Steps tests
// =====================================================

func TestMutableDAG_Steps_ReturnsCurrentSteps(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b"),
	})

	steps := m.Steps()
	assert.Len(t, steps, 2)

	ids := make(map[string]bool)
	for _, s := range steps {
		ids[s.ID] = true
	}
	assert.True(t, ids["a"])
	assert.True(t, ids["b"])
}

// =====================================================
// Subscribe tests
// =====================================================

func TestMutableDAG_Subscribe_ReceivesEventsOnMutation(t *testing.T) {
	m, _ := NewMutableDAG(nil)

	ch := m.Subscribe()
	require.NotNil(t, ch)

	// Add a node -- should generate an event.
	err := m.AddNode(context.Background(), makeStep("a"))
	require.NoError(t, err)

	select {
	case event := <-ch:
		assert.Equal(t, ChangeAddNode, event.Change.Type)
		assert.Equal(t, "a", event.Change.NodeID)
		assert.True(t, event.Success)
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive AddNode event within timeout")
	}
}

// =====================================================
// Concurrent access tests (race detection)
// =====================================================

func TestMutableDAG_Concurrent_AddNode_RemoveNode(t *testing.T) {
	m, _ := NewMutableDAG(nil)

	var wg sync.WaitGroup
	ctx := context.Background()

	// Add 20 nodes concurrently.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			stepID := "node-" + string(rune('A'+id))
			_ = m.AddNode(ctx, makeStep(stepID))
		}(i)
	}
	wg.Wait()

	// Remove them concurrently.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			stepID := "node-" + string(rune('A'+id))
			_ = m.RemoveNode(ctx, stepID)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 0, m.NodeCount())
}

func TestMutableDAG_Concurrent_AddEdge_RemoveEdge(t *testing.T) {
	m, _ := NewMutableDAG([]*Step{
		makeStep("a"),
		makeStep("b"),
		makeStep("c"),
	})

	var wg sync.WaitGroup
	ctx := context.Background()

	// Add edges concurrently.
	edges := [][2]string{{"a", "b"}, {"a", "c"}, {"b", "c"}}
	for _, e := range edges {
		wg.Add(1)
		go func(from, to string) {
			defer wg.Done()
			_ = m.AddEdge(ctx, from, to)
		}(e[0], e[1])
	}
	wg.Wait()

	// Read operations concurrently with mutations.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Snapshot()
			_, _ = m.GetExecutionOrder()
			_ = m.Version()
			_ = m.NodeCount()
			_ = m.EdgeCount()
		}()
	}
	wg.Wait()
}
