package main

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// evolutionSpawnCognition is the A1 execution body a GA/evolution policy would
// inject into its spawned agents (F1: GA spawn 真实执行体). It completes every
// task in one quantum.
type evolutionSpawnCognition struct{}

func (evolutionSpawnCognition) ExecuteStep(_ context.Context, task *models.Task) (*agentfabric.StepOutcome, error) {
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess(nil, "evolved by "+task.TaskID)
	return &agentfabric.StepOutcome{Done: true, Result: res}, nil
}

// TestF1_EvolutionSpawnedAgentIsExecutableAndSchedulable verifies the F1
// acceptance (aresos-agentos-plan F1: GA spawn 的 agent 能被真实调度执行，
// 非 phantom): an evolution policy that spawns agents WITH their execution body
// (CognitionFactory — the A1 factory) produces REAL cognitive processes that
// the kernel scheduler selects and executes, not empty shells. The chain is
// exactly the production one: AdaptPopulation → agents.Spawn → scheduler
// WithAgentFabric candidate → Schedule → Acquire → RunQuantum → COMPLETED.
func TestF1_EvolutionSpawnedAgentIsExecutableAndSchedulable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agents := agentfabric.NewFabric()
	fabric := taskfabric.NewFabric()
	sched := NewKernelScheduler(fabric, map[string]CapabilityExecutor{}, newLoadTracker())
	sched.PollInterval = 20 * time.Millisecond
	sched.WithAgentFabric(agents)
	go sched.Run(ctx)

	// GA/evolution decides to spawn a "reviewer" capability agent, carrying
	// its execution body (A1 CognitionFactory).
	adapter := aresrecovery.NewEvolutionAdapter(agents, agents)
	spawned, err := adapter.AdaptPopulation(ctx, []agentfabric.SpawnSpec{
		{
			Identity:     "evolved-reviewer",
			Capabilities: []string{"reviewer"},
			CognitionFactory: func([]string) agentfabric.Cognition {
				return evolutionSpawnCognition{}
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("AdaptPopulation: %v", err)
	}
	if len(spawned) != 1 {
		t.Fatalf("want 1 spawned, got %d", len(spawned))
	}

	// The spawned agent is a REAL execution body — not a phantom shell.
	a, err := agents.Get("evolved-reviewer")
	if err != nil {
		t.Fatalf("Get evolved agent: %v", err)
	}
	if !a.Executable() {
		t.Fatal("F1: GA-spawned agent must be executable (Cognition injected), not a phantom")
	}

	// The GA-spawned agent is schedulable: a task requiring its capability is
	// executed by it through the real scheduler chain.
	if err := fabric.Create(&taskfabric.Task{
		ID:          "f1-task",
		Capability:  "reviewer",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if state := waitTaskState(t, fabric, "f1-task", 3*time.Second); state != taskfabric.StateCompleted {
		t.Fatalf("GA-spawned agent must be scheduled and complete the task, got %s", state)
	}
}
