package memory

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
)

// historyWithPairs builds a system message plus n assistant-tool_call /
// tool_result pairs, each pair large enough to matter to the budget.
func historyWithPairs(n int) []Message {
	body := strings.Repeat("x", 400)
	msgs := []Message{{Role: memctx.RoleSystem, Content: "you are helpful"}}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		msgs = append(msgs,
			Message{
				Role:      memctx.RoleAssistant,
				Content:   "because " + body,
				ToolCalls: []memctx.ToolCall{{ID: id, Type: "function", Function: memctx.ToolCallFunction{Name: "grep", Arguments: `{"query":"q"}`}}},
			},
			Message{Role: memctx.RoleToolResult, Content: body, ToolCallID: id},
		)
	}
	return msgs
}

// TestTrimToTokenBudget_NeverOrphansAToolResult is the regression test for the
// structural half of the causal chain: a tool_result whose tool_call was
// dropped is an effect with no cause, and the old per-message trim produced
// exactly that. Units are now all-or-nothing.
func TestTrimToTokenBudget_NeverOrphansAToolResult(t *testing.T) {
	msgs := historyWithPairs(4)
	before := estimateTokens(msgs)

	// A budget that fits the system message and roughly two pairs.
	pairCost := estimateMessageTokens(msgs[1]) + estimateMessageTokens(msgs[2])
	budget := estimateMessageTokens(msgs[0]) + 2*pairCost

	got, used := trimToTokenBudget(msgs, budget)
	require.Less(t, len(got), len(msgs), "the trim must actually drop history")
	assert.LessOrEqual(t, used, budget)

	keptCalls := make(map[string]bool)
	for _, m := range got {
		if m.Role == memctx.RoleAssistant {
			for _, tc := range m.ToolCalls {
				keptCalls[tc.ID] = true
			}
		}
	}
	for _, m := range got {
		if m.Role != memctx.RoleToolResult {
			continue
		}
		assert.True(t, keptCalls[m.ToolCallID],
			"tool_result %q survived without its tool_call: an observation with no action", m.ToolCallID)
	}

	// And the symmetric half: an intent must not survive without the action it
	// produced, or a successor reasons about a plan that never ran.
	keptResults := make(map[string]bool)
	for _, m := range got {
		if m.Role == memctx.RoleToolResult {
			keptResults[m.ToolCallID] = true
		}
	}
	for id := range keptCalls {
		assert.True(t, keptResults[id], "tool_call %q kept without its observation", id)
	}

	assert.Less(t, used, before, "usage must drop with the history")
}

// TestCausalUnits_GroupsCallWithItsResults pins the grouping rule itself: a call
// and its results are ONE unit regardless of how many results answer it.
func TestCausalUnits_GroupsCallWithItsResults(t *testing.T) {
	msgs := []Message{
		{Role: memctx.RoleUser, Content: "go"},
		{Role: memctx.RoleAssistant, Content: "why", ToolCalls: []memctx.ToolCall{
			{ID: "c1", Function: memctx.ToolCallFunction{Name: "a"}},
			{ID: "c2", Function: memctx.ToolCallFunction{Name: "b"}},
		}},
		{Role: memctx.RoleToolResult, Content: "r1", ToolCallID: "c1"},
		{Role: memctx.RoleToolResult, Content: "r2", ToolCallID: "c2"},
		{Role: memctx.RoleAssistant, Content: "done"},
	}

	units := causalUnits(msgs)
	require.Len(t, units, 3, "user, the two-call action with both observations, and the closing turn")
	assert.Equal(t, []int{0}, units[0].indexes)
	assert.Equal(t, []int{1, 2, 3}, units[1].indexes)
	assert.Equal(t, 1, units[1].tier, "a tool-causal unit is tier 1")
	assert.Equal(t, 2, units[2].tier)
}

// TestCausalUnits_OrphanResultStaysAlone: a result whose call is already gone
// must not drag a healthy neighbouring pair into its unit.
func TestCausalUnits_OrphanResultStaysAlone(t *testing.T) {
	msgs := []Message{
		{Role: memctx.RoleToolResult, Content: "orphan", ToolCallID: "gone"},
		{Role: memctx.RoleAssistant, Content: "why", ToolCalls: []memctx.ToolCall{
			{ID: "c1", Function: memctx.ToolCallFunction{Name: "a"}},
		}},
		{Role: memctx.RoleToolResult, Content: "r1", ToolCallID: "c1"},
	}

	units := causalUnits(msgs)
	require.Len(t, units, 2)
	assert.Equal(t, []int{0}, units[0].indexes)
	assert.Equal(t, []int{1, 2}, units[1].indexes)
}

// TestCausalUnits_SystemIsNeverDroppable pins the floor: the schema/system
// message survives every trim, which is asserted through the tier-0 unit.
func TestCausalUnits_SystemIsNeverDroppable(t *testing.T) {
	msgs := historyWithPairs(3)
	msgs = append(msgs, Message{Role: memctx.RoleSystem, Content: "schema"})

	got, _ := trimToTokenBudget(msgs, 10) // impossibly small: drop everything droppable
	var systems int
	for _, m := range got {
		if m.Role == memctx.RoleSystem {
			systems++
		}
	}
	assert.Equal(t, 2, systems, "system messages are the floor and are never dropped")
}

// TestCausalUnits_OutOfOrderResults: providers may answer call A and call B in
// either order. Both results belong to the ONE decision that issued them, so an
// out-of-order arrival must not split the pair into two units — splitting would
// let a trim drop one and keep the other.
func TestCausalUnits_OutOfOrderResults(t *testing.T) {
	msgs := []Message{
		{Role: memctx.RoleAssistant, Content: "why", ToolCalls: []memctx.ToolCall{
			{ID: "A", Function: memctx.ToolCallFunction{Name: "a"}},
			{ID: "B", Function: memctx.ToolCallFunction{Name: "b"}},
		}},
		{Role: memctx.RoleToolResult, Content: "result B", ToolCallID: "B"},
		{Role: memctx.RoleToolResult, Content: "result A", ToolCallID: "A"},
	}

	units := causalUnits(msgs)
	require.Len(t, units, 1, "both answers belong to the decision that issued both calls")
	assert.Equal(t, []int{0, 1, 2}, units[0].indexes)

	// End to end: the unit is dropped whole, so neither result survives alone.
	got, _ := trimToTokenBudget(msgs, 1)
	assert.Empty(t, got)
}

// TestCausalUnits_EmptyIDResultAbsorbedIntoPendingCall pins a CONSERVATIVE
// behaviour that is easy to lose in a refactor: a tool result with no
// ToolCallID arriving inside a pending window cannot be matched to anything, so
// it is absorbed into the in-flight decision rather than left as its own unit.
// Left alone it would be orphaned the moment the call is dropped — or worse,
// kept alone as an observation with no action.
func TestCausalUnits_EmptyIDResultAbsorbedIntoPendingCall(t *testing.T) {
	msgs := []Message{
		{Role: memctx.RoleAssistant, Content: "why", ToolCalls: []memctx.ToolCall{
			{ID: "A", Function: memctx.ToolCallFunction{Name: "a"}},
		}},
		{Role: memctx.RoleToolResult, Content: "unattributed output", ToolCallID: ""},
	}

	units := causalUnits(msgs)
	require.Len(t, units, 1)
	assert.Equal(t, []int{0, 1}, units[0].indexes)
	assert.Equal(t, 1, units[0].tier)
}

// TestTrimToTokenBudget_ExhaustedStaysSystemOnly: when even dropping every
// droppable unit leaves the estimate over budget, the trim must terminate,
// return the system floor intact, and not panic. "Over budget" is a reporting
// fact for the caller, not an error to force by dropping the floor too.
func TestTrimToTokenBudget_ExhaustedStaysSystemOnly(t *testing.T) {
	msgs := historyWithPairs(3)
	before := estimateTokens(msgs)

	got, used := trimToTokenBudget(msgs, 1) // unsatisfiable on purpose
	require.Len(t, got, 1, "only the system message can survive")
	assert.Equal(t, memctx.RoleSystem, got[0].Role)
	assert.Equal(t, "you are helpful", got[0].Content, "the system message is preserved verbatim")
	assert.Greater(t, used, 0, "usage is still reported after an unsatisfiable trim")
	assert.Less(t, used, before, "and it did drop everything droppable")
}
