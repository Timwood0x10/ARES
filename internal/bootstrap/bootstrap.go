// package bootstrap provides startup wiring for MCP and Dashboard components.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"goagentx/internal/config"
	"goagentx/internal/dashboard"
	"goagentx/internal/events"
	"goagentx/internal/mcp"
	"goagentx/internal/memory"
	"goagentx/internal/runtime"
	"goagentx/internal/tools/resources/core"
)

// MCPDashboard holds the initialized MCP and Dashboard components.
type MCPDashboard struct {
	MCPManager *mcp.MCPManager
	HTTPServer *http.Server
	hub        *dashboard.WSHub
	bridge     *dashboard.EventBridge
}

// SetupMCP initializes the MCP manager from config and connects to servers.
func SetupMCP(ctx context.Context, cfg *config.MCPConfig, registry *core.Registry) (*mcp.MCPManager, error) {
	if cfg == nil || len(cfg.Servers) == 0 {
		return nil, nil
	}

	managerConfig := &mcp.MCPManagerConfig{
		Servers: make([]mcp.MCPServerConfig, 0, len(cfg.Servers)),
	}

	for _, s := range cfg.Servers {
		sc := mcp.MCPServerConfig{
			Name:      s.Name,
			Enabled:   s.Enabled,
			AutoStart: s.AutoStart,
			Timeout:   time.Duration(s.Timeout) * time.Second,
			Transport: mcp.TransportConfig{
				Type: s.Transport.Type,
			},
		}

		switch s.Transport.Type {
		case "stdio":
			if s.Transport.Stdio != nil {
				sc.Transport.Stdio = &mcp.StdioConfig{
					Command: s.Transport.Stdio.Command,
					Args:    s.Transport.Stdio.Args,
					Env:     s.Transport.Stdio.Env,
					WorkDir: s.Transport.Stdio.WorkDir,
				}
			}
		case "sse":
			if s.Transport.SSE != nil {
				sc.Transport.SSE = &mcp.SSEConfig{
					URL:     s.Transport.SSE.URL,
					Headers: s.Transport.SSE.Headers,
					Timeout: time.Duration(s.Transport.SSE.Timeout) * time.Second,
				}
			}
		}

		managerConfig.Servers = append(managerConfig.Servers, sc)
	}

	manager := mcp.NewMCPManager(managerConfig, registry)

	if err := manager.Start(ctx); err != nil {
		return nil, fmt.Errorf("start mcp manager: %w", err)
	}

	slog.Info("bootstrap: mcp manager started", "servers", len(cfg.Servers))
	return manager, nil
}

// SetupDashboard initializes the dashboard service, WebSocket hub, and HTTP server.
func SetupDashboard(
	ctx context.Context,
	cfg *config.DashboardAppConfig,
	rt runtime.Runtime,
	agents dashboard.AgentProvider,
	eventStore events.EventStore,
	memMgr memory.MemoryManager,
	mcpMgr dashboard.MCPStatusProvider,
) (*MCPDashboard, error) {
	if cfg == nil || cfg.Addr == "" {
		return nil, nil
	}

	dashConfig := &dashboard.DashboardConfig{
		Addr:           cfg.Addr,
		EnableAuth:     cfg.EnableAuth,
		WSPingInterval: time.Duration(cfg.WSPingInterval) * time.Second,
	}
	if dashConfig.WSPingInterval == 0 {
		dashConfig.WSPingInterval = 30 * time.Second
	}

	hub := dashboard.NewWSHub()

	go hub.Run()

	// Start event bridge if event store is available.
	var bridge *dashboard.EventBridge
	if eventStore != nil {
		bridge = dashboard.NewEventBridge(eventStore, hub)
		if err := bridge.Start(ctx); err != nil {
			slog.Warn("bootstrap: event bridge start failed", "error", err)
		}
	}

	api := dashboard.NewAPIv2(nil, mcpMgr, hub)

	httpServer := &http.Server{
		Addr:         cfg.Addr,
		Handler:      api.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("bootstrap: dashboard initialized", "addr", cfg.Addr)

	return &MCPDashboard{
		MCPManager: nil,
		HTTPServer: httpServer,
		hub:        hub,
		bridge:     bridge,
	}, nil
}

// StartDashboard starts the dashboard HTTP server in a goroutine.
func StartDashboard(md *MCPDashboard) error {
	if md == nil || md.HTTPServer == nil {
		return nil
	}

	go func() {
		if err := md.HTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("bootstrap: dashboard server error", "error", err)
		}
	}()

	return nil
}

// StopDashboard gracefully shuts down the dashboard HTTP server, hub, and bridge.
func StopDashboard(ctx context.Context, md *MCPDashboard) error {
	if md == nil {
		return nil
	}

	if md.bridge != nil {
		md.bridge.Stop()
	}

	if md.hub != nil {
		md.hub.Stop()
	}

	if md.HTTPServer != nil {
		return md.HTTPServer.Shutdown(ctx)
	}
	return nil
}
