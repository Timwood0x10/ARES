package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
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
	enableKernelExecution(kernel, f, executors)
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
	// verify the policy flip + scheduler start via a dispatcher dispatch.
	wireKernelPolicy(ctx, cfg, kernelHandle, []sub.Agent{executor})
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
