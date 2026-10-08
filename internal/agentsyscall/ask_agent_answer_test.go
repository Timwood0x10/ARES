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
