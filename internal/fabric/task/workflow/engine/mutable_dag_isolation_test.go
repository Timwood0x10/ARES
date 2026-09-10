package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The DEEP_CODE_REVIEW_2026 HIGH regression #6: Steps()/StepIndex() must
// return isolated copies, never the live *Step pointers that AddEdge /
// RemoveEdge / ReplaceNode mutate under the write lock.

// TestStepsAndStepIndexReturnIsolatedCopies pins the isolation contract: a
// snapshot taken before a structural mutation must not observe the mutation.
// Pre-fix, Steps()/StepIndex() handed out the live pointers, so AddEdge's
// in-place append to step.DependsOn changed ALREADY-RETURNED snapshots.
func TestStepsAndStepIndexReturnIsolatedCopies(t *testing.T) {
	m, err := NewMutableDAG([]*Step{
		{ID: "a", AgentType: "x"},
		{ID: "b", AgentType: "y"},
	})
	require.NoError(t, err)

	steps := m.Steps()
	idx := m.StepIndex()
	snapB := m.StepSnapshot("b")

	require.NoError(t, m.AddEdge(context.Background(), "a", "b"))

	// The live DAG reflects the new edge...
	deps := m.ReadDeps("b")
	assert.Equal(t, []string{"a"}, deps, "the live DAG must show the new dependency")

	// ...but the pre-mutation snapshots must not have changed underneath the
	// caller.
	for _, s := range steps {
		if s.ID == "b" {
			assert.Empty(t, s.DependsOn, "Steps() snapshot must be isolated from AddEdge's in-place mutation")
		}
	}
	assert.Empty(t, idx["b"].DependsOn, "StepIndex() snapshot must be isolated from AddEdge's in-place mutation")
	assert.Empty(t, snapB.DependsOn, "StepSnapshot() copy must be isolated from AddEdge's in-place mutation")

	// Mutating a returned copy must not leak into the live DAG either.
	steps[0].DependsOn = []string{"bogus"}
	liveDeps := m.ReadDeps("a")
	assert.Empty(t, liveDeps, "writes to a returned copy must not reach the live DAG")

	// StepSnapshot of a missing node is nil, never an empty step.
	assert.Nil(t, m.StepSnapshot("nope"))
}

// TestMutableDAGStepsConcurrentReadVsEdgeMutation is the -race red test for
// the same defect: one goroutine iterates Steps()/StepIndex() snapshots while
// another mutates the graph via AddEdge/RemoveEdge. Pre-fix, the returned
// live pointers raced the in-place DependsOn writes (slice-header read vs
// append) — detected by the race detector.
func TestMutableDAGStepsConcurrentReadVsEdgeMutation(t *testing.T) {
	const nodes = 12
	initial := make([]*Step, 0, nodes)
	for i := 0; i < nodes; i++ {
		initial = append(initial, &Step{ID: string(rune('a' + i)), AgentType: "x"})
	}
	m, err := NewMutableDAG(initial)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			for _, s := range m.Steps() {
				_ = s.DependsOn
				_ = s.Metadata
				_ = s.AgentType
			}
			for _, s := range m.StepIndex() {
				_ = s.DependsOn
			}
			if s := m.StepSnapshot("a"); s != nil {
				_ = s.DependsOn
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			_ = m.AddEdge(ctx, "a", "b")
			_ = m.RemoveEdge(ctx, "a", "b")
		}
	}()
	wg.Wait()
}
