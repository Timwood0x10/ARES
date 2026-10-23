package context

import (
	"strings"
	"testing"
)

// TestClean_IntentSurvivesCompression is the regression test for the defect:
// an assistant turn that CALLS a tool states why it is calling it, and the old
// cleaner cut that prose to a 50-rune gist — the causal chain's "why" was the
// first casualty of compression. Within the intent budget it must now arrive
// intact, including the whitespace normalization that keeps its cost stable.
func TestClean_IntentSurvivesCompression(t *testing.T) {
	cl := NewContextCleaner()
	intent := "I grep the parser package first because the failing symbol is not\n\n" +
		"reachable from the lexer tests, and the parser owns that path."

	msg := Message{
		Role:    RoleAssistant,
		Content: intent,
		ToolCalls: []ToolCall{
			{ID: "c1", Type: "function", Function: ToolCallFunction{Name: "grep"}},
		},
	}

	got := cl.Clean([]Message{msg})[0].Content
	if !strings.Contains(got, "failing symbol is not reachable from the lexer tests") {
		t.Fatalf("the reason for the call was destroyed by compression: %q", got)
	}
	if len([]rune(got)) > DefaultCleanOptions().MaxIntentLen {
		t.Fatalf("intent exceeded its budget: %d runes", len([]rune(got)))
	}
}

// TestClean_IntentFallsBackToReasoning: reasoning models answer a tool-call
// turn with an EMPTY Content and a non-empty thinking trace. Dropping the trace
// (the old behaviour, via the field never existing) is how exactly the turns
// that act lose their reason.
func TestClean_IntentFallsBackToReasoning(t *testing.T) {
	cl := NewContextCleaner()
	msg := Message{
		Role:             RoleAssistant,
		Content:          "",
		ReasoningContent: "The build failed on the tokenizer, so I will read that file next.",
		ToolCalls:        []ToolCall{{ID: "c1", Function: ToolCallFunction{Name: "read"}}},
	}

	got := cl.Clean([]Message{msg})[0].Content
	if !strings.Contains(got, "build failed on the tokenizer") {
		t.Fatalf("the reasoning trace was not used as the intent: %q", got)
	}
}

// TestClean_PreservesCausalFields locks the provenance contract: EventKind,
// ParentID and ArtifactRefs are the edges of the causal chain. The cleaner used
// to rebuild each Message field-by-field and silently dropped all three, so
// cleaning erased the structure the session store had recorded.
func TestClean_PreservesCausalFields(t *testing.T) {
	cl := NewContextCleaner()
	msg := Message{
		Role:         RoleToolResult,
		Content:      "ok",
		EventKind:    "tool.result",
		ParentID:     "sess/1/plan/0",
		ArtifactRefs: []string{"artifact://run/7"},
		ToolCallID:   "c1",
	}

	got := cl.Clean([]Message{msg})[0]
	if got.EventKind != msg.EventKind {
		t.Errorf("EventKind lost: %q", got.EventKind)
	}
	if got.ParentID != msg.ParentID {
		t.Errorf("ParentID (the causal edge) lost: %q", got.ParentID)
	}
	if len(got.ArtifactRefs) != 1 || got.ArtifactRefs[0] != "artifact://run/7" {
		t.Errorf("ArtifactRefs (where the full evidence lives) lost: %v", got.ArtifactRefs)
	}

	// The intent-carrying assistant turn must keep them too.
	msgs := []Message{{
		Role:         RoleAssistant,
		Content:      "because",
		ToolCalls:    []ToolCall{{ID: "c1", Function: ToolCallFunction{Name: "grep"}}},
		EventKind:    "assistant.tool_call",
		ParentID:     "sess/1/plan/0",
		ArtifactRefs: []string{"artifact://a"},
	}}
	out := cl.Clean(msgs)[0]
	if out.EventKind != "assistant.tool_call" || out.ParentID != "sess/1/plan/0" || len(out.ArtifactRefs) != 1 {
		t.Errorf("assistant causal fields lost: kind=%q parent=%q refs=%v",
			out.EventKind, out.ParentID, out.ArtifactRefs)
	}
}

// TestCompressObservation_CarriesIdentity: a compressed result must stay
// verifiable — how much was dropped, and which bytes it was. A bare gist is
// indistinguishable from a complete short answer.
func TestCompressObservation_CarriesIdentity(t *testing.T) {
	long := strings.Repeat("warning: deprecated call site\n", 40) + "done\n"
	got := compressObservation(Message{Role: RoleToolResult, Content: long}, 50)

	if len([]rune(got)) >= len([]rune(long)) {
		t.Fatalf("not compressed: %d vs %d runes", len([]rune(got)), len([]rune(long)))
	}
	if !strings.Contains(got, "sha256:") {
		t.Errorf("a cut observation must identify its original: %q", got)
	}
	if !strings.Contains(got, "runes") {
		t.Errorf("a cut observation must say how much it dropped: %q", got)
	}

	// A result that fits is returned untouched; identity marking is only for
	// what was actually cut.
	if short := compressObservation(Message{Role: RoleToolResult, Content: "ok"}, 50); short != "ok" {
		t.Errorf("short observation must pass through: %q", short)
	}
}

// TestSummarizeToolCall_KeepsArgumentValues is the "why did I already do this"
// fix: keys alone ("file_tools(operation, path)") cannot tell a repeated call
// from a new one, which is the measured cause of a tool being re-issued every
// round. Values are kept, bounded.
func TestSummarizeToolCall_KeepsArgumentValues(t *testing.T) {
	msg := Message{
		Role:    RoleToolCall,
		Content: "read the config",
		ToolCalls: []ToolCall{{
			ID:   "c9",
			Type: "function",
			Function: ToolCallFunction{
				Name:      "file_tools",
				Arguments: `{"operation":"read","path":"/etc/ares/agent.yaml"}`,
			},
		}},
	}

	got := summarizeToolCall(msg)
	for _, want := range []string{"c9", "file_tools", "read", "/etc/ares/agent.yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lost %q: %s", want, got)
		}
	}
}
