package ares_events

import (
	"testing"
)

// TestToolArgShape_CollapsesValues is the $11.0 aggregation-key invariant:
// the same tool called with the same argument KEY SET collapses to one shape
// regardless of values, while a different key set diverges. This is what lets
// the trajectory projection (Y1 C2) aggregate "same tool, same usage" without
// fragmenting on parameter values.
func TestToolArgShape_CollapsesValues(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool // true when a and b collapse to the same shape
	}{
		{"same keys diff values collapse", `{"q":"foo"}`, `{"q":"bar"}`, true},
		{"key order ignored", `{"q":"x","k":1}`, `{"k":2,"q":"y"}`, true},
		{"extra key diverges", `{"q":"foo"}`, `{"q":"foo","k":1}`, false},
		{"empty vs absent collapse", ``, ``, true},
		{"malformed -> empty", `not json`, ``, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sa := ToolArgShape(c.a)
			sb := ToolArgShape(c.b)
			if (sa == sb) != c.want {
				t.Fatalf("ToolArgShape(%q)=%q vs ToolArgShape(%q)=%q; collapse expected %v",
					c.a, sa, c.b, sb, c.want)
			}
		})
	}
}

// TestToolCompletedPayload_UnifiedKeys is the C1 identity-set invariant: every
// executor's completed payload must carry the SAME key set so the projection
// layer reads one contract, not three ad-hoc shapes. The keys here must match
// what agentfabric/chat_cognition.go, agents/sub/executor.go and
// agentloop/engine.go emit.
func TestToolCompletedPayload_UnifiedKeys(t *testing.T) {
	p := ToolCompletedPayload{
		AgentID:     "a1",
		ToolName:    "web_search",
		ToolCallID:  "call-1",
		Round:       2,
		Seq:         0,
		Success:     false,
		Error:       "boom",
		ArgShape:    "q",
		ExtraResult: "error: boom",
	}
	m := p.AsMap()
	// The identity keys are always present regardless of optional fields, so
	// the projection's key-set equality check has a stable, non-empty base.
	want := map[string]bool{
		EventKeyAgentID:    true,
		EventKeyToolName:   true,
		EventKeyToolCallID: true,
		EventKeyRound:      true,
		EventKeySeq:        true,
		EventKeySuccess:    true,
		EventKeyError:      true,
		EventKeyArgShape:   true,
	}
	for k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("unified completed payload missing key %q", k)
		}
	}
	// Success first, then explicit misuse of the shape key must not appear:
	// arg_shape must reflect the normalized key names, not values.
	if m[EventKeyArgShape] != "q" {
		t.Fatalf("arg_shape=%v, want %q", m[EventKeyArgShape], "q")
	}
}
