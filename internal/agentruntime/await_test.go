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
	require.True(t, errors.Is(aw.Err, ErrSessionStalled), "errors.Is must match the sentinel")
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
