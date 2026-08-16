package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/core/models"
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
