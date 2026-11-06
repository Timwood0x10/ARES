package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// slowQuantumExecutor reports each task it starts and then blocks until released (or cancelled),
// which is what a slow LLM call looks like to the scheduler.
type slowQuantumExecutor struct {
	id      string
	started chan string
	release chan struct{}
}

func (e *slowQuantumExecutor) ID() string             { return e.id }
func (e *slowQuantumExecutor) Type() models.AgentType { return "cap" }

func (e *slowQuantumExecutor) ExecuteStep(ctx context.Context, task *models.Task) (*sub.StepOutcome, error) {
	select {
	case e.started <- task.TaskID:
	default:
	}
	select {
	case <-e.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &sub.StepOutcome{Done: true}, nil
}

// TestScheduler_DispatchesNewWorkWhileASlowQuantumRuns is the end-to-end form of the M2 claim.
//
// The unit tests around admitQuantum prove the budget is counted correctly; they say nothing about
// the loop. Before the pump, drain ended with wg.Wait(): a scheduling pass blocked until its
// slowest quantum finished, so work arriving afterwards waited for the whole batch no matter how
// many agents were idle. This test holds one quantum inside the executor and requires the next task
// to start anyway.
func TestScheduler_DispatchesNewWorkWhileASlowQuantumRuns(t *testing.T) {
	fabric := taskfabric.NewFabric()
	// TWO agents: work conservation is "an idle agent picks up the new task", and one executor
	// cannot do that — maxConcurrentPerAgent=1 means a single agent is busy while its quantum runs,
	// which the scheduler reports as "no capable candidate" rather than as a blocked loop.
	started := make(chan string, 4)
	release := make(chan struct{})
	exec := &slowQuantumExecutor{id: "agent-1", started: started, release: release}
	exec2 := &slowQuantumExecutor{id: "agent-2", started: started, release: release}
	sched := New(fabric, map[string]CapabilityExecutor{"agent-1": exec, "agent-2": exec2}, NewLoadTracker()).
		WithMaxConcurrent(2)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		close(release)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("scheduler did not stop after cancellation")
		}
	})

	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "slow", Capability: "cap"}))
	require.Equal(t, "slow", awaitStarted(t, started),
		"the first quantum must start before the assertion below means anything")

	// The first quantum is still inside ExecuteStep. Dispatched work arriving now is exactly the
	// case the batch barrier used to defer until the slow quantum finished.
	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "next", Capability: "cap"}))

	assert.Equal(t, "next", awaitStarted(t, started),
		"new READY work must be dispatched while a slow quantum is in flight; "+
			"waiting for the in-flight quantum is the batch barrier the pump removed")
}

// awaitStarted waits for the next executor entry, failing the test rather than blocking forever.
func awaitStarted(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case id := <-started:
		return id
	case <-time.After(3 * time.Second):
		t.Fatal("no quantum started within 3s")
		return ""
	}
}
