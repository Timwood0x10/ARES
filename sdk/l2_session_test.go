package sdk

import (
	"context"
	"testing"
	"time"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// TestSubmit_SessionContinuation locks the multi-turn L2 session contract:
// two submissions sharing a Task.SessionID run in the same session, each
// Result carries ITS OWN turn's answer — the second turn must never be
// answered by the first turn's completed answer — and the plan task's
// cumulative token usage rides on the Result (the planner quantum's LLM
// spend, accumulated by the kernel re-wrap path).
func TestSubmit_SessionContinuation(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	rt.llmSvc = &mockLLMSvc{responses: []*llmcore.GenerateResponse{
		{Content: "turn one answer", Usage: llmcore.TokenUsage{PromptTokens: 7, CompletionTokens: 3}},
		{Content: "turn two answer", Usage: llmcore.TokenUsage{PromptTokens: 5, CompletionTokens: 2}},
	}}

	first, err := rt.Submit(context.Background(), Task{
		Capability: "auditor",
		Input:      "question one",
		SessionID:  "chat-42",
	})
	if err != nil {
		t.Fatalf("first Submit error: %v", err)
	}
	if first.Output != "turn one answer" {
		t.Fatalf("first Output = %q, want %q", first.Output, "turn one answer")
	}
	if first.TokenUsage.Input != 7 || first.TokenUsage.Output != 3 || first.TokenUsage.Total != 10 {
		t.Fatalf("first TokenUsage = %+v, want {Input:7 Output:3 Total:10}", first.TokenUsage)
	}

	second, err := rt.Submit(context.Background(), Task{
		Capability: "auditor",
		Input:      "question two",
		SessionID:  "chat-42",
	})
	if err != nil {
		t.Fatalf("second Submit error: %v", err)
	}
	if second.Output != "turn two answer" {
		t.Fatalf("second Output = %q — a continuation must serve THIS turn's answer, not a stale one", second.Output)
	}
	if second.TokenUsage.Input != 5 || second.TokenUsage.Output != 2 || second.TokenUsage.Total != 7 {
		t.Fatalf("second TokenUsage = %+v, want {Input:5 Output:2 Total:7}", second.TokenUsage)
	}
}

// TestSubmit_SessionIDWithSlashRejected locks the admission boundary: a
// session id containing "/" would break the reaper keep-set reverse parse
// (SessionIDFromNode splits at the first slash), so the submission must fail
// fast instead of corrupting reaper scoping.
func TestSubmit_SessionIDWithSlashRejected(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	rt.llmSvc = &mockLLMSvc{responses: []*llmcore.GenerateResponse{
		{Content: "must not run"},
	}}

	_, err := rt.Submit(context.Background(), Task{
		Capability: "auditor",
		Input:      "q",
		SessionID:  "team/42",
	})
	if err == nil {
		t.Fatal("Submit with a slash-containing SessionID must fail fast")
	}
}

// TestL2WaitContext_Bounds pins the wait-bound contract of the L2 settle
// loop: an explicit Task.Timeout wins; a caller ctx that carries its own
// deadline is respected as-is; a ctx with neither inherits the serve-parity
// default cap so Submit can never block forever.
func TestL2WaitContext_Bounds(t *testing.T) {
	t.Run("explicit timeout wins", func(t *testing.T) {
		waitCtx, taskBounded, cancel := l2WaitContext(context.Background(), 200*time.Millisecond)
		if cancel == nil {
			t.Fatal("explicit timeout must derive a bounded context")
		}
		defer cancel()
		if !taskBounded {
			t.Fatal("an explicit timeout must be attributed to the task")
		}
		if _, ok := waitCtx.Deadline(); !ok {
			t.Fatal("derived context must carry a deadline")
		}
	})
	t.Run("caller deadline respected", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		waitCtx, taskBounded, gotCancel := l2WaitContext(ctx, 0)
		if waitCtx != ctx {
			t.Fatal("a caller deadline must be used as-is (no derived context)")
		}
		if taskBounded || gotCancel != nil {
			t.Fatal("no task timeout means no task attribution and no cancel")
		}
	})
	t.Run("default cap fallback", func(t *testing.T) {
		waitCtx, taskBounded, cancel := l2WaitContext(context.Background(), 0)
		if cancel == nil {
			t.Fatal("a deadline-less ctx must get the default wait cap")
		}
		defer cancel()
		if taskBounded {
			t.Fatal("the fallback cap must not be attributed to the task")
		}
		deadline, ok := waitCtx.Deadline()
		if !ok {
			t.Fatal("fallback context must carry a deadline")
		}
		if d := time.Until(deadline); d <= 0 || d > l2DefaultWait {
			t.Fatalf("fallback deadline out of range: %v (cap %v)", d, l2DefaultWait)
		}
	})
}

// TestL2SessionAnswerFailed pins the answer-node fast-fail scan: a
// terminally FAILED answer under the session prefix closes the session's
// sole exit; sibling sessions and non-terminal answers do not trigger it.
// (Prior-turn answers cannot false-trigger this in real runs: Sessions.Admit
// harvests a re-admitted session's terminal tasks before this turn's plan
// can grow an answer.)
func TestL2SessionAnswerFailed(t *testing.T) {
	fabric := taskfabric.NewFabric()
	mkAnswer := func(id string, fail bool) {
		t.Helper()
		if err := fabric.Create(&taskfabric.Task{
			ID:          id,
			Capability:  "ares/answer",
			RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 0},
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		if !fail {
			return
		}
		epoch, err := fabric.Acquire(id, "agt", time.Minute)
		if err != nil {
			t.Fatalf("Acquire %s: %v", id, err)
		}
		if err := fabric.Start(id, "agt", epoch); err != nil {
			t.Fatalf("Start %s: %v", id, err)
		}
		if err := fabric.Fail(id, "agt", epoch); err != nil {
			t.Fatalf("Fail %s: %v", id, err)
		}
	}

	mkAnswer("sess/s1/d1/t#s/answer#0", true)
	if !l2SessionAnswerFailed(fabric, "s1") {
		t.Fatal("a terminally FAILED answer must close the session")
	}
	if l2SessionAnswerFailed(fabric, "s10") {
		t.Fatal("the prefix match must not leak across sibling sessions")
	}
	mkAnswer("sess/s2/d1/t#s/answer#0", false)
	if l2SessionAnswerFailed(fabric, "s2") {
		t.Fatal("a non-terminal answer must not close the session")
	}
}
