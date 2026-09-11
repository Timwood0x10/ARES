package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/task"
)

// The HIGH regressions for the kernel scheduler:
// need-based preemption quantity (#2) and the panic-guard registration gap
// between TryBegin and the quantum (#3).

// --- #2: need-based preemption quantity ---

// acquireAndStart drives a READY task to RUNNING under the given agent, the
// minimal setup the preemption tests need (the fabric's own acquire/start
// path, no scheduler loop involved).
func acquireAndStart(t *testing.T, f *taskfabric.Fabric, id, agent string) {
	t.Helper()
	epoch, err := f.Acquire(id, agent, time.Minute)
	if err != nil {
		t.Fatalf("Acquire %s: %v", id, err)
	}
	if err := f.Start(id, agent, epoch); err != nil {
		t.Fatalf("Start %s: %v", id, err)
	}
}

// TestPreemptLowerPrioritySkipsWhenFreeCapableExecutorExists pins the
// need-based half of the preemption contract: a higher-priority READY task
// must NOT preempt a lower-priority RUNNING task while a free, capable agent
// exists to run it. The regression: PreemptLowerPriority preempted EVERY
// lower-priority RUNNING task whenever any prioritized ready task existed —
// churning in-flight quantum work even though the ready task could be
// scheduled without evicting anyone.
func TestPreemptLowerPrioritySkipsWhenFreeCapableExecutorExists(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"low":  &smokeExecutor{id: "low", typ: models.AgentType("batch")},
		"high": &smokeExecutor{id: "high", typ: models.AgentType("urgent")},
	}, NewLoadTracker())

	if err := fabric.Create(&taskfabric.Task{
		ID: "low-task", Capability: "batch", Priority: 1,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create low: %v", err)
	}
	acquireAndStart(t, fabric, "low-task", "low")

	// The high-priority task needs the "urgent" capability — exactly what
	// the idle "high" executor provides.
	if err := fabric.Create(&taskfabric.Task{
		ID: "high-task", Capability: "urgent", Priority: 5,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create high: %v", err)
	}

	sched.PreemptLowerPriority(fabric.ResumableTasks())

	tk, err := fabric.Task("low-task")
	if err != nil {
		t.Fatalf("Task low: %v", err)
	}
	if tk.State != taskfabric.StateRunning {
		t.Fatalf("low-priority task must keep RUNNING while a free capable executor can take the high-priority work, got %s", tk.State)
	}
	if tk.Owner != "low" {
		t.Fatalf("low-priority task must keep its owner, got %q", tk.Owner)
	}
}

// TestPreemptLowerPrioritySkipsWhenIncapableFreeExecutorIsIrrelevant pins
// the capability-aware half: a free executor that CANNOT run the
// prioritized ready task does not count as capacity, so the low-priority
// RUNNING task IS preempted to free a capable agent.
func TestPreemptLowerPriorityIncapableFreeExecutorDoesNotCount(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"solo": &smokeExecutor{id: "solo", typ: models.AgentType("batch")},
		// Idle but useless for the high-priority task's capability.
		"other": &smokeExecutor{id: "other", typ: models.AgentType("trading")},
	}, NewLoadTracker())

	if err := fabric.Create(&taskfabric.Task{
		ID: "low-task", Capability: "batch", Priority: 1,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create low: %v", err)
	}
	acquireAndStart(t, fabric, "low-task", "solo")

	if err := fabric.Create(&taskfabric.Task{
		ID: "high-task", Capability: "batch", Priority: 5,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create high: %v", err)
	}

	sched.PreemptLowerPriority(fabric.ResumableTasks())

	tk, err := fabric.Task("low-task")
	if err != nil {
		t.Fatalf("Task low: %v", err)
	}
	if tk.State != taskfabric.StateReady {
		t.Fatalf("low-priority task must be preempted when no free CAPABLE executor exists, got %s", tk.State)
	}
}

// TestPreemptLowerPriorityPreemptsOnlyAsManyAsNeeded pins the quantity
// bound: with the capable pool saturated by N running tasks and only one
// prioritized ready task, exactly ONE preemption happens — the
// lowest-priority running task. The regression: all three lower-priority
// running tasks were preempted at once.
func TestPreemptLowerPriorityPreemptsOnlyAsManyAsNeeded(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"e1": &smokeExecutor{id: "e1", typ: models.AgentType("batch")},
		"e2": &smokeExecutor{id: "e2", typ: models.AgentType("batch")},
		"e3": &smokeExecutor{id: "e3", typ: models.AgentType("batch")},
	}, NewLoadTracker())

	running := []struct {
		id       string
		priority int
		agent    string
	}{
		{"l1", 1, "e1"},
		{"l2", 2, "e2"},
		{"l3", 3, "e3"},
	}
	for _, r := range running {
		if err := fabric.Create(&taskfabric.Task{
			ID: r.id, Capability: "batch", Priority: r.priority,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("Create %s: %v", r.id, err)
		}
		acquireAndStart(t, fabric, r.id, r.agent)
	}

	if err := fabric.Create(&taskfabric.Task{
		ID: "high-task", Capability: "batch", Priority: 5,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create high: %v", err)
	}

	sched.PreemptLowerPriority(fabric.ResumableTasks())

	// Exactly the lowest-priority running task is preempted.
	assertState := func(id string, want taskfabric.TaskState) {
		t.Helper()
		tk, err := fabric.Task(id)
		if err != nil {
			t.Fatalf("Task %s: %v", id, err)
		}
		if tk.State != want {
			t.Fatalf("%s state = %s, want %s", id, tk.State, want)
		}
	}
	assertState("l1", taskfabric.StateReady)
	assertState("l2", taskfabric.StateRunning)
	assertState("l3", taskfabric.StateRunning)
}

// --- #3: panic-guard registration gap ---

// panicOnceHook is a QuantumHook whose FIRST BeforeQuantum call panics —
// user plugin code executing in the gap between TryBegin and the quantum.
type panicOnceHook struct {
	panicked bool
}

func (h *panicOnceHook) BeforeQuantum(_ context.Context, _, _ string) error {
	if !h.panicked {
		h.panicked = true
		panic("beforeQuantum: plugin panic in the TryBegin gap")
	}
	return nil
}

func (h *panicOnceHook) AfterQuantum(_ context.Context, _, _ string, _ error) {}

// TestBeforeQuantumPanicDoesNotLeakLoadSlot pins the panic-guard continuity:
// a panic in the beforeQuantum hook (which runs after TryBegin takes the
// busy slot and, pre-fix, before the panic guard was registered) must still
// release the LoadTracker slot. The regression: the slot leaked forever —
// load stays 1, Score's (1-load) factor zeroes the agent, TryBegin refuses
// every later quantum, and the task can never complete (it is requeued by
// lease expiry into a world where its only executor is permanently
// "unschedulable").
func TestBeforeQuantumPanicDoesNotLeakLoadSlot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	tracker := NewLoadTracker()
	exec := &smokeExecutor{id: "solo", typ: models.AgentType("batch")}
	sched := New(fabric, map[string]CapabilityExecutor{"solo": exec}, tracker)
	hook := &panicOnceHook{}
	sched.WithQuantumHook(hook)
	// Short lease so the abandoned LEASED task (the panic left it leased) is
	// requeued by expiry quickly instead of stalling for the 5m default.
	sched.WithTTL(150 * time.Millisecond)
	sched.PollInterval = 10 * time.Millisecond
	go sched.Run(ctx)

	// Stand in for the recovery loop's periodic expired-lease sweep: without
	// it, a bare kernel test fabric never requeues the abandoned lease.
	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fabric.CheckExpiredLeases()
			}
		}
	}()

	if err := fabric.Create(&taskfabric.Task{
		ID: "panic-task", Capability: "batch",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The task must still complete: the panic consumed one attempt path, the
	// slot was released, the lease expired, and the next drain re-scheduled.
	waitForTaskState(t, fabric, "panic-task", taskfabric.StateCompleted, 5*time.Second)

	if !hook.panicked {
		t.Fatal("the beforeQuantum panic path was never exercised")
	}
	// The busy slot must be back to zero (released on the panic path AND on
	// the successful retry's endQuantumOutcome).
	if got := tracker.Load("solo"); got != 0 {
		t.Fatalf("load slot leaked after beforeQuantum panic: load = %v, want 0", got)
	}
}
