package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// stubLeaderDispatcher records legacy dispatch calls and optionally fails.
type stubLeaderDispatcher struct {
	calls   int
	err     error
	lastTID string
}

func (s *stubLeaderDispatcher) Dispatch(_ context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	results := make([]*models.TaskResult, 0, len(tasks))
	for _, t := range tasks {
		if t != nil {
			s.lastTID = t.TaskID
			res := models.NewTaskResult(t.TaskID, t.AgentType)
			res.SetSuccess(nil, "ok")
			results = append(results, res)
		}
	}
	return results, nil
}

var _ leader.TaskDispatcher = (*stubLeaderDispatcher)(nil)

// TestWireKernelDispatcherDefaults verifies the assembly: flag defaults to
// legacy, shadow mode is on, and both tracks are wired.
func TestWireKernelDispatcherDefaults(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	if !flag.IsLegacy() {
		t.Fatal("flag must default to legacy policy")
	}
	if kernel == nil {
		t.Fatal("kernel dispatcher must not be nil")
	}
	// A dispatch with a capable candidate must succeed with zero mismatches
	// (legacy ok, shadow picks code_01).
	payload := map[string]any{"agent_type": "code"}
	if err := kernel.Dispatch(context.Background(), "", "t1", payload); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if kernel.Mismatches() != 0 {
		t.Fatalf("want 0 mismatches, got %d", kernel.Mismatches())
	}
	if inner.calls != 1 {
		t.Fatalf("legacy dispatcher must be called once, got %d", inner.calls)
	}
}

// TestWireKernelDispatcherShadowMismatch verifies shadow detects a divergence:
// legacy succeeds but the taskfabric path finds no capable candidate.
func TestWireKernelDispatcherShadowMismatch(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, _ := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	// Task requires a capability no candidate has → shadow fails, legacy ok.
	payload := map[string]any{"agent_type": "rust"}
	if err := kernel.Dispatch(context.Background(), "", "t1", payload); err != nil {
		t.Fatalf("legacy dispatch must succeed, got %v", err)
	}
	if kernel.Mismatches() != 1 {
		t.Fatalf("want 1 mismatch (no capable candidate), got %d", kernel.Mismatches())
	}
}

// TestWireKernelDispatcherLegacyErrorPropagates verifies a legacy failure
// propagates as the dispatch error (active path = legacy).
func TestWireKernelDispatcherLegacyErrorPropagates(t *testing.T) {
	inner := &stubLeaderDispatcher{err: errors.New("legacy down")}
	kernel, _ := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	payload := map[string]any{"agent_type": "code"}
	if err := kernel.Dispatch(context.Background(), "", "t1", payload); err == nil {
		t.Fatal("legacy error must propagate")
	}
}

// TestKernelTaskDispatcherBatchAdapter verifies the batch adapter routes every
// task through the kernel and returns the leader-shaped results. With no
// fabric and no event store the legacy path runs synchronously (each task
// completes through the inner dispatcher), so results succeed without faking
// worker output.
func TestKernelTaskDispatcherBatchAdapter(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	// No fabric (legacy path): tasks run synchronously through the inner
	// dispatcher. With a store wired the batch adapter observes completion via
	// the event contract; without fabric there is nothing async to wait for,
	// so each task reports a real (legacy) success.
	adapter := newKernelTaskDispatcher(kernel, ares_events.NewMemoryEventStore())

	tasks := []*models.Task{
		models.NewTask("t1", models.AgentTypeTop, nil),
		models.NewTask("t2", models.AgentTypeTop, nil),
	}
	results, err := adapter.Dispatch(context.Background(), tasks)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	// Legacy sync path (no fabric): the inner dispatcher completed each task,
	// so success is real — but the reason must NOT be a fake worker output.
	for _, r := range results {
		if !r.Success {
			t.Fatalf("legacy sync dispatch must succeed, got %+v", r)
		}
	}
	if inner.calls != 2 {
		t.Fatalf("legacy dispatcher must be called once per task, got %d", inner.calls)
	}
	if flag.IsTaskFabric() {
		t.Fatal("flag must stay legacy after batch dispatch")
	}
}

// TestTaskFromPayload verifies payload decoding: agent_type is honored, absent
// metadata falls back to a default type.
func TestTaskFromPayload(t *testing.T) {
	task, err := taskFromPayload("t1", map[string]any{"agent_type": "rust"})
	if err != nil {
		t.Fatalf("taskFromPayload: %v", err)
	}
	if task.TaskID != "t1" || task.AgentType != models.AgentType("rust") {
		t.Fatalf("unexpected task: %+v", task)
	}
	if _, err := taskFromPayload("", nil); err == nil {
		t.Fatal("empty task id must error")
	}
	if _, err := taskFromPayload("t2", nil); err != nil {
		t.Fatalf("nil payload must not error: %v", err)
	}
}

// TestTaskFromPayloadRestoresDependencies verifies the DAG wiring: the kernel
// dispatch payload carries Context.Dependencies through the agentipc hop so
// executeFabricTask can create the fabric task with its DAG edges.
func TestTaskFromPayloadRestoresDependencies(t *testing.T) {
	cases := []struct {
		name string
		// deps is the payload value for "dependencies": []string comes from
		// kernelTaskDispatcher.Dispatch (in-memory hop), []any comes from a
		// JSON round-trip.
		deps any
	}{
		{name: "in-memory []string", deps: []string{"task_a", "task_b"}},
		{name: "json-shaped []any", deps: []any{"task_a", "task_b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{
				"agent_type":   "write",
				"dependencies": tc.deps,
			}
			task, err := taskFromPayload("t1", payload)
			if err != nil {
				t.Fatalf("taskFromPayload: %v", err)
			}
			if !slicesEqual(task.Context.Dependencies, []string{"task_a", "task_b"}) {
				t.Fatalf("dependencies not restored: %v", task.Context.Dependencies)
			}
		})
	}
}

// TestKernelDAGGateDefersDependentTask verifies the DAG-as-scheduling-source
// wiring in the kernel path (ares-runtime.md §9): the leader dispatch SUBMITS
// tasks to the fabric (submitFabricTask) and the kernelScheduler drains only
// READY tasks — a task whose dependencies are not all COMPLETED stays queued
// until its dependency completes (it never executes out of order).
func TestKernelDAGGateDefersDependentTask(t *testing.T) {
	f := taskfabric.NewFabric()
	research := &stubAgent{id: "research_01", typ: models.AgentType("research")}
	writer := &stubAgent{id: "writer_01", typ: models.AgentType("write")}
	executors := map[string]CapabilityExecutor{"research_01": research, "writer_01": writer}
	tracker := newLoadTracker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The scheduler drains READY tasks; the DAG gate means B waits for A.
	sched := NewKernelScheduler(f, executors, tracker)
	sched.pollInterval = 10 * time.Millisecond
	go sched.Run(ctx)

	// Submit B (depends on A) first: it must NOT run before A completes.
	b := models.NewTask("task_b", models.AgentType("write"), nil)
	b.Context.Dependencies = []string{"task_a"}
	if err := submitFabricTask(ctx, f, b); err != nil {
		t.Fatalf("submitFabricTask(B): %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if writer.executedCount() != 0 {
		t.Fatal("B must not execute before its dependency A completes")
	}

	// Submit A: the scheduler runs it, completing A and unlocking B.
	a := models.NewTask("task_a", models.AgentType("research"), nil)
	if err := submitFabricTask(ctx, f, a); err != nil {
		t.Fatalf("submitFabricTask(A): %v", err)
	}

	// Both tasks must complete, in dependency order.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if research.executedCount() == 1 && writer.executedCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if research.executedCount() != 1 {
		t.Fatalf("A must execute once, got %d", research.executedCount())
	}
	if writer.executedCount() != 1 {
		t.Fatalf("B must execute exactly once after A completes, got %d", writer.executedCount())
	}
}

// slicesEqual is a tiny helper comparing two string slices (test-only).
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestEnableKernelExecutionRunsFabricPath verifies that after flipping to the
// Task Fabric policy, the kernel's new path executes the task through the
// fabric (Create→Schedule→Acquire→RunQuantum) instead of scoring only, and
// that shadow mode is turned off (so the legacy path is not re-run).
func TestEnableKernelExecutionRunsFabricPath(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})

	f := taskfabric.NewFabric()
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code")}
	executors := map[string]CapabilityExecutor{"code_01": executor}
	tracker := newLoadTracker()

	// Flip: replace the shadow scorer with the submit-only new path, disable
	// shadow. The leader dispatch now SUBMITS the task; the kernelScheduler
	// is the single executor (GAP #2: no double-path acquire race).
	enableKernelExecution(kernel, f)
	flag.Set(agentipc.PolicyTaskFabric)

	// Dispatch through the kernel (active path = fabric submit).
	payload := map[string]any{"agent_type": "code"}
	if err := kernel.Dispatch(context.Background(), "", "t1", payload); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// After dispatch the task is SUBMITTED (READY), not yet executed — the
	// scheduler must complete it.
	tk, err := f.Task("t1")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if tk.State != taskfabric.StateReady {
		t.Fatalf("after dispatch want READY (submitted, scheduler owns execution), got %s", tk.State)
	}
	if executor.executedCount() != 0 {
		t.Fatalf("dispatch must NOT execute the task (scheduler owns execution), got %d", executor.executedCount())
	}

	// Now the kernelScheduler drains it to completion (single executor).
	sched := NewKernelScheduler(f, executors, tracker)
	sched.pollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tk, err = f.Task("t1")
		if err == nil && tk.State == taskfabric.StateCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, err = f.Task("t1")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if tk.State != taskfabric.StateCompleted {
		t.Fatalf("want COMPLETED after scheduler drain, got %s", tk.State)
	}
	if executor.executedCount() != 1 {
		t.Fatalf("executor must run once, got %d", executor.executedCount())
	}
	// Shadow is off: the legacy path must NOT have run.
	if inner.calls != 0 {
		t.Fatalf("legacy dispatcher must not run after flip (shadow off), got %d calls", inner.calls)
	}
}

// TestWireKernelPolicyTaskFabric flips the policy via config and verifies the
// scheduler starts draining ready tasks to completion.
func TestWireKernelPolicyTaskFabric(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	kernelHandle := &kernelHandle{dual: kernel, flag: flag}

	executor := &stubAgent{id: "code_01", typ: models.AgentType("code")}
	cfg := &ares_config.Config{}
	cfg.Kernel.Policy = "taskfabric"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// wireKernelPolicy starts its own scheduler goroutine; the created fabric
	// is internal, so we drive a task by creating it through the kernel's new
	// path. But wireKernelPolicy's fabric is not reachable here — instead we
	// verify the policy flip + scheduler start via a dispatcher dispatch. The
	// EventStore is nil in this test: the recovery loop degrades to periodic
	// sweeps, which is fine for a unit test.
	wireKernelPolicy(ctx, cfg, kernelHandle, []sub.Agent{executor}, nil)
	if !flag.IsTaskFabric() {
		t.Fatal("flag must be TaskFabric after wireKernelPolicy(taskfabric)")
	}

	// The kernel's new path is now the submit-only fabric submitter; the
	// scheduler goroutine started by wireKernelPolicy drains the submitted
	// task. Dispatch submits; we poll until the scheduler completes it.
	adapter := &kernelTaskDispatcher{kernel: kernel}
	task := models.NewTask("t-policy", models.AgentType("code"), nil)
	results, err := adapter.Dispatch(ctx, []*models.Task{task})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && executor.executedCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if executor.executedCount() == 0 {
		t.Fatal("executor must have run via the fabric scheduler")
	}
}

// TestFlipKernelToTaskFabricLive verifies the live mid-run flip: a task
// dispatched while the kernel is still on the legacy policy runs through the
// leader; after flipKernelToTaskFabric, new dispatches run through the Task
// Fabric path (flag flipped, shadow off, no double execution).
func TestFlipKernelToTaskFabricLive(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	handle := &kernelHandle{dual: kernel, flag: flag}
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adapter := &kernelTaskDispatcher{kernel: kernel}

	// Before the flip: legacy leader dispatches (shadow scoring observes).
	if _, err := adapter.Dispatch(ctx, []*models.Task{
		models.NewTask("t-legacy", models.AgentType("code"), nil),
	}); err != nil {
		t.Fatalf("legacy dispatch: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("legacy dispatcher must run before flip, got %d calls", inner.calls)
	}
	if executor.executedCount() != 0 {
		t.Fatal("executor must not run before flip (shadow only)")
	}

	// Live flip: same entry a runtime operator would call.
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor}, nil)
	if !flag.IsTaskFabric() {
		t.Fatal("flag must be TaskFabric after live flip")
	}

	// After the flip: new dispatches submit through the fabric path. The
	// legacy dispatcher must NOT be called again (shadow off, no double
	// execution). The scheduler goroutine started by the flip drains the
	// submitted task; poll until it executes exactly once.
	if _, err := adapter.Dispatch(ctx, []*models.Task{
		models.NewTask("t-fabric", models.AgentType("code"), nil),
	}); err != nil {
		t.Fatalf("fabric dispatch: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && executor.executedCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if executor.executedCount() != 1 {
		t.Fatalf("executor must run exactly once via fabric path, got %d", executor.executedCount())
	}
	if inner.calls != 1 {
		t.Fatalf("legacy dispatcher must not run after flip, got %d calls", inner.calls)
	}
}

// TestFlipKernelToTaskFabricIdempotent verifies a second live flip is a no-op:
// no second scheduler is started (a second scheduler would double-drain
// ReadyTasks and double-execute tasks).
func TestFlipKernelToTaskFabricIdempotent(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	handle := &kernelHandle{dual: kernel, flag: flag}
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor}, nil)
	if !flag.IsTaskFabric() {
		t.Fatal("flag must be TaskFabric after first flip")
	}

	// Second flip: no-op (kernel.flipped guard). Submitting a task afterwards
	// must run it exactly once — a second scheduler would double-execute.
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor}, nil)
	adapter := &kernelTaskDispatcher{kernel: kernel}
	if _, err := adapter.Dispatch(ctx, []*models.Task{
		models.NewTask("t1", models.AgentType("code"), nil),
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && executor.executedCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if executor.executedCount() != 1 {
		t.Fatalf("task must execute exactly once after double flip, got %d", executor.executedCount())
	}
}

// TestDispatchRacingLiveFlipNoLoss verifies the mid-run flip boundary under
// concurrency (-race): dispatches racing the flag flip are each executed
// exactly once — via legacy before the flip, via the fabric executor after —
// with no task lost and none double-executed.
func TestDispatchRacingLiveFlipNoLoss(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	handle := &kernelHandle{dual: kernel, flag: flag}
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adapter := &kernelTaskDispatcher{kernel: kernel}

	const total = 60
	var wg sync.WaitGroup
	start := make(chan struct{})
	// Half the dispatches race the flip from concurrent goroutines.
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			if _, err := adapter.Dispatch(ctx, []*models.Task{
				models.NewTask(fmt.Sprintf("t-%d", n), models.AgentType("code"), nil),
			}); err != nil {
				t.Errorf("dispatch %d: %v", n, err)
			}
		}(i)
	}
	close(start)
	// Flip while dispatches are in flight.
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor}, nil)
	wg.Wait()

	// Every dispatch must eventually execute exactly once: legacy counts +
	// fabric executions must equal the number of dispatched tasks (no loss, no
	// double-run). Since GAP4 the kernel scheduler drains the fabric asynchron
	// (pollInterval 500ms): a task whose single-slot executor is busy is queued
	// READY in the fabric and executed by the scheduler's next drain, so the
	// final count is reached asynchronously — poll instead of asserting
	// immediately after the dispatches return.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if inner.calls+executor.executedCount() == total {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	legacyCalls := inner.calls
	fabricRuns := executor.executedCount()
	if legacyCalls+fabricRuns != total {
		t.Fatalf("execution loss or duplication: legacy=%d fabric=%d total=%d want %d",
			legacyCalls, fabricRuns, legacyCalls+fabricRuns, total)
	}
	if fabricRuns > total {
		t.Fatalf("double execution: fabric=%d exceeds total=%d", fabricRuns, total)
	}
}

// TestWireKernelPolicyWiresLifecycle verifies the Kernel integration fix
// (code-review-2025-01-16 #1 + #4): flipping to policy=taskfabric also
// assembles the Lifecycle pillar (agentfabric + aresrecovery) under the same
// unified kernel entry, and the P5 resource budget from config is enforced at
// spawn (claims over budget are rejected).
func TestWireKernelPolicyWiresLifecycle(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	handle := &kernelHandle{dual: kernel, flag: flag}
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code")}
	cfg := &ares_config.Config{}
	cfg.Kernel.Policy = "taskfabric"
	cfg.Kernel.Resources = map[string]float64{"cpu": 4}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wireKernelPolicy(ctx, cfg, handle, []sub.Agent{executor}, nil)

	// Lifecycle pillar wired: agents fabric + recovery present.
	handle.mu.Lock()
	agents := handle.agents
	recovery := handle.recovery
	handle.mu.Unlock()
	if agents == nil {
		t.Fatal("kernel.agents must be wired after wireKernelPolicy(taskfabric)")
	}
	if recovery == nil {
		t.Fatal("kernel.recovery must be wired after wireKernelPolicy(taskfabric)")
	}
	if !flag.IsTaskFabric() {
		t.Fatal("flag must be TaskFabric")
	}

	// Resource enforcement (code-review #4): the budget came from config, so a
	// spawn claiming more than the remaining budget is rejected.
	_, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "a1",
		Capabilities: []string{"code"},
		Resources:    map[string]any{"cpu": 2},
	})
	if err != nil {
		t.Fatalf("spawn within budget: %v", err)
	}
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "a2",
		Capabilities: []string{"code"},
		Resources:    map[string]any{"cpu": 3}, // 2 + 3 > 4
	}); !errors.Is(err, agentfabric.ErrResourceQuotaExceeded) {
		t.Fatalf("over-budget spawn must be rejected, got %v", err)
	}
}

// TestRunKernelRecoveryLoopEventDriven verifies the event-driven recovery loop
// (code-review-2025-01-16 #2): task lifecycle events on the shared EventStore
// drive the recovery chain instead of a command loop. Publishing an
// EventTaskExpired event triggers the kernel's requeue-only recovery (v0.3.0
// review Bug 1 fix): the expired task returns to READY, UNOWNED — NOT re-leased
// to a phantom replacement agent that no registered executor can drive. The
// kernelScheduler (which owns execution) picks up the READY task and resumes
// from its preserved checkpoint.
func TestRunKernelRecoveryLoopEventDriven(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	// A task fabric with one READY task whose lease is expired.
	tf := taskfabric.NewFabric().WithEventStore(store)
	if err := tf.Create(&taskfabric.Task{ID: "t1", Capability: "code"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Acquire to put it on a lease.
	if _, err := tf.Acquire("t1", "agent-a", 5*time.Minute); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Expire the lease by advancing the clock past the TTL.
	tf.WithClock(func() time.Time { return time.Now().Add(10 * time.Minute) })

	agents := agentfabric.NewFabric()
	recovery := aresrecovery.New(tf, agents, aresrecovery.DefaultRestartPolicy())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runKernelRecoveryLoop(ctx, store, recovery, kernelLoopConfig{}, nil, nil, nil)

	// Publish the TaskExpired event (as CheckExpiredLeases would) and wait for
	// the requeue: the expired lease returns to READY, unowned. The OLD
	// behavior handed it to a freshly-spawned replacement agent (LEASED to a
	// phantom — see Bug 1); the kernel now requeues only and lets the
	// scheduler re-acquire with registered executors.
	if err := store.Append(ctx, "t1", []*ares_events.Event{
		{Type: ares_events.EventTaskExpired, StreamID: "t1", Payload: map[string]any{}},
	}, 0); err != nil {
		t.Fatalf("append expired event: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tk, err := tf.Task("t1")
		if err == nil && tk.State == taskfabric.StateReady && tk.Owner == "" {
			return // recovered: requeued to READY, awaiting a registered executor
		}
		time.Sleep(20 * time.Millisecond)
	}
	tk, err := tf.Task("t1")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	t.Fatalf("task must be requeued to READY unowned after recovery (no phantom agent), state=%s owner=%q", tk.State, tk.Owner)
}

// resultStubAgent is a sub.Agent stub that returns a real worker result
// (items + reason) and records the UserProfile it was executed with, so the
// tests can verify the profile/result reflux chain end to end.
type resultStubAgent struct {
	id        string
	typ       models.AgentType
	mu        sync.Mutex
	executed  []string
	profiles  []*models.UserProfile
	lastItems []*models.RecommendItem
	lastReas  string
}

var _ sub.Agent = (*resultStubAgent)(nil)

func (a *resultStubAgent) ID() string                  { return a.id }
func (a *resultStubAgent) Type() models.AgentType      { return a.typ }
func (a *resultStubAgent) Status() models.AgentStatus  { return models.AgentStatusReady }
func (a *resultStubAgent) Start(context.Context) error { return nil }
func (a *resultStubAgent) Stop(context.Context) error  { return nil }
func (a *resultStubAgent) Process(context.Context, any) (any, error) {
	return nil, nil
}
func (a *resultStubAgent) ProcessStream(context.Context, any) (<-chan base.AgentEvent, error) {
	return nil, nil
}
func (a *resultStubAgent) Execute(_ context.Context, task *models.Task) (*models.TaskResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.executed = append(a.executed, task.TaskID)
	a.profiles = append(a.profiles, task.UserProfile)
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess([]*models.RecommendItem{
		{ItemID: "item-1", Name: "First"},
		{ItemID: "item-2", Name: "Second"},
	}, "worker real reason")
	a.lastItems = res.Items
	a.lastReas = res.Reason
	return res, nil
}
func (a *resultStubAgent) ExecuteStep(_ context.Context, task *models.Task) (*sub.StepOutcome, error) {
	// Stub has no internal loop: the whole run completes in one quantum.
	res, _ := a.Execute(context.Background(), task)
	return &sub.StepOutcome{Done: true, Result: res}, nil
}

func (a *resultStubAgent) profileCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.profiles)
}

// TestKernelDispatchEventDrivenResultReflux is the contract test for the serve
// result-reflux fix: a dispatch through the flipped kernel must return the
// WORKER's real outcome (items/reason, not a placeholder success), and the
// worker must see the submission-time UserProfile (no more profile==nil
// degradation to an empty fallback).
func TestKernelDispatchEventDrivenResultReflux(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	handle := &kernelHandle{dual: kernel, flag: flag}
	executor := &resultStubAgent{id: "code_01", typ: models.AgentType("code")}
	store := ares_events.NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adapter := newKernelTaskDispatcher(kernel, store)
	handle.taskDispatcher = adapter
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor}, store)

	profile := &models.UserProfile{UserID: "u-1"}
	task := models.NewTask("t-reflux", models.AgentType("code"), profile)
	task.Payload = map[string]any{"task_desc": "do the thing"}
	results, err := adapter.Dispatch(ctx, []*models.Task{task})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Success {
		t.Fatalf("worker completed, result must succeed, got %+v", r)
	}
	if len(r.Items) != 2 || r.Items[0].ItemID != "item-1" {
		t.Fatalf("result must carry the worker's real items, got %+v", r.Items)
	}
	if r.Reason != "worker real reason" {
		t.Fatalf("result must carry the worker's reason, got %q", r.Reason)
	}
	// The worker must have run with the submission-time profile restored.
	if executor.profileCount() != 1 || executor.profiles[0] != profile {
		t.Fatalf("worker must see the dispatch-time UserProfile, got %d profiles", executor.profileCount())
	}
}

// TestKernelDispatchEventDrivenTimeout verifies a task that never completes
// inside the dispatch window is reported as an explicit failure (never a fake
// success) when the event store is wired but no worker ever finishes it.
func TestKernelDispatchEventDrivenTimeout(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, _ := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	store := ares_events.NewMemoryEventStore()
	adapter := newKernelTaskDispatcher(kernel, store)
	// Fabric wired but no scheduler attached: the task is created in the
	// fabric (async) and never executed, so no completion event ever arrives.
	// With a real fabric the adapter waits and must time out into an explicit
	// failure instead of a fake success.
	adapter.fabric = taskfabric.NewFabric()
	adapter.eventTimeout = 50 * time.Millisecond

	// No fabric, no scheduler: the task is submitted through the kernel but
	// never executed, so no completion event ever arrives. Dispatch must
	// time out into an explicit failure.
	results, err := adapter.Dispatch(context.Background(), []*models.Task{
		models.NewTask("t-timeout", models.AgentType("code"), nil),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Success {
		t.Fatalf("uncompleted task must fail, got %+v", r)
	}
	if r.Error == "" {
		t.Fatalf("failed result must carry a reason, got %+v", r)
	}
}

// TestKernelDispatchEventDrivenWorkerFailure verifies a worker failure (the
// sub-agent emits EventTaskFailed with an error payload) surfaces as a failed
// TaskResult instead of a fake success.
func TestKernelDispatchEventDrivenWorkerFailure(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	handle := &kernelHandle{dual: kernel, flag: flag}
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code"), resultErr: "worker boom"}
	store := ares_events.NewMemoryEventStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adapter := newKernelTaskDispatcher(kernel, store)
	handle.taskDispatcher = adapter
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor}, store)

	results, err := adapter.Dispatch(ctx, []*models.Task{
		models.NewTask("t-fail", models.AgentType("code"), nil),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Success {
		t.Fatalf("worker failure must surface as failed result, got %+v", r)
	}
	if r.Error == "" {
		t.Fatalf("failed result must carry the worker error, got %+v", r)
	}
}

// flakyQuotaSource blocks the FIRST ActiveQuotaPolicy call until its context
// is cancelled (a policy store that hangs once), then answers subsequent calls
// normally. Used by TestRunKernelQuotaLoopSurvivesBlockedApply.
type flakyQuotaSource struct {
	mu    sync.Mutex
	calls int
}

func (s *flakyQuotaSource) ActiveQuotaPolicy(ctx context.Context) (aresrecovery.QuotaPolicy, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		<-ctx.Done() // block until the caller's timeout cancels ctx
		return aresrecovery.QuotaPolicy{}, ctx.Err()
	}
	return aresrecovery.QuotaPolicy{}, nil
}

func (s *flakyQuotaSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestRunKernelQuotaLoopSurvivesBlockedApply (C1): a quota Apply that hangs on
// the policy store must be bounded by the loop's per-apply timeout — the loop
// must keep ticking instead of spinning forever on a single blocked Apply.
func TestRunKernelQuotaLoopSurvivesBlockedApply(t *testing.T) {
	source := &flakyQuotaSource{}
	mgr := aresrecovery.NewEvolutionAwareQuotaManager(agentfabric.NewFabric(), source)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := kernelLoopConfig{
		QuotaApplyInterval: 20 * time.Millisecond,
		QuotaApplyTimeout:  50 * time.Millisecond,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runKernelQuotaLoop(ctx, mgr, cfg)
	}()

	// The first Apply blocks (50ms), then the ticker must drive a second
	// Apply — proof the loop survived the stalled call.
	deadline := time.Now().Add(2 * time.Second)
	for source.callCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := source.callCount(); got < 2 {
		t.Fatalf("quota loop must survive a blocked Apply and tick again, calls=%d", got)
	}
	cancel()
	<-done
}

// TestKernelDispatchReleasesResultSubscription (C2): after Dispatch returns,
// the waitCtx-bounded result subscription must be released. Subscribing with
// the raw parent ctx would leave every completed Dispatch's subscription — and
// its cleanup goroutine — alive until the parent context is cancelled,
// accumulating across dispatches.
func TestKernelDispatchReleasesResultSubscription(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	handle := &kernelHandle{dual: kernel, flag: flag}
	executor := &resultStubAgent{id: "code_01", typ: models.AgentType("code")}
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adapter := newKernelTaskDispatcher(kernel, store)
	handle.taskDispatcher = adapter
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor}, store)

	// The flipped scheduler subscribes to task events on ctx (long-lived);
	// wait for that baseline so the dispatch's subscription is the only
	// transient one being measured.
	deadline := time.Now().Add(2 * time.Second)
	for store.SubscriberCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	before := store.SubscriberCount()

	results, err := adapter.Dispatch(ctx, []*models.Task{
		models.NewTask("t-sub", models.AgentType("code"), nil),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("want one successful result, got %+v", results)
	}

	// The dispatch's subscription is released once Dispatch returns (its
	// waitCtx is cancelled on exit); the long-lived scheduler subscription
	// stays. Poll briefly for the async unsubscribe to settle.
	deadline = time.Now().Add(2 * time.Second)
	for store.SubscriberCount() != before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := store.SubscriberCount(); got != before {
		t.Fatalf("result subscription must be released after Dispatch, want %d subscribers, got %d", before, got)
	}
}

// TestToModelTaskPreservesMetaAcrossYieldCheckpoint verifies the v0.3.0 review
// Bug 3 fix: a yielded task's checkpoint is the meta envelope re-wrapped by the
// scheduler's quantum step, so toModelTask can still restore UserProfile/
// Payload/UsedExperienceID on resume (the old code type-asserted a plain map
// after a yield and degraded the executor to executeByType).
func TestToModelTaskPreservesMetaAcrossYieldCheckpoint(t *testing.T) {
	up := models.NewUserProfile("u1", "alice")
	up.Style = []models.StyleTag{models.StyleMinimalist}
	meta := &taskfabric.CheckpointEnvelope{
		UserProfile:      up,
		Payload:          map[string]any{"task_desc": "pick"},
		UsedExperienceID: "exp-1",
		// What RunQuantum stores after a yield: the step's durable progress.
		StepCheckpoint: map[string]any{"step": 2},
	}
	s := NewKernelScheduler(taskfabric.NewFabric(), nil, nil)

	tk := &taskfabric.Task{ID: "t1", Capability: "code", Checkpoint: meta}
	model := s.toModelTask(tk)
	if model.UserProfile == nil || model.UserProfile.UserID != "u1" {
		t.Fatalf("resume must restore UserProfile, got %+v", model.UserProfile)
	}
	if model.UsedExperienceID != "exp-1" {
		t.Fatalf("resume must restore UsedExperienceID, got %q", model.UsedExperienceID)
	}
	step, ok := model.Payload["checkpoint"].(map[string]any)
	if !ok || step["step"] != float64(2) && step["step"] != 2 {
		t.Fatalf("resume must surface the step checkpoint in payload, got %#v", model.Payload)
	}
	// The initial (pre-quantum) envelope without StepCheckpoint must also still
	// restore the profile, and must not invent a checkpoint key.
	initModel := s.toModelTask(&taskfabric.Task{ID: "t1", Capability: "code", Checkpoint: &taskfabric.CheckpointEnvelope{
		UserProfile: up, Payload: map[string]any{"task_desc": "pick"}, UsedExperienceID: "exp-1",
	}})
	if initModel.UserProfile == nil || initModel.UsedExperienceID != "exp-1" {
		t.Fatalf("initial envelope must restore meta, got profile=%+v exp=%q", initModel.UserProfile, initModel.UsedExperienceID)
	}
	if _, hasKey := initModel.Payload["checkpoint"]; hasKey {
		t.Fatal("initial envelope must not expose a checkpoint key")
	}
}

// TestRetryPolicyAllowsOneRetry verifies the v0.3.0 review Bug 2 fix:
// submitFabricTask now grants ONE real retry (MaxRetries counts total
// attempts, so 2 = first attempt + one retry). A transient failure requeues
// the task to READY; only the second failure finalizes FAILED.
func TestRetryPolicyAllowsOneRetry(t *testing.T) {
	f := taskfabric.NewFabric()
	task := models.NewTask("t-retry", models.AgentType("code"), nil)
	if err := submitFabricTask(context.Background(), f, task); err != nil {
		t.Fatalf("submitFabricTask: %v", err)
	}
	tk, err := f.Task("t-retry")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if tk.RetryPolicy.MaxRetries != 2 {
		t.Fatalf("submitFabricTask must grant 1 retry (MaxRetries=2), got %d", tk.RetryPolicy.MaxRetries)
	}
	// First execution fails → requeued to READY (the retry).
	epoch, err := f.Acquire("t-retry", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	if err := f.Start("t-retry", "agent-a", epoch); err != nil {
		t.Fatalf("start 1: %v", err)
	}
	if err := f.Fail("t-retry", "agent-a", epoch); err != nil {
		t.Fatalf("fail 1: %v", err)
	}
	tk, _ = f.Task("t-retry")
	if tk.State != taskfabric.StateReady {
		t.Fatalf("first failure must requeue (1 retry granted), got state %s", tk.State)
	}
	// Second execution fails → budget exhausted → FAILED.
	epoch, err = f.Acquire("t-retry", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	if err := f.Start("t-retry", "agent-a", epoch); err != nil {
		t.Fatalf("start 2: %v", err)
	}
	if err := f.Fail("t-retry", "agent-a", epoch); err != nil {
		t.Fatalf("fail 2: %v", err)
	}
	tk, _ = f.Task("t-retry")
	if tk.State != taskfabric.StateFailed {
		t.Fatalf("second failure must finalize FAILED, got state %s", tk.State)
	}
}

// TestSchedulerPriorityPreemption verifies the v0.3.0 review wiring fix:
// fabric.Preempt is exercised from the scheduler — a RUNNING low-priority task
// is cooperatively handed back to READY (checkpoint preserved) when a READY
// high-priority task arrives, freeing the executor for the next drain.
func TestSchedulerPriorityPreemption(t *testing.T) {
	f := taskfabric.NewFabric()
	// A low-priority task that is RUNNING (executor busy across a drain).
	if err := f.Create(&taskfabric.Task{ID: "low", Capability: "code", Priority: 1}); err != nil {
		t.Fatalf("create low: %v", err)
	}
	epoch, err := f.Acquire("low", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire low: %v", err)
	}
	if err := f.Start("low", "agent-a", epoch); err != nil {
		t.Fatalf("start low: %v", err)
	}
	if err := f.Yield("low", "agent-a", epoch, map[string]any{"step": 3}); err != nil {
		t.Fatalf("yield low (checkpoint): %v", err)
	}
	// Re-acquire to RUNNING so there is a RUNNING task to preempt.
	epoch, err = f.Acquire("low", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("re-acquire low: %v", err)
	}
	if err := f.Start("low", "agent-a", epoch); err != nil {
		t.Fatalf("re-start low: %v", err)
	}
	// A higher-priority READY task.
	if err := f.Create(&taskfabric.Task{ID: "high", Capability: "code", Priority: 10}); err != nil {
		t.Fatalf("create high: %v", err)
	}
	rt := f.RunningTasks()
	if len(rt) != 1 || rt[0].ID != "low" {
		t.Fatalf("want exactly one running task (low), got %+v", rt)
	}

	s := NewKernelScheduler(f, map[string]CapabilityExecutor{"agent-a": &stubAgent{id: "agent-a", typ: models.AgentType("code")}}, nil)
	s.preemptLowerPriority([]string{"high"})

	tk, err := f.Task("low")
	if err != nil {
		t.Fatalf("Task low: %v", err)
	}
	if tk.State != taskfabric.StateReady {
		t.Fatalf("low-priority RUNNING task must be preempted to READY, got %s", tk.State)
	}
	if tk.Checkpoint == nil {
		t.Fatal("preempted task must preserve its checkpoint")
	}
	// No-op on ties / unset priorities: a RUNNING task is never churned.
	if err := f.Create(&taskfabric.Task{ID: "tie", Capability: "code", Priority: 0}); err != nil {
		t.Fatalf("create tie: %v", err)
	}
	epoch, err = f.Acquire("tie", "agent-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire tie: %v", err)
	}
	if err := f.Start("tie", "agent-a", epoch); err != nil {
		t.Fatalf("start tie: %v", err)
	}
	s.preemptLowerPriority([]string{"tie"})
	tk, _ = f.Task("tie")
	if tk.State != taskfabric.StateRunning {
		t.Fatal("zero-priority preempt must not churn a running task")
	}
}

// TestTaskFromPayloadRestoresJSONUserProfile verifies the v0.3.0 review Bug 3
// (kernel side) fix: a user_profile that survived a JSON round-trip arrives as
// a plain map, not a *models.UserProfile. taskFromPayload must still restore it
// so the executor never degrades to executeByType.
func TestTaskFromPayloadRestoresJSONUserProfile(t *testing.T) {
	up := models.NewUserProfile("u1", "alice")
	raw, err := json.Marshal(map[string]any{"user_profile": up})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := payload["user_profile"].(map[string]any); !ok {
		t.Fatalf("test precondition: user_profile must be a plain map after round-trip, got %T", payload["user_profile"])
	}
	task, err := taskFromPayload("t-json", payload)
	if err != nil {
		t.Fatalf("taskFromPayload: %v", err)
	}
	if task.UserProfile == nil || task.UserProfile.UserID != "u1" {
		t.Fatalf("JSON round-tripped profile must be restored, got %+v", task.UserProfile)
	}
}

// checkpointStubAgent is a sub.Agent stub that records the checkpoint it
// observes on resume, so the W1 recovery E2E test can assert that a
// replacement executor sees the dead agent's preserved checkpoint.
type checkpointStubAgent struct {
	id         string
	typ        models.AgentType
	mu         sync.Mutex
	executed   []string
	checkpoint any
	observed   bool
}

var _ sub.Agent = (*checkpointStubAgent)(nil)

func (a *checkpointStubAgent) ID() string                  { return a.id }
func (a *checkpointStubAgent) Type() models.AgentType      { return a.typ }
func (a *checkpointStubAgent) Status() models.AgentStatus  { return models.AgentStatusReady }
func (a *checkpointStubAgent) Start(context.Context) error { return nil }
func (a *checkpointStubAgent) Stop(context.Context) error  { return nil }
func (a *checkpointStubAgent) Process(context.Context, any) (any, error) {
	return nil, nil
}
func (a *checkpointStubAgent) ProcessStream(context.Context, any) (<-chan base.AgentEvent, error) {
	return nil, nil
}
func (a *checkpointStubAgent) Execute(_ context.Context, task *models.Task) (*models.TaskResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.executed = append(a.executed, task.TaskID)
	if task.Payload != nil {
		if cp, ok := task.Payload["checkpoint"]; ok && cp != nil {
			a.checkpoint = cp
			a.observed = true
		}
	}
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess(nil, "recovery executor completed")
	return res, nil
}
func (a *checkpointStubAgent) ExecuteStep(ctx context.Context, task *models.Task) (*sub.StepOutcome, error) {
	res, err := a.Execute(ctx, task)
	if err != nil {
		return nil, err
	}
	return &sub.StepOutcome{Done: true, Result: res}, nil
}

func (a *checkpointStubAgent) didObserveCheckpoint() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.observed
}

func (a *checkpointStubAgent) getCheckpoint() any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checkpoint
}

// TestW1RecoveryClosureE2E verifies the W1 production-grade recovery闭环:
//
//	Task → executor A executes quantum#1 (writes checkpoint) → A crashes
//	(lease expiry) → recovery loop → replacement executor A' registered →
//	scheduler schedules A' → A' observes A's checkpoint → COMPLETE.
//
// The test proves:
//  1. The replacement executor is a real registered executor (not phantom).
//  2. The replacement observes the dead agent's preserved checkpoint.
//  3. The task completes via the new executor.
//  4. RequeueExpiredLeases → bound replacement registration have a caller
//     through the real recovery loop.
func TestW1RecoveryClosureE2E(t *testing.T) {
	// Build a fabric with one task.
	f := taskfabric.NewFabric()
	if err := f.Create(&taskfabric.Task{
		ID:          "w1-task",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
		Checkpoint: &taskfabric.CheckpointEnvelope{
			StepCheckpoint: map[string]any{"step": 1, "data": "quantum-1-output"},
		},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Acquire and yield to simulate quantum#1 execution with a checkpoint.
	epoch, err := f.Acquire("w1-task", "agent-A", time.Minute)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := f.Start("w1-task", "agent-A", epoch); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := f.Yield("w1-task", "agent-A", epoch, &taskfabric.CheckpointEnvelope{
		StepCheckpoint: map[string]any{"step": 1, "data": "quantum-1-output"},
	}); err != nil {
		t.Fatalf("yield: %v", err)
	}

	// Verify the task is SUSPENDED with the checkpoint preserved.
	tk, _ := f.Task("w1-task")
	if tk.State != taskfabric.StateSuspended {
		t.Fatalf("task must be SUSPENDED after yield, got %s", tk.State)
	}
	if tk.Checkpoint == nil {
		t.Fatal("task must have a preserved checkpoint after yield")
	}

	// Expire the lease to simulate agent A's death.
	f.WithClock(func() time.Time { return time.Now().Add(10 * time.Minute) })

	// Build the recovery subsystem and scheduler.
	agents := agentfabric.NewFabric()
	recovery := aresrecovery.New(f, agents, aresrecovery.DefaultRestartPolicy())

	// The replacement executor that will be created by the factory.
	replacement := &checkpointStubAgent{id: "replacement-A", typ: models.AgentType("code")}

	// Build the scheduler with NO initial executors (simulating A's death —
	// the only executor is gone). The recovery loop will register the
	// replacement dynamically.
	sched := NewKernelScheduler(f, map[string]CapabilityExecutor{}, nil)
	sched.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register the replacement via the recovery loop's factory. The
	// registerFn binds the executor to the specific recovered task.
	registerFn := func(taskID, agentID string, executor CapabilityExecutor) {
		sched.RegisterExecutorForTask(taskID, agentID, executor)
	}
	// Override the replacement to use our checkpoint-recording stub.
	factoryFn := func(agentID, capability string) CapabilityExecutor {
		replacement.id = agentID
		return replacement
	}
	// No registered executor can resume the task (the scheduler starts empty),
	// so the recovery loop must spawn a replacement.
	hasCapable := func(taskID string) bool { return sched.HasCapableExecutor(taskID) }

	// Start the recovery loop with the W1 full recovery chain.
	go runKernelRecoveryLoop(ctx, nil, recovery, kernelLoopConfig{
		RecoverySweepInterval: 50 * time.Millisecond,
		RecoverySweepTimeout:  5 * time.Second,
	}, registerFn, factoryFn, hasCapable)

	// Start the scheduler.
	go sched.Run(ctx)

	// Wait for the task to complete via the replacement executor.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tk, err := f.Task("w1-task")
		if err == nil && tk.State == taskfabric.StateCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Assert: task completed.
	tk, err = f.Task("w1-task")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if tk.State != taskfabric.StateCompleted {
		t.Fatalf("task must be COMPLETED by replacement executor, got state %s", tk.State)
	}

	// Assert: the replacement executor observed the checkpoint from quantum#1.
	if !replacement.didObserveCheckpoint() {
		t.Fatal("replacement executor must observe the dead agent's checkpoint")
	}
	// The checkpoint rides inside a *taskfabric.CheckpointEnvelope. After the
	// fabric's yield→requeue→toModelTask path it arrives as a map[string]any
	// (the scheduler's toModelTask unwraps the envelope and places the
	// StepCheckpoint into payload["checkpoint"]) — the step data must be
	// preserved.
	cp := replacement.getCheckpoint()
	v, ok := cp.(map[string]any)
	if !ok {
		t.Fatalf("checkpoint must be map[string]any, got %T", cp)
	}
	if v["data"] != "quantum-1-output" {
		t.Fatalf("checkpoint data must be 'quantum-1-output', got %v", v["data"])
	}
}

// TestW1RegisterExecutorDynamic verifies the scheduler's dynamic executor
// registration: a task that was unschedulable (no capable candidate) becomes
// schedulable after RegisterExecutor injects a matching executor.
func TestW1RegisterExecutorDynamic(t *testing.T) {
	f := taskfabric.NewFabric()
	if err := f.Create(&taskfabric.Task{
		ID:          "reg-task",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Scheduler with no executors — task is unschedulable.
	sched := NewKernelScheduler(f, map[string]CapabilityExecutor{}, nil)
	sched.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	// Wait a bit to confirm the task stays READY (no executor).
	time.Sleep(100 * time.Millisecond)
	tk, _ := f.Task("reg-task")
	if tk.State != taskfabric.StateReady {
		t.Fatalf("task must stay READY with no executor, got %s", tk.State)
	}

	// Dynamically register an executor.
	executor := &stubAgent{id: "dynamic-1", typ: models.AgentType("code")}
	sched.RegisterExecutor("dynamic-1", executor)

	// Wait for the task to be scheduled and completed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tk, err := f.Task("reg-task")
		if err == nil && tk.State == taskfabric.StateCompleted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	tk, _ = f.Task("reg-task")
	if tk.State != taskfabric.StateCompleted {
		t.Fatalf("task must be COMPLETED after dynamic executor registration, got %s", tk.State)
	}
}

// TestW1UnregisterExecutor verifies that unregistering an executor removes it
// from the scheduling candidate pool.
func TestW1UnregisterExecutor(t *testing.T) {
	f := taskfabric.NewFabric()
	executor := &stubAgent{id: "removable", typ: models.AgentType("code")}
	sched := NewKernelScheduler(f, map[string]CapabilityExecutor{"removable": executor}, nil)

	// Verify it's registered.
	if count := sched.executorCount(); count != 1 {
		t.Fatalf("expected 1 executor, got %d", count)
	}

	// Unregister.
	sched.UnregisterExecutor("removable")
	if count := sched.executorCount(); count != 0 {
		t.Fatalf("expected 0 executors after unregister, got %d", count)
	}

	// Lookup must return false.
	if _, ok := sched.lookupExecutor("removable"); ok {
		t.Fatal("lookup must return false after unregister")
	}
}
