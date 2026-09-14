package taskfabric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPreemptEventIdentifiesThePreemptedHolder pins F-4.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md finding F-4): Preempt
// cleared t.Owner/t.Lease BEFORE calling recordLocked, and recordLocked reads
// t.Owner for the event's AgentID — so every task.preempted event was anonymous
// and "who was preempted" was unanswerable from the durable log. Every other
// ownership-clearing transition (Release / Fail / CheckExpiredLeases) records
// first precisely to avoid this.
func TestPreemptEventIdentifiesThePreemptedHolder(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(newTask("t1")))

	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	require.NoError(t, err)

	require.NoError(t, f.Preempt("t1", "agent-a", epoch, "higher priority work arrived"))

	var found bool
	for _, ev := range f.events {
		if ev.Type != EventTaskPreempted {
			continue
		}
		found = true
		require.Equal(t, "agent-a", ev.AgentID,
			"task.preempted must identify the preempted holder")
	}

	require.True(t, found, "Preempt must record a task.preempted event")

	task, err := f.Task("t1")
	require.NoError(t, err)
	require.Equal(t, StateReady, task.State, "a preempted task returns to READY")
	require.Empty(t, task.Owner, "ownership is cleared after the event is captured")
}
