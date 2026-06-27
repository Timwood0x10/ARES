package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
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
		Name:      "Custom Agent",
		MCPTool:   "custom",
		MCPArgs:   map[string]any{"key": "value"},
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
		if _, err := orch.CreateAgent(AgentRequest{
			Name:      fmt.Sprintf("Agent %d", i),
			MCPTool:   "",
			LLMPrompt: "test",
		}); err != nil {
			t.Fatalf("CreateAgent %d: %v", i, err)
		}
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

func TestOrchestratorEventStoreGetter(t *testing.T) {
	orch := NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})

	// Before setting store, EventStore() should return nil.
	if orch.EventStore() != nil {
		t.Error("expected nil event store before SetEventStore")
	}

	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close })
	orch.SetEventStore(store)

	if orch.EventStore() != store {
		t.Error("expected EventStore() to return the configured store")
	}
}

// trackMCPExecutor records CallTool invocations and returns configurable results.
type trackMCPExecutor struct {
	tools    []MCPToolInfo
	calls    []string // records tool names in order
	response string
	err      error
}

func (m *trackMCPExecutor) CallTool(_ context.Context, name string, _ map[string]any) (*MCPToolResult, error) {
	m.calls = append(m.calls, name)
	if m.err != nil {
		return nil, m.err
	}
	return &MCPToolResult{
		Content: []MCPContentBlock{{Type: "text", Text: m.response}},
	}, nil
}

func (m *trackMCPExecutor) ListTools(_ context.Context) ([]MCPToolInfo, error) {
	return m.tools, nil
}

func TestOrchestratorResumeSkipsCompletedSteps(t *testing.T) {
	mcp := &trackMCPExecutor{
		tools:    []MCPToolInfo{{Name: "t", Description: "d"}},
		response: "step data",
	}
	llm := &mockLLMExecutor{response: "analysis"}
	orch := NewOrchestrator(mcp, llm)

	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close })
	orch.SetEventStore(store)

	steps := []AgentStep{
		{Tool: "tool_a", Args: map[string]any{"k": "1"}},
		{Tool: "tool_b", Args: map[string]any{"k": "2"}},
		{Tool: "tool_c", Args: map[string]any{"k": "3"}},
	}

	// Simulate a previous agent that completed steps 1 and 2.
	prevID := "agent-prev"
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		evt := &ares_events.Event{
			ID:        fmt.Sprintf("evt-step-%d", i+1),
			StreamID:  prevID,
			Type:      "mcp.step.completed",
			Payload:   map[string]any{"step": i + 1, "tool": steps[i].Tool, "total": 3},
			Timestamp: time.Now(),
		}
		if err := store.Append(ctx, prevID, []*ares_events.Event{evt}, 0); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	// Create a new agent that resumes from the previous one.
	id, err := orch.CreateAgent(AgentRequest{
		Name:       "Resuming Agent",
		Steps:      steps,
		ResumeFrom: prevID,
		LLMPrompt:  "Analyze data",
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

	// Only step 3 (tool_c) should have been called; steps 1 and 2 were skipped.
	if len(mcp.calls) != 1 {
		t.Fatalf("expected 1 MCP call (step 3 only), got %d: %v", len(mcp.calls), mcp.calls)
	}
	if mcp.calls[0] != "tool_c" {
		t.Errorf("expected tool_c, got %s", mcp.calls[0])
	}
}

func TestOrchestratorResumeSkipsNoStepsWhenNoneCompleted(t *testing.T) {
	mcp := &trackMCPExecutor{
		tools:    []MCPToolInfo{{Name: "t", Description: "d"}},
		response: "data",
	}
	llm := &mockLLMExecutor{response: "analysis"}
	orch := NewOrchestrator(mcp, llm)

	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close })
	orch.SetEventStore(store)

	steps := []AgentStep{
		{Tool: "tool_a"},
		{Tool: "tool_b"},
	}

	// Previous agent has ares_events but none are mcp.step.completed.
	prevID := "agent-prev-empty"
	ctx := context.Background()
	evt := &ares_events.Event{
		ID:        "evt-start",
		StreamID:  prevID,
		Type:      ares_events.EventType("agent.started"),
		Payload:   map[string]any{"name": "prev"},
		Timestamp: time.Now(),
	}
	if err := store.Append(ctx, prevID, []*ares_events.Event{evt}, 0); err != nil {
		t.Fatalf("append event: %v", err)
	}

	id, err := orch.CreateAgent(AgentRequest{
		Name:       "Resuming Agent",
		Steps:      steps,
		ResumeFrom: prevID,
		LLMPrompt:  "Analyze",
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

	// Both steps should have been called since none were completed.
	if len(mcp.calls) != 2 {
		t.Fatalf("expected 2 MCP calls, got %d: %v", len(mcp.calls), mcp.calls)
	}
}

func TestOrchestratorResumeWithNoEventStore(t *testing.T) {
	mcp := &trackMCPExecutor{
		tools:    []MCPToolInfo{{Name: "t", Description: "d"}},
		response: "data",
	}
	llm := &mockLLMExecutor{response: "analysis"}
	orch := NewOrchestrator(mcp, llm)
	// No event store configured.

	steps := []AgentStep{
		{Tool: "tool_a"},
		{Tool: "tool_b"},
	}

	id, err := orch.CreateAgent(AgentRequest{
		Name:       "Resuming No Store",
		Steps:      steps,
		ResumeFrom: "agent-ghost",
		LLMPrompt:  "Analyze",
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

	// Without an event store, all steps should run from the beginning.
	if len(mcp.calls) != 2 {
		t.Fatalf("expected 2 MCP calls, got %d: %v", len(mcp.calls), mcp.calls)
	}
}

func TestOrchestratorEmitsStepCompletedEvents(t *testing.T) {
	mcp := &trackMCPExecutor{
		tools:    []MCPToolInfo{{Name: "t", Description: "d"}},
		response: "data",
	}
	llm := &mockLLMExecutor{response: "analysis"}
	orch := NewOrchestrator(mcp, llm)

	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close })
	orch.SetEventStore(store)

	steps := []AgentStep{
		{Tool: "alpha"},
		{Tool: "beta"},
		{Tool: "gamma"},
	}

	id, err := orch.CreateAgent(AgentRequest{
		Name:      "Step Emitter",
		Steps:     steps,
		LLMPrompt: "Analyze",
	})
	if err != nil {
		t.Fatalf("CreateAgent error: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Read back ares_events for this agent.
	ctx := context.Background()
	ares_events, err := store.Read(ctx, id, ares_events.ReadOptions{Direction: ares_events.ReadAscending})
	if err != nil {
		t.Fatalf("read ares_events: %v", err)
	}

	stepEvents := 0
	for _, evt := range ares_events {
		if evt.Type == "mcp.step.completed" {
			stepEvents++
			tool, _ := evt.Payload["tool"].(string)
			step, _ := evt.Payload["step"].(int)
			if step < 1 || step > 3 {
				t.Errorf("unexpected step number: %d", step)
			}
			if tool == "" {
				t.Error("expected non-empty tool name in step event")
			}
		}
	}

	if stepEvents != 3 {
		t.Errorf("expected 3 mcp.step.completed ares_events, got %d", stepEvents)
	}
}

func TestOrchestratorResumePromptIncludesPreviousProgress(t *testing.T) {
	mcp := &trackMCPExecutor{
		tools:    []MCPToolInfo{{Name: "t", Description: "d"}},
		response: "data",
	}

	var capturedPrompt string
	llm := &capturePromptLLM{response: "analysis", prompt: &capturedPrompt}
	orch := NewOrchestrator(mcp, llm)

	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close })
	orch.SetEventStore(store)

	steps := []AgentStep{
		{Tool: "tool_a"},
		{Tool: "tool_b"},
		{Tool: "tool_c"},
	}

	// Simulate a previous agent that completed step 1.
	prevID := "agent-prev-prompt"
	ctx := context.Background()
	evt := &ares_events.Event{
		ID:        "evt-s1",
		StreamID:  prevID,
		Type:      "mcp.step.completed",
		Payload:   map[string]any{"step": 1, "tool": "tool_a", "total": 3},
		Timestamp: time.Now(),
	}
	if err := store.Append(ctx, prevID, []*ares_events.Event{evt}, 0); err != nil {
		t.Fatalf("append event: %v", err)
	}

	id, err := orch.CreateAgent(AgentRequest{
		Name:       "Prompt Check",
		Steps:      steps,
		ResumeFrom: prevID,
		LLMPrompt:  "Analyze the data",
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

	// The LLM prompt should contain the resume preamble.
	if !strings.Contains(capturedPrompt, "This agent was interrupted and is being resumed") {
		t.Error("expected resume preamble in LLM prompt")
	}
	if !strings.Contains(capturedPrompt, "Continuing from step 2") {
		t.Errorf("expected 'Continuing from step 2' in prompt, got: %s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "Agent agent-prev-prompt completed 1/3 steps") {
		t.Errorf("expected previous agent summary in prompt, got: %s", capturedPrompt)
	}
}

// capturePromptLLM records the prompt passed to Generate.
type capturePromptLLM struct {
	response string
	prompt   *string
	err      error
}

func (m *capturePromptLLM) Generate(_ context.Context, prompt string) (string, error) {
	if m.prompt != nil {
		*m.prompt = prompt
	}
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

// mockStreamLLMExecutor implements both LLMExecutor and StreamLLMExecutor for testing.
type mockStreamLLMExecutor struct {
	chunks []StreamChunk
	err    error
}

func (m *mockStreamLLMExecutor) Generate(_ context.Context, _ string) (string, error) {
	return "fallback", nil
}

func (m *mockStreamLLMExecutor) GenerateStream(_ context.Context, _ string) (<-chan StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan StreamChunk, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// hangingStreamLLMExecutor returns a channel that is never closed (simulates a hung stream).
type hangingStreamLLMExecutor struct{}

func (h *hangingStreamLLMExecutor) Generate(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("should not be called")
}

func (h *hangingStreamLLMExecutor) GenerateStream(_ context.Context, _ string) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk)
	// Intentionally never close the channel — simulates a hung LLM adapter.
	return ch, nil
}

func TestConsumeStreamContextCancellation(t *testing.T) {
	orch := NewOrchestrator(&mockMCPExecutor{}, &hangingStreamLLMExecutor{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	analysis, err := orch.llmGenerateStreaming(ctx, "test-agent", "prompt")
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
	if analysis != "" {
		t.Errorf("expected empty analysis from cancelled stream, got: %q", analysis)
	}
}

func TestConsumeStreamNormalCompletion(t *testing.T) {
	llm := &mockStreamLLMExecutor{
		chunks: []StreamChunk{
			{Content: "hello "},
			{Content: "world"},
			{Done: true},
		},
	}
	orch := NewOrchestrator(&mockMCPExecutor{}, llm)

	ctx := context.Background()
	analysis, err := orch.llmGenerateStreaming(ctx, "test-agent", "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if analysis != "hello world" {
		t.Errorf("expected 'hello world', got %q", analysis)
	}
}

func TestConsumeStreamErrorChunk(t *testing.T) {
	streamErr := fmt.Errorf("LLM rate limited")
	llm := &mockStreamLLMExecutor{
		chunks: []StreamChunk{
			{Content: "partial"},
			{Err: streamErr},
		},
	}
	orch := NewOrchestrator(&mockMCPExecutor{}, llm)

	ctx := context.Background()
	analysis, err := orch.llmGenerateStreaming(ctx, "test-agent", "prompt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, streamErr) {
		t.Errorf("expected stream error, got: %v", err)
	}
	if analysis != "partial" {
		t.Errorf("expected 'partial' analysis before error, got %q", analysis)
	}
}
