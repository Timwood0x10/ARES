//go:build integration

package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/llmcore"
)

// TestLive_MeasureRationaleContentAvailability answers the question
// docs/defects/cognition-persistence.md defers the `record_rationale` decision to:
// "工具调用回合 `resp.Content` 通常为空".
//
// The planner detects a final answer by `len(resp.ToolCalls) == 0`, so on the turns that
// matter most for a rationale — the ones that DO something — the text may not exist at
// all. If it reliably does not, then storing `resp.Content` as "the rationale" stores
// nothing exactly where a reader would want it, and the meta-tool (that document's
// option ①) is the only contract that can carry it.
//
// This is a MEASUREMENT, not a behaviour test. It logs counts and asserts only that the
// measurement was VALID (the model actually chose the tool at least once), because a run
// where the model never called the tool would report "no tool turns" as though it were
// "every tool turn was empty".
func TestLive_MeasureRationaleContentAvailability(t *testing.T) {
	client := liveClient(t)

	tools := []llmcore.Tool{{
		Type: "function",
		Function: llmcore.FunctionDefinition{
			Name:        "lookup_price",
			Description: "Look up the price of a product by name. Use this whenever the user asks for a price.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
				},
				"required": []string{"name"},
			},
		},
	}}

	const runs = 5
	var toolTurns, toolTurnsWithContent int
	var answerTurns, answerTurnsWithContent int
	contentRunes := make([]int, 0, runs)

	for i := 0; i < runs; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		resp, err := client.Chat(ctx, []*llmcore.LLMMessage{{
			Role:    "user",
			Content: fmt.Sprintf("What does the widget cost? (run %d)", i+1),
		}}, tools, nil)
		cancel()
		require.NoErrorf(t, err, "run %d", i+1)

		if len(resp.ToolCalls) > 0 {
			toolTurns++
			trimmed := strings.TrimSpace(resp.Content)
			contentRunes = append(contentRunes, len(trimmed))
			if trimmed != "" {
				toolTurnsWithContent++
			}
			continue
		}
		answerTurns++
		if strings.TrimSpace(resp.Content) != "" {
			answerTurnsWithContent++
		}
	}

	t.Logf("P0-4A measurement over %d runs: tool-call turns=%d (non-empty Content=%d); final-answer turns=%d (non-empty=%d); tool-turn content runes=%v",
		runs, toolTurns, toolTurnsWithContent, answerTurns, answerTurnsWithContent, contentRunes)
	t.Logf("note: GenerateResponse carries no ReasoningContent field, so the doc's tier 1 (store the model's thinking) needs a type change before it is even available")

	require.Greater(t, toolTurns, 0,
		"the measurement is meaningless unless the model actually chose the tool at least once")

	if toolTurnsWithContent == 0 {
		t.Logf("VERDICT: tool-call turns carry NO content ⇒ on exactly the turns a reason matters, there is nothing to store ⇒ `record_rationale` (option ①) is the only contract that can carry it")
	} else {
		t.Logf("VERDICT: %d/%d tool-call turns carried content ⇒ storing resp.Content would cover part of the rationale, but the meta-tool is still the only DETERMINISTIC contract",
			toolTurnsWithContent, toolTurns)
	}
}
