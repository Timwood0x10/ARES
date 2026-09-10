package sub

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

// blockingExecutor blocks Execute until released or the context is
// cancelled, so tests can observe the agent's status while a task is in
// flight and verify stop-signal propagation.
type blockingExecutor struct {
	started  chan struct{}
	release  chan struct{}
	executed int
}

func newBlockingExecutor() *blockingExecutor {
	return &blockingExecutor{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (e *blockingExecutor) Execute(ctx context.Context, _ *models.Task) (*models.TaskResult, error) {
	e.executed++
	e.started <- struct{}{}
	select {
	case <-e.release:
		res := models.NewTaskResult("blocked-task", models.AgentTypeTop)
		res.Success = true
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *blockingExecutor) RegisterFallback(models.AgentType, FallbackHandler) {}

func newTask() *models.Task {
	task := models.NewTask("stream-task", models.AgentTypeTop, nil)
	return task
}

// TestSubAgent_ProcessStreamStaysBusyUntilTaskCompletes is the #57
// regression: the outer ProcessStream deferred setStatus(Ready), which fired
// the moment the channel was returned — before the task goroutine had done
// any work. Busy/Ready admission control was broken: a second submission
// was admitted while the first task was still executing. Ready must be
// restored only when the task goroutine finishes.
func TestSubAgent_ProcessStreamStaysBusyUntilTaskCompletes(t *testing.T) {
	exec := newBlockingExecutor()
	agent := New("sub-busy", models.AgentTypeTop, exec, nil)
	require.NoError(t, agent.Start(context.Background()))

	ch, err := agent.ProcessStream(context.Background(), newTask())
	require.NoError(t, err)

	// The channel is already in the caller's hands, but the task has not
	// completed: the agent must still report Busy.
	<-exec.started // task is now executing
	assert.Equal(t, models.AgentStatusBusy, agent.Status(),
		"agent must stay Busy while the streamed task is executing, not reset on channel return")

	// A second submission must be rejected while the first runs.
	_, err = agent.ProcessStream(context.Background(), newTask())
	assert.ErrorIs(t, err, errors.ErrAgentNotReady, "second ProcessStream must be rejected while Busy")

	// Complete the task.
	close(exec.release)
	for evt := range ch {
		if evt.Type == base.EventComplete {
			break
		}
	}

	assert.Eventually(t, func() bool {
		return agent.Status() == models.AgentStatusReady
	}, 5*time.Second, 10*time.Millisecond, "agent must return to Ready after the task goroutine finishes")
	assert.Equal(t, 1, exec.executed, "exactly one task must have executed")
}

// TestSubAgent_ProcessStreamInvalidInputRestoresReady guards the reset path
// for admission failures: a rejected input must restore Ready synchronously
// (no goroutine was spawned).
func TestSubAgent_ProcessStreamInvalidInputRestoresReady(t *testing.T) {
	exec := newBlockingExecutor()
	agent := New("sub-invalid", models.AgentTypeTop, exec, nil)
	require.NoError(t, agent.Start(context.Background()))

	_, err := agent.ProcessStream(context.Background(), "not a task")
	require.ErrorIs(t, err, errors.ErrInvalidInput)
	assert.Equal(t, models.AgentStatusReady, agent.Status())
}

// TestSubAgent_ProcessDoesNotResurrectAfterStop is the #58 regression:
// Process auto-Starts when the agent is Offline, so after an explicit Stop
// (status Offline) the next Process call silently restarted the agent —
// Stop was not durable. A stopped agent must refuse Process until an
// explicit Start.
func TestSubAgent_ProcessDoesNotResurrectAfterStop(t *testing.T) {
	exec := newBlockingExecutor()
	agent := New("sub-stop-resurrect", models.AgentTypeTop, exec, nil)
	require.NoError(t, agent.Start(context.Background()))
	require.NoError(t, agent.Stop(context.Background()))
	require.Equal(t, models.AgentStatusOffline, agent.Status())

	_, err := agent.Process(context.Background(), newTask())
	require.Error(t, err, "Process after Stop must not silently restart the agent")
	assert.Equal(t, models.AgentStatusOffline, agent.Status(),
		"agent must stay Offline — Process must not resurrect it")
	assert.Equal(t, 0, exec.executed, "no task may execute after Stop")

	// An explicit Start makes the agent usable again (documented restart path).
	require.NoError(t, agent.Start(context.Background()))
	assert.Equal(t, models.AgentStatusReady, agent.Status())
}

// TestSubAgent_ProcessStreamDoesNotResurrectAfterStop: same contract for the
// streaming path.
func TestSubAgent_ProcessStreamDoesNotResurrectAfterStop(t *testing.T) {
	exec := newBlockingExecutor()
	agent := New("sub-stream-resurrect", models.AgentTypeTop, exec, nil)
	require.NoError(t, agent.Start(context.Background()))
	require.NoError(t, agent.Stop(context.Background()))

	_, err := agent.ProcessStream(context.Background(), newTask())
	require.Error(t, err, "ProcessStream after Stop must not silently restart the agent")
	assert.Equal(t, models.AgentStatusOffline, agent.Status())
	assert.Equal(t, 0, exec.executed)
}

// TestSubAgent_ProcessAbortsWhenStoppedMidFlight is the #58 stopCh half:
// once Stop is signaled, an admitted Process must abort before executing
// the task instead of running it to completion after Stop returned.
func TestSubAgent_ProcessAbortsWhenStoppedMidFlight(t *testing.T) {
	exec := newBlockingExecutor()
	agent := New("sub-stop-midflight", models.AgentTypeTop, exec, nil)
	require.NoError(t, agent.Start(context.Background()))

	done := make(chan struct{})
	var procErr error
	go func() {
		defer close(done)
		_, procErr = agent.Process(context.Background(), newTask())
	}()

	// Admit: the task goroutine is now inside Process.
	<-exec.started
	// Stop while the task is executing. The blocking executor's release
	// channel stays open — if Process ignored the stop it would hang until
	// the test timeout.
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		_ = agent.Stop(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Process did not return after Stop — it ignores the stop signal and blocks on the executor")
	}
	<-stopDone

	require.Error(t, procErr, "Process aborted by Stop must surface an error")
	assert.Equal(t, models.AgentStatusOffline, agent.Status())
}
