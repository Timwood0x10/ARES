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
package apiimpl

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/api/handler"
	"github.com/Timwood0x10/ares/api/router"
	ares_bootstrap "github.com/Timwood0x10/ares/internal/ares_bootstrap"
	ares_config "github.com/Timwood0x10/ares/internal/ares_config"
	evalapi "github.com/Timwood0x10/ares/internal/ares_eval/service"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	"github.com/Timwood0x10/ares/internal/dashboard"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/memoryservice"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/retrievalservice"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/query"
)

// Service is the top-level application entry point. One call to StartService
// starts everything: LLM connection, MCP servers, event bridge, orchestrator,
// flight recorder, bootstrap components (runtime, memory, evolution), and HTTP
// dashboard. Use Stop for graceful shutdown and Wait to block until the context
// is cancelled.
type Service struct {
	cfg        *ServiceConfig
	orch       *dashboard.Orchestrator
	hub        *dashboard.WSHub
	eventStore *EventStore
	httpServer *http.Server
	handler    http.Handler
	handlerMu  sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.RWMutex
	closed     bool
	g          *errgroup.Group

	// Wired subsystems (constructed in StartService). They are retained so
	// they can be cleaned up on Stop and inspected by callers.
	pgPool     *postgres.Pool          // optional, only when Postgres.Enabled
	queryCache *query.MemoryQueryCache // in-memory query cache (has a cleanup goroutine)

	// Bootstrap components — wired via ares_bootstrap.Bootstrap for autonomous
	// runtime, memory, evolution, and service discovery.
	bootstrap *ares_bootstrap.Components
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
	llmCfg := &output.Config{
		Provider:  cfg.LLM.Provider,
		Model:     cfg.LLM.Model,
		BaseURL:   cfg.LLM.BaseURL,
		APIKey:    cfg.LLM.APIKey,
		MaxTokens: 4096,
		Timeout:   cfg.LLM.Timeout,
	}
	if cfg.LLM.MaxPromptLength > 0 {
		llmCfg.MaxPromptLength = cfg.LLM.MaxPromptLength
	}
	llm, err := output.CreateAdapter(cfg.LLM.Provider, llmCfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("llm init: %w", err)
	}
	if _, err := llm.Generate(ctx, "Reply OK"); err != nil {
		cancel()
		return nil, fmt.Errorf("llm not reachable: %w", err)
	}
	log.Info("llm connected", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)

	// --- MCP (support multiple servers) ---
	if len(cfg.MCP.Servers) == 0 {
		cancel()
		return nil, fmt.Errorf("no ares_mcp servers configured")
	}

	var allTools []ares_mcp.MCPToolDef
	var clientEntries []clientTools
	seenTools := make(map[string]bool)
	for i, srv := range cfg.MCP.Servers {
		mcpClient := ares_mcp.NewMCPClient(ares_mcp.MCPClientConfig{
			ServerName: srv.Name,
			Timeout:    60 * time.Second,
		})
		mcpTransport := ares_mcp.NewStdioTransport(ares_mcp.StdioConfig{
			Command: srv.Transport.Stdio.Command,
			Args:    srv.Transport.Stdio.Args,
		})
		if err := mcpClient.Connect(ctx, mcpTransport); err != nil {
			cancel()
			return nil, fmt.Errorf("ares_mcp connect server[%d] %q: %w", i, srv.Name, err)
		}
		tools, listErr := mcpClient.ListTools(ctx)
		if listErr != nil {
			log.Warn("ares_mcp list tools failed", "server", srv.Name, "error", listErr)
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
		log.Info("ares_mcp server connected", "server", srv.Name, "tools", len(tools))
	}
	log.Info("ares_mcp tools discovered", "total_servers", len(cfg.MCP.Servers), "tools", len(allTools))

	// --- Bootstrap: infrastructure components via single wiring hub ---
	// Wires EventStore, Runtime, Memory, MCP, LLM, Evolution, NewEvolution,
	// and service discovery — enabling autonomous agent operation without
	// manual wiring. Uses the ares_config.Config built from ServiceConfig.
	bootstrapCfg, bsErr := ares_configFromService(cfg)
	if bsErr != nil {
		cancel()
		return nil, fmt.Errorf("build bootstrap config: %w", bsErr)
	}
	comp, bsErr := ares_bootstrap.Bootstrap(ctx, bootstrapCfg, nil)
	if bsErr != nil {
		cancel()
		return nil, fmt.Errorf("bootstrap: %w", bsErr)
	}
	s.bootstrap = comp
	log.Info("bootstrap components initialized",
		"runtime", comp.Runtime != nil,
		"memory", comp.Memory != nil,
		"evolution", comp.Evolution != nil,
		"new_evolution", comp.NewEvolution != nil,
	)

	// Use errgroup for structured concurrency with error propagation.
	// The derived ctx is cancelled automatically when any goroutine returns
	// a non-nil error, ensuring sibling goroutines are notified.
	s.g, s.ctx = errgroup.WithContext(ctx)
	log.Info("service context derived from errgroup error propagation")

	// --- Hub + EventStore ---
	hub := dashboard.NewWSHub()
	s.handler = http.NotFoundHandler() // initialize before httpServer uses wrapper
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

	eventStore, err := NewEventStore()
	if err != nil {
		return nil, fmt.Errorf("create event store: %w", err)
	}
	s.eventStore = eventStore

	// ── Intelligence engine: powers anomaly detection + health scoring ──
	intelEngine := dashboard.NewEngine(nil)

	bridge := dashboard.NewEventBridge(eventStore, hub, intelEngine)
	if startErr := bridge.Start(ctx); startErr != nil {
		log.Warn("event bridge start failed", "error", startErr)
	}

	// --- Orchestrator ---
	if len(clientEntries) == 0 {
		cancel()
		return nil, fmt.Errorf("no ares_mcp client available for orchestrator")
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
		log.Warn("flight recorder start failed", "error", startErr)
	}
	orch.SetFlightRecorder(fr)

	// --- Dashboard HTTP server (unified Gin engine) ---
	statusServers := make([]MCPStatusServer, 0, len(clientEntries))
	for _, e := range clientEntries {
		statusServers = append(statusServers, MCPStatusServer{Name: e.name, Tools: e.tools})
	}
	dashAPI := dashboard.NewAPIv2(orch, &MCPStatusBridge{Tools: allTools, Servers: statusServers}, hub)
	adapter := &ArenaAdapter{Orch: orch, Store: eventStore}
	dashAPI.SetArena(adapter)
	dashAPI.SetSurvival(adapter)

	// ── Wired subsystems: memory / retrieval / eval / experience / query ──
	// Memory and retrieval use in-memory repositories (no external backend
	// required) and are always mounted. Eval is PostgreSQL-backed, so it is
	// mounted only when Postgres is enabled AND reachable. The experience
	// ranking/conflict resolvers and the query cache are constructed and
	// retained on the Service for later (embedding-dependent) wiring.
	wireWiredServices(s, dashAPI, cfg)

	// Create unified Gin server with dashboard + monitoring routes.
	monSrv := monitoring.NewHTTPServer(nil, monitoring.WithDashboardAPI(dashAPI))
	s.handler = monSrv
	s.httpServer = &http.Server{Addr: cfg.Dashboard.Addr, Handler: s.handlerWrapper(), ReadHeaderTimeout: 30 * time.Second} // nosec G112
	s.g.Go(func() error {
		log.Info("dashboard started", "url", "http://localhost"+cfg.Dashboard.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("dashboard http server: %w", err)
		}
		return nil
	})

	return s, nil
}

// wireWiredServices constructs and mounts the memory, retrieval, eval,
// experience, and query subsystems onto the dashboard API and/or the Service.
// Paths that cannot be wired in the current configuration (e.g. eval without a
// reachable Postgres) are skipped with a warning rather than failing the whole
// service startup — this keeps the existing launch path intact.
func wireWiredServices(s *Service, dashAPI *dashboard.APIv2, cfg *ServiceConfig) {
	// Memory service (in-memory repository).
	memSvc, err := memoryservice.NewService(&memoryservice.Config{
		Repo: memoryservice.NewMemoryRepository(),
	})
	if err != nil {
		log.Warn("memory service init failed, skipping memory wiring", "error", err)
	} else {
		memRouter := router.NewRouter()
		memRouter.RegisterMemoryEndpoints(handler.NewMemoryHandler(memSvc))
		dashAPI.SetMemoryMux(memRouter.Handler().(*http.ServeMux))
		log.Info("memory service wired (in-memory)")
	}

	// Retrieval service (in-memory repository).
	retSvc, err := retrievalservice.NewService(&retrievalservice.Config{
		Repo: retrievalservice.NewMemoryRepository(),
	})
	if err != nil {
		log.Warn("retrieval service init failed, skipping retrieval wiring", "error", err)
	} else {
		retRouter := router.NewRouter()
		retRouter.RegisterRetrievalEndpoints(handler.NewRetrievalHandler(retSvc))
		dashAPI.SetRetrievalMux(retRouter.Handler().(*http.ServeMux))
		log.Info("retrieval service wired (in-memory)")
	}

	// Eval service (PostgreSQL-backed, optional).
	if cfg.Postgres.Enabled {
		pgCfg := &postgres.Config{
			Host:     cfg.Postgres.Host,
			Port:     cfg.Postgres.Port,
			User:     cfg.Postgres.User,
			Password: cfg.Postgres.Password,
			Database: cfg.Postgres.Database,
			SSLMode:  cfg.Postgres.SSLMode,
		}
		if vErr := pgCfg.Validate(); vErr != nil {
			log.Warn("postgres config invalid, skipping eval wiring", "error", vErr)
		} else if pool, pErr := postgres.NewPool(pgCfg); pErr != nil {
			log.Warn("postgres pool init failed, skipping eval wiring", "error", pErr)
		} else {
			s.pgPool = pool
			evalRepo := evalapi.NewPGEvalResultRepository(pool.GetDB(), pool.GetDB())
			evalSvc, sErr := evalapi.NewService(evalRepo)
			if sErr != nil {
				log.Warn("eval service init failed, skipping eval wiring", "error", sErr)
				_ = pool.Close()
				s.pgPool = nil
			} else {
				evalRouter := router.NewRouter()
				if rErr := evalapi.RegisterRoutes(evalRouter, evalapi.NewHandler(evalSvc)); rErr != nil {
					log.Warn("eval routes register failed, skipping eval wiring", "error", rErr)
					_ = pool.Close()
					s.pgPool = nil
				} else {
					dashAPI.SetEvalMux(evalRouter.Handler().(*http.ServeMux))
					log.Info("eval service wired (postgres-backed)")
				}
			}
		}
	}

	// TODO(tech-debt): experience ranking/conflict-resolver construction removed.
	// The internal/ares_experience/service shim (RankingService/ConflictResolver
	// re-exports) was constructed here but its methods were never invoked on any
	// serve path — the underlying internal/ares_experience logic is consumed
	// directly via postgres retrieval and bootstrap distillation. If the service
	// layer needs to call these directly, re-add the wiring here.

	// Query cache (in-memory, runs a cleanup goroutine that must be closed on Stop).
	s.queryCache = query.NewMemoryQueryCache()
	log.Info("query cache constructed (in-memory)")
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

	// Close the in-memory query cache (stops its cleanup goroutine).
	if s.queryCache != nil {
		s.queryCache.Close()
	}

	// Close the optional PostgreSQL pool if it was wired.
	if s.pgPool != nil {
		if closeErr := s.pgPool.Close(); closeErr != nil {
			errs = append(errs, fmt.Errorf("postgres pool close: %w", closeErr))
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
			log.Error("create agent failed", "name", task.Name, "error", err)
			continue
		}
		log.Info("agent launched", "id", id, "name", task.Name)
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

// SetHTTPHandler replaces the HTTP server's handler atomically.
// Safe to call before or after ListenAndServe starts.
func (s *Service) SetHTTPHandler(handler http.Handler) {
	s.handlerMu.Lock()
	s.handler = handler
	s.handlerMu.Unlock()
}

// handlerWrapper returns an http.Handler that delegates to the currently set
// handler under handlerMu read lock. This avoids data races between
// SetHTTPHandler and the http.Server's internal reads during request processing.
func (s *Service) handlerWrapper() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handlerMu.RLock()
		h := s.handler
		s.handlerMu.RUnlock()
		h.ServeHTTP(w, r)
	})
}

// ares_configFromService converts a ServiceConfig to ares_config.Config
// so that Bootstrap can wire the full infrastructure stack (runtime, memory,
// evolution) from the same configuration used by the service entry point.
func ares_configFromService(cfg *ServiceConfig) (*ares_config.Config, error) {
	if cfg == nil {
		return nil, fmt.Errorf("service config must not be nil")
	}
	out := &ares_config.Config{}
	out.LLM.Provider = cfg.LLM.Provider
	out.LLM.Model = cfg.LLM.Model
	out.LLM.BaseURL = cfg.LLM.BaseURL
	out.LLM.APIKey = cfg.LLM.APIKey
	out.LLM.Timeout = cfg.LLM.Timeout
	out.Dashboard.Addr = cfg.Dashboard.Addr
	// Storage defaults to in-memory when no postgres config is provided.
	out.Storage.Enabled = cfg.Postgres.Enabled
	out.Storage.Type = "memory"
	if cfg.Postgres.Enabled {
		out.Storage.Type = "postgres"
		out.Storage.Host = cfg.Postgres.Host
		out.Storage.Port = cfg.Postgres.Port
		out.Storage.Username = cfg.Postgres.User
		out.Storage.Password = cfg.Postgres.Password
		out.Storage.Database = cfg.Postgres.Database
		out.Storage.SSLMode = cfg.Postgres.SSLMode
	}
	// Evolution defaults: disabled in the minimal service path unless
	// explicitly configured via the ServiceConfig's postgres section.
	out.Evolution.Enabled = cfg.Postgres.Enabled
	return out, nil
}

// Wait blocks until the service context is cancelled (e.g., by Stop or OS signal).
// It then performs a best-effort HTTP server shutdown and waits for all
// background goroutines managed by the errgroup to finish.
func (s *Service) Wait() {
	<-s.ctx.Done()
	_ = s.httpServer.Shutdown(context.Background())
	// Wait for all errgroup-managed goroutines to finish before returning.
	if s.g != nil {
		_ = s.g.Wait()
	}
}
