package sub

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llm/output"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// loopChatClient is a ChatClient stub that always answers with tool calls and
// never produces a final text answer — it simulates the model-behavior hazard
// where an agentic tool loop never converges.
type loopChatClient struct {
	calls int
}

func (c *loopChatClient) Chat(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool, params map[string]any) (*core.GenerateResponse, error) {
	c.calls++
	return &core.GenerateResponse{
		Content: "",
		ToolCalls: []core.ToolCall{
			{ID: "call_1", Type: "function", Function: core.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}},
		},
	}, nil
}

// answerAdapter is an LLMAdapter stub whose text-only generation returns a
// parsable recommendation answer — the target of the tool-loop degradation.
type answerAdapter struct{}

func (a *answerAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	return `[{"item_id":"i1","category":"general","name":"Result","description":"answer"}]`, nil
}

func (a *answerAdapter) GenerateWithParams(ctx context.Context, prompt string, params map[string]any) (string, error) {
	return a.Generate(ctx, prompt)
}

func (a *answerAdapter) GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error) {
	return nil, nil
}

func (a *answerAdapter) GenerateStream(ctx context.Context, prompt string) (<-chan output.StreamChunk, error) {
	return nil, nil
}

func (a *answerAdapter) GetModel() string { return "test" }

// stubToolBinder is a minimal ToolBinder that reports one tool so the
// executor takes the Chat+tools path, and executes tool calls successfully.
type stubToolBinder struct{}

func (b *stubToolBinder) BindTool(name string, fn func(ctx context.Context, args map[string]any) (any, error)) {
}
func (b *stubToolBinder) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	return map[string]any{"ok": true}, nil
}
func (b *stubToolBinder) ListTools() []string               { return []string{"web_search"} }
func (b *stubToolBinder) IsToolIdempotent(name string) bool { return true }
func (b *stubToolBinder) ListIdempotentTools() []string     { return []string{"web_search"} }
func (b *stubToolBinder) GetToolSchemas() []resources.ToolSchema {
	return []resources.ToolSchema{{Name: "web_search", Description: "search"}}
}
func (b *stubToolBinder) BridgeFromRegistry(registry *resources.Registry) {}
func (b *stubToolBinder) WithPlannerBridge(bridge interface {
	Execute(ctx context.Context, toolName string, params map[string]any, userRequest string) (resources.Result, error)
}) {
}

// TestExecuteWithChatAndToolsDegradesToTextOnly locks the graceful-degradation
// contract: when the agentic tool loop exceeds max rounds without a final
// answer, the executor must fall back to a plain text-only call with the same
// prompt instead of failing the task. Regression guard for the real serve run
// where the worker looped on ~20 tools and produced items=0
// ("exceeded max tool rounds (5) without final answer").
func TestExecuteWithChatAndToolsDegradesToTextOnly(t *testing.T) {
	e := &taskExecutor{
		chatClient:       &loopChatClient{},
		llmAdapter:       &answerAdapter{},
		maxToolRounds:    3, // small to keep the test fast
		template:         output.NewTemplateEngine(),
		promptTpl:        ares_config.DefaultRecommendationPrompt,
		validator:        output.NewValidator(),
		maxRetries:       1,
		logger:           nil,
		eventStore:       nil,
		agentID:          "worker_test",
		fallbackHandlers: nil,
	}
	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})
	task.Payload = map[string]any{"task_desc": "Plan a trip to Kyoto"}

	items, err := e.executeWithLLM(t.Context(), task, task.UserProfile)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "Result", items[0].Name)
}

// TestExecuteWithChatAndToolsToolLoopStillFailsWithoutAdapter verifies that a
// nil llmAdapter (no text-only path) surfaces the degradation failure instead
// of silently returning empty results.
func TestExecuteWithChatAndToolsToolLoopStillFailsWithoutAdapter(t *testing.T) {
	loop := &loopChatClient{}
	e := &taskExecutor{
		chatClient:    loop,
		toolBinder:    &stubToolBinder{},
		llmAdapter:    nil,
		maxToolRounds: 2,
		template:      output.NewTemplateEngine(),
		promptTpl:     ares_config.DefaultRecommendationPrompt,
		maxRetries:    1,
	}
	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})
	task.Payload = map[string]any{"task_desc": "Plan a trip to Kyoto"}

	_, err := e.executeWithLLM(t.Context(), task, task.UserProfile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no text-only adapter")
	assert.GreaterOrEqual(t, loop.calls, 2)
}
