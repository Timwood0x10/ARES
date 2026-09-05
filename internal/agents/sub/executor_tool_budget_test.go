package sub

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/llm/output"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// multiToolBinder registers two tools so budget filtering is observable: with a
// single registered tool every filter result is either "all" or "none", which
// cannot distinguish gating from the zero-tools fallback.
type multiToolBinder struct {
	stubToolBinder
	failTool string // when non-empty, CallTool fails for this tool
}

func (b *multiToolBinder) GetToolSchemas() []resources.ToolSchema {
	return []resources.ToolSchema{
		{Name: "web_search", Description: "search"},
		{Name: "calculator", Description: "compute"},
	}
}

func (b *multiToolBinder) ListTools() []string { return []string{"web_search", "calculator"} }

func (b *multiToolBinder) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	if b.failTool != "" && name == b.failTool {
		return nil, errors.New("boom")
	}
	return map[string]any{"ok": true}, nil
}

// recordingChatClient captures the tool list offered on every round and always
// requests the same tool, so a budget must eventually withdraw it.
type recordingChatClient struct {
	toolName  string
	offered   [][]string
	callCount int
}

func (c *recordingChatClient) Chat(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool, params map[string]any) (*core.GenerateResponse, error) {
	c.callCount++
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Function.Name)
	}
	c.offered = append(c.offered, names)
	return &core.GenerateResponse{
		ToolCalls: []core.ToolCall{
			{ID: "call_1", Type: "function", Function: core.FunctionCall{Name: c.toolName, Arguments: `{}`}},
		},
	}, nil
}

func newBudgetExecutor(t *testing.T, client ChatClient, binder ToolBinder) *taskExecutor {
	t.Helper()
	return &taskExecutor{
		chatClient:    client,
		toolBinder:    binder,
		maxToolRounds: 5,
		template:      output.NewTemplateEngine(),
		promptTpl:     ares_config.DefaultRecommendationPrompt,
		maxRetries:    1,
		agentID:       "worker_test",
	}
}

func newBudgetState(params map[string]any) *chatStepState {
	return &chatStepState{
		SchemaVersion: stepSchemaVersion,
		MaxRounds:     5,
		Prompt:        "do something",
		Params:        params,
		Messages:      []*core.LLMMessage{{Role: "user", Content: "do something"}},
	}
}

// TestToolBudgetWithdrawsExhaustedTool locks the C5 budget gate: once a tool has
// been called `budget` times in this session it is removed from the advertised
// set, while the remaining tools stay available. Without the gate an evolved
// budget was inert — the LLM kept being offered the tool and kept calling it.
func TestToolBudgetWithdrawsExhaustedTool(t *testing.T) {
	client := &recordingChatClient{toolName: "web_search"}
	e := newBudgetExecutor(t, client, &multiToolBinder{})
	st := newBudgetState(map[string]any{agents.ParamKeyBudget: 1})

	// Round 1: both tools offered, web_search called once (budget now spent).
	_, done, err := e.chatStep(t.Context(), st)
	require.NoError(t, err)
	require.False(t, done)
	assert.ElementsMatch(t, []string{"web_search", "calculator"}, client.offered[0])
	assert.Equal(t, 1, st.ToolUses["web_search"])

	// Round 2: web_search is withdrawn, calculator survives.
	_, _, err = e.chatStep(t.Context(), st)
	require.NoError(t, err)
	require.Len(t, client.offered, 2)
	assert.Equal(t, []string{"calculator"}, client.offered[1],
		"the exhausted tool must be withdrawn, the unspent one kept")
}

// TestToolBudgetFailedCallStillSpendsBudget is the reason the counter is
// incremented BEFORE execution: if a failing tool did not spend its budget, a
// tool that always errors would be retried every round and the cap would never
// bind — exactly the runaway the budget exists to stop.
func TestToolBudgetFailedCallStillSpendsBudget(t *testing.T) {
	client := &recordingChatClient{toolName: "web_search"}
	e := newBudgetExecutor(t, client, &multiToolBinder{failTool: "web_search"})
	st := newBudgetState(map[string]any{agents.ParamKeyBudget: 1})

	_, _, err := e.chatStep(t.Context(), st)
	require.NoError(t, err, "a tool error is reported to the LLM, not raised as a step error")
	assert.Equal(t, 1, st.ToolUses["web_search"], "a failed call must still spend its budget")

	_, _, err = e.chatStep(t.Context(), st)
	require.NoError(t, err)
	assert.Equal(t, []string{"calculator"}, client.offered[1])
}

// TestToolBudgetNeverAdvertisesZeroTools locks the same dead-end guard the
// whitelist has: when every advertised tool is exhausted the executor must fall
// back to the full set rather than hand the model an empty tool list.
func TestToolBudgetNeverAdvertisesZeroTools(t *testing.T) {
	client := &recordingChatClient{toolName: "web_search"}
	e := newBudgetExecutor(t, client, &multiToolBinder{})
	st := newBudgetState(map[string]any{agents.ParamKeyBudget: 1})
	// Pre-spend the budget of BOTH registered tools.
	st.ToolUses = map[string]int{"web_search": 1, "calculator": 1}

	_, _, err := e.chatStep(t.Context(), st)
	require.NoError(t, err)
	require.Len(t, client.offered, 1)
	assert.ElementsMatch(t, []string{"web_search", "calculator"}, client.offered[0],
		"an all-exhausted budget must not degrade to zero tools")
}

// TestToolBudgetZeroMeansUnlimited verifies the zero-value default: no budget in
// Params leaves the advertised set untouched no matter how many calls were made.
func TestToolBudgetZeroMeansUnlimited(t *testing.T) {
	client := &recordingChatClient{toolName: "web_search"}
	e := newBudgetExecutor(t, client, &multiToolBinder{})
	st := newBudgetState(map[string]any{"temperature": 0.7})
	st.ToolUses = map[string]int{"web_search": 99}

	_, _, err := e.chatStep(t.Context(), st)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"web_search", "calculator"}, client.offered[0])
}

// burstChatClient returns n calls to the same tool in ONE round: the schema
// gate runs once per round, so without per-call enforcement one round can
// overshoot the cap by up to n-1.
type burstChatClient struct {
	toolName string
	n        int
}

func (c *burstChatClient) Chat(_ context.Context, _ []*core.LLMMessage, _ []core.Tool, _ map[string]any) (*core.GenerateResponse, error) {
	calls := make([]core.ToolCall, 0, c.n)
	for i := 0; i < c.n; i++ {
		calls = append(calls, core.ToolCall{
			ID:       "call-burst-" + string(rune('0'+i)),
			Type:     "function",
			Function: core.FunctionCall{Name: c.toolName, Arguments: `{}`},
		})
	}
	return &core.GenerateResponse{Content: "burst", ToolCalls: calls}, nil
}

// countingBinder records every executed call so the test can prove a skipped
// call never reached the tool.
type countingBinder struct {
	ToolBinder
	mu    sync.Mutex
	calls []string
}

func (b *countingBinder) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	b.mu.Lock()
	b.calls = append(b.calls, name)
	b.mu.Unlock()
	return b.ToolBinder.CallTool(ctx, name, args)
}

func (b *countingBinder) callCount(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, seen := range b.calls {
		if seen == name {
			n++
		}
	}
	return n
}

// TestToolBudgetBindsWithinOneRound locks the intra-round half of C5: with
// budget=1 and two same-tool calls in a single round, exactly one call
// executes. The schema gate alone cannot do this — it filters once per round,
// before the LLM answers.
func TestToolBudgetBindsWithinOneRound(t *testing.T) {
	binder := &countingBinder{ToolBinder: &multiToolBinder{}}
	e := newBudgetExecutor(t, &burstChatClient{toolName: "web_search", n: 2}, binder)
	st := newBudgetState(map[string]any{agents.ParamKeyBudget: 1})

	_, _, err := e.chatStep(t.Context(), st)
	require.NoError(t, err)
	assert.Equal(t, 1, binder.callCount("web_search"), "exactly one call must reach the tool")
	assert.Equal(t, 1, st.ToolUses["web_search"], "only the executed call spends budget")

	skipped := 0
	for _, m := range st.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "skipped: per-session budget") {
			skipped++
		}
	}
	assert.Equal(t, 1, skipped, "the over-budget call must be reported as a skipped observation")
}

// TestToolBudgetComposesWithWhitelist checks the two gates stack in the right
// order: the whitelist narrows first, then the budget withdraws from what is
// left. A whitelisted-but-exhausted tool must fall back to the full set rather
// than produce an empty list.
func TestToolBudgetComposesWithWhitelist(t *testing.T) {
	client := &recordingChatClient{toolName: "web_search"}
	e := newBudgetExecutor(t, client, &multiToolBinder{})
	st := newBudgetState(map[string]any{agents.ParamKeyTools: "web_search", agents.ParamKeyBudget: 1})

	// Round 1: whitelist narrows to web_search only.
	_, _, err := e.chatStep(t.Context(), st)
	require.NoError(t, err)
	assert.Equal(t, []string{"web_search"}, client.offered[0])

	// Round 2: the only whitelisted tool is exhausted → fallback keeps a
	// non-empty list instead of stalling the model.
	_, _, err = e.chatStep(t.Context(), st)
	require.NoError(t, err)
	assert.NotEmpty(t, client.offered[1])
}
