package kernel

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestPreemptLowerPriorityDoesNotEvictAnAgentThatCannotServe is RT-03.
//
// Both capability pools are busy and an urgent "database" task is ready. The lowest-priority RUNNING
// task happens to be a "batch" one, so the victim scan picked it — freeing a batch agent for a task
// that needs a database agent. The urgent task still had no executor, and the batch quantum had been
// thrown away for nothing.
func TestPreemptLowerPriorityDoesNotEvictAnAgentThatCannotServe(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"batch-agent": &smokeExecutor{id: "batch-agent", typ: models.AgentType("batch")},
		"db-agent":    &smokeExecutor{id: "db-agent", typ: models.AgentType("database")},
	}, NewLoadTracker())

	createTask := func(id, capability string, priority int) {
		t.Helper()
		if err := fabric.Create(&taskfabric.Task{
			ID: id, Capability: capability, Priority: priority,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	// Both pools busy. The batch task is the cheapest victim by priority, and useless to evict.
	createTask("low-batch", "batch", 1)
	acquireAndStart(t, fabric, "low-batch", "batch-agent")
	createTask("busy-db", "database", 50)
	acquireAndStart(t, fabric, "busy-db", "db-agent")

	// Urgent work that only the database pool can run.
	createTask("urgent-db", "database", 10)

	sched.PreemptLowerPriority(context.Background(), fabric.ResumableTasks())

	tk, err := fabric.Task("low-batch")
	if err != nil {
		t.Fatalf("Task low-batch: %v", err)
	}
	if tk.State != taskfabric.StateRunning {
		t.Fatalf("a batch task must not be evicted for database work: state = %s", tk.State)
	}
	if tk.Owner != "batch-agent" {
		t.Fatalf("low-batch must keep its owner, got %q", tk.Owner)
	}
}

// TestPreemptLowerPriorityEvictsACapableOwner is the other half: when the busy owner CAN serve the
// urgent work, that task is the victim. The fix must not turn preemption off.
func TestPreemptLowerPriorityEvictsACapableOwner(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"batch-agent": &smokeExecutor{id: "batch-agent", typ: models.AgentType("batch")},
		"db-agent":    &smokeExecutor{id: "db-agent", typ: models.AgentType("database")},
	}, NewLoadTracker())

	createTask := func(id, capability string, priority int) {
		t.Helper()
		if err := fabric.Create(&taskfabric.Task{
			ID: id, Capability: capability, Priority: priority,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	createTask("high-batch", "batch", 50)
	acquireAndStart(t, fabric, "high-batch", "batch-agent")
	createTask("low-db", "database", 1)
	acquireAndStart(t, fabric, "low-db", "db-agent")

	createTask("urgent-db", "database", 10)

	sched.PreemptLowerPriority(context.Background(), fabric.ResumableTasks())

	tk, err := fabric.Task("low-db")
	if err != nil {
		t.Fatalf("Task low-db: %v", err)
	}
	if tk.State == taskfabric.StateRunning {
		t.Fatal("the database owner can serve the urgent task and must be the victim")
	}

	other, err := fabric.Task("high-batch")
	if err != nil {
		t.Fatalf("Task high-batch: %v", err)
	}
	if other.State != taskfabric.StateRunning {
		t.Fatalf("the batch task is not a usable victim here: state = %s", other.State)
	}
}

// TestPreemptLowerPriorityMatchesEachDemandToOneCapableVictim is the CPU+GPU case: two urgent tasks,
// two CPU owners and one GPU owner all busy with low-priority work. Counting "free agents that can run
// something prioritized" as a single number evicted both CPU owners and left the GPU task queued.
func TestPreemptLowerPriorityMatchesEachDemandToOneCapableVictim(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"cpu-1": &smokeExecutor{id: "cpu-1", typ: models.AgentType("cpu")},
		"cpu-2": &smokeExecutor{id: "cpu-2", typ: models.AgentType("cpu")},
		"gpu-1": &smokeExecutor{id: "gpu-1", typ: models.AgentType("gpu")},
	}, NewLoadTracker())

	mk := func(id, capability string, priority int) {
		t.Helper()
		if err := fabric.Create(&taskfabric.Task{
			ID: id, Capability: capability, Priority: priority,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	mk("low-cpu-a", "cpu", 1)
	acquireAndStart(t, fabric, "low-cpu-a", "cpu-1")
	mk("low-cpu-b", "cpu", 1)
	acquireAndStart(t, fabric, "low-cpu-b", "cpu-2")
	mk("low-gpu", "gpu", 1)
	acquireAndStart(t, fabric, "low-gpu", "gpu-1")

	mk("urgent-cpu", "cpu", 10)
	mk("urgent-gpu", "gpu", 10)

	sched.PreemptLowerPriority(context.Background(), fabric.ResumableTasks())

	gpuTask, err := fabric.Task("low-gpu")
	if err != nil {
		t.Fatalf("Task low-gpu: %v", err)
	}
	if gpuTask.State == taskfabric.StateRunning {
		t.Fatal("the GPU demand must evict a GPU owner, not be cancelled by CPU capacity")
	}

	// Exactly one CPU owner is spent for the CPU demand; the second is not a second victim.
	first, err := fabric.Task("low-cpu-a")
	if err != nil {
		t.Fatalf("Task low-cpu-a: %v", err)
	}
	second, err := fabric.Task("low-cpu-b")
	if err != nil {
		t.Fatalf("Task low-cpu-b: %v", err)
	}
	if first.State == taskfabric.StateRunning && second.State == taskfabric.StateRunning {
		t.Fatal("one CPU demand must not evict two CPU owners")
	}
}

// TestPreemptLowerPriorityIdleAgentsDoNotCancelAnotherCapabilityDemand is the other half of the same
// bug: three idle CPU agents must not be counted against a GPU demand, or the low-priority GPU work is
// never preempted and the urgent GPU task waits forever.
func TestPreemptLowerPriorityIdleAgentsDoNotCancelAnotherCapabilityDemand(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"cpu-a": &smokeExecutor{id: "cpu-a", typ: models.AgentType("cpu")},
		"cpu-b": &smokeExecutor{id: "cpu-b", typ: models.AgentType("cpu")},
		"cpu-c": &smokeExecutor{id: "cpu-c", typ: models.AgentType("cpu")},
		"gpu-1": &smokeExecutor{id: "gpu-1", typ: models.AgentType("gpu")},
	}, NewLoadTracker())

	mk := func(id, capability string, priority int) {
		t.Helper()
		if err := fabric.Create(&taskfabric.Task{
			ID: id, Capability: capability, Priority: priority,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	mk("low-gpu", "gpu", 1)
	acquireAndStart(t, fabric, "low-gpu", "gpu-1")

	// One urgent CPU task (three idle CPU agents can serve it) and one urgent GPU task (none can).
	mk("urgent-cpu", "cpu", 10)
	mk("urgent-gpu", "gpu", 10)

	sched.PreemptLowerPriority(context.Background(), fabric.ResumableTasks())

	gpuTask, err := fabric.Task("low-gpu")
	if err != nil {
		t.Fatalf("Task low-gpu: %v", err)
	}
	if gpuTask.State == taskfabric.StateRunning {
		t.Fatal("surplus CPU agents must not stand in for the missing GPU capacity")
	}
}

// TestPreemptLowerPriorityUsesFabricCapabilitiesNotStaticType is the multi-capability case: one Fabric
// agent declares cpu+gpu, but its static registration only says "cpu". Reading the static Type() made
// the scheduler treat it as unable to serve GPU work, so the urgent GPU task waited behind that
// agent's low-priority CPU task while preemption looked elsewhere.
func TestPreemptLowerPriorityUsesFabricCapabilitiesNotStaticType(t *testing.T) {
	ctx := context.Background()
	taskFabric := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()

	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "hybrid",
		Capabilities: []string{"cpu", "gpu"},
		CognitionFactory: func([]string) agentfabric.Cognition {
			return &countingCognition{}
		},
	}); err != nil {
		t.Fatalf("spawn hybrid agent: %v", err)
	}

	// The static registration is deliberately narrow: this is what the old code consulted.
	sched := New(taskFabric, map[string]CapabilityExecutor{
		"hybrid": &smokeExecutor{id: "hybrid", typ: models.AgentType("cpu")},
	}, NewLoadTracker()).WithAgentFabric(agents)

	if err := taskFabric.Create(&taskfabric.Task{
		ID: "low-cpu", Capability: "cpu", Priority: 1,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create low-cpu: %v", err)
	}
	acquireAndStart(t, taskFabric, "low-cpu", "hybrid")

	if err := taskFabric.Create(&taskfabric.Task{
		ID: "urgent-gpu", Capability: "gpu", Priority: 10,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create urgent-gpu: %v", err)
	}

	sched.PreemptLowerPriority(ctx, taskFabric.ResumableTasks())

	tk, err := taskFabric.Task("low-cpu")
	if err != nil {
		t.Fatalf("Task low-cpu: %v", err)
	}
	if tk.State == taskfabric.StateRunning {
		t.Fatal("the Fabric agent can serve GPU work and must be the victim; its static Type() must not decide")
	}
}

// TestPreemptLowerPriorityDoesNotEvictBoundExecutor: a recovery-bound executor can only serve the task
// it is bound to. Preempting it frees nothing for the urgent task and interrupts recovery work that
// will simply be resumed — the bound check has to live in the victim loop, not only in free capacity.
func TestPreemptLowerPriorityDoesNotEvictBoundExecutor(t *testing.T) {
	fabric := taskfabric.NewFabric()
	bound := &smokeExecutor{id: "bound-agent", typ: models.AgentType("cpu")}
	sched := New(fabric, map[string]CapabilityExecutor{
		"bound-agent": bound,
	}, NewLoadTracker())

	// The recovery binding that makes this owner unusable for anything else.
	sched.RegisterExecutorForTask("recovered-task", "bound-agent", bound)

	if err := fabric.Create(&taskfabric.Task{
		ID: "low-bound", Capability: "cpu", Priority: 1,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create low-bound: %v", err)
	}
	acquireAndStart(t, fabric, "low-bound", "bound-agent")

	if err := fabric.Create(&taskfabric.Task{
		ID: "urgent-cpu", Capability: "cpu", Priority: 10,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create urgent-cpu: %v", err)
	}

	sched.PreemptLowerPriority(context.Background(), fabric.ResumableTasks())

	tk, err := fabric.Task("low-bound")
	if err != nil {
		t.Fatalf("Task low-bound: %v", err)
	}
	if tk.State != taskfabric.StateRunning {
		t.Fatal("a bound executor must not be preempted: it cannot serve the urgent task afterwards")
	}
}

// TestPreemptLowerPriorityIgnoresNonExecutableAgents: an idle agent with no cognition injected cannot
// take the urgent task, so it is not spare capacity. Counting it cancels the preemption that was the
// only way the urgent task could start.
func TestPreemptLowerPriorityIgnoresNonExecutableAgents(t *testing.T) {
	ctx := context.Background()
	agents := agentfabric.NewFabric()

	// Two idle fabric agents with the same capability; only one can actually run work. The pair makes
	// the assertion meaningful: without the executable filter BOTH would be counted as spare capacity.
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity: "gpu-ghost", Capabilities: []string{"gpu"},
	}); err != nil {
		t.Fatalf("spawn ghost: %v", err)
	}
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "gpu-real",
		Capabilities: []string{"gpu"},
		CognitionFactory: func([]string) agentfabric.Cognition {
			return &countingCognition{}
		},
	}); err != nil {
		t.Fatalf("spawn real: %v", err)
	}

	sched := New(taskfabric.NewFabric(), nil, NewLoadTracker()).WithAgentFabric(agents)

	free := sched.freeAgentIDs(nil)

	if !containsID(free, "gpu-real") {
		t.Fatal("an idle, executable agent must count as spare capacity")
	}
	if containsID(free, "gpu-ghost") {
		t.Fatal("an idle agent with no cognition cannot take the urgent task and must not cancel the demand")
	}
}

// containsID reports whether ids holds want.
func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestPlanPreemptions_ReassignsInsteadOfEvicting is the greedy-match case: a-flex(cpu,gpu)
// and z-cpu(cpu) are both idle and can serve CPU+GPU together. Taking the CPU demand with a-flex (the
// first name in order) leaves the GPU demand unmatched and a preemption is manufactured for work that
// had a free executor all along.
func TestAssignFreeAgents_ReassignsInsteadOfLeavingADemandUnmet(t *testing.T) {
	ctx := context.Background()
	agents := agentfabric.NewFabric()

	spawn := func(id string, caps ...string) {
		t.Helper()
		if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
			Identity:     id,
			Capabilities: caps,
			CognitionFactory: func([]string) agentfabric.Cognition {
				return &countingCognition{}
			},
		}); err != nil {
			t.Fatalf("spawn %s: %v", id, err)
		}
	}
	spawn("a-flex", "cpu", "gpu")
	spawn("z-cpu", "cpu")

	sched := New(taskfabric.NewFabric(), nil, NewLoadTracker()).WithAgentFabric(agents)

	demands := []preemptionDemand{
		{id: "urgent-cpu", capability: "cpu", priority: 10},
		{id: "urgent-gpu", capability: "gpu", priority: 10},
	}

	plan := sched.planPreemptions(ctx, demands, nil, nil)

	if len(plan.victims) != 0 {
		t.Fatalf("both demands have a free executor; %d victims were chosen", len(plan.victims))
	}
}

// spawnCaps spawns executable Fabric agents in one call.
func spawnCaps(t *testing.T, agents *agentfabric.Fabric, specs map[string][]string) {
	t.Helper()
	for id, caps := range specs {
		if _, err := agents.Spawn(context.Background(), agentfabric.SpawnSpec{
			Identity:     id,
			Capabilities: caps,
			CognitionFactory: func([]string) agentfabric.Cognition {
				return &countingCognition{}
			},
		}); err != nil {
			t.Fatalf("spawn %s: %v", id, err)
		}
	}
}

// TestPreemptLowerPriorityMatchingSpansFreeAndVictimAgents is the all-busy counterexample: the CPU
// demand takes the dual-capability victim (it sorts first), leaving the GPU demand nothing it can
// serve, while the CPU-only victim keeps running.
func TestPreemptLowerPriorityMatchingSpansFreeAndVictimAgents(t *testing.T) {
	ctx := context.Background()
	taskFabric := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()
	spawnCaps(t, agents, map[string][]string{
		"aaa-dual": {"cpu", "gpu"},
		"zzz-cpu":  {"cpu"},
	})
	sched := New(taskFabric, nil, NewLoadTracker()).WithAgentFabric(agents)

	mk := func(id, capability string, priority int) {
		t.Helper()
		if err := taskFabric.Create(&taskfabric.Task{
			ID: id, Capability: capability, Priority: priority,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	// The dual victim sorts FIRST by task id, which is what makes the first-fit failure visible:
	// without cross-phase matching it is spent on the CPU demand and GPU finds nothing.
	mk("aaa-low-dual", "gpu", 1)
	acquireAndStart(t, taskFabric, "aaa-low-dual", "aaa-dual")
	mk("zzz-low-cpu", "cpu", 1)
	acquireAndStart(t, taskFabric, "zzz-low-cpu", "zzz-cpu")

	mk("urgent-cpu", "cpu", 10)
	mk("urgent-gpu", "gpu", 10)

	sched.PreemptLowerPriority(ctx, taskFabric.ResumableTasks())

	cpuTask, err := taskFabric.Task("zzz-low-cpu")
	if err != nil {
		t.Fatalf("Task zzz-low-cpu: %v", err)
	}
	if cpuTask.State == taskfabric.StateRunning {
		t.Fatal("the CPU-only victim must be spent on the CPU demand so the dual victim is free for GPU")
	}
}

// TestPreemptLowerPriorityReassignsAFreeAgentToTheRigidDemand is the partially-free counterexample: a
// free dual-capability agent is spent on the CPU demand, GPU has no victim, and the CPU-only low
// priority task keeps running. Matching across both kinds lets the CPU demand evict the CPU-only
// owner and the dual agent take GPU.
func TestPreemptLowerPriorityReassignsAFreeAgentToTheRigidDemand(t *testing.T) {
	ctx := context.Background()
	taskFabric := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()
	spawnCaps(t, agents, map[string][]string{
		"aaa-flex": {"cpu", "gpu"},
		"zzz-cpu":  {"cpu"},
	})
	sched := New(taskFabric, nil, NewLoadTracker()).WithAgentFabric(agents)

	mk := func(id, capability string, priority int) {
		t.Helper()
		if err := taskFabric.Create(&taskfabric.Task{
			ID: id, Capability: capability, Priority: priority,
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	mk("low-cpu", "cpu", 1)
	acquireAndStart(t, taskFabric, "low-cpu", "zzz-cpu")

	mk("urgent-cpu", "cpu", 10)
	mk("urgent-gpu", "gpu", 10)

	sched.PreemptLowerPriority(ctx, taskFabric.ResumableTasks())

	cpuTask, err := taskFabric.Task("low-cpu")
	if err != nil {
		t.Fatalf("Task low-cpu: %v", err)
	}
	if cpuTask.State == taskfabric.StateRunning {
		t.Fatal("the dual agent must be reassigned to GPU and the CPU-only owner evicted for CPU")
	}
}
