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
	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/introspect"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"golang.org/x/sync/errgroup"
)

// setupServeControlPlane builds the runtime introspection control plane
// (monitoring.md Phase 4): the intelligence engine (health/anomalies/insights,
// migrated from internal/dashboard) and the read-only control server that
// serves the old monitoring /api/agents + /api/health surface. The old
// MonitorPlugin / tabs / PluginBus bridge are gone — the introspection panel
// (internal/introspect) is the single observability surface.
func setupServeControlPlane(
	ctx context.Context,
	g *errgroup.Group,
	cfg *ares_config.Config,
	cfgStore *ares_config.ConfigStore,
	store ares_events.EventStore,
	peerKernel *kernelHandle,
	obs *ares_bootstrap.ObservabilityProviders,
) (*introspect.Engine, *introspect.ControlServer, error) {
	// Intelligence engine: observes the shared event stream (fed by the
	// dedicated goroutine below, migrated from dashboard.EventBridge) to
	// score health / detect anomalies.
	intelEngine := introspect.NewEngine(nil)
	log.Printf("intelligence engine started: system=%s anomalies=%d",
		intelEngine.SystemHealth().Level, len(intelEngine.Anomalies()))

	// Feed the intelligence engine from the shared event store. Independent of
	// the introspect panel sink: this subscription only powers
	// health/anomalies/insights. Best-effort — a broken subscribe is logged,
	// the engine just stays empty (deny-by-default health).
	if store != nil {
		g.Go(func() error {
			ch, err := store.Subscribe(ctx, ares_events.EventFilter{})
			if err != nil {
				log.Printf("[intel] event subscribe failed: %v", err)
				return nil
			}
			for {
				select {
				case <-ctx.Done():
					return nil
				case evt := <-ch:
					introspect.FeedIntel(intelEngine, evt)
				}
			}
		})
	}

	// Read-only control server: /api/agents, /api/agents/:id, /api/health,
	// /api/anomalies, /api/insights. Agent source comes from the peer kernel's
	// agent fabric when the full kernel exists; otherwise the endpoints report
	// 503 (partial paths must still compile and serve).
	var agentsSource introspect.AgentSource
	if peerKernel != nil && peerKernel.agents != nil {
		agentsSource = &fabricAgentSource{fabric: peerKernel.agents}
	}
	var cfgOpt introspect.ControlServerOption
	if cfgStore != nil {
		cfgOpt = introspect.WithRuntimeConfig(func() (any, []map[string]any) {
			cfg := cfgStore.Current().Redacted()
			history := cfgStore.History()
			out := make([]map[string]any, 0, len(history))
			for _, h := range history {
				out = append(out, map[string]any{
					"time":    h.Time,
					"ok":      h.OK,
					"message": h.Message,
				})
			}
			return cfg, out
		})
	}
	opts := []introspect.ControlServerOption{
		introspect.WithIntel(intelEngine),
		cfgOpt,
	}
	// M3/M4 observability (migrated from the deleted dashboard :8090 server):
	// evolution trajectory / human feedback / cross-Fabric spans.
	if obs != nil {
		opts = append(opts, obs.IntrospectOptions()...)
	}
	server := introspect.NewControlServer(agentsSource, opts...)
	return intelEngine, server, nil
}

// fabricAgentSource adapts *agentfabric.Fabric to introspect.AgentSource so
// the control plane lists the live fabric population.
type fabricAgentSource struct {
	fabric *agentfabric.Fabric
}

// ListAgents implements introspect.AgentSource.
func (s *fabricAgentSource) ListAgents() []introspect.AgentView {
	views := s.fabric.AgentsView()
	out := make([]introspect.AgentView, 0, len(views))
	for _, v := range views {
		row := introspect.AgentView{
			ID:     v.Identity,
			Name:   v.Identity,
			Status: string(v.State),
		}
		if len(v.Capabilities) > 0 {
			row.Role = v.Capabilities[0]
		}
		out = append(out, row)
	}
	return out
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
	controlServer *introspect.ControlServer,
	intelEngine *introspect.Engine,
	mgr *ares_runtime.Manager,
	registry *api_tools.Registry,
	toolBinder sub.ToolBinder,
	shutdownMgr *ares_shutdown.Manager,
	comp *ares_bootstrap.Components,
	peerKernel *kernelHandle,
) (*http.Server, error) {
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	fmt.Println("=== ARES Console — Live Runtime ===")
	fmt.Printf("Console:  http://localhost%s/introspect\n", addr)
	fmt.Printf("LLM:      %s / %s\n", cfg.LLM.Provider, cfg.LLM.Model)
	fmt.Printf("Tools:    %v\n", toolBinder.ListTools())
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	// API key for destructive endpoints (agents/chaos/tools). When empty,
	// all destructive requests are denied (deny-by-default). Configure via
	// ARES_API_KEY environment variable.
	serveAPIKey := os.Getenv("ARES_API_KEY")
	// One shared audit sink for the actionHandler, so auth decisions and
	// destructive actions land in the same process log stream.
	auditLogger := ares_security.NewAuditLogger(slog.Default())

	// The actionHandler intercepts agent/chaos/tool/MCP routes BEFORE the
	// read-only control server (introspect.ControlServer), so it must carry
	// the same credentials and audit sink. JWT is enabled when configured.
	var authMW *ares_security.AuthMiddleware
	if cfg.Security.AuthEnabled && cfg.Security.JWTSecret != "" {
		authMW = ares_security.NewAuthMiddleware([]byte(cfg.Security.JWTSecret), ares_security.PermWrite,
			ares_security.WithAudit(auditLogger))
	}
	handler := &actionHandler{
		inner:  controlServer,
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
		// Runtime introspection panel (monitoring.md): UI + read API.
		intro: peerKernel.intro,
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
