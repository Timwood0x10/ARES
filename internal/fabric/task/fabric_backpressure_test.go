package taskfabric

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMaxReadyTasksBackpressure verifies the M4 backpressure property:
// when maxReadyTasks is set, creating more than the cap rejects with
// ErrReadyQueueFull. The error carries the current depth and the cap.
func TestMaxReadyTasksBackpressure(t *testing.T) {
	f := NewFabric().WithMaxReadyTasks(3)

	// First 3 tasks are accepted.
	for i := 0; i < 3; i++ {
		task := &Task{ID: "task-" + string(rune('a'+i))}
		require.NoError(t, f.Create(task))
	}

	// The 4th is rejected.
	err := f.Create(&Task{ID: "task-d"})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrReadyQueueFull)

	// Error message contains depth and cap for observability.
	require.Contains(t, err.Error(), "depth 3")
	require.Contains(t, err.Error(), "max 3")
}

// TestMaxReadyTasksZeroIsUnlimited verifies that the default (zero)
// keeps the existing unlimited behavior — no rejection.
func TestMaxReadyTasksZeroIsUnlimited(t *testing.T) {
	f := NewFabric() // no WithMaxReadyTasks → default 0

	for i := 0; i < 100; i++ {
		id := "task-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		task := &Task{ID: id}
		require.NoError(t, f.Create(task))
	}
}

// TestMaxReadyTasksReleasesSlot verifies that when a task leaves READY
// (e.g. acquired/leased), its slot frees up for new creation.
func TestMaxReadyTasksReleasesSlot(t *testing.T) {
	f := NewFabric().WithMaxReadyTasks(2)

	require.NoError(t, f.Create(&Task{ID: "t1"}))
	require.NoError(t, f.Create(&Task{ID: "t2"}))

	// Queue is full.
	err := f.Create(&Task{ID: "t3"})
	require.ErrorIs(t, err, ErrReadyQueueFull)

	// Acquire t1 → it leaves READY, freeing a slot.
	_, acquireErr := f.Acquire("t1", "agent-a", 0)
	require.NoError(t, acquireErr)

	// Now t3 can be created.
	require.NoError(t, f.Create(&Task{ID: "t3"}))
}

// TestMaxReadyTasksTerminalDoesNotCount verifies that COMPLETED/FAILED tasks
// do not count against the READY cap.
func TestMaxReadyTasksTerminalDoesNotCount(t *testing.T) {
	f := NewFabric().WithMaxReadyTasks(1)

	require.NoError(t, f.Create(&Task{ID: "t1"}))
	epoch, err := f.Acquire("t1", "agent-a", 0)
	require.NoError(t, err)
	require.NoError(t, f.Start("t1", "agent-a", epoch))
	require.NoError(t, f.Complete("t1", "agent-a", epoch))

	// t1 is COMPLETED — it does not count. t2 should be accepted.
	require.NoError(t, f.Create(&Task{ID: "t2"}))
}

// TestMaxReadyTasksCompilePlanRespectsCap verifies that createLocked via
// CompilePlan's internal path also respects the cap.
func TestMaxReadyTasksCompilePlanRespectsCap(t *testing.T) {
	f := NewFabric().WithMaxReadyTasks(1)

	require.NoError(t, f.Create(&Task{ID: "t1"}))

	// Direct createLocked call (as CompilePlan does) should also reject.
	pending := make([]*pendingAppend, 0, 1)
	err := f.createLocked(&Task{ID: "t2"}, "", &pending)
	require.ErrorIs(t, err, ErrReadyQueueFull)
}

// TestBackpressure_CountsSuspendedWork: a task that yields between steps goes SUSPENDED and is
// scheduled again by the next drain, so it is still outstanding work. Counting READY alone would
// let a workload that yields between every step bypass the cap entirely.
func TestBackpressure_CountsSuspendedWork(t *testing.T) {
	f := NewFabric().WithMaxReadyTasks(2)

	require.NoError(t, f.Create(&Task{ID: "t-1"}))
	require.NoError(t, f.Create(&Task{ID: "t-2"}))
	f.mu.Lock()
	f.tasks["t-2"].State = StateSuspended
	f.mu.Unlock()

	err := f.Create(&Task{ID: "t-3"})

	require.Error(t, err)
	require.ErrorIs(t, err, ErrReadyQueueFull)
	require.Contains(t, err.Error(), "depth 2")
}
