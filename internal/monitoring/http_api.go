package monitoring

import (
	"net/http"

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

// registerRoutes wires all API endpoints.
func (s *HTTPServer) registerRoutes() {
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

// handleGetAgent returns details for a single agent.
func (s *HTTPServer) handleGetAgent(c *gin.Context) {
	id := c.Param("id")
	detail, err := s.plugin.Detail(c.Request.Context(), "agent", id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, detail)
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
			"action":  action,
			"error":   err.Error(),
			"status":  "error",
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

// handleSubscribe is a placeholder for SSE streaming.
func (s *HTTPServer) handleSubscribe(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "SSE not yet implemented"})
}
