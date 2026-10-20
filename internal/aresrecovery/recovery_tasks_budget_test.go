package aresrecovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestRecoveryTasksFor_CarriesTheTasksRetryBudget is the P2-5 contract: the recovery
// snapshot must carry the task's OWN retry budget, because the caller of the recovery
// path is the only place that holds both budgets at once — the task fabric knows nothing
// about agents, and the recovery policy is handed a dead agent id and nothing else.
//
// Without these two numbers the caller can report "the revival budget is spent" forever
// while never saying whether that refusal matters: a task with attempts left will simply
// be run again, while one without any has nowhere left to go.
func TestRecoveryTasksFor_CarriesTheTasksRetryBudget(t *testing.T) {
	tasks := taskfabric.NewFabric()
	require.NoError(t, tasks.Create(&taskfabric.Task{
		ID:          "t-1",
		Capability:  "coder",
		RetryPolicy: taskfabric.RetryPolicy{Attempts: 2, MaxRetries: 3},
	}))
	r := New(tasks, agentfabric.NewFabric(), RestartPolicy{MaxRestarts: 1})

	got := r.RecoveryTasksFor([]string{"t-1"})
	require.Len(t, got, 1)
	assert.Equal(t, 2, got[0].Attempts, "the snapshot must carry the task's spent attempts verbatim")
	assert.Equal(t, 3, got[0].MaxRetries, "and its ceiling, so the caller can tell whether anything is left")
}

// TestRecoveryTasksFor_CarriesAZeroBudgetVerbatim: the numbers are copied, never
// inferred. A task that never had a retry policy carries 0/0 — which reads as "no retry
// budget", and a caller that saw it as "unlimited" would be inventing a policy the task
// does not have.
func TestRecoveryTasksFor_CarriesAZeroBudgetVerbatim(t *testing.T) {
	tasks := taskfabric.NewFabric()
	require.NoError(t, tasks.Create(&taskfabric.Task{ID: "t-2", Capability: "coder"}))
	r := New(tasks, agentfabric.NewFabric(), RestartPolicy{MaxRestarts: 1})

	got := r.RecoveryTasksFor([]string{"t-2"})
	require.Len(t, got, 1)
	assert.Equal(t, 0, got[0].Attempts)
	assert.Equal(t, 0, got[0].MaxRetries)
}
