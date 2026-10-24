package taskfabric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPreemptSetsPreemptRequested verifies that Preempt sets the
// PreemptRequested flag (M5), so a multi-step quantum checking the flag
// at its next step boundary stops dispatching further steps.
func TestPreemptSetsPreemptRequested(t *testing.T) {
	f := NewFabric()

	require.NoError(t, f.Create(&Task{ID: "t1"}))
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t1", "agent-a", epoch))

	// Preempt the task.
	require.NoError(t, f.Preempt("t1", "agent-a", epoch, "higher priority"))

	// The flag should be set.
	f.mu.Lock()
	flag := f.tasks["t1"].PreemptRequested
	f.mu.Unlock()
	require.True(t, flag, "Preempt must set PreemptRequested")
}

// TestRunQuantumPreemptedAtStepBoundary verifies the M5 property: when a
// task is preempted while its quantum step is in flight, RunQuantum
// detects the preemption at the step boundary and returns without
// attempting a state transition (the task is already READY).
func TestRunQuantumPreemptedAtStepBoundary(t *testing.T) {
	f := NewFabric()

	require.NoError(t, f.Create(&Task{ID: "t1"}))
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	require.NoError(t, err)

	// Step that simulates an in-flight LLM call, then preemption happens
	// during the step. The step completes normally (the LLM call was NOT
	// cancelled — see PLAN §5.1), but the result should be discarded.
	stepCalled := false
	step := func() (any, bool, error) {
		stepCalled = true
		// Preempt during the step (simulating a background preemption sweep).
		require.NoError(t, f.Preempt("t1", "agent-a", epoch, "higher priority"))
		// The step completes normally — the in-flight call was not cancelled.
		return "llm-result", false, nil
	}

	err = f.RunQuantum("t1", "agent-a", epoch, step)
	require.NoError(t, err, "preempted quantum should return nil (not a failure)")
	require.True(t, stepCalled, "step must have been called")

	// The task should be READY (Preempt put it there) and the checkpoint
	// from the step should NOT be persisted (the result was discarded).
	task, err := f.Task("t1")
	require.NoError(t, err)
	require.Equal(t, StateReady, task.State)
}

// TestPreemptRequestedClearedOnReacquire verifies that the PreemptRequested
// flag is cleared when the task is re-acquired, so the next quantum starts
// fresh.
func TestPreemptRequestedClearedOnReacquire(t *testing.T) {
	f := NewFabric()

	require.NoError(t, f.Create(&Task{ID: "t1"}))
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t1", "agent-a", epoch))

	require.NoError(t, f.Preempt("t1", "agent-a", epoch, "test"))

	// Flag is set.
	f.mu.Lock()
	require.True(t, f.tasks["t1"].PreemptRequested)
	f.mu.Unlock()

	// Re-acquire the task.
	_, err = f.Acquire("t1", "agent-b", time.Minute)
	require.NoError(t, err)

	// Flag should be cleared.
	f.mu.Lock()
	require.False(t, f.tasks["t1"].PreemptRequested)
	f.mu.Unlock()
}
