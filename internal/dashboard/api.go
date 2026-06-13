// package dashboard — unified API v2.
//
// Two resources, one router:
//
//	/agents  — observe, create, interact with agents
//	/mcp     — configure, inspect MCP servers
//	/ws      — real-time updates
package dashboard

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// APIv2 is the unified dashboard API.
type APIv2 struct {
	orch  *Orchestrator
	mcp   MCPStatusProvider
	hub   *WSHub
	start time.Time
}

// NewAPIv2 creates a new unified API.
func NewAPIv2(orch *Orchestrator, mcp MCPStatusProvider, hub *WSHub) *APIv2 {
	return &APIv2{
		orch:  orch,
		mcp:   mcp,
		hub:   hub,
		start: time.Now(),
	}
}

// Handler returns the http.Handler with all routes mounted.
func (a *APIv2) Handler() http.Handler {
	mux := http.NewServeMux()

	// ── Agent resource ──────────────────────────
	// GET    /agents          → list all agents (filterable)
	// POST   /agents          → create & launch agent
	// GET    /agents/{id}     → agent detail + full result
	// DELETE /agents/{id}     → (future) cancel agent
	mux.HandleFunc("/agents", a.handleAgents)
	mux.HandleFunc("/agents/", a.handleAgentByID)

	// ── MCP resource ────────────────────────────
	// GET    /mcp             → list servers with tools
	// POST   /mcp             → add a new server
	// DELETE /mcp/{name}      → remove a server (future)
	// GET    /mcp/{name}      → server detail
	mux.HandleFunc("/mcp", a.handleMCP)
	mux.HandleFunc("/mcp/", a.handleMCPByName)

	// ── WebSocket ───────────────────────────────
	// GET    /ws              → upgrade to WebSocket
	mux.HandleFunc("/ws", a.handleWS)

	// ── System ──────────────────────────────────
	// GET    /                → system overview
	mux.HandleFunc("/", a.handleRoot)

	return withRecovery(withCORS(mux))
}

// ── Agent handlers ────────────────────────────

func (a *APIv2) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/agents" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.listAgents(w, r)
	case http.MethodPost:
		a.createAgent(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
	}
}

func (a *APIv2) listAgents(w http.ResponseWriter, r *http.Request) {
	if a.orch == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	agents := a.orch.ListAgents()

	// Filter by status.
	if status := r.URL.Query().Get("status"); status != "" {
		filtered := make([]AgentResult, 0)
		for _, a := range agents {
			if a.Status == status {
				filtered = append(filtered, a)
			}
		}
		agents = filtered
	}

	writeJSON(w, http.StatusOK, agents)
}

func (a *APIv2) createAgent(w http.ResponseWriter, r *http.Request) {
	if a.orch == nil {
		writeJSON(w, http.StatusServiceUnavailable, errResp("orchestrator not available"))
		return
	}

	var req AgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid request body"))
		return
	}

	id, err := a.orch.CreateAgent(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errResp(err.Error()))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "status": "pending"})
}

func (a *APIv2) handleAgentByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/agents/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errResp("agent id required"))
		return
	}

	if a.orch == nil {
		writeJSON(w, http.StatusNotFound, errResp("agent not found"))
		return
	}

	result, ok := a.orch.GetAgent(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, errResp("agent not found"))
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── MCP handlers ──────────────────────────────

func (a *APIv2) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.listMCPServers(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
	}
}

func (a *APIv2) listMCPServers(w http.ResponseWriter, _ *http.Request) {
	if a.mcp == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, a.mcp.ListServers())
}

func (a *APIv2) handleMCPByName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/mcp/")
	if name == "" {
		a.listMCPServers(w, r)
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	if a.mcp == nil {
		writeJSON(w, http.StatusNotFound, errResp("no MCP servers"))
		return
	}

	for _, srv := range a.mcp.ListServers() {
		if srv.Name == name {
			writeJSON(w, http.StatusOK, srv)
			return
		}
	}

	writeJSON(w, http.StatusNotFound, errResp("server not found"))
}

// ── WebSocket ─────────────────────────────────

func (a *APIv2) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	upgrader := newUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := NewWSClient(a.hub, conn)
	a.hub.Register(client)

	pingInterval := 30 * time.Second
	go client.WritePump(pingInterval)
	go client.ReadPump()
}

// ── System ────────────────────────────────────

func (a *APIv2) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		// Serve static files.
		http.FileServer(http.FS(staticFS)).ServeHTTP(w, r)
		return
	}

	agentCount := 0
	if a.orch != nil {
		agentCount = len(a.orch.ListAgents())
	}

	mcpServers := 0
	mcpTools := 0
	if a.mcp != nil {
		for _, s := range a.mcp.ListServers() {
			mcpServers++
			mcpTools += s.ToolCount
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"uptime":       time.Since(a.start).Round(time.Second).String(),
		"agents":       agentCount,
		"mcp_servers":  mcpServers,
		"mcp_tools":    mcpTools,
		"dashboard":    "http://" + r.Host,
	})
}

// ── Middleware ─────────────────────────────────

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("api: panic recovered", "path", r.URL.Path, "recover", rec)
				writeJSON(w, http.StatusInternalServerError, errResp("internal server error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ── Helpers ───────────────────────────────────

func errResp(msg string) map[string]string {
	return map[string]string{"error": msg}
}

func newUpgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
}
