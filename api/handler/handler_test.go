// Package handler — tests for all HTTP handlers.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/api/core"
)

// ---- Mocks ----

type mockAgentService struct {
	core.AgentService
	agents map[string]*core.Agent
}

func newMockAgentService() *mockAgentService {
	return &mockAgentService{agents: make(map[string]*core.Agent)}
}

func (m *mockAgentService) CreateAgent(_ context.Context, config *core.AgentConfig) (*core.Agent, error) {
	a := &core.Agent{ID: config.ID, Name: config.Name, Type: config.Type, Status: core.AgentStatusReady}
	m.agents[config.ID] = a
	return a, nil
}

func (m *mockAgentService) GetAgent(_ context.Context, agentID string) (*core.Agent, error) {
	a, ok := m.agents[agentID]
	if !ok {
		return nil, nil //nolint: nilnil
	}
	return a, nil
}

func (m *mockAgentService) ListAgents(_ context.Context, _ *core.AgentFilter) ([]*core.Agent, *core.PaginationResponse, error) {
	list := make([]*core.Agent, 0, len(m.agents))
	for _, a := range m.agents {
		list = append(list, a)
	}
	return list, nil, nil
}

func (m *mockAgentService) DeleteAgent(_ context.Context, agentID string) error {
	delete(m.agents, agentID)
	return nil
}

type mockMemoryService struct {
	core.MemoryService
	sessions map[string]*core.Session
	messages map[string][]*core.Message
}

func newMockMemoryService() *mockMemoryService {
	return &mockMemoryService{
		sessions: make(map[string]*core.Session),
		messages: make(map[string][]*core.Message),
	}
}

func (m *mockMemoryService) CreateSession(_ context.Context, config *core.SessionConfig) (string, error) {
	id := "session-" + config.UserID
	m.sessions[id] = &core.Session{ID: id, UserID: config.UserID}
	return id, nil
}

func (m *mockMemoryService) GetSession(_ context.Context, sessionID string) (*core.Session, error) {
	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, nil //nolint: nilnil
	}
	return s, nil
}

func (m *mockMemoryService) DeleteSession(_ context.Context, sessionID string) error {
	delete(m.sessions, sessionID)
	delete(m.messages, sessionID)
	return nil
}

func (m *mockMemoryService) AddMessage(_ context.Context, sessionID string, role core.MessageRole, content string) error {
	msg := &core.Message{Role: role, Content: content, Time: time.Now()}
	m.messages[sessionID] = append(m.messages[sessionID], msg)
	return nil
}

func (m *mockMemoryService) GetMessages(_ context.Context, sessionID string, _ *core.PaginationRequest) ([]*core.Message, error) {
	return m.messages[sessionID], nil
}

type mockWorkflowService struct {
	core.WorkflowService
	workflows  map[string]*core.WorkflowDefinition
	executions int
}

func newMockWorkflowService() *mockWorkflowService {
	return &mockWorkflowService{workflows: make(map[string]*core.WorkflowDefinition)}
}

func (m *mockWorkflowService) Execute(_ context.Context, req *core.WorkflowRequest) (*core.WorkflowResponse, error) {
	m.executions++
	return &core.WorkflowResponse{
		ExecutionID: "exec-1",
		WorkflowID:  req.WorkflowID,
		Status:      core.WorkflowStatusCompleted,
	}, nil
}

func (m *mockWorkflowService) ListWorkflows(_ context.Context) ([]*core.WorkflowSummary, error) {
	return []*core.WorkflowSummary{
		{ID: "wf-1", Name: "Test Workflow", StepCount: 3},
	}, nil
}

func (m *mockWorkflowService) GetWorkflow(_ context.Context, id string) (*core.WorkflowDefinition, error) {
	wf, ok := m.workflows[id]
	if !ok {
		return nil, nil //nolint: nilnil
	}
	return wf, nil
}

type mockArena struct{ core.Arena }

func (m *mockArena) InjectFault(_ context.Context, _ core.FaultType, _ string) error { return nil }

func (m *mockArena) Score() *core.ResilienceScore {
	return &core.ResilienceScore{Overall: 0.85, Recovery: 0.9, Stability: 0.8}
}

func (m *mockArena) RunRandom(_ context.Context, _ time.Duration) (*core.ArenaReport, error) {
	return &core.ArenaReport{Duration: 30, FaultsInjected: 5}, nil
}

func (m *mockArena) ListAgents() []string { return []string{"agent-a", "agent-b"} }

type mockRuntime struct{ core.Runtime }

func (m *mockRuntime) Start(_ context.Context) error { return nil }

func (m *mockRuntime) Stop() error { return nil }

func (m *mockRuntime) GetAgent(agentID string) core.Agent {
	if agentID == "agent-1" {
		return core.Agent{ID: "agent-1", Status: core.AgentStatusReady}
	}
	return core.Agent{}
}

func (m *mockRuntime) Stats() core.RuntimeStats {
	return core.RuntimeStats{ActiveAgents: 2, TotalRestarts: 100}
}

type mockRetrievalService struct {
	core.RetrievalService
	items []*core.KnowledgeItem
}

func newMockRetrievalService() *mockRetrievalService {
	return &mockRetrievalService{}
}

func (m *mockRetrievalService) Search(_ context.Context, _, query string) ([]*core.RetrievalResult, error) {
	return []*core.RetrievalResult{{Content: "result for " + query, Score: 0.95}}, nil
}

func (m *mockRetrievalService) AddKnowledge(_ context.Context, item *core.KnowledgeItem) (*core.KnowledgeItem, error) {
	item.ID = "item-1"
	m.items = append(m.items, item)
	return item, nil
}

func (m *mockRetrievalService) GetKnowledge(_ context.Context, _, itemID string) (*core.KnowledgeItem, error) {
	for _, item := range m.items {
		if item.ID == itemID {
			return item, nil
		}
	}
	return nil, nil //nolint: nilnil
}

func (m *mockRetrievalService) DeleteKnowledge(_ context.Context, _, itemID string) error {
	for i, item := range m.items {
		if item.ID == itemID {
			m.items = append(m.items[:i], m.items[i+1:]...)
			break
		}
	}
	return nil
}

type mockEvaluatorRegistry struct {
	core.EvaluatorRegistry
}

func (m *mockEvaluatorRegistry) Get(name string) core.Evaluator {
	if name == "exact_match" {
		return &mockEvaluator{}
	}
	return nil
}

type mockEvaluator struct{ core.Evaluator }

func (m *mockEvaluator) Evaluate(_ context.Context, _, output, expected string) (float64, error) {
	if output == expected {
		return 1.0, nil
	}
	return 0.0, nil
}

type mockFlightRecorder struct{ core.FlightRecorder }

func (m *mockFlightRecorder) Replay(_ context.Context, sessionID string) (interface{}, error) {
	return map[string]string{"session_id": sessionID, "events": "replayed"}, nil
}

func (m *mockFlightRecorder) Stop() {}

// ---- Tests ----

func TestAgentHandler_CreateAndGet(t *testing.T) {
	t.Parallel()

	h := NewAgentHandler(newMockAgentService())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/agents", h.HandleCreate)
	mux.HandleFunc("GET /api/v1/agents/{id}", h.HandleGet)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Create agent.
	reqBody := `{"id":"test-agent","name":"Tester","type":"leader"}`
	resp, err := http.Post(srv.URL+"/api/v1/agents", "application/json", strBody(reqBody))
	if err != nil {
		t.Fatalf("POST /agents: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created core.Agent
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID != "test-agent" {
		t.Fatalf("expected id=test-agent, got %s", created.ID)
	}

	// Get agent.
	resp2, err := http.Get(srv.URL + "/api/v1/agents/test-agent")
	if err != nil {
		t.Fatalf("GET /agents/test-agent: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestAgentHandler_List(t *testing.T) {
	t.Parallel()

	mock := newMockAgentService()
	_, _ = mock.CreateAgent(context.TODO(), &core.AgentConfig{ID: "a1"})
	_, _ = mock.CreateAgent(context.TODO(), &core.AgentConfig{ID: "a2"})

	h := NewAgentHandler(mock)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agents", h.HandleList)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/agents")
	if err != nil {
		t.Fatalf("GET /agents: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var agents []*core.Agent
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

func TestAgentHandler_Delete(t *testing.T) {
	t.Parallel()

	h := NewAgentHandler(newMockAgentService())
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/agents/{id}", h.HandleDelete)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/agents/test-x", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /agents: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMemoryHandler_CreateSession(t *testing.T) {
	t.Parallel()

	h := NewMemoryHandler(newMockMemoryService())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions", h.HandleCreateSession)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/sessions", "application/json", strBody(`{"user_id":"u1"}`))
	if err != nil {
		t.Fatalf("POST /sessions: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["session_id"] == "" {
		t.Fatal("expected non-empty session_id")
	}
}

func TestMemoryHandler_AddGetMessages(t *testing.T) {
	t.Parallel()

	mock := newMockMemoryService()
	_, _ = mock.CreateSession(context.TODO(), &core.SessionConfig{UserID: "u1"})

	h := NewMemoryHandler(mock)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/messages", h.HandleAddMessage)
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", h.HandleGetMessages)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Add message.
	resp, err := http.Post(srv.URL+"/api/v1/sessions/session-u1/messages", "application/json", strBody(`{"role":"user","content":"hello"}`))
	if err != nil {
		t.Fatalf("POST /messages: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Get messages.
	resp2, err := http.Get(srv.URL + "/api/v1/sessions/session-u1/messages")
	if err != nil {
		t.Fatalf("GET /messages: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	var msgs []*core.Message
	if err := json.NewDecoder(resp2.Body).Decode(&msgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestWorkflowHandler_Execute(t *testing.T) {
	t.Parallel()

	h := NewWorkflowHandler(newMockWorkflowService())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/workflows/execute", h.HandleExecute)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/workflows/execute", "application/json", strBody(`{"workflow_id":"wf-1"}`))
	if err != nil {
		t.Fatalf("POST /workflows/execute: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result core.WorkflowResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.WorkflowID != "wf-1" {
		t.Fatalf("expected workflow_id=wf-1, got %s", result.WorkflowID)
	}
}

func TestWorkflowHandler_List(t *testing.T) {
	t.Parallel()

	h := NewWorkflowHandler(newMockWorkflowService())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workflows", h.HandleList)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/workflows")
	if err != nil {
		t.Fatalf("GET /workflows: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestArenaHandler_InjectFault(t *testing.T) {
	t.Parallel()

	h := NewArenaHandler(&mockArena{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/arena/faults", h.HandleInjectFault)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/arena/faults", "application/json", strBody(`{"fault_type":"kill_agent","target_id":"agent-1"}`))
	if err != nil {
		t.Fatalf("POST /arena/faults: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestArenaHandler_Score(t *testing.T) {
	t.Parallel()

	h := NewArenaHandler(&mockArena{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/arena/score", h.HandleScore)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/arena/score")
	if err != nil {
		t.Fatalf("GET /arena/score: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var score core.ResilienceScore
	if err := json.NewDecoder(resp.Body).Decode(&score); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if score.Overall != 0.85 {
		t.Fatalf("expected overall=0.85, got %f", score.Overall)
	}
}

func TestRuntimeHandler_StartStop(t *testing.T) {
	t.Parallel()

	h := NewRuntimeHandler(&mockRuntime{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/runtime/start", h.HandleStart)
	mux.HandleFunc("POST /api/v1/runtime/stop", h.HandleStop)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Start.
	resp, err := http.Post(srv.URL+"/api/v1/runtime/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /runtime/start: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Stop.
	resp2, err := http.Post(srv.URL+"/api/v1/runtime/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /runtime/stop: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestRuntimeHandler_Stats(t *testing.T) {
	t.Parallel()

	h := NewRuntimeHandler(&mockRuntime{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runtime/stats", h.HandleStats)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/runtime/stats")
	if err != nil {
		t.Fatalf("GET /runtime/stats: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var stats core.RuntimeStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.ActiveAgents != 2 {
		t.Fatalf("expected ActiveAgents=2, got %d", stats.ActiveAgents)
	}
}

func TestRetrievalHandler_Search(t *testing.T) {
	t.Parallel()

	h := NewRetrievalHandler(newMockRetrievalService())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/knowledge/search", h.HandleSearch)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/knowledge/search", "application/json", strBody(`{"tenant_id":"t1","query":"test query"}`))
	if err != nil {
		t.Fatalf("POST /knowledge/search: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRetrievalHandler_AddGetDelete(t *testing.T) {
	t.Parallel()

	h := NewRetrievalHandler(newMockRetrievalService())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/knowledge", h.HandleAddKnowledge)
	mux.HandleFunc("GET /api/v1/knowledge/{tenant_id}/{id}", h.HandleGetKnowledge)
	mux.HandleFunc("DELETE /api/v1/knowledge/{tenant_id}/{id}", h.HandleDeleteKnowledge)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Add.
	resp, err := http.Post(srv.URL+"/api/v1/knowledge", "application/json", strBody(`{"tenant_id":"t1","content":"test content"}`))
	if err != nil {
		t.Fatalf("POST /knowledge: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created core.KnowledgeItem
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty ID")
	}

	// Get.
	resp2, err := http.Get(srv.URL + "/api/v1/knowledge/t1/" + created.ID)
	if err != nil {
		t.Fatalf("GET /knowledge: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}

	// Delete.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/knowledge/t1/"+created.ID, nil)
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /knowledge: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp3.StatusCode)
	}
}

func TestEvalHandler_Evaluate(t *testing.T) {
	t.Parallel()

	h := NewEvalHandler(&mockEvaluatorRegistry{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/eval/evaluate", h.HandleEvaluate)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/eval/evaluate", "application/json", strBody(`{"evaluator":"exact_match","input":"hello","output":"hello","expected":"hello"}`))
	if err != nil {
		t.Fatalf("POST /eval/evaluate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["score"] != 1.0 {
		t.Fatalf("expected score=1.0, got %f", result["score"])
	}
}

func TestFlightHandler_Replay(t *testing.T) {
	t.Parallel()

	h := NewFlightHandler(&mockFlightRecorder{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/flight/replay/{id}", h.HandleReplay)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/flight/replay/sess-1")
	if err != nil {
		t.Fatalf("GET /flight/replay: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandler_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func() *httptest.Server
		path       string
		body       string
		statusCode int
	}{
		{
			name: "agent: missing id",
			setup: func() *httptest.Server {
				h := NewAgentHandler(newMockAgentService())
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/agents", h.HandleCreate)
				return httptest.NewServer(mux)
			},
			path:       "/api/v1/agents",
			statusCode: http.StatusBadRequest,
		},
		{
			name: "workflow: missing workflow_id",
			setup: func() *httptest.Server {
				h := NewWorkflowHandler(newMockWorkflowService())
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/workflows/execute", h.HandleExecute)
				return httptest.NewServer(mux)
			},
			path:       "/api/v1/workflows/execute",
			statusCode: http.StatusBadRequest,
		},
		{
			name: "session: missing user_id",
			setup: func() *httptest.Server {
				h := NewMemoryHandler(newMockMemoryService())
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/sessions", h.HandleCreateSession)
				return httptest.NewServer(mux)
			},
			path:       "/api/v1/sessions",
			statusCode: http.StatusBadRequest,
		},
		{
			name: "arena: missing fault_type",
			setup: func() *httptest.Server {
				h := NewArenaHandler(&mockArena{})
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/arena/faults", h.HandleInjectFault)
				return httptest.NewServer(mux)
			},
			path:       "/api/v1/arena/faults",
			statusCode: http.StatusBadRequest,
		},
		{
			name: "knowledge: missing query",
			setup: func() *httptest.Server {
				h := NewRetrievalHandler(newMockRetrievalService())
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/knowledge/search", h.HandleSearch)
				return httptest.NewServer(mux)
			},
			path:       "/api/v1/knowledge/search",
			body:       `{"tenant_id":"t1"}`,
			statusCode: http.StatusBadRequest,
		},
		{
			name: "eval: missing evaluator",
			setup: func() *httptest.Server {
				h := NewEvalHandler(&mockEvaluatorRegistry{})
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/eval/evaluate", h.HandleEvaluate)
				return httptest.NewServer(mux)
			},
			path:       "/api/v1/eval/evaluate",
			statusCode: http.StatusBadRequest,
		},
		{
			name: "eval: unknown evaluator",
			setup: func() *httptest.Server {
				h := NewEvalHandler(&mockEvaluatorRegistry{})
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/eval/evaluate", h.HandleEvaluate)
				return httptest.NewServer(mux)
			},
			path:       "/api/v1/eval/evaluate",
			body:       `{"evaluator":"bad"}`,
			statusCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := tt.setup()
			defer srv.Close()

			body := tt.body
			if body == "" {
				body = `{}`
			}
			resp, err := http.Post(srv.URL+tt.path, "application/json", strBody(body))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.statusCode {
				t.Fatalf("expected %d, got %d", tt.statusCode, resp.StatusCode)
			}
		})
	}
}

func TestHandler_ServiceUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		bodyFn func(srv *httptest.Server) (*http.Response, error)
	}{
		{
			name: "workflow", method: "POST", path: "/api/v1/workflows/execute",
			bodyFn: func(srv *httptest.Server) (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/workflows/execute", "application/json", strBody(`{}`))
			},
		},
		{
			name: "agent", method: "POST", path: "/api/v1/agents",
			bodyFn: func(srv *httptest.Server) (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/agents", "application/json", strBody(`{}`))
			},
		},
		{
			name: "memory", method: "POST", path: "/api/v1/sessions",
			bodyFn: func(srv *httptest.Server) (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/sessions", "application/json", strBody(`{}`))
			},
		},
		{
			name: "arena", method: "POST", path: "/api/v1/arena/faults",
			bodyFn: func(srv *httptest.Server) (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/arena/faults", "application/json", strBody(`{}`))
			},
		},
		{
			name: "runtime", method: "POST", path: "/api/v1/runtime/start",
			bodyFn: func(srv *httptest.Server) (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/runtime/start", "application/json", nil)
			},
		},
		{
			name: "retrieval", method: "POST", path: "/api/v1/knowledge/search",
			bodyFn: func(srv *httptest.Server) (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/knowledge/search", "application/json", strBody(`{}`))
			},
		},
		{
			name: "eval", method: "POST", path: "/api/v1/eval/evaluate",
			bodyFn: func(srv *httptest.Server) (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/eval/evaluate", "application/json", strBody(`{}`))
			},
		},
		{
			name: "flight", method: "GET", path: "/api/v1/flight/replay/x",
			bodyFn: func(srv *httptest.Server) (*http.Response, error) {
				return http.Get(srv.URL + "/api/v1/flight/replay/x")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var srv *httptest.Server
			switch tt.name {
			case "workflow":
				h := NewWorkflowHandler(nil)
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/workflows/execute", h.HandleExecute)
				srv = httptest.NewServer(mux)
			case "agent":
				h := NewAgentHandler(nil)
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/agents", h.HandleCreate)
				srv = httptest.NewServer(mux)
			case "memory":
				h := NewMemoryHandler(nil)
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/sessions", h.HandleCreateSession)
				srv = httptest.NewServer(mux)
			case "arena":
				h := NewArenaHandler(nil)
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/arena/faults", h.HandleInjectFault)
				srv = httptest.NewServer(mux)
			case "runtime":
				h := NewRuntimeHandler(nil)
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/runtime/start", h.HandleStart)
				srv = httptest.NewServer(mux)
			case "retrieval":
				h := NewRetrievalHandler(nil)
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/knowledge/search", h.HandleSearch)
				srv = httptest.NewServer(mux)
			case "eval":
				h := NewEvalHandler(nil)
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/eval/evaluate", h.HandleEvaluate)
				srv = httptest.NewServer(mux)
			case "flight":
				h := NewFlightHandler(nil)
				mux := http.NewServeMux()
				mux.HandleFunc("GET /api/v1/flight/replay/{id}", h.HandleReplay)
				srv = httptest.NewServer(mux)
			}
			defer srv.Close()

			resp, err := tt.bodyFn(srv)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("expected 503, got %d", resp.StatusCode)
			}
		})
	}
}

// ---- Helpers ----

func strBody(s string) *bodyReader {
	return &bodyReader{data: s}
}

type bodyReader struct {
	data string
}

func (b *bodyReader) Read(p []byte) (int, error) {
	return copy(p, []byte(b.data)), nil
}

func (b *bodyReader) Close() error { return nil }
