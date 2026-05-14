// package graph - tests for node implementations.

package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/tools/resources/core"
)

// mockTool is a simple mock tool for testing.
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

// mockAgent is a simple mock agent for testing.
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

func TestFuncNode(t *testing.T) {
	called := false
	node := NewFuncNode("test", func(ctx context.Context, state *State) error {
		called = true
		return nil
	})

	if node.ID() != "test" {
		t.Errorf("expected ID test, got %s", node.ID())
	}

	state := NewState()
	err := node.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected function to be called")
	}
}

func TestFuncNodeWithError(t *testing.T) {
	expectedErr := errors.New("test error")
	node := NewFuncNode("test", func(ctx context.Context, state *State) error {
		return expectedErr
	})

	state := NewState()
	err := node.Execute(context.Background(), state)
	if err == nil {
		t.Error("expected error")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error to contain %v, got %v", expectedErr, err)
	}
}

func TestFuncNodeWithTimeout(t *testing.T) {
	node := NewFuncNode("test", func(ctx context.Context, state *State) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	state := NewState()
	err := node.Execute(ctx, state)
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

	node := NewToolNode(tool)

	if node.ID() != "test-tool" {
		t.Errorf("expected ID test-tool, got %s", node.ID())
	}

	state := NewState()
	state.Set("input", "test")
	err := node.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected tool to be called")
	}

	// Check that result is stored with node prefix
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

	node := NewToolNode(tool)
	state := NewState()
	err := node.Execute(context.Background(), state)
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

	node := NewToolNode(tool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	state := NewState()
	err := node.Execute(ctx, state)
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

	node := NewAgentNode(agent)

	if node.ID() != "test-agent" {
		t.Errorf("expected ID test-agent, got %s", node.ID())
	}

	state := NewState()
	state.Set("input", "test")
	err := node.Execute(context.Background(), state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected agent to be called")
	}

	// Check that result is stored with node prefix
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

	node := NewAgentNode(agent)
	state := NewState()
	err := node.Execute(context.Background(), state)
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

	node := NewAgentNode(agent)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	state := NewState()
	err := node.Execute(ctx, state)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestNodeNilTool(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil tool")
		}
	}()

	// This should panic because NewToolNode receives nil
	NewToolNode(nil)
}

func TestNodeNilAgent(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil agent")
		}
	}()

	// This should panic because NewAgentNode receives nil
	NewAgentNode(nil)
}
