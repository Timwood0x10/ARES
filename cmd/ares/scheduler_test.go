package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// stubAgent is a minimal sub.Agent that records executed tasks and reports a
// configurable result.
type stubAgent struct {
	id        string
	typ       models.AgentType
	executed  []string
	resultErr string
	mu        sync.Mutex
}

var _ sub.Agent = (*stubAgent)(nil)

func (a *stubAgent) ID() string                  { return a.id }
func (a *stubAgent) Type() models.AgentType      { return a.typ }
func (a *stubAgent) Status() models.AgentStatus  { return models.AgentStatusReady }
func (a *stubAgent) Start(context.Context) error { return nil }
func (a *stubAgent) Stop(context.Context) error  { return nil }
func (a *stubAgent) Process(context.Context, any) (any, error) {
	return nil, nil
}
func (a *stubAgent) ProcessStream(context.Context, any) (<-chan base.AgentEvent, error) {
	return nil, nil
}
func (a *stubAgent) Execute(_ context.Context, task *models.Task) (*models.TaskResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.executed = append(a.executed, task.TaskID)
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	if a.resultErr != "" {
		res.SetError(a.resultErr)
	}
	return res, nil
}
func (a *stubAgent) StartEventListener(context.Context) error { return nil }
func (a *stubAgent) executedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.executed)
}

// TestKernelSchedulerRunsFullFabricPath verifies the no-leader loop end to end:
// a task created in the fabric is drained by the scheduler, scheduled to a
// capable agent, executed via sub.Agent.Execute, and finalized COMPLETED.
func TestKernelSchedulerRunsFullFabricPath(t *testing.T) {
	f := taskfabric.NewFabric()
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code")}
	sched := NewKernelScheduler(f, map[string]sub.Agent{"code_01": executor})
	sched.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	// A task requiring the "code" capability (declared by the executor).
	if err := f.Create(&taskfabric.Task{
		ID:          "t1",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Wait for the task to reach COMPLETED.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tk, err := f.Task("t1")
		if err == nil && tk.State == taskfabric.StateCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, err := f.Task("t1")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if tk.State != taskfabric.StateCompleted {
		t.Fatalf("want COMPLETED, got %s", tk.State)
	}
	if executor.executedCount() != 1 {
		t.Fatalf("executor must run the task once, got %d", executor.executedCount())
	}
}

// TestKernelSchedulerNoCapableCandidate verifies a task whose required
// capability no executor declares is not executed (the scheduler cannot
// schedule it, so it skips without a spurious failure).
func TestKernelSchedulerNoCapableCandidate(t *testing.T) {
	f := taskfabric.NewFabric()
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code")}
	sched := NewKernelScheduler(f, map[string]sub.Agent{"code_01": executor})
	sched.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	if err := f.Create(&taskfabric.Task{
		ID:          "t-rust",
		Capability:  "rust",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Let the scheduler poll a few times; it must never execute the task.
	time.Sleep(150 * time.Millisecond)
	if executor.executedCount() != 0 {
		t.Fatalf("executor must not run an unschedulable task, got %d", executor.executedCount())
	}
	tk, err := f.Task("t-rust")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if tk.State != taskfabric.StateReady {
		t.Fatalf("unschedulable task stays READY, got %s", tk.State)
	}
}

// TestKernelSchedulerExecutionFailureRequeues verifies the fabric RetryPolicy
// drives requeueing: a failing executor requeues the task to READY, and a
// second drain retries it before exhausting the budget.
func TestKernelSchedulerExecutionFailureRequeues(t *testing.T) {
	f := taskfabric.NewFabric()
	executor := &stubAgent{id: "code_01", typ: models.AgentType("code"), resultErr: "boom"}
	sched := NewKernelScheduler(f, map[string]sub.Agent{"code_01": executor})
	sched.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	if err := f.Create(&taskfabric.Task{
		ID:          "t-fail",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 3},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The executor always errors; the fabric must requeue (READY again) until
	// the retry budget is exhausted, then finalize FAILED.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tk, err := f.Task("t-fail")
		if err == nil && (tk.State == taskfabric.StateFailed || tk.State == taskfabric.StateCompleted) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, err := f.Task("t-fail")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if tk.State != taskfabric.StateFailed {
		t.Fatalf("want FAILED after retries exhausted, got %s", tk.State)
	}
	if executor.executedCount() == 0 {
		t.Fatal("executor must have been retried at least once")
	}
}
