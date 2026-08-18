// Package ares_bootstrap — Dashboard provider.
package ares_bootstrap

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"net/http"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_mcp"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
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

// ProvideDashboard assembles the dashboard server and wires the v0.3.0 M3/M4
// observability surfaces from the SHARED aresrecovery components (created once
// in Bootstrap, not per-call), so the dashboard endpoints read the same tracer
// / feedback store the runtime writes.
//
// Args:
//   - ctx: lifetime of the hub goroutine.
//   - mcpMgr: MCP status provider (may be nil).
//   - addr: HTTP listen address.
//   - evolutionTracer: shared evolution trajectory tracer (M3-1).
//   - feedbackStore: shared human feedback store (M3-2).
//   - globalTracer: shared cross-Fabric tracer (M4-1).
func ProvideDashboard(
	ctx context.Context,
	mcpMgr *ares_mcp.MCPManager,
	addr string,
	evolutionTracer *aresrecovery.EvolutionTracer,
	feedbackStore *aresrecovery.FeedbackStore,
	globalTracer *aresrecovery.GlobalTracer,
) (*DashboardComponents, error) {
	hub := dashboard.NewWSHub()
	statusProvider := &mcpStatusAdapter{mcp: mcpMgr}
	api := dashboard.NewAPIv2(nil, statusProvider, hub)

	// Wire the v0.3.0 M3/M4 observability surfaces: the evolution trajectory
	// (M3-1), human feedback sink (M3-2) and cross-Fabric span provider (M4-1)
	// are backed by the shared aresrecovery components, so the dashboard
	// endpoints return data recorded by the runtime (GA generations, human
	// feedback, task/agent lifecycle) instead of empty lists.
	api.SetEvolutionTrajectory(NewEvolutionTrajectoryProvider(evolutionTracer))
	api.SetEvolutionFeedback(NewEvolutionFeedbackSink(feedbackStore))
	api.SetObservability(NewObservabilitySpansProvider(globalTracer))

	hubGrp, hubCtx := errgroup.WithContext(ctx)
	hubGrp.Go(func() error {
		hub.Run()
		return hubCtx.Err()
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
	}
	return &DashboardComponents{
		Start: func(ctx context.Context) error { return srv.ListenAndServe() },
		Stop: func(ctx context.Context) error {
			err := srv.Shutdown(ctx)
			hub.Stop()
			// Wait for the hub goroutine to exit so it is not leaked.
			_ = hubGrp.Wait()
			_ = hubCtx
			return err
		},
	}, nil
}
