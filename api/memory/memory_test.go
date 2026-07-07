package memory

import (
	"context"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Storage != "memory" {
		t.Fatalf("expected 'memory' storage, got %s", cfg.Storage)
	}
	if cfg.MaxHistory <= 0 {
		t.Fatal("expected positive MaxHistory")
	}
}

func TestNewManagerWithNilConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTasks = 10
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager(nil): %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil Manager")
	}
}

func TestNewManagerWithDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTasks = 10
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil Manager")
	}
}

func newTestManager(t *testing.T) Manager {
	cfg := DefaultConfig()
	cfg.MaxTasks = 10
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

func TestCreateSession(t *testing.T) {
	mgr := newTestManager(t)
	ctx := context.Background()
	sessionID, err := mgr.CreateSession(ctx, "user-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sessionID == "" {
		t.Fatal("expected non-empty session ID")
	}
}

func TestAddAndGetMessages(t *testing.T) {
	mgr, _ := NewManager(DefaultConfig())
	ctx := context.Background()

	sid, _ := mgr.CreateSession(ctx, "user-1")
	if err := mgr.AddMessage(ctx, sid, "user", "hello"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if err := mgr.AddMessage(ctx, sid, "assistant", "hi there"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	msgs, err := mgr.GetMessages(ctx, sid)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(msgs))
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hello" {
		t.Fatalf("unexpected first message: %+v", msgs[0])
	}
}

func TestAddStructuredMessage(t *testing.T) {
	mgr, _ := NewManager(DefaultConfig())
	ctx := context.Background()

	sid, _ := mgr.CreateSession(ctx, "user-1")
	msg := Message{Role: "tool_call", Content: "{}", ToolCalls: []ToolCall{
		{ID: "tc-1", Type: "function", Function: ToolCallFunction{Name: "echo", Arguments: `{"msg":"hi"}`}},
	}}
	if err := mgr.AddStructuredMessage(ctx, sid, msg); err != nil {
		t.Fatalf("AddStructuredMessage: %v", err)
	}

	msgs, _ := mgr.GetMessages(ctx, sid)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msgs[1].ToolCalls))
	}
}

func TestBuildContext(t *testing.T) {
	mgr, _ := NewManager(DefaultConfig())
	ctx := context.Background()

	sid, _ := mgr.CreateSession(ctx, "user-1")
	_ = mgr.AddMessage(ctx, sid, "user", "what is ares?")
	_ = mgr.AddMessage(ctx, sid, "assistant", "ares is an agent system")

	out, err := mgr.BuildContext(ctx, "tell me more", sid)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty context")
	}
}

func TestDeleteSession(t *testing.T) {
	mgr, _ := NewManager(DefaultConfig())
	ctx := context.Background()

	sid, _ := mgr.CreateSession(ctx, "user-1")
	if err := mgr.DeleteSession(ctx, sid); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, err := mgr.GetMessages(ctx, sid)
	if err == nil {
		t.Fatal("expected error after deleting session")
	}
}

func TestCreateAndUpdateTask(t *testing.T) {
	mgr, _ := NewManager(DefaultConfig())
	ctx := context.Background()

	sid, _ := mgr.CreateSession(ctx, "user-1")
	taskID, err := mgr.CreateTask(ctx, sid, "user-1", "analyze the code")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	if err := mgr.UpdateTaskOutput(ctx, taskID, "analysis complete"); err != nil {
		t.Fatalf("UpdateTaskOutput: %v", err)
	}
}

func TestBuildPromptMessages(t *testing.T) {
	mgr, _ := NewManager(DefaultConfig())
	ctx := context.Background()

	sid, _ := mgr.CreateSession(ctx, "user-1")
	_ = mgr.AddMessage(ctx, sid, "user", "hello")

	msgs, err := mgr.BuildPromptMessages(ctx, sid)
	if err != nil {
		t.Fatalf("BuildPromptMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
}

func TestStartStop(t *testing.T) {
	mgr, _ := NewManager(DefaultConfig())
	ctx := context.Background()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
