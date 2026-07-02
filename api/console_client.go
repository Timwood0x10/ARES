package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

// ClientOption configures ConsoleClient.
type ClientOption func(*ConsoleClient)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) ClientOption {
	return func(cc *ConsoleClient) {
		cc.httpClient = c
	}
}

// WithAPIKey sets an optional API key for authentication.
func WithAPIKey(key string) ClientOption {
	return func(cc *ConsoleClient) {
		cc.apiKey = key
	}
}

// Verify ConsoleClient satisfies the ConsoleAPI interface at compile time.
var _ monitoring.ConsoleAPI = (*ConsoleClient)(nil)

// ConsoleClient implements monitoring.ConsoleAPI by calling a remote
// HTTP server (served by monitoring.HTTPServer).
type ConsoleClient struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
}

// NewConsoleClient creates a ConsoleClient targeting the given base URL.
func NewConsoleClient(baseURL string, opts ...ClientOption) *ConsoleClient {
	c := &ConsoleClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// doRequest performs an HTTP request and decodes the JSON response into dst.
func (c *ConsoleClient) doRequest(ctx context.Context, method, path string, body any, dst any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("api: close response body", "error", err)
		}
	}()

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errResp.Error)
	}

	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Snapshot returns the full console state.
func (c *ConsoleClient) Snapshot(ctx context.Context) (*monitoring.ConsoleSnapshot, error) {
	var snap monitoring.ConsoleSnapshot
	if err := c.doRequest(ctx, http.MethodGet, "/api/console", nil, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// DAG returns the current DAG snapshot.
func (c *ConsoleClient) DAG(ctx context.Context) (*dag.DAGSnapshot, error) {
	var d dag.DAGSnapshot
	if err := c.doRequest(ctx, http.MethodGet, "/api/console/dag", nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Events returns recent events.
func (c *ConsoleClient) Events(_ context.Context, _ int) ([]*ares_events.Event, error) {
	return nil, fmt.Errorf("events: %w", monitoring.ErrNotImplemented)
}

// Agent returns details for a single agent.
func (c *ConsoleClient) Agent(ctx context.Context, agentID string) (*monitoring.UnifiedAgent, error) {
	var agent monitoring.UnifiedAgent
	if err := c.doRequest(ctx, http.MethodGet, "/api/agents/"+agentID, nil, &agent); err != nil {
		return nil, err
	}
	return &agent, nil
}

// AgentCost returns the cost breakdown for a specific agent.
func (c *ConsoleClient) AgentCost(_ context.Context, _ string) (*monitoring.AgentCost, error) {
	return nil, fmt.Errorf("agent cost: %w", monitoring.ErrNotImplemented)
}

// CostBreakdown returns the full cost breakdown.
func (c *ConsoleClient) CostBreakdown(ctx context.Context) (*monitoring.CostBreakdown, error) {
	var cb monitoring.CostBreakdown
	if err := c.doRequest(ctx, http.MethodGet, "/api/cost", nil, &cb); err != nil {
		return nil, err
	}
	return &cb, nil
}

// CostAlerts returns active cost alerts.
func (c *ConsoleClient) CostAlerts(_ context.Context) ([]monitoring.CostAlert, error) {
	return nil, fmt.Errorf("cost alerts: %w", monitoring.ErrNotImplemented)
}

// Tasks returns all task views.
func (c *ConsoleClient) Tasks(_ context.Context, _ *dag.NodeStatus) ([]monitoring.TaskView, error) {
	return nil, fmt.Errorf("tasks: %w", monitoring.ErrNotImplemented)
}

// Traces returns trace spans for a given trace ID.
func (c *ConsoleClient) Traces(ctx context.Context, traceID string) ([]monitoring.TraceSpan, error) {
	var resp struct {
		TraceID string                 `json:"trace_id"`
		Spans   []monitoring.TraceSpan `json:"spans"`
	}
	if err := c.doRequest(ctx, http.MethodGet, "/api/trace/"+traceID, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Spans, nil
}

// Timeline returns timeline events for a specific node.
func (c *ConsoleClient) Timeline(_ context.Context, _ string) ([]dag.TimelineEvent, error) {
	return nil, fmt.Errorf("timeline: %w", monitoring.ErrNotImplemented)
}

// Actions returns available actions for a specific node.
func (c *ConsoleClient) Actions(_ context.Context, _ string) ([]monitoring.NodeAction, error) {
	return nil, fmt.Errorf("actions: %w", monitoring.ErrNotImplemented)
}

// ExecuteAction performs an action on a node.
func (c *ConsoleClient) ExecuteAction(ctx context.Context, actionID string) (*monitoring.ActionResult, error) {
	var result monitoring.ActionResult
	if err := c.doRequest(ctx, http.MethodPost, "/api/agents/"+actionID+"/kill", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Interactions returns recent interactions.
func (c *ConsoleClient) Interactions(_ context.Context, _ int) ([]monitoring.Interaction, error) {
	return nil, fmt.Errorf("interactions: %w", monitoring.ErrNotImplemented)
}

// Detail returns a detailed view for a selected entity.
func (c *ConsoleClient) Detail(ctx context.Context, _, entityID string) (*monitoring.DetailView, error) {
	var detail monitoring.DetailView
	if err := c.doRequest(ctx, http.MethodGet, "/api/agents/"+entityID, nil, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// AgentMemory returns the memory state of an agent.
func (c *ConsoleClient) AgentMemory(_ context.Context, _ string) (*monitoring.AgentMemory, error) {
	return nil, fmt.Errorf("agent memory: %w", monitoring.ErrNotImplemented)
}

// AgentEvolution returns the evolutionary history of an agent.
func (c *ConsoleClient) AgentEvolution(_ context.Context, _ string) (*monitoring.AgentEvolution, error) {
	return nil, fmt.Errorf("agent evolution: %w", monitoring.ErrNotImplemented)
}

// MCPToolCalls returns MCP tool call records.
func (c *ConsoleClient) MCPToolCalls(_ context.Context, _ string, _ int) ([]monitoring.MCPToolCall, error) {
	return nil, fmt.Errorf("MCP tool calls: %w", monitoring.ErrNotImplemented)
}

// LLMCalls returns LLM call records.
func (c *ConsoleClient) LLMCalls(_ context.Context, _ string, _ int) ([]monitoring.LLMCallRecord, error) {
	return nil, fmt.Errorf("LLM calls: %w", monitoring.ErrNotImplemented)
}

// Recommendations returns current recommendations.
func (c *ConsoleClient) Recommendations(_ context.Context) ([]monitoring.Recommendation, error) {
	return nil, fmt.Errorf("recommendations: %w", monitoring.ErrNotImplemented)
}

// ListMCPTools returns all available MCP tools.
func (c *ConsoleClient) ListMCPTools(ctx context.Context) ([]monitoring.MCPToolInfo, error) {
	var tools []monitoring.MCPToolInfo
	if err := c.doRequest(ctx, http.MethodGet, "/api/mcp/tools", nil, &tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// CallMCPTool invokes an MCP tool by name.
func (c *ConsoleClient) CallMCPTool(ctx context.Context, toolName string, args map[string]any) (*monitoring.MCPToolResult, error) {
	var result monitoring.MCPToolResult
	if err := c.doRequest(ctx, http.MethodPost, "/api/mcp/tools/"+toolName+"/call", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
