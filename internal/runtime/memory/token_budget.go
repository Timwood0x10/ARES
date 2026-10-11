package memory

import (
	"unicode/utf8"

	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
)

// Token-budget trimming (plan/0.3.3_task.md Appendix B G2).
//
// BuildContext's history window is measured in message COUNT (MaxHistory); a
// single large tool result can still blow the real context window. The token
// budget is an optional, opt-in SECOND gate that trims the windowed messages
// to an estimated token ceiling BEFORE the rule-based cleaner runs (fixed
// order: budget first, clean second). It never widens any budget; off
// (budget <= 0) is a no-op.
//
// Pairing caveat — see the fuller note on trimToTokenBudget: the cleaner that
// runs next does NOT necessarily repair a pair the budget split, because the
// default flat Clean only truncates each message in place.

// perMessageTokenOverhead approximates the role + delimiter framing every
// message carries on the wire. It is part of the deliberate OVER-estimate.
const perMessageTokenOverhead = 4

// estimateMessageTokens returns a DELIBERATELY CONSERVATIVE (over-)estimate of
// a message's token cost. It is NOT a tokenizer: it approximates tokens as
// ceil(runes/3) — biased high versus the common ~4-chars-per-token heuristic —
// plus a small per-message framing overhead, so the budget trims MORE rather
// than risk overflowing the real context window ("宁可少塞"). Do not treat the
// result as exact.
func estimateMessageTokens(m Message) int {
	runes := utf8.RuneCountInString(m.Content)
	for _, tc := range m.ToolCalls {
		runes += utf8.RuneCountInString(tc.Function.Name) + utf8.RuneCountInString(tc.Function.Arguments)
	}
	return perMessageTokenOverhead + (runes+2)/3 // ceil(runes/3), an estimate
}

// estimateTokens sums the conservative per-message estimate over a slice.
func estimateTokens(msgs []Message) int {
	total := 0
	for i := range msgs {
		total += estimateMessageTokens(msgs[i])
	}
	return total
}

// budgetTier classifies a message for the FIXED token-budget drop order:
//
//	tier 0 = system  (floor — never dropped)
//	tier 1 = tool-causal (tool_call / tool_result / assistant carrying ToolCalls)
//	tier 2 = plain history (user / assistant without tool calls)
//
// When over budget, tier 2 is dropped before tier 1, and tier 0 is never
// dropped — "system/schema 保底 → tool 因果链 → assistant/user 历史".
func budgetTier(m Message) int {
	switch {
	case m.Role == memctx.RoleSystem:
		return 0
	case m.Role == memctx.RoleToolCall, m.Role == memctx.RoleToolResult,
		m.Role == memctx.RoleAssistant && len(m.ToolCalls) > 0:
		return 1
	default:
		return 2
	}
}

// trimToTokenBudget drops messages until the conservative token estimate fits
// budget, in the fixed order history (tier 2) → tool-causal (tier 1), oldest
// first within a tier; system (tier 0) is never dropped. It returns the kept
// messages (original order preserved) and their estimated token use. A budget
// <= 0 disables trimming and returns the input unchanged.
//
// Pairing caveat: dropping is per-message, so a tool pair can be split — tier 1
// is dropped oldest-first, which removes an assistant tool_call before the
// tool_result answering it. The caller cleans afterwards, but the DEFAULT flat
// ContextCleaner.Clean truncates each message in place (its output slice has
// the same length as its input) and neither re-pairs nor drops orphans; only
// CleanWithTurns (TurnAwareCleaning — OFF by default and independent of this
// budget) groups by turn. So under the default turn-aware setting a trim can
// leave a tool_result whose tool_call was dropped. BuildContext renders history
// as plain text, so the consequence is a less coherent transcript for the
// model rather than an invalid structured payload.
func trimToTokenBudget(msgs []Message, budget int) ([]Message, int) {
	total := estimateTokens(msgs)
	if budget <= 0 || total <= budget {
		return msgs, total
	}

	keep := make([]bool, len(msgs))
	for i := range msgs {
		keep[i] = true
	}

	cur := total
	for _, tier := range []int{2, 1} {
		for i := 0; i < len(msgs) && cur > budget; i++ {
			if keep[i] && budgetTier(msgs[i]) == tier {
				keep[i] = false
				cur -= estimateMessageTokens(msgs[i])
			}
		}
		if cur <= budget {
			break
		}
	}

	out := make([]Message, 0, len(msgs))
	for i := range msgs {
		if keep[i] {
			out = append(out, msgs[i])
		}
	}
	return out, cur
}
