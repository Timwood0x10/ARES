package sdk

import (
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// completedTask drives a task through Create → Acquire → Start → Complete so
// it sits in COMPLETED the way a settled turn's nodes do. Create alone is not
// enough: it always inserts a task as READY.
func completedTask(t *testing.T, f *taskfabric.Fabric, id string) {
	t.Helper()
	if err := f.Create(&taskfabric.Task{ID: id, Capability: "ares/plan"}); err != nil {
		t.Fatalf("Create %s: %v", id, err)
	}
	driveToCompleted(t, f, id)
}

// driveToCompleted moves an ALREADY-created task to COMPLETED.
func driveToCompleted(t *testing.T, f *taskfabric.Fabric, id string) {
	t.Helper()
	epoch, err := f.Acquire(id, "agent-test", time.Minute)
	if err != nil {
		t.Fatalf("Acquire %s: %v", id, err)
	}
	if err := f.Start(id, "agent-test", epoch); err != nil {
		t.Fatalf("Start %s: %v", id, err)
	}
	if err := f.Complete(id, "agent-test", epoch); err != nil {
		t.Fatalf("Complete %s: %v", id, err)
	}
}

// TestL2StallDetector_RequiresSustainedEvidence pins the false-stall fix.
//
// The planner grows the answer node through the SAME asynchronous compile
// pipeline as a tool node (planner_cognition.growAnswerNode → L2Graph.
// AddToolNode → GraphEvent → CompileCoordinator → fabric.CompileNode). So
// there is a real window in which the plan quantum has already returned Done
// (plan task COMPLETED) while the grown answer task does not exist in the
// fabric yet.
//
// In that window SessionStalled sees: plan terminal, every remaining session
// task terminal, at least one session task present — and declares the
// session stalled, even though the answer node is a few milliseconds from
// being compiled. The wait loop then returned "stalled — all tasks
// terminal, no answer" for a session that was still making progress, which
// is what made TestSubmit_SessionContinuation fail intermittently under
// load (the extra scheduling delay widened the compile window past one
// poll).
//
// The detector therefore requires the condition to hold across consecutive
// polls: a transient compile gap resets it, a genuinely dead session still
// reports within a few polls.
func TestL2StallDetector_RequiresSustainedEvidence(t *testing.T) {
	const sessionID = "chat-42"
	const planTaskID = "peer-plan-2"
	prefix := agentfabric.SessionTaskPrefix(sessionID)

	f := taskfabric.NewFabric()
	// Turn 1: fully settled.
	for _, id := range []string{prefix + "root", prefix + "1/answer#0"} {
		completedTask(t, f, id)
	}
	// Turn 2: the plan quantum finished, the grown answer is not compiled yet.
	completedTask(t, f, planTaskID)

	// Premise: the shared predicate really does call this a stall. If this
	// ever stops being true the rest of the test is meaningless.
	if !agentruntime.SessionStalled(f, sessionID, planTaskID) {
		t.Fatal("premise: SessionStalled must report true in the compile-gap window")
	}

	d := &agentruntime.StallDetector{}
	if d.Stalled(f, sessionID, planTaskID) {
		t.Fatal("a single stall observation must not be enough — the grown answer node may simply not be compiled yet")
	}
	if d.Stalled(f, sessionID, planTaskID) {
		t.Fatal("two observations must not be enough either; the confirmation window must be wider than one poll gap")
	}

	// The compile lands: a non-terminal task appears, so the condition is no
	// longer even pending — the detector must fully reset.
	grownID := prefix + "2/answer#0"
	if err := f.Create(&taskfabric.Task{ID: grownID, Capability: "ares/answer"}); err != nil {
		t.Fatalf("Create grown answer: %v", err)
	}
	if d.Stalled(f, sessionID, planTaskID) {
		t.Fatal("a live grown node must clear the stall verdict")
	}

	// Drive that grown node to completion so the session is stalled again,
	// and reuse the SAME detector: had the earlier evidence survived the
	// reset, it would fire before the full window elapsed.
	driveToCompleted(t, f, grownID)
	firedEarly := false
	for i := 0; i < agentruntime.StallConfirmations-1; i++ {
		if d.Stalled(f, sessionID, planTaskID) {
			firedEarly = true
			break
		}
	}
	if firedEarly {
		t.Fatal("evidence must restart from zero after a cleared condition, not resume from the pre-reset count")
	}
	if !d.Stalled(f, sessionID, planTaskID) {
		t.Fatalf("a sustained stall must be reported within %d polls", agentruntime.StallConfirmations)
	}

	// Rebuild the stalled shape on a fresh fabric and confirm the detector
	// fires exactly at the window edge.
	f2 := taskfabric.NewFabric()
	for _, id := range []string{prefix + "root", prefix + "1/answer#0", planTaskID} {
		completedTask(t, f2, id)
	}
	d2 := &agentruntime.StallDetector{}
	fired := false
	for i := 0; i < agentruntime.StallConfirmations; i++ {
		if d2.Stalled(f2, sessionID, planTaskID) {
			fired = true
			if i != agentruntime.StallConfirmations-1 {
				t.Fatalf("detector fired after %d observations, want exactly %d", i+1, agentruntime.StallConfirmations)
			}
		}
	}
	if !fired {
		t.Fatalf("a genuinely stalled session must be reported within %d polls", agentruntime.StallConfirmations)
	}
}

// TestL2StallDetector_ResetOnLiveWork locks the reset semantics: any single
// poll that sees non-terminal work wipes the accumulated evidence, so
// on-again-off-again progress never builds up to a false stall.
func TestL2StallDetector_ResetOnLiveWork(t *testing.T) {
	const sessionID = "chat-42"
	const planTaskID = "peer-plan-2"
	prefix := agentfabric.SessionTaskPrefix(sessionID)

	f := taskfabric.NewFabric()
	for _, id := range []string{prefix + "root", planTaskID} {
		completedTask(t, f, id)
	}

	d := &agentruntime.StallDetector{}
	// Accumulate evidence up to (but not including) the threshold.
	for i := 0; i < agentruntime.StallConfirmations-1; i++ {
		if d.Stalled(f, sessionID, planTaskID) {
			t.Fatalf("must not fire at observation %d", i+1)
		}
	}
	// One poll of live work must wipe it.
	if err := f.Create(&taskfabric.Task{ID: prefix + "3/tool#0", Capability: "tool/grep"}); err != nil {
		t.Fatalf("Create live tool: %v", err)
	}
	if d.Stalled(f, sessionID, planTaskID) {
		t.Fatal("live work must not be reported as a stall")
	}
	// And the evidence starts over: the threshold must be reached again
	// from zero, not resumed from the pre-reset count. The task already
	// exists (created above), so drive it to COMPLETED instead of
	// re-creating it.
	driveToCompleted(t, f, prefix+"3/tool#0")
	for i := 0; i < agentruntime.StallConfirmations-1; i++ {
		if d.Stalled(f, sessionID, planTaskID) {
			t.Fatalf("evidence must restart from zero; fired early at %d", i+1)
		}
	}
	if !d.Stalled(f, sessionID, planTaskID) {
		t.Fatal("sustained stall must eventually be reported")
	}
}

// TestL2StallDetector_ConfirmationWindowIsBoundedByPollCadence documents the
// chosen trade-off: the extra latency a genuinely stalled caller pays before
// hearing about it. It is a few polls, not a new timeout.
func TestL2StallDetector_ConfirmationWindowIsBoundedByPollCadence(t *testing.T) {
	if agentruntime.StallConfirmations < 2 {
		t.Fatalf("confirmations must be > 1 to absorb a compile gap, got %d", agentruntime.StallConfirmations)
	}
	window := time.Duration(agentruntime.StallConfirmations) * l2PollInterval
	if window > 500*time.Millisecond {
		t.Fatalf("stall confirmation window %v is too long to sit inside a caller's wait budget", window)
	}
}
