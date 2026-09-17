package taskfabric

import (
	"testing"
	"time"
)

// depTask builds a task with dependencies.
func depTask(id string, deps ...string) *Task {
	return &Task{ID: id, Capability: "rust", Priority: 1, Dependencies: deps}
}

// TestFabricIsReady verifies the dependency gate: a task is ready only when
// READY itself and every dependency is COMPLETED.
func TestFabricIsReady(t *testing.T) {
	f := NewFabric()
	if err := f.Create(depTask("a")); err != nil {
		t.Fatalf("Create a: %v", err)
	}
	if err := f.Create(depTask("b", "a")); err != nil {
		t.Fatalf("Create b: %v", err)
	}
	// a has no deps → ready immediately.
	ready, err := f.IsReady("a")
	if err != nil || !ready {
		t.Fatalf("a must be ready, got %v err=%v", ready, err)
	}
	// b depends on a which is not completed → not ready.
	if ready, _ := f.IsReady("b"); ready {
		t.Fatal("b must not be ready while a is incomplete")
	}
	// Complete a → b becomes ready.
	epoch, err := f.Acquire("a", "agent-x", time.Minute)
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	if err := f.Start("a", "agent-x", epoch); err != nil {
		t.Fatalf("Start a: %v", err)
	}
	if err := f.Complete("a", "agent-x", epoch); err != nil {
		t.Fatalf("Complete a: %v", err)
	}
	if ready, _ := f.IsReady("b"); !ready {
		t.Fatal("b must become ready after a completes")
	}
	// Unknown id.
	if _, err := f.IsReady("nope"); err != ErrTaskNotFound {
		t.Fatalf("want ErrTaskNotFound, got %v", err)
	}
}

// TestFabricReadyTasksDAG verifies the DAG-as-scheduling-source flow: A → B →
// C. Only A is initially ready; after A completes B becomes ready; after B
// completes C becomes ready. No leader dispatch needed.
func TestFabricReadyTasksDAG(t *testing.T) {
	f := NewFabric()
	if err := f.Create(depTask("a")); err != nil {
		t.Fatalf("Create a: %v", err)
	}
	if err := f.Create(depTask("b", "a")); err != nil {
		t.Fatalf("Create b: %v", err)
	}
	if err := f.Create(depTask("c", "b")); err != nil {
		t.Fatalf("Create c: %v", err)
	}

	// Stage 1: only a is ready.
	ready := f.ReadyTasks()
	if len(ready) != 1 || ready[0] != "a" {
		t.Fatalf("stage 1: want [a], got %v", ready)
	}
	// Complete a.
	if err := completeSimple(f, t, "a"); err != nil {
		t.Fatalf("complete a: %v", err)
	}
	// Stage 2: b becomes ready, c still blocked.
	ready = f.ReadyTasks()
	if len(ready) != 1 || ready[0] != "b" {
		t.Fatalf("stage 2: want [b], got %v", ready)
	}
	// Complete b.
	if err := completeSimple(f, t, "b"); err != nil {
		t.Fatalf("complete b: %v", err)
	}
	// Stage 3: c becomes ready.
	ready = f.ReadyTasks()
	if len(ready) != 1 || ready[0] != "c" {
		t.Fatalf("stage 3: want [c], got %v", ready)
	}
}

// TestFailCascadesToDependents is the failure-propagation contract: a terminal
// FAILED task must not strand its downstream subgraph in READY forever.
// Pre-fix, depsCompletedLocked only accepted COMPLETED, so one exhausted task
// made every transitive dependent permanently unschedulable (never in
// ReadyTasks, protected from the reaper, and PlanLoop.round active forever).
func TestFailCascadesToDependents(t *testing.T) {
	f := NewFabric()
	for _, tk := range []*Task{
		{ID: "a", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 0}},
		{ID: "b", Capability: "rust", Dependencies: []string{"a"}},
		{ID: "c", Capability: "rust", Dependencies: []string{"b"}},
		// d is on a parallel branch off a: it must cascade too.
		{ID: "d", Capability: "rust", Dependencies: []string{"a"}},
	} {
		if err := f.Create(tk); err != nil {
			t.Fatalf("Create %s: %v", tk.ID, err)
		}
	}
	epoch, err := f.Acquire("a", "agent-x", time.Minute)
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	if err := f.Start("a", "agent-x", epoch); err != nil {
		t.Fatalf("Start a: %v", err)
	}
	if err := f.Fail("a", "agent-x", epoch); err != nil {
		t.Fatalf("Fail a: %v", err)
	}

	for _, id := range []string{"a", "b", "c", "d"} {
		tk, err := f.Task(id)
		if err != nil {
			t.Fatalf("Task %s: %v", id, err)
		}
		if tk.State != StateFailed {
			t.Fatalf("task %s: want FAILED, got %s", id, tk.State)
		}
	}
	// Provenance: the cascaded tasks name the task that failed underneath
	// them; the root failure names nothing.
	if root, _ := f.Task("a"); root.FailedDependency != "" {
		t.Fatalf("root a must not carry FailedDependency, got %q", root.FailedDependency)
	}
	if b, _ := f.Task("b"); b.FailedDependency != "a" {
		t.Fatalf("b.FailedDependency: want a, got %q", b.FailedDependency)
	}
	if c, _ := f.Task("c"); c.FailedDependency != "b" {
		t.Fatalf("c.FailedDependency: want b (transitive), got %q", c.FailedDependency)
	}
	if ready := f.ReadyTasks(); len(ready) != 0 {
		t.Fatalf("no task may stay ready after cascade, got %v", ready)
	}
	// Harvestability: Delete accepts terminal tasks, so the reaper can now
	// reclaim the whole subgraph instead of leaving it as protected READY.
	for _, id := range []string{"b", "c", "d", "a"} {
		if err := f.Delete(id); err != nil {
			t.Fatalf("Delete %s after cascade: %v", id, err)
		}
	}
}

// TestFailRetryRequeueDoesNotCascade locks the non-cascade side of the
// contract: a requeued failure (retry budget remains) is not terminal, so
// dependents must stay READY and unscheduled — they may still run once the
// predecessor's retry succeeds.
func TestFailRetryRequeueDoesNotCascade(t *testing.T) {
	f := NewFabric()
	for _, tk := range []*Task{
		{ID: "a", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 2}},
		{ID: "b", Capability: "rust", Dependencies: []string{"a"}},
	} {
		if err := f.Create(tk); err != nil {
			t.Fatalf("Create %s: %v", tk.ID, err)
		}
	}
	epoch, err := f.Acquire("a", "agent-x", time.Minute)
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	if err := f.Start("a", "agent-x", epoch); err != nil {
		t.Fatalf("Start a: %v", err)
	}
	if err := f.Fail("a", "agent-x", epoch); err != nil {
		t.Fatalf("Fail a: %v", err)
	}
	if a, _ := f.Task("a"); a.State != StateReady {
		t.Fatalf("a must requeue to READY, got %s", a.State)
	}
	if b, _ := f.Task("b"); b.State != StateReady || b.FailedDependency != "" {
		t.Fatalf("b must stay untouched READY, got state=%s dep=%q", b.State, b.FailedDependency)
	}
}

// TestCascadeEmitsFailedEventsPerDependent locks observability: every
// cascaded task records its own task.failed, so the event log (and any
// subscriber, including the L2 answer-failure release) sees the subgraph die.
func TestCascadeEmitsFailedEventsPerDependent(t *testing.T) {
	f := NewFabric()
	for _, tk := range []*Task{
		{ID: "a", Capability: "rust", RetryPolicy: RetryPolicy{}},
		{ID: "b", Capability: "rust", Dependencies: []string{"a"}},
	} {
		if err := f.Create(tk); err != nil {
			t.Fatalf("Create %s: %v", tk.ID, err)
		}
	}
	epoch, _ := f.Acquire("a", "agent-x", time.Minute)
	if err := f.Start("a", "agent-x", epoch); err != nil {
		t.Fatalf("Start a: %v", err)
	}
	if err := f.Fail("a", "agent-x", epoch); err != nil {
		t.Fatalf("Fail a: %v", err)
	}
	var failedB bool
	for _, ev := range f.Events() {
		if ev.Type == EventTaskFailed && ev.TaskID == "b" {
			failedB = true
			if ev.State != StateFailed {
				t.Fatalf("b's failed event must carry FAILED state, got %s", ev.State)
			}
		}
	}
	if !failedB {
		t.Fatal("cascade must record a task.failed event for dependent b")
	}
}

// completeSimple acquires, starts and completes a task via the fabric
// (LEASED → RUNNING → COMPLETED).
func completeSimple(f *Fabric, t *testing.T, id string) error {
	epoch, err := f.Acquire(id, "agent-x", time.Minute)
	if err != nil {
		return err
	}
	if err := f.Start(id, "agent-x", epoch); err != nil {
		return err
	}
	return f.Complete(id, "agent-x", epoch)
}
