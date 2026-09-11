package kernel

// §3.1 (MEDIUM) regressions for the scheduler:
// budget-exhaustion livelock, neutral outcome attribution, Scheduled task
// counting, recovery-binding capability mismatch, quantum shutdown boundary,
// and the drain semaphore's ctx-aware send.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/task"
)

// ── §3.1 #1: budget-exhausted sole candidate must not churn leases ──

// TestBudgetExhaustedSoleCandidateDoesNotChurnLeases pins the pre-schedule
// budget filter: when every candidate's budget is exhausted, the scheduler
// must leave the task in the throttled "no capable candidate" wait state —
// NOT acquire a lease and immediately release it on every drain. The
// acquire/release cycle appended two durable events per poll forever and
// kept the task bouncing LEASED→READY with zero progress.
func TestBudgetExhaustedSoleCandidateDoesNotChurnLeases(t *testing.T) {
	ctx := context.Background()

	agents := agentfabric.NewFabric()
	// A governed agent whose single tool round is already spent.
	spec := agentfabric.SpawnSpec{
		Identity:     "spent",
		Capabilities: []string{"code"},
		CognitionFactory: func([]string) agentfabric.Cognition {
			return &countingCognition{}
		},
		Governance: agentfabric.Governance{ToolBudget: 1},
	}
	a, err := agents.Spawn(ctx, spec)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := agents.ConsumeResource(a.Identity, 0, 1); err != nil {
		t.Fatalf("consume budget: %v", err)
	}

	fabric := taskfabric.NewFabric()
	tracker := NewLoadTracker()
	sched := New(fabric, map[string]CapabilityExecutor{}, tracker)
	sched.WithAgentFabric(agents)
	sched.WithGovernance(agents)

	if err := fabric.Create(&taskfabric.Task{
		ID:          "t-budget",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Budget is exhausted: the sole candidate must be filtered BEFORE
	// Schedule, so the execute call fails with the (throttled) no-capable-
	// candidate sentinel and the fabric records no lease churn.
	err = sched.execute(ctx, "t-budget")
	if err == nil {
		t.Fatal("execute with an exhausted budget must report no capable candidate, got nil")
	}
	if !errors.Is(err, taskfabric.ErrNoCapableCandidate) {
		t.Fatalf("execute error = %v, want ErrNoCapableCandidate", err)
	}
	for _, ev := range fabric.Events() {
		if ev.Type == taskfabric.EventTaskAcquired || ev.Type == taskfabric.EventTaskReleased {
			t.Fatalf("budget-exhausted task must not acquire/release a lease, saw %s", ev.Type)
		}
	}

	// After a budget reset the same task must become schedulable again —
	// the filter is a wait state, not a black hole.
	if err := agents.ResetResource(a.Identity); err != nil {
		t.Fatalf("reset budget: %v", err)
	}
	runCtx, runCancel := context.WithCancel(ctx)
	go sched.Run(runCtx)
	defer runCancel()
	waitForTaskState(t, fabric, "t-budget", taskfabric.StateCompleted, 5*time.Second)
}

// ── §3.1 #2 + #3: non-executor failures end neutral ──

// TestEndQuantumOutcomeNeutralForNonExecutorConditions pins the neutral
// attribution set: fabric start-stage sentinels (the task was concurrently
// finalized or vanished — the executor never ran) and scheduler-shutdown
// cancellation must not be recorded as the agent's failure. Pre-fix, a
// graceful shutdown mid-quantum dropped every in-flight agent's confidence
// toward 0.
func TestEndQuantumOutcomeNeutralForNonExecutorConditions(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"illegal_state", taskfabric.ErrIllegalState},
		{"task_not_found", taskfabric.ErrTaskNotFound},
		{"context_canceled", context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fabric := taskfabric.NewFabric()
			tracker := NewLoadTracker()
			sched := New(fabric, map[string]CapabilityExecutor{}, tracker)
			attribution := aresrecovery.NewExecutionAttribution()
			sched.WithAttribution(attribution)

			// Give the agent real history so a neutral outcome is
			// observable as "confidence unchanged" rather than as the
			// untouched neutral prior.
			tracker.Begin("coder")
			tracker.End("coder", true)

			sched.endQuantumOutcome("coder", "code", "t-x", tc.err, time.Millisecond, 0)

			if got := tracker.Confidence("coder"); got != 1.0 {
				t.Fatalf("confidence must stay 1.0 (neutral outcome), got %v", got)
			}
			if got := tracker.Load("coder"); got != 0 {
				t.Fatalf("busy slot must still be released, load=%v", got)
			}
			for _, cr := range attribution.Snapshot().PerCapability {
				if cr.AgentID == "coder" {
					t.Fatal("a non-executor condition must not be attributed to the agent")
				}
			}
		})
	}
}

// ── §3.1 #4: Scheduled counts tasks, not quanta ──

// yieldingExecutor yields on its first step (Done=false) and completes on
// the second — one task, two quanta.
type yieldingExecutor struct {
	id    string
	typ   models.AgentType
	calls int
	mu    sync.Mutex
}

func (e *yieldingExecutor) ID() string { return e.id }
func (e *yieldingExecutor) Type() models.AgentType {
	return e.typ
}
func (e *yieldingExecutor) ExecuteStep(_ context.Context, task *models.Task) (*sub.StepOutcome, error) {
	e.mu.Lock()
	e.calls++
	first := e.calls == 1
	e.mu.Unlock()
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	if first {
		res.SetSuccess(nil, "yielding for more work")
		return &sub.StepOutcome{Done: false, Result: res}, nil
	}
	res.SetSuccess(nil, "done")
	return &sub.StepOutcome{Done: true, Result: res}, nil
}

// TestScheduledCountsTasksNotQuanta pins the observability contract: a task
// that spans multiple quanta (yield → resume → done) increments Scheduled
// exactly once. Pre-fix every successful quantum incremented the counter.
func TestScheduledCountsTasksNotQuanta(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	exec := &yieldingExecutor{id: "coder", typ: models.AgentType("code")}
	sched := New(fabric, map[string]CapabilityExecutor{"coder": exec}, nil)
	sched.PollInterval = 10 * time.Millisecond
	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{
		ID:          "t-multi",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	waitForTaskState(t, fabric, "t-multi", taskfabric.StateCompleted, 5*time.Second)

	if got := sched.Scheduled.Load(); got != 1 {
		t.Fatalf("Scheduled must count the completed TASK once, got %d", got)
	}
	if exec.calls != 2 {
		t.Fatalf("expected exactly 2 quanta (yield + done), got %d", exec.calls)
	}
}

// ── §3.1 #5: recovery binding with a non-overlapping capability ──

// TestBoundExecutorCapabilityMismatchFallsThrough pins the stranding fix: a
// recovery executor bound to a task whose capability it cannot run must not
// monopolize the candidate list — the task falls through to the general
// pool so a capable executor can pick it up. Pre-fix the task waited
// forever on a binding only released at terminal state.
func TestBoundExecutorCapabilityMismatchFallsThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	capable := &smokeExecutor{id: "coder", typ: models.AgentType("code")}
	sched := New(fabric, map[string]CapabilityExecutor{"coder": capable}, nil)
	// The replacement was spawned for the WRONG capability: it is bound to
	// the task but cannot execute it.
	sched.RegisterExecutorForTask("t-strand", "replacement",
		&smokeExecutor{id: "replacement", typ: models.AgentType("trading")})

	sched.PollInterval = 10 * time.Millisecond
	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{
		ID:          "t-strand",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	waitForTaskState(t, fabric, "t-strand", taskfabric.StateCompleted, 5*time.Second)
	if capable.executed != 1 {
		t.Fatalf("the capable executor must have run the task, got %d calls", capable.executed)
	}
}

// ── §3.1 #6: a stuck executor must not block shutdown ──

// blockingExecutor parks every step until release is closed.
type blockingExecutor struct {
	id      string
	typ     models.AgentType
	enter   chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *blockingExecutor) ID() string { return e.id }
func (e *blockingExecutor) Type() models.AgentType {
	return e.typ
}
func (e *blockingExecutor) ExecuteStep(_ context.Context, _ *models.Task) (*sub.StepOutcome, error) {
	e.once.Do(func() { close(e.enter) })
	<-e.release
	return nil, fmt.Errorf("unblocked after cancellation")
}

// TestStuckExecutorDoesNotBlockShutdown pins the quantum's cancellation
// boundary: when the scheduler context is cancelled while an executor step
// is stuck, the quantum must abort (retry-policy Fail) so the drain loop and
// Run can return. Pre-fix the drain blocked on wg.Wait forever.
func TestStuckExecutorDoesNotBlockShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	fabric := taskfabric.NewFabric()
	exec := &blockingExecutor{
		id:      "stuck",
		typ:     models.AgentType("code"),
		enter:   make(chan struct{}),
		release: make(chan struct{}),
	}
	sched := New(fabric, map[string]CapabilityExecutor{"stuck": exec}, nil)
	sched.PollInterval = 10 * time.Millisecond
	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{
		ID:          "t-stuck",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	select {
	case <-exec.enter:
	case <-time.After(5 * time.Second):
		t.Fatal("executor step never started")
	}

	// Graceful shutdown while the quantum is parked inside the executor.
	cancel()

	// Run must observe the cancellation, abort the quantum at its boundary,
	// and return — observable as the readiness flag going down.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !sched.Running() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sched.Running() {
		t.Fatal("shutdown is blocked by a stuck executor: Run never returned")
	}
	// Cleanup: let the abandoned step goroutine finish.
	close(exec.release)
}

// ── §3.1 #7: the drain semaphore send must respect ctx cancellation ──

// TestDrainSemaphoreSendRespectsCancellation pins the shutdown liveness of
// the spawn loop: with every semaphore slot held by a stuck quantum, a
// cancelled ctx must break the loop's parking send instead of blocking
// until the stuck quantum returns. Pre-fix, `sem <- struct{}{}` was a bare
// send and the drain hung past cancellation.
func TestDrainSemaphoreSendRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	fabric := taskfabric.NewFabric()
	exec := &blockingExecutor{
		id:      "stuck",
		typ:     models.AgentType("code"),
		enter:   make(chan struct{}),
		release: make(chan struct{}),
	}
	sched := New(fabric, map[string]CapabilityExecutor{"stuck": exec}, nil)
	sched.WithMaxConcurrent(1)

	for _, id := range []string{"t-one", "t-two"} {
		if err := fabric.Create(&taskfabric.Task{
			ID:          id,
			Capability:  "code",
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		sched.drain(ctx)
	}()

	select {
	case <-exec.enter:
	case <-time.After(5 * time.Second):
		t.Fatal("first quantum never started")
	}
	// Give the drain loop a moment to park on the semaphore send for the
	// second task (all slots are held by the stuck quantum).
	time.Sleep(100 * time.Millisecond)

	cancel()
	select {
	case <-drainDone:
		// The loop observed the cancellation while parked on the send.
	case <-time.After(5 * time.Second):
		t.Fatal("drain is blocked on the semaphore send despite ctx cancellation")
	}
	close(exec.release)
}

// ── §3.1 #10: LoadTracker entries for dead agents are swept ──

// TestReconcileForgetsDeadAgentTrackerEntries pins the bounded-growth fix:
// when an agent disappears from the fabric, its accumulated tracker stats
// are forgotten on the next reconciliation, while a config-injected
// priority for a not-yet-spawned static peer survives (that set is fixed,
// not rotating).
func TestReconcileForgetsDeadAgentTrackerEntries(t *testing.T) {
	ctx := context.Background()

	agents := agentfabric.NewFabric()
	fabric := taskfabric.NewFabric()
	tracker := NewLoadTracker()
	sched := New(fabric, map[string]CapabilityExecutor{}, tracker)
	sched.WithAgentFabric(agents)

	// A rotating agent: spawns, accumulates history, dies.
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "rot-1",
		Capabilities: []string{"code"},
		CognitionFactory: func([]string) agentfabric.Cognition {
			return &countingCognition{}
		},
	}); err != nil {
		t.Fatalf("spawn rot-1: %v", err)
	}
	tracker.Begin("rot-1")
	tracker.End("rot-1", true)
	if err := agents.Kill(ctx, "rot-1"); err != nil {
		t.Fatalf("kill rot-1: %v", err)
	}

	// A static config injection (priority for a peer that has not spawned
	// yet) must survive the sweep.
	tracker.SetPriority("cfg-peer", 7)

	sched.reconcileFabricDeaths()

	snap := tracker.Snapshot()
	for _, a := range snap.Agents {
		if a.AgentID == "rot-1" {
			t.Fatal("dead agent's tracker entry must be forgotten by reconcileFabricDeaths")
		}
	}
	found := false
	for _, a := range snap.Agents {
		if a.AgentID == "cfg-peer" {
			found = true
			if a.Priority != 7 {
				t.Fatalf("cfg-peer priority = %v, want 7", a.Priority)
			}
		}
	}
	if !found {
		t.Fatal("priority-only config entry must survive the sweep (not rotating)")
	}
}
