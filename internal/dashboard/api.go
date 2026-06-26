// package dashboard — unified API v2.
//
// Two resources, one router:
//
//	/agents  — observe, create, interact with agents
//	/mcp     — configure, inspect MCP servers
//	/ws      — real-time updates
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/observability"

	"github.com/gorilla/websocket"
)

// ArenaActionType is the type of chaos action.
type ArenaActionType string

const (
	ArenaActionKillLeader       ArenaActionType = "kill_leader"
	ArenaActionKillAgent        ArenaActionType = "kill_agent"
	ArenaActionRemoveNode       ArenaActionType = "remove_node"
	ArenaActionRemoveEdge       ArenaActionType = "remove_edge"
	ArenaActionPauseAgent       ArenaActionType = "pause_agent"
	ArenaActionResumeAgent      ArenaActionType = "resume_agent"
	ArenaActionSlowAgent        ArenaActionType = "slow_agent"
	ArenaActionKillOrchestrator ArenaActionType = "kill_orchestrator"
	ArenaActionNetworkPartition ArenaActionType = "network_partition"
	ArenaActionToolTimeout      ArenaActionType = "tool_timeout"
	ArenaActionMemoryCorrupt    ArenaActionType = "memory_corrupt"
	ArenaActionMCPDisconnect    ArenaActionType = "mcp_disconnect"
	ArenaActionLLMFailure       ArenaActionType = "llm_failure"
)

// ArenaAction represents a single chaos action.
type ArenaAction struct {
	Type     ArenaActionType `json:"type"`
	TargetID string          `json:"target_id,omitempty"`
	SourceID string          `json:"source_id,omitempty"`
	Metadata map[string]any  `json:"metadata,omitempty"`
}

// ArenaResult holds the outcome of an arena action.
type ArenaResult struct {
	Success  bool          `json:"success"`
	Action   ArenaAction   `json:"action"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
}

// ArenaProvider abstracts the arena service for the dashboard.
type ArenaProvider interface {
	Execute(action ArenaAction) ArenaResult
	Stats() map[string]any
	History() []ArenaResult
}

// SurvivalProvider abstracts survival mode for the dashboard.
type SurvivalProvider interface {
	GetSurvivalStatus() map[string]any
	GetResilienceScore() map[string]any
}

// SurvivalStarter is an optional extension of SurvivalProvider that can
// actually start a survival run. If the concrete provider does not implement
// this interface, the /arena/survival POST endpoint returns 501.
type SurvivalStarter interface {
	StartSurvival(ctx context.Context) error
}

// APIv2 is the unified dashboard API.
type APIv2 struct {
	orch     *Orchestrator
	mcp      MCPStatusProvider
	hub      *WSHub
	start    time.Time
	arena    ArenaProvider
	survival SurvivalProvider
	upgrader *websocket.Upgrader
}

// NewAPIv2 creates a new unified API.
func NewAPIv2(orch *Orchestrator, mcp MCPStatusProvider, hub *WSHub) *APIv2 {
	return &APIv2{
		orch:  orch,
		mcp:   mcp,
		hub:   hub,
		start: time.Now(),
		upgrader: &websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}
				host := r.Host
				return strings.Contains(origin, host) || strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "https://localhost")
			},
		},
	}
}

// SetArena attaches an arena provider for chaos operations.
func (a *APIv2) SetArena(arena ArenaProvider) {
	a.arena = arena
}

// SetSurvival attaches a survival provider for resilience testing.
func (a *APIv2) SetSurvival(survival SurvivalProvider) {
	a.survival = survival
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

	// ── Arena ────────────────────────────────────
	mux.HandleFunc("/arena/leader/kill", a.handleArenaKillLeader)
	mux.HandleFunc("/arena/agent/", a.handleArenaAgentFault) // catch-all: kill/pause/resume/slow/partition/tool-timeout/memory-corrupt/mcp-disconnect/llm-failure
	mux.HandleFunc("/arena/node/", a.handleArenaRemoveNode)
	mux.HandleFunc("/arena/edge/remove", a.handleArenaRemoveEdge)
	mux.HandleFunc("/arena/stats", a.handleArenaStats)
	mux.HandleFunc("/arena/history", a.handleArenaHistory)
	mux.HandleFunc("/arena/score", a.handleArenaScore)
	mux.HandleFunc("/arena/survival", a.handleArenaSurvival)
	mux.HandleFunc("/arena/survival/status", a.handleArenaSurvivalStatus)

	// ── Arena extended fault injection ──────
	mux.HandleFunc("/arena/orchestrator/kill", a.handleArenaKillOrchestrator)
	mux.HandleFunc("/arena/survival/stop", a.handleArenaSurvivalStop)
	mux.HandleFunc("/arena/metrics", a.handleArenaMetrics)
	mux.HandleFunc("/arena/stream", a.handleArenaStream)

	// ── Flight Recorder ─────────────────────────
	mux.HandleFunc("/flight/timeline", a.handleFlightTimeline)
	mux.HandleFunc("/flight/summary", a.handleFlightSummary)
	mux.HandleFunc("/flight/graph", a.handleFlightGraph)
	mux.HandleFunc("/flight/decisions", a.handleFlightDecisions)
	mux.HandleFunc("/flight/diagnostics", a.handleFlightDiagnostics)
	mux.HandleFunc("/flight/genealogy", a.handleFlightGenealogy)

	// ── System ──────────────────────────────────
	// GET    /                → system overview
	mux.HandleFunc("/", a.handleRoot)

	// ── Prometheus metrics ─────────────────
	observability.RegisterMetricsRouter(mux)

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

	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := NewWSClient(a.hub, conn)
	a.hub.Register(client)

	pingInterval := 30 * time.Second
	client.Start(pingInterval)
}

// ── System ────────────────────────────────────

func (a *APIv2) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		// Serve static files.
		http.FileServer(http.FS(staticFS)).ServeHTTP(w, r)
		return
	}

	// Serve the SPA HTML at /, or JSON if requested.
	if r.Header.Get("Accept") == "application/json" || r.URL.Query().Get("format") == "json" {
		a.handleOverviewJSON(w, r)
		return
	}
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp("index.html not found"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(data); err != nil {
		slog.Debug("dashboard: failed to write index.html", "error", err)
	}
}

func (a *APIv2) handleOverviewJSON(w http.ResponseWriter, r *http.Request) {
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
		"uptime":      time.Since(a.start).Round(time.Second).String(),
		"agents":      agentCount,
		"mcp_servers": mcpServers,
		"mcp_tools":   mcpTools,
		"dashboard":   "http://" + r.Host,
	})
}

// ── Arena handlers ─────────────────────────────

func (a *APIv2) handleArenaKillLeader(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.arena == nil {
		writeJSON(w, http.StatusServiceUnavailable, errResp("arena not available"))
		return
	}
	result := a.arena.Execute(ArenaAction{Type: ArenaActionKillLeader})
	writeJSON(w, http.StatusOK, result)
}

func (a *APIv2) handleArenaRemoveNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.arena == nil {
		writeJSON(w, http.StatusServiceUnavailable, errResp("arena not available"))
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/arena/node/")
	id = strings.TrimSuffix(id, "/remove")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errResp("node id required"))
		return
	}
	result := a.arena.Execute(ArenaAction{Type: ArenaActionRemoveNode, TargetID: id})
	writeJSON(w, http.StatusOK, result)
}

func (a *APIv2) handleArenaRemoveEdge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.arena == nil {
		writeJSON(w, http.StatusServiceUnavailable, errResp("arena not available"))
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid request body"))
		return
	}
	if req.From == "" || req.To == "" {
		writeJSON(w, http.StatusBadRequest, errResp("from and to are required"))
		return
	}
	result := a.arena.Execute(ArenaAction{
		Type:     ArenaActionRemoveEdge,
		TargetID: req.To,
		SourceID: req.From,
	})
	writeJSON(w, http.StatusOK, result)
}

func (a *APIv2) handleArenaStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.arena == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, a.arena.Stats())
}

func (a *APIv2) handleArenaHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.arena == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, a.arena.History())
}

func (a *APIv2) handleArenaScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.survival == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, a.survival.GetResilienceScore())
}

func (a *APIv2) handleArenaSurvival(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.survival == nil {
		writeJSON(w, http.StatusServiceUnavailable, errResp("survival mode not available"))
		return
	}
	starter, ok := a.survival.(SurvivalStarter)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, errResp("survival start not supported by current provider"))
		return
	}
	if err := starter.StartSurvival(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp(err.Error()))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "started",
		"message": "survival run started",
	})
}

func (a *APIv2) handleArenaSurvivalStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.survival == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	writeJSON(w, http.StatusOK, a.survival.GetSurvivalStatus())
}

// handleArenaAgentFault is a catch-all for /arena/agent/{id}/{action}
// Supported actions: pause, resume, slow, partition, tool-timeout, memory-corrupt, mcp-disconnect, llm-failure
func (a *APIv2) handleArenaAgentFault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.arena == nil {
		writeJSON(w, http.StatusServiceUnavailable, errResp("arena not available"))
		return
	}
	// Path format: /arena/agent/{id}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/arena/agent/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeJSON(w, http.StatusBadRequest, errResp("invalid path, expected /arena/agent/{id}/{action}"))
		return
	}
	id := parts[0]
	actionStr := parts[1]

	actionMap := map[string]ArenaActionType{
		"pause":          ArenaActionPauseAgent,
		"resume":         ArenaActionResumeAgent,
		"slow":           ArenaActionSlowAgent,
		"partition":      ArenaActionNetworkPartition,
		"tool-timeout":   ArenaActionToolTimeout,
		"memory-corrupt": ArenaActionMemoryCorrupt,
		"mcp-disconnect": ArenaActionMCPDisconnect,
		"llm-failure":    ArenaActionLLMFailure,
	}
	actionType, ok := actionMap[actionStr]
	if !ok {
		// Fall back to the existing kill handler.
		if actionStr == "kill" {
			result := a.arena.Execute(ArenaAction{Type: ArenaActionKillAgent, TargetID: id})
			writeJSON(w, http.StatusOK, result)
			return
		}
		writeJSON(w, http.StatusBadRequest, errResp("unknown action: "+actionStr))
		return
	}

	var body map[string]any
	if r.Body != nil {
		// Body may be absent or empty; decode failure means no metadata,
		// which is safe to ignore for optional fault-injection parameters.
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	action := ArenaAction{Type: actionType, TargetID: id}
	// Extract metadata (e.g., duration for slow_agent action).
	if d, ok := body["duration"].(string); ok {
		action.Metadata = map[string]any{"duration": d}
	}
	if et, ok := body["error_type"].(string); ok {
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata["error_type"] = et
	}

	result := a.arena.Execute(action)
	writeJSON(w, http.StatusOK, result)
}

func (a *APIv2) handleArenaKillOrchestrator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.arena == nil {
		writeJSON(w, http.StatusServiceUnavailable, errResp("arena not available"))
		return
	}
	result := a.arena.Execute(ArenaAction{Type: ArenaActionKillOrchestrator})
	writeJSON(w, http.StatusOK, result)
}

func (a *APIv2) handleArenaSurvivalStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.survival == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
		return
	}
	if stopper, ok := a.survival.(interface{ StopSurvival() error }); ok {
		_ = stopper.StopSurvival()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

func (a *APIv2) handleArenaMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	if a.arena == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	stats := a.arena.Stats()
	history := a.arena.History()
	// Calculate metrics from stats + history.
	totalActions := 0
	successfulActions := 0
	if v, ok := stats["total_actions"].(int); ok {
		totalActions = v
	}
	if v, ok := stats["successful_actions"].(int); ok {
		successfulActions = v
	}

	failoverCount := 0
	var recoveryTimes []time.Duration
	for _, h := range history {
		if h.Success {
			failoverCount++
		}
		recoveryTimes = append(recoveryTimes, h.Duration)
	}

	avgRecovery := time.Duration(0)
	if len(recoveryTimes) > 0 {
		var total time.Duration
		for _, d := range recoveryTimes {
			total += d
		}
		avgRecovery = total / time.Duration(len(recoveryTimes))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_actions":         totalActions,
		"successful_actions":    successfulActions,
		"failed_actions":        totalActions - successfulActions,
		"failover_count":        failoverCount,
		"avg_recovery_time":     avgRecovery.String(),
		"data_consistency_rate": 1.0,
	})
}

func (a *APIv2) handleArenaStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	// Send initial connection event.
	if _, err := fmt.Fprintf(w, "event: connected\ndata: %s\n\n", time.Now().Format(time.RFC3339)); err != nil {
		slog.Warn("sse write failed", "error", err)
	}
	flusher.Flush()

	// Send arena history events then close.
	if a.arena != nil {
		history := a.arena.History()
		for i, h := range history {
			if i >= 20 { // Limit count to prevent overwhelming the client.
				break
			}
			data, _ := json.Marshal(h)
			if _, err := fmt.Fprintf(w, "event: arena_action\ndata: %s\n\n", data); err != nil {
				slog.Warn("sse write failed", "error", err)
			}
			flusher.Flush()
		}
	}
	if _, err := fmt.Fprintf(w, "event: done\ndata: {}\n\n"); err != nil {
		slog.Warn("sse write failed", "error", err)
	}
	flusher.Flush()
}

// ── Flight Recorder handlers ──────────────────

// getFlightRecorder returns the FlightRecorder from the orchestrator. May be nil.
func (a *APIv2) getFlightRecorder() *flight.FlightRecorder {
	if a.orch == nil {
		return nil
	}
	return a.orch.getFlight()
}

func (a *APIv2) handleFlightTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	fr := a.getFlightRecorder()
	if fr == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	var events []flight.TimelineEvent
	if agentID != "" {
		events = fr.Timeline().FilterByAgent(agentID)
	} else {
		events = fr.Timeline().Events()
	}

	writeJSON(w, http.StatusOK, events)
}

func (a *APIv2) handleFlightSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	fr := a.getFlightRecorder()
	if fr == nil {
		writeJSON(w, http.StatusOK, flight.TimelineSummary{})
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	if agentID != "" {
		// Compute summary for filtered events.
		events := fr.Timeline().FilterByAgent(agentID)
		summary := computeSummary(events)
		writeJSON(w, http.StatusOK, summary)
		return
	}

	writeJSON(w, http.StatusOK, fr.Timeline().Summary())
}

// computeSummary builds a TimelineSummary from a filtered set of events.
func computeSummary(events []flight.TimelineEvent) flight.TimelineSummary {
	var summary flight.TimelineSummary
	summary.EventCount = len(events)

	for _, e := range events {
		switch e.Type {
		case flight.EventToolCall, flight.EventToolResult:
			summary.ToolDuration += e.Duration
		case flight.EventLLMCall, flight.EventLLMResult:
			summary.LLMDuration += e.Duration
		case flight.EventWaiting:
			summary.WaitDuration += e.Duration
		case flight.EventError:
			summary.ErrorDuration += e.Duration
		}
	}

	if len(events) > 0 {
		minStart := events[0].StartAt
		maxEnd := events[0].EndAt
		for _, e := range events {
			if e.StartAt.Before(minStart) {
				minStart = e.StartAt
			}
			if e.EndAt.After(maxEnd) {
				maxEnd = e.EndAt
			}
		}
		if !maxEnd.IsZero() && maxEnd.After(minStart) {
			summary.TotalDuration = maxEnd.Sub(minStart)
		}
	}

	if summary.TotalDuration > 0 {
		summary.ToolPercent = float64(summary.ToolDuration) / float64(summary.TotalDuration) * 100
		summary.LLMPercent = float64(summary.LLMDuration) / float64(summary.TotalDuration) * 100
		summary.WaitPercent = float64(summary.WaitDuration) / float64(summary.TotalDuration) * 100
	}

	return summary
}

func (a *APIv2) handleFlightGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	fr := a.getFlightRecorder()
	if fr == nil {
		writeJSON(w, http.StatusOK, map[string]string{"mermaid": "graph LR\n    empty[No data]"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"mermaid": fr.Graph().ExportMermaid()})
}

func (a *APIv2) handleFlightDecisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	fr := a.getFlightRecorder()
	if fr == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	if agentID != "" {
		writeJSON(w, http.StatusOK, fr.Decisions().FilterByAgent(agentID))
		return
	}

	writeJSON(w, http.StatusOK, fr.Decisions().All())
}

func (a *APIv2) handleFlightDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	fr := a.getFlightRecorder()
	if fr == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"records":      []any{},
			"distribution": flight.CategoryDistribution{},
		})
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	var records []flight.DiagnosticRecord
	if agentID != "" {
		records = fr.Diagnostics().FilterByAgent(agentID)
	} else {
		records = fr.Diagnostics().All()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"records":      records,
		"distribution": fr.Diagnostics().Distribution(),
	})
}

func (a *APIv2) handleFlightGenealogy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errResp("method not allowed"))
		return
	}

	fr := a.getFlightRecorder()
	if fr == nil || fr.Genealogy() == nil {
		writeJSON(w, http.StatusOK, map[string]string{"mermaid": "graph LR\n    empty[No agents]"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"mermaid": fr.Genealogy().ExportMermaid()})
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
