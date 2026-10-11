package memory

import (
	"strings"
	"testing"

	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
)

// roles returns the role sequence of a message slice, for order assertions.
func roles(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i := range msgs {
		out[i] = msgs[i].Role
	}
	return out
}

// TestTrimToTokenBudget_DropOrder pins the G2 contract
// (plan/0.3.3_task.md Appendix B): over budget, plain history (tier 2) is
// dropped before tool-causal messages (tier 1), and system (tier 0) is never
// dropped. Order of survivors is preserved.
func TestTrimToTokenBudget_DropOrder(t *testing.T) {
	big := strings.Repeat("x", 600) // ~200 estimated tokens, the heaviest msg
	msgs := []Message{
		{Role: memctx.RoleSystem, Content: "system floor"},
		{Role: memctx.RoleUser, Content: big}, // tier 2, oldest + heaviest
		{Role: memctx.RoleToolCall, Content: "grep(error)", ToolCallID: "c1"},
		{Role: memctx.RoleToolResult, Content: "found 3", ToolCallID: "c1"},
		{Role: memctx.RoleUser, Content: "recent question"},
	}

	// Budget large enough to keep everything EXCEPT the one heavy tier-2 msg.
	total := estimateTokens(msgs)
	budget := total - estimateMessageTokens(msgs[1]) // just below total

	kept, used := trimToTokenBudget(msgs, budget)
	if used > budget {
		t.Fatalf("used %d exceeds budget %d", used, budget)
	}
	// The heavy tier-2 user message must be the one dropped; system, the
	// tool-causal pair and the recent user message survive.
	wantRoles := []string{memctx.RoleSystem, memctx.RoleToolCall, memctx.RoleToolResult, memctx.RoleUser}
	got := roles(kept)
	if len(got) != len(wantRoles) {
		t.Fatalf("kept roles = %v, want %v", got, wantRoles)
	}
	for i := range wantRoles {
		if got[i] != wantRoles[i] {
			t.Fatalf("kept roles = %v, want %v", got, wantRoles)
		}
	}
	for _, m := range kept {
		if m.Content == big {
			t.Fatal("the heavy tier-2 history message must have been dropped first")
		}
	}
}

// TestTrimToTokenBudget_SystemIsFloor asserts system messages are never
// dropped, even when the budget is smaller than their estimated cost.
func TestTrimToTokenBudget_SystemIsFloor(t *testing.T) {
	msgs := []Message{
		{Role: memctx.RoleSystem, Content: "immovable system prompt"},
		{Role: memctx.RoleUser, Content: strings.Repeat("y", 300)},
		{Role: memctx.RoleToolResult, Content: strings.Repeat("z", 300), ToolCallID: "c1"},
	}
	kept, _ := trimToTokenBudget(msgs, 1) // impossibly small
	if len(kept) != 1 || kept[0].Role != memctx.RoleSystem {
		t.Fatalf("system must survive as the floor, got %v", roles(kept))
	}
}

// TestTrimToTokenBudget_DisabledAndUnderBudget are the no-op paths: a
// non-positive budget, or a total already within budget, returns the input
// unchanged (default BuildContext behaviour).
func TestTrimToTokenBudget_DisabledAndUnderBudget(t *testing.T) {
	msgs := []Message{
		{Role: memctx.RoleUser, Content: "hi"},
		{Role: memctx.RoleAssistant, Content: "hello"},
	}
	t.Run("disabled", func(t *testing.T) {
		kept, _ := trimToTokenBudget(msgs, 0)
		if len(kept) != len(msgs) {
			t.Fatalf("budget<=0 must be a no-op, got %d", len(kept))
		}
	})
	t.Run("under_budget", func(t *testing.T) {
		kept, used := trimToTokenBudget(msgs, 100000)
		if len(kept) != len(msgs) {
			t.Fatalf("under-budget must be a no-op, got %d", len(kept))
		}
		if used != estimateTokens(msgs) {
			t.Fatalf("used %d, want %d", used, estimateTokens(msgs))
		}
	})
}

// TestEstimateMessageTokens_IsConservative pins that the estimate is biased
// HIGH versus the common ~4-chars-per-token heuristic, so the budget trims
// more rather than overflow ("宁可少塞"). It is an estimate, not a tokenizer.
func TestEstimateMessageTokens_IsConservative(t *testing.T) {
	content := strings.Repeat("a", 120) // 120 runes
	est := estimateMessageTokens(Message{Role: memctx.RoleUser, Content: content})
	naive := len(content) / 4 // 30, the optimistic heuristic
	if est <= naive {
		t.Fatalf("estimate %d must exceed the optimistic %d (conservative bias)", est, naive)
	}
}
