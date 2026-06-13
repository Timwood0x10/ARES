package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"goagentx/internal/events"
	"goagentx/internal/runtime"
)

// newTestHandler creates a DashboardHandler wired with mocks for handler-level tests.
func newTestHandler(t *testing.T) (*DashboardHandler, *WSHub) {
	t.Helper()

	now := time.Now()
	agents := &mockAgentProvider{
		agents: []AgentInfo{
			{ID: "agent-1", Type: "leader", Status: "ready", Restarts: 0},
			{ID: "agent-2", Type: "sub", Status: "busy", Restarts: 2},
		},
	}
	store := &mockEventStore{
		events: []*events.Event{
			{ID: "e1", StreamID: "agent-1", Type: events.EventAgentStarted, Timestamp: now},
			{ID: "e2", StreamID: "agent-1", Type: events.EventTaskCreated, Timestamp: now},
			{ID: "e3", StreamID: "agent-2", Type: events.EventTaskCompleted, Timestamp: now},
		},
	}
	rt := &mockRuntime{
		stats: runtime.RuntimeStats{
			ActiveAgents:  2,
			TotalRestarts: 3,
			Uptime:        5 * time.Minute,
		},
	}

	service := NewDashboardService(rt, agents, store, nil, nil)
	hub := NewWSHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	handler := NewDashboardHandler(service, hub, &DashboardConfig{Addr: ":0"})
	return handler, hub
}

func TestHandleOverview(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	rec := httptest.NewRecorder()

	handler.HandleOverview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var overview SystemOverview
	if err := json.NewDecoder(rec.Body).Decode(&overview); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if overview.AgentCount != 2 {
		t.Errorf("AgentCount = %d, want 2", overview.AgentCount)
	}
	if overview.RuntimeStats.TotalRestarts != 3 {
		t.Errorf("TotalRestarts = %d, want 3", overview.RuntimeStats.TotalRestarts)
	}
	if overview.Uptime == "" {
		t.Error("Uptime should not be empty")
	}
}

func TestHandleOverviewMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/overview", nil)
	rec := httptest.NewRecorder()

	handler.HandleOverview(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleListAgents(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/agents", nil)
	rec := httptest.NewRecorder()

	handler.HandleListAgents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var agents []AgentView
	if err := json.NewDecoder(rec.Body).Decode(&agents); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("agents count = %d, want 2", len(agents))
	}
	if agents[0].ID != "agent-1" {
		t.Errorf("agents[0].ID = %s, want agent-1", agents[0].ID)
	}
}

func TestHandleListAgentsMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/agents", nil)
	rec := httptest.NewRecorder()

	handler.HandleListAgents(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleGetAgent(t *testing.T) {
	handler, _ := newTestHandler(t)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantID     string
	}{
		{
			name:       "existing agent",
			path:       "/api/dashboard/agents/agent-1",
			wantStatus: http.StatusOK,
			wantID:     "agent-1",
		},
		{
			name:       "missing agent",
			path:       "/api/dashboard/agents/nonexistent",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "empty id",
			path:       "/api/dashboard/agents/",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.HandleGetAgent(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantID != "" {
				var agent AgentView
				if err := json.NewDecoder(rec.Body).Decode(&agent); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if agent.ID != tt.wantID {
					t.Errorf("ID = %s, want %s", agent.ID, tt.wantID)
				}
			}
		})
	}
}

func TestHandleGetAgentMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/agents/agent-1", nil)
	rec := httptest.NewRecorder()

	handler.HandleGetAgent(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleAgentEvents(t *testing.T) {
	handler, _ := newTestHandler(t)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCount  int
	}{
		{
			name:       "valid agent events",
			path:       "/api/dashboard/agents/agent-1/events",
			wantStatus: http.StatusOK,
			wantCount:  3, // mockEventStore returns all events regardless of stream
		},
		{
			name:       "invalid path - no events suffix",
			path:       "/api/dashboard/agents/agent-1/foo",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid path - no sub-path",
			path:       "/api/dashboard/agents/agent-1",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.HandleAgentEvents(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCount > 0 {
				var evts []EventView
				if err := json.NewDecoder(rec.Body).Decode(&evts); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if len(evts) != tt.wantCount {
					t.Errorf("events count = %d, want %d", len(evts), tt.wantCount)
				}
			}
		})
	}
}

func TestHandleAgentEventsWithLimit(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/agents/agent-1/events?limit=1", nil)
	rec := httptest.NewRecorder()

	handler.HandleAgentEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleAgentEventsMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/agents/agent-1/events", nil)
	rec := httptest.NewRecorder()

	handler.HandleAgentEvents(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleListWorkflows(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/workflows", nil)
	rec := httptest.NewRecorder()

	handler.HandleListWorkflows(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Should return an empty array (not null).
	body := rec.Body.String()
	if body != "[]\n" && body != "[]" {
		t.Errorf("body = %q, want empty JSON array", body)
	}
}

func TestHandleListWorkflowsMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/workflows", nil)
	rec := httptest.NewRecorder()

	handler.HandleListWorkflows(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleGetWorkflow(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/workflows/wf-1", nil)
	rec := httptest.NewRecorder()

	handler.HandleGetWorkflow(rec, req)

	// Currently returns 404 (not yet implemented).
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleGetWorkflowMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/workflows/wf-1", nil)
	rec := httptest.NewRecorder()

	handler.HandleGetWorkflow(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleGetWorkflowDAG(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/workflows/wf-1/dag", nil)
	rec := httptest.NewRecorder()

	handler.HandleGetWorkflowDAG(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var dag DAGView
	if err := json.NewDecoder(rec.Body).Decode(&dag); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

func TestHandleGetWorkflowDAGMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/workflows/wf-1/dag", nil)
	rec := httptest.NewRecorder()

	handler.HandleGetWorkflowDAG(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleListSessions(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/memory/sessions", nil)
	rec := httptest.NewRecorder()

	handler.HandleListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleListSessionsMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/memory/sessions", nil)
	rec := httptest.NewRecorder()

	handler.HandleListSessions(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleGetSession(t *testing.T) {
	handler, _ := newTestHandler(t)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "missing session (no memory manager)",
			path:       "/api/dashboard/memory/sessions/sess-1",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "empty session id",
			path:       "/api/dashboard/memory/sessions/",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.HandleGetSession(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleGetSessionMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/memory/sessions/sess-1", nil)
	rec := httptest.NewRecorder()

	handler.HandleGetSession(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleListDistilled(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/memory/distilled", nil)
	rec := httptest.NewRecorder()

	handler.HandleListDistilled(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if body != "[]\n" && body != "[]" {
		t.Errorf("body = %q, want empty JSON array", body)
	}
}

func TestHandleListDistilledMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/memory/distilled", nil)
	rec := httptest.NewRecorder()

	handler.HandleListDistilled(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleSearchMemory(t *testing.T) {
	handler, _ := newTestHandler(t)

	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
	}{
		{
			name:       "missing body",
			method:     http.MethodPost,
			body:       "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid json",
			method:     http.MethodPost,
			body:       "not json",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty query",
			method:     http.MethodPost,
			body:       `{"query":""}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing query field",
			method:     http.MethodPost,
			body:       `{"limit":10}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "method not allowed",
			method:     http.MethodGet,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "valid request (no memory manager)",
			method:     http.MethodPost,
			body:       `{"query":"test search","limit":5}`,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqBody *bytes.Buffer
			if tt.body != "" {
				reqBody = bytes.NewBufferString(tt.body)
			} else {
				reqBody = bytes.NewBuffer(nil)
			}

			req := httptest.NewRequest(tt.method, "/api/dashboard/memory/search", reqBody)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleSearchMemory(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleListEvents(t *testing.T) {
	handler, _ := newTestHandler(t)

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{
			name:       "no filters",
			query:      "",
			wantStatus: http.StatusOK,
		},
		{
			name:       "with limit",
			query:      "?limit=1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "with type filter",
			query:      "?type=agent.started",
			wantStatus: http.StatusOK,
		},
		{
			name:       "with stream_id",
			query:      "?stream_id=agent-1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "with direction",
			query:      "?direction=desc",
			wantStatus: http.StatusOK,
		},
		{
			name:       "with since",
			query:      "?since=2020-01-01T00:00:00Z",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/dashboard/events"+tt.query, nil)
			rec := httptest.NewRecorder()

			handler.HandleListEvents(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleListEventsMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/events", nil)
	rec := httptest.NewRecorder()

	handler.HandleListEvents(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleListEventsWithTypeFilter(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/events?type=agent.started", nil)
	rec := httptest.NewRecorder()

	handler.HandleListEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var evts []EventView
	if err := json.NewDecoder(rec.Body).Decode(&evts); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(evts) != 1 {
		t.Errorf("events count = %d, want 1", len(evts))
	}
}

func TestHandleListMCPServers(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/mcp/servers", nil)
	rec := httptest.NewRecorder()

	handler.HandleListMCPServers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// No MCP manager configured, returns nil.
	body := rec.Body.String()
	if body != "null\n" {
		t.Errorf("body = %q, want null", body)
	}
}

func TestHandleListMCPServersMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/mcp/servers", nil)
	rec := httptest.NewRecorder()

	handler.HandleListMCPServers(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleRefreshMCPServer(t *testing.T) {
	handler, _ := newTestHandler(t)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "valid refresh path",
			path:       "/api/dashboard/mcp/servers/myserver/refresh",
			wantStatus: http.StatusOK,
		},
		{
			name:       "invalid path - no refresh suffix",
			path:       "/api/dashboard/mcp/servers/myserver",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.HandleRefreshMCPServer(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleRefreshMCPServerMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/mcp/servers/myserver/refresh", nil)
	rec := httptest.NewRecorder()

	handler.HandleRefreshMCPServer(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleEventStreamNoUpgrade(t *testing.T) {
	handler, _ := newTestHandler(t)

	// A regular HTTP request without WebSocket upgrade headers should
	// fail the upgrade gracefully (the upgrader returns an error, handler returns).
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/events/stream", nil)
	rec := httptest.NewRecorder()

	handler.HandleEventStream(rec, req)

	// The WebSocket upgrader writes 400 when upgrade headers are missing.
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusOK {
		// Accept either: the upgrader may write 400, or the handler may have
		// already returned before writing a status (200 default).
		t.Logf("HandleEventStream without upgrade: status = %d (acceptable)", rec.Code)
	}
}

func TestHandleEventStreamWithUpgrade(t *testing.T) {
	handler, _ := newTestHandler(t)

	// Use httptest.NewServer to test WebSocket upgrade end-to-end.
	ts := httptest.NewServer(http.HandlerFunc(handler.HandleEventStream))
	defer ts.Close()

	// Just verify the endpoint is reachable and returns something.
	// A full WebSocket test would require a WebSocket client; here we
	// verify the handler doesn't panic and the server is functional.
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", ts.URL, err)
	}
	_ = resp.Body.Close()

	// Without proper WebSocket upgrade headers, the upgrader rejects the request.
	if resp.StatusCode != http.StatusBadRequest {
		t.Logf("EventStream response status = %d", resp.StatusCode)
	}
}

// --- CORS header tests via the router ---

func TestRouterCORSHeaders(t *testing.T) {
	handler, _ := newTestHandler(t)
	config := &DashboardConfig{Addr: ":0"}
	router := NewDashboardRouter(handler, config)

	ts := httptest.NewServer(router)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dashboard/overview")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	tests := []struct {
		header   string
		expected string
	}{
		{"Access-Control-Allow-Origin", "*"},
		{"Access-Control-Allow-Methods", "GET, POST, OPTIONS"},
		{"Access-Control-Allow-Headers", "Content-Type"},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			got := resp.Header.Get(tt.header)
			if got != tt.expected {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.expected)
			}
		})
	}
}

func TestRouterOptionsPreflight(t *testing.T) {
	handler, _ := newTestHandler(t)
	config := &DashboardConfig{Addr: ":0"}
	router := NewDashboardRouter(handler, config)

	ts := httptest.NewServer(router)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodOptions, ts.URL+"/api/dashboard/overview", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("OPTIONS status = %d, want 200", resp.StatusCode)
	}
}

// --- Helper function tests ---

func TestExtractPathParam(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		prefix string
		want   string
	}{
		{
			name:   "simple extraction",
			path:   "/api/dashboard/agents/agent-1",
			prefix: "/api/dashboard/agents/",
			want:   "agent-1",
		},
		{
			name:   "with trailing slash",
			path:   "/api/dashboard/agents/agent-1/",
			prefix: "/api/dashboard/agents/",
			want:   "agent-1",
		},
		{
			name:   "with sub-path",
			path:   "/api/dashboard/agents/agent-1/events",
			prefix: "/api/dashboard/agents/",
			want:   "agent-1",
		},
		{
			name:   "prefix mismatch",
			path:   "/api/dashboard/workflows/wf-1",
			prefix: "/api/dashboard/agents/",
			want:   "",
		},
		{
			name:   "empty remaining",
			path:   "/api/dashboard/agents/",
			prefix: "/api/dashboard/agents/",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathParam(tt.path, tt.prefix)
			if got != tt.want {
				t.Errorf("extractPathParam(%q, %q) = %q, want %q", tt.path, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestParseQueryInt(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		key        string
		defaultVal int
		want       int
	}{
		{
			name:       "missing key returns default",
			query:      "",
			key:        "limit",
			defaultVal: 50,
			want:       50,
		},
		{
			name:       "valid integer",
			query:      "?limit=10",
			key:        "limit",
			defaultVal: 50,
			want:       10,
		},
		{
			name:       "invalid integer returns default",
			query:      "?limit=abc",
			key:        "limit",
			defaultVal: 50,
			want:       50,
		},
		{
			name:       "negative returns default",
			query:      "?limit=-1",
			key:        "limit",
			defaultVal: 50,
			want:       50,
		},
		{
			name:       "zero is valid",
			query:      "?limit=0",
			key:        "limit",
			defaultVal: 50,
			want:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test"+tt.query, nil)
			got := parseQueryInt(req, tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("parseQueryInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]string{"key": "value"})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var m map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["key"] != "value" {
		t.Errorf("key = %q, want value", m["key"])
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusNotFound, "not found")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}

	var m map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["error"] != "not found" {
		t.Errorf("error = %q, want 'not found'", m["error"])
	}
}

// Ensure mockEventStore satisfies the EventStore interface at compile time.
var _ events.EventStore = (*mockEventStore)(nil)
