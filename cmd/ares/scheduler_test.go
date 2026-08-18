package main

import (
	"context"
	"fmt"
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
	sched := NewKernelScheduler(f, map[string]sub.Agent{"code_01": executor}, nil)
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
	sched := NewKernelScheduler(f, map[string]sub.Agent{"code_01": executor}, nil)
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
	sched := NewKernelScheduler(f, map[string]sub.Agent{"code_01": executor}, nil)
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

// blockingAgent is a stub sub.Agent whose Execute blocks for execDelay so the
// test can observe true concurrency: N tasks with a per-task delay complete in
// ~delay when drained in parallel, but ~N*delay when drained serially.
//
// Each agent is single-slot (one task at a time), so per-agent concurrency is
// always 1; true concurrency is observed through the SHARED meter: the peak
// number of agents executing simultaneously across the whole fleet. When the
// scheduler drains N ready tasks concurrently (work stealing), N agents are
// active at once (meter peak ≈ N); when it drains serially, the peak is 1.
type blockingAgent struct {
	id        string
	typ       models.AgentType
	execDelay time.Duration
	meter     *concurrencyMeter
	mu        sync.Mutex
	executed  []string
}

var _ sub.Agent = (*blockingAgent)(nil)

// concurrencyMeter tracks the peak number of concurrent executions across all
// agents sharing it (the fleet-wide work-stealing concurrency).
type concurrencyMeter struct {
	mu     sync.Mutex
	active int
	peak   int
}

func (m *concurrencyMeter) begin() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active++
	if m.active > m.peak {
		m.peak = m.active
	}
}

func (m *concurrencyMeter) end() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active--
}

func (m *concurrencyMeter) Peak() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peak
}

func (a *blockingAgent) ID() string                  { return a.id }
func (a *blockingAgent) Type() models.AgentType      { return a.typ }
func (a *blockingAgent) Status() models.AgentStatus  { return models.AgentStatusReady }
func (a *blockingAgent) Start(context.Context) error { return nil }
func (a *blockingAgent) Stop(context.Context) error  { return nil }
func (a *blockingAgent) Process(context.Context, any) (any, error) {
	return nil, nil
}
func (a *blockingAgent) ProcessStream(context.Context, any) (<-chan base.AgentEvent, error) {
	return nil, nil
}
func (a *blockingAgent) Execute(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
	a.mu.Lock()
	a.executed = append(a.executed, task.TaskID)
	a.mu.Unlock()
	a.meter.begin()
	defer a.meter.end()

	select {
	case <-time.After(a.execDelay):
	case <-ctx.Done():
	}
	return models.NewTaskResult(task.TaskID, task.AgentType), nil
}

func (a *blockingAgent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.executed)
}

// TestKernelSchedulerConcurrentDrainWorkStealing verifies the scheduler drains
// multiple ready tasks CONCURRENTLY across agents (work stealing): with 3
// single-slot agents and 3 ready tasks, the fleet-wide concurrency peak must
// reach 3 (all agents busy at once) and every agent must execute its own task.
// Serial draining would yield a fleet peak of 1 and take ~3× the delay.
func TestKernelSchedulerConcurrentDrainWorkStealing(t *testing.T) {
	const numAgents = 3
	const perTaskDelay = 200 * time.Millisecond

	f := taskfabric.NewFabric()
	executors := make(map[string]sub.Agent, numAgents)
	agents := make([]*blockingAgent, 0, numAgents)
	meter := &concurrencyMeter{}
	for i := 0; i < numAgents; i++ {
		cap := fmt.Sprintf("cap-%d", i)
		ag := &blockingAgent{id: fmt.Sprintf("agent_%d", i), typ: models.AgentType(cap), execDelay: perTaskDelay, meter: meter}
		agents = append(agents, ag)
		executors[ag.id] = ag
	}
	sched := NewKernelScheduler(f, executors, nil)
	sched.pollInterval = 10 * time.Millisecond
	sched.WithMaxConcurrent(numAgents)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	go sched.Run(ctx)

	// One task per agent capability — all ready at once.
	for i := 0; i < numAgents; i++ {
		if err := f.Create(&taskfabric.Task{
			ID:          fmt.Sprintf("t-%d", i),
			Capability:  fmt.Sprintf("cap-%d", i),
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("Create t-%d: %v", i, err)
		}
	}

	// All tasks must complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allDone := true
		for i := 0; i < numAgents; i++ {
			tk, err := f.Task(fmt.Sprintf("t-%d", i))
			if err != nil || tk.State != taskfabric.StateCompleted {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	elapsed := time.Since(start)
	for i := 0; i < numAgents; i++ {
		tk, err := f.Task(fmt.Sprintf("t-%d", i))
		if err != nil || tk.State != taskfabric.StateCompleted {
			t.Fatalf("task t-%d must complete, got %+v err=%v", i, tk, err)
		}
	}

	// Concurrent execution: N tasks with a per-task delay must finish well
	// under N*delay (serial would take ~600ms; parallel ~200ms + drain jitter).
	if elapsed >= numAgents*perTaskDelay {
		t.Fatalf("tasks must drain in parallel: elapsed=%v, serial would be ~%v",
			elapsed.Round(time.Millisecond), (numAgents * perTaskDelay).Round(time.Millisecond))
	}
	// Work stealing: the fleet concurrency peak must reach N (all agents busy
	// at once). Serial draining would leave the peak at 1.
	if peak := meter.Peak(); peak < numAgents {
		t.Fatalf("work stealing must run all %d agents concurrently, fleet peak = %d", numAgents, peak)
	}
	// Every agent picked up its own task (work distributed, not serialized).
	for _, ag := range agents {
		if got := ag.count(); got != 1 {
			t.Fatalf("agent %s must execute its task once, got %d", ag.id, got)
		}
	}
}
