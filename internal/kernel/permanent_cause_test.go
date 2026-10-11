package kernel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	apperrors "github.com/Timwood0x10/ares/internal/errors"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// erroringExecutor is a CapabilityExecutor whose every step fails with err.
type erroringExecutor struct {
	id  string
	err error
}

func (e *erroringExecutor) ID() string             { return e.id }
func (e *erroringExecutor) Type() models.AgentType { return models.AgentType(e.id) }
func (e *erroringExecutor) ExecuteStep(context.Context, *models.Task) (*sub.StepOutcome, error) {
	return nil, e.err
}

// runStepThroughKernel drives one real quantum through buildQuantumStep — the
// closure where the kernel applies the permanent-cause classification — and
// returns the task afterwards.
func runStepThroughKernel(t *testing.T, stepErr error) *taskfabric.Task {
	t.Helper()
	f := taskfabric.NewFabric()
	if err := f.Create(&taskfabric.Task{
		ID:          "t",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 3},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	epoch, err := f.Acquire("t", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	tk, err := f.Task("t")
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	sched := New(f, map[string]CapabilityExecutor{}, NewLoadTracker())
	step := sched.buildQuantumStep(context.Background(),
		&erroringExecutor{id: "agent-a", err: stepErr}, tk, taskfabric.DecodedCheckpoint{}, &quantumUsage{})
	if runErr := f.RunQuantum("t", "agent-a", epoch, step); runErr == nil {
		t.Fatal("RunQuantum must return the step error")
	}
	got, err := f.Task("t")
	if err != nil {
		t.Fatalf("task after quantum: %v", err)
	}
	return got
}

// TestMarkPermanentStepCause_InvalidArgumentGoesTerminalImmediately pins the
// production wiring of taskfabric.MarkPermanent: a step failure caused by an
// invalid request reaches terminal FAILED on its FIRST attempt, instead of
// burning the whole retry budget on attempts that provably reproduce it.
//
// Before this hook existed MarkPermanent had zero production callers, so the
// budget was spent in full and the real cause was buried under N repeats.
func TestMarkPermanentStepCause_InvalidArgumentGoesTerminalImmediately(t *testing.T) {
	tk := runStepThroughKernel(t, fmt.Errorf("llm: %w", apperrors.ErrInvalidArgument))
	if tk.State != taskfabric.StateFailed {
		t.Fatalf("state = %s, want FAILED (an invalid-request cause is not retryable)", tk.State)
	}
	if tk.RetryPolicy.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1: the retry budget must not be spent on a permanent cause",
			tk.RetryPolicy.Attempts)
	}
}

// TestMarkPermanentStepCause_UnclassifiedStaysRetryable is the backward-compat
// half: everything the classifier does not recognise keeps 0.3.2 behaviour —
// requeued to READY with the retry budget consumed, never silently retired.
func TestMarkPermanentStepCause_UnclassifiedStaysRetryable(t *testing.T) {
	tk := runStepThroughKernel(t, fmt.Errorf("upstream 503: %w", apperrors.ErrTimeout))
	if tk.State != taskfabric.StateReady {
		t.Fatalf("state = %s, want READY (an unclassified cause stays retryable)", tk.State)
	}
	if tk.RetryPolicy.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", tk.RetryPolicy.Attempts)
	}
}

// TestMarkPermanentStepCause_ClassifiesOnlyInvalidArgument locks the
// classification boundary itself: nil stays nil, and only the invalid-request
// marker is promoted.
func TestMarkPermanentStepCause_ClassifiesOnlyInvalidArgument(t *testing.T) {
	if got := markPermanentStepCause(nil); got != nil {
		t.Fatalf("nil must stay nil, got %v", got)
	}
	wrapped := fmt.Errorf("llm: %w", apperrors.ErrInvalidArgument)
	if got := markPermanentStepCause(wrapped); got == wrapped {
		t.Fatal("ErrInvalidArgument must be wrapped as permanent, got the bare error")
	} else if !errorsIsPermanent(got) {
		t.Fatalf("ErrInvalidArgument must reach the fabric as a permanent cause, got %v", got)
	}
	transient := fmt.Errorf("upstream 503: %w", apperrors.ErrTimeout)
	if got := markPermanentStepCause(transient); got != transient {
		t.Fatalf("an unclassified cause must pass through untouched, got %v", got)
	}
}

// errorsIsPermanent probes the fabric's own verdict for a cause: a task with
// retries left that still ends FAILED was classified permanent.
func errorsIsPermanent(cause error) bool {
	f := taskfabric.NewFabric()
	if err := f.Create(&taskfabric.Task{
		ID:          "probe",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 3},
	}); err != nil {
		return false
	}
	epoch, err := f.Acquire("probe", "agent-a", time.Minute)
	if err != nil {
		return false
	}
	if runErr := f.RunQuantum("probe", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, cause
	}); runErr == nil {
		return false
	}
	tk, err := f.Task("probe")
	return err == nil && tk.State == taskfabric.StateFailed
}
