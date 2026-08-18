package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAPIKey is the API key used by newTestHTTPServer; tests that hit
// protected endpoints must send it as `Authorization: Bearer <testAPIKey>`.
const testAPIKey = "test-api-key"

func newTestHTTPServer(t *testing.T) *HTTPServer {
	t.Helper()
	gin.SetMode(gin.TestMode)
	p := NewConsole().(*MonitorPlugin)
	return NewHTTPServer(p, WithAPIKey(testAPIKey))
}

// withTestAuth sets the Authorization header expected by newTestHTTPServer.
func withTestAuth(req *http.Request) *http.Request {
	req.Header.Set("Authorization", bearerPrefix+testAPIKey)
	return req
}

func TestHTTPServer_Console(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/console", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var snap ConsoleSnapshot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snap))
	assert.False(t, snap.UpdateTime.IsZero())
}

func TestHTTPServer_DAG(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/console/dag", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_CostBar(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/console/cost-bar", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_Agents(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/agents", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_GetAgent_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := NewConsole(WithAgentTracker(newMockAgentTrackerImpl())).(*MonitorPlugin)
	srv := NewHTTPServer(p)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/agents/missing", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHTTPServer_KillAgent(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := withTestAuth(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/agents/a1/kill", nil))
	srv.ServeHTTP(w, req)

	// Without interaction engine, should return 501.
	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestHTTPServer_ResumeAgent(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := withTestAuth(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/agents/a1/resume", nil))
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestHTTPServer_RetryAgent(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := withTestAuth(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/agents/a1/retry", nil))
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestHTTPServer_MCPTools_NoManager(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/mcp/tools", nil)
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/mcp/tools", nil)
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
	srv := NewHTTPServer(p, WithAPIKey(testAPIKey))

	body, _ := json.Marshal(map[string]any{"path": "/tmp/test"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mcp/tools/tool1/call", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withTestAuth(req)
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
	srv := NewHTTPServer(p, WithAPIKey(testAPIKey))

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mcp/tools/tool1/call", nil)
	withTestAuth(req)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_Tab_NotFound(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/tabs/missing", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHTTPServer_Cost(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/cost", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_Trace(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/trace/trace-1", nil)
	srv.ServeHTTP(w, req)

	// Traces not yet implemented, returns 503.
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHTTPServer_Subscribe(t *testing.T) {
	srv := newTestHTTPServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/subscribe", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		srv.ServeHTTP(w, req)
		close(done)
	}()

	// Wait for at least one SSE event to be written.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "event: snapshot")
}

func TestHTTPServer_ServeHTTP(t *testing.T) {
	srv := newTestHTTPServer(t)

	// Verify ServeHTTP implements http.Handler.
	var _ http.Handler = srv
}

func TestHTTPServer_ConsolePage(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/console/", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ARES Console")
	assert.Contains(t, w.Body.String(), "app.js")
}

func TestHTTPServer_ConsoleStatic(t *testing.T) {
	srv := newTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/console/static/style.css", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "--bg-deep:")
}

// ── JWT auth (dual credential: API key OR JWT) ──────────────────────────────

// newJWTTestHTTPServer builds a server that accepts both the legacy API key
// and a JWT with write permission (admin/operator).
func newJWTTestHTTPServer(t *testing.T) *HTTPServer {
	t.Helper()
	gin.SetMode(gin.TestMode)
	p := NewConsole().(*MonitorPlugin)
	return NewHTTPServer(p, WithAPIKey(testAPIKey), WithJWT([]byte(testJWTSecret)))
}

const testJWTSecret = "test-jwt-secret"

func TestHTTPServer_JWTAcceptedOnProtectedEndpoint(t *testing.T) {
	tok, err := ares_security.SignJWT([]byte(testJWTSecret), "deploy-user", "operator", time.Hour, time.Now())
	require.NoError(t, err)

	// MCP tool call is protected; a valid operator JWT must pass.
	p := NewConsole(WithMCP(&mockMCPManager{
		result: &MCPToolResult{ToolName: "tool1", Output: map[string]any{"ok": true}},
	})).(*MonitorPlugin)
	srv := NewHTTPServer(p, WithAPIKey(testAPIKey), WithJWT([]byte(testJWTSecret)))

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mcp/tools/tool1/call", nil)
	req.Header.Set("Authorization", bearerPrefix+tok)
	srv.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHTTPServer_JWTRejectedWhenRoleInsufficient(t *testing.T) {
	srv := newJWTTestHTTPServer(t)
	// agent role has read only — protected (write) endpoint must deny.
	tok, err := ares_security.SignJWT([]byte(testJWTSecret), "worker", "agent", time.Hour, time.Now())
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mcp/tools/tool1/call", nil)
	req.Header.Set("Authorization", bearerPrefix+tok)
	srv.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHTTPServer_JWTRejectedWhenExpired(t *testing.T) {
	srv := newJWTTestHTTPServer(t)
	tok, err := ares_security.SignJWT([]byte(testJWTSecret), "old-user", "operator", time.Minute, time.Now().Add(-2*time.Minute))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mcp/tools/tool1/call", nil)
	req.Header.Set("Authorization", bearerPrefix+tok)
	srv.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHTTPServer_APIKeyStillAcceptedWithJWTConfigured(t *testing.T) {
	// Backward compat: when both API key and JWT are configured, the API key
	// path must keep working.
	srv := newJWTTestHTTPServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/agents/a1/kill", nil)
	withTestAuth(req)
	srv.ServeHTTP(w, req)
	// The handler itself may 404/400 for unknown agent, but auth must pass
	// (not 401).
	assert.NotEqual(t, http.StatusUnauthorized, w.Code)
	assert.NotEqual(t, http.StatusForbidden, w.Code)
}

func TestHTTPServer_JWTAuthDeniesWhenSecretEmpty(t *testing.T) {
	// WithJWT([]byte("")) is a no-op (deny-by-default); the API key remains
	// the only credential.
	gin.SetMode(gin.TestMode)
	p := NewConsole().(*MonitorPlugin)
	srv := NewHTTPServer(p, WithJWT(nil), WithAPIKey(testAPIKey))

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/agents/a1/kill", nil)
	req.Header.Set("Authorization", bearerPrefix+"some-garbage-token")
	srv.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
