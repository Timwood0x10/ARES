package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	api_mcp "github.com/Timwood0x10/ares/api/mcp"
)

// MCPHealthChecker checks MCP servers by connecting and listing tools.
type MCPHealthChecker struct {
	timeout time.Duration
}

// NewMCPHealthChecker creates a health checker for MCP services.
func NewMCPHealthChecker(timeout time.Duration) *MCPHealthChecker {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &MCPHealthChecker{timeout: timeout}
}

func (c *MCPHealthChecker) CheckHealth(ctx context.Context, svc *DiscoveredService) (*HealthStatus, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is nil")
	}

	// Pick best endpoint.
	endpoint, args := bestEndpoint(svc)
	if endpoint == "" {
		return &HealthStatus{
			Healthy:   false,
			Message:   "no endpoint",
			CheckedAt: time.Now(),
		}, nil
	}

	start := time.Now()
	checkCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	status := probeMCP(checkCtx, svc.Identity.Name, endpoint, args)
	status.Latency = time.Since(start)
	status.CheckedAt = time.Now()

	return status, nil
}

// probeMCP connects to an MCP server via stdio, does initialize → list_tools → close.
func probeMCP(ctx context.Context, name, endpoint string, args []string) *HealthStatus {
	if isURL(endpoint) {
		// TODO: implement SSE health check when api/mcp supports it.
		return &HealthStatus{
			Healthy: true,
			Message: "SSE endpoint assumed healthy (probe not implemented)",
		}
	}

	client, err := api_mcp.ConnectStdio(ctx, name, endpoint, args)
	if err != nil {
		return &HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("connect failed: %v", err),
		}
	}
	defer func() {
		if err := client.Close(); err != nil {
			slog.Warn("health: close mcp client failed", "name", name, "error", err)
		}
	}()

	// list_tools verifies the server is fully functional.
	tools, err := client.ListTools(ctx)
	if err != nil {
		return &HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("list_tools failed: %v", err),
		}
	}

	return &HealthStatus{
		Healthy: true,
		Message: fmt.Sprintf("ok, %d tools", len(tools)),
	}
}

// bestEndpoint extracts the highest-confidence endpoint from a service.
func bestEndpoint(svc *DiscoveredService) (string, []string) {
	if len(svc.Records) == 0 {
		return "", nil
	}

	best := svc.Records[0]
	for _, r := range svc.Records[1:] {
		if r.Confidence > best.Confidence {
			best = r
		}
	}
	return best.Endpoint, best.Args
}

// isURL checks if an endpoint looks like a URL (http/https).
func isURL(endpoint string) bool {
	return strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://")
}
