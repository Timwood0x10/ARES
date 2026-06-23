// Package api provides the application-level entry point for ares.
// It is designed as a "complete application launcher" that wires up LLM, MCP,
// dashboard, event store, and flight recorder in a single call.
//
// Usage:
//
//	cfg, _ := api.LoadServiceConfig("config.yaml")
//	svc, _ := api.StartService(ctx, cfg)
//	svc.RunReview()
//	svc.Wait()
//
// For library-style embedding (modular access), use ares/api/client instead.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/dashboard"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/mcp"
)

// Service is the top-level application entry point. One call to StartService
// starts everything: LLM connection, MCP servers, event bridge, orchestrator,
// flight recorder, and HTTP dashboard. Use Stop for graceful shutdown and
// Wait to block until the context is cancelled.
type Service struct {
	cfg        *ServiceConfig
	orch       *dashboard.Orchestrator
	hub        *dashboard.WSHub
	eventStore *EventStore
	httpServer *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.RWMutex
	closed     bool
	g          *errgroup.Group // FIX: structured concurrency group (replaces bare go)
}

// StartService connects LLM, all MCP servers, creates orchestrator, starts
// dashboard, event store, and flight recorder. This is the complete application
// startup sequence — do not call it if you only need a subset of components.
//
// Startup failures return an error immediately; all goroutine errors are
// propagated via errgroup.
//
// Args:
//
//	ctx - parent context for cancellation propagation.
//	cfg - service configuration (LLM, MCP, Dashboard settings).
//
// Returns:
//
//	service - the fully initialized service instance.
//	err - error if any critical component fails to start (LLM unreachable, MCP connect failure).
func StartService(ctx context.Context, cfg *ServiceConfig) (*Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("service config must not be nil")
	}

	ctx, cancel := context.WithCancel(ctx)
	s := &Service{cfg: cfg, ctx: ctx, cancel: cancel}

	// --- LLM ---
	llm, err := output.CreateAdapter(cfg.LLM.Provider, &output.Config{
		Provider:  cfg.LLM.Provider,
		Model:     cfg.LLM.Model,
		BaseURL:   cfg.LLM.BaseURL,
		APIKey:    cfg.LLM.APIKey,
		MaxTokens: 4096,
		Timeout:   cfg.LLM.Timeout,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("llm init: %w", err)
	}
	if _, err := llm.Generate(ctx, "Reply OK"); err != nil {
		cancel()
		return nil, fmt.Errorf("llm not reachable: %w", err)
	}
	slog.Info("llm connected", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)

	// --- MCP (support multiple servers) ---
	if len(cfg.MCP.Servers) == 0 {
		cancel()
		return nil, fmt.Errorf("no mcp servers configured")
	}

	var allTools []mcp.MCPToolDef
	var clientEntries []clientTools
	seenTools := make(map[string]bool)
	for i, srv := range cfg.MCP.Servers {
		mcpClient := mcp.NewMCPClient(mcp.MCPClientConfig{
			ServerName: srv.Name,
			Timeout:    60 * time.Second,
		})
		mcpTransport := mcp.NewStdioTransport(mcp.StdioConfig{
			Command: srv.Transport.Stdio.Command,
			Args:    srv.Transport.Stdio.Args,
		})
		if err := mcpClient.Connect(ctx, mcpTransport); err != nil {
			cancel()
			return nil, fmt.Errorf("mcp connect server[%d] %q: %w", i, srv.Name, err)
		}
		tools, listErr := mcpClient.ListTools(ctx)
		if listErr != nil {
			slog.Warn("mcp list tools failed", "server", srv.Name, "error", listErr)
		}
		for _, t := range tools {
			if !seenTools[t.Name] {
				seenTools[t.Name] = true
				allTools = append(allTools, t)
			}
		}
		clientEntries = append(clientEntries, clientTools{
			client: mcpClient,
			name:   srv.Name,
			tools:  tools,
		})
		slog.Info("mcp server connected", "server", srv.Name, "tools", len(tools))
	}
	slog.Info("mcp tools discovered", "total_servers", len(cfg.MCP.Servers), "tools", len(allTools))

	// FIX: replace bare go with errgroup for structured concurrency (code rule 4.5).
	// All goroutines must be managed via errgroup to ensure error propagation
	// and context cancellation support.
	s.g, _ = errgroup.WithContext(ctx)

	// --- Hub + EventStore ---
	hub := dashboard.NewWSHub()
	s.g.Go(func() error {
		// Run hub's main loop — exits when hub.Stop() closes h.done.
		hub.Run()
		return nil
	})
	s.g.Go(func() error {
		// Watch service context and signal hub shutdown on cancel.
		<-s.ctx.Done()
		hub.Stop()
		return nil
	})
	s.hub = hub

	eventStore := NewEventStore()
	s.eventStore = eventStore
	bridge := dashboard.NewEventBridge(eventStore, hub)
	if startErr := bridge.Start(ctx); startErr != nil {
		slog.Warn("event bridge start failed", "error", startErr)
	}

	// --- Orchestrator ---
	if len(clientEntries) == 0 {
		cancel()
		return nil, fmt.Errorf("no mcp client available for orchestrator")
	}
	var mcpExecutor dashboard.MCPExecutor
	if len(clientEntries) == 1 {
		mcpExecutor = &MCPAdapter{Client: clientEntries[0].client}
	} else {
		mcpExecutor = NewMultiMCPAdapter(clientEntries)
	}
	orch := dashboard.NewOrchestrator(
		mcpExecutor,
		&LLMAdapter{Adapter: llm},
	)
	orch.SetToolAliases(BuildToolAliases(allTools))
	orch.SetTemplates(BuildTemplates())
	orch.SetHub(hub)
	orch.SetEventStore(eventStore.RawStore())
	s.orch = orch

	// --- Flight Recorder ---
	fr := flight.NewFlightRecorder(flight.FlightRecorderConfig{EventStore: eventStore})
	if startErr := fr.Start(ctx); startErr != nil {
		slog.Warn("flight recorder start failed", "error", startErr)
	}
	orch.SetFlightRecorder(fr)

	// --- Dashboard HTTP server ---
	statusServers := make([]MCPStatusServer, 0, len(clientEntries))
	for _, e := range clientEntries {
		statusServers = append(statusServers, MCPStatusServer{Name: e.name, Tools: e.tools})
	}
	dashAPI := dashboard.NewAPIv2(orch, &MCPStatusBridge{Tools: allTools, Servers: statusServers}, hub)
	adapter := &ArenaAdapter{Orch: orch, Store: eventStore}
	dashAPI.SetArena(adapter)
	dashAPI.SetSurvival(adapter)
	s.httpServer = &http.Server{Addr: cfg.Dashboard.Addr, Handler: dashAPI.Handler(), ReadHeaderTimeout: 30 * time.Second} //nosec G112
	s.g.Go(func() error {
		slog.Info("dashboard started", "url", "http://localhost"+cfg.Dashboard.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("dashboard http server: %w", err)
		}
		return nil
	})

	return s, nil
}

// Stop gracefully shuts down all service resources: HTTP server, WebSocket hub,
// event store, and the internal context. It is safe to call Stop multiple times;
// subsequent calls are no-ops.
//
// Args:
//
//	ctx - shutdown context with timeout for graceful operations.
//
// Returns:
//
//	err - the first non-nil error encountered during shutdown, or nil on success.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	// Signal all dependent goroutines to stop via context cancellation
	if s.cancel != nil {
		s.cancel()
	}

	var errs []error

	// Shutdown HTTP server with timeout
	if s.httpServer != nil {
		if shutdownErr := s.httpServer.Shutdown(ctx); shutdownErr != nil {
			errs = append(errs, fmt.Errorf("http server shutdown: %w", shutdownErr))
		}
	}

	// Stop hub and event store explicitly.
	if s.hub != nil {
		s.hub.Stop()
	}
	if s.eventStore != nil {
		if closeErr := s.eventStore.RawStore().Close(); closeErr != nil {
			errs = append(errs, fmt.Errorf("event store close: %w", closeErr))
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// RunReview launches one agent per review task defined in DefaultReviewTasks.
func (s *Service) RunReview() {
	for _, task := range DefaultReviewTasks {
		req := BuildAgentRequest(task)
		id, err := s.orch.CreateAgent(req)
		if err != nil {
			slog.Error("create agent failed", "name", task.Name, "error", err)
			continue
		}
		slog.Info("agent launched", "id", id, "name", task.Name)
	}
}

// Orchestrator returns the underlying orchestrator for custom agent creation.
// Callers must not mutate the returned orchestrator's internal state.
func (s *Service) Orchestrator() *dashboard.Orchestrator {
	return s.orch
}

// HTTPServer returns the underlying HTTP server for handler customization.
// Must be called before Wait or Stop.
func (s *Service) HTTPServer() *http.Server {
	return s.httpServer
}

// SetHTTPHandler replaces the HTTP server's handler.
// FIX: protected by mutex to prevent data race when called concurrently
// with running requests (code rule 4.5). Must be called before Wait or Stop;
// behavior after server start is undefined.
func (s *Service) SetHTTPHandler(handler http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.httpServer.Handler = handler
}

// Wait blocks until the service context is cancelled (e.g., by Stop or OS signal).
// It then performs a best-effort HTTP server shutdown and waits for all
// background goroutines managed by the errgroup to finish.
func (s *Service) Wait() {
	<-s.ctx.Done()
	_ = s.httpServer.Shutdown(context.Background())
	// FIX: wait for all errgroup-managed goroutines to finish (replaces bare go).
	if s.g != nil {
		_ = s.g.Wait()
	}
}
