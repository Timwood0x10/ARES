package taskfabric

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompilePlan_OverCapacityLeavesNoHalfGraph is the plan's S04 item that says a batch which
// exceeds the admission cap must not leave "half a graph".
//
// The implementation already rolls back (CompilePlan deletes every id it created when createLocked
// fails), but nothing exercised that path through CompilePlan itself: the test named
// TestMaxReadyTasksCompilePlanRespectsCap calls createLocked directly, so deleting the rollback
// loop would have left every test green.
func TestCompilePlan_OverCapacityLeavesNoHalfGraph(t *testing.T) {
	f := NewFabric().WithMaxReadyTasks(2)
	steps := []PlanStep{
		{ID: "n1", Capability: "cap"},
		{ID: "n2", Capability: "cap"},
		{ID: "n3", Capability: "cap"},
	}

	created, err := f.CompilePlan(context.Background(), steps)

	require.Error(t, err, "three nodes do not fit a cap of two")
	require.ErrorIs(t, err, ErrReadyQueueFull, "the caller must be able to classify it as backpressure")
	assert.Empty(t, created, "a refused batch creates nothing")

	// The whole point: the two nodes that were admitted before the refusal are gone too. A partial
	// batch is worse than none — the scheduler would run a plan whose remaining nodes will never
	// exist.
	assert.Empty(t, f.IDs(), "no task from the refused batch may survive")
	assert.Empty(t, f.ReadyTasks(), "and none may be schedulable")
}

// TestCompilePlan_FitsExactlyAtTheCap is the control: without it, a CompilePlan that refused
// everything would satisfy the test above.
func TestCompilePlan_FitsExactlyAtTheCap(t *testing.T) {
	f := NewFabric().WithMaxReadyTasks(3)
	steps := []PlanStep{
		{ID: "n1", Capability: "cap"},
		{ID: "n2", Capability: "cap"},
		{ID: "n3", Capability: "cap"},
	}

	created, err := f.CompilePlan(context.Background(), steps)

	require.NoError(t, err)
	assert.Len(t, created, 3)
	assert.Len(t, f.ReadyTasks(), 3)
}

// TestCompilePlan_OverCapacityDoesNotDisturbExistingWork: the rollback must delete only what this
// batch created. A pre-existing task that happens to share the fabric is someone else's work.
func TestCompilePlan_OverCapacityDoesNotDisturbExistingWork(t *testing.T) {
	f := NewFabric().WithMaxReadyTasks(2)
	require.NoError(t, f.Create(&Task{ID: "existing", Capability: "cap"}))

	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "n1", Capability: "cap"},
		{ID: "n2", Capability: "cap"},
	})
	require.Error(t, err)

	assert.Equal(t, []string{"existing"}, f.ReadyTasks(),
		"the rollback removed the batch, not the fabric")
}

// TestBackpressure_ConcurrentCreatesNeverExceedTheCap is the "concurrent submissions" item: the
// count and the insertion must happen under one lock, or two callers each read depth=cap-1 and
// both insert.
//
// Fifty goroutines against a cap of five: exactly five succeed, and the fabric holds exactly five.
// Run under -race as well; the assertion is the count, which is what an operator would notice.
func TestBackpressure_ConcurrentCreatesNeverExceedTheCap(t *testing.T) {
	const (
		cap        = 5
		submitters = 50
	)
	f := NewFabric().WithMaxReadyTasks(cap)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		refused   int
	)
	start := make(chan struct{})
	for i := 0; i < submitters; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start // release them together so they actually contend
			err := f.Create(&Task{ID: fmt.Sprintf("t-%d", n), Capability: "cap"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrReadyQueueFull):
				refused++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	assert.Equal(t, cap, succeeded, "the cap is the number of admissions, not a suggestion")
	assert.Equal(t, submitters-cap, refused)
	assert.Len(t, f.ReadyTasks(), cap)
}
