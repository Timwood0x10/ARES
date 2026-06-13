package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// DashboardHandler holds HTTP handlers for the dashboard API.
type DashboardHandler struct {
	service *DashboardService
	hub     *WSHub
	config  *DashboardConfig
}

// NewDashboardHandler creates a new handler.
func NewDashboardHandler(service *DashboardService, hub *WSHub, config *DashboardConfig) *DashboardHandler {
	return &DashboardHandler{
		service: service,
		hub:     hub,
		config:  config,
	}
}

// HandleOverview returns system overview.
func (h *DashboardHandler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	overview, err := h.service.GetOverview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, overview)
}

// HandleListAgents returns all agents.
func (h *DashboardHandler) HandleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	agents := h.service.ListAgents()
	writeJSON(w, http.StatusOK, agents)
}

// HandleGetAgent returns a specific agent.
func (h *DashboardHandler) HandleGetAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := extractPathParam(r.URL.Path, "/api/dashboard/agents/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "agent id is required")
		return
	}

	agent, err := h.service.GetAgent(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// HandleAgentEvents returns events for a specific agent.
func (h *DashboardHandler) HandleAgentEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Path: /api/dashboard/agents/{id}/events
	path := strings.TrimPrefix(r.URL.Path, "/api/dashboard/agents/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[1] != "events" {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	agentID := parts[0]

	limit := parseQueryInt(r, "limit", 50)

	events, err := h.service.GetAgentEvents(r.Context(), agentID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, events)
}

// HandleListWorkflows returns workflow executions.
func (h *DashboardHandler) HandleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// TODO: implement when workflow package exposes execution listing.
	writeJSON(w, http.StatusOK, []WorkflowExecutionView{})
}

// HandleGetWorkflow returns a specific workflow execution.
func (h *DashboardHandler) HandleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// TODO: implement when workflow package exposes execution queries.
	writeError(w, http.StatusNotFound, "workflow not found")
}

// HandleGetWorkflowDAG returns the DAG for a workflow.
func (h *DashboardHandler) HandleGetWorkflowDAG(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// TODO: implement when workflow package exposes DAG queries.
	writeJSON(w, http.StatusOK, DAGView{})
}

// HandleListSessions returns memory sessions.
func (h *DashboardHandler) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sessions := h.service.GetSessions(r.Context())
	writeJSON(w, http.StatusOK, sessions)
}

// HandleGetSession returns a specific session with messages.
func (h *DashboardHandler) HandleGetSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := extractPathParam(r.URL.Path, "/api/dashboard/memory/sessions/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	session, err := h.service.GetSessionMessages(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, session)
}

// HandleListDistilled returns distilled memories.
func (h *DashboardHandler) HandleListDistilled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// TODO: implement when memory package exposes distilled listing.
	writeJSON(w, http.StatusOK, []DistilledMemoryView{})
}

// HandleSearchMemory searches distilled memories.
func (h *DashboardHandler) HandleSearchMemory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req MemorySearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	results, err := h.service.SearchMemory(r.Context(), req.Query, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, results)
}

// HandleListEvents returns events with optional filtering.
func (h *DashboardHandler) HandleListEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	params := EventQueryParams{
		StreamID:  r.URL.Query().Get("stream_id"),
		Direction: r.URL.Query().Get("direction"),
		Limit:     parseQueryInt(r, "limit", 50),
	}

	if typesStr := r.URL.Query().Get("type"); typesStr != "" {
		params.Types = strings.Split(typesStr, ",")
	}

	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			params.Since = t
		}
	}

	events, err := h.service.GetEvents(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, events)
}

// HandleEventStream upgrades the connection to WebSocket for real-time events.
func (h *DashboardHandler) HandleEventStream(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for development.
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := NewWSClient(h.hub, conn)
	h.hub.Register(client)

	pingInterval := 30 * time.Second
	if h.config != nil && h.config.WSPingInterval > 0 {
		pingInterval = h.config.WSPingInterval
	}

	go client.WritePump(pingInterval)
	go client.ReadPump()
}

// HandleListMCPServers returns MCP server status.
func (h *DashboardHandler) HandleListMCPServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	servers := h.service.GetMCPServers()
	writeJSON(w, http.StatusOK, servers)
}

// HandleRefreshMCPServer refreshes tools for a specific MCP server.
func (h *DashboardHandler) HandleRefreshMCPServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Path: /api/dashboard/mcp/servers/{name}/refresh
	path := strings.TrimPrefix(r.URL.Path, "/api/dashboard/mcp/servers/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[1] != "refresh" {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	// TODO: implement MCP server refresh when MCPManager is wired.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Best effort - header already sent.
		_ = err
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// extractPathParam extracts a path parameter from the URL.
func extractPathParam(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remaining := strings.TrimPrefix(path, prefix)
	// Remove trailing slash and any sub-paths.
	remaining = strings.SplitN(remaining, "/", 2)[0]
	return remaining
}

// parseQueryInt parses an integer query parameter with a default value.
func parseQueryInt(r *http.Request, key string, defaultVal int) int {
	str := r.URL.Query().Get(key)
	if str == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(str)
	if err != nil || val < 0 {
		return defaultVal
	}
	return val
}
