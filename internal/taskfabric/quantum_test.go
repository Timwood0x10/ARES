package taskfabric

import (
	"errors"
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

// TestFabricRunQuantumYields verifies D1 + SUSPENDED semantics lock:
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
// READY when the retry policy allows).
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
	err = f.RunQuantum("t1", "agent-a", epoch, func() (any, bool, error) {
		return nil, false, errors.New("step blew up")
	})
	if err != nil {
		t.Fatalf("RunQuantum must swallow step error into FAIL: %v", err)
	}
	task, _ := f.Task("t1")
	if task.State != StateReady {
		t.Fatalf("want requeue to READY (retry allowed), got %s", task.State)
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
