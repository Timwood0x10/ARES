package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockMCPExecutor implements MCPExecutor for testing.
type mockMCPExecutor struct {
	tools []MCPToolInfo
	err   error
}

func (m *mockMCPExecutor) CallTool(_ context.Context, _ string, _ map[string]any) (*MCPToolResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &MCPToolResult{
		Content: []MCPContentBlock{{Type: "text", Text: "mock result data"}},
	}, nil
}

func (m *mockMCPExecutor) ListTools(_ context.Context) ([]MCPToolInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tools, nil
}

// mockLLMExecutor implements LLMExecutor for testing.
type mockLLMExecutor struct {
	response string
	err      error
}

func (m *mockLLMExecutor) Generate(_ context.Context, _ string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func TestNewOrchestrator(t *testing.T) {
	orch := NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	if orch == nil {
		t.Fatal("expected non-nil orchestrator")
	}
	if len(orch.GetTemplates()) != 0 {
		t.Error("expected empty templates")
	}
	if len(orch.ListAgents()) != 0 {
		t.Error("expected empty agents")
	}
}

func TestOrchestratorSetTemplates(t *testing.T) {
	orch := NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})

	templates := []AgentTemplate{
		{ID: "t1", Name: "Template 1", MCPTool: "tool1", LLMPrompt: "prompt1"},
		{ID: "t2", Name: "Template 2", MCPTool: "tool2", LLMPrompt: "prompt2"},
	}
	orch.SetTemplates(templates)

	got := orch.GetTemplates()
	if len(got) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(got))
	}
	if got[0].ID != "t1" {
		t.Errorf("expected t1, got %s", got[0].ID)
	}
}

func TestOrchestratorCreateAgent(t *testing.T) {
	mcp := &mockMCPExecutor{
		tools: []MCPToolInfo{{Name: "test", Description: "test tool"}},
	}
	llm := &mockLLMExecutor{response: "analysis result"}
	orch := NewOrchestrator(mcp, llm)

	templates := []AgentTemplate{
		{ID: "tpl-1", Name: "Test Template", MCPTool: "test", LLMPrompt: "Analyze: {{.raw_data}}"},
	}
	orch.SetTemplates(templates)

	// Create agent from template.
	id, err := orch.CreateAgent(AgentRequest{TemplateID: "tpl-1"})
	if err != nil {
		t.Fatalf("CreateAgent error: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	// Wait for completion.
	time.Sleep(500 * time.Millisecond)

	result, ok := orch.GetAgent(id)
	if !ok {
		t.Fatal("expected agent to exist")
	}
	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if result.Analysis != "analysis result" {
		t.Errorf("expected 'analysis result', got %s", result.Analysis)
	}
	if result.Progress != 100 {
		t.Errorf("expected 100, got %d", result.Progress)
	}
	if result.RawDataLen == 0 {
		t.Error("expected non-zero raw data length")
	}
}

func TestOrchestratorCreateCustomAgent(t *testing.T) {
	mcp := &mockMCPExecutor{
		tools: []MCPToolInfo{{Name: "custom", Description: "custom tool"}},
	}
	llm := &mockLLMExecutor{response: "custom analysis"}
	orch := NewOrchestrator(mcp, llm)

	// Create agent without template.
	id, err := orch.CreateAgent(AgentRequest{
		Name:     "Custom Agent",
		MCPTool:  "custom",
		MCPArgs:  map[string]any{"key": "value"},
		LLMPrompt: "Custom prompt: {{.raw_data}}",
	})
	if err != nil {
		t.Fatalf("CreateAgent error: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	result, ok := orch.GetAgent(id)
	if !ok {
		t.Fatal("expected agent to exist")
	}
	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if result.Analysis != "custom analysis" {
		t.Errorf("expected 'custom analysis', got %s", result.Analysis)
	}
}

func TestOrchestratorCreateAgentNoName(t *testing.T) {
	orch := NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})

	_, err := orch.CreateAgent(AgentRequest{})
	if err == nil {
		t.Error("expected error for empty agent name")
	}
}

func TestOrchestratorCreateAgentMCPError(t *testing.T) {
	mcp := &mockMCPExecutor{err: fmt.Errorf("mcp connection failed")}
	llm := &mockLLMExecutor{response: "should not reach"}
	orch := NewOrchestrator(mcp, llm)

	templates := []AgentTemplate{
		{ID: "tpl-fail", Name: "Failing Template", MCPTool: "bad", LLMPrompt: "{{.raw_data}}"},
	}
	orch.SetTemplates(templates)

	id, err := orch.CreateAgent(AgentRequest{TemplateID: "tpl-fail"})
	if err != nil {
		t.Fatalf("CreateAgent should not error synchronously: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	result, ok := orch.GetAgent(id)
	if !ok {
		t.Fatal("expected agent to exist")
	}
	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
	if result.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestOrchestratorCreateAgentLLMError(t *testing.T) {
	mcp := &mockMCPExecutor{
		tools: []MCPToolInfo{{Name: "t", Description: "d"}},
	}
	llm := &mockLLMExecutor{err: fmt.Errorf("llm overloaded")}
	orch := NewOrchestrator(mcp, llm)

	templates := []AgentTemplate{
		{ID: "tpl-llm", Name: "LLM Fail", MCPTool: "t", LLMPrompt: "{{.raw_data}}"},
	}
	orch.SetTemplates(templates)

	id, _ := orch.CreateAgent(AgentRequest{TemplateID: "tpl-llm"})
	time.Sleep(500 * time.Millisecond)

	result, ok := orch.GetAgent(id)
	if !ok {
		t.Fatal("expected agent to exist")
	}
	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
	if result.Error != "llm overloaded" {
		t.Errorf("expected 'llm overloaded', got %s", result.Error)
	}
}

func TestOrchestratorCreateAgentNoTool(t *testing.T) {
	// Agent with no MCPTool should list tools as raw data.
	mcp := &mockMCPExecutor{
		tools: []MCPToolInfo{
			{Name: "a", Description: "tool a"},
			{Name: "b", Description: "tool b"},
		},
	}
	llm := &mockLLMExecutor{response: "tool listing analysis"}
	orch := NewOrchestrator(mcp, llm)

	id, _ := orch.CreateAgent(AgentRequest{
		Name:      "No Tool Agent",
		MCPTool:   "",
		LLMPrompt: "Analyze these tools: {{.raw_data}}",
	})

	time.Sleep(500 * time.Millisecond)

	result, ok := orch.GetAgent(id)
	if !ok {
		t.Fatal("expected agent to exist")
	}
	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if result.RawDataLen == 0 {
		t.Error("expected non-zero raw data from tool listing")
	}
}

func TestOrchestratorListAgents(t *testing.T) {
	orch := NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})

	// Create multiple agents.
	for i := 0; i < 3; i++ {
		orch.CreateAgent(AgentRequest{
			Name:      fmt.Sprintf("Agent %d", i),
			MCPTool:   "",
			LLMPrompt: "test",
		})
	}

	agents := orch.ListAgents()
	if len(agents) != 3 {
		t.Errorf("expected 3 agents, got %d", len(agents))
	}
}

func TestOrchestratorGetAgentNotFound(t *testing.T) {
	orch := NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})

	_, ok := orch.GetAgent("nonexistent")
	if ok {
		t.Error("expected false for nonexistent agent")
	}
}

func TestOrchestratorWithEventStore(t *testing.T) {
	mcp := &mockMCPExecutor{
		tools: []MCPToolInfo{{Name: "t", Description: "d"}},
	}
	llm := &mockLLMExecutor{response: "result"}
	orch := NewOrchestrator(mcp, llm)

	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	orch.SetHub(hub)
	orch.SetTemplates([]AgentTemplate{
		{ID: "tpl", Name: "Test", MCPTool: "t", LLMPrompt: "{{.raw_data}}"},
	})

	id, _ := orch.CreateAgent(AgentRequest{TemplateID: "tpl"})
	time.Sleep(500 * time.Millisecond)

	result, ok := orch.GetAgent(id)
	if !ok {
		t.Fatal("expected agent")
	}
	if result.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Status)
	}
}

func TestOrchestratorConcurrentCreate(t *testing.T) {
	mcp := &mockMCPExecutor{
		tools: []MCPToolInfo{{Name: "t", Description: "d"}},
	}
	llm := &mockLLMExecutor{response: "ok"}
	orch := NewOrchestrator(mcp, llm)
	orch.SetTemplates([]AgentTemplate{
		{ID: "tpl", Name: "Test", MCPTool: "t", LLMPrompt: "{{.raw_data}}"},
	})

	// Create 10 agents concurrently.
	ids := make(chan string, 10)
	for i := 0; i < 10; i++ {
		go func() {
			id, err := orch.CreateAgent(AgentRequest{TemplateID: "tpl"})
			if err != nil {
				t.Errorf("CreateAgent error: %v", err)
			}
			ids <- id
		}()
	}

	// Collect all IDs.
	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		id := <-ids
		if seen[id] {
			t.Errorf("duplicate agent id: %s", id)
		}
		seen[id] = true
	}

	if len(seen) != 10 {
		t.Errorf("expected 10 unique ids, got %d", len(seen))
	}
}

func TestTemplateDefaults(t *testing.T) {
	mcp := &mockMCPExecutor{
		tools: []MCPToolInfo{{Name: "t", Description: "d"}},
	}
	llm := &mockLLMExecutor{response: "ok"}
	orch := NewOrchestrator(mcp, llm)

	templates := []AgentTemplate{
		{ID: "tpl", Name: "Template Name", MCPTool: "t", MCPArgs: map[string]any{"k": "v"}, LLMPrompt: "prompt"},
	}
	orch.SetTemplates(templates)

	// Create with template_id only — should inherit name, tool, args, prompt.
	id, _ := orch.CreateAgent(AgentRequest{TemplateID: "tpl"})
	time.Sleep(500 * time.Millisecond)

	result, _ := orch.GetAgent(id)
	if result.Name != "Template Name" {
		t.Errorf("expected name 'Template Name', got %s", result.Name)
	}
	if result.MCPTool != "t" {
		t.Errorf("expected tool 't', got %s", result.MCPTool)
	}
}
