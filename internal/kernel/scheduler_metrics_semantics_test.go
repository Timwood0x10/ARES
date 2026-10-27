package kernel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestScheduler_DispatchSampleRequiresAClaim is the first acceptance item for the metrics
// calibration: a reserved admission slot is not a dispatch.
//
// The positive half matters as much as the negative one — an earlier version of these tests only
// asserted that a deferred queue produced MORE age samples than dispatch samples, which stays true
// when the production dispatch sampling is deleted entirely.
func TestScheduler_DispatchSampleRequiresAClaim(t *testing.T) {
	t.Run("a claimed task is sampled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		fabric := taskfabric.NewFabric()
		agents := agentfabric.NewFabric()
		if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
			Identity:         "worker",
			Capabilities:     []string{"code"},
			CognitionFactory: func([]string) agentfabric.Cognition { return &countingCognition{} },
		}); err != nil {
			t.Fatalf("spawn: %v", err)
		}

		rec := &recordingSchedulerMetrics{}
		sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
		sched.PollInterval = 5 * time.Millisecond
		sched.WithAgentFabric(agents)
		sched.WithSchedulerMetrics(rec)
		go sched.Run(ctx)

		if err := fabric.Create(&taskfabric.Task{ID: "runnable", Capability: "code"}); err != nil {
			t.Fatalf("create: %v", err)
		}
		waitForTaskState(t, fabric, "runnable", taskfabric.StateCompleted, 5*time.Second)

		dispatched, _, _ := rec.snapshot()
		if dispatched != 1 {
			t.Fatalf("a task that ran recorded %d dispatch samples, want exactly 1", dispatched)
		}
	})

	t.Run("a task that finds no agent is not sampled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		agents := agentfabric.NewFabric()
		if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
			Identity:         "code-only",
			Capabilities:     []string{"code"},
			CognitionFactory: func([]string) agentfabric.Cognition { return &countingCognition{} },
		}); err != nil {
			t.Fatalf("spawn: %v", err)
		}

		rec := &recordingSchedulerMetrics{}
		sched := New(taskfabric.NewFabric(), map[string]CapabilityExecutor{}, NewLoadTracker())
		sched.PollInterval = 5 * time.Millisecond
		sched.WithAgentFabric(agents)
		sched.WithSchedulerMetrics(rec)

		if err := sched.fabric.Create(&taskfabric.Task{ID: "unrunnable", Capability: "other"}); err != nil {
			t.Fatalf("create: %v", err)
		}

		sched.safeDrain(ctx)
		time.Sleep(100 * time.Millisecond) // let any (wrongly) spawned quantum report

		dispatched, _, _ := rec.snapshot()
		if dispatched != 0 {
			t.Fatalf("a task that never found an agent recorded %d dispatch samples", dispatched)
		}
	})
}

// TestScheduler_DeferredHeadContributesQueueAge is the second acceptance item: work that never gets
// dispatched must still be visible in the queue-age signal.
//
// The assertion is now two-sided. "ages > dispatched" alone was satisfied by the running task's own
// departure sample, so deleting the deferred-head sampling (or the dispatch sampling) left it
// true. Here the running task must contribute exactly one dispatch sample, and the queued task must
// add exactly one age sample on top of that.
func TestScheduler_DeferredHeadContributesQueueAge(t *testing.T) {
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

	rec := &recordingSchedulerMetrics{}
	sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
	sched.PollInterval = 5 * time.Millisecond
	sched.WithAgentFabric(agents)
	sched.WithSchedulerMetrics(rec)
	sched.WithMaxConcurrent(1)
	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{ID: "running", Capability: "code"}); err != nil {
		t.Fatalf("create running: %v", err)
	}
	select {
	case <-cog.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first task never started")
	}

	dispatchedBefore, agesBefore, _ := rec.snapshot()
	if dispatchedBefore != 1 || agesBefore != 1 {
		t.Fatalf("after the first claim want 1 dispatch + 1 age sample, got %d/%d",
			dispatchedBefore, agesBefore)
	}

	if err := fabric.Create(&taskfabric.Task{ID: "queued", Capability: "code"}); err != nil {
		t.Fatalf("create queued: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		dispatched, ages, _ := rec.snapshot()
		if dispatched == 1 && ages == 2 {
			return // the queued task added exactly one age sample and no dispatch sample
		}
		if time.Now().After(deadline) {
			t.Fatalf("want 1 dispatch + 2 age samples (head sampled once), got %d/%d",
				dispatched, ages)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// yieldOnceCognition spends measurable time in its first quantum, then yields; the second quantum
// finishes at once.
type yieldOnceCognition struct {
	attempts atomic.Int32
	work     time.Duration
}

func (c *yieldOnceCognition) ExecuteStep(ctx context.Context, task *models.Task) (*agentfabric.StepOutcome, error) {
	if c.attempts.Add(1) == 1 {
		select {
		case <-time.After(c.work):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &agentfabric.StepOutcome{Done: false, Checkpoint: "half-done"}, nil
	}
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess(nil, "done")
	return &agentfabric.StepOutcome{Done: true, Result: res}, nil
}

// TestScheduler_DispatchWaitExcludesThePreviousQuantum pins the difference between a task's age and
// one attempt's queueing delay.
//
// Yielding does not reset ReadyAt (fairness ages the task, and a yielding workload must not look
// perpetually fresh), so measuring dispatch latency from ReadyAt charged the second attempt for the
// first quantum's execution: a 150ms quantum followed by an immediate resume reported ~150ms of
// waiting for a quantum that waited under a millisecond.
func TestScheduler_DispatchWaitExcludesThePreviousQuantum(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()
	cog := &yieldOnceCognition{work: 150 * time.Millisecond}
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:         "yielder",
		Capabilities:     []string{"code"},
		CognitionFactory: func([]string) agentfabric.Cognition { return cog },
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	rec := &recordingSchedulerMetrics{}
	sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
	sched.PollInterval = 5 * time.Millisecond
	sched.WithAgentFabric(agents)
	sched.WithSchedulerMetrics(rec)
	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{ID: "yielder-task", Capability: "code"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	waitForTaskState(t, fabric, "yielder-task", taskfabric.StateCompleted, 10*time.Second)

	samples := rec.dispatchSamples()
	if len(samples) != 2 {
		t.Fatalf("want one dispatch sample per attempt, got %d (%v)", len(samples), samples)
	}
	if samples[1] >= 50*time.Millisecond.Seconds() {
		t.Fatalf("the resumed attempt reported %.1fms of waiting; it was queued for under a "+
			"millisecond and the rest is the previous quantum's execution",
			samples[1]*1000)
	}
}
