package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/dashboard"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/adapter"
	"github.com/Timwood0x10/ares/internal/monitoring/data"
	"github.com/Timwood0x10/ares/internal/monitoring/tabs"
	"golang.org/x/sync/errgroup"
)

// setupServeMonitoring builds the PluginBus + MonitorPlugin console, the
// dashboard intelligence engine, the evolution store bridge, and the
// EventStore→PluginBus forwarder goroutine. Extracted from runServe to keep its
// cyclomatic complexity within gocyclo's 30 limit.
func setupServeMonitoring(
	ctx context.Context,
	g *errgroup.Group,
	cfg *ares_config.Config,
	mgr *ares_runtime.Manager,
	registry *api_tools.Registry,
	store ares_events.EventStore,
) (*monitoring.MonitorPlugin, error) {
	// NOTE: The ares_runtime plugin framework (PluginBus + capability discovery)
	// is actively consumed by the production workflow Runner
	// (internal/workflow/runner_plugins.go): CapCheckpoint/CapEvolution/Flusher/
	// EvolutionPlugin are type-asserted there to flush checkpoints and record
	// evolution outcomes at run boundaries.
	//
	// The built-in plugin IMPLEMENTATIONS, however, are retained as capability
	// reserves (future "nice-to-have", not dead code) and are intentionally NOT
	// registered here, because the unified workflow Runner already provides
	// native loop/checkpoint/routing via LoopSpec/WithCheckpointStore/NodeRouter:
	//   - LoopPlugin/CheckpointPlugin — exercised as fixtures by the graph
	//     executor's plugin-integration tests (internal/workflow/graph).
	//   - ArenaPlugin (fault injection) / ObserverPlugin (event observation) /
	//     ToolPlugin (tool bridge) / MemoryRouter / EvolutionRouter /
	//     FallbackRouter / NewEvolutionPlugin — complete, tested capability
	//     reserves. Wiring them changes runtime behavior, which is a product/
	//     direction decision (code_rules_v2 铁律 #4), so they are deferred. See
	//     docs/analysis-reports/ares-runtime-capability-reserve.md for the enablement
	//     path.
	bus := ares_runtime.NewPluginBus()
	tracker := data.NewAgentTracker()
	linker := data.NewTraceLinker()
	tabMap := map[string]monitoring.Tab{
		"events":    tabs.NewEventTab(),
		"memory":    tabs.NewMemoryTab(),
		"evolution": tabs.NewEvolutionTab(),
		"arena":     tabs.NewArenaTab(),
		"workflow":  tabs.NewWorkflowTab(),
		"mcp":       tabs.NewMCPTab(),
		"llm":       tabs.NewLLMTab(),
	}

	rtAdapter := adapter.NewRuntimeAdapter(&runtimeAdapterShim{mgr})
	mcpMgr := &mcpAdapter{registry: registry}

	plugin := monitoring.NewConsole(
		monitoring.WithAgentTracker(tracker),
		monitoring.WithTraceLinkerOption(linker),
		monitoring.WithTabMap(tabMap),
		monitoring.WithRuntimeManager(rtAdapter),
		monitoring.WithMCP(mcpMgr),
		// REVIEW #14: without a pruner the DAG engine's node map grows with
		// every agent/task for the process lifetime — the only remover is
		// the Pruner, which is constructed solely when this option is set.
		monitoring.WithPruneConfig(monitoring.PruneConfig{
			MaxAgentAge:   2 * time.Hour,
			PruneInterval: 10 * time.Minute,
		}),
	).(*monitoring.MonitorPlugin)

	if err := plugin.Start(ctx, bus); err != nil {
		return nil, fmt.Errorf("start monitor plugin: %w", err)
	}

	// ── Intelligence engine: bridge dashboard.Engine → monitoring.IntelProvider ──
	intelEngine := dashboard.NewEngine(nil)
	plugin.SetIntel(adapter.NewIntelAdapter(intelEngine))
	log.Printf("intelligence engine started: system=%s anomalies=%d",
		intelEngine.SystemHealth().Level, len(intelEngine.Anomalies()))

	// ── Evolution store: bridges flight genealogy → console AgentEvolution ──
	evoStore := &monitoring.EvolutionStore{}
	plugin.SetEvolutionStore(evoStore)

	// --- Bridge: EventStore → PluginBus ---
	// Agent metadata must cover BOTH config shapes: the C1 flat peers list is
	// the default and the legacy agents.sub entries are the fallback — the
	// same normalization createPeerAgents applies (normalizedPeers). Keying
	// off agents.sub alone left peers-config deployments with an empty meta
	// map, so bridged events carried no name/role/model enrichment.
	agentMetaByID := make(map[string]agentMeta, len(cfg.Agents.Sub))
	for _, s := range normalizedPeers(cfg) {
		role := ""
		if len(s.Capabilities) > 0 {
			role = s.Capabilities[0]
		}
		agentMetaByID[s.ID] = agentMeta{
			name:     s.ID,
			role:     role,
			model:    cfg.LLM.Model,
			parentID: "",
		}
	}
	g.Go(func() error {
		bridgeEvents(ctx, store, bus, agentMetaByID)
		return nil
	})
	return plugin, nil
}

// startServeHTTPAndHooks builds the console HTTP server, starts it in the
// background, and registers the graceful-shutdown hooks (HTTP → MCP → runtime
// → flight recorder) now that those components are initialized. It returns
// the started server so the caller can assign it to its signal-handler
// closure for a graceful Ctrl+C shutdown.
func startServeHTTPAndHooks(
	ctx context.Context,
	g *errgroup.Group,
	cfg *ares_config.Config,
	cfgStore *ares_config.ConfigStore,
	plugin *monitoring.MonitorPlugin,
	mgr *ares_runtime.Manager,
	registry *api_tools.Registry,
	toolBinder sub.ToolBinder,
	shutdownMgr *ares_shutdown.Manager,
	comp *ares_bootstrap.Components,
	peerKernel *kernelHandle,
) (*http.Server, error) {
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	fmt.Println("=== ARES Console — Live Runtime ===")
	fmt.Printf("Console:  http://localhost%s/console/\n", addr)
	fmt.Printf("LLM:      %s / %s\n", cfg.LLM.Provider, cfg.LLM.Model)
	fmt.Printf("Tools:    %v\n", toolBinder.ListTools())
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	server := monitoring.NewHTTPServer(plugin)

	// API key for destructive endpoints (agents/chaos/tools). When empty,
	// all destructive requests are denied (deny-by-default). Configure via
	// ARES_API_KEY environment variable.
	serveAPIKey := os.Getenv("ARES_API_KEY")
	// One shared audit sink for the gin middleware, the actionHandler and the
	// monitoring server, so auth decisions and destructive actions land in the
	// same process log stream (no duplicated loggers for one sink).
	auditLogger := ares_security.NewAuditLogger(slog.Default())
	opts := []monitoring.HTTPServerOption{
		monitoring.WithConfigStore(cfgStore),
		// Modular audit: records auth decisions (middleware) and destructive
		// actions (kill/resume/retry, MCP tool calls) on the process logger.
		monitoring.WithAudit(auditLogger),
	}
	if serveAPIKey != "" {
		opts = append(opts, monitoring.WithAPIKey(serveAPIKey))
	}
	// JWT auth for the same destructive endpoints. A request is accepted when
	// it presents either the API key or a valid JWT with write permission.
	// Secret comes from security.jwt_secret / ARES_JWT_SECRET; when auth is
	// enabled but no secret is set, protected endpoints stay deny-by-default
	// (misconfig is safer than an open endpoint).
	if cfg.Security.AuthEnabled {
		opts = append(opts, monitoring.WithJWT([]byte(cfg.Security.JWTSecret)))
	}
	if len(opts) > 0 {
		server = monitoring.NewHTTPServer(plugin, opts...)
	}

	// The actionHandler intercepts agent/chaos/tool routes BEFORE the gin
	// server, so it must carry the same credentials and audit sink as the gin
	// middleware (v0.3.0 review: these routes were API-key-only and un-audited
	// because the interception bypassed requireAPIKey). JWT is enabled exactly
	// when the gin server gets it.
	var authMW *ares_security.AuthMiddleware
	if cfg.Security.AuthEnabled && cfg.Security.JWTSecret != "" {
		authMW = ares_security.NewAuthMiddleware([]byte(cfg.Security.JWTSecret), ares_security.PermWrite,
			ares_security.WithAudit(auditLogger))
	}
	handler := &actionHandler{
		inner:  server,
		mgr:    mgr,
		tools:  registry,
		apiKey: serveAPIKey,
		auth:   authMW,
		audit:  auditLogger,
		// Peer runtime kernel: powers the POST /api/tasks submission endpoint
		// (submitPeerTask).
		kernel: peerKernel,
		// Chaos emergency-stop credential (#12 Phase 2): POST /api/chaos/stop
		// requires a matching X-Chaos-Token header; empty disables the route.
		chaosStopToken: cfg.Kernel.Chaos.StopToken,
	}

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	// Start HTTP server; gracefully shut down on signal.
	g.Go(func() error {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("HTTP server error: %w", err)
		}
		return nil
	})

	// Register graceful-shutdown hooks now that the server, MCP, and runtime
	// are initialized. Only the HTTP server stays here: MCP, Runtime, and
	// FlightRecorder teardown now lives in the System Runtime orchestrator
	// (Stage 9), which drives real Stop in reverse topological order during
	// the graceful shutdown sequence — removing the old duplicated teardown.
	if err := shutdownMgr.AddCallback(ares_shutdown.PhasePreShutdown, func(ctx context.Context) error {
		return httpSrv.Shutdown(ctx)
	}); err != nil {
		return nil, fmt.Errorf("register http shutdown hook: %w", err)
	}

	return httpSrv, nil
}

// shutdownSystemRuntime drives the System Runtime orchestration kernel through
// the same graceful shutdown so the managed component graph transitions to
// Stopped and the snapshot reflects the orderly teardown. Adapters now carry
// Stopper hooks (Stage 9), so the orchestrator stops MCP/Runtime/Flight in
// reverse topological order; nil guards keep this safe on the bootstrap-failure
// path. Extracted from runServe to keep its cyclomatic complexity within lint
// limits.
func shutdownSystemRuntime(compPtr *atomic.Pointer[ares_bootstrap.Components], ctx context.Context) {
	comp := compPtr.Load()
	if comp == nil || comp.SystemRuntime == nil {
		return
	}
	if err := comp.SystemRuntime.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "system_runtime shutdown error: %v\n", err)
	}
}

func loadServeConfig() (*ares_config.Config, error) {
	// Minimal setup: the user provides only the LLM endpoint (--llm-url) and
	// optionally the API key / model. Everything else — agents, memory, tools,
	// storage, kernel policy — is assembled by the runtime from defaults, so no
	// config file is required.
	if serveLLMURL != "" {
		cfg := ares_config.NewMinimalConfig(serveLLMURL, serveLLMKey, serveLLMModel)
		if servePort > 0 {
			cfg.Server.Port = servePort
		}
		log.Printf("serve: minimal config (llm-url only); runtime defaults for all subsystems")
		return cfg, nil
	}

	configPath := serveConfigPath
	if configPath == "" {
		for _, p := range []string{
			"ares.yaml",
			"./ares.yaml",
		} {
			if _, err := os.Stat(p); err == nil {
				configPath = p
				break
			}
		}
		if configPath == "" {
			configPath = "ares.yaml"
		}
		// Write the resolved path back so runServe's watcher starts for the
		// auto-detected config too (previously Watch only ran with an explicit
		// --config; hot-reload silently no-op'd on the default ares.yaml).
		serveConfigPath = configPath
	}

	cfg, err := ares_config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := ares_config.LoadFromEnv(cfg); err != nil {
		return nil, fmt.Errorf("load env: %w", err)
	}
	if servePort > 0 {
		cfg.Server.Port = servePort
	}
	return cfg, nil
}

// validateServeConfig enforces the dependencies required by the full agent
// serving entry point before Bootstrap starts any component.
func validateServeConfig(cfg *ares_config.Config) error {
	if cfg == nil {
		return errors.New("serve: config is required")
	}
	return nil
}

// createLLMAdapterWithFallback creates an LLM adapter with fallback chain.
func createLLMAdapterWithFallback(cfg *ares_config.Config) (output.LLMAdapter, error) {
	factory := output.NewFactory()

	// Try primary
	primaryCfg := &output.Config{
		Provider:  cfg.LLM.Provider,
		APIKey:    cfg.LLM.APIKey,
		BaseURL:   cfg.LLM.BaseURL,
		Model:     cfg.LLM.Model,
		Timeout:   cfg.LLM.Timeout,
		MaxTokens: cfg.LLM.MaxTokens,
	}

	adapter, err := factory.Create(cfg.LLM.Provider, primaryCfg)
	if err == nil {
		log.Printf("LLM adapter created: provider=%s model=%s", cfg.LLM.Provider, cfg.LLM.Model)
		return adapter, nil
	}
	log.Printf("primary LLM failed, trying fallbacks: %v", err)

	// Try fallbacks from config
	for _, fb := range cfg.LLM.Fallbacks {
		fbCfg := &output.Config{
			Provider:  fb.Provider,
			APIKey:    fb.APIKey,
			BaseURL:   fb.BaseURL,
			Model:     fb.Model,
			Timeout:   fb.Timeout,
			MaxTokens: fb.MaxTokens,
		}
		if fbCfg.Provider == "" {
			fbCfg.Provider = "openai"
		}
		adapter, err = factory.Create(fbCfg.Provider, fbCfg)
		if err == nil {
			log.Printf("LLM fallback adapter created: provider=%s model=%s", fbCfg.Provider, fbCfg.Model)
			return adapter, nil
		}
		log.Printf("fallback LLM failed: provider=%s error=%v", fbCfg.Provider, err)
	}

	// Last resort: ollama local
	log.Print("all remote LLMs failed, falling back to local ollama")
	ollamaCfg := &output.Config{
		Provider:  "ollama",
		BaseURL:   "http://localhost:11434",
		Model:     "llama3.2",
		Timeout:   120,
		MaxTokens: 2048,
	}
	adapter, err = factory.Create("ollama", ollamaCfg)
	if err != nil {
		// Wrap the sentinel so callers can errors.Is(err, ErrNoLLMAdapter)
		// while still retaining the underlying adapter-creation error.
		return nil, fmt.Errorf("no LLM adapter available: %w (last attempt: %v)", ErrNoLLMAdapter, err)
	}
	log.Printf("LLM fallback to ollama: model=llama3.2")
	return adapter, nil
}

// ErrNoLLMAdapter is the sentinel returned by createLLMAdapterWithFallback when
// every configured provider (primary, fallbacks, and the local ollama last
// resort) fails to produce an adapter. Callers that need to distinguish "no
// LLM available" from other serve failures should use errors.Is(err,
// ErrNoLLMAdapter) — e.g. to surface a degraded-mode warning instead of a hard
// crash. (code_rules_v2 §3: prefer typed errors over string matching.)
var ErrNoLLMAdapter = errors.New("serve: no LLM adapter available")

// agentMeta holds metadata for enriching events from real agents.
type agentMeta struct {
	name     string
	role     string
	model    string
	parentID string
}

// runtimeAdapterShim adapts ares_runtime.Manager to adapter.RuntimeManager.
type runtimeAdapterShim struct {
	mgr *ares_runtime.Manager
}

func (s *runtimeAdapterShim) NotifyAgentDead(agentID, reason string) {
	s.mgr.NotifyAgentDead(agentID, reason)
}

func (s *runtimeAdapterShim) RestartAgent(ctx context.Context, agentID string) error {
	return s.mgr.RestartAgent(ctx, agentID)
}

func (s *runtimeAdapterShim) GetAgentInfo(agentID string) (*adapter.AgentInfo, bool) {
	info, ok := s.mgr.GetAgentInfo(agentID)
	if !ok {
		return nil, false
	}
	return &adapter.AgentInfo{
		ID:       info.ID,
		Type:     info.Type,
		Status:   info.Status,
		Restarts: info.Restarts,
	}, true
}

var (
	_ adapter.RuntimeManager = (*runtimeAdapterShim)(nil)
)
