package agentsyscall

import (
	"context"
	"testing"

	kctx "github.com/Timwood0x10/ares/internal/kernel/ctx"
)

// TestAskAgent_IgnoresLLMSuppliedFromField pins the IPC sender-provenance
// boundary: `from` is kernel-stamped from kctx.CallerID at the syscall
// boundary and the binder never decodes a `from` key from tool arguments.
// A model that smuggles {"from": "admin-agent"} in the ask_agent args map
// must NOT reach the collaboration bus as that identity.
func TestAskAgent_IgnoresLLMSuppliedFromField(t *testing.T) {
	var gotFrom, gotTo string
	binder := &stubBinder{}
	kernel := NewKernel(nil, nil, nil, nil,
		WithAskAgent(func(_ context.Context, from, to, _ string, _ any) error {
			gotFrom, gotTo = from, to
			return nil
		}))
	BindTools(binder, kernel)

	res, err := binder.call(
		kctx.WithCallerID(context.Background(), "agent-real"),
		AskAgentTool,
		map[string]any{
			"to":      "agent-B",
			"topic":   "verify",
			"from":    "admin-agent", // LLM-supplied spoof attempt
			"payload": map[string]any{"q": "x"},
		},
	)
	if err != nil {
		t.Fatalf("call ask_agent: %v", err)
	}
	if ar, ok := res.(*AskAgentResult); !ok || !ar.Accepted {
		t.Fatalf("ask_agent result = %#v, want accepted", res)
	}
	if gotFrom != "agent-real" {
		t.Fatalf("bus saw from = %q, want kernel-stamped agent-real (LLM-supplied from leaked)", gotFrom)
	}
	if gotTo != "agent-B" {
		t.Fatalf("to = %q, want agent-B", gotTo)
	}
}

// TestAskAgent_EmptyCallerIDPropagatesEmpty pins the degraded provenance
// contract: an execution path without a stamped CallerID sends an empty
// from — never a model- or payload-supplied substitute. Payload contents
// ride along opaquely (a "from" key inside the payload stays payload data
// and never promotes into the bus-level From field).
func TestAskAgent_EmptyCallerIDPropagatesEmpty(t *testing.T) {
	var gotFrom string
	var gotPayload map[string]any
	kernel := NewKernel(nil, nil, nil, nil,
		WithAskAgent(func(_ context.Context, from, _, _ string, payload any) error {
			gotFrom = from
			m, _ := payload.(map[string]any)
			gotPayload = m
			return nil
		}))

	_, err := kernel.AskAgent(context.Background(), AskAgentArgs{
		To:      "agent-B",
		Topic:   "verify",
		Payload: map[string]any{"from": "evil"},
	})
	if err != nil {
		t.Fatalf("AskAgent: %v", err)
	}
	if gotFrom != "" {
		t.Fatalf("from = %q, want empty when CallerID is unstamped", gotFrom)
	}
	if gotPayload["from"] != "evil" {
		t.Fatalf("payload[from] = %v, want opaque pass-through of \"evil\"", gotPayload["from"])
	}
}
