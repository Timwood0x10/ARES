package agentfabric

import (
	"context"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llm/output"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// fakeChatClient drives the tool-loop's ReAct rounds. Each Chat call returns
// the next scripted response: first a tool call (so the quantum yields with a
// checkpoint), then a final text answer.
type fakeChatClient struct {
	mu       sync.Mutex
	calls    int
	toolName string
}

func (f *fakeChatClient) Chat(_ context.Context, messages []*core.LLMMessage, tools []core.Tool, params map[string]any) (*core.GenerateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return &core.GenerateResponse{
			Content: "calling the tool",
			ToolCalls: []core.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: core.FunctionCall{
					Name:      f.toolName,
					Arguments: `{"arg":"v"}`,
				},
			}},
		}, nil
	}
	return &core.GenerateResponse{Content: `{"items":[{"item_id":"i1","name":"result","content":"final answer"}]}`}, nil
}

// fakeToolBinder records tool executions and advertises one schema.
type fakeToolBinder struct {
	mu   sync.Mutex
	seen []string
}

func (b *fakeToolBinder) CallTool(_ context.Context, name string, _ map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seen = append(b.seen, name)
	return map[string]any{"ok": true}, nil
}
func (b *fakeToolBinder) ListTools() []string { return []string{"lookup"} }
func (b *fakeToolBinder) IsToolIdempotent(name string) bool {
	return name == "lookup"
}
func (b *fakeToolBinder) GetToolSchemas() []resources.ToolSchema {
	return []resources.ToolSchema{{Name: "lookup", Description: "look up data"}}
}

// TestChatCognitionQuantumSemantics is the A1.4 acceptance (aresos-agentos-plan
// A1.4: StepOutcome 语义与原 sub.StepOutcome 一致 — 迁移测试全绿). A fabric
// agent spawned with the default ChatCognition is directly executable: a
// quantum that triggers a tool call YIELDS with a resumable checkpoint (and
// the tool runs), the resume quantum completes with the final answer.
func TestChatCognitionQuantumSemantics(t *testing.T) {
	ctx := context.Background()
	f := NewFabric()

	client := &fakeChatClient{toolName: "lookup"}
	binder := &fakeToolBinder{}
	factory := func([]string) Cognition {
		cog, err := NewChatCognition(ChatCognitionDeps{
			ChatClient:     client,
			ToolBinder:     binder,
			Template:       output.NewTemplateEngine(),
			PromptTemplate: "do {{.input}}",
			AgentID:        "chat-agent",
		})
		if err != nil {
			t.Fatalf("NewChatCognition: %v", err)
		}
		return cog
	}
	if _, err := f.Spawn(ctx, SpawnSpec{
		Identity:         "chat-agent",
		Capabilities:     []string{"code"},
		CognitionFactory: factory,
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// The spawned fabric agent is directly executable — no sub.Agent wrapper.
	a, err := f.Get("chat-agent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !a.Executable() {
		t.Fatal("A1.4: fabric agent with ChatCognition must be executable")
	}

	// Quantum 1: the LLM asks for a tool → the quantum yields with a
	// checkpoint (Done=false), the tool already ran.
	task := models.NewTask("t-a14", models.AgentType("code"), nil)
	task.Payload = map[string]any{"task_desc": "research"}
	out1, err := a.ExecuteStep(ctx, task)
	if err != nil {
		t.Fatalf("quantum 1: %v", err)
	}
	if out1.Done {
		t.Fatal("quantum 1 must yield (tool call in flight)")
	}
	if out1.Checkpoint == nil {
		t.Fatal("quantum 1 must carry a resumable checkpoint")
	}
	binder.mu.Lock()
	seen := len(binder.seen)
	first := ""
	if seen > 0 {
		first = binder.seen[0]
	}
	binder.mu.Unlock()
	if seen != 1 || first != "lookup" {
		t.Fatalf("tool must have run once, got %v", binder.seen)
	}

	// Resume: surface the checkpoint as payload["checkpoint"] — exactly what
	// the scheduler's toModelTask does on a SUSPENDED→READY resume.
	task2 := models.NewTask("t-a14", models.AgentType("code"), nil)
	task2.Payload = map[string]any{"checkpoint": out1.Checkpoint}
	out2, err := a.ExecuteStep(ctx, task2)
	if err != nil {
		t.Fatalf("quantum 2: %v", err)
	}
	if !out2.Done {
		t.Fatal("quantum 2 must complete after the final answer")
	}
	if out2.Result == nil || len(out2.Result.Items) == 0 {
		t.Fatal("completed quantum must carry the final result items")
	}
}

// TestChatCognitionRefusesForeignCheckpoint verifies the resume identity check
// (§6.2) survives the move: a checkpoint for another task is refused, not
// executed.
func TestChatCognitionRefusesForeignCheckpoint(t *testing.T) {
	ctx := context.Background()
	cog, err := NewChatCognition(ChatCognitionDeps{
		ChatClient:     &fakeChatClient{},
		Template:       output.NewTemplateEngine(),
		PromptTemplate: "do {{.input}}",
		AgentID:        "x",
	})
	if err != nil {
		t.Fatalf("NewChatCognition: %v", err)
	}
	st := &chatStepState{SchemaVersion: stepSchemaVersion, TaskID: "other-task"}
	task := models.NewTask("this-task", models.AgentType("code"), nil)
	task.Payload = map[string]any{"checkpoint": st}
	if _, err := cog.ExecuteStep(ctx, task); err == nil {
		t.Fatal("foreign checkpoint must be refused")
	}
}

// fakeLLMAdapter is the text-only generation path.
type fakeLLMAdapter struct{}

func (fakeLLMAdapter) Generate(_ context.Context, _ string) (string, error) {
	return `{"items":[{"item_id":"i1","name":"r","content":"text only"}]}`, nil
}
func (fakeLLMAdapter) GenerateWithParams(_ context.Context, _ string, _ map[string]any) (string, error) {
	return `{"items":[{"item_id":"i1","name":"r","content":"text only"}]}`, nil
}
func (fakeLLMAdapter) GenerateStructured(_ context.Context, _ string, _ string) (*models.RecommendResult, error) {
	return nil, nil
}
func (fakeLLMAdapter) GenerateStream(_ context.Context, _ string) (<-chan output.StreamChunk, error) {
	return nil, nil
}
func (fakeLLMAdapter) GetModel() string { return "mock" }

// TestChatCognitionTextOnly verifies the no-tool degradation: without a chat
// client / tools, the cognition completes in one quantum through the text-only
// adapter.
func TestChatCognitionTextOnly(t *testing.T) {
	ctx := context.Background()
	cog, err := NewChatCognition(ChatCognitionDeps{
		LLMAdapter:     fakeLLMAdapter{},
		Template:       output.NewTemplateEngine(),
		PromptTemplate: "do {{.input}}",
		AgentID:        "text-agent",
	})
	if err != nil {
		t.Fatalf("NewChatCognition: %v", err)
	}
	out, err := cog.ExecuteStep(ctx, models.NewTask("t-text", models.AgentType("code"), nil))
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if !out.Done {
		t.Fatal("text-only quantum must complete in one step")
	}
	if out.Result == nil {
		t.Fatal("text-only quantum must carry a result")
	}
}

// TestNewChatCognitionRejectsNoExecutionPath verifies the fail-loud contract:
// a cognition with neither a chat client nor an adapter is a construction
// error, never a phantom execution body (code_rules_v2 §0.2).
func TestNewChatCognitionRejectsNoExecutionPath(t *testing.T) {
	if _, err := NewChatCognition(ChatCognitionDeps{}); err == nil {
		t.Fatal("construction without any execution path must fail")
	}
}

// compile-time check: the fake binder satisfies the minimal ToolBinder shape
// used by the cognition (not asserted elsewhere).
var _ ToolBinder = (*fakeToolBinder)(nil)
