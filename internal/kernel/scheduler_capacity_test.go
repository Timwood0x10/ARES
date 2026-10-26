package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// blockingCognition reports which task reached the agent, then blocks until released. It is how a
// test holds a quantum "in flight" without racing the scheduler.
type blockingCognition struct {
	started chan string
	release chan struct{}
}

func (c *blockingCognition) ExecuteStep(ctx context.Context, task *models.Task) (*agentfabric.StepOutcome, error) {
	select {
	case c.started <- task.TaskID:
	default:
	}
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess(nil, "blocking cognition done")
	return &agentfabric.StepOutcome{Done: true, Result: res}, nil
}

// TestS01_AutoCapacityAdmitsWhileAnAgentIsIdle is the counter-example the 0.3.6 plan asks for,
// kept as a regression test. It reproduced before the fix: drainLimit() in auto mode counted IDLE
// agents, while admission compares the limit against ALL in-flight quanta, so with 3 agents,
// 2 busy and 1 idle the limit was 1, inflight was 2, and the third task was refused while an agent
// sat free.
//
// The control repeats the scenario with an explicit capacity of 3. It passed even while auto
// failed, which is what located the defect in the auto-capacity computation rather than in
// capability matching or the lease.
func TestS01_AutoCapacityAdmitsWhileAnAgentIsIdle(t *testing.T) {
	run := func(t *testing.T, explicit int) string {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		fabric := taskfabric.NewFabric()
		agents := agentfabric.NewFabric()
		cog := &blockingCognition{started: make(chan string, 8), release: make(chan struct{})}
		for i := 0; i < 3; i++ {
			if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
				Identity:     "worker-" + string(rune('a'+i)),
				Capabilities: []string{"code"},
				CognitionFactory: func([]string) agentfabric.Cognition {
					return cog
				},
			}); err != nil {
				t.Fatalf("spawn worker %d: %v", i, err)
			}
		}
		t.Cleanup(func() { close(cog.release) })

		sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
		sched.PollInterval = 5 * time.Millisecond
		sched.WithAgentFabric(agents)
		if explicit > 0 {
			sched.WithMaxConcurrent(explicit)
		}
		go sched.Run(ctx)

		// Two quanta in flight, held open. Waiting on the executor entry (not a sleep) is what
		// makes "busy" an observed fact rather than an assumption.
		for _, id := range []string{"held-1", "held-2"} {
			if err := fabric.Create(&taskfabric.Task{ID: id, Capability: "code", RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 5}}); err != nil {
				t.Fatalf("create %s: %v", id, err)
			}
			select {
			case got := <-cog.started:
				if got != id {
					t.Fatalf("expected %s to start, got %s", id, got)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s never reached an agent", id)
			}
		}

		// Third agent is idle. Nothing is released.
		if err := fabric.Create(&taskfabric.Task{ID: "third", Capability: "code", RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 5}}); err != nil {
			t.Fatalf("create third: %v", err)
		}

		select {
		case got := <-cog.started:
			return got
		case <-time.After(2 * time.Second):
			return ""
		}
	}

	t.Run("auto_capacity", func(t *testing.T) {
		if got := run(t, 0); got != "third" {
			t.Fatalf("an idle agent was available but the third task never started (got %q); "+
				"auto mode is comparing an IDLE-agent limit against ALL in-flight quanta again", got)
		}
	})

	t.Run("explicit_capacity_control", func(t *testing.T) {
		if got := run(t, 3); got != "third" {
			t.Fatalf("control failed too (got %q): the defect is not the auto-capacity computation", got)
		}
	})
}

// TestS02_AllBusyPreemptionIsRequested is the second counter-example, kept as a regression test.
// It reproduced before the fix: with every agent busy and the static registry empty, the entry
// condition evaluated ExecutorCount()==0 || idleExecutable==0 and returned before looking at
// priorities — skipping preemption in the one situation it exists for.
//
// The assertion is on PreemptRequested rather than on the high-priority task running, because
// preemption is cooperative: the in-flight quantum finishes its current step, and only the next
// dispatch can move.
func TestS02_AllBusyPreemptionIsRequested(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()
	cog := &blockingCognition{started: make(chan string, 4), release: make(chan struct{})}
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:         "only-worker",
		Capabilities:     []string{"code"},
		CognitionFactory: func([]string) agentfabric.Cognition { return cog },
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { close(cog.release) })

	sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
	sched.PollInterval = 5 * time.Millisecond
	sched.WithAgentFabric(agents)
	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{ID: "low", Capability: "code", Priority: 1}); err != nil {
		t.Fatalf("create low: %v", err)
	}
	select {
	case <-cog.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the low-priority task never started")
	}
	// Wait until the task is observably RUNNING: preemption only looks at RunningTasks, so a task
	// still in the acquire path would make this test pass or fail for the wrong reason.
	deadline := time.Now().Add(5 * time.Second)
	for {
		tk, err := fabric.Task("low")
		if err == nil && tk.State == taskfabric.StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("low-priority task never reached RUNNING (state %v)", tk.State)
		}
		time.Sleep(2 * time.Millisecond)
	}

	if err := fabric.Create(&taskfabric.Task{ID: "high", Capability: "code", Priority: 10}); err != nil {
		t.Fatalf("create high: %v", err)
	}

	preemptDeadline := time.Now().Add(2 * time.Second)
	for {
		tk, err := fabric.Task("low")
		if err == nil && tk.PreemptRequested {
			return // preemption reached the running task
		}
		if time.Now().After(preemptDeadline) {
			t.Fatalf("a high-priority task was waiting and the only agent was busy with lower " +
				"priority work, but preemption was never requested: the entry guard returned early")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
