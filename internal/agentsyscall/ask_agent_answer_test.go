package agentsyscall

import (
	"context"
	"errors"
	"testing"

	kctx "github.com/Timwood0x10/ares/internal/kernel/ctx"
)

// TestAskAgent_AnswerRoundTrip pins the answer round-trip contract:
// when the AskAgentFn returns a direct answer, AskAgent maps it to
// {Accepted: true, Status: "delivered", Answer: <payload>}.
func TestAskAgent_AnswerRoundTrip(t *testing.T) {
	kernel := NewKernel(nil, nil, nil, nil, WithAskAgent(
		func(_ context.Context, _, _, _ string, _ any) (any, error) {
			return "the-answer", nil
		},
	))

	res, err := kernel.AskAgent(
		kctx.WithCallerID(context.Background(), "agent-A"),
		AskAgentArgs{To: "agent-B", Topic: "q"},
	)
	if err != nil {
		t.Fatalf("AskAgent: %v", err)
	}
	if !res.Accepted {
		t.Fatal("Accepted must be true")
	}
	if res.Status != "delivered" {
		t.Fatalf("Status = %q, want \"delivered\"", res.Status)
	}
	if res.Answer != "the-answer" {
		t.Fatalf("Answer = %v, want \"the-answer\"", res.Answer)
	}
}

// TestAskAgent_AnswerNilOnNoReply verifies that a nil answer (target did not
// reply) is distinct from an error. Status is "delivered" with a nil Answer.
func TestAskAgent_AnswerNilOnNoReply(t *testing.T) {
	kernel := NewKernel(nil, nil, nil, nil, WithAskAgent(
		func(_ context.Context, _, _, _ string, _ any) (any, error) {
			return nil, nil // no reply, no error
		},
	))

	res, err := kernel.AskAgent(
		kctx.WithCallerID(context.Background(), "agent-A"),
		AskAgentArgs{To: "agent-B", Topic: "q"},
	)
	if err != nil {
		t.Fatalf("AskAgent: %v", err)
	}
	if !res.Accepted {
		t.Fatal("Accepted must be true even with nil answer")
	}
	if res.Status != "delivered" {
		t.Fatalf("Status = %q, want \"delivered\"", res.Status)
	}
	if res.Answer != nil {
		t.Fatalf("Answer = %v, want nil", res.Answer)
	}
}

// TestAskAgent_Yielding verifies the C-1 fix: when AskAgentFn returns
// ErrAskAgentYielding, AskAgent maps it to {Accepted: true, Status: "pending",
// Answer: nil} — the quantum completes without blocking drain.
func TestAskAgent_Yielding(t *testing.T) {
	kernel := NewKernel(nil, nil, nil, nil, WithAskAgent(
		func(_ context.Context, _, _, _ string, _ any) (any, error) {
			return nil, ErrAskAgentYielding
		},
	))

	res, err := kernel.AskAgent(
		kctx.WithCallerID(context.Background(), "agent-A"),
		AskAgentArgs{To: "agent-B", Topic: "q"},
	)
	if err != nil {
		t.Fatalf("AskAgent: %v", err)
	}
	if !res.Accepted {
		t.Fatal("Accepted must be true on yielding")
	}
	if res.Status != "pending" {
		t.Fatalf("Status = %q, want \"pending\"", res.Status)
	}
	if res.Answer != nil {
		t.Fatalf("Answer = %v, want nil on pending", res.Answer)
	}
}

// TestAskAgent_NoReply verifies the default-branch path: when AskAgentFn
// returns ErrAskAgentNoReply, AskAgent maps it to {Accepted: true,
// Status: "no-reply", Answer: nil}.
func TestAskAgent_NoReply(t *testing.T) {
	kernel := NewKernel(nil, nil, nil, nil, WithAskAgent(
		func(_ context.Context, _, _, _ string, _ any) (any, error) {
			return nil, ErrAskAgentNoReply
		},
	))

	res, err := kernel.AskAgent(
		kctx.WithCallerID(context.Background(), "agent-A"),
		AskAgentArgs{To: "agent-B", Topic: "q"},
	)
	if err != nil {
		t.Fatalf("AskAgent: %v", err)
	}
	if !res.Accepted {
		t.Fatal("Accepted must be true on no-reply")
	}
	if res.Status != "no-reply" {
		t.Fatalf("Status = %q, want \"no-reply\"", res.Status)
	}
	if res.Answer != nil {
		t.Fatalf("Answer = %v, want nil on no-reply", res.Answer)
	}
}

// TestAskAgent_DuplicatePendingSuppressed pins the retry-amplification guard:
// while a yielded (pending) ask is still in flight, an identical repeat must
// NOT reach the primitive again — the reply is never written back, so a
// relaunch could only spawn a second target-side session for an answer nobody
// will read. A distinct target is a distinct question and must still go
// through.
func TestAskAgent_DuplicatePendingSuppressed(t *testing.T) {
	var calls int
	kernel := NewKernel(nil, nil, nil, nil, WithAskAgent(
		func(_ context.Context, _, _, _ string, _ any) (any, error) {
			calls++
			return nil, ErrAskAgentYielding
		},
	))
	ctx := kctx.WithCallerID(context.Background(), "agent-A")
	args := AskAgentArgs{To: "agent-B", Topic: "q", Payload: map[string]any{"task_desc": "review"}}

	first, err := kernel.AskAgent(ctx, args)
	if err != nil {
		t.Fatalf("first AskAgent: %v", err)
	}
	if first.Duplicate {
		t.Fatal("the first ask must not be flagged duplicate")
	}
	if first.Status != "pending" {
		t.Fatalf("Status = %q, want \"pending\"", first.Status)
	}

	second, err := kernel.AskAgent(ctx, args)
	if err != nil {
		t.Fatalf("second AskAgent: %v", err)
	}
	if !second.Duplicate {
		t.Fatal("an identical ask while one is pending must be flagged duplicate")
	}
	if second.Status != "pending" {
		t.Fatalf("Status = %q, want \"pending\"", second.Status)
	}
	if calls != 1 {
		t.Fatalf("primitive called %d times, want 1 (duplicate must not relaunch)", calls)
	}

	// A different target (and a different payload) are different questions.
	if _, err := kernel.AskAgent(ctx, AskAgentArgs{To: "agent-C", Topic: "q"}); err != nil {
		t.Fatalf("distinct target: %v", err)
	}
	if _, err := kernel.AskAgent(ctx, AskAgentArgs{
		To: "agent-B", Topic: "q", Payload: map[string]any{"task_desc": "other"},
	}); err != nil {
		t.Fatalf("distinct payload: %v", err)
	}
	if calls != 3 {
		t.Fatalf("primitive called %d times, want 3 (distinct asks must pass)", calls)
	}
}

// TestAskAgent_DeliveredNotDeduplicated pins the scope of the guard: only a
// PENDING ask is remembered. A synchronously delivered answer (in-process /
// SDK callers injecting their own AskAgentFn) leaves no entry, so an
// identical follow-up question is still allowed.
func TestAskAgent_DeliveredNotDeduplicated(t *testing.T) {
	var calls int
	kernel := NewKernel(nil, nil, nil, nil, WithAskAgent(
		func(_ context.Context, _, _, _ string, _ any) (any, error) {
			calls++
			return "the-answer", nil
		},
	))
	ctx := kctx.WithCallerID(context.Background(), "agent-A")
	args := AskAgentArgs{To: "agent-B", Topic: "q"}

	for i := 0; i < 2; i++ {
		res, err := kernel.AskAgent(ctx, args)
		if err != nil {
			t.Fatalf("AskAgent #%d: %v", i+1, err)
		}
		if res.Status != "delivered" || res.Duplicate {
			t.Fatalf("AskAgent #%d = %+v, want delivered and not duplicate", i+1, res)
		}
	}
	if calls != 2 {
		t.Fatalf("primitive called %d times, want 2 (delivered asks are never deduped)", calls)
	}
}

// TestAskAgent_AnswerErrorPropagates verifies that an error from the
// collaboration primitive is returned to the caller, not swallowed.
func TestAskAgent_AnswerErrorPropagates(t *testing.T) {
	kernel := NewKernel(nil, nil, nil, nil, WithAskAgent(
		func(_ context.Context, _, _, _ string, _ any) (any, error) {
			return nil, errors.New("target unreachable")
		},
	))

	_, err := kernel.AskAgent(
		kctx.WithCallerID(context.Background(), "agent-A"),
		AskAgentArgs{To: "agent-B", Topic: "q"},
	)
	if err == nil {
		t.Fatal("error must propagate")
	}
}
