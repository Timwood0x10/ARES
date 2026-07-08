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
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_observability"

	"github.com/gin-gonic/gin"
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
	intel    *Engine
	start    time.Time
	arena    ArenaProvider
	survival SurvivalProvider
	upgrader *websocket.Upgrader
	apiKey   string // optional API key protecting destructive endpoints
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
			CheckOrigin:     checkWSOrigin,
		},
	}
}

// SetAPIKey configures the API key required to invoke destructive endpoints
// (chaos/arena actions). If no key is configured, those endpoints deny all
// requests by default.
func (a *APIv2) SetAPIKey(key string) {
	a.apiKey = key
}

// checkWSOrigin implements a strict WebSocket origin check.
//
// Empty Origin (non-browser clients) is allowed. Otherwise the origin's
// host:port must exactly match the Host header of the request. This replaces
// the previous substring match (strings.Contains) which was bypassable
// (e.g. https://evil.com/?target=realhost).
func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u == nil {
		return false
	}
	originHost := u.Host
	if originHost == "" {
		return false
	}
	// url.Host always includes the port if the URL had one; r.Host also
	// includes the port when present, so a direct comparison is sufficient.
	return originHost == r.Host
}

// SetArena attaches an arena provider for chaos operations.
func (a *APIv2) SetArena(arena ArenaProvider) {
	a.arena = arena
}

// SetSurvival attaches a survival provider for resilience testing.
func (a *APIv2) SetSurvival(survival SurvivalProvider) {
	a.survival = survival
}

// SetIntelligence attaches the intelligence engine for health/anomaly endpoints.
func (a *APIv2) SetIntelligence(intel *Engine) {
	a.intel = intel
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
	// All arena endpoints (chaos actions + status) require API key auth.
	mux.HandleFunc("/arena/leader/kill", a.requireAPIKeyHTTP(a.handleArenaKillLeader))
	mux.HandleFunc("/arena/agent/", a.requireAPIKeyHTTP(a.handleArenaAgentFault)) // catch-all: kill/pause/resume/slow/partition/tool-timeout/memory-corrupt/mcp-disconnect/llm-failure
	mux.HandleFunc("/arena/node/", a.requireAPIKeyHTTP(a.handleArenaRemoveNode))
	mux.HandleFunc("/arena/edge/remove", a.requireAPIKeyHTTP(a.handleArenaRemoveEdge))
	mux.HandleFunc("/arena/stats", a.requireAPIKeyHTTP(a.handleArenaStats))
	mux.HandleFunc("/arena/history", a.requireAPIKeyHTTP(a.handleArenaHistory))
	mux.HandleFunc("/arena/score", a.requireAPIKeyHTTP(a.handleArenaScore))
	mux.HandleFunc("/arena/survival", a.requireAPIKeyHTTP(a.handleArenaSurvival))
	mux.HandleFunc("/arena/survival/status", a.requireAPIKeyHTTP(a.handleArenaSurvivalStatus))

	// ── Arena extended fault injection ──────
	mux.HandleFunc("/arena/orchestrator/kill", a.requireAPIKeyHTTP(a.handleArenaKillOrchestrator))
	mux.HandleFunc("/arena/survival/stop", a.requireAPIKeyHTTP(a.handleArenaSurvivalStop))
	mux.HandleFunc("/arena/metrics", a.requireAPIKeyHTTP(a.handleArenaMetrics))
	mux.HandleFunc("/arena/stream", a.requireAPIKeyHTTP(a.handleArenaStream))

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

	// ── Intelligence ────────────────────────────
	// GET    /health          → system health
	// GET    /health/agents   → per-agent health
	// GET    /anomalies       → active anomalies
	// GET    /insights        → active insights
	// POST   /anomalies/{id}/resolve
	// POST   /insights/{id}/acknowledge
	mux.HandleFunc("/health", a.handleHealth)
	mux.HandleFunc("/health/agents", a.handleHealthAgents)
	mux.HandleFunc("/anomalies", a.handleAnomalies)
	mux.HandleFunc("/anomalies/", a.handleAnomalyByID)
	mux.HandleFunc("/insights", a.handleInsights)
	mux.HandleFunc("/insights/", a.handleInsightByID)

	// ── Prometheus metrics ─────────────────
	ares_observability.RegisterMetricsRouter(mux)

	return withRecovery(withCORS(mux))
}

// MountGinRoutes registers dashboard routes onto a Gin router group.
// This is the preferred way to serve dashboard endpoints — use instead of
// the standalone Handler() for unified serving with the monitoring console.
func (a *APIv2) MountGinRoutes(rg *gin.RouterGroup) {
	// ── Agents ──
	rg.GET("/agents", a.wrapGin(a.handleAgents))
	rg.POST("/agents", a.wrapGin(a.handleAgents))
	rg.GET("/agents/:id", a.wrapGin(a.handleAgentByID))

	// ── MCP ──
	rg.GET("/mcp", a.wrapGin(a.handleMCP))
	rg.POST("/mcp", a.wrapGin(a.handleMCP))
	rg.GET("/mcp/:name", a.wrapGin(a.handleMCPByName))

	// ── WebSocket ──
	rg.GET("/ws", a.wrapGinWS)

	// ── Arena ── (all require API key auth)
	arenaAuth := a.requireAPIKeyGin()
	rg.POST("/arena/leader/kill", arenaAuth, a.wrapGin(a.handleArenaKillLeader))
	rg.POST("/arena/agent/:id/:action", arenaAuth, a.wrapGin(a.handleArenaAgentFault))
	rg.POST("/arena/node/:id", arenaAuth, a.wrapGin(a.handleArenaRemoveNode))
	rg.POST("/arena/edge/remove", arenaAuth, a.wrapGin(a.handleArenaRemoveEdge))
	rg.GET("/arena/stats", arenaAuth, a.wrapGin(a.handleArenaStats))
	rg.GET("/arena/history", arenaAuth, a.wrapGin(a.handleArenaHistory))
	rg.GET("/arena/score", arenaAuth, a.wrapGin(a.handleArenaScore))
	rg.POST("/arena/survival", arenaAuth, a.wrapGin(a.handleArenaSurvival))
	rg.GET("/arena/survival/status", arenaAuth, a.wrapGin(a.handleArenaSurvivalStatus))
	rg.POST("/arena/orchestrator/kill", arenaAuth, a.wrapGin(a.handleArenaKillOrchestrator))
	rg.POST("/arena/survival/stop", arenaAuth, a.wrapGin(a.handleArenaSurvivalStop))
	rg.GET("/arena/metrics", arenaAuth, a.wrapGin(a.handleArenaMetrics))
	rg.GET("/arena/stream", arenaAuth, a.wrapGin(a.handleArenaStream))

	// ── Flight Recorder ──
	rg.GET("/flight/timeline", a.wrapGin(a.handleFlightTimeline))
	rg.GET("/flight/summary", a.wrapGin(a.handleFlightSummary))
	rg.GET("/flight/graph", a.wrapGin(a.handleFlightGraph))
	rg.GET("/flight/decisions", a.wrapGin(a.handleFlightDecisions))
	rg.GET("/flight/diagnostics", a.wrapGin(a.handleFlightDiagnostics))
	rg.GET("/flight/genealogy", a.wrapGin(a.handleFlightGenealogy))
}

// wrapGin converts a standard http.HandlerFunc to a gin.HandlerFunc.
func (a *APIv2) wrapGin(fn http.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		fn(c.Writer, c.Request)
	}
}

// wrapGinWS handles WebSocket upgrade via Gin.
func (a *APIv2) wrapGinWS(c *gin.Context) {
	a.handleWS(c.Writer, c.Request)
}

// requireAPIKeyHTTP wraps an http.HandlerFunc with API key auth. When no key
// is configured on the server, every request is denied (deny by default).
func (a *APIv2) requireAPIKeyHTTP(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.checkAPIKey(r) {
			writeJSON(w, http.StatusUnauthorized, errResp("invalid or missing API key"))
			return
		}
		fn(w, r)
	}
}

// requireAPIKeyGin returns a Gin middleware that enforces the same API key
// auth as requireAPIKeyHTTP.
func (a *APIv2) requireAPIKeyGin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.checkAPIKey(c.Request) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing API key"})
			return
		}
		c.Next()
	}
}

// checkAPIKey reports whether r carries the configured API key. With no key
// configured, all requests are refused (deny by default).
func (a *APIv2) checkAPIKey(r *http.Request) bool {
	if a.apiKey == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return strings.TrimPrefix(auth, prefix) == a.apiKey
}
