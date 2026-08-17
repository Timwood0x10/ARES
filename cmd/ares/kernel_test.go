package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/agentipc"
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
// task through the kernel and returns the leader-shaped results.
func TestKernelTaskDispatcherBatchAdapter(t *testing.T) {
	inner := &stubLeaderDispatcher{}
	kernel, flag := wireKernelDispatcher(inner, []subAgentCapability{
		{ID: "code_01", Type: "code"},
	})
	adapter := &kernelTaskDispatcher{kernel: kernel}

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
	for _, r := range results {
		if !r.Success {
			t.Fatalf("result must succeed, got %+v", r)
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
// wiring in the kernel path (ares-runtime.md §9): a task whose dependencies
// are not all COMPLETED is created but NOT executed by executeFabricTask; it
// only becomes executable once its dependencies complete.
func TestKernelDAGGateDefersDependentTask(t *testing.T) {
	f := taskfabric.NewFabric()
	research := &stubAgent{id: "research_01", typ: models.AgentType("research")}
	writer := &stubAgent{id: "writer_01", typ: models.AgentType("write")}
	executors := map[string]sub.Agent{"research_01": research, "writer_01": writer}
	ctx := context.Background()

	// Task B depends on task A; A is not created yet → B must not run.
	b := models.NewTask("task_b", models.AgentType("write"), nil)
	b.Context.Dependencies = []string{"task_a"}
	if err := executeFabricTask(ctx, f, executors, newLoadTracker(), b); err != nil {
		t.Fatalf("executeFabricTask(B): %v", err)
	}
	if writer.executedCount() != 0 {
		t.Fatal("B must not execute before its dependency A completes")
	}
	if got := f.ReadyTasks(); len(got) != 0 {
		t.Fatalf("no task must be ready before A completes, got %v", got)
	}

	// Run A through the fabric path; it completes and unlocks B.
	a := models.NewTask("task_a", models.AgentType("research"), nil)
	if err := executeFabricTask(ctx, f, executors, newLoadTracker(), a); err != nil {
		t.Fatalf("executeFabricTask(A): %v", err)
	}
	if research.executedCount() != 1 {
		t.Fatalf("A must execute once, got %d", research.executedCount())
	}

	// Now B is ready: ReadyTasks surfaces it and executing it completes.
	ready := f.ReadyTasks()
	if !slicesEqual(ready, []string{"task_b"}) {
		t.Fatalf("B must be ready after A completes, got %v", ready)
	}
	if err := executeFabricTask(ctx, f, executors, newLoadTracker(), b); err != nil {
		t.Fatalf("executeFabricTask(B) after A ready: %v", err)
	}
	if writer.executedCount() != 1 {
		t.Fatalf("B must execute exactly once, got %d", writer.executedCount())
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
	executors := map[string]sub.Agent{"code_01": executor}

	// Flip: replace the shadow scorer with the real executor, disable shadow.
	enableKernelExecution(kernel, f, executors, newLoadTracker())
	flag.Set(agentipc.PolicyTaskFabric)

	// Dispatch through the kernel (active path = fabric).
	payload := map[string]any{"agent_type": "code"}
	if err := kernel.Dispatch(context.Background(), "", "t1", payload); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// The task must have been created and executed in the fabric.
	tk, err := f.Task("t1")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if tk.State != taskfabric.StateCompleted {
		t.Fatalf("want COMPLETED, got %s", tk.State)
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

	// The kernel's new path is now the real executor: dispatch through the
	// batch adapter to run the full fabric path.
	adapter := &kernelTaskDispatcher{kernel: kernel}
	task := models.NewTask("t-policy", models.AgentType("code"), nil)
	results, err := adapter.Dispatch(ctx, []*models.Task{task})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if executor.executedCount() == 0 {
		t.Fatal("executor must have run via the fabric path")
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
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor})
	if !flag.IsTaskFabric() {
		t.Fatal("flag must be TaskFabric after live flip")
	}

	// After the flip: new dispatches run through the fabric path. The legacy
	// dispatcher must NOT be called again (shadow off, no double execution).
	if _, err := adapter.Dispatch(ctx, []*models.Task{
		models.NewTask("t-fabric", models.AgentType("code"), nil),
	}); err != nil {
		t.Fatalf("fabric dispatch: %v", err)
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

	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor})
	if !flag.IsTaskFabric() {
		t.Fatal("flag must be TaskFabric after first flip")
	}

	// Second flip: no-op (kernel.flipped guard). Executing a task afterwards
	// must run it exactly once — a second scheduler would double-execute.
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor})
	adapter := &kernelTaskDispatcher{kernel: kernel}
	if _, err := adapter.Dispatch(ctx, []*models.Task{
		models.NewTask("t1", models.AgentType("code"), nil),
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
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
	flipKernelToTaskFabric(ctx, handle, []sub.Agent{executor})
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
// EventTaskExpired event triggers RequeueExpiredLeases (the first recovery
// path), so an expired task returns to READY.
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
	go runKernelRecoveryLoop(ctx, store, recovery)

	// Publish the TaskExpired event (as CheckExpiredLeases would) and wait for
	// the recovery sweep to requeue the task to READY.
	if err := store.Append(ctx, "t1", []*ares_events.Event{
		{Type: ares_events.EventTaskExpired, StreamID: "t1", Payload: map[string]any{}},
	}, 0); err != nil {
		t.Fatalf("append expired event: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tk, err := tf.Task("t1")
		if err == nil && tk.State == taskfabric.StateReady {
			return // recovered: lease expired → task requeued to READY
		}
		time.Sleep(20 * time.Millisecond)
	}
	tk, err := tf.Task("t1")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	t.Fatalf("task must be requeued to READY after TaskExpired event, state=%s", tk.State)
}
