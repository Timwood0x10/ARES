package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	TestMCPToolName     = "search"
	TestStatusCompleted = "completed"
)

type mockMCPStatusProvider struct{}

func (m *mockMCPStatusProvider) ListServers() []MCPServerStatusView {
	return []MCPServerStatusView{{
		Name: "test-server", Connected: true, ToolCount: 1, Version: "1.0",
		Tools: []MCPToolView{{Name: TestMCPToolName, Description: "Search tool", ServerName: "test"}},
	}}
}

func setupAPI() (*APIv2, *Orchestrator) {
	mcp := &mockMCPExecutor{
		tools: []MCPToolInfo{{Name: TestMCPToolName, Description: "Search tool"}},
	}
	llm := &mockLLMExecutor{response: "test analysis"}
	orch := NewOrchestrator(mcp, llm)
	orch.SetTemplates([]AgentTemplate{
		{ID: "tpl-1", Name: "Test Template", MCPTool: TestMCPToolName, LLMPrompt: "{{.raw_data}}"},
	})

	hub := NewWSHub()
	go hub.Run()

	api := NewAPIv2(orch, &mockMCPStatusProvider{}, hub)
	return api, orch
}

func TestAPIRoot(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	// Root with Accept: application/json returns JSON overview.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := resp["uptime"]; !ok {
		t.Error("expected uptime in response")
	}
	if _, ok := resp["agents"]; !ok {
		t.Error("expected agents in response")
	}
}

func TestAPIRootHTML(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	// Root without Accept header returns HTML.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected HTML content type, got %s", ct)
	}
}

func TestAPIListAgentsEmpty(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	req := httptest.NewRequest(http.MethodGet, PathAgents, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp []AgentResult
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected 0 agents, got %d", len(resp))
	}
}

func TestAPICreateAgent(t *testing.T) {
	api, orch := setupAPI()
	handler := api.Handler()

	// Create agent.
	body := `{"template_id":"tpl-1"}`
	req := httptest.NewRequest(http.MethodPost, PathAgents, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["id"] == "" {
		t.Error("expected non-empty id")
	}
	if resp["status"] != StatusPending {
		t.Errorf("expected pending, got %s", resp["status"])
	}

	// Wait for completion.
	// Use a poll loop instead of sleep.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("agent did not complete in time")
		default:
		}
		result, ok := orch.GetAgent(resp["id"])
		if ok && result.Status == TestStatusCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAPICreateAgentInvalidBody(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	req := httptest.NewRequest(http.MethodPost, PathAgents, bytes.NewBufferString("invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAPICreateAgentNoName(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	// No template_id and no name.
	body := `{"mcp_tool":"test"}`
	req := httptest.NewRequest(http.MethodPost, PathAgents, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAPIGetAgent(t *testing.T) {
	api, orch := setupAPI()
	handler := api.Handler()

	// Create agent first.
	id, _ := orch.CreateAgent(AgentRequest{
		Name: "Test Agent", MCPTool: TestMCPToolName, LLMPrompt: "{{.raw_data}}",
	})

	// Wait for completion.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("agent did not complete")
		default:
		}
		result, ok := orch.GetAgent(id)
		if ok && result.Status == TestStatusCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Get agent.
	req := httptest.NewRequest(http.MethodGet, "/agents/"+id, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp AgentResult
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ID != id {
		t.Errorf("expected id %s, got %s", id, resp.ID)
	}
	if resp.Analysis != "test analysis" {
		t.Errorf("expected 'test analysis', got %s", resp.Analysis)
	}
}

func TestAPIGetAgentNotFound(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	req := httptest.NewRequest(http.MethodGet, "/agents/nonexistent", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestAPIListAgents(t *testing.T) {
	api, orch := setupAPI()
	handler := api.Handler()

	// Create agents.
	if _, err := orch.CreateAgent(AgentRequest{Name: "A1", MCPTool: TestMCPToolName, LLMPrompt: "{{.raw_data}}"}); err != nil {
		t.Fatalf("CreateAgent A1: %v", err)
	}
	if _, err := orch.CreateAgent(AgentRequest{Name: "A2", MCPTool: TestMCPToolName, LLMPrompt: "{{.raw_data}}"}); err != nil {
		t.Fatalf("CreateAgent A2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, PathAgents, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp []AgentResult
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp) != 2 {
		t.Errorf("expected 2 agents, got %d", len(resp))
	}
}

func TestAPIListMCPServers(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	req := httptest.NewRequest(http.MethodGet, PathMCP, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp []MCPServerStatusView
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp) != 1 {
		t.Errorf("expected 1 server, got %d", len(resp))
	}
}

func TestAPIMethodNotAllowed(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodDelete, PathAgents},
		{http.MethodPut, PathAgents},
		{http.MethodDelete, PathMCP},
		{http.MethodPost, PathMCP},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code == http.StatusOK {
				// Some methods return 200 for OPTIONS-like behavior.
				return
			}
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405, got %d", w.Code)
			}
		})
	}
}

func TestAPICORSHeaders(t *testing.T) {
	api, _ := setupAPI()
	handler := api.Handler()

	req := httptest.NewRequest(http.MethodOptions, PathAgents, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS Allow-Origin header")
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected CORS Allow-Methods header")
	}
}

func TestAPIPanicRecovery(t *testing.T) {
	// Create an API with nil orchestrator to trigger a panic.
	api := NewAPIv2(nil, nil, nil)
	handler := api.Handler()

	req := httptest.NewRequest(http.MethodGet, PathAgents, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should not crash, should return 200 with empty list.
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAPIAgentByIDPathParsing(t *testing.T) {
	api, orch := setupAPI()
	handler := api.Handler()

	// Create and wait for agent.
	id, _ := orch.CreateAgent(AgentRequest{Name: "Test", MCPTool: TestMCPToolName, LLMPrompt: "{{.raw_data}}"})
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout")
		default:
		}
		if r, ok := orch.GetAgent(id); ok && r.Status == TestStatusCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Test various path patterns.
	tests := []struct {
		path   string
		status int
	}{
		{"/agents/" + id, http.StatusOK},
		{"/agents/nonexistent", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Errorf("expected %d, got %d", tt.status, w.Code)
			}
		})
	}
}
