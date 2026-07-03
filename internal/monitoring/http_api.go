package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// HTTPServer exposes the monitoring plugin over a Gin-based HTTP server.
type HTTPServer struct {
	engine *gin.Engine
	plugin *MonitorPlugin
}

// HTTPServerOption configures the HTTPServer.
type HTTPServerOption func(*HTTPServer)

// WithGinMode sets the Gin mode (debug, release, test).
func WithGinMode(mode string) HTTPServerOption {
	return func(_ *HTTPServer) {
		gin.SetMode(mode)
	}
}

// NewHTTPServer creates an HTTPServer wrapping the given MonitorPlugin.
func NewHTTPServer(plugin *MonitorPlugin, opts ...HTTPServerOption) *HTTPServer {
	for _, opt := range opts {
		opt(nil)
	}

	engine := gin.New()
	engine.Use(gin.Recovery(), gin.Logger())

	s := &HTTPServer{
		engine: engine,
		plugin: plugin,
	}
	s.registerRoutes()
	return s
}

// Run starts the HTTP server on the given address.
func (s *HTTPServer) Run(addr string) error {
	return s.engine.Run(addr)
}

// ServeHTTP implements http.Handler for testing with httptest.
func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.engine.ServeHTTP(w, r)
}

// registerRoutes wires all API endpoints and serves the console SPA.
func (s *HTTPServer) registerRoutes() {
	// Serve embedded static files.
	staticFS, err := fs.Sub(consoleFS, "static")
	if err == nil {
		s.engine.StaticFS("/console/static", http.FS(staticFS))
	}

	// Serve index.html at /console/.
	s.engine.GET("/console/", func(c *gin.Context) {
		data, err := consoleFS.ReadFile("static/index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "failed to load index.html")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})

	api := s.engine.Group("/api")

	// Console
	api.GET("/console", s.handleConsole)
	api.GET("/console/dag", s.handleDAG)
	api.GET("/console/cost-bar", s.handleCostBar)

	// Agents
	api.GET("/agents", s.handleListAgents)
	api.GET("/agents/:id", s.handleGetAgent)
	api.POST("/agents/:id/kill", s.handleKillAgent)
	api.POST("/agents/:id/resume", s.handleResumeAgent)
	api.POST("/agents/:id/retry", s.handleRetryAgent)

	// MCP
	api.GET("/mcp/tools", s.handleListMCPTools)
	api.POST("/mcp/tools/:name/call", s.handleCallMCPTool)

	// Tabs
	api.GET("/tabs/:name", s.handleTab)

	// Cost
	api.GET("/cost", s.handleCost)

	// Trace
	api.GET("/trace/:id", s.handleTrace)

	// SSE placeholder
	api.GET("/subscribe", s.handleSubscribe)

	// Intelligence — health, anomalies, insights (via plugin intel provider)
	api.GET("/health", s.handleHealth)
	api.GET("/health/agents", s.handleHealthAgents)
	api.GET("/anomalies", s.handleAnomalies)
	api.GET("/insights", s.handleInsights)
}

// handleConsole returns the full console snapshot.
func (s *HTTPServer) handleConsole(c *gin.Context) {
	snap, err := s.plugin.Snapshot(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, snap)
}

// handleDAG returns the DAG snapshot.
func (s *HTTPServer) handleDAG(c *gin.Context) {
	d, err := s.plugin.DAG(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

// handleCostBar returns the cost bar snapshot.
func (s *HTTPServer) handleCostBar(c *gin.Context) {
	cb, err := s.plugin.CostBreakdown(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cb)
}

// handleListAgents returns all agents from the console snapshot.
func (s *HTTPServer) handleListAgents(c *gin.Context) {
	snap, err := s.plugin.Snapshot(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, snap.Agents)
}

// handleGetAgent returns a rich detail view for a single agent.
func (s *HTTPServer) handleGetAgent(c *gin.Context) {
	id := c.Param("id")

	tracker := s.plugin.mainPage.Tracker()
	if tracker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "tracker not configured"})
		return
	}
	agent, ok := tracker.GetAgent(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found: " + id})
		return
	}

	snap := s.plugin.mainPage.Snapshot()

	// Tasks belonging to this agent.
	var agentTasks []TaskView
	for _, t := range snap.Tasks {
		if t.AgentID == id {
			agentTasks = append(agentTasks, t)
		}
	}

	// Relationships.
	allAgents := snap.Agents
	var parent *UnifiedAgent
	var children []UnifiedAgent
	var peers []UnifiedAgent
	for _, a := range allAgents {
		if a.ID == id {
			continue
		}
		if a.ID == agent.ParentID {
			cp := a
			parent = &cp
		}
		if a.ParentID == id {
			children = append(children, a)
		}
		if agent.ParentID != "" && a.ParentID == agent.ParentID {
			peers = append(peers, a)
		}
	}

	// Event count from events tab (count events where module_name matches).
	var eventCount int
	var eventTypes map[string]int
	if tab, hasTab := s.plugin.mainPage.GetTab("events"); hasTab {
		tabSnap := tab.Snapshot()
		// The snapshot is EventTabSnapshot{Events, Total}.
		// We can't type-assert across packages, so marshal/unmarshal.
		raw, err := json.Marshal(tabSnap)
		if err == nil {
			var parsed struct {
				Events []struct {
					ModuleName string `json:"module_name"`
					Type       string `json:"type"`
				} `json:"events"`
			}
			if json.Unmarshal(raw, &parsed) == nil {
				eventTypes = make(map[string]int)
				for _, e := range parsed.Events {
					if e.ModuleName == id {
						eventCount++
						eventTypes[e.Type]++
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"agent": agent,
		"tasks": agentTasks,
		"relationships": gin.H{
			"parent":   parent,
			"children": children,
			"peers":    peers,
		},
		"events": gin.H{
			"total":   eventCount,
			"by_type": eventTypes,
		},
	})
}

// handleKillAgent kills an agent.
func (s *HTTPServer) handleKillAgent(c *gin.Context) {
	s.executeNodeAction(c, "kill")
}

// handleResumeAgent resumes an agent.
func (s *HTTPServer) handleResumeAgent(c *gin.Context) {
	s.executeNodeAction(c, "resume")
}

// handleRetryAgent retries an agent.
func (s *HTTPServer) handleRetryAgent(c *gin.Context) {
	s.executeNodeAction(c, "retry")
}

// executeNodeAction dispatches an action on a node.
func (s *HTTPServer) executeNodeAction(c *gin.Context, action string) {
	result, err := s.plugin.ExecuteAction(c.Request.Context(), action)
	if err != nil {
		c.JSON(http.StatusNotImplemented, gin.H{
			"action": action,
			"error":  err.Error(),
			"status": "error",
		})
		return
	}
	c.JSON(http.StatusOK, result)
}

// handleListMCPTools returns available MCP tools.
func (s *HTTPServer) handleListMCPTools(c *gin.Context) {
	tools, err := s.plugin.ListMCPTools(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tools)
}

// handleCallMCPTool invokes an MCP tool.
func (s *HTTPServer) handleCallMCPTool(c *gin.Context) {
	name := c.Param("name")

	var args map[string]any
	if err := c.ShouldBindJSON(&args); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	result, err := s.plugin.CallMCPTool(c.Request.Context(), name, args)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// handleTab returns a tab snapshot by name.
func (s *HTTPServer) handleTab(c *gin.Context) {
	name := c.Param("name")
	tab, ok := s.plugin.mainPage.GetTab(name)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "tab not found", "name": name})
		return
	}
	c.JSON(http.StatusOK, tab.Snapshot())
}

// handleCost returns the full cost breakdown.
func (s *HTTPServer) handleCost(c *gin.Context) {
	cb, err := s.plugin.CostBreakdown(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cb)
}

// handleTrace returns trace spans for a given trace ID.
func (s *HTTPServer) handleTrace(c *gin.Context) {
	id := c.Param("id")
	spans, err := s.plugin.Traces(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"trace_id": id, "spans": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"trace_id": id, "spans": spans})
}

// handleSubscribe streams console snapshots as Server-Sent Events.
// The client receives a "snapshot" event every 2 seconds until disconnect.
func (s *HTTPServer) handleSubscribe(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Send initial snapshot immediately.
	s.writeSSESnapshot(c.Writer, flusher)

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			if !s.writeSSESnapshot(c.Writer, flusher) {
				return
			}
		}
	}
}

// writeSSESnapshot writes a single SSE event with the current console snapshot.
// Returns false if the write failed (client disconnected).
func (s *HTTPServer) writeSSESnapshot(w gin.ResponseWriter, flusher http.Flusher) bool {
	snap, err := s.plugin.Snapshot(context.Background())
	if err != nil {
		if _, err := fmt.Fprintf(w, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error()); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	data, err := json.Marshal(snap)
	if err != nil {
		if _, err := fmt.Fprintf(w, "event: error\ndata: {\"error\":\"marshal failed\"}\n\n"); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// ── Intelligence handlers ───────────────────────

func (s *HTTPServer) handleHealth(c *gin.Context) {
	if s.plugin.intel == nil {
		c.JSON(http.StatusOK, gin.H{"level": "unknown"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"level":  s.plugin.intel.SystemLevel(),
		"agents": s.plugin.intel.AnomalyCount(),
	})
}

func (s *HTTPServer) handleHealthAgents(c *gin.Context) {
	if s.plugin.intel == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": 0})
}

func (s *HTTPServer) handleAnomalies(c *gin.Context) {
	if s.plugin.intel == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": s.plugin.intel.AnomalyCount()})
}

func (s *HTTPServer) handleInsights(c *gin.Context) {
	if s.plugin.intel == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": s.plugin.intel.InsightCount()})
}
