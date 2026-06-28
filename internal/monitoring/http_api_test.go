package monitoring

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHTTPServer(t *testing.T) *HTTPServer {
	t.Helper()
	gin.SetMode(gin.TestMode)
	p := NewConsole().(*MonitorPlugin)
	return NewHTTPServer(p)
}

func TestHTTPServer_Console(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/console", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var snap ConsoleSnapshot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snap))
	assert.False(t, snap.UpdateTime.IsZero())
}

func TestHTTPServer_DAG(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/console/dag", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_CostBar(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/console/cost-bar", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_Agents(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_GetAgent_NotFound(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents/missing", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHTTPServer_KillAgent(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agents/a1/kill", nil)
	srv.ServeHTTP(w, req)

	// Without interaction engine, should return 501.
	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestHTTPServer_ResumeAgent(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agents/a1/resume", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestHTTPServer_RetryAgent(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agents/a1/retry", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestHTTPServer_MCPTools_NoManager(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mcp/tools", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHTTPServer_MCPTools_WithManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := NewConsole(WithMCP(&mockMCPManager{
		tools: []MCPToolInfo{
			{Name: "tool1", Description: "A tool"},
		},
	})).(*MonitorPlugin)
	srv := NewHTTPServer(p)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mcp/tools", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var tools []MCPToolInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tools))
	assert.Len(t, tools, 1)
	assert.Equal(t, "tool1", tools[0].Name)
}

func TestHTTPServer_CallMCPTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := NewConsole(WithMCP(&mockMCPManager{
		result: &MCPToolResult{ToolName: "tool1", Output: map[string]any{"ok": true}},
	})).(*MonitorPlugin)
	srv := NewHTTPServer(p)

	body, _ := json.Marshal(map[string]any{"path": "/tmp/test"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/tools/tool1/call", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var result MCPToolResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, "tool1", result.ToolName)
}

func TestHTTPServer_CallMCPTool_EmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := NewConsole(WithMCP(&mockMCPManager{
		result: &MCPToolResult{ToolName: "tool1"},
	})).(*MonitorPlugin)
	srv := NewHTTPServer(p)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/tools/tool1/call", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_Tab_NotFound(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tabs/missing", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHTTPServer_Cost(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/cost", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_Trace(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/trace/trace-1", nil)
	srv.ServeHTTP(w, req)

	// Traces not yet implemented, returns 503.
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHTTPServer_Subscribe(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/subscribe", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestHTTPServer_ServeHTTP(t *testing.T) {
	srv := newTestHTTPServer(t)

	// Verify ServeHTTP implements http.Handler.
	var _ http.Handler = srv
}
