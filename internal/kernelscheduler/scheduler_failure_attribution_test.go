package kernelscheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// failingExecutor always fails the step; it exists to pin the outcome
// attribution contract: a failed quantum must be recorded as a FAILURE by the
// load tracker and the W4 attribution — never as a success.
type failingExecutor struct {
	id  string
	typ models.AgentType
}

func (e *failingExecutor) ID() string { return e.id }
func (e *failingExecutor) Type() models.AgentType {
	return e.typ
}
func (e *failingExecutor) ExecuteStep(_ context.Context, _ *models.Task) (*sub.StepOutcome, error) {
	return nil, errors.New("executor exploded")
}

// panickingExecutor panics on every call; it pins the panic-path contract:
// the winner's LoadTracker slot must be released so the agent stays
// schedulable after a panic instead of scoring 0 forever.
type panickingExecutor struct {
	id  string
	typ models.AgentType
}

func (e *panickingExecutor) ID() string { return e.id }
func (e *panickingExecutor) Type() models.AgentType {
	return e.typ
}
func (e *panickingExecutor) ExecuteStep(_ context.Context, _ *models.Task) (*sub.StepOutcome, error) {
	panic("boom inside executor")
}

// TestSchedulerAttributesFailureAsFailure drives a FAILING executor through
// Schedule → Acquire → RunQuantum → Fail and asserts that neither the load
// tracker nor the W4 attribution records a success. Regression for the
// RunQuantum error-swallowing bug: endQuantumOutcome previously received a nil
// error for every failed quantum, inflating agent confidence toward 1.0 and
// hiding every task failure from the logs.
func TestSchedulerAttributesFailureAsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	exec := &failingExecutor{id: "coder", typ: models.AgentType("code")}
	tracker := NewLoadTracker()
	sched := New(fabric, map[string]CapabilityExecutor{"coder": exec}, tracker)
	sched.PollInterval = 10 * time.Millisecond

	attribution := aresrecovery.NewExecutionAttribution()
	sched.WithAttribution(attribution)

	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{
		ID:          "t-fail",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1}, // single attempt → terminal FAILED
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitForTaskState(t, fabric, "t-fail", taskfabric.StateFailed, 3*time.Second)

	// One attributed outcome exists for ("coder","code") and it is a failure:
	// the capability confidence drops from the neutral prior (1.0) to 0.
	if got := attribution.CapabilityConfidence("coder", "code"); got != 0 {
		t.Fatalf("failed quantum must be attributed as failure (confidence 0), got %v", got)
	}
	// The load tracker must agree: one finished quantum, zero successes.
	if got := tracker.Confidence("coder"); got != 0 {
		t.Fatalf("load tracker recorded a success for a failing executor (confidence %v)", got)
	}
	if got := tracker.Load("coder"); got != 0 {
		t.Fatalf("busy slot must be released after the failed quantum, load=%v", got)
	}
	if sched.Scheduled.Load() != 0 {
		t.Fatalf("Scheduled counter must not count failed quanta, got %d", sched.Scheduled.Load())
	}
}

// TestSchedulerPanicReleasesLoadSlot drives a PANICKING executor through the
// quantum path and asserts the agent's load slot is released: after the panic
// the agent must still be schedulable (Load back to 0). Regression for the
// leaked-slot bug where one panic permanently zeroed an agent's Score.
func TestSchedulerPanicReleasesLoadSlot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	exec := &panickingExecutor{id: "coder", typ: models.AgentType("code")}
	tracker := NewLoadTracker()
	sched := New(fabric, map[string]CapabilityExecutor{"coder": exec}, tracker)
	sched.PollInterval = 10 * time.Millisecond
	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{
		ID:          "t-panic",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Wait for the executor to be acquired (task leaves READY — the panic is
	// recovered by the drain goroutine; the task stays leased and recovery
	// requeues it later), then allow the deferred release to run.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tk, err := fabric.Task("t-panic"); err == nil && tk.State != taskfabric.StateReady {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	if got := tracker.Load("coder"); got != 0 {
		t.Fatalf("panic must release the LoadTracker slot, load=%v", got)
	}
}
