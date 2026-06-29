package discovery

import (
	"context"
	"fmt"
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

	status := probeMCP(checkCtx, endpoint, args)
	status.Latency = time.Since(start)
	status.CheckedAt = time.Now()

	return status, nil
}

// probeMCP connects to an MCP server, does initialize → list_tools → close.
func probeMCP(ctx context.Context, endpoint string, args []string) *HealthStatus {
	client, err := api_mcp.ConnectStdio(ctx, endpoint, endpoint, args)
	if err != nil {
		return &HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("connect failed: %v", err),
		}
	}
	defer client.Close()

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
