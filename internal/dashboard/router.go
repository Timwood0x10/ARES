package dashboard

import (
	"log/slog"
	"net/http"
	"strings"
)

// DashboardRouter mounts all dashboard endpoints and serves static files.
type DashboardRouter struct {
	mux     *http.ServeMux
	handler *DashboardHandler
	config  *DashboardConfig
}

// NewDashboardRouter creates a new router with all endpoints registered.
func NewDashboardRouter(handler *DashboardHandler, config *DashboardConfig) *DashboardRouter {
	r := &DashboardRouter{
		mux:     http.NewServeMux(),
		handler: handler,
		config:  config,
	}

	r.registerRoutes()
	return r
}

// ServeHTTP implements http.Handler.
func (r *DashboardRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Panic recovery — one bad request must not crash the server.
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("dashboard: panic recovered", "path", req.URL.Path, "recover", rec)
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
	}()

	// CORS headers for development.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if req.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	r.mux.ServeHTTP(w, req)
}

// registerRoutes registers all dashboard API routes.
func (r *DashboardRouter) registerRoutes() {
	h := r.handler

	// System overview.
	r.mux.HandleFunc("/api/dashboard/overview", h.HandleOverview)

	// Agents.
	r.mux.HandleFunc("/api/dashboard/agents", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if path == "/api/dashboard/agents" {
			h.HandleListAgents(w, req)
			return
		}

		// Check for /api/dashboard/agents/{id}/events
		trimmed := strings.TrimPrefix(path, "/api/dashboard/agents/")
		if strings.Contains(trimmed, "/events") {
			h.HandleAgentEvents(w, req)
			return
		}

		// /api/dashboard/agents/{id}
		h.HandleGetAgent(w, req)
	})

	// Workflows.
	r.mux.HandleFunc("/api/dashboard/workflows", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if path == "/api/dashboard/workflows" {
			h.HandleListWorkflows(w, req)
			return
		}

		trimmed := strings.TrimPrefix(path, "/api/dashboard/workflows/")
		if strings.Contains(trimmed, "/dag") {
			h.HandleGetWorkflowDAG(w, req)
			return
		}

		h.HandleGetWorkflow(w, req)
	})

	// Memory.
	r.mux.HandleFunc("/api/dashboard/memory/sessions", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if path == "/api/dashboard/memory/sessions" {
			h.HandleListSessions(w, req)
			return
		}

		h.HandleGetSession(w, req)
	})

	r.mux.HandleFunc("/api/dashboard/memory/distilled", h.HandleListDistilled)
	r.mux.HandleFunc("/api/dashboard/memory/search", h.HandleSearchMemory)

	// Events.
	r.mux.HandleFunc("/api/dashboard/events", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/dashboard/events/stream" {
			h.HandleEventStream(w, req)
			return
		}
		h.HandleListEvents(w, req)
	})

	// MCP.
	r.mux.HandleFunc("/api/dashboard/mcp/servers", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if path == "/api/dashboard/mcp/servers" {
			h.HandleListMCPServers(w, req)
			return
		}

		h.HandleRefreshMCPServer(w, req)
	})

	// Orchestrator (agent management).
	r.mux.HandleFunc("/api/dashboard/orchestrator/templates", h.HandleListTemplates)
	r.mux.HandleFunc("/api/dashboard/orchestrator/agents/", func(w http.ResponseWriter, req *http.Request) {
		// /api/dashboard/orchestrator/agents/{id}
		h.HandleGetAgentResult(w, req)
	})
	r.mux.HandleFunc("/api/dashboard/orchestrator/agents", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/dashboard/orchestrator/agents" {
			// Delegate to the trailing-slash handler for /agents/{id}.
			http.NotFound(w, req)
			return
		}
		if req.Method == http.MethodPost {
			h.HandleCreateAgent(w, req)
		} else {
			h.HandleListRunningAgents(w, req)
		}
	})

	// Static files (SPA).
	r.mux.HandleFunc("/", r.handleStatic)
}

// handleStatic serves the embedded SPA or static files from disk.
func (r *DashboardRouter) handleStatic(w http.ResponseWriter, req *http.Request) {
	// If a static directory is configured, serve from disk.
	if r.config != nil && r.config.StaticDir != "" {
		http.StripPrefix("/", http.FileServer(http.Dir(r.config.StaticDir))).ServeHTTP(w, req)
		return
	}

	// Serve embedded static files.
	http.FileServer(http.FS(staticFS)).ServeHTTP(w, req)
}
