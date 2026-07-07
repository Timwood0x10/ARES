package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

type mockTool struct {
	name        string
	description string
	executeFn   func(context.Context, map[string]interface{}) (core.Result, error)
}

func (m *mockTool) Name() string {
	return m.name
}

func (m *mockTool) Description() string {
	return m.description
}

func (m *mockTool) Category() core.ToolCategory {
	return core.CategoryCore
}

func (m *mockTool) Capabilities() []core.Capability {
	return nil
}

func (m *mockTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	return m.executeFn(ctx, params)
}

func (m *mockTool) Parameters() *core.ParameterSchema {
	return &core.ParameterSchema{
		Type: "object",
	}
}

type mockAgent struct {
	id        string
	agentType models.AgentType
	processFn func(context.Context, any) (any, error)
}

func (m *mockAgent) ID() string {
	return m.id
}

func (m *mockAgent) Type() models.AgentType {
	return m.agentType
}

func (m *mockAgent) Status() models.AgentStatus {
	return models.AgentStatusReady
}

func (m *mockAgent) Start(ctx context.Context) error {
	return nil
}

func (m *mockAgent) Stop(ctx context.Context) error {
	return nil
}

func (m *mockAgent) Process(ctx context.Context, input any) (any, error) {
	return m.processFn(ctx, input)
}

func (m *mockAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	result, err := m.Process(ctx, input)
	ch := make(chan base.AgentEvent, 1)
	ch <- base.AgentEvent{Type: base.EventComplete, Data: result, Err: err}
	close(ch)
	return ch, nil
}

func TestFuncNode(t *testing.T) {
	called := false
	node, err := NewFuncNode("test", func(ctx context.Context, state *State) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("NewFuncNode failed: %v", err)
	}

	if node.ID() != "test" {
		t.Errorf("expected ID test, got %s", node.ID())
	}

	state := NewState()
	err = node.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected function to be called")
	}
}

func TestFuncNodeWithError(t *testing.T) {
	expectedErr := errors.New("test error")
	node, err := NewFuncNode("test", func(ctx context.Context, state *State) error {
		return expectedErr
	})
	if err != nil {
		t.Fatalf("NewFuncNode failed: %v", err)
	}

	state := NewState()
	err = node.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error to contain %v, got %v", expectedErr, err)
	}
}

func TestFuncNodeWithTimeout(t *testing.T) {
	node, err := NewFuncNode("test", func(ctx context.Context, state *State) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatalf("NewFuncNode failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	state := NewState()
	err = node.Execute(ctx, state)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestToolNode(t *testing.T) {
	called := false
	tool := &mockTool{
		name:        "test-tool",
		description: "A test tool",
		executeFn: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			called = true
			return core.Result{
				Success: true,
				Data:    "result",
			}, nil
		},
	}

	node, err := NewToolNode(tool)
	if err != nil {
		t.Fatalf("NewToolNode failed: %v", err)
	}

	if node.ID() != "test-tool" {
		t.Errorf("expected ID test-tool, got %s", node.ID())
	}

	state := NewState()
	state.Set("input", "test")
	err = node.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected tool to be called")
	}

	val, ok := state.Get("node.test-tool")
	if !ok {
		t.Error("expected node.test-tool in state")
	}
	if val != "result" {
		t.Errorf("expected result, got %v", val)
	}
}

func TestToolNodeWithError(t *testing.T) {
	expectedErr := errors.New("tool error")
	tool := &mockTool{
		name:        "test-tool",
		description: "A test tool",
		executeFn: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			return core.Result{}, expectedErr
		},
	}

	node, err := NewToolNode(tool)
	if err != nil {
		t.Fatalf("NewToolNode failed: %v", err)
	}
	state := NewState()
	err = node.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error to contain %v, got %v", expectedErr, err)
	}
}

func TestToolNodeWithTimeout(t *testing.T) {
	tool := &mockTool{
		name:        "test-tool",
		description: "A test tool",
		executeFn: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			select {
			case <-ctx.Done():
				return core.Result{}, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return core.Result{Success: true}, nil
			}
		},
	}

	node, err := NewToolNode(tool)
	if err != nil {
		t.Fatalf("NewToolNode failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	state := NewState()
	err = node.Execute(ctx, state)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestAgentNode(t *testing.T) {
	called := false
	agent := &mockAgent{
		id:        "test-agent",
		agentType: models.AgentType("test"),
		processFn: func(ctx context.Context, input any) (any, error) {
			called = true
			return "agent-result", nil
		},
	}

	node, err := NewAgentNode(agent)
	if err != nil {
		t.Fatalf("NewAgentNode failed: %v", err)
	}

	if node.ID() != "test-agent" {
		t.Errorf("expected ID test-agent, got %s", node.ID())
	}

	state := NewState()
	state.Set("input", "test")
	err = node.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected agent to be called")
	}

	val, ok := state.Get("node.test-agent")
	if !ok {
		t.Error("expected node.test-agent in state")
	}
	if val != "agent-result" {
		t.Errorf("expected agent-result, got %v", val)
	}
}

func TestAgentNodeWithError(t *testing.T) {
	expectedErr := errors.New("agent error")
	agent := &mockAgent{
		id:        "test-agent",
		agentType: models.AgentType("test"),
		processFn: func(ctx context.Context, input any) (any, error) {
			return nil, expectedErr
		},
	}

	node, err := NewAgentNode(agent)
	if err != nil {
		t.Fatalf("NewAgentNode failed: %v", err)
	}

	state := NewState()
	state.Set("input", "test input")
	err = node.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error to contain %v, got %v", expectedErr, err)
	}
}

func TestAgentNodeWithTimeout(t *testing.T) {
	agent := &mockAgent{
		id:        "test-agent",
		agentType: models.AgentType("test"),
		processFn: func(ctx context.Context, input any) (any, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return "result", nil
			}
		},
	}

	node, err := NewAgentNode(agent)
	if err != nil {
		t.Fatalf("NewAgentNode failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	state := NewState()
	err = node.Execute(ctx, state)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestNodeNilTool(t *testing.T) {
	_, err := NewToolNode(nil)
	if err == nil {
		t.Error("expected error for nil tool")
	}
}

func TestNodeNilAgent(t *testing.T) {
	_, err := NewAgentNode(nil)
	if err == nil {
		t.Error("expected error for nil agent")
	}
}

// ── ToolNode Bridge Fallback Tests ────────────────────────

// mockBridgeSuccess returns a successful result on any call.
type mockBridgeSuccess struct{}

func (m *mockBridgeSuccess) Execute(_ context.Context, _ string, params map[string]interface{}, _ string) (core.Result, error) {
	return core.Result{Success: true, Data: map[string]interface{}{"bridged": true, "input": params}}, nil
}

// mockBridgeError returns an error on any call.
type mockBridgeError struct{}

func (m *mockBridgeError) Execute(_ context.Context, _ string, _ map[string]interface{}, _ string) (core.Result, error) {
	return core.Result{}, errors.New("bridge fallback also failed")
}

func TestToolNode_BridgeFallbackUsedOnToolFailure(t *testing.T) {
	failingTool := &mockTool{
		name:        "failing_tool",
		description: "A tool that always fails",
		executeFn: func(_ context.Context, _ map[string]interface{}) (core.Result, error) {
			return core.Result{}, errors.New("tool error")
		},
	}

	node, err := NewToolNode(failingTool)
	if err != nil {
		t.Fatalf("NewToolNode failed: %v", err)
	}
	node.WithBridge(&mockBridgeSuccess{}, "user request")

	state := NewState()
	err = node.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("bridge should have handled the failure, got: %v", err)
	}
}

func TestToolNode_BridgeNotUsedWhenToolSucceeds(t *testing.T) {
	succeedingTool := &mockTool{
		name:        "good_tool",
		description: "A tool that always succeeds",
		executeFn: func(_ context.Context, _ map[string]interface{}) (core.Result, error) {
			return core.Result{Success: true, Data: map[string]interface{}{"result": "ok"}}, nil
		},
	}

	node, err := NewToolNode(succeedingTool)
	if err != nil {
		t.Fatalf("NewToolNode failed: %v", err)
	}
	node.WithBridge(&mockBridgeSuccess{}, "user request")

	state := NewState()
	err = node.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("tool should have succeeded without bridge, got: %v", err)
	}
}

func TestToolNode_BridgeNotConfigured(t *testing.T) {
	failingTool := &mockTool{
		name:        "failing_tool",
		description: "A tool that always fails",
		executeFn: func(_ context.Context, _ map[string]interface{}) (core.Result, error) {
			return core.Result{}, errors.New("tool error")
		},
	}

	node, err := NewToolNode(failingTool)
	if err != nil {
		t.Fatalf("NewToolNode failed: %v", err)
	}
	// No bridge configured.

	state := NewState()
	err = node.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error when no bridge is configured and tool fails")
	}
}

func TestToolNode_BridgeFallbackAlsoFails(t *testing.T) {
	failingTool := &mockTool{
		name:        "failing_tool",
		description: "A tool that always fails",
		executeFn: func(_ context.Context, _ map[string]interface{}) (core.Result, error) {
			return core.Result{}, errors.New("tool error")
		},
	}

	node, err := NewToolNode(failingTool)
	if err != nil {
		t.Fatalf("NewToolNode failed: %v", err)
	}
	node.WithBridge(&mockBridgeError{}, "user request")

	state := NewState()
	err = node.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error when both tool and bridge fail")
	}
}

func TestToolNode_NilNodeExecute(t *testing.T) {
	var node *ToolNode
	err := node.Execute(context.Background(), NewState())
	if err == nil {
		t.Error("expected error from nil node Execute")
	}
}
