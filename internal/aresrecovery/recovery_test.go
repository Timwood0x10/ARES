package aresrecovery

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// newRecoveryHarness wires a fresh Task Fabric + Agent Fabric + Recovery +
// Chaos for a test, with a controllable clock.
func newRecoveryHarness(t *testing.T) (*taskfabric.Fabric, *agentfabric.Fabric, *Recovery, *Chaos, *time.Time) {
	t.Helper()
	tasks := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()
	now := time.Now()
	// taskfabric.Fabric exposes its clock via WithClock (same pattern as
	// taskfabric/fabric_test.go's withClock helper, but cross-package).
	tasks.WithClock(func() time.Time { return now })
	rec := New(tasks, agents, DefaultRestartPolicy()).WithClock(func() time.Time { return now })
	chaos := NewChaos(agents, rec)
	return tasks, agents, rec, chaos, &now
}

// TestRequeueExpiredLeases verifies the first recovery path: a dead agent's
// lease expires, the task is requeued to READY, and another agent can
// acquire it (Agent 死亡 ≠ Task 死亡).
func TestRequeueExpiredLeases(t *testing.T) {
	tasks, agents, rec, _, now := newRecoveryHarness(t)
	ctx := context.Background()
	if err := tasks.Create(&taskfabric.Task{ID: "t1", Capability: "rust"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Agent a acquires and starts the task.
	epoch, err := tasks.Acquire("t1", "a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := tasks.Start("t1", "a", epoch); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Agent a dies (kill — lease is now orphaned).
	_ = agents.Kill(ctx, "a")
	// Advance past the TTL.
	*now = now.Add(2 * time.Minute)
	requeued := rec.RequeueExpiredLeases()
	if requeued != 1 {
		t.Fatalf("want 1 requeued, got %d", requeued)
	}
	// Agent b can now acquire the requeued task.
	if _, err := tasks.Acquire("t1", "b", time.Minute); err != nil {
		t.Fatalf("b must acquire after recovery: %v", err)
	}
}

// TestRecoverTaskCheckpoint verifies the second recovery path: a task with a
// preserved checkpoint is resumed by a replacement agent that picks up the
// checkpoint.
func TestRecoverTaskCheckpoint(t *testing.T) {
	tasks, _, rec, _, now := newRecoveryHarness(t)
	ctx := context.Background()
	if err := tasks.Create(&taskfabric.Task{ID: "t1", Capability: "rust"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Agent a acquires, starts, yields a checkpoint.
	epoch, err := tasks.Acquire("t1", "a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := tasks.Start("t1", "a", epoch); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tasks.Yield("t1", "a", epoch, map[string]any{"step": 5}); err != nil {
		t.Fatalf("Yield: %v", err)
	}
	// Lease expires.
	*now = now.Add(2 * time.Minute)
	rec.RequeueExpiredLeases()
	// Recover the checkpoint with a replacement agent.
	repID, newEpoch, err := rec.RecoverTaskCheckpoint(ctx, "t1", "")
	if err != nil {
		t.Fatalf("RecoverTaskCheckpoint: %v", err)
	}
	if repID == "" || newEpoch == 0 {
		t.Fatalf("want replacement id + epoch, got %q %d", repID, newEpoch)
	}
	// The replacement now owns the task and can resume.
	task, _ := tasks.Task("t1")
	if task.Owner != repID {
		t.Fatalf("replacement must own the task, got %q", task.Owner)
	}
}

// TestRestartAgent verifies the third recovery path: a crashed agent is
// replaced by a new one that picks up the cognitive state.
func TestRestartAgent(t *testing.T) {
	_, agents, rec, _, _ := newRecoveryHarness(t)
	ctx := context.Background()
	// Original agent a1 (now dead — we just need its cognitive state).
	cognitive := agentfabric.CognitiveState{
		Context:       "analyzing",
		WorkingMemory: []string{"step1", "step2"},
	}
	caps := []string{"rust", "unsafe-analysis"}
	a2, err := rec.RestartAgent(ctx, "a1", cognitive, caps)
	if err != nil {
		t.Fatalf("RestartAgent: %v", err)
	}
	if a2.Identity == "a1" {
		t.Fatal("replacement must have a new id")
	}
	if a2.State != agentfabric.StateIdle {
		t.Fatalf("replacement must be IDLE, got %s", a2.State)
	}
	// Cognitive state must be installed.
	cs, _ := agents.CognitiveState(a2.Identity)
	if cs.Context != "analyzing" {
		t.Fatalf("cognitive state must be installed, got %+v", cs)
	}
	if rec.RestartCount("a1") != 1 {
		t.Fatalf("restart count must be 1, got %d", rec.RestartCount("a1"))
	}
}

// TestRestartBudgetExhausted verifies the restart policy bounds attempts.
func TestRestartBudgetExhausted(t *testing.T) {
	_, _, rec, _, _ := newRecoveryHarness(t)
	ctx := context.Background()
	// Exhaust the budget.
	for i := 0; i < DefaultRestartPolicy().MaxRestarts; i++ {
		if _, err := rec.RestartAgent(ctx, "a1", agentfabric.CognitiveState{}, []string{"rust"}); err != nil {
			t.Fatalf("restart %d must succeed, got %v", i, err)
		}
	}
	// Next attempt must be rejected.
	_, err := rec.RestartAgent(ctx, "a1", agentfabric.CognitiveState{}, []string{"rust"})
	if err == nil {
		t.Fatal("must error after budget exhausted")
	}
}

// TestFullRecoveryChain verifies the complete P5 acceptance path: inject
// failure (kill agent) → lease expires → Task READY → B acquire → checkpoint
// resume. The task survives the agent's death.
func TestFullRecoveryChain(t *testing.T) {
	tasks, agents, _, chaos, now := newRecoveryHarness(t)
	ctx := context.Background()
	if err := tasks.Create(&taskfabric.Task{ID: "t1", Capability: "rust"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Spawn agent a so the chaos harness can kill it.
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity: "a", Capabilities: []string{"rust"},
	}); err != nil {
		t.Fatalf("Spawn a: %v", err)
	}
	// Agent a acquires, starts, yields a checkpoint.
	epoch, err := tasks.Acquire("t1", "a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := tasks.Start("t1", "a", epoch); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tasks.Yield("t1", "a", epoch, map[string]any{"step": 7}); err != nil {
		t.Fatalf("Yield: %v", err)
	}
	// Inject failure: kill agent a.
	if err := chaos.InjectFailure(ctx, "a", FailureKill); err != nil {
		t.Fatalf("InjectFailure: %v", err)
	}
	// Advance past the TTL.
	*now = now.Add(2 * time.Minute)
	// Verify recovery: the Runtime restores the task.
	recovered := chaos.VerifyRecovery(ctx)
	if recovered == 0 {
		t.Fatal("at least 1 task must be recovered")
	}
	// The task must now be owned by a replacement agent.
	task, _ := tasks.Task("t1")
	if task.Owner == "a" || task.Owner == "" {
		t.Fatalf("replacement must own the task, got %q", task.Owner)
	}
}

// TestEvolutionAdaptPopulation verifies the Evolution Runtime Adaptation:
// the adapter can spawn and retire agents (agent population adaptation).
func TestEvolutionAdaptPopulation(t *testing.T) {
	_, agents, _, _, _ := newRecoveryHarness(t)
	ctx := context.Background()
	adapter := NewEvolutionAdapter(agents, agents)
	// Spawn two agents.
	spawned, err := adapter.AdaptPopulation(ctx,
		[]agentfabric.SpawnSpec{
			{Identity: "e1", Capabilities: []string{"rust"}},
			{Identity: "e2", Capabilities: []string{"python"}},
		}, nil)
	if err != nil {
		t.Fatalf("AdaptPopulation spawn: %v", err)
	}
	if len(spawned) != 2 {
		t.Fatalf("want 2 spawned, got %d", len(spawned))
	}
	// Retire one.
	_, err = adapter.AdaptPopulation(ctx, nil, []string{"e1"})
	if err != nil {
		t.Fatalf("AdaptPopulation retire: %v", err)
	}
	// A retired agent stays in the registry (provenance preserved) but is
	// in the RETIRED state — it cannot be resumed.
	a, err := agents.Get("e1")
	if err != nil {
		t.Fatalf("e1 must still be in the registry (retired), got %v", err)
	}
	if a.State != agentfabric.StateRetired {
		t.Fatalf("e1 must be RETIRED, got %s", a.State)
	}
	// e2 must survive (unaffected by e1's retire).
	e2, err := agents.Get("e2")
	if err != nil {
		t.Fatalf("e2 must survive retire of e1: %v", err)
	}
	if e2.State != agentfabric.StateIdle {
		t.Fatalf("e2 must be IDLE, got %s", e2.State)
	}
}

// TestChaosSuspendFailure verifies the suspend failure type.
func TestChaosSuspendFailure(t *testing.T) {
	_, agents, _, chaos, _ := newRecoveryHarness(t)
	ctx := context.Background()
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity: "a", Capabilities: []string{"rust"},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := chaos.InjectFailure(ctx, "a", FailureSuspend); err != nil {
		t.Fatalf("InjectFailure suspend: %v", err)
	}
	a, _ := agents.Get("a")
	if a.State != agentfabric.StateSuspended {
		t.Fatalf("want SUSPENDED, got %s", a.State)
	}
	inj := chaos.InjectedFailures()
	if inj["a"] != FailureSuspend {
		t.Fatalf("injection must be recorded, got %+v", inj)
	}
}
