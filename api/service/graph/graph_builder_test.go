// Package graph provides tests for graph builder.

package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"goagent/internal/agents/base"
	"goagent/internal/core/models"
)

// mockAgent is a test agent implementation.
type mockAgent struct {
	id   string
	name string
}

func (m *mockAgent) Process(ctx context.Context, input any) (any, error) {
	return "processed", nil
}

// ProcessStream handles input and returns a stream of events.
func (m *mockAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	result, err := m.Process(ctx, input)
	ch := make(chan base.AgentEvent, 1)
	ch <- base.AgentEvent{Type: base.EventComplete, Data: result, Err: err}
	close(ch)
	return ch, nil
}

func (m *mockAgent) ID() string {
	return m.id
}

func (m *mockAgent) Type() models.AgentType {
	return models.AgentTypeLeader
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

func TestNewGraphBuilder(t *testing.T) {
	builder := NewGraphBuilder()
	require.NotNil(t, builder)

	if builder.agentRegistry == nil {
		t.Error("agentRegistry not initialized")
	}

	if builder.toolRegistry == nil {
		t.Error("toolRegistry not initialized")
	}
}

func TestGraphBuilderRegisterAgent(t *testing.T) {
	builder := NewGraphBuilder()
	agent := &mockAgent{id: "test-agent", name: "Test Agent"}

	builder.RegisterAgent(agent)

	registered, exists := builder.agentRegistry["test-agent"]
	if !exists {
		t.Error("agent not registered")
	}

	if registered.ID() != "test-agent" {
		t.Errorf("agent ID = %s, want test-agent", registered.ID())
	}
}

func TestGraphBuilderRegisterNilAgent(t *testing.T) {
	builder := NewGraphBuilder()
	builder.RegisterAgent(nil)

	// Should not panic.
}

func TestGraphBuilderRegisterTool(t *testing.T) {
	builder := NewGraphBuilder()
	tool := "mock-tool"

	builder.RegisterTool("test-tool", tool)

	registered, exists := builder.toolRegistry["test-tool"]
	if !exists {
		t.Error("tool not registered")
	}

	if registered != "mock-tool" {
		t.Errorf("tool = %v, want mock-tool", registered)
	}
}

func TestGraphBuilderRegisterNilTool(t *testing.T) {
	builder := NewGraphBuilder()
	builder.RegisterTool("", nil)

	// Should not panic.
}

func TestGraphBuilderBuild(t *testing.T) {
	builder := NewGraphBuilder()

	config := &GraphConfig{
		Graph: GraphDefinition{
			ID:        "test-graph",
			StartNode: "node1",
			Nodes: []Node{
				{ID: "node1", Type: "function"},
				{ID: "node2", Type: "function"},
			},
			Edges: []Edge{
				{From: "node1", To: "node2"},
			},
		},
	}

	g, err := builder.Build(config)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if g == nil {
		t.Fatal("Build() returned nil graph")
	}

	if g.ID() != "test-graph" {
		t.Errorf("Graph ID = %s, want test-graph", g.ID())
	}
}

func TestGraphBuilderBuildWithNilBuilder(t *testing.T) {
	var builder *GraphBuilder
	config := &GraphConfig{}

	_, err := builder.Build(config)
	if err == nil {
		t.Error("Build() should return error for nil builder")
	}
}

func TestGraphBuilderBuildWithNilConfig(t *testing.T) {
	builder := NewGraphBuilder()

	_, err := builder.Build(nil)
	if err == nil {
		t.Error("Build() should return error for nil config")
	}
}

func TestGraphBuilderBuildWithAgentNode(t *testing.T) {
	builder := NewGraphBuilder()
	agent := &mockAgent{id: "test-agent", name: "Test Agent"}
	builder.RegisterAgent(agent)

	config := &GraphConfig{
		Graph: GraphDefinition{
			ID:        "test-graph",
			StartNode: "node1",
			Nodes: []Node{
				{
					ID:   "node1",
					Type: "agent",
					Config: map[string]interface{}{
						"agent_id": "test-agent",
					},
				},
			},
			Edges: []Edge{},
		},
	}

	g, err := builder.Build(config)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if g == nil {
		t.Fatal("Build() returned nil graph")
	}
}

func TestGraphBuilderBuildWithUnregisteredAgent(t *testing.T) {
	builder := NewGraphBuilder()

	config := &GraphConfig{
		Graph: GraphDefinition{
			ID:        "test-graph",
			StartNode: "node1",
			Nodes: []Node{
				{
					ID:   "node1",
					Type: "agent",
					Config: map[string]interface{}{
						"agent_id": "nonexistent-agent",
					},
				},
			},
			Edges: []Edge{},
		},
	}

	_, err := builder.Build(config)
	if err == nil {
		t.Error("Build() should return error for unregistered agent")
	}
}

func TestGraphBuilderBuildWithToolNode(t *testing.T) {
	builder := NewGraphBuilder()
	tool := "mock-tool"
	builder.RegisterTool("test-tool", tool)

	config := &GraphConfig{
		Graph: GraphDefinition{
			ID:        "test-graph",
			StartNode: "node1",
			Nodes: []Node{
				{
					ID:   "node1",
					Type: "tool",
					Config: map[string]interface{}{
						"tool_id": "test-tool",
					},
				},
			},
			Edges: []Edge{},
		},
	}

	_, err := builder.Build(config)
	if err == nil {
		t.Error("Build() should return error for tool nodes (not yet implemented)")
	}
}

func TestGraphBuilderBuildWithUnsupportedNodeType(t *testing.T) {
	builder := NewGraphBuilder()

	config := &GraphConfig{
		Graph: GraphDefinition{
			ID:        "test-graph",
			StartNode: "node1",
			Nodes: []Node{
				{ID: "node1", Type: "unsupported"},
			},
			Edges: []Edge{},
		},
	}

	_, err := builder.Build(config)
	if err == nil {
		t.Error("Build() should return error for unsupported node type")
	}
}

func TestBuildWithService(t *testing.T) {
	yaml := `
graph:
  id: "test-graph"
  start_node: "node1"
  nodes:
    - id: "node1"
      type: "function"
  edges: []
`

	builder := NewGraphBuilder()
	service, g, err := BuildWithService([]byte(yaml), builder)
	if err != nil {
		t.Fatalf("BuildWithService() error = %v", err)
	}

	if service == nil {
		t.Fatal("BuildWithService() returned nil service")
	}

	if g == nil {
		t.Fatal("BuildWithService() returned nil graph")
	}
}

func TestBuildWithServiceWithEmptyYAML(t *testing.T) {
	builder := NewGraphBuilder()

	_, _, err := BuildWithService([]byte(""), builder)
	if err == nil {
		t.Error("BuildWithService() should return error for empty YAML")
	}
}

func TestBuildWithServiceWithNilBuilder(t *testing.T) {
	yaml := `
graph:
  id: "test-graph"
  start_node: "node1"
  nodes:
    - id: "node1"
      type: "function"
  edges: []
`

	_, _, err := BuildWithService([]byte(yaml), nil)
	if err == nil {
		t.Error("BuildWithService() should return error for nil builder")
	}
}

func TestBuildSimple(t *testing.T) {
	yaml := `
graph:
  id: "test-graph"
  start_node: "node1"
  nodes:
    - id: "node1"
      type: "function"
  edges: []
`

	g, err := BuildSimple([]byte(yaml))
	if err != nil {
		t.Fatalf("BuildSimple() error = %v", err)
	}

	if g == nil {
		t.Fatal("BuildSimple() returned nil graph")
	}

	if g.ID() != "test-graph" {
		t.Errorf("Graph ID = %s, want test-graph", g.ID())
	}
}

func TestBuildSimpleWithEmptyYAML(t *testing.T) {
	_, err := BuildSimple([]byte(""))
	if err == nil {
		t.Error("BuildSimple() should return error for empty YAML")
	}
}
