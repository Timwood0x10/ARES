package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// agentBlockingCognition reports WHICH agent took the task, then blocks until its own release.
// Two of these give a test control over which quantum finishes first, which is what makes an
// overlapping-exit fixture deterministic instead of a race.
type agentBlockingCognition struct {
	agent   string
	started chan string
	release chan struct{}
}

func (c *agentBlockingCognition) ExecuteStep(ctx context.Context, task *models.Task) (*agentfabric.StepOutcome, error) {
	select {
	case c.started <- c.agent:
	default:
	}
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess(nil, "done")
	return &agentfabric.StepOutcome{Done: true, Result: res}, nil
}

// TestScheduler_BusySinceSurvivesPartialRelease pins the clock the work-conserving gap measures
// from, in the case the simple fixtures miss: quanta overlap, and the first to finish is not the
// one that started first.
//
// The field is not "start of the oldest running quantum" — it is "since when has the pipeline been
// continuously saturated". After one of two overlapping quanta exits, the pipeline is still busy,
// so the clock must keep running from the original start. Clearing it on a partial exit would make
// a permanently saturated scheduler look freshly busy on every completion, and the gap histogram
// would then report the duration of the LAST quantum instead of the length of the saturation.
func TestScheduler_BusySinceSurvivesPartialRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // ExecuteStep returns on ctx.Done, so cleanup does not need to close the releases

	fabric := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()
	started := make(chan string, 4)
	releases := map[string]chan struct{}{}
	for _, id := range []string{"a", "b"} {
		cog := &agentBlockingCognition{agent: id, started: started, release: make(chan struct{})}
		releases[id] = cog.release
		if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
			Identity:         id,
			Capabilities:     []string{"code"},
			CognitionFactory: func([]string) agentfabric.Cognition { return cog },
		}); err != nil {
			t.Fatalf("spawn %s: %v", id, err)
		}
	}

	sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
	sched.PollInterval = 5 * time.Millisecond
	sched.WithAgentFabric(agents)
	sched.WithMaxConcurrent(2)
	go sched.Run(ctx)

	if err := fabric.Create(&taskfabric.Task{ID: "t1", Capability: "code"}); err != nil {
		t.Fatalf("create t1: %v", err)
	}
	first := awaitStarted(t, started)

	// Captured after the FIRST quantum is in flight. Taking it later (after the second starts)
	// would not distinguish "set once on 0->1" from "set on every admission".
	sched.quantumMu.Lock()
	busyAt := sched.busySince
	inflight := sched.inflightQuanta
	sched.quantumMu.Unlock()
	if inflight != 1 {
		t.Fatalf("expected one quantum in flight, got %d", inflight)
	}
	if busyAt.IsZero() {
		t.Fatal("busySince was never set when the first quantum started")
	}

	if err := fabric.Create(&taskfabric.Task{ID: "t2", Capability: "code"}); err != nil {
		t.Fatalf("create t2: %v", err)
	}
	second := awaitStarted(t, started)
	if first == second {
		t.Fatalf("both tasks ran on the same agent (%s): the fixture needs two", first)
	}

	sched.quantumMu.Lock()
	secondInflight := sched.inflightQuanta
	afterSecondAdmit := sched.busySince
	sched.quantumMu.Unlock()
	if secondInflight != 2 {
		t.Fatalf("expected two quanta in flight, got %d", secondInflight)
	}
	if !afterSecondAdmit.Equal(busyAt) {
		t.Fatalf("busySince moved when a SECOND quantum was admitted (%v -> %v): the clock measures "+
			"the continuous non-empty interval, not each admission", busyAt, afterSecondAdmit)
	}

	// Let the FIRST agent finish while the second is still running: inflight goes 2 -> 1 and never
	// reaches zero.
	close(releases[first])
	waitForInflight(t, sched, 1)

	sched.quantumMu.Lock()
	afterPartial := sched.busySince
	sched.quantumMu.Unlock()
	if !afterPartial.Equal(busyAt) {
		t.Fatalf("busySince moved on a partial exit (%v -> %v): the pipeline never went idle, "+
			"so the saturation clock must keep running", busyAt, afterPartial)
	}

	// Now the second finishes: 1 -> 0 clears it.
	close(releases[second])
	waitForInflight(t, sched, 0)

	sched.quantumMu.Lock()
	afterIdle := sched.busySince
	sched.quantumMu.Unlock()
	if !afterIdle.IsZero() {
		t.Fatalf("busySince must be cleared when the pipeline goes idle, got %v", afterIdle)
	}
}

// waitForInflight polls the counter the saturation clock is tied to.
func waitForInflight(t *testing.T, s *Scheduler, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.quantumMu.Lock()
		got := s.inflightQuanta
		s.quantumMu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("inflight stayed at %d, want %d", got, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
