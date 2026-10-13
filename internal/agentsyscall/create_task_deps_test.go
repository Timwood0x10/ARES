package agentsyscall

import (
	"context"
	"errors"
	"testing"
	"time"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestCreateTaskRejectsDanglingDependency is the P0-2 (T1) regression: an LLM
// create_task naming a dependency that does not exist used to be accepted
// verbatim. The fabric's gate treats a missing dependency as unsatisfied
// forever, so the task — and every task chained behind it — waited in
// READY-forever limbo with nothing reported anywhere. The tool call must now
// fail, so the model can re-plan against ids it actually created.
func TestCreateTaskRejectsDanglingDependency(t *testing.T) {
	fab := taskfabric.NewFabric()
	kernel := NewKernel(nil, fab, nil, nil)

	_, err := kernel.CreateTask(context.Background(), CreateTaskArgs{
		Capability:   "ares/answer",
		Dependencies: []string{"task-ares/plan-999"},
	})
	if !errors.Is(err, taskfabric.ErrDependencyUnresolved) {
		t.Fatalf("dangling dependency must be refused with ErrDependencyUnresolved, got: %v", err)
	}
	// The refusal must leave no trace: a registered task with a dead edge would
	// be picked up by the scheduler and never run.
	if ids := fab.IDs(); len(ids) != 0 {
		t.Fatalf("a refused create_task must leave the fabric empty, got %v", ids)
	}
}

// TestBindToolsCreateTaskSurfacesUnresolvedDependencies pins the refusal at the
// boundary the MODEL actually sees, which is where the T1 fix earns its keep: a
// dangling dependency must come back as a readable result, NOT as a tool error.
// A tool error fails the quantum (toolCognition), and because L2-projected tasks
// are not AllowPartial the fabric then cascades FAILED through the downstream
// subgraph — so an error here would cost the whole session branch instead of
// just the one call.
func TestBindToolsCreateTaskSurfacesUnresolvedDependencies(t *testing.T) {
	fab := taskfabric.NewFabric()
	kernel := NewKernel(nil, fab, nil, nil)
	binder := &stubBinder{}
	BindTools(binder, kernel)
	ctx := context.Background()

	res, err := binder.call(ctx, CreateTaskTool, map[string]any{
		"capability":   "ares/answer",
		"dependencies": []any{"task-ares/plan-999", "ghost"},
	})
	if err != nil {
		t.Fatalf("an unresolvable dependency must be a result, not a tool error: %v", err)
	}
	tr, ok := res.(*CreateTaskResult)
	if !ok {
		t.Fatalf("result type = %T, want *CreateTaskResult", res)
	}
	if tr.Rejected != CreateTaskRejectedUnresolvedDeps {
		t.Fatalf("rejected = %q, want %q", tr.Rejected, CreateTaskRejectedUnresolvedDeps)
	}
	if len(tr.MissingDependencies) != 2 {
		t.Fatalf("missing = %v, want both offending ids", tr.MissingDependencies)
	}
	if tr.TaskID != "" {
		t.Fatalf("a refused create_task must not report a task id, got %q", tr.TaskID)
	}
	if ids := fab.IDs(); len(ids) != 0 {
		t.Fatalf("a refused create_task must leave the fabric empty, got %v", ids)
	}

	// The Kernel-level contract is unchanged: the syscall still reports the
	// refusal as an error, so a non-model caller cannot mistake it for a create.
	_, kerr := kernel.CreateTask(ctx, CreateTaskArgs{
		Capability:   "ares/answer",
		Dependencies: []string{"ghost"},
	})
	if !errors.Is(kerr, taskfabric.ErrDependencyUnresolved) {
		t.Fatalf("Kernel.CreateTask must still report the refusal as an error, got: %v", kerr)
	}

	// And the same binding still creates a real task when the request is sound.
	okRes, err := binder.call(ctx, CreateTaskTool, map[string]any{"capability": "tool/grep"})
	if err != nil {
		t.Fatalf("call create_task (valid): %v", err)
	}
	created, ok := okRes.(*CreateTaskResult)
	if !ok || created.TaskID == "" || created.Rejected != "" {
		t.Fatalf("a valid create_task must return a created task, got %+v", okRes)
	}
}

// TestCreateTaskAcceptsChainedDependencies is the counter-case the fix must not
// break: decomposing into a real DAG — create A, then create B depending on A —
// is what create_task is for. B must still be gated on A and unblock exactly
// when A completes.
func TestCreateTaskAcceptsChainedDependencies(t *testing.T) {
	fab := taskfabric.NewFabric()
	kernel := NewKernel(nil, fab, nil, nil)
	ctx := context.Background()

	first, err := kernel.CreateTask(ctx, CreateTaskArgs{Capability: "tool/grep"})
	if err != nil {
		t.Fatalf("CreateTask (predecessor): %v", err)
	}
	second, err := kernel.CreateTask(ctx, CreateTaskArgs{
		Capability:   "ares/answer",
		Dependencies: []string{first.TaskID},
	})
	if err != nil {
		t.Fatalf("CreateTask (dependent): %v", err)
	}

	ready, err := fab.IsReady(second.TaskID)
	if err != nil {
		t.Fatalf("IsReady (before predecessor completes): %v", err)
	}
	if ready {
		t.Fatal("the dependent must not be ready while its predecessor is unfinished")
	}

	epoch, err := fab.Acquire(first.TaskID, "agent-1", time.Minute)
	if err != nil {
		t.Fatalf("Acquire (predecessor): %v", err)
	}
	if err := fab.Start(first.TaskID, "agent-1", epoch); err != nil {
		t.Fatalf("Start (predecessor): %v", err)
	}
	if err := fab.Complete(first.TaskID, "agent-1", epoch); err != nil {
		t.Fatalf("Complete (predecessor): %v", err)
	}

	ready, err = fab.IsReady(second.TaskID)
	if err != nil {
		t.Fatalf("IsReady (after predecessor completes): %v", err)
	}
	if !ready {
		t.Fatal("the dependent must be ready once its predecessor completes")
	}
}
