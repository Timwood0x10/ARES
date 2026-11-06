package kernel

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

func createPlacementTask(t *testing.T, fabric *taskfabric.Fabric, task *taskfabric.Task) {
	t.Helper()
	task.RetryPolicy = taskfabric.RetryPolicy{MaxRetries: 1}
	require.NoError(t, fabric.Create(task))
}

func TestPreemptionPrefersReachableFreeSlotOverVictim(t *testing.T) {
	for _, tc := range []struct {
		name     string
		gpuAgent string
	}{
		{name: "direct-free-endpoint", gpuAgent: "z-gpu"},
		{name: "free-slot-sorts-first", gpuAgent: "a-gpu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fabric := taskfabric.NewFabric()
			agents := agentfabric.NewFabric()
			spawnCaps(t, agents, map[string][]string{
				"m-flex":    {"cpu", "gpu"},
				tc.gpuAgent: {"gpu"},
				"busy-cpu":  {"cpu"},
			})
			for _, id := range agents.Agents() {
				t.Cleanup(func() { require.NoError(t, agents.Kill(context.Background(), id)) })
			}
			sched := New(fabric, nil, nil).WithAgentFabric(agents)
			createPlacementTask(t, fabric, &taskfabric.Task{ID: "low", Capability: "cpu", Priority: 1})
			acquireAndStart(t, fabric, "low", "busy-cpu")
			createPlacementTask(t, fabric, &taskfabric.Task{ID: "urgent-cpu", Capability: "cpu", Priority: 100})
			createPlacementTask(t, fabric, &taskfabric.Task{ID: "urgent-gpu", Capability: "gpu", Priority: 90})

			sched.PreemptLowerPriority(t.Context(), fabric.ResumableTasks())
			low, err := fabric.Task("low")
			require.NoError(t, err)
			require.Equal(t, taskfabric.StateRunning, low.State, "both demands fit idle capacity")
		})
	}
}

// Real quanta establish busy slots. Test both serial dispatch orders and the concurrent production
// drain: scoring must not give CPU the only GPU-capable agent, even when its confidence is higher.
func TestPreemptionAssignmentSurvivesDispatch(t *testing.T) {
	for _, order := range []string{"cpu-first", "gpu-first", "concurrent-drain"} {
		t.Run(order, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				fabric := taskfabric.NewFabric()
				agents := agentfabric.NewFabric()
				sched := New(fabric, nil, nil).WithAgentFabric(agents)
				spawn := func(id string, caps ...string) {
					t.Helper()
					_, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
						Identity: id, Capabilities: caps,
						CognitionFactory: func([]string) agentfabric.Cognition {
							return &blockingCognition{started: make(chan string, 4), release: make(chan struct{})}
						},
					})
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, agents.Kill(context.Background(), id)) })
				}
				spawn("cpu", "cpu")
				createPlacementTask(t, fabric, &taskfabric.Task{ID: "low", Capability: "cpu", Priority: 1})
				lowCtx, stopLow := context.WithCancel(ctx)
				defer stopLow()
				lowDone := make(chan error, 1)
				go func() { lowDone <- sched.execute(lowCtx, "low") }()
				synctest.Wait()
				low, err := fabric.Task("low")
				require.NoError(t, err)
				require.Equal(t, taskfabric.StateRunning, low.State)
				spawn("flex", "cpu", "gpu")
				sched.tracker.SetAgentConfidence("cpu", 0.5)
				sched.tracker.SetAgentConfidence("flex", 1)
				createPlacementTask(t, fabric, &taskfabric.Task{ID: "urgent-cpu", Capability: "cpu", Priority: 100})
				createPlacementTask(t, fabric, &taskfabric.Task{ID: "urgent-gpu", Capability: "gpu", Priority: 90})
				sched.PreemptLowerPriority(ctx, fabric.ResumableTasks())
				low, err = fabric.Task("low")
				require.NoError(t, err)
				require.Equal(t, taskfabric.StateReady, low.State)

				// A second sweep happens before the fenced quantum exits. Its pending CPU slot must
				// remain paired with urgent-cpu; that task cannot fall back to the reserved flex agent.
				sched.PreemptLowerPriority(ctx, fabric.ResumableTasks())
				require.ErrorIs(t, sched.execute(ctx, "urgent-cpu"), taskfabric.ErrNoCapableCandidate)
				stopLow()
				synctest.Wait()
				// The low-priority quantum must exit cleanly; dropping its error would hide a failure
				// behind the assertion this test is about.
				require.NoError(t, <-lowDone)
				if order == "concurrent-drain" {
					sched.drain(ctx)
				} else {
					ids := []string{"urgent-cpu", "urgent-gpu"}
					if order == "gpu-first" {
						ids[0], ids[1] = ids[1], ids[0]
					}
					for _, id := range ids {
						go func() { _ = sched.execute(ctx, id) }()
						synctest.Wait()
					}
				}
				synctest.Wait()
				for id, owner := range map[string]string{"urgent-cpu": "cpu", "urgent-gpu": "flex"} {
					task, err := fabric.Task(id)
					require.NoError(t, err)
					require.Equal(t, taskfabric.StateRunning, task.State, "%s must start", id)
					require.Equal(t, owner, task.Owner)
				}
				cancel()
				synctest.Wait()
			})
		})
	}
}

func TestPreemptionDoesNotEvictAgainWhileVictimExits(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"cpu-a": &smokeExecutor{id: "cpu-a", typ: models.AgentType("cpu")},
		"cpu-b": &smokeExecutor{id: "cpu-b", typ: models.AgentType("cpu")},
	}, nil)
	for _, id := range []string{"cpu-a", "cpu-b"} {
		createPlacementTask(t, fabric, &taskfabric.Task{ID: id, Capability: "cpu", Priority: 1})
		acquireAndStart(t, fabric, id, id)
		sched.tracker.Begin(id)
	}
	createPlacementTask(t, fabric, &taskfabric.Task{ID: "urgent", Capability: "cpu", Priority: 10})
	sched.PreemptLowerPriority(t.Context(), fabric.ResumableTasks())
	sched.PreemptLowerPriority(t.Context(), fabric.ResumableTasks())
	first, err := fabric.Task("cpu-a")
	require.NoError(t, err)
	require.Equal(t, taskfabric.StateReady, first.State)
	second, err := fabric.Task("cpu-b")
	require.NoError(t, err)
	require.Equal(t, taskfabric.StateRunning, second.State, "one urgent demand buys only one eviction")
}

func TestDispatchReleasesObsoleteAssignments(t *testing.T) {
	for _, change := range []string{"task-deleted", "executor-removed", "recovery-bound"} {
		t.Run(change, func(t *testing.T) {
			fabric := taskfabric.NewFabric()
			sched := New(fabric, map[string]CapabilityExecutor{
				"cpu": &smokeExecutor{id: "cpu", typ: models.AgentType("cpu")},
			}, nil)
			createPlacementTask(t, fabric, &taskfabric.Task{ID: "urgent", Capability: "cpu", Priority: 10})
			sched.PreemptLowerPriority(t.Context(), fabric.ResumableTasks())
			target := "urgent"
			switch change {
			case "task-deleted":
				require.NoError(t, fabric.Delete("urgent"))
				target = "new"
				createPlacementTask(t, fabric, &taskfabric.Task{ID: target, Capability: "cpu"})
			case "executor-removed":
				sched.UnregisterExecutor("cpu")
				sched.RegisterExecutor("replacement", &smokeExecutor{id: "replacement", typ: models.AgentType("cpu")})
			case "recovery-bound":
				sched.RegisterExecutorForTask(
					"urgent", "replacement", &smokeExecutor{id: "replacement", typ: models.AgentType("cpu")},
				)
			}
			require.NoError(t, sched.execute(t.Context(), target))
			task, err := fabric.Task(target)
			require.NoError(t, err)
			require.Equal(t, taskfabric.StateCompleted, task.State)
		})
	}
}

func TestDispatchRejectsSnapshotThatWouldStealReservedAgent(t *testing.T) {
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{
		"cpu": &smokeExecutor{id: "cpu", typ: models.AgentType("cpu")},
	}, nil)
	createPlacementTask(t, fabric, &taskfabric.Task{ID: "background", Capability: "cpu"})
	stale := sched.buildCandidates("background")
	createPlacementTask(t, fabric, &taskfabric.Task{ID: "urgent", Capability: "cpu", Priority: 10})
	sched.PreemptLowerPriority(t.Context(), fabric.ResumableTasks())

	// The candidate snapshot predates the plan. Admission must consult the current reservation,
	// and completion must release it so ordinary work can use the executor again.
	require.ErrorIs(t,
		sched.executeWithCandidates(t.Context(), "background", stale), taskfabric.ErrNoCapableCandidate,
	)
	require.NoError(t, sched.execute(t.Context(), "urgent"))
	require.NoError(t, sched.execute(t.Context(), "background"))
	task, err := fabric.Task("background")
	require.NoError(t, err)
	require.Equal(t, taskfabric.StateCompleted, task.State)
}
