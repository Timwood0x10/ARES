package sdk

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestSubmit_RegisteredAgent verifies the closed loop
// (NewRuntime → RegisterAgent → Submit → result): a task
// submitted with a registered capability is executed by the agent registered
// for it, and the result flows back unchanged.
func TestSubmit_RegisteredAgent(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	rt.llmSvc = &mockLLMSvc{responses: []*llmcore.GenerateResponse{
		{Content: "handled by coder", Usage: llmcore.TokenUsage{PromptTokens: 2, CompletionTokens: 4}},
	}}

	rt.RegisterAgent("coder", WithInstruction("you handle code tasks"))
	res, err := rt.Submit(context.Background(), Task{Capability: "coder", Input: "refactor main.go"})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if res.Output != "handled by coder" {
		t.Fatalf("Output = %q, want %q", res.Output, "handled by coder")
	}
}

// TestSubmit_UnregisteredRoutesThroughL2 locks the B3 stage-2 routing: a
// runtime never refuses a well-formed task just because its capability was
// not pre-registered — an unregistered capability is auto-admitted as an L2
// session and executed by the shared router cognition (the same path serve
// uses), NOT by an auto-created ReAct agent. The routing leaves no static
// executor behind.
func TestSubmit_UnregisteredRoutesThroughL2(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	rt.llmSvc = &mockLLMSvc{responses: []*llmcore.GenerateResponse{
		{Content: "routed through the L2 router"},
	}}

	res, err := rt.Submit(context.Background(), Task{Capability: "auditor", Input: "audit config"})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if res.Output != "routed through the L2 router" {
		t.Fatalf("Output = %q, want the answer the L2 router completed with", res.Output)
	}
	// The L2 core must be wired as a consequence of the first L2 submission.
	if rt.ensureL2() == nil {
		t.Fatal("unregistered Submit must wire the shared L2 execution core")
	}
	// The L2 path routes by session admission — it must NOT leave an
	// auto-created ReAct executor registered under the capability.
	if _, ok := rt.sched.LookupExecutor("auditor"); ok {
		t.Fatal("unregistered capability must not get a static ReAct executor")
	}
}

// TestSubmit_EmptyCapabilityRoutesThroughL2 verifies that a task without a
// capability is normalized onto the L2 session path (PlanCapability), the
// same admission semantics serve-mode submissions get.
func TestSubmit_EmptyCapabilityRoutesThroughL2(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	rt.llmSvc = &mockLLMSvc{responses: []*llmcore.GenerateResponse{
		{Content: "answered without a capability"},
	}}

	rt.RegisterAgent("coder")
	res, err := rt.Submit(context.Background(), Task{Input: "do the thing"})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if res.Output != "answered without a capability" {
		t.Fatalf("Output = %q, want the answer the L2 router completed with", res.Output)
	}
}

// blockingLLM blocks until the context is done, then returns the context
// error — it makes Timeout propagation observable through Submit → the
// L2 execution core.
type blockingLLM struct{}

func (b *blockingLLM) Generate(ctx context.Context, _ *llmcore.GenerateRequest) (*llmcore.GenerateResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingLLM) GetProvider() llmcore.LLMProvider { return llmcore.LLMProviderOllama }
func (b *blockingLLM) GetModel() string                 { return "mock-blocking" }
func (b *blockingLLM) Close()                           {}

// TestSubmit_TimeoutPropagates verifies that Task.Timeout bounds the
// execution: a blocked LLM surfaces a deadline-exceeded cause from Submit
// (context cancellation propagates, never swallowed).
func TestSubmit_TimeoutPropagates(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	rt.llmSvc = &blockingLLM{}

	_, err := rt.Submit(context.Background(), Task{
		Capability: "slow",
		Input:      "long task",
		Timeout:    50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Submit must surface the timeout error")
	}
	// The L2 path wraps the LLM error with FriendlyErr (a string
	// label, not an unwrappable %w chain), so assert on the surfaced message
	// containing the deadline cause rather than errors.Is.
	if !errors.Is(err, context.DeadlineExceeded) &&
		!contains(err.Error(), "deadline exceeded") &&
		!contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("Submit error = %v, want a deadline-exceeded cause", err)
	}
}

// TestRegisterAgent_FirstCapabilityWins verifies the registration semantics:
// the first agent registered for a capability wins, and a later registration
// for the same capability does not silently replace it.
func TestRegisterAgent_FirstCapabilityWins(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()

	first := rt.RegisterAgent("coder")
	second := rt.RegisterAgent("writer")
	if got := rt.lookupAgent("coder"); got != first {
		t.Fatal("first registration for a capability must win")
	}
	if got := rt.lookupAgent("writer"); got != second {
		t.Fatal("second capability must map to its agent")
	}
	// Re-registering the same capability must NOT replace the first agent.
	_ = rt.RegisterAgent("coder")
	if got := rt.lookupAgent("coder"); got != first {
		t.Fatal("re-registration must not replace the winning agent")
	}
}
