package agentfabric

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llm/output"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// budgetToolBinder advertises TWO tools so budget filtering is observable: with a
// single registered tool every filter outcome is either "all" or "none", which
// cannot distinguish real gating from the zero-tools fallback.
type budgetToolBinder struct {
	mu       sync.Mutex
	seen     []string
	failTool string // when non-empty, CallTool fails for this tool
}

func (b *budgetToolBinder) CallTool(_ context.Context, name string, _ map[string]any) (any, error) {
	b.mu.Lock()
	b.seen = append(b.seen, name)
	b.mu.Unlock()
	if b.failTool != "" && name == b.failTool {
		return nil, errors.New("boom")
	}
	return map[string]any{"ok": true}, nil
}

func (b *budgetToolBinder) ListTools() []string { return []string{"web_search", "calculator"} }

func (b *budgetToolBinder) IsToolIdempotent(string) bool { return true }

func (b *budgetToolBinder) GetToolSchemas() []resources.ToolSchema {
	return []resources.ToolSchema{
		{Name: "web_search", Description: "search"},
		{Name: "calculator", Description: "compute"},
	}
}

// offerRecordingClient records the tool list advertised on every round and always
// asks for the same tool, so a budget must eventually withdraw it.
type offerRecordingClient struct {
	mu       sync.Mutex
	toolName string
	offered  [][]string
}

func (c *offerRecordingClient) Chat(_ context.Context, _ []*core.LLMMessage, tools []core.Tool, _ map[string]any) (*core.GenerateResponse, error) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Function.Name)
	}
	c.mu.Lock()
	c.offered = append(c.offered, names)
	c.mu.Unlock()
	return &core.GenerateResponse{
		Content: "calling the tool",
		ToolCalls: []core.ToolCall{{
			ID:       "call-1",
			Type:     "function",
			Function: core.FunctionCall{Name: c.toolName, Arguments: `{}`},
		}},
	}, nil
}

func (c *offerRecordingClient) round(i int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i >= len(c.offered) {
		return nil
	}
	return c.offered[i]
}

func newBudgetCognition(t *testing.T, client ChatClient, binder ToolBinder) *chatCognition {
	t.Helper()
	cog, err := NewChatCognition(ChatCognitionDeps{
		ChatClient:     client,
		ToolBinder:     binder,
		Template:       output.NewTemplateEngine(),
		PromptTemplate: "do {{.input}}",
		AgentID:        "budget-agent",
	})
	if err != nil {
		t.Fatalf("NewChatCognition: %v", err)
	}
	c, ok := cog.(*chatCognition)
	if !ok {
		t.Fatalf("unexpected cognition type %T", cog)
	}
	return c
}

// newBudgetTask wraps a state as the resume payload the scheduler surfaces.
func newBudgetTask(st *chatStepState) *models.Task {
	task := models.NewTask("t-budget", models.AgentType("code"), nil)
	task.Payload = map[string]any{"checkpoint": st}
	return task
}

func newBudgetStepState(params map[string]any) *chatStepState {
	return &chatStepState{
		SchemaVersion: stepSchemaVersion,
		TaskID:        "t-budget",
		MaxRounds:     5,
		Prompt:        "do something",
		Params:        params,
		Messages:      []*core.LLMMessage{{Role: "user", Content: "do something"}},
	}
}

// TestChatCognitionBudgetWithdrawsExhaustedTool is the C5 acceptance on the
// fabric execution body: once a tool has been called `budget` times in this
// session it disappears from the advertised set, while unspent tools remain.
// Before the gate an evolved budget was inert — the LLM kept being offered the
// tool and kept calling it.
func TestChatCognitionBudgetWithdrawsExhaustedTool(t *testing.T) {
	client := &offerRecordingClient{toolName: "web_search"}
	c := newBudgetCognition(t, client, &budgetToolBinder{})
	st := newBudgetStepState(map[string]any{agents.ParamKeyBudget: 1})

	if _, done, err := c.chatStep(context.Background(), st); err != nil || done {
		t.Fatalf("round 1: done=%v err=%v", done, err)
	}
	if got := client.round(0); len(got) != 2 {
		t.Fatalf("round 1 must offer both tools, got %v", got)
	}
	if st.ToolUses["web_search"] != 1 {
		t.Fatalf("call must be counted, got %v", st.ToolUses)
	}

	if _, _, err := c.chatStep(context.Background(), st); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	got := client.round(1)
	if len(got) != 1 || got[0] != "calculator" {
		t.Fatalf("exhausted tool must be withdrawn and the unspent one kept, got %v", got)
	}
}

// TestChatCognitionBudgetFailedCallStillSpends is why the counter is incremented
// BEFORE execution: if a failing tool did not spend its budget, a tool that
// always errors would be retried every round and the cap would never bind.
func TestChatCognitionBudgetFailedCallStillSpends(t *testing.T) {
	client := &offerRecordingClient{toolName: "web_search"}
	c := newBudgetCognition(t, client, &budgetToolBinder{failTool: "web_search"})
	st := newBudgetStepState(map[string]any{agents.ParamKeyBudget: 1})

	if _, _, err := c.chatStep(context.Background(), st); err != nil {
		t.Fatalf("a tool error is reported to the LLM, not raised as a step error: %v", err)
	}
	if st.ToolUses["web_search"] != 1 {
		t.Fatalf("a failed call must still spend its budget, got %v", st.ToolUses)
	}

	if _, _, err := c.chatStep(context.Background(), st); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if got := client.round(1); len(got) != 1 || got[0] != "calculator" {
		t.Fatalf("failing tool must be withdrawn after its budget, got %v", got)
	}
}

// TestChatCognitionBudgetNeverAdvertisesZeroTools locks the same dead-end guard
// the whitelist has: when every advertised tool is exhausted, fall back to the
// full set rather than hand the model an empty tool list.
func TestChatCognitionBudgetNeverAdvertisesZeroTools(t *testing.T) {
	client := &offerRecordingClient{toolName: "web_search"}
	c := newBudgetCognition(t, client, &budgetToolBinder{})
	st := newBudgetStepState(map[string]any{agents.ParamKeyBudget: 1})
	st.ToolUses = map[string]int{"web_search": 1, "calculator": 1}

	if _, _, err := c.chatStep(context.Background(), st); err != nil {
		t.Fatalf("chatStep: %v", err)
	}
	if got := client.round(0); len(got) != 2 {
		t.Fatalf("an all-exhausted budget must not degrade to zero tools, got %v", got)
	}
}

// TestChatCognitionBudgetSurvivesCheckpointRoundTrip is the persistence half of
// C5: the counter rides the checkpoint, so a resumed task does not get a fresh
// budget. Without this, every yield would reset the cap and it would never bind.
func TestChatCognitionBudgetSurvivesCheckpointRoundTrip(t *testing.T) {
	client := &offerRecordingClient{toolName: "web_search"}
	c := newBudgetCognition(t, client, &budgetToolBinder{})
	st := newBudgetStepState(map[string]any{agents.ParamKeyBudget: 1})

	if _, _, err := c.chatStep(context.Background(), st); err != nil {
		t.Fatalf("round 1: %v", err)
	}

	// Round-trip the checkpoint exactly as the scheduler does on resume.
	task := newBudgetTask(st)
	restored, found, err := c.decodeChatStepState(task)
	if err != nil || !found {
		t.Fatalf("decode checkpoint: found=%v err=%v", found, err)
	}
	if restored.ToolUses["web_search"] != 1 {
		t.Fatalf("budget usage must survive the checkpoint, got %v", restored.ToolUses)
	}

	if _, _, err := c.chatStep(context.Background(), restored); err != nil {
		t.Fatalf("resumed round: %v", err)
	}
	if got := client.round(1); len(got) != 1 || got[0] != "calculator" {
		t.Fatalf("resumed round must honor the spent budget, got %v", got)
	}
}

// burstClient returns n calls to the same tool in ONE round: the schema gate
// runs once per round, so without per-call enforcement one round can overshoot
// the cap by up to n-1.
type burstClient struct {
	toolName string
	n        int
}

func (c *burstClient) Chat(_ context.Context, _ []*core.LLMMessage, _ []core.Tool, _ map[string]any) (*core.GenerateResponse, error) {
	calls := make([]core.ToolCall, 0, c.n)
	for i := 0; i < c.n; i++ {
		// Distinct IDs keep the conversation well-formed (one reply per call).
		calls = append(calls, core.ToolCall{
			ID:       "call-burst-" + string(rune('0'+i)),
			Type:     "function",
			Function: core.FunctionCall{Name: c.toolName, Arguments: `{}`},
		})
	}
	return &core.GenerateResponse{Content: "burst", ToolCalls: calls}, nil
}

func (b *budgetToolBinder) callCount(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, seen := range b.seen {
		if seen == name {
			n++
		}
	}
	return n
}

// TestChatCognitionBudgetBindsWithinOneRound locks the intra-round half of C5:
// with budget=1 and two same-tool calls in a single round, exactly one call
// reaches the binder. The schema gate alone cannot do this — it filters once
// per round, before the LLM answers.
func TestChatCognitionBudgetBindsWithinOneRound(t *testing.T) {
	binder := &budgetToolBinder{}
	c := newBudgetCognition(t, &burstClient{toolName: "web_search", n: 2}, binder)
	st := newBudgetStepState(map[string]any{agents.ParamKeyBudget: 1})

	if _, _, err := c.chatStep(context.Background(), st); err != nil {
		t.Fatalf("chatStep: %v", err)
	}
	if got := binder.callCount("web_search"); got != 1 {
		t.Fatalf("exactly one call must reach the binder, got %d", got)
	}
	if st.ToolUses["web_search"] != 1 {
		t.Fatalf("only the executed call spends budget, got %v", st.ToolUses)
	}
	// The skipped call still gets a paired tool reply (skipped, not executed),
	// so the conversation stays well-formed for the next round.
	skipped := 0
	for _, m := range st.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "skipped: per-session budget") {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("the over-budget call must be reported as a skipped observation, got %d skip replies", skipped)
	}
}

// TestChatCognitionNoBudgetIsUnlimited guards the zero value: no budget in
// Params leaves the advertised set untouched no matter how many calls were made,
// so the non-evolved path is byte-identical to before the gate.
func TestChatCognitionNoBudgetIsUnlimited(t *testing.T) {
	client := &offerRecordingClient{toolName: "web_search"}
	c := newBudgetCognition(t, client, &budgetToolBinder{})
	st := newBudgetStepState(map[string]any{"temperature": 0.7})
	st.ToolUses = map[string]int{"web_search": 99}

	if _, _, err := c.chatStep(context.Background(), st); err != nil {
		t.Fatalf("chatStep: %v", err)
	}
	if got := client.round(0); len(got) != 2 {
		t.Fatalf("no budget means unlimited, got %v", got)
	}
}
