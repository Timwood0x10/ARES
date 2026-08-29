package sub

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llm/output"
	resources_core "github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedChatClient returns a fixed sequence of Chat responses, recording the
// messages it saw on each call so a test can prove a resumed quantum received
// the accumulated conversation (continuation, not a restart).
type scriptedChatClient struct {
	mu     sync.Mutex
	rounds []func(messages []*core.LLMMessage) *core.GenerateResponse
	seen   [][]*core.LLMMessage
}

func (c *scriptedChatClient) Chat(_ context.Context, messages []*core.LLMMessage, _ []core.Tool, _ map[string]any) (*core.GenerateResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	step := len(c.seen)
	c.seen = append(c.seen, messages)
	if step >= len(c.rounds) {
		return nil, fmt.Errorf("unexpected extra chat call %d", step)
	}
	return c.rounds[step](messages), nil
}

func (c *scriptedChatClient) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}

func (c *scriptedChatClient) lastMessages() []*core.LLMMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen[len(c.seen)-1]
}

// newStepExecutor returns a taskExecutor configured for the Chat+tools path
// with the scripted client, mirroring the executor_tool_loop_test.go fixtures.
func newStepExecutor(t *testing.T, client ChatClient, maxRounds int) *taskExecutor {
	t.Helper()
	return &taskExecutor{
		chatClient:    client,
		toolBinder:    &stubToolBinder{},
		llmAdapter:    &answerAdapter{},
		maxToolRounds: maxRounds,
		template:      output.NewTemplateEngine(),
		promptTpl:     ares_config.DefaultRecommendationPrompt,
		validator:     output.NewValidator(),
		maxRetries:    1,
		agentID:       "worker_test",
	}
}

// TestExecuteStepYieldsThenResumes is the contract test for P1.1 Execution
// Quantum at the executor level: quantum 1 (a tool call round) yields with a
// resumable checkpoint, and quantum 2 (resumed from that checkpoint) completes
// with a parsed final answer — proving the loop is yieldable, not all-or-nothing.
func TestExecuteStepYieldsThenResumes(t *testing.T) {
	client := &scriptedChatClient{rounds: []func(messages []*core.LLMMessage) *core.GenerateResponse{
		// Round 1: the LLM requests a tool call.
		func(messages []*core.LLMMessage) *core.GenerateResponse {
			return &core.GenerateResponse{ToolCalls: []core.ToolCall{
				{ID: "call_1", Type: "function", Function: core.FunctionCall{Name: "web_search", Arguments: `{"q":"kyoto"}`}},
			}}
		},
		// Round 2: the LLM answers with a final text result.
		func(messages []*core.LLMMessage) *core.GenerateResponse {
			return &core.GenerateResponse{Content: `[{"item_id":"i1","category":"general","name":"Result","description":"answer"}]`}
		},
	}}
	e := newStepExecutor(t, client, 5)

	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})
	task.Payload = map[string]any{"task_desc": "Plan a trip to Kyoto"}

	// Quantum 1: a tool round is not a terminal state.
	out, err := e.ExecuteStep(t.Context(), task)
	require.NoError(t, err)
	require.False(t, out.Done, "a tool round must yield")
	require.Nil(t, out.Result)
	require.NotNil(t, out.Checkpoint, "a yielded quantum must carry a resumable checkpoint")

	// The scheduler re-surfaces the checkpoint as payload["checkpoint"] on resume.
	task.Payload["checkpoint"] = out.Checkpoint

	// Quantum 2: resumed execution completes with the final answer.
	out2, err := e.ExecuteStep(t.Context(), task)
	require.NoError(t, err)
	require.True(t, out2.Done)
	require.NotNil(t, out2.Result)
	require.True(t, out2.Result.Success)
	require.Len(t, out2.Result.Items, 1)

	// Continuation proof: round 2 saw user + assistant tool call + tool result
	// (3 messages) — the checkpoint actually carried the conversation forward.
	require.Equal(t, 2, client.calls())
	last := client.lastMessages()
	require.Len(t, last, 3)
	assert.Equal(t, "tool", last[2].Role)
	assert.Equal(t, "call_1", last[2].ToolCallID)
}

// TestExecuteStepTextOnlySingleQuantum verifies the no-chat path completes in
// a single quantum (text-only generation, no yield possible).
func TestExecuteStepTextOnlySingleQuantum(t *testing.T) {
	e := &taskExecutor{
		llmAdapter: &answerAdapter{},
		template:   output.NewTemplateEngine(),
		promptTpl:  ares_config.DefaultRecommendationPrompt,
		maxRetries: 1,
		agentID:    "worker_test",
	}
	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})
	task.Payload = map[string]any{"task_desc": "Plan a trip"}

	out, err := e.ExecuteStep(t.Context(), task)
	require.NoError(t, err)
	require.True(t, out.Done)
	require.True(t, out.Result.Success)
	require.Len(t, out.Result.Items, 1)
}

// TestExecuteStepRefusesForeignCheckpoint verifies the resume identity check
// code_rules: a checkpoint recorded for another task must be refused.
func TestExecuteStepRefusesForeignCheckpoint(t *testing.T) {
	e := newStepExecutor(t, &scriptedChatClient{rounds: []func(messages []*core.LLMMessage) *core.GenerateResponse{
		func(messages []*core.LLMMessage) *core.GenerateResponse {
			return &core.GenerateResponse{Content: `[{"item_id":"i1","category":"general","name":"R","description":"d"}]`}
		},
	}}, 5)
	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})
	task.Payload = map[string]any{"checkpoint": &chatStepState{SchemaVersion: stepSchemaVersion, TaskID: "other-task"}}

	_, err := e.ExecuteStep(t.Context(), task)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match task")
}

// TestExecuteStepRejectsUnknownSchema verifies the checkpoint schema guard
// code_rules: an unsupported schema version must be refused on resume.
func TestExecuteStepRejectsUnknownSchema(t *testing.T) {
	e := newStepExecutor(t, &scriptedChatClient{rounds: []func(messages []*core.LLMMessage) *core.GenerateResponse{
		func(messages []*core.LLMMessage) *core.GenerateResponse {
			return &core.GenerateResponse{Content: `[{"item_id":"i1","category":"general","name":"R","description":"d"}]`}
		},
	}}, 5)
	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})
	task.Payload = map[string]any{"checkpoint": &chatStepState{SchemaVersion: 999, TaskID: "t1"}}

	_, err := e.ExecuteStep(t.Context(), task)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema version")
}

// TestExecuteStepRoundBudgetExhaustedDegradesToTextOnly verifies a resumed
// checkpoint whose round budget is spent degrades to a text-only call (the
// same graceful-degradation contract as the full-run loop).
func TestExecuteStepRoundBudgetExhaustedDegradesToTextOnly(t *testing.T) {
	// Always-tool-calling client: the single quantum consumes the budget, the
	// resume degrades to the text-only adapter (answerAdapter returns a result).
	e := newStepExecutor(t, &loopChatClient{}, 1)
	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})
	task.Payload = map[string]any{"task_desc": "Plan a trip to Kyoto"}

	out, err := e.ExecuteStep(t.Context(), task)
	require.NoError(t, err)
	require.False(t, out.Done, "the only round was a tool round, not a final answer")
	task.Payload["checkpoint"] = out.Checkpoint

	out2, err := e.ExecuteStep(t.Context(), task)
	require.NoError(t, err)
	require.True(t, out2.Done)
	require.True(t, out2.Result.Success)
	require.Len(t, out2.Result.Items, 1)
}

// emptySchemasBinder has registered tools (ListTools non-empty) but an empty
// LLM-advertised schema set — the exact condition that caused
// available_tools=0 in scheduler_trace_with_logs.log when the active-tools
// subset was empty. The executor must still enter the Chat+tools path.
type emptySchemasBinder struct{ *stubToolBinder }

func (b *emptySchemasBinder) GetToolSchemas() []resources_core.ToolSchema {
	return nil
}

// TestExecuteWithLLMSingle_UsesChatWhenToolsRegisteredButSchemasEmpty is the
// regression for the run-log finding: the gate for the Chat+tools path must be
// the FULL registered tool set, not the (active-tools-filtered) advertised
// schema set. With ListTools=["web_search"] but GetToolSchemas()=nil the
// executor must still call the Chat API (the tool loop degrades internally
// when no schemas reach the model), instead of silently dropping to
// text-only and starving plan_tasks of available_tools.
// TestSubTaskResultEmit_ConcurrentEventStore is a data-race regression test for
// the emitSubTaskResult read of e.agentID. emitSubTaskResult builds its payload
// before calling emitEvent (which takes the read lock itself), so a direct,
// unlocked e.agentID read would race with the write in SetEventStore. Run with
// -race to tie the D9-style snapshot in emitSubTaskResult to a real detector.
func TestSubTaskResultEmit_ConcurrentEventStore(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	e := newStepExecutor(t, &scriptedChatClient{rounds: []func(messages []*core.LLMMessage) *core.GenerateResponse{
		func(messages []*core.LLMMessage) *core.GenerateResponse {
			return &core.GenerateResponse{Content: `{"category":"general","item_id":"x","name":"R","description":"ok"}`}
		},
	}}, 3)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_, _ = e.Execute(t.Context(), &models.Task{TaskID: "t-concurrent", AgentType: models.AgentTypeTop})
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.SetEventStore(store, "sub-concurrent")
		}
	}()
	wg.Wait()
}

func TestExecuteWithLLMSingle_UsesChatWhenToolsRegisteredButSchemasEmpty(t *testing.T) {
	client := &scriptedChatClient{rounds: []func(messages []*core.LLMMessage) *core.GenerateResponse{
		func(messages []*core.LLMMessage) *core.GenerateResponse {
			// The Chat API must be reached even with zero advertised schemas.
			return &core.GenerateResponse{Content: `{"category":"general","item_id":"x","name":"Result","description":"answer"}`}
		},
	}}
	e := newStepExecutor(t, client, 3)
	e.toolBinder = &emptySchemasBinder{stubToolBinder: &stubToolBinder{}}

	items, err := e.executeWithLLMSingle(t.Context(), &models.Task{TaskID: "t1"}, nil)
	require.NoError(t, err)
	require.Len(t, items, 1, "Chat path must be used and produce a parsed result")
	require.Equal(t, 1, client.calls(), "Chat API must be called exactly once")
}
