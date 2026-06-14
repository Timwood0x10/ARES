// package api provides a high-level API for starting GoAgentX services.
//
// Usage:
//
//	cfg, _ := api.LoadConfig("config.yaml")
//	svc, _ := api.StartService(ctx, cfg)
//	svc.RunReview()
//	svc.Wait()
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"goagentx/internal/dashboard"
	"goagentx/internal/events"
	"goagentx/internal/flight"
	"goagentx/internal/llm/output"
	"goagentx/internal/mcp"
)

// Service is the top-level entry point. One call to StartService, everything runs.
type Service struct {
	cfg        *ServiceConfig
	orch       *dashboard.Orchestrator
	hub        *dashboard.WSHub
	eventStore *events.MemoryEventStore
	httpServer *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
}

// StartService connects MCP, LLM, creates orchestrator, starts dashboard.
func StartService(ctx context.Context, cfg *ServiceConfig) (*Service, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{cfg: cfg, ctx: ctx, cancel: cancel}

	// LLM
	llm, err := output.CreateAdapter(ctx, cfg.LLM.Provider, &output.Config{
		Provider: cfg.LLM.Provider, Model: cfg.LLM.Model,
		BaseURL: cfg.LLM.BaseURL, APIKey: cfg.LLM.APIKey,
		MaxTokens: 4096, Timeout: cfg.LLM.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("llm init: %w", err)
	}
	if _, err := llm.Generate(ctx, "Reply OK"); err != nil {
		return nil, fmt.Errorf("llm not reachable: %w", err)
	}
	slog.Info("llm connected", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)

	// MCP
	if len(cfg.MCP.Servers) == 0 {
		return nil, fmt.Errorf("no mcp servers configured")
	}
	mcpClient := mcp.NewMCPClient(mcp.MCPClientConfig{
		ServerName: cfg.MCP.Servers[0].Name,
		Timeout:    60 * time.Second,
	})
	mcpTransport := mcp.NewStdioTransport(mcp.StdioConfig{
		Command: cfg.MCP.Servers[0].Transport.Stdio.Command,
		Args:    cfg.MCP.Servers[0].Transport.Stdio.Args,
	})
	if err := mcpClient.Connect(ctx, mcpTransport); err != nil {
		return nil, fmt.Errorf("mcp connect: %w", err)
	}
	tools, _ := mcpClient.ListTools(ctx)
	slog.Info("mcp tools discovered", "count", len(tools))

	// Hub + EventStore
	hub := dashboard.NewWSHub()
	go hub.Run()
	s.hub = hub

	eventStore := events.NewMemoryEventStore()
	s.eventStore = eventStore
	bridge := dashboard.NewEventBridge(eventStore, hub)
	if startErr := bridge.Start(ctx); startErr != nil {
		slog.Warn("event bridge start failed", "error", startErr)
	}

	// Orchestrator
	orch := dashboard.NewOrchestrator(
		&MCPAdapter{Client: mcpClient},
		&LLMAdapter{Adapter: llm},
	)
	orch.SetToolAliases(BuildToolAliases(tools))
	orch.SetTemplates(BuildTemplates())
	orch.SetHub(hub)
	orch.SetEventStore(eventStore)
	s.orch = orch

	// Flight Recorder
	fr := flight.NewFlightRecorder(flight.FlightRecorderConfig{EventStore: eventStore})
	if startErr := fr.Start(ctx); startErr != nil {
		slog.Warn("flight recorder start failed", "error", startErr)
	}
	orch.SetFlightRecorder(fr)

	// Dashboard
	api := dashboard.NewAPIv2(orch, &MCPStatusBridge{Tools: tools}, hub)
	api.SetArena(&ArenaAdapter{Orch: orch, Store: eventStore})
	s.httpServer = &http.Server{Addr: cfg.Dashboard.Addr, Handler: api.Handler()}
	go func() {
		slog.Info("dashboard started", "url", "http://localhost"+cfg.Dashboard.Addr)
		_ = s.httpServer.ListenAndServe()
	}()

	return s, nil
}

// RunReview launches one agent per review task.
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
func (s *Service) Orchestrator() *dashboard.Orchestrator {
	return s.orch
}

// Wait blocks until shutdown signal.
func (s *Service) Wait() {
	<-s.ctx.Done()
	_ = s.httpServer.Shutdown(context.Background())
}
