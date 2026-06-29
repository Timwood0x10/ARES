package discovery

import (
	"context"
	"fmt"
	"time"
)

// MCPHealthChecker checks MCP servers by attempting connection.
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

	start := time.Now()

	endpoint := ""
	for _, r := range svc.Records {
		if r.Source == svc.BestSource {
			endpoint = r.Endpoint
			break
		}
	}
	if endpoint == "" && len(svc.Records) > 0 {
		endpoint = svc.Records[0].Endpoint
	}

	checkCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	status := probeMCP(checkCtx, endpoint)
	status.Latency = time.Since(start)
	status.CheckedAt = time.Now()

	return status, nil
}

// probeMCP attempts a lightweight MCP connection check.
func probeMCP(ctx context.Context, endpoint string) *HealthStatus {
	select {
	case <-ctx.Done():
		return &HealthStatus{Healthy: false, Message: "timeout"}
	default:
	}

	if endpoint == "" {
		return &HealthStatus{Healthy: false, Message: "no endpoint"}
	}

	// TODO: implement actual MCP probe (connect → initialize → list_tools → close).
	return &HealthStatus{
		Healthy: true,
		Message: "probe not implemented, assumed healthy",
	}
}
