package sdk

// l2_submit.go is the SDK's stage-2 L2 submission glue: an unregistered
// capability is driven through the shared L2 execution core
// (agentruntime.Submitter → session admission → ares/plan root task → the
// router cognition) and the caller waits for the session's terminal answer.
// It mirrors cmd/ares's executeAskViaSession so the SDK and serve entry
// points share one admission + answer-extraction semantic.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// l2PollInterval is the settle-poll cadence for L2 submissions (same as
// cmd/ares's ask-session wait loop).
const l2PollInterval = 20 * time.Millisecond

// l2DefaultWait bounds an L2 wait that has neither an explicit Task.Timeout
// nor a caller-context deadline (serve parity: cmd/ares's collabTimeout caps
// its ask-session wait loop the same way). Without it a submission that can
// never win — e.g. a plan task with no capable candidate, or a plan that
// completed while the answer node exhausted its retries — would block
// Submit forever.
const l2DefaultWait = 10 * time.Minute

// submitThroughL2 drives one submission through the shared L2 execution core
// and waits for the session's terminal answer. Static executors never reach
// here — the caller (submitThroughScheduler) routes only unregistered
// capabilities onto this path.
//
// Lifecycle (mirrors executeAskViaSession):
//   - Submitter.Submit auto-admits a fresh session (sess-auto-N) and creates
//     the ares/plan root task;
//   - the SDK scheduler drains it through the L2 peer's router cognition —
//     the planner grows the session graph and the terminal answer node
//     completes with the answer body as items[0].Content;
//   - the session is released quietly on EVERY exit path (the answer body
//     releases it on success; failure/timeout release here so the reaper can
//     reclaim the session's tasks instead of pinning them forever);
//   - the loop fails fast when the session can never answer (plan FAILED or
//     a terminally failed answer node) and is always bounded: explicit
//     Task.Timeout, else the caller ctx deadline, else l2DefaultWait.
func (r *Runtime) submitThroughL2(ctx context.Context, execCore *agentruntime.Execution, t Task) (*Result, error) {
	start := time.Now()
	payload := map[string]any{"input": t.Input}
	// Caller-supplied session: continuation semantics. Staleness is handled at
	// the ADMISSION boundary — a re-admitted session harvests the previous
	// turn's terminal tasks before recompiling its root (Sessions.Admit), so
	// any completed answer the wait loop below finds belongs to THIS turn.
	// (A pre-submit answer snapshot would break here: recompiled session
	// nodes legitimately reuse their IDs, e.g. d1/answer#0, and the snapshot
	// would reject the fresh answer forever.)
	if t.SessionID != "" {
		payload["session_id"] = t.SessionID
	}
	taskID, sessionID, err := execCore.Submitter.Submit(ctx, t.Capability, payload)
	if err != nil {
		// Admission can register the session BEFORE a later failure (fabric
		// Create collision); release so only the reaper does not have to be
		// the sole reclaimer. A never-admitted session is a swallowed no-op.
		if sessionID != "" {
			execCore.Sessions.ReleaseQuietly(sessionID)
		}
		return nil, fmt.Errorf("sdk submit: %w", err)
	}
	// reclaim the session on EVERY exit path (success releases it inside the
	// answer node; this covers failure / timeout / cancellation).
	defer execCore.Sessions.ReleaseQuietly(sessionID)

	waitCtx, taskBounded, cancel := l2WaitContext(ctx, t.Timeout)
	if cancel != nil {
		defer cancel()
	}
	ticker := time.NewTicker(l2PollInterval)
	defer ticker.Stop()
	for {
		// Fast failure: a failed plan means the session can never answer.
		if tk, err := r.sdkFabric.Task(taskID); err == nil && tk.State == taskfabric.StateFailed {
			return nil, fmt.Errorf("sdk submit: task %s failed", taskID)
		}
		// Fast failure: a terminally failed answer node closes the session's
		// sole exit — no answer can ever arrive (the plan itself may have
		// completed, so the check above alone would spin to the deadline).
		if l2SessionAnswerFailed(r.sdkFabric, sessionID) {
			return nil, fmt.Errorf("sdk submit: session %s answer task failed", sessionID)
		}
		if answer, ok := l2SessionAnswer(r.sdkFabric, sessionID); ok {
			if answer == "" {
				return nil, fmt.Errorf("sdk submit: session %s answered empty", sessionID)
			}
			return &Result{
				Output:     answer,
				TokenUsage: l2PlanTokenUsage(r.sdkFabric, taskID),
				Duration:   time.Since(start),
			}, nil
		}
		select {
		case <-waitCtx.Done():
			// Task.Timeout and a caller-context deadline share waitCtx; name
			// the duration only when the task set one (a caller deadline is
			// reported as the plain wrapped context error).
			if waitCtx.Err() == context.DeadlineExceeded && taskBounded {
				return nil, fmt.Errorf("sdk submit: task %s timed out after %s: %w", taskID, t.Timeout, context.DeadlineExceeded)
			}
			return nil, fmt.Errorf("sdk submit: task %s: %w", taskID, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

// l2SessionAnswer returns the first COMPLETED answer task's content for a
// session. It scans fabric task ids (sess/<sid>/…/answer#…) rather than the
// session graph: the answer body releases its session on success, so the
// registry entry is already gone by the time the poll loop looks. The
// "sess/<sid>/" boundary match keeps sibling sessions (sess-auto-1 vs
// sess-auto-12) from shadowing each other. Continuation turns need no extra
// staleness filter here: Sessions.Admit harvests the previous turn's terminal
// tasks at re-admission, before this turn's plan can grow an answer.
func l2SessionAnswer(f *taskfabric.Fabric, sessionID string) (string, bool) {
	prefix := "sess/" + sessionID + "/"
	for _, id := range f.IDs() {
		if !strings.HasPrefix(id, prefix) || !strings.Contains(id, "/answer#") {
			continue
		}
		tk, err := f.Task(id)
		if err != nil || tk.State != taskfabric.StateCompleted {
			continue
		}
		content, err := l2AnswerContent(tk)
		if err != nil || content == "" {
			continue
		}
		return content, true
	}
	return "", false
}

// l2WaitContext bounds the settle-poll loop: an explicit Task.Timeout wins;
// otherwise a caller ctx that already carries a deadline is respected as-is,
// and a ctx with neither gets the l2DefaultWait fallback (serve parity).
// taskBounded reports that Task.Timeout set the bound, so the caller can
// attribute a deadline error to the task rather than to the caller's ctx.
// cancel is nil when no derived context was created.
func l2WaitContext(ctx context.Context, timeout time.Duration) (waitCtx context.Context, taskBounded bool, cancel context.CancelFunc) {
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		return waitCtx, true, cancel
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, false, nil
	}
	waitCtx, cancel = context.WithTimeout(ctx, l2DefaultWait)
	return waitCtx, false, cancel
}

// l2SessionAnswerFailed reports whether any answer task of the session is
// terminally FAILED — the session's sole exit is closed, so the wait loop
// can give up immediately instead of spinning to its deadline. Prior-turn
// answers cannot false-trigger this: Sessions.Admit harvests a re-admitted
// session's terminal tasks before this turn's plan can grow an answer, so
// any answer# task in the fabric belongs to the current turn.
func l2SessionAnswerFailed(f *taskfabric.Fabric, sessionID string) bool {
	prefix := "sess/" + sessionID + "/"
	for _, id := range f.IDs() {
		if !strings.HasPrefix(id, prefix) || !strings.Contains(id, "/answer#") {
			continue
		}
		if tk, err := f.Task(id); err == nil && tk.State == taskfabric.StateFailed {
			return true
		}
	}
	return false
}

// l2PlanTokenUsage reads the submission's LLM spend from the plan task's
// envelope: the kernel re-wrap path accumulates each quantum's
// input_tokens/output_tokens metadata into the executing task's cumulative
// totals, and the planner quantum (the LLM call) runs on the plan task.
// Tool/answer quanta book their own LLM spend on their OWN node tasks — this
// total is the submission's planner spend, not its full LLM usage. An
// unreadable envelope yields zero usage rather than an error (observability,
// not correctness).
func l2PlanTokenUsage(f *taskfabric.Fabric, planTaskID string) TokenUsage {
	tk, err := f.Task(planTaskID)
	if err != nil {
		return TokenUsage{}
	}
	dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if err != nil {
		return TokenUsage{}
	}
	return TokenUsage{
		Input:  dc.InputTokens,
		Output: dc.OutputTokens,
		Total:  dc.InputTokens + dc.OutputTokens,
	}
}

// l2AnswerContent reads the terminal answer body from its completion
// checkpoint (same read path as cmd/ares's sessionAnswerContent:
// items[0].Content). The in-memory fabric keeps the concrete
// []*models.RecommendItem type, so no serialization fallback is needed.
func l2AnswerContent(tk *taskfabric.Task) (string, error) {
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
