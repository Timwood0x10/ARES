package agentruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// awaitTestFabric builds a fabric with one session task created/driven
// through the real lease protocol.
func awaitTestFabric(t *testing.T) *taskfabric.Fabric {
	t.Helper()
	return taskfabric.NewFabric()
}

// mkAwaitTask creates a task and optionally drives it to COMPLETED/FAILED.
func mkAwaitTask(t *testing.T, fabric *taskfabric.Fabric, id string, state string) {
	t.Helper()
	if err := fabric.Create(&taskfabric.Task{
		ID:          id,
		Capability:  "ares/answer",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
	}); err != nil {
		t.Fatalf("Create %s: %v", id, err)
	}
	if state == "" {
		return
	}
	epoch, err := fabric.Acquire(id, "agt", time.Minute)
	require.NoError(t, err)
	require.NoError(t, fabric.Start(id, "agt", epoch))
	if state == "completed" {
		require.NoError(t, fabric.Complete(id, "agt", epoch))
	}
	if state == "failed" {
		require.NoError(t, fabric.Fail(id, "agt", epoch, nil))
	}
}

// TestAwaitSessionResult_AnswerlessTerminal pins the stall path for a
// terminal session with a bodyless answer task: the answer scan skips a
// completed answer whose checkpoint carries no body, so the loop falls
// through to the stall verdict (sustained + race-guard re-checked).
func TestAwaitSessionResult_AnswerlessTerminal(t *testing.T) {
	t.Parallel()
	fabric := awaitTestFabric(t)
	mkAwaitTask(t, fabric, "plan-1", "completed")
	mkAwaitTask(t, fabric, "sess/s1/d1/t#s/answer#0", "completed")

	aw := AwaitSessionResult(context.Background(), fabric, "s1", "plan-1", AwaitOptions{PollInterval: time.Millisecond})
	require.Error(t, aw.Err, "a bodyless completed answer is not an answer — the session stalls")
	require.True(t, aw.Stalled)
	var se *StalledError
	require.True(t, errors.As(aw.Err, &se))
	require.Empty(t, se.FailedTasks, "everything completed — no failed-task diagnostics")
}

// TestAwaitSessionResult_PlanFailed pins the fast-fail path: a FAILED plan
// root returns PlanFailed immediately instead of spinning the wait budget.
func TestAwaitSessionResult_PlanFailed(t *testing.T) {
	t.Parallel()
	fabric := awaitTestFabric(t)
	mkAwaitTask(t, fabric, "plan-1", "failed")

	start := time.Now()
	aw := AwaitSessionResult(context.Background(), fabric, "s1", "plan-1", AwaitOptions{PollInterval: time.Millisecond})
	require.Error(t, aw.Err)
	require.True(t, aw.PlanFailed)
	require.Less(t, time.Since(start), DefaultAwaitWait, "plan failure must fail fast, not spin the deadline")
}

// TestAwaitSessionResult_StalledDiagnostics pins the E5 contract: a sustained
// stall over FAILED session tasks returns *StalledError carrying the failed
// task IDs — the caller can distinguish "no answer AND failed tasks" from
// "no answer AND everything completed".
func TestAwaitSessionResult_StalledDiagnostics(t *testing.T) {
	t.Parallel()
	fabric := awaitTestFabric(t)
	mkAwaitTask(t, fabric, "plan-1", "completed")
	mkAwaitTask(t, fabric, "sess/s1/d1/tool#0", "failed")
	mkAwaitTask(t, fabric, "sess/s1/d1/tool#1", "completed")

	aw := AwaitSessionResult(context.Background(), fabric, "s1", "plan-1", AwaitOptions{PollInterval: time.Millisecond})
	require.Error(t, aw.Err)
	require.True(t, aw.Stalled)

	var se *StalledError
	require.True(t, errors.As(aw.Err, &se), "stall must surface as *StalledError")
	require.Equal(t, "s1", se.SessionID)
	require.Len(t, se.FailedTasks, 1)
	require.Equal(t, "sess/s1/d1/tool#0", se.FailedTasks[0].TaskID)
	require.Equal(t, "ares/answer", se.FailedTasks[0].Capability, "E5: FailedTask carries the capability")
	require.True(t, errors.Is(aw.Err, ErrSessionStalled), "errors.Is must match the sentinel")
}

// TestAwaitSessionResult_FailedTaskQuantum pins the H3 contract: the Quantum
// surfaced in FailedTask is the 1-based execution round the task failed in.
// RunQuantum counts the quantum BEFORE running the step, so a failure on the
// task's first quantum must report 1 (never 0 — 0 is reserved for a task that
// never entered a quantum, e.g. a dependency cascade).
func TestAwaitSessionResult_FailedTaskQuantum(t *testing.T) {
	t.Parallel()
	fabric := awaitTestFabric(t)
	mkAwaitTask(t, fabric, "plan-1", "completed")

	const failedID = "sess/s1/d1/tool#0"
	require.NoError(t, fabric.Create(&taskfabric.Task{
		ID:          failedID,
		Capability:  "ares/answer",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
	}))
	epoch, err := fabric.Acquire(failedID, "agt", time.Minute)
	require.NoError(t, err)
	// RunQuantum applies the FAIL transition and then returns the step error.
	require.Error(t, fabric.RunQuantum(failedID, "agt", epoch, func() (any, bool, error) {
		return nil, false, errors.New("step boom")
	}))

	aw := AwaitSessionResult(context.Background(), fabric, "s1", "plan-1", AwaitOptions{PollInterval: time.Millisecond})
	require.Error(t, aw.Err)
	var se *StalledError
	require.True(t, errors.As(aw.Err, &se), "stall must surface as *StalledError")
	require.Len(t, se.FailedTasks, 1)
	require.Equal(t, failedID, se.FailedTasks[0].TaskID)
	require.Equal(t, 1, se.FailedTasks[0].Quantum, "first-quantum failure is quantum 1, not 0")
}

// TestTaskResolved_FailedKeepsSessionID pins the H1 contract: a FAILED
// session-scoped task resolves AND still reports its session ID, so the caller
// can locate the failure diagnostics for the right session.
func TestTaskResolved_FailedKeepsSessionID(t *testing.T) {
	t.Parallel()
	fabric := awaitTestFabric(t)
	require.NoError(t, fabric.Create(&taskfabric.Task{
		ID:          "plan-sess",
		Capability:  "ares/answer",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
		// A session-scoped task carries its SessionID in the checkpoint; Fail
		// with a nil cause preserves it (see checkpointWithCause).
		Checkpoint: taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{SessionID: "s2"}),
	}))
	epoch, err := fabric.Acquire("plan-sess", "agt", time.Minute)
	require.NoError(t, err)
	require.NoError(t, fabric.Start("plan-sess", "agt", epoch))
	require.NoError(t, fabric.Fail("plan-sess", "agt", epoch, nil))

	tk, err := fabric.Task("plan-sess")
	require.NoError(t, err)
	require.Equal(t, taskfabric.StateFailed, tk.State)

	resolved, sid := TaskResolved(fabric, tk, &StallDetector{})
	require.True(t, resolved, "failed task resolves")
	require.Equal(t, "s2", sid, "H1: a failed session-scoped task still reports its session ID")
}

// TestAwaitSessionResult_SiblingBoundary pins the prefix-boundary contract:
// tasks of a sibling session (s10) must never resolve s1's wait.
func TestAwaitSessionResult_SiblingBoundary(t *testing.T) {
	t.Parallel()
	fabric := awaitTestFabric(t)
	mkAwaitTask(t, fabric, "sess/s10/d1/t#s/answer#0", "completed")

	aw := AwaitSessionResult(context.Background(), fabric, "s1", "plan-1", AwaitOptions{
		PollInterval: time.Millisecond,
		Wait:         30 * time.Millisecond,
	})
	require.Error(t, aw.Err, "sibling session's answer must not resolve s1")
	require.False(t, aw.Stalled, "budget expiry is not a stall verdict")
}

// TestTaskResolved pins the shared per-poll resolution invariant (E6): the
// answer scan runs first, then the stall verdict — the ordering the serve
// read path used to duplicate in resultResolved.
func TestTaskResolved(t *testing.T) {
	t.Parallel()

	// Non-session task: terminal states resolve immediately.
	fabric := awaitTestFabric(t)
	mkAwaitTask(t, fabric, "plain-1", "completed")
	tk, _ := fabric.Task("plain-1")
	resolved, sid := TaskResolved(fabric, tk, &StallDetector{})
	require.True(t, resolved, "non-session completed task resolves")
	require.Empty(t, sid, "non-session task has no session ID")

	mkAwaitTask(t, fabric, "plain-2", "failed")
	tk, _ = fabric.Task("plain-2")
	resolved, _ = TaskResolved(fabric, tk, &StallDetector{})
	require.True(t, resolved, "non-session failed task resolves")

	// Non-session pending task does not resolve.
	mkAwaitTask(t, fabric, "plain-3", "")
	tk, _ = fabric.Task("plain-3")
	resolved, _ = TaskResolved(fabric, tk, &StallDetector{})
	require.False(t, resolved, "pending task does not resolve")

	// Session-scoped completed task with no answer does not resolve (answer
	// has not landed yet) — this is the invariant the serve read path relies
	// on: COMPLETED alone does not mean the answer is readable.
	fabric2 := awaitTestFabric(t)
	// Create + start the plan task, then Complete with a session-scoped
	// checkpoint so TaskResolved sees the SessionID.
	require.NoError(t, fabric2.Create(&taskfabric.Task{
		ID:          "plan-sess",
		Capability:  "ares/answer",
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
	}))
	epoch, err := fabric2.Acquire("plan-sess", "agt", time.Minute)
	require.NoError(t, err)
	require.NoError(t, fabric2.Start("plan-sess", "agt", epoch))
	require.NoError(t, fabric2.CompleteWithCheckpoint("plan-sess", "agt", epoch,
		taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{SessionID: "s2"})))
	// A completed answer task with no body → SessionAnswer returns false.
	mkAwaitTask(t, fabric2, "sess/s2/d1/t#s/answer#0", "completed")
	tk, _ = fabric2.Task("plan-sess")
	resolved, sid = TaskResolved(fabric2, tk, &StallDetector{})
	require.False(t, resolved, "bodyless answer → not resolved in a single poll")
	require.Equal(t, "s2", sid, "session ID extracted from checkpoint")
}

// TestTaskResolved_NilFabricOrTask pins the nil-input guards: a nil fabric or
// task is never resolvable. A real detector is passed because the
// session-scoped path requires one (passing nil there is a programming error,
// not a guarded input).
func TestTaskResolved_NilFabricOrTask(t *testing.T) {
	t.Parallel()
	resolved, sid := TaskResolved(nil, nil, &StallDetector{})
	require.False(t, resolved)
	require.Empty(t, sid)
}

// TestSessionAnswerAndFailed pin the shared scan helpers' boundary behavior
// directly (the contract the three former loops each implemented by hand).
func TestSessionAnswerAndFailed(t *testing.T) {
	t.Parallel()
	fabric := awaitTestFabric(t)

	_, ok := SessionAnswer(fabric, "s1")
	require.False(t, ok, "empty fabric: no answer")
	require.False(t, SessionAnswerFailed(fabric, "s1"))

	mkAwaitTask(t, fabric, "sess/s1/d1/t#s/answer#0", "failed")
	_, ok = SessionAnswer(fabric, "s1")
	require.False(t, ok, "a FAILED answer is not an answer")
	require.True(t, SessionAnswerFailed(fabric, "s1"))

	// nil-fabric / empty-session guards.
	_, ok = SessionAnswer(nil, "s1")
	require.False(t, ok)
	require.False(t, SessionAnswerFailed(nil, ""))
}
