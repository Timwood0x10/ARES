package agentruntime

// await.go is the shared session-wait primitive (E6): the "wait for a
// session's terminal answer or a confirmed stall" loop lived as three
// independently-evolving copies (sdk/l2_submit.go, cmd/ares's
// executeAskViaSession, cmd/ares's waitForTaskResult) with drifted poll
// intervals, copy-pasted answer-decode logic, and bare "sess/" literals
// that bypassed agentfabric.SessionTaskPrefix. This file collapses the
// shared invariants into one place:
//   - the answer scan runs FIRST, before any stall verdict;
//   - a stall verdict is race-guarded by one re-check of the answer scan
//     (the answer can land between the two reads);
//   - sustained evidence (StallDetector) separates a live compile gap from
//     a dead session;
//   - the terminal answer body reads through the single decode path.
//
// Entry points keep their own poll cadence and their own release semantics —
// only the invariants are shared here.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// DefaultAwaitPollInterval is the shared poll cadence for session wait
// loops. The serve read path may override it (AwaitOptions.PollInterval) to
// keep its documented 202-degrade latency budget.
const DefaultAwaitPollInterval = 20 * time.Millisecond

// DefaultAwaitWait bounds a wait whose context carries neither a deadline
// nor an explicit task timeout (SDK and serve collab parity: 10 minutes).
const DefaultAwaitWait = 10 * time.Minute

// ErrSessionStalled is returned by AwaitSessionResult when the session's
// tasks are all terminal with no answer (sustained across
// StallConfirmations polls and race-guard re-checked). Errors.As unwraps to
// it; StalledError carries the failure diagnostics (E5).
var ErrSessionStalled = errors.New("session stalled — all tasks terminal, no answer")

// StalledError is the diagnostic-bearing stall failure (E5): it lets the
// caller distinguish "no answer AND failed session tasks" from "no answer
// AND everything completed" — the precondition for any future automatic
// retry/re-plan/re-agent decision, which 0.3.2 deliberately does NOT make.
type StalledError struct {
	// SessionID is the stalled session.
	SessionID string
	// FailedTasks lists the session's FAILED task IDs with their recorded
	// causes (empty when every task completed and the answer never grew).
	FailedTasks []FailedTask
}

// FailedTask names one FAILED session task and why it failed.
type FailedTask struct {
	// TaskID is the fabric task ID ("sess/<sid>/dN/tool#M" or the plan root).
	TaskID string
	// Cause is the persisted terminal failure cause; empty when the task
	// failed without a recorded reason (agent death, cascade provenance —
	// see Task.FailedDependency).
	Cause string
	// FailedDependency names the prerequisite whose failure cascaded into
	// this task (empty when the task failed on its own).
	FailedDependency string
}

// Error implements error with the caller-visible message shape the three
// former wait loops used ("all tasks terminal, no answer").
func (e *StalledError) Error() string {
	if len(e.FailedTasks) == 0 {
		return fmt.Sprintf("session %s: %v", e.SessionID, ErrSessionStalled)
	}
	return fmt.Sprintf("session %s: %v (%d failed task(s), first: %s)",
		e.SessionID, ErrSessionStalled, len(e.FailedTasks), e.FailedTasks[0].TaskID)
}

// Unwrap exposes the sentinel so errors.Is(err, ErrSessionStalled) works.
func (e *StalledError) Unwrap() error { return ErrSessionStalled }

// SessionAnswer returns the first COMPLETED answer task's content for a
// session. It scans fabric task ids (sess/<sid>/…/answer#…) rather than the
// session graph: the answer body releases its session on success, so the
// registry entry is already gone by the time the poll loop looks. The
// SessionTaskPrefix boundary match keeps sibling sessions (sess-auto-1 vs
// sess-auto-12) from shadowing each other.
//
// Returns ok=false when no completed answer task exists yet. An answer task
// whose body is unreadable is skipped (it cannot be the terminal answer).
func SessionAnswer(fabric *taskfabric.Fabric, sessionID string) (string, bool) {
	if fabric == nil || sessionID == "" {
		return "", false
	}
	prefix := agentfabric.SessionTaskPrefix(sessionID)
	for _, id := range fabric.IDs() {
		if !strings.HasPrefix(id, prefix) || !strings.Contains(id, "/answer#") {
			continue
		}
		tk, err := fabric.Task(id)
		if err != nil || tk.State != taskfabric.StateCompleted {
			continue
		}
		content, err := SessionAnswerContent(tk)
		if err != nil || content == "" {
			continue
		}
		return content, true
	}
	return "", false
}

// SessionAnswerFailed reports whether any answer task of the session is
// terminally FAILED — the session's sole exit is closed, so the wait loop
// can give up immediately instead of spinning to its deadline. Prior-turn
// answers cannot false-trigger this: Sessions.Admit harvests a re-admitted
// session's terminal tasks before this turn's plan can grow an answer.
func SessionAnswerFailed(fabric *taskfabric.Fabric, sessionID string) bool {
	if fabric == nil || sessionID == "" {
		return false
	}
	prefix := agentfabric.SessionTaskPrefix(sessionID)
	for _, id := range fabric.IDs() {
		if !strings.HasPrefix(id, prefix) || !strings.Contains(id, "/answer#") {
			continue
		}
		if tk, err := fabric.Task(id); err == nil && tk.State == taskfabric.StateFailed {
			return true
		}
	}
	return false
}

// SessionAnswerContent reads the terminal answer body from its completion
// checkpoint (the shared decode path: items[0].Content of the step
// checkpoint). The in-memory fabric keeps the concrete
// []*models.RecommendItem type, so no serialization fallback is needed.
func SessionAnswerContent(tk *taskfabric.Task) (string, error) {
	dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if err != nil {
		return "", err
	}
	sc, ok := dc.StepCheckpoint.(map[string]any)
	if !ok {
		return "", fmt.Errorf("answer checkpoint carries no result map")
	}
	raw, ok := sc["items"]
	if !ok {
		return "", fmt.Errorf("answer envelope carries no items")
	}
	items, ok := raw.([]*models.RecommendItem)
	if !ok || len(items) == 0 {
		return "", fmt.Errorf("answer items unreadable, got %T", raw)
	}
	return items[0].Content, nil
}

// AwaitResult is the terminal outcome of a session wait: the answer body
// with the caller-visible diagnostics, or the stall/failure verdict.
type AwaitResult struct {
	// Answer is the terminal answer body (non-empty when Stalled/Failed are
	// both false).
	Answer string
	// Stalled is true when the session died without an answer (sustained
	// verdict, race-guard re-checked). err carries *StalledError.
	Stalled bool
	// AnswerFailed is true when the session's answer task is terminally
	// FAILED — the sole exit is closed.
	AnswerFailed bool
	// PlanFailed is true when the submission root task FAILED.
	PlanFailed bool
	// Err carries the terminal failure: *StalledError on Stalled, the plan
	// / answer failure error otherwise. Nil on success.
	Err error
}

// AwaitOptions tunes the shared wait loop.
type AwaitOptions struct {
	// PollInterval overrides the shared cadence (serve keeps 200ms for its
	// 202-degrade budget). Zero = DefaultAwaitPollInterval.
	PollInterval time.Duration
	// Wait is the fallback bound when ctx has no deadline. Zero =
	// DefaultAwaitWait. Ignored when ctx already carries a deadline.
	Wait time.Duration
}

// AwaitSessionResult runs the shared wait loop: poll the fabric until the
// session produces its terminal answer, or until failure/stall closes every
// exit, or until the wait budget expires. It is the single implementation of
// the wait invariants the three former loops each maintained by hand.
//
// The loop returns on the FIRST of:
//   - a completed answer (AwaitResult.Answer non-empty, Err nil);
//   - the plan root FAILED (PlanFailed + Err);
//   - the answer task FAILED (AnswerFailed + Err);
//   - a sustained stall verdict (Stalled + *StalledError carrying the E5
//     failure diagnostics);
//   - the wait budget expiring (Err = the context/deadline error).
//
// The session's release is the CALLER's concern: the answer path releases
// via the answer node on success, and failure cleanup differs per entry
// point (auto-admitted vs caller-owned sessions).
func AwaitSessionResult(
	ctx context.Context,
	fabric *taskfabric.Fabric,
	sessionID, planTaskID string,
	opts AwaitOptions,
) AwaitResult {
	poll := opts.PollInterval
	if poll <= 0 {
		poll = DefaultAwaitPollInterval
	}
	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		wait := opts.Wait
		if wait <= 0 {
			wait = DefaultAwaitWait
		}
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	stalls := &StallDetector{}
	for {
		// Fast failure: a failed plan means the session can never answer.
		if tk, err := fabric.Task(planTaskID); err == nil && tk.State == taskfabric.StateFailed {
			return AwaitResult{PlanFailed: true, Err: fmt.Errorf("plan task %s failed", planTaskID)}
		}
		// Fast failure: a terminally failed answer node closes the session's
		// sole exit — no answer can ever arrive.
		if SessionAnswerFailed(fabric, sessionID) {
			return AwaitResult{AnswerFailed: true, Err: fmt.Errorf("session %s answer task failed", sessionID)}
		}
		// The answer scan runs FIRST (before any stall verdict below).
		if answer, ok := SessionAnswer(fabric, sessionID); ok {
			return AwaitResult{Answer: answer}
		}
		if stalls.Stalled(fabric, sessionID, planTaskID) {
			// Race guard: the answer scan above and this verdict are two
			// separate reads, so the answer node can complete and record its
			// body between them. Re-check once so a stall declared inside
			// that window still returns the now-available answer.
			if answer, ok := SessionAnswer(fabric, sessionID); ok && answer != "" {
				return AwaitResult{Answer: answer}
			}
			return AwaitResult{
				Stalled: true,
				Err: &StalledError{
					SessionID:   sessionID,
					FailedTasks: sessionFailedTasks(fabric, sessionID),
				},
			}
		}
		select {
		case <-waitCtx.Done():
			return AwaitResult{Err: fmt.Errorf("session %s: %w", sessionID, waitCtx.Err())}
		case <-ticker.C:
		}
	}
}

// sessionFailedTasks collects the session's FAILED tasks with their failure
// causes (E5 diagnostics). Best-effort: a task that disappears mid-scan is
// skipped — the diagnostics are advisory context on the stall error, not a
// transactional snapshot.
func sessionFailedTasks(fabric *taskfabric.Fabric, sessionID string) []FailedTask {
	if fabric == nil {
		return nil
	}
	prefix := agentfabric.SessionTaskPrefix(sessionID)
	var failed []FailedTask
	for _, id := range fabric.IDs() {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		tk, err := fabric.Task(id)
		if err != nil || tk.State != taskfabric.StateFailed {
			continue
		}
		ft := FailedTask{TaskID: id, FailedDependency: tk.FailedDependency}
		if dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint); err == nil {
			ft.Cause = dc.LastError
		}
		failed = append(failed, ft)
	}
	return failed
}
