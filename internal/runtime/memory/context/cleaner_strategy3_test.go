// cleaner_strategy3_test.go locks REVIEW 3.3#3: the structural-linkage
// grouping (strategy 3) was unreachable because groupByUserBoundary always
// returns at least one turn, and — had it been reachable — split turns at
// the WRONG boundary (the delimiting user message ended the previous turn
// instead of starting the new one, inconsistent with strategy 2).
package context

import "testing"

func TestGroupByStructuralLinkageUserStartsNewTurn(t *testing.T) {
	msgs := []Message{
		testMsg(RoleUser, "question 1"),
		testMsg(RoleAssistant, "answer 1"),
		testMsg(RoleUser, "question 2"),
		testMsg(RoleAssistant, "answer 2"),
	}

	turns := groupByStructuralLinkage(msgs)

	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d: %+v", len(turns), turns)
	}
	if turns[0][0].Content != "question 1" || turns[0][1].Content != "answer 1" {
		t.Errorf("turn 0 boundary wrong: %+v", turns[0])
	}
	// The second user message must START turn 1, not end turn 0.
	if turns[1][0].Content != "question 2" {
		t.Errorf("turn 1 must begin with the user message, got %q", turns[1][0].Content)
	}
	if len(turns[0]) != 2 {
		t.Errorf("turn 0 must have exactly 2 messages, got %d", len(turns[0]))
	}
}

func TestGroupByStructuralLinkageHoldsTurnWhileToolPending(t *testing.T) {
	// A user interjection while a tool call is pending stays inside the
	// active structural unit instead of splitting it.
	toolResult := testMsg(RoleToolResult, "result")
	toolResult.ToolCallID = "c1" // completes the pending call
	msgs := []Message{
		testMsg(RoleUser, "look this up"),
		testMsgWithTool(RoleAssistant, "searching", []ToolCall{{ID: "c1"}}),
		testMsg(RoleUser, "and hurry"),
		toolResult,
		testMsg(RoleAssistant, "final answer"),
		testMsg(RoleUser, "next question"),
		testMsg(RoleAssistant, "next answer"),
	}

	turns := groupByStructuralLinkage(msgs)

	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d: %+v", len(turns), turns)
	}
	if turns[0][0].Content != "look this up" {
		t.Errorf("turn 0 must start at the first user message")
	}
	if turns[0][len(turns[0])-1].Content != "final answer" {
		t.Errorf("turn 0 must extend until the unit completes, got last message %q",
			turns[0][len(turns[0])-1].Content)
	}
	if turns[1][0].Content != "next question" {
		t.Errorf("turn 1 must start at the post-completion user message, got %q", turns[1][0].Content)
	}
}

func TestGroupIntoTurnsFallsBackWithoutUserMessages(t *testing.T) {
	// No user messages: strategy 2 has no boundaries to offer. The
	// structural-linkage path must be reachable (pre-fix it was dead code)
	// and group everything into one turn.
	msgs := []Message{
		testMsg(RoleSystem, "boot"),
		testMsgWithTool(RoleAssistant, "call tool", []ToolCall{{ID: "c1"}}),
		testMsg(RoleToolResult, "result"),
		testMsg(RoleAssistant, "done"),
	}

	turns := groupIntoTurns(msgs)

	if len(turns) != 1 {
		t.Fatalf("expected 1 turn for a user-less conversation, got %d", len(turns))
	}
	if len(turns[0]) != 4 {
		t.Errorf("expected all 4 messages in the single turn, got %d", len(turns[0]))
	}
}
