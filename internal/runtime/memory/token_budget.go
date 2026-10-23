package memory

import (
	"math"

	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
	"github.com/Timwood0x10/ares/internal/runtime/memory/tokencount"
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
// Trimming is per CAUSAL UNIT, not per message: an assistant tool_call and the
// tool results that answer it are dropped together or kept together (see
// trimToTokenBudget). A per-message trim used to be able to leave a tool_result
// whose call was gone — an effect with no cause — which is precisely the
// structural half of the causal chain the history is supposed to preserve.

// perMessageTokenOverhead approximates the role + delimiter framing every
// message carries on the wire. It is part of the deliberate OVER-estimate.
const perMessageTokenOverhead = 4

// DefaultContextBudgetRatio is the share of the model window a prompt may occupy.
//
// 45%, not 50%+: the prompt is not the only thing that has to fit. The answer needs room, and
// the skills block plus RAG snippets are appended AFTER this budget is applied — so a budget
// set at the window's edge leaves the last-added block with nowhere to go. The remaining 55%
// is not waste; it is the space those additions and the completion use.
const DefaultContextBudgetRatio = 0.45

// EffectiveContextTokenBudget resolves which budget applies: an explicit ContextTokenBudget
// wins, otherwise DefaultContextBudgetRatio of a configured model window, otherwise disabled.
//
// Explicit-wins matters: a deployment that measured its own prompts must be able to pin the
// number, and a derived default must never override it.
func (c MemoryConfig) EffectiveContextTokenBudget() int {
	if c.ContextTokenBudget > 0 {
		return c.ContextTokenBudget
	}
	if c.ModelContextWindow > 0 {
		return int(float64(c.ModelContextWindow) * DefaultContextBudgetRatio)
	}
	return 0
}

// estimateMessageTokens estimates a message's cost plus the per-message framing overhead
// (role, delimiters). Tool-call arguments are counted: they travel on the wire and are often
// the largest part of a message.
func estimateMessageTokens(m Message) int {
	tokens := tokencount.Text(m.Content)
	for _, tc := range m.ToolCalls {
		tokens += tokencount.Text(tc.Function.Name) + tokencount.Text(tc.Function.Arguments)
	}
	return perMessageTokenOverhead + int(math.Ceil(tokens))
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

// trimToTokenBudget drops CAUSAL UNITS until the conservative token estimate
// fits budget, in the fixed order history (tier 2) → tool-causal (tier 1),
// oldest first within a tier; system (tier 0) is never dropped. It returns the
// kept messages (original order preserved) and their estimated token use. A
// budget <= 0 disables trimming and returns the input unchanged.
//
// The unit is what changed. Dropping used to be per-MESSAGE, so a tier-1 trim
// could remove an assistant tool_call and keep the tool_result answering it:
// the model then read an answer with no question, and — worse for the causal
// chain — an intent with no action, or an action with no intent. A tool call
// and its results are ONE fact about the world; half of that fact is not a
// smaller fact, it is a wrong one. So the drop unit is the call plus every
// result that answers it, and the pair survives or dies together.
func trimToTokenBudget(msgs []Message, budget int) ([]Message, int) {
	total := estimateTokens(msgs)
	if budget <= 0 || total <= budget {
		return msgs, total
	}

	units := causalUnits(msgs)

	keep := make([]bool, len(msgs))
	for i := range msgs {
		keep[i] = true
	}

	cur := total
	// No liveness guard is needed: causalUnits partitions the history, so each
	// message belongs to exactly one unit and each unit is visited at most once
	// across the two tiers. (The earlier code carried a "kept" check described
	// as protection against overlapping units — overlap is impossible under
	// this grouping, so it was dead code that implied a hazard that cannot
	// occur.)
	for _, tier := range []int{2, 1} {
		for _, u := range units {
			if cur <= budget {
				break
			}
			if u.tier != tier {
				continue
			}
			for _, i := range u.indexes {
				keep[i] = false
			}
			cur -= u.tokens
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

// causalUnit is one indivisible piece of history: an assistant tool_call plus
// the tool messages that answer it, or a single standalone message.
type causalUnit struct {
	// indexes are the message positions this unit owns, in history order.
	indexes []int
	// tier is budgetTier of the unit: 0 system, 1 tool-causal, 2 plain. A unit
	// takes the LOWEST tier of its members, so a system message inside a unit
	// protects the whole unit (nothing half-droppable).
	tier int
	// tokens is the unit's estimated cost.
	tokens int
}

// causalUnits groups a history into indivisible units. The grouping is
// deliberately conservative: a missing result still binds to its call (the
// active turn's call has no result YET), and a tool message with no matching
// call becomes a unit of its own, so an already-orphaned message cannot take a
// healthy pair down with it.
func causalUnits(msgs []Message) []causalUnit {
	units := make([]causalUnit, 0, len(msgs))

	i := 0
	for i < len(msgs) {
		msg := msgs[i]
		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			// pending is per-unit: it holds the call ids this one decision
			// issued, so a result belongs to the decision that is still owed an
			// answer, never to an older one.
			pending := make(map[string]bool, len(msg.ToolCalls))
			unit := causalUnit{indexes: []int{i}, tier: budgetTier(msg), tokens: estimateMessageTokens(msg)}
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					pending[tc.ID] = true
				}
			}
			// Absorb the results that answer this call. The loop stops at the
			// next decision (a plain message or another call): a result that
			// arrives after that belongs to a different fact.
			for j := i + 1; j < len(msgs); j++ {
				next := msgs[j]
				if next.Role != RoleToolResult && next.Role != RoleToolCall {
					break
				}
				if len(pending) == 0 {
					break
				}
				if next.ToolCallID != "" && !pending[next.ToolCallID] {
					break
				}
				// An EMPTY ToolCallID inside a pending window is absorbed: it
				// cannot be matched to another call, and leaving it behind
				// would orphan it the moment this unit is dropped. Conservative
				// on purpose — an unattributable result stays with the call
				// that was in flight, and the two die together. (Pinned by
				// TestCausalUnits_EmptyIDResultAbsorbedIntoPendingCall.)
				delete(pending, next.ToolCallID)
				unit.indexes = append(unit.indexes, j)
				unit.tokens += estimateMessageTokens(next)
				if t := budgetTier(next); t < unit.tier {
					unit.tier = t
				}
				i = j
			}
			units = append(units, unit)
			i++
			continue
		}

		units = append(units, causalUnit{
			indexes: []int{i},
			tier:    budgetTier(msg),
			tokens:  estimateMessageTokens(msg),
		})
		i++
	}
	return units
}
