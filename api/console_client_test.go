package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServer creates an httptest.Server with the given handler.
func newTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestNewConsoleClient(t *testing.T) {
	c := NewConsoleClient("http://localhost:8080")
	require.NotNil(t, c)
	assert.Equal(t, "http://localhost:8080", c.baseURL)
	assert.NotNil(t, c.httpClient)
}

func TestNewConsoleClient_Options(t *testing.T) {
	customClient := &http.Client{Timeout: 5}
	c := NewConsoleClient("http://localhost:8080",
		WithHTTPClient(customClient),
		WithAPIKey("test-key"),
	)
	assert.Equal(t, customClient, c.httpClient)
	assert.Equal(t, "test-key", c.apiKey)
}

func TestConsoleClient_Snapshot(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/console", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		_ = json.NewEncoder(w).Encode(monitoring.ConsoleSnapshot{
			Agents: []monitoring.UnifiedAgent{{ID: "a1", Name: "worker"}},
		})
	}))

	c := NewConsoleClient(srv.URL)
	snap, err := c.Snapshot(context.Background())
	require.NoError(t, err)
	assert.Len(t, snap.Agents, 1)
	assert.Equal(t, "a1", snap.Agents[0].ID)
}

func TestConsoleClient_Snapshot_Error(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
	}))

	c := NewConsoleClient(srv.URL)
	_, err := c.Snapshot(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal error")
}

func TestConsoleClient_DAG(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/console/dag", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nodes": map[string]any{},
			"edges": map[string]any{},
		})
	}))

	c := NewConsoleClient(srv.URL)
	d, err := c.DAG(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, d)
}

func TestConsoleClient_Agent(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/agents/a1", r.URL.Path)
		// Server returns UnifiedAgent-like data for the agent endpoint.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "a1",
			"name": "worker",
		})
	}))

	c := NewConsoleClient(srv.URL)
	agent, err := c.Agent(context.Background(), "a1")
	require.NoError(t, err)
	assert.Equal(t, "a1", agent.ID)
	assert.Equal(t, "worker", agent.Name)
}

func TestConsoleClient_Agent_NotFound(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))

	c := NewConsoleClient(srv.URL)
	_, err := c.Agent(context.Background(), "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestConsoleClient_CostBreakdown(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/cost", r.URL.Path)
		_ = json.NewEncoder(w).Encode(monitoring.CostBreakdown{
			Total:    1.5,
			Currency: "USD",
		})
	}))

	c := NewConsoleClient(srv.URL)
	cb, err := c.CostBreakdown(context.Background())
	require.NoError(t, err)
	assert.InDelta(t, 1.5, cb.Total, 0.001)
}

func TestConsoleClient_ListMCPTools(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/mcp/tools", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]monitoring.MCPToolInfo{
			{Name: "read_file", Description: "Read a file"},
		})
	}))

	c := NewConsoleClient(srv.URL)
	tools, err := c.ListMCPTools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 1)
	assert.Equal(t, "read_file", tools[0].Name)
}

func TestConsoleClient_CallMCPTool(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/mcp/tools/read_file/call", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		var args map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&args))
		assert.Equal(t, "/tmp/test", args["path"])

		_ = json.NewEncoder(w).Encode(monitoring.MCPToolResult{
			ToolName: "read_file",
			Output:   map[string]any{"content": "hello"},
		})
	}))

	c := NewConsoleClient(srv.URL)
	result, err := c.CallMCPTool(context.Background(), "read_file", map[string]any{"path": "/tmp/test"})
	require.NoError(t, err)
	assert.Equal(t, "read_file", result.ToolName)
	assert.Equal(t, "hello", result.Output["content"])
}

func TestConsoleClient_Traces(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/trace/trace-1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"trace_id": "trace-1",
			"spans":    []monitoring.TraceSpan{{TraceID: "trace-1", SpanID: "s1"}},
		})
	}))

	c := NewConsoleClient(srv.URL)
	spans, err := c.Traces(context.Background(), "trace-1")
	require.NoError(t, err)
	assert.Len(t, spans, 1)
}

func TestConsoleClient_WithAPIKey(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(monitoring.ConsoleSnapshot{})
	}))

	c := NewConsoleClient(srv.URL, WithAPIKey("test-key"))
	_, err := c.Snapshot(context.Background())
	require.NoError(t, err)
}

func TestConsoleClient_ContextCanceled(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(monitoring.ConsoleSnapshot{})
	}))

	c := NewConsoleClient(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Snapshot(ctx)
	require.Error(t, err)
}

func TestConsoleClient_UnimplementedMethods(t *testing.T) {
	c := NewConsoleClient("http://localhost:0")
	ctx := context.Background()

	t.Run("Events", func(t *testing.T) {
		_, err := c.Events(ctx, 10)
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("AgentCost", func(t *testing.T) {
		_, err := c.AgentCost(ctx, "a1")
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("CostAlerts", func(t *testing.T) {
		_, err := c.CostAlerts(ctx)
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("Tasks", func(t *testing.T) {
		_, err := c.Tasks(ctx, nil)
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("Timeline", func(t *testing.T) {
		_, err := c.Timeline(ctx, "n1")
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("Actions", func(t *testing.T) {
		_, err := c.Actions(ctx, "n1")
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("Interactions", func(t *testing.T) {
		_, err := c.Interactions(ctx, 10)
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("AgentMemory", func(t *testing.T) {
		_, err := c.AgentMemory(ctx, "a1")
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("AgentEvolution", func(t *testing.T) {
		_, err := c.AgentEvolution(ctx, "a1")
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("MCPToolCalls", func(t *testing.T) {
		_, err := c.MCPToolCalls(ctx, "a1", 10)
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("LLMCalls", func(t *testing.T) {
		_, err := c.LLMCalls(ctx, "a1", 10)
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})

	t.Run("Recommendations", func(t *testing.T) {
		_, err := c.Recommendations(ctx)
		assert.ErrorIs(t, err, monitoring.ErrNotImplemented)
	})
}
