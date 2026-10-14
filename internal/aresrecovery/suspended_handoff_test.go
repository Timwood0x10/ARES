package aresrecovery

// suspended_handoff_test.go is the cross-module half of the SUSPENDED reclamation
// arm (defect: a paused agent was unreclaimable, so it held its resource claim and
// its Cognition for the life of the process).
//
// Retiring a paused agent is only correct because the paused WORK survives it, and
// that is a causal chain across three modules:
//
//	agent fabric: ReclaimIdleAgents retires the paused agent (claim released)
//	task  fabric: its SUSPENDED task keeps its lease (Yield only transitions state)
//	task  fabric: the lease expires -> CheckExpiredLeases requeues it to READY
//	agent fabric: another agent acquires it, checkpoint intact (drain picks it up
//	              through ResumableTasks)
//
// Every link existed before this change and was only described in comments. This is
// the assertion that the chain holds, so "reclaim the paused agent" cannot silently
// become "throw the paused work away".

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

func TestReclaimSuspendedAgent_ItsWorkIsResumedByTheLeasePath(t *testing.T) {
	ctx := context.Background()
	agentNow := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	taskNow := agentNow
	agents := agentfabric.NewFabric().WithClock(func() time.Time { return agentNow })
	tasks := taskfabric.NewFabric().WithClock(func() time.Time { return taskNow })

	rec := New(tasks, agents, RestartPolicy{
		MaxRestarts:           5,
		Backoff:               time.Second,
		MaxBackoff:            30 * time.Second,
		IdleAgentTTL:          10 * time.Minute,
		SuspendedAgentTTL:     30 * time.Minute,
		RetiredAgentRetention: time.Hour,
	})

	// A worker holding a claim, paused mid-flight.
	_, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "worker",
		Capabilities: []string{"coder"},
		Resources:    map[string]any{"slot": 1.0},
	})
	require.NoError(t, err)
	require.NoError(t, agents.Suspend(ctx, "worker"))

	// Its quantum yields: the task becomes SUSPENDED with its checkpoint preserved
	// and its lease KEPT — the lease is what carries the work across the retirement.
	require.NoError(t, tasks.Create(&taskfabric.Task{ID: "t1", Capability: "coder"}))
	epoch, err := tasks.Acquire("t1", "worker", time.Minute)
	require.NoError(t, err)
	require.NoError(t, tasks.Start("t1", "worker", epoch))
	require.NoError(t, tasks.Yield("t1", "worker", epoch, map[string]any{"step": 3}))

	// Time passes: past the suspended window AND past the lease.
	agentNow = agentNow.Add(31 * time.Minute)
	taskNow = taskNow.Add(2 * time.Minute)

	// A fresh holder of the capability, so the per-capability floor is not what
	// decides the outcome here.
	_, err = agents.Spawn(ctx, agentfabric.SpawnSpec{Identity: "keeper", Capabilities: []string{"coder"}})
	require.NoError(t, err)

	// Phase 1 — the hand-off has NOT run yet, and the sweep must leave the agent
	// alone. Not an accident: LeaseSnapshot reports every NON-TERMINAL task without
	// filtering by lease expiry (it copies ExpiresAt rather than judging it), so the
	// agent still owns work no one else can take. Retiring it here would tear down
	// the holder before anything else had picked the work up.
	require.Empty(t, rec.ReclaimIdleAgents(ctx),
		"an agent whose task is still assigned to it must not be reclaimed")

	// Phase 2 — the kernel sweep's own order: requeue expired leases BEFORE
	// reclaiming (runSweepChain's steps 3 and 4). That order is what makes this arm
	// safe, because the requeue is what clears the owner.
	require.Equal(t, []string{"t1"}, rec.RequeueExpiredLeases(),
		"the expired lease must return the paused task to READY")
	tk, err := tasks.Task("t1")
	require.NoError(t, err)
	require.Equal(t, taskfabric.StateReady, tk.State)
	require.NotNil(t, tk.Checkpoint, "with its checkpoint intact: the work is resumed, not restarted")

	reclaimed := rec.ReclaimIdleAgents(ctx)
	require.Len(t, reclaimed, 1, "a paused agent past its window must be reclaimed")
	require.Equal(t, agentfabric.StatePaused, reclaimed[0].State,
		"and the report must say which state it was retired from")
	allocated, _ := agents.QuotaSnapshot()
	require.Zero(t, allocated["slot"], "its claim must be back in the pool")

	// Phase 3 — another agent takes the work over, which is what the scheduler's
	// drain does through ResumableTasks.
	_, err = tasks.Acquire("t1", "keeper", time.Minute)
	require.NoError(t, err, "a live agent must be able to take the paused work over")
}
