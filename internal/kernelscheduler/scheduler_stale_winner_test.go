package kernelscheduler

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// stubExecutor is a minimal CapabilityExecutor for testing.
type stubExecutor struct {
	id  string
	typ models.AgentType
}

func (e *stubExecutor) ID() string             { return e.id }
func (e *stubExecutor) Type() models.AgentType { return e.typ }
func (e *stubExecutor) ExecuteStep(_ context.Context, _ *models.Task) (*sub.StepOutcome, error) {
	return &sub.StepOutcome{Done: true}, nil
}

// TestStaleWinnerReleasedWhenReplacementExists verifies that when a stale
// winner (selected by Schedule but no longer executable) has another capable
// executor available, the scheduler releases the task so the next drain
// re-schedules it within one poll interval (EDGE-4: 5-minute stall).
//
// Before the fix the task stays LEASED for the full TTL (5 min); after the
// fix it is released to READY when another capable executor exists.
func TestStaleWinnerReleasedWhenReplacementExists(t *testing.T) {
	ctx := context.Background()
	fab := taskfabric.NewFabric()
	// "live" executor is capable of the task's capability.
	execs := map[string]CapabilityExecutor{
		"live": &stubExecutor{id: "live", typ: "code"},
	}
	sched := New(fab, execs, nil)
	sched.ttl = time.Minute // short enough for test, not used in stale path

	if err := fab.Create(&taskfabric.Task{
		ID:          "t1",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0}, // no retries
	}); err != nil {
		t.Fatalf("Create task: %v", err)
	}

	// Hand-craft a candidate list with a "ghost" winner that does not exist
	// in the executor registry. Schedule picks it (only candidate), then
	// executor lookup fails → stale-winner path.
	cands := []taskfabric.Candidate{
		{AgentID: "ghost", Capabilities: []string{"code"}, Confidence: 1.0},
	}
	if err := sched.executeWithCandidates(ctx, "t1", cands); err != nil {
		// stale-winner path returns nil; any other error is unexpected.
		t.Fatalf("executeWithCandidates: %v", err)
	}

	tk, err := fab.Task("t1")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	// After fix: another capable executor ("live") exists → task released to READY.
	if tk.State != taskfabric.StateReady {
		t.Fatalf("expected task to be READY (released for re-schedule), got %s", tk.State)
	}
}

// TestStaleWinnerKeepsLeasedWhenNoReplacement verifies that when a stale
// winner has NO other capable executor, the scheduler keeps the lease so the
// recovery loop's lease-expiry path can requeue it (E1: death → lease expiry
// → replacement executor resumes checkpoint).
func TestStaleWinnerKeepsLeasedWhenNoReplacement(t *testing.T) {
	ctx := context.Background()
	fab := taskfabric.NewFabric()
	// No capable executors registered.
	sched := New(fab, nil, nil)
	sched.ttl = time.Minute

	if err := fab.Create(&taskfabric.Task{
		ID:          "t1",
		Capability:  "code",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
	}); err != nil {
		t.Fatalf("Create task: %v", err)
	}

	cands := []taskfabric.Candidate{
		{AgentID: "ghost", Capabilities: []string{"code"}, Confidence: 1.0},
	}
	if err := sched.executeWithCandidates(ctx, "t1", cands); err != nil {
		t.Fatalf("executeWithCandidates: %v", err)
	}

	tk, err := fab.Task("t1")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	// No capable replacement → task stays LEASED for recovery.
	if tk.State != taskfabric.StateLeased {
		t.Fatalf("expected task to stay LEASED (no replacement available), got %s", tk.State)
	}
}
