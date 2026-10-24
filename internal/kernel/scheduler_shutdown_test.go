package kernel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// captureKernelLogs points the default logger at a buffer for the duration of the test.
//
// The kernel logger is logger.New("kernel"), a thin slog wrapper whose slog() resolves
// slog.Default() per call, so swapping the default is enough to observe what a drain reports —
// the repository's other logging seam (NewWithBase) also ends in slog.
func captureKernelLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// shutdownBlockingExecutor stays inside ExecuteStep until the context is cancelled, which is what a slow
// quantum looks like to the scheduler at shutdown.
type shutdownBlockingExecutor struct {
	once    sync.Once
	started chan struct{}
	err     error // returned once the block ends; nil means "aborted by cancellation"
}

func (e *shutdownBlockingExecutor) ID() string             { return "blocking-executor" }
func (e *shutdownBlockingExecutor) Type() models.AgentType { return "cap" }

func (e *shutdownBlockingExecutor) ExecuteStep(ctx context.Context, _ *models.Task) (*sub.StepOutcome, error) {
	e.once.Do(func() { close(e.started) })
	<-ctx.Done()
	if e.err != nil {
		return nil, e.err
	}
	return nil, ctx.Err()
}

// shutdownFailingExecutor fails on its own, with a live context — the case that must STILL be reported.
type shutdownFailingExecutor struct{}

func (e *shutdownFailingExecutor) ID() string             { return "failing-executor" }
func (e *shutdownFailingExecutor) Type() models.AgentType { return "cap" }

func (e *shutdownFailingExecutor) ExecuteStep(context.Context, *models.Task) (*sub.StepOutcome, error) {
	return nil, errors.New("boom")
}

// newDrainFixture builds a scheduler whose only executor is exec, with one READY task for it.
func newDrainFixture(t *testing.T, exec CapabilityExecutor) *Scheduler {
	t.Helper()
	f := taskfabric.NewFabric()
	s := New(f, map[string]CapabilityExecutor{"cap": exec}, NewLoadTracker())
	require.NoError(t, f.Create(&taskfabric.Task{ID: "t-1", Capability: "cap"}))
	return s
}

// TestDrain_ShutdownAbortIsNotReportedAsFailure pins the distinction the pump made louder: cancel()
// can catch many quanta in flight at once, and reporting each one at ERROR turns an orderly
// shutdown into a burst of apparent task failures on the operator's dashboard.
func TestDrain_ShutdownAbortIsNotReportedAsFailure(t *testing.T) {
	buf := captureKernelLogs(t)
	exec := &shutdownBlockingExecutor{started: make(chan struct{})}
	s := newDrainFixture(t, exec)

	ctx, cancel := context.WithCancel(context.Background())
	s.drain(ctx)
	<-exec.started // the quantum is inside the executor, so the abort below is a real one
	cancel()
	s.inflightMu.Lock()
	s.inflight.Wait()
	s.inflightMu.Unlock()

	assert.NotContains(t, buf.String(), "execute task failed",
		"a quantum the scheduler aborted at shutdown is not a task failure")
}

// TestDrain_RealFailureIsReported is the control: without it, silencing every execution error would
// pass the test above while hiding the failures the log exists for.
func TestDrain_RealFailureIsReported(t *testing.T) {
	buf := captureKernelLogs(t)
	s := newDrainFixture(t, &shutdownFailingExecutor{})

	s.drain(context.Background())
	s.inflightMu.Lock()
	s.inflight.Wait()
	s.inflightMu.Unlock()

	assert.Contains(t, buf.String(), "execute task failed",
		"a task that fails with a live context must still be reported")
}
