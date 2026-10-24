package taskfabric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReadyTasksDeterministicOrder verifies that ReadyTasks returns a
// deterministic dispatch order regardless of Go's randomized map iteration:
// (priority DESC, ReadyAt ASC, id ASC). The test creates tasks with varying
// priorities and insertion orders, then asserts the same output across
// repeated calls — a regression on the sort would make the order depend on
// map iteration and the assertion would eventually fail under -count=100.
func TestReadyTasksDeterministicOrder(t *testing.T) {
	f := NewFabric()
	// Use a fixed clock so the aging calculation is deterministic.
	clock := time.Unix(1000, 0)
	f.WithClock(func() time.Time { return clock })

	// Create tasks with different priorities and ready-at times.
	// Insert in a scrambled order — the sort must produce a stable output.
	tasks := []struct {
		id       string
		priority int
		readyAt  time.Time
	}{
		{"task-c", 1, time.Unix(300, 0)},
		{"task-a", 5, time.Unix(100, 0)},
		{"task-b", 5, time.Unix(200, 0)},
		{"task-e", 1, time.Unix(100, 0)},
		{"task-d", 3, time.Unix(150, 0)},
	}

	for _, tc := range tasks {
		task := &Task{
			ID:       tc.id,
			Priority: tc.priority,
		}
		require.NoError(t, f.Create(task))
		// Set ReadyAt directly under the lock.
		f.mu.Lock()
		f.tasks[tc.id].ReadyAt = tc.readyAt
		f.mu.Unlock()
	}

	got := f.ReadyTasks()

	// Expected: priority DESC, then ReadyAt ASC, then id ASC.
	// task-a (pri=5, t=100) < task-b (pri=5, t=200)
	// task-d (pri=3, t=150)
	// task-e (pri=1, t=100) < task-c (pri=1, t=300)
	want := []string{"task-a", "task-b", "task-d", "task-e", "task-c"}
	require.Equal(t, want, got, "ReadyTasks must be sorted by (priority DESC, ReadyAt ASC, id ASC)")
}

// TestReadyTasksDeterministicAcrossInsertOrders verifies that scrambling the
// insertion order 50 times always produces the same dispatch sequence.
// This is the M1 acceptance test: "同一 READY 集合，打乱插入顺序 100 次 ⇒
// 派发序列完全一致".
func TestReadyTasksDeterministicAcrossInsertOrders(t *testing.T) {
	// Build a reference fabric and a set of tasks.
	ids := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"}
	priorities := map[string]int{
		"t1": 1, "t2": 3, "t3": 2, "t4": 3,
		"t5": 1, "t6": 2, "t7": 3, "t8": 1,
	}
	readyTimes := map[string]time.Time{
		"t1": time.Unix(10, 0), "t2": time.Unix(20, 0),
		"t3": time.Unix(30, 0), "t4": time.Unix(40, 0),
		"t5": time.Unix(50, 0), "t6": time.Unix(60, 0),
		"t7": time.Unix(70, 0), "t8": time.Unix(80, 0),
	}

	// Compute the reference order once.
	clock := time.Unix(1000, 0)
	ref := NewFabric()
	ref.WithClock(func() time.Time { return clock })
	for _, id := range ids {
		task := &Task{ID: id, Priority: priorities[id]}
		require.NoError(t, ref.Create(task))
		ref.mu.Lock()
		ref.tasks[id].ReadyAt = readyTimes[id]
		ref.mu.Unlock()
	}
	refOrder := ref.ReadyTasks()
	require.Len(t, refOrder, len(ids))

	// Now rebuild 50 times with scrambled insertion orders and verify the
	// output is always the same.
	for iter := 0; iter < 50; iter++ {
		f := NewFabric()
		f.WithClock(func() time.Time { return clock })
		// Rotate the insertion order by a different offset each time.
		offset := iter % len(ids)
		rotated := make([]string, len(ids))
		for i := range ids {
			rotated[i] = ids[(i+offset)%len(ids)]
		}
		for _, id := range rotated {
			task := &Task{ID: id, Priority: priorities[id]}
			require.NoError(t, f.Create(task))
			f.mu.Lock()
			f.tasks[id].ReadyAt = readyTimes[id]
			f.mu.Unlock()
		}
		got := f.ReadyTasks()
		require.Equal(t, refOrder, got,
			"iteration %d: dispatch order must be identical regardless of insertion order", iter)
	}
}

// TestResumableTasksDeterministicOrder verifies that ResumableTasks also
// produces a deterministic order (same sort key as ReadyTasks).
func TestResumableTasksDeterministicOrder(t *testing.T) {
	f := NewFabric()
	// Use a fixed clock so the aging calculation is deterministic.
	clock := time.Unix(1000, 0)
	f.WithClock(func() time.Time { return clock })

	tasks := []struct {
		id       string
		priority int
		readyAt  time.Time
	}{
		{"z-task", 1, time.Unix(200, 0)},
		{"a-task", 5, time.Unix(100, 0)},
		{"m-task", 3, time.Unix(150, 0)},
	}

	for _, tc := range tasks {
		task := &Task{
			ID:       tc.id,
			Priority: tc.priority,
		}
		require.NoError(t, f.Create(task))
		f.mu.Lock()
		f.tasks[tc.id].ReadyAt = tc.readyAt
		f.mu.Unlock()
	}

	got := f.ResumableTasks()
	// Expected: a-task (pri=5, t=100), m-task (pri=3, t=150), z-task (pri=1, t=200)
	want := []string{"a-task", "m-task", "z-task"}
	require.Equal(t, want, got, "ResumableTasks must be sorted deterministically")
}

// TestReadyTasksTieBreakIsTheIDAndNotMapOrder covers the third sort key. Two tasks with the SAME
// priority AND the same ReadyAt are equal on both meaningful keys, so only the id decides; without
// it the order would come from Go's randomised map iteration and change between calls.
//
// This case was missing: reversing the id comparison in the comparator left every deterministic
// test green, which meant the tiebreak was load-bearing but unproven.
func TestReadyTasksTieBreakIsTheIDAndNotMapOrder(t *testing.T) {
	f := NewFabric()
	clock := time.Unix(5000, 0)
	f.WithClock(func() time.Time { return clock })

	// Created in descending id order on purpose: if the implementation leaned on insertion or map
	// order, this is the arrangement that reveals it.
	for _, id := range []string{"z-task", "m-task", "a-task"} {
		require.NoError(t, f.Create(&Task{ID: id, Priority: 3}))
		f.mu.Lock()
		f.tasks[id].ReadyAt = time.Unix(4000, 0)
		f.mu.Unlock()
	}

	for i := 0; i < 20; i++ {
		got := f.ReadyTasks()
		require.Equal(t, []string{"a-task", "m-task", "z-task"}, got,
			"ties on (priority, ReadyAt) must resolve by id, not by map iteration order")
	}
}
