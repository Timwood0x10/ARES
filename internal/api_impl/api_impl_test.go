package apiimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	"github.com/Timwood0x10/ares/internal/dashboard"
)

// ──────────────────────────────────────────────
// Mocks
// ──────────────────────────────────────────────

// mockTransport implements ares_mcp.Transport for testing MCPAdapter.
type mockTransport struct {
	mu      sync.Mutex
	started bool
	respCh  chan *ares_mcp.JSONRPCMessage
	handler func(method string, params json.RawMessage) (any, error)
}

func newMockTransport(handler func(method string, params json.RawMessage) (any, error)) *mockTransport {
	return &mockTransport{
		respCh:  make(chan *ares_mcp.JSONRPCMessage, 16),
		handler: handler,
	}
}

func (t *mockTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = true
	return nil
}

func (t *mockTransport) Send(ctx context.Context, msg *ares_mcp.JSONRPCMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if msg.ID != nil && t.handler != nil {
		result, err := t.handler(msg.Method, msg.Params)
		if err != nil {
			resp, _ := ares_mcp.NewErrorResponse(*msg.ID, -32603, err.Error(), nil)
			t.respCh <- resp
		} else {
			resp, _ := ares_mcp.NewResponse(*msg.ID, result)
			t.respCh <- resp
		}
	}
	return nil
}

func (t *mockTransport) Receive(ctx context.Context) (*ares_mcp.JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-t.respCh:
		return msg, nil
	}
}

func (t *mockTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = false
	close(t.respCh)
	return nil
}

func newMockClient(ctx context.Context, t *testing.T, tools []ares_mcp.MCPToolDef) *ares_mcp.MCPClient {
	t.Helper()
	handler := func(method string, params json.RawMessage) (any, error) {
		switch method {
		case ares_mcp.MethodInitialize:
			return ares_mcp.InitializeResult{
				ProtocolVersion: ares_mcp.ProtocolVersion,
				ServerInfo:      ares_mcp.Implementation{Name: "mock", Version: "1.0"},
				Capabilities:    ares_mcp.ServerCapabilities{},
			}, nil
		case ares_mcp.MethodToolsList:
			return ares_mcp.ToolsListResult{Tools: tools}, nil
		case ares_mcp.MethodToolsCall:
			var p ares_mcp.ToolCallParams
			if len(params) > 0 {
				_ = json.Unmarshal(params, &p)
			}
			return ares_mcp.ToolCallResult{
				Content: []ares_mcp.ContentBlock{{Type: "text", Text: "result:" + p.Name}},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected method: %s", method)
		}
	}
	client := ares_mcp.NewMCPClient(ares_mcp.MCPClientConfig{
		ServerName: "mock-server",
		Timeout:    5 * time.Second,
	})
	if err := client.Connect(ctx, newMockTransport(handler)); err != nil {
		t.Fatalf("failed to connect mock client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// mockMCPExecutor implements dashboard.MCPExecutor for orchestrator testing.
type mockMCPExecutor struct {
	callToolFn  func(ctx context.Context, name string, args map[string]any) (*dashboard.MCPToolResult, error)
	listToolsFn func(ctx context.Context) ([]dashboard.MCPToolInfo, error)
}

func (m *mockMCPExecutor) CallTool(ctx context.Context, name string, args map[string]any) (*dashboard.MCPToolResult, error) {
	if m.callToolFn != nil {
		return m.callToolFn(ctx, name, args)
	}
	return &dashboard.MCPToolResult{Content: []dashboard.MCPContentBlock{{Type: "text", Text: "ok"}}}, nil
}

func (m *mockMCPExecutor) ListTools(ctx context.Context) ([]dashboard.MCPToolInfo, error) {
	if m.listToolsFn != nil {
		return m.listToolsFn(ctx)
	}
	return []dashboard.MCPToolInfo{{Name: "mock_tool", Description: "mock tool"}}, nil
}

// mockLLMExecutor implements dashboard.LLMExecutor for orchestrator testing.
type mockLLMExecutor struct {
	generateFn func(ctx context.Context, prompt string) (string, error)
}

func (m *mockLLMExecutor) Generate(ctx context.Context, prompt string) (string, error) {
	if m.generateFn != nil {
		return m.generateFn(ctx, prompt)
	}
	return "analysis result", nil
}

// mockLLMAdapter implements output.LLMAdapter for LLMAdapter testing.
type mockLLMAdapter struct {
	generateFn func(ctx context.Context, prompt string) (string, error)
}

func (m *mockLLMAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	if m.generateFn != nil {
		return m.generateFn(ctx, prompt)
	}
	return "mock response", nil
}

// ──────────────────────────────────────────────
// config.go
// ──────────────────────────────────────────────

func TestLoadServiceConfig_FileNotFound(t *testing.T) {
	cfg, err := LoadServiceConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
	if cfg != nil {
		t.Fatal("expected nil config on error")
	}
}

func TestLoadServiceConfig_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
llm:
  provider: openai
  model: gpt-4
  base_url: https://api.openai.com/v1
  api_key: sk-test123
  timeout: 30
  max_prompt_length: 8000
mcp:
  servers:
    - name: codegraph
      transport:
        stdio:
          command: codegraph
          args: ["--port", "8080"]
dashboard:
  addr: ":9090"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadServiceConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.LLM.Provider != "openai" {
		t.Errorf("LLM.Provider = %q, want %q", cfg.LLM.Provider, "openai")
	}
	if cfg.LLM.Model != "gpt-4" {
		t.Errorf("LLM.Model = %q, want %q", cfg.LLM.Model, "gpt-4")
	}
	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("LLM.BaseURL = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}
	if cfg.LLM.APIKey != "sk-test123" {
		t.Errorf("LLM.APIKey = %q, want %q", cfg.LLM.APIKey, "sk-test123")
	}
	if cfg.LLM.Timeout != 30 {
		t.Errorf("LLM.Timeout = %d, want 30", cfg.LLM.Timeout)
	}
	if cfg.LLM.MaxPromptLength != 8000 {
		t.Errorf("LLM.MaxPromptLength = %d, want 8000", cfg.LLM.MaxPromptLength)
	}
	if len(cfg.MCP.Servers) != 1 {
		t.Fatalf("len(MCP.Servers) = %d, want 1", len(cfg.MCP.Servers))
	}
	if cfg.MCP.Servers[0].Name != "codegraph" {
		t.Errorf("MCP.Servers[0].Name = %q, want %q", cfg.MCP.Servers[0].Name, "codegraph")
	}
	if cfg.MCP.Servers[0].Transport.Stdio.Command != "codegraph" {
		t.Errorf("MCP.Servers[0].Transport.Stdio.Command = %q, want %q", cfg.MCP.Servers[0].Transport.Stdio.Command, "codegraph")
	}
	if cfg.Dashboard.Addr != ":9090" {
		t.Errorf("Dashboard.Addr = %q, want %q", cfg.Dashboard.Addr, ":9090")
	}
}

func TestLoadServiceConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	content := `llm: { provider: openai model: gpt-4 }` // invalid YAML (missing colon in map)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadServiceConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestServiceConfig_Defaults(t *testing.T) {
	var cfg ServiceConfig
	if cfg.LLM.Provider != "" {
		t.Errorf("expected empty Provider, got %q", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "" {
		t.Errorf("expected empty Model, got %q", cfg.LLM.Model)
	}
	if cfg.Dashboard.Addr != "" {
		t.Errorf("expected empty Dashboard.Addr, got %q", cfg.Dashboard.Addr)
	}
	if len(cfg.MCP.Servers) != 0 {
		t.Errorf("expected empty MCP.Servers, got %d", len(cfg.MCP.Servers))
	}
}

// ──────────────────────────────────────────────
// reviews.go
// ──────────────────────────────────────────────

func TestDefaultReviewTasks_AllFive(t *testing.T) {
	if len(DefaultReviewTasks) != 5 {
		t.Fatalf("expected 5 DefaultReviewTasks, got %d", len(DefaultReviewTasks))
	}
	expectedNames := []string{
		"Architecture Review",
		"Error Handling Review",
		"Concurrency Review",
		"API Surface Review",
		"Change Impact Analysis",
	}
	for i, task := range DefaultReviewTasks {
		if task.Name != expectedNames[i] {
			t.Errorf("DefaultReviewTasks[%d].Name = %q, want %q", i, task.Name, expectedNames[i])
		}
		if len(task.Tools) == 0 {
			t.Errorf("DefaultReviewTasks[%d] has no tools", i)
		}
		if task.Prompt == "" {
			t.Errorf("DefaultReviewTasks[%d] has empty prompt", i)
		}
	}
}

func TestBuildAgentRequest_WithArgs(t *testing.T) {
	task := ReviewTask{
		Name:   "test-review",
		Tools:  [][2]string{{"search", "func main"}, {"context", "analyze dependencies"}},
		Prompt: "Test prompt {{.raw_data}}",
	}
	req := BuildAgentRequest(task)
	if req.Name != task.Name {
		t.Errorf("Name = %q, want %q", req.Name, task.Name)
	}
	if req.LLMPrompt != task.Prompt {
		t.Errorf("LLMPrompt = %q, want %q", req.LLMPrompt, task.Prompt)
	}
	if len(req.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(req.Steps))
	}
	if req.Steps[0].Tool != "search" {
		t.Errorf("Steps[0].Tool = %q, want %q", req.Steps[0].Tool, "search")
	}
	if req.Steps[0].Args["search"] != "func main" {
		t.Errorf("Steps[0].Args[search] = %q, want %q", req.Steps[0].Args["search"], "func main")
	}
	if req.Steps[1].Tool != "context" {
		t.Errorf("Steps[1].Tool = %q, want %q", req.Steps[1].Tool, "context")
	}
	if req.Steps[1].Args["task"] != "analyze dependencies" {
		t.Errorf("Steps[1].Args[task] = %q, want %q", req.Steps[1].Args["task"], "analyze dependencies")
	}
}

func TestBuildAgentRequest_EmptyArgs(t *testing.T) {
	task := ReviewTask{
		Name:   "no-args",
		Tools:  [][2]string{{"files", ""}}, // empty argValue
		Prompt: "test",
	}
	req := BuildAgentRequest(task)
	if len(req.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(req.Steps))
	}
	if req.Steps[0].Tool != "files" {
		t.Errorf("Steps[0].Tool = %q, want %q", req.Steps[0].Tool, "files")
	}
	if req.Steps[0].Args != nil {
		t.Errorf("expected nil Args for empty argValue, got %v", req.Steps[0].Args)
	}
}

func TestBuildAgentRequest_UnknownShortName(t *testing.T) {
	task := ReviewTask{
		Name:   "unknown",
		Tools:  [][2]string{{"nonexistent", "value"}},
		Prompt: "test",
	}
	req := BuildAgentRequest(task)
	if len(req.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(req.Steps))
	}
	if req.Steps[0].Tool != "nonexistent" {
		t.Errorf("Steps[0].Tool = %q, want %q", req.Steps[0].Tool, "nonexistent")
	}
	if req.Steps[0].Args != nil {
		t.Errorf("expected nil Args for unknown shortName, got %v", req.Steps[0].Args)
	}
}

func TestBuildAgentRequest_AllDefaultTasks(t *testing.T) {
	for i, task := range DefaultReviewTasks {
		req := BuildAgentRequest(task)
		if req.Name != task.Name {
			t.Errorf("task %d: Name = %q, want %q", i, req.Name, task.Name)
		}
		if req.LLMPrompt != task.Prompt {
			t.Errorf("task %d: LLMPrompt mismatch", i)
		}
		if len(req.Steps) != len(task.Tools) {
			t.Errorf("task %d: Steps count = %d, want %d", i, len(req.Steps), len(task.Tools))
		}
		for j, step := range req.Steps {
			if step.Tool != task.Tools[j][0] {
				t.Errorf("task %d, step %d: Tool = %q, want %q", i, j, step.Tool, task.Tools[j][0])
			}
		}
	}
}

func TestReviewArgKeys(t *testing.T) {
	expected := map[string]string{
		"search":  "search",
		"context": "task",
		"callers": "symbol",
		"impact":  "symbol",
	}
	for k, v := range expected {
		if got, ok := reviewArgKeys[k]; !ok {
			t.Errorf("reviewArgKeys missing key %q", k)
		} else if got != v {
			t.Errorf("reviewArgKeys[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestBuildTemplates(t *testing.T) {
	templates := BuildTemplates()
	if len(templates) != len(DefaultReviewTasks) {
		t.Fatalf("expected %d templates, got %d", len(DefaultReviewTasks), len(templates))
	}
	for i, tmpl := range templates {
		if tmpl.ID != fmt.Sprintf("tpl-%d", i) {
			t.Errorf("template %d: ID = %q, want %q", i, tmpl.ID, fmt.Sprintf("tpl-%d", i))
		}
		if tmpl.Name != DefaultReviewTasks[i].Name {
			t.Errorf("template %d: Name = %q, want %q", i, tmpl.Name, DefaultReviewTasks[i].Name)
		}
		if tmpl.LLMPrompt != DefaultReviewTasks[i].Prompt {
			t.Errorf("template %d: LLMPrompt mismatch", i)
		}
		expectedTool := ""
		if len(DefaultReviewTasks[i].Tools) > 0 {
			expectedTool = DefaultReviewTasks[i].Tools[0][0]
		}
		if tmpl.MCPTool != expectedTool {
			t.Errorf("template %d: MCPTool = %q, want %q", i, tmpl.MCPTool, expectedTool)
		}
	}
}

func TestBuildToolAliases(t *testing.T) {
	tools := []ares_mcp.MCPToolDef{
		{Name: "codegraph_files", Description: "File listing tool"},
		{Name: "codegraph_search", Description: "Code search tool"},
		{Name: "web_fetch", Description: "Web fetch tool"},
	}
	aliases := BuildToolAliases(tools)
	// Each full name maps to itself
	if aliases["codegraph_files"] != "codegraph_files" {
		t.Errorf("expected codegraph_files -> codegraph_files")
	}
	if aliases["codegraph_search"] != "codegraph_search" {
		t.Errorf("expected codegraph_search -> codegraph_search")
	}
	// Short names from suffix after _
	if aliases["files"] != "codegraph_files" {
		t.Errorf("expected files -> codegraph_files, got %q", aliases["files"])
	}
	if aliases["search"] != "codegraph_search" {
		t.Errorf("expected search -> codegraph_search, got %q", aliases["search"])
	}
}

func TestBuildToolAliases_Empty(t *testing.T) {
	aliases := BuildToolAliases(nil)
	if aliases == nil {
		t.Fatal("expected non-nil map")
	}
	if len(aliases) != 0 {
		t.Errorf("expected empty map, got %d entries", len(aliases))
	}
}

// ──────────────────────────────────────────────
// store.go
// ──────────────────────────────────────────────

func TestNewEventStore_NonNil(t *testing.T) {
	es, err := NewEventStore()
	if err != nil {
		t.Fatalf("NewEventStore() returned error: %v", err)
	}
	if es == nil {
		t.Fatal("NewEventStore() returned nil")
	}
}

func TestEventStore_RawStore(t *testing.T) {
	es, err := NewEventStore()
	if err != nil {
		t.Fatalf("NewEventStore() returned error: %v", err)
	}
	raw := es.RawStore()
	if raw == nil {
		t.Fatal("RawStore() returned nil")
	}
}

func TestEventStore_CompactableEmbedding(t *testing.T) {
	es, err := NewEventStore()
	if err != nil {
		t.Fatalf("NewEventStore() returned error: %v", err)
	}
	if es.CompactableEventStore == nil {
		t.Fatal("CompactableEventStore is nil (embedding failed)")
	}
	// Verify promoted methods are available — ForceCompact is promoted from CompactableEventStore.
	compacted, err := es.ForceCompact(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("ForceCompact via promoted method failed: %v", err)
	}
	if compacted {
		t.Error("expected no compaction for nonexistent stream")
	}
	// Verify Append is promoted (both EventStore and CompactableEventStore have it).
	evt := &ares_events.Event{
		ID: "test-id", Type: "test", StreamID: "test-stream",
		Payload: map[string]any{}, Timestamp: time.Now(),
	}
	if err := es.Append(context.Background(), "test-stream", []*ares_events.Event{evt}, 0); err != nil {
		t.Fatalf("Append via promoted method failed: %v", err)
	}
}

// ──────────────────────────────────────────────
// adapters.go — MCPAdapter
// ──────────────────────────────────────────────

func TestMCPAdapter_ListTools(t *testing.T) {
	ctx := context.Background()
	tools := []ares_mcp.MCPToolDef{
		{Name: "tool_a", Description: "Tool A"},
		{Name: "tool_b", Description: "Tool B"},
	}
	client := newMockClient(ctx, t, tools)
	adapter := &MCPAdapter{Client: client}

	result, err := adapter.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}
	if result[0].Name != "tool_a" || result[0].Description != "Tool A" {
		t.Errorf("result[0] = %+v, want {tool_a Tool A}", result[0])
	}
	if result[1].Name != "tool_b" || result[1].Description != "Tool B" {
		t.Errorf("result[1] = %+v, want {tool_b Tool B}", result[1])
	}
}

func TestMCPAdapter_CallTool(t *testing.T) {
	ctx := context.Background()
	client := newMockClient(ctx, t, nil)
	adapter := &MCPAdapter{Client: client}

	result, err := adapter.CallTool(ctx, "test_tool", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Errorf("expected IsError=false, got true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Type != "text" {
		t.Errorf("Content[0].Type = %q, want %q", result.Content[0].Type, "text")
	}
	if result.Content[0].Text != "result:test_tool" {
		t.Errorf("Content[0].Text = %q, want %q", result.Content[0].Text, "result:test_tool")
	}
}

// ──────────────────────────────────────────────
// adapters.go — MultiMCPAdapter
// ──────────────────────────────────────────────

func TestMultiMCPAdapter_NewAndCallTool(t *testing.T) {
	ctx := context.Background()
	clientA := newMockClient(ctx, t, []ares_mcp.MCPToolDef{
		{Name: "tool_a", Description: "From server A"},
	})
	clientB := newMockClient(ctx, t, []ares_mcp.MCPToolDef{
		{Name: "tool_b", Description: "From server B"},
	})
	entries := []clientTools{
		{client: clientA, name: "server-a", tools: []ares_mcp.MCPToolDef{{Name: "tool_a", Description: "From server A"}}},
		{client: clientB, name: "server-b", tools: []ares_mcp.MCPToolDef{{Name: "tool_b", Description: "From server B"}}},
	}
	adapter := NewMultiMCPAdapter(entries)
	if adapter == nil {
		t.Fatal("NewMultiMCPAdapter returned nil")
	}

	result, err := adapter.CallTool(ctx, "tool_a", nil)
	if err != nil {
		t.Fatalf("CallTool(tool_a) failed: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Text != "result:tool_a" {
		t.Errorf("got text %q, want %q", result.Content[0].Text, "result:tool_a")
	}
}

func TestMultiMCPAdapter_CallTool_NotFound(t *testing.T) {
	ctx := context.Background()
	adapter := NewMultiMCPAdapter(nil)
	_, err := adapter.CallTool(ctx, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
}

func TestMultiMCPAdapter_ListTools_Dedup(t *testing.T) {
	ctx := context.Background()
	client := newMockClient(ctx, t, []ares_mcp.MCPToolDef{
		{Name: "tool_x", Description: "Dup tool"},
	})
	entries := []clientTools{
		{client: client, name: "s1", tools: []ares_mcp.MCPToolDef{{Name: "tool_x", Description: "Dup tool"}}},
		{client: client, name: "s2", tools: []ares_mcp.MCPToolDef{{Name: "tool_x", Description: "Dup tool"}}},
	}
	adapter := NewMultiMCPAdapter(entries)

	result, err := adapter.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 tool (deduplicated), got %d", len(result))
	}
}

// ──────────────────────────────────────────────
// adapters.go — LLMAdapter
// ──────────────────────────────────────────────

func TestLLMAdapter_Generate(t *testing.T) {
	ctx := context.Background()
	mock := &mockLLMAdapter{
		generateFn: func(ctx context.Context, prompt string) (string, error) {
			if prompt != "hello" {
				t.Errorf("unexpected prompt: %q", prompt)
			}
			return "world", nil
		},
	}
	adapter := &LLMAdapter{Adapter: mock}
	result, err := adapter.Generate(ctx, "hello")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if result != "world" {
		t.Errorf("result = %q, want %q", result, "world")
	}
}

func TestLLMAdapter_Generate_Error(t *testing.T) {
	ctx := context.Background()
	mock := &mockLLMAdapter{
		generateFn: func(ctx context.Context, prompt string) (string, error) {
			return "", fmt.Errorf("llm error")
		},
	}
	adapter := &LLMAdapter{Adapter: mock}
	_, err := adapter.Generate(ctx, "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ──────────────────────────────────────────────
// adapters.go — MCPStatusBridge
// ──────────────────────────────────────────────

func TestMCPStatusBridge_WithServers(t *testing.T) {
	bridge := &MCPStatusBridge{
		Tools: []ares_mcp.MCPToolDef{
			{Name: "tool_a", Description: "Tool A"},
		},
		Servers: []MCPStatusServer{
			{Name: "server-1", Tools: []ares_mcp.MCPToolDef{{Name: "tool_a", Description: "Tool A"}}},
		},
	}
	views := bridge.ListServers()
	if len(views) != 1 {
		t.Fatalf("expected 1 server view, got %d", len(views))
	}
	if views[0].Name != "server-1" {
		t.Errorf("Name = %q, want %q", views[0].Name, "server-1")
	}
	if !views[0].Connected {
		t.Errorf("expected Connected=true")
	}
	if views[0].ToolCount != 1 {
		t.Errorf("ToolCount = %d, want 1", views[0].ToolCount)
	}
	if len(views[0].Tools) != 1 {
		t.Fatalf("expected 1 tool view, got %d", len(views[0].Tools))
	}
	if views[0].Tools[0].Name != "tool_a" {
		t.Errorf("Tool[0].Name = %q, want %q", views[0].Tools[0].Name, "tool_a")
	}
	if views[0].Tools[0].ServerName != "server-1" {
		t.Errorf("Tool[0].ServerName = %q, want %q", views[0].Tools[0].ServerName, "server-1")
	}
}

func TestMCPStatusBridge_WithoutServers(t *testing.T) {
	bridge := &MCPStatusBridge{
		Tools: []ares_mcp.MCPToolDef{
			{Name: "tool_a", Description: "Tool A"},
			{Name: "tool_b", Description: "Tool B"},
		},
	}
	views := bridge.ListServers()
	if len(views) != 1 {
		t.Fatalf("expected 1 server view (fallback), got %d", len(views))
	}
	if views[0].Name != "ares_mcp" {
		t.Errorf("Name = %q, want %q", views[0].Name, "ares_mcp")
	}
	if views[0].ToolCount != 2 {
		t.Errorf("ToolCount = %d, want 2", views[0].ToolCount)
	}
	if len(views[0].Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(views[0].Tools))
	}
	if views[0].Tools[0].ServerName != "ares_mcp" {
		t.Errorf("Tool[0].ServerName = %q, want %q", views[0].Tools[0].ServerName, "ares_mcp")
	}
}

func TestMCPStatusBridge_Empty(t *testing.T) {
	bridge := &MCPStatusBridge{}
	views := bridge.ListServers()
	if len(views) != 1 {
		t.Fatalf("expected 1 fallback view, got %d", len(views))
	}
	if views[0].ToolCount != 0 {
		t.Errorf("ToolCount = %d, want 0", views[0].ToolCount)
	}
}

// ──────────────────────────────────────────────
// adapters.go — ArenaAdapter (without real Orchestrator)
// ──────────────────────────────────────────────

func TestArenaAdapter_Stats_Empty(t *testing.T) {
	a := &ArenaAdapter{}
	stats := a.Stats()
	if stats["total_actions"] != 0 {
		t.Errorf("total_actions = %v, want 0", stats["total_actions"])
	}
	if stats["successful_actions"] != 0 {
		t.Errorf("successful_actions = %v, want 0", stats["successful_actions"])
	}
	if stats["failed_actions"] != 0 {
		t.Errorf("failed_actions = %v, want 0", stats["failed_actions"])
	}
	if stats["paused_agents"] != 0 {
		t.Errorf("paused_agents = %v, want 0", stats["paused_agents"])
	}
	if stats["slow_agents"] != 0 {
		t.Errorf("slow_agents = %v, want 0", stats["slow_agents"])
	}
}

func TestArenaAdapter_History_Empty(t *testing.T) {
	a := &ArenaAdapter{}
	if h := a.History(); h != nil {
		t.Errorf("expected nil history, got %v", h)
	}
}

func TestArenaAdapter_ResilienceScore_Empty(t *testing.T) {
	a := &ArenaAdapter{}
	score := a.ResilienceScore()
	if score["score"] != 100.0 {
		t.Errorf("score = %v, want 100.0", score["score"])
	}
	if score["grade"] != "A+" {
		t.Errorf("grade = %v, want A+", score["grade"])
	}
}

func TestArenaAdapter_ResilienceScore_Partial(t *testing.T) {
	a := &ArenaAdapter{}
	a.totalActions = 10
	a.successfulActions = 7
	a.failedActions = 3
	score := a.ResilienceScore()
	expectedScore := math.Round(float64(7)/float64(10)*100*10) / 10
	if score["score"] != expectedScore {
		t.Errorf("score = %v, want %v", score["score"], expectedScore)
	}
	// 7/10 = 70% maps to "C-"
	if score["grade"] != "C-" {
		t.Errorf("grade = %v, want C-", score["grade"])
	}
}

func TestArenaAdapter_ResilienceScore_FullRange(t *testing.T) {
	tests := []struct {
		success int
		total   int
		grade   string
	}{
		{100, 100, "A+"},
		{97, 100, "A+"},
		{95, 100, "A"},
		{93, 100, "A"},
		{91, 100, "A-"},
		{90, 100, "A-"},
		{88, 100, "B+"},
		{85, 100, "B"},
		{82, 100, "B-"},
		{80, 100, "B-"},
		{78, 100, "C+"},
		{75, 100, "C"},
		{72, 100, "C-"},
		{70, 100, "C-"},
		{68, 100, "D+"},
		{65, 100, "D"},
		{62, 100, "D-"},
		{60, 100, "D-"},
		{50, 100, "F"},
		{0, 100, "F"},
	}
	for _, tt := range tests {
		a := &ArenaAdapter{totalActions: tt.total, successfulActions: tt.success, failedActions: tt.total - tt.success}
		score := a.ResilienceScore()
		if score["grade"] != tt.grade {
			t.Errorf("success=%d/%d: grade = %v, want %s", tt.success, tt.total, score["grade"], tt.grade)
		}
	}
}

func TestArenaAdapter_StartSurvival(t *testing.T) {
	a := &ArenaAdapter{}
	err := a.StartSurvival(context.Background())
	if err != nil {
		t.Fatalf("StartSurvival failed: %v", err)
	}
}

func TestArenaAdapter_StopSurvival(t *testing.T) {
	a := &ArenaAdapter{}
	err := a.StopSurvival()
	if err != nil {
		t.Fatalf("StopSurvival failed: %v", err)
	}
}

func TestArenaAdapter_GetResilienceScore(t *testing.T) {
	a := &ArenaAdapter{}
	score := a.GetResilienceScore()
	if score["score"] != 100.0 {
		t.Errorf("score = %v, want 100.0", score["score"])
	}
}

func TestArenaAdapter_GetSurvivalStatus(t *testing.T) {
	a := &ArenaAdapter{}
	status := a.GetSurvivalStatus()
	if status["running"] != false {
		t.Errorf("running = %v, want false", status["running"])
	}
	if status["mode"] != "chaos_demo" {
		t.Errorf("mode = %v, want chaos_demo", status["mode"])
	}
}

// ──────────────────────────────────────────────
// adapters.go — ArenaAdapter (with real Orchestrator)
// ──────────────────────────────────────────────

func TestArenaAdapter_Execute_KillLeader(t *testing.T) {
	slowMCP := &mockMCPExecutor{
		callToolFn: func(ctx context.Context, name string, args map[string]any) (*dashboard.MCPToolResult, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return &dashboard.MCPToolResult{Content: []dashboard.MCPContentBlock{{Type: "text", Text: "ok"}}}, nil
			}
		},
	}
	orch := dashboard.NewOrchestrator(slowMCP, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch, Store: ares_events.NewMemoryEventStore()}

	_, err := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "test-agent",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	// Give the agent goroutine time to start executing.
	time.Sleep(100 * time.Millisecond)

	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionKillLeader})
	if !result.Success {
		t.Fatal("expected KillLeader to succeed")
	}
}

func TestArenaAdapter_Execute_KillLeader_NoAgents(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch, Store: ares_events.NewMemoryEventStore()}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionKillLeader})
	if result.Success {
		t.Error("expected KillLeader to fail when no running agents")
	}
}

func TestArenaAdapter_Execute_KillAgent(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	id, err := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "target",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionKillAgent, TargetID: id})
	if !result.Success {
		t.Error("expected KillAgent to succeed")
	}
}

func TestArenaAdapter_Execute_KillAgent_NoTarget(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionKillAgent})
	if result.Success {
		t.Error("expected KillAgent with empty TargetID to fail")
	}
}

func TestArenaAdapter_Execute_KillAgent_NotFound(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionKillAgent, TargetID: "nonexistent"})
	if result.Success {
		t.Error("expected KillAgent for nonexistent ID to fail")
	}
}

func TestArenaAdapter_Execute_PauseAgent(t *testing.T) {
	adapter := &ArenaAdapter{}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionPauseAgent, TargetID: "agent-1"})
	if !result.Success {
		t.Error("expected PauseAgent to succeed (tracking-only)")
	}
	stats := adapter.Stats()
	if stats["paused_agents"] != 1 {
		t.Errorf("paused_agents = %v, want 1", stats["paused_agents"])
	}
}

func TestArenaAdapter_Execute_PauseAgent_NoTarget(t *testing.T) {
	adapter := &ArenaAdapter{}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionPauseAgent})
	if result.Success {
		t.Error("expected PauseAgent with no target to fail")
	}
}

func TestArenaAdapter_Execute_ResumeAgent(t *testing.T) {
	adapter := &ArenaAdapter{}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionResumeAgent, TargetID: "agent-1"})
	if !result.Success {
		t.Error("expected ResumeAgent to succeed")
	}
}

func TestArenaAdapter_Execute_SlowAgent(t *testing.T) {
	adapter := &ArenaAdapter{}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionSlowAgent, TargetID: "agent-1"})
	if !result.Success {
		t.Error("expected SlowAgent to succeed")
	}
	stats := adapter.Stats()
	if stats["slow_agents"] != 1 {
		t.Errorf("slow_agents = %v, want 1", stats["slow_agents"])
	}
}

func TestArenaAdapter_Execute_SlowAgent_NoTarget(t *testing.T) {
	adapter := &ArenaAdapter{}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionSlowAgent})
	if result.Success {
		t.Error("expected SlowAgent with no target to fail")
	}
}

func TestArenaAdapter_Execute_KillOrchestrator(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	_, err := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "agent-1",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}
	_, err = orch.CreateAgent(dashboard.AgentRequest{
		Name:  "agent-2",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionKillOrchestrator})
	if !result.Success {
		t.Error("expected KillOrchestrator to succeed")
	}
}

func TestArenaAdapter_Execute_NetworkPartition(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	id, _ := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "target",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionNetworkPartition, TargetID: id})
	if !result.Success {
		t.Error("expected NetworkPartition to succeed")
	}
}

func TestArenaAdapter_Execute_ToolTimeout(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	id, _ := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "target",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionToolTimeout, TargetID: id})
	if !result.Success {
		t.Error("expected ToolTimeout to succeed")
	}
}

func TestArenaAdapter_Execute_MemoryCorrupt(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	id, _ := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "target",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionMemoryCorrupt, TargetID: id})
	if !result.Success {
		t.Error("expected MemoryCorrupt to succeed")
	}
}

func TestArenaAdapter_Execute_MCPDisconnect(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	id, _ := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "target",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionMCPDisconnect, TargetID: id})
	if !result.Success {
		t.Error("expected MCPDisconnect to succeed")
	}
}

func TestArenaAdapter_Execute_MCPDisconnect_WithAdapter(t *testing.T) {
	ctx := context.Background()
	client := newMockClient(ctx, t, nil)
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)
	adapter := &ArenaAdapter{
		Orch:       orch,
		mcpAdapter: &MCPAdapter{Client: client},
	}
	id, _ := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "target",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionMCPDisconnect, TargetID: id})
	if !result.Success {
		t.Error("expected MCPDisconnect with adapter to succeed")
	}
}

func TestArenaAdapter_Execute_LLMFailure(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	adapter := &ArenaAdapter{Orch: orch}
	id, _ := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "target",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionLLMFailure, TargetID: id})
	if !result.Success {
		t.Error("expected LLMFailure to succeed")
	}
}

func TestArenaAdapter_Execute_LLMFailure_WithAdapter(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)
	adapter := &ArenaAdapter{
		Orch:       orch,
		llmAdapter: &LLMAdapter{Adapter: &mockLLMAdapter{}},
	}
	id, _ := orch.CreateAgent(dashboard.AgentRequest{
		Name:  "target",
		Steps: []dashboard.AgentStep{{Tool: "mock_tool"}},
	})
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionLLMFailure, TargetID: id})
	if !result.Success {
		t.Error("expected LLMFailure with adapter to succeed")
	}
}

func TestArenaAdapter_Execute_WithStore(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	adapter := &ArenaAdapter{Store: store}
	result := adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionResumeAgent})
	if !result.Success {
		t.Error("expected ResumeAgent to succeed")
	}
	evts, err := store.Read(context.Background(), "arena", ares_events.ReadOptions{Limit: 10, Direction: ares_events.ReadAscending})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if len(evts) == 0 {
		t.Fatal("expected events in store")
	}
	if evts[0].Type != "arena.action" {
		t.Errorf("event type = %q, want %q", evts[0].Type, "arena.action")
	}
}

func TestArenaAdapter_Stats_AfterActions(t *testing.T) {
	adapter := &ArenaAdapter{}
	adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionResumeAgent})                // success
	adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionPauseAgent, TargetID: "a1"}) // success
	adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionKillAgent})                  // fail (no target)
	adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionSlowAgent, TargetID: "a2"})  // success

	stats := adapter.Stats()
	if stats["total_actions"] != 4 {
		t.Errorf("total_actions = %v, want 4", stats["total_actions"])
	}
	if stats["successful_actions"] != 3 {
		t.Errorf("successful_actions = %v, want 3", stats["successful_actions"])
	}
	if stats["failed_actions"] != 1 {
		t.Errorf("failed_actions = %v, want 1", stats["failed_actions"])
	}
	if stats["paused_agents"] != 1 {
		t.Errorf("paused_agents = %v, want 1", stats["paused_agents"])
	}
	if stats["slow_agents"] != 1 {
		t.Errorf("slow_agents = %v, want 1", stats["slow_agents"])
	}
}

func TestArenaAdapter_History_AfterActions(t *testing.T) {
	adapter := &ArenaAdapter{}
	adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionResumeAgent, TargetID: "a1"})
	adapter.Execute(dashboard.ArenaAction{Type: dashboard.ArenaActionPauseAgent, TargetID: "a2"})

	h := adapter.History()
	if len(h) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(h))
	}
	if h[0].Action.TargetID != "a1" {
		t.Errorf("history[0].TargetID = %q, want %q", h[0].Action.TargetID, "a1")
	}
	if h[1].Action.TargetID != "a2" {
		t.Errorf("history[1].TargetID = %q, want %q", h[1].Action.TargetID, "a2")
	}

	h[0].Action.TargetID = "mutated"
	// Verify original was not mutated (copy)
	if adapter.history[0].Action.TargetID == "mutated" {
		t.Error("History() did not return a copy")
	}
}

// ──────────────────────────────────────────────
// service.go
// ──────────────────────────────────────────────

func TestService_Stop_Idempotent(t *testing.T) {
	s := &Service{}
	ctx := context.Background()

	err1 := s.Stop(ctx)
	if err1 != nil {
		t.Fatalf("first Stop failed: %v", err1)
	}
	err2 := s.Stop(ctx)
	if err2 != nil {
		t.Fatalf("second Stop should be no-op: %v", err2)
	}
}

func TestService_Stop_WithHTTPServer(t *testing.T) {
	s := &Service{
		httpServer: &http.Server{},
	}
	ctx := context.Background()
	err := s.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestService_Orchestrator_Accessor(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)
	s := &Service{orch: orch}
	if got := s.Orchestrator(); got != orch {
		t.Error("Orchestrator() returned different instance")
	}
}

func TestService_HTTPServer_Accessor(t *testing.T) {
	srv := &http.Server{Addr: ":9999"}
	s := &Service{httpServer: srv}
	if got := s.HTTPServer(); got != srv {
		t.Error("HTTPServer() returned different instance")
	}
}

func TestService_RunReview(t *testing.T) {
	orch := dashboard.NewOrchestrator(&mockMCPExecutor{}, &mockLLMExecutor{})
	t.Cleanup(orch.Stop)

	s := &Service{orch: orch}
	s.RunReview()

	agents := orch.ListAgents()
	if len(agents) != len(DefaultReviewTasks) {
		t.Errorf("expected %d agents created, got %d", len(DefaultReviewTasks), len(agents))
	}
	found := make(map[string]bool)
	for _, a := range agents {
		found[a.Name] = true
	}
	for _, task := range DefaultReviewTasks {
		if !found[task.Name] {
			t.Errorf("agent %q not created", task.Name)
		}
	}
}

func TestService_SetHTTPHandler(t *testing.T) {
	mux := http.NewServeMux()
	s := &Service{httpServer: &http.Server{}}
	s.SetHTTPHandler(mux)
	if s.httpServer.Handler != mux {
		t.Error("SetHTTPHandler did not set handler")
	}
}

// ──────────────────────────────────────────────
// roundTo (white-box)
// ──────────────────────────────────────────────

func TestRoundTo(t *testing.T) {
	tests := []struct {
		v    float64
		prec int
		want float64
	}{
		{3.14159, 2, 3.14},
		{3.14159, 1, 3.1},
		{3.14159, 0, 3.0},
		{99.99, 1, 100.0},
		{0.0, 2, 0.0},
	}
	for _, tt := range tests {
		got := roundTo(tt.v, tt.prec)
		if got != tt.want {
			t.Errorf("roundTo(%v, %d) = %v, want %v", tt.v, tt.prec, got, tt.want)
		}
	}
}
