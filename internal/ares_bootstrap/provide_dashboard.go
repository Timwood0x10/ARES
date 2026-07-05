// Package ares_bootstrap — Dashboard provider.
package ares_bootstrap

import (
	"context"
	"net/http"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_mcp"
	"github.com/Timwood0x10/ares/internal/dashboard"

	"golang.org/x/sync/errgroup"
)

// DashboardComponents holds dashboard server start/stop functions.
type DashboardComponents struct {
	Start func(ctx context.Context) error
	Stop  func(ctx context.Context) error
}

// mcpStatusAdapter wraps *ares_mcp.MCPManager to implement dashboard.MCPStatusProvider.
type mcpStatusAdapter struct {
	mcp *ares_mcp.MCPManager
}

func (a *mcpStatusAdapter) ListServers() []dashboard.MCPServerStatusView {
	if a.mcp == nil {
		return nil
	}
	servers := a.mcp.ListServers()
	views := make([]dashboard.MCPServerStatusView, len(servers))
	for i, s := range servers {
		views[i] = dashboard.MCPServerStatusView{
			Name: s.Name, Connected: s.Connected,
			ToolCount: s.ToolCount, Version: s.Version,
		}
	}
	return views
}

func ProvideDashboard(ctx context.Context, mcpMgr *ares_mcp.MCPManager) (*DashboardComponents, error) {
	hub := dashboard.NewWSHub()
	statusProvider := &mcpStatusAdapter{mcp: mcpMgr}
	api := dashboard.NewAPIv2(nil, statusProvider, hub)
	hubGrp, hubCtx := errgroup.WithContext(ctx)
	hubGrp.Go(func() error {
		hub.Run()
		return hubCtx.Err()
	})
	srv := &http.Server{
		Handler:           api.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
	}
	return &DashboardComponents{
		Start: func(ctx context.Context) error { return srv.ListenAndServe() },
		Stop:  func(ctx context.Context) error { return srv.Shutdown(ctx) },
	}, nil
}
