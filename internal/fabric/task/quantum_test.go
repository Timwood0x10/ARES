package taskfabric

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestFabricRunQuantumCompletes verifies a quantum whose step reports done
// finalizes the task as COMPLETED.
func TestFabricRunQuantumCompletes(t *testing.T) {
	f := NewFabric()
	if err := f.Create(newTask("t1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	err = f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return map[string]any{"done": true}, true, nil
	})
	if err != nil {
		t.Fatalf("RunQuantum: %v", err)
	}
	task, _ := f.Task("t1")
	if task.State != StateCompleted {
		t.Fatalf("want COMPLETED, got %s", task.State)
	}
}

// TestFabricRunQuantumYields verifies the quantum + SUSPENDED semantics lock:
// unfinished work goes through SUSPENDED — the task's durable intent is not
// yet complete (not "the agent was suspended") — with the checkpoint
// preserved, recording both TaskYielded and TaskCheckpointed events.
func TestFabricRunQuantumYields(t *testing.T) {
	f := NewFabric()
	if err := f.Create(newTask("t1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	cp := map[string]any{"step": 2}
	err = f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return cp, false, nil
	})
	if err != nil {
		t.Fatalf("RunQuantum: %v", err)
	}
	task, _ := f.Task("t1")
	if task.State != StateSuspended {
		t.Fatalf("want SUSPENDED, got %s", task.State)
	}
	if task.Checkpoint.(map[string]any)["step"] != 2 {
		t.Fatalf("checkpoint must be preserved, got %+v", task.Checkpoint)
	}
	// Both events recorded.
	gotYielded, gotCheckpointed := false, false
	for _, ev := range f.Events() {
		if ev.Type == EventTaskYielded {
			gotYielded = true
		}
		if ev.Type == EventTaskCheckpointed {
			gotCheckpointed = true
		}
	}
	if !gotYielded || !gotCheckpointed {
		t.Fatalf("want TaskYielded+TaskCheckpointed events, yielded=%v checkpointed=%v", gotYielded, gotCheckpointed)
	}
}

// TestFabricRunQuantumFails verifies a step error fails the task (requeued to
// READY when the retry policy allows) AND is propagated to the caller: the
// scheduler attributes outcomes from RunQuantum's return value, so a swallowed
// error would record failed quanta as successes.
func TestFabricRunQuantumFails(t *testing.T) {
	f := NewFabric()
	tk := newTask("t1")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 2} // 2 attempts: 1st failure requeues, 2nd fails out
	if err := f.Create(tk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	stepErr := errors.New("step blew up")
	err = f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, stepErr
	})
	if !errors.Is(err, stepErr) {
		t.Fatalf("RunQuantum must propagate the step error (state transition already applied), got %v", err)
	}
	task, _ := f.Task("t1")
	if task.State != StateReady {
		t.Fatalf("want requeue to READY (retry allowed), got %s", task.State)
	}
}

// TestFabricRunQuantumFailsExhausted verifies the exhausted-retry path: the
// step error is still propagated while the task finalizes FAILED.
func TestFabricRunQuantumFailsExhausted(t *testing.T) {
	f := NewFabric()
	tk := newTask("t1")
	tk.RetryPolicy = RetryPolicy{MaxRetries: 1} // single attempt: first failure is terminal
	if err := f.Create(tk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	stepErr := errors.New("terminal step failure")
	err = f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, stepErr
	})
	if !errors.Is(err, stepErr) {
		t.Fatalf("want propagated step error on exhausted retries, got %v", err)
	}
	task, _ := f.Task("t1")
	if task.State != StateFailed {
		t.Fatalf("want FAILED after retry budget exhausted, got %s", task.State)
	}
}

// TestFabricRunQuantumStaleEpoch verifies the fencing token is enforced at
// the quantum boundary: a stale holder cannot drive the task.
func TestFabricRunQuantumStaleEpoch(t *testing.T) {
	f := NewFabric()
	if err := f.Create(newTask("t1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := f.Release("t1", "agent-a", epoch); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := f.Acquire("t1", "agent-b", time.Minute); err != nil {
		t.Fatalf("B acquire: %v", err)
	}
	// agent-a with its stale epoch must not run the task.
	err = f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return nil, true, nil
	})
	if err != ErrNotOwner {
		t.Fatalf("stale owner must be rejected, got %v", err)
	}
}

// cancelledStepErr mirrors the kernel scheduler's shutdown abort shape
// (scheduler_quantum.go wraps the drain context error around the step).
func cancelledStepErr() error {
	return fmt.Errorf("quantum aborted by scheduler shutdown: %w", context.Canceled)
}

// TestFabricRunQuantumCancellationReleases locks the shutdown contract: a
// cancelled quantum is NOT a task failure. The kernel cancels the drain
// context on scheduler shutdown, so every in-flight step returns
// context.Canceled — routing that through Fail burned the retry budget and,
// for a zero-retry task, finalized it FAILED and cascaded the whole
// downstream subgraph. The durable task.failed event then made the loss
// permanent across restart. Cancellation must hand the task back to READY
// unowned with the retry budget untouched.
func TestFabricRunQuantumCancellationReleases(t *testing.T) {
	f := NewFabric()
	tk := newTask("t1")
	// Zero retry budget: the pre-fix code finalized FAILED on the first
	// "failure", so this case is the sharpest detector.
	tk.RetryPolicy = RetryPolicy{MaxRetries: 0}
	if err := f.Create(tk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	err = f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, cancelledStepErr()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must still be propagated to the caller, got %v", err)
	}
	task, _ := f.Task("t1")
	if task.State != StateReady {
		t.Fatalf("cancelled quantum must return the task to READY, got %s", task.State)
	}
	if task.RetryPolicy.Attempts != 0 {
		t.Fatalf("cancellation must not burn retry budget, Attempts=%d", task.RetryPolicy.Attempts)
	}
	if task.Owner != "" || task.Lease != nil {
		t.Fatalf("cancelled quantum must clear ownership, owner=%q lease=%v", task.Owner, task.Lease)
	}
}

// TestFabricRunQuantumCancellationPreservesCheckpoint locks that a cancelled
// quantum keeps the checkpoint a previous quantum saved: the next acquire
// resumes from the same PCB instead of restarting from scratch.
func TestFabricRunQuantumCancellationPreservesCheckpoint(t *testing.T) {
	f := NewFabric()
	if err := f.Create(newTask("t1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// First quantum makes progress and yields a checkpoint.
	if err := f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return map[string]any{"step": 7}, false, nil
	}); err != nil {
		t.Fatalf("yielding quantum: %v", err)
	}
	// Second quantum is cancelled mid-step (lease re-acquired implicitly by
	// the reaper in production; here we re-acquire explicitly).
	epoch, err = f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	if err := f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, cancelledStepErr()
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want propagated cancellation, got %v", err)
	}
	task, _ := f.Task("t1")
	if task.State != StateReady {
		t.Fatalf("want READY after cancellation, got %s", task.State)
	}
	cp, ok := task.Checkpoint.(map[string]any)
	if !ok || cp["step"] != 7 {
		t.Fatalf("prior checkpoint must survive cancellation, got %+v", task.Checkpoint)
	}
}

// TestFabricRunQuantumCancellationDoesNotCascade locks that a cancelled
// quantum never fails the downstream subgraph: a dependent stays blocked on
// its (now READY again) predecessor rather than being finalized FAILED.
func TestFabricRunQuantumCancellationDoesNotCascade(t *testing.T) {
	f := NewFabric()
	head := newTask("t1")
	head.RetryPolicy = RetryPolicy{MaxRetries: 0}
	if err := f.Create(head); err != nil {
		t.Fatalf("Create t1: %v", err)
	}
	dep := newTask("t2")
	dep.Dependencies = []string{"t1"}
	if err := f.Create(dep); err != nil {
		t.Fatalf("Create t2: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, cancelledStepErr()
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want propagated cancellation, got %v", err)
	}
	dependent, _ := f.Task("t2")
	if dependent.State == StateFailed {
		t.Fatalf("a cancelled predecessor must not cascade FAILED to its dependents")
	}
}

// TestFabricRunQuantumWrappedCancellationReleases verifies detection works
// through error wrapping chains, which is how the kernel surfaces it
// (fmt.Errorf %w around context.Canceled, possibly several layers deep).
func TestFabricRunQuantumWrappedCancellationReleases(t *testing.T) {
	f := NewFabric()
	if err := f.Create(newTask("t1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	epoch, err := f.Acquire("t1", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	deep := fmt.Errorf("executor layer: %w", fmt.Errorf("dispatch: %w", context.Canceled))
	err = f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, deep
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want propagated cancellation, got %v", err)
	}
	task, _ := f.Task("t1")
	if task.State != StateReady {
		t.Fatalf("deeply wrapped cancellation must release to READY, got %s", task.State)
	}
	if task.RetryPolicy.Attempts != 0 {
		t.Fatalf("deeply wrapped cancellation must not burn retry budget, Attempts=%d", task.RetryPolicy.Attempts)
	}
}
