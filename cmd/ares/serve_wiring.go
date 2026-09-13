package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	api_tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/introspect"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	akf_mcp "github.com/Timwood0x10/ares/internal/knowledge/mcp"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/Timwood0x10/ares/internal/runtime/archive"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	flight "github.com/Timwood0x10/ares/internal/runtime/observability/flight"
	ares_skills "github.com/Timwood0x10/ares/internal/runtime/protocol/skills"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// wiringServeSignalWatch starts the signal-handling goroutine on the errgroup
// (M-C2 segment extraction: body moved verbatim from runServe). compPtr is
// the atomic slot the Bootstrap assignment publishes to.
func wiringServeSignalWatch(
	g *errgroup.Group,
	sigCh <-chan os.Signal,
	ctx context.Context,
	cancel context.CancelFunc,
	shutdownMgr *ares_shutdown.Manager,
	compPtr *atomic.Pointer[ares_bootstrap.Components],
) {
	g.Go(func() error {
		select {
		case <-sigCh:
			fmt.Println("\nShutting down...")
			// A second SIGINT/SIGTERM during the graceful shutdown forces an
			// immediate exit — a hung shutdown phase (or a stuck component)
			// must never trap the operator in an unstoppable process.
			// NOT adopted into the orchestrator: this watcher
			// must stay alive while the managed pools are being drained —
			// exactly the window when adopted loops are being torn down.
			// One-shot short task with its own recover boundary.
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Fprintf(os.Stderr, "force-exit watcher panicked: %v\n", r)
						os.Exit(1)
					}
				}()
				<-sigCh
				fmt.Fprintln(os.Stderr, "\nSecond signal received: forcing immediate exit")
				os.Exit(1)
			}()
			// Run the registered shutdown phases (HTTP → MCP → runtime) with a
			// bounded overall timeout. cancel() afterwards stops background
			// goroutines (event bridge, task submission) that wait on ctx.
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()
			if err := shutdownMgr.StartShutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "graceful shutdown error: %v\n", err)
			}
			// Give SystemRuntime Shutdown its own 15s budget so it
			// is not starved by phase callbacks that consumed the shared
			// 30s shutdownCtx. An expired context would skip MCP/Runtime/
			// FlightRecorder Stop, leaking goroutines and connections.
			sysRuntimeCtx, sysRuntimeCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer sysRuntimeCancel()
			shutdownSystemRuntime(compPtr, sysRuntimeCtx)
			cancel()
		case <-ctx.Done():
		}
		comp := compPtr.Load()
		if comp == nil {
			return nil
		}
		// Record the pre-shutdown component snapshot for shutdown diagnostics
		// (which components were still running before background exit).
		if snapJSON, snapErr := comp.Snapshot().JSON(); snapErr == nil {
			log.Info("system_runtime snapshot (shutdown)", "snap_json", string(snapJSON))
		}
		// Wait for Bootstrap's background goroutines (distillation subscriber,
		// GA evolution ticker, LLM suggestion ticker) to exit after the
		// context is cancelled, so none outlives the graceful shutdown.
		comp.WaitBackground()
		return nil
	})
}

// wiringServeEventStore builds the serve event store and runs Bootstrap
// (M-C2 segment extraction: body moved verbatim from runServe — EventStore
// first, Postgres when storage is configured / archive-enabled memory
// otherwise, then the infrastructure components via the single wiring hub).
//
// Persistence contract (M4.1): when cfg.Storage points at Postgres, the
// serve event stream is the durable events table via PostgresEventStore —
// fitness evidence and the task.* log survive restarts, and the Task
// Fabric folds them back on boot (createPeerAgents → RestoreFromStore).
// PG construction failures are FATAL, mirroring Bootstrap's evidence-pool
// posture: silently falling back to the in-memory store would make the
// persistence feature lie about durability. Memory mode keeps the
// archive-enabled compactable store (round_N.json archive +
// compaction/trim) unchanged.
//
// The store is passed via deps so Bootstrap wires Runtime/Memory against the
// real serve store instead of creating a throwaway MemoryEventStore. On
// success the store's shutdown is owned by the System Runtime (the
// eventstore stop hook closes it in reverse-topological order).
func wiringServeEventStore(ctx context.Context, cfg *ares_config.Config) (*ares_bootstrap.Components, ares_events.EventStore, *runtime.Manager, error) {
	serveStore, closeStore, err := newServeEventStore(cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create event store: %w", err)
	}
	comp, err := ares_bootstrap.Bootstrap(ctx, cfg, &ares_bootstrap.BootstrapDeps{
		EventStore: serveStore,
	})
	if err != nil {
		// Bootstrap ran its own cleanups; the store came from serve, so its
		// resources (the PG pool) are released here — mirroring Bootstrap's
		// cleanup of its evidence pool on partial failure.
		_ = closeStore() // best-effort cleanup on the failure path
		return nil, nil, nil, fmt.Errorf("bootstrap: %w", err)
	}
	return comp, comp.EventStore, comp.Runtime, nil
}

// wiringServeCfgStoreWatch builds the runtime config store + hot-reload
// watcher and reports the startup component snapshot (M-C2 segment
// extraction: body moved verbatim from runServe).
//
// The store holds the last-good config and its reload history, served via
// /runtime/config on the console HTTP server. When the serve command was
// started with an explicit config file, an fsnotify watcher hot-reloads it
// on change (failed reloads keep the previous config). With no config file
// (minimal --llm-url mode) the watcher is skipped — the store still serves
// the effective config snapshot.
func wiringServeCfgStoreWatch(ctx context.Context, g *errgroup.Group, cfg *ares_config.Config, comp *ares_bootstrap.Components) *ares_config.ConfigStore {
	cfgStore := ares_config.NewConfigStore(cfg)
	if serveConfigPath != "" {
		cfgPath := serveConfigPath
		g.Go(func() error {
			// Watch blocks until ctx cancels; a reload error is logged inside
			// the store (recorded to history), so returning here is only for
			// watcher setup failures and ctx cancellation.
			return cfgStore.Watch(ctx, cfgPath)
		})
	}

	// EventStore is wired into Memory during Bootstrap,
	// not post-Bootstrap here. validateServeConfig has already rejected a
	// config that fails Config.Validate, so a Memory section that is present
	// but nonsensical cannot reach this point. Memory itself is optional:
	// Bootstrap leaves comp.Memory nil when memory.enabled is false and the
	// consumers below probe for nil rather than assuming it exists.

	// Stage 1 observability: report the System Runtime component snapshot
	// (names, modes, lifecycle states) so operators can confirm which
	// components were assembled and reached Ready at startup.
	if snapJSON, snapErr := comp.Snapshot().JSON(); snapErr == nil {
		log.Info("system_runtime snapshot (startup)", "snap_json", string(snapJSON))
	} else {
		log.Warn("system_runtime snapshot unavailable", "err", snapErr)
	}
	return cfgStore
}

// wiringServeToolchain assembles the tool plane: public registry, MCP bridge,
// AKF tools, native tool discovery, capability search, skill catalog tools,
// and the ToolBinder with its planner bridge and tool-call feedback channel
// (M-C2 segment extraction: body moved verbatim from runServe).
func wiringServeToolchain(ctx context.Context, cfg *ares_config.Config, comp *ares_bootstrap.Components) (toolBinder sub.ToolBinder, registry *api_tools.Registry, internalReg *core_tools.Registry, err error) {
	// --- Tool registry (public API) ---
	registry, err = newToolRegistry()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create tool registry: %w", err)
	}

	// --- MCP servers: reuse the manager started by Bootstrap (single manager,
	// single set of connections; its Stop hook is registered below) and bridge
	// its tools into the internal + public registries. ---
	internalReg, err = setupMCP(ctx, comp.MCP, registry, ares_bootstrap.ToolDepsFromComponents(comp))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("MCP setup: %w", err)
	}

	// Register AKF (Knowledge Fabric) tools into the internal registry using
	// the shared KnowledgeRuntime from bootstrap. This is the critical wiring
	// that makes knowledge genome patches (ChangeBudget/ChangePlanner/
	// ChangeReducer) affect the actual runtime used by the agent's knowledge
	// tools — because both the evolution system's KnowledgePatchExecutor and
	// the agent's AKF tools share the same comp.KnowledgeRuntime instance.
	wiringServeAKFTools(comp, internalReg)

	// --- ToolBinder for agents ---
	// Primitive 7 wiring: probe host commands from the ARES_NATIVE_TOOLS
	// allowlist and register them into the internal registry (command -v +
	// --help; security boundary = allowlist only). Registered tools flow into
	// GetLLMTools naturally; SetActiveTools lets the runtime narrow the active
	// subset per task (progressive disclosure), and serve keeps the full set
	// active by default (zero-value behavior, no change to LLM tool injection).
	if err := registerNativeTools(ctx, internalReg); err != nil {
		return nil, nil, nil, fmt.Errorf("register native tools: %w", err)
	}

	// Expose the environment-capability searcher as
	// the `search_capabilities` tool so agents can actively discover tools,
	// skills, and native commands. Registered before the binder is built so it
	// flows into the agent tool set naturally. comp.SkillsRegistry may be nil
	// (skills disabled) — the searcher skips that source.
	if err := registerCapabilitySearch(internalReg, comp.SkillsRegistry); err != nil {
		return nil, nil, nil, fmt.Errorf("register capability search: %w", err)
	}

	// Expose the skill catalog as first-class agent tools
	// (skill_search / skill_load / ...) so the LLM can drive progressive
	// disclosure itself instead of only receiving the resident prompt block.
	if comp.SkillCatalog != nil {
		for _, t := range ares_skills.CatalogTools(comp.SkillCatalog) {
			if err := internalReg.Register(t); err != nil {
				log.Warn("serve: register skill tool skipped", "tool", t.Name(), "err", err)
			}
		}
		log.Info("serve: skill catalog tools registered (progressive disclosure active)")
	}

	toolBinder = newToolBinder(internalReg)
	log.Info("tools registered", "count", len(toolBinder.ListTools()))

	// --- Capability Planner bridge for agent tool fallback ---
	if bridge := newPlannerBridge(internalReg); bridge != nil {
		toolBinder.WithPlannerBridge(bridge)
		log.Info("planner bridge: attached")
	}

	// Step Y.3: arm the tool-call perception channel. The decorator wraps the
	// binder AFTER the planner bridge is attached, so planner-resolved calls are
	// measured too, and it is applied at the single site every execution body
	// receives its binder from — instrumenting the cognition loop instead
	// would miss calls resolved elsewhere. A nil
	// recorder (channel not armed — the default) returns the binder untouched.
	if comp.NewEvolution != nil && comp.NewEvolution.ChannelFeedback.ToolCallsArmed() {
		toolBinder = sub.ObserveToolCalls(toolBinder, comp.NewEvolution.ChannelFeedback)
		log.Info("serve: tool-call feedback channel armed (evolution reads tool outcomes)")
	}

	return toolBinder, registry, internalReg, nil
}

// wiringServeAKFTools registers the AKF (Knowledge Fabric) tools into the
// internal registry using the shared KnowledgeRuntime from bootstrap (M-C2
// segment extraction: body moved verbatim from runServe). This is the
// critical wiring that makes knowledge genome patches (ChangeBudget/
// ChangePlanner/ChangeReducer) affect the actual runtime used by the agent's
// knowledge tools — because both the evolution system's
// KnowledgePatchExecutor and the agent's AKF tools share the same
// comp.KnowledgeRuntime instance.
func wiringServeAKFTools(comp *ares_bootstrap.Components, internalReg *core_tools.Registry) {
	if comp.KnowledgeRuntime != nil {
		akfSvc := akf_mcp.NewAKFService(comp.KnowledgeRuntime, &compiler.DefaultCompiler{})
		for _, akfTool := range akfSvc.Tools() {
			t := akfTool // capture
			adapted := &akfToolAdapter{name: t.Name, desc: t.Description, fn: t.Execute}
			if err := internalReg.Register(adapted); err != nil {
				log.Warn("AKF: failed to register tool", "name", t.Name, "err", err)
			}
		}
		log.Info("AKF tools registered with shared KnowledgeRuntime", "count", len(akfSvc.Tools()))
	}
}

// newServeEventStore builds the serve event store plus a cleanup function.
//
// Postgres mode — cfg.Storage.Enabled && cfg.Storage.Host != "", the exact
// predicate Bootstrap uses for its evidence pool so one storage config drives
// every durable subsystem consistently — the event stream persists in the
// events table via PostgresEventStore, so fitness evidence, the task.* log
// and thus the restore path survive restarts. Construction fail-loudly
// rejects an unreachable database instead of silently degrading to memory: a
// silent fallback would make the persistence feature lie (the operator
// believes events survive restarts while they evaporate on exit).
//
// TODO(tech-debt) partially resolved: PG mode consciously keeps dropping the
// memory-mode round_N.json archive and compaction/trim. Archive: the compactable
// wrapper's round/lastArchivedVersion boundaries are in-memory and would re-archive the
// whole restored history over existing round files after a restart; and the
// archive exists to preserve rounds before TRIM deletes them, which never
// happens in PG mode (the table itself is the durable history — a round file
// would be a redundant copy, not a preservation). Compaction: PostgresEventStore
// implements no TrimAwareStore and no PG SummaryRepository exists, so the wrapper
// would summarize into a repo that dies on restart while never trimming — pure
// overhead on the append path. The retention follow-up IS wired:
// storage.events_retention_days registers an events-table retention cleaner
// with the bootstrap maintenance worker (the evidence store's ExpiryCleaners
// pattern), bounding the table's growth without an archive.
//
// Returns:
//   - store: the event store to inject via BootstrapDeps.EventStore.
//   - close: releases the store's resources (PG pool); never nil.
//   - err: wrapped construction failure.
func newServeEventStore(cfg *ares_config.Config) (ares_events.EventStore, func() error, error) {
	if cfg.Storage.Enabled && cfg.Storage.Host != "" {
		pgCfg := &postgres.Config{
			Host:     cfg.Storage.Host,
			Port:     cfg.Storage.Port,
			User:     cfg.Storage.Username,
			Password: cfg.Storage.Password,
			Database: cfg.Storage.Database,
			SSLMode:  cfg.Storage.SSLMode,
		}
		pool, err := postgres.NewPool(pgCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("event store: create postgres pool: %w", err)
		}
		store, err := ares_events.NewPostgresEventStore(pool)
		if err != nil {
			_ = pool.Close() // best-effort: the pool must not leak when store construction fails
			return nil, nil, fmt.Errorf("event store: create postgres store: %w", err)
		}
		log.Info("serve: event store = postgres (task/event stream persists across restarts; task fabric restores on boot)")
		return store, store.Close, nil
	}
	compactable, _, err := archive.NewCompactableStoreWithArchive(cfg.Memory.Archive)
	if err != nil {
		return nil, nil, err
	}
	return compactable, compactable.Close, nil
}

// evolutionLifecycleForServe returns the wired evolution lifecycle, or nil
// when the evolution pipeline is not active. Both the control-plane snapshot
// endpoint and the actionHandler approval endpoint share one instance.
func evolutionLifecycleForServe(comp *ares_bootstrap.Components) *evolution.StrategyLifecycle {
	if comp == nil || comp.NewEvolution == nil {
		return nil
	}
	return comp.NewEvolution.Lifecycle
}

// setupServeControlPlane builds the runtime introspection control plane:
// the intelligence engine (health/anomalies/insights,
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
	flightRecorder *flight.FlightRecorder,
	lifecycle *evolution.StrategyLifecycle,
) (*introspect.Engine, *introspect.ControlServer, error) {
	// Intelligence engine: observes the shared event stream (fed by the
	// dedicated goroutine below, migrated from dashboard.EventBridge) to
	// score health / detect anomalies.
	intelEngine := introspect.NewEngine(nil)
	log.Info("intelligence engine started", "level", intelEngine.SystemHealth().Level, "count", len(intelEngine.Anomalies()))

	// Feed the intelligence engine from the shared event store. Independent of
	// the introspect panel sink: this subscription only powers
	// health/anomalies/insights. Best-effort — a broken subscribe is logged,
	// the engine just stays empty (deny-by-default health).
	if store != nil {
		g.Go(func() error {
			ch, err := store.Subscribe(ctx, ares_events.EventFilter{})
			if err != nil {
				log.Warn("[intel] event subscribe failed", "err", err)
				return nil
			}
			for {
				select {
				case <-ctx.Done():
					return nil
				case evt, ok := <-ch:
					if !ok {
						// Subscription closed on store shutdown: stop instead
						// of busy-spinning on a closed channel returning nil.
						return nil
					}
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
	// Observability (migrated from the deleted dashboard :8090 server):
	// evolution trajectory / human feedback / cross-Fabric spans.
	if obs != nil {
		opts = append(opts, obs.IntrospectOptions()...)
	}
	// Flight-recorder read surfaces (migrated from the deleted dashboard
	// /flight/* endpoints): timeline / summary / graph / decisions /
	// diagnostics / genealogy.
	if flightRecorder != nil {
		opts = append(opts, introspect.WithFlight(introspect.NewFlightRecorderAdapter(flightRecorder)))
	}
	// Evolution lifecycle state snapshot at /api/evolution/lifecycle.
	if lifecycle != nil {
		opts = append(opts, introspect.WithLifecycleSnapshot(lifecycle))
	}
	server := introspect.NewControlServer(agentsSource, opts...)
	return intelEngine, server, nil
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
	mgr *runtime.Manager,
	registry *api_tools.Registry,
	toolBinder sub.ToolBinder,
	shutdownMgr *ares_shutdown.Manager,
	comp *ares_bootstrap.Components,
	peerKernel *kernelHandle,
) (*http.Server, error) {
	addr := serverBindAddr(cfg.Server.Host, cfg.Server.Port)
	fmt.Println("=== ARES Console — Live Runtime ===")
	fmt.Printf("Console:  http://%s/introspect\n", displayServeHost(cfg.Server.Host, addr))
	fmt.Printf("LLM:      %s / %s\n", cfg.LLM.Provider, cfg.LLM.Model)
	fmt.Printf("Tools:    %v\n", toolBinder.ListTools())
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	// The introspect read side (/api/v1/introspect/*) carries task payloads;
	// its credential layers are the JWT read middleware, the legacy API key,
	// and the static introspect.token. Fail-safe posture: warn loudly (do
	// not block startup) so operators who deliberately opt into a wider bind
	// without credentials still see the exposure.
	authConfigured := cfg.Security.AuthEnabled && cfg.Security.JWTSecret != ""

	// API key for destructive endpoints (agents/chaos/tools). When empty,
	// all destructive requests are denied (deny-by-default). Configure via
	// ARES_API_KEY environment variable.
	serveAPIKey := os.Getenv("ARES_API_KEY")

	// M-S1: the control plane's exposure state is printed on every startup —
	// bind address, credential layers, introspect token — so the effective
	// security posture is visible, not implied by defaults.
	log.Info("serve: control-plane exposure state",
		"bind", addr,
		"wildcard_bind", isWildcardHost(cfg.Server.Host),
		"auth", authConfigured,
		"api_key", serveAPIKey != "",
		"introspect_token", cfg.Introspect.Token != "",
	)
	if cfg.Introspect.Token == "" {
		log.Warn("introspect read side: localhost only, no token — set introspect.token to require a bearer token from non-loopback clients")
	}
	// M-S2: the endpoint registry is the control-plane inventory — print its
	// auth-level distribution so the startup log answers "how many endpoints
	// are public / read-gated / write-gated" without reading code.
	routeLevels := map[authLevel]int{}
	for _, spec := range actionRoutes {
		routeLevels[spec.Auth]++
	}
	log.Info("serve: control-plane endpoint registry",
		"routes", len(actionRoutes),
		"none", routeLevels[authNone],
		"read", routeLevels[authRead],
		"write", routeLevels[authWrite],
		"local", routeLevels[authLocal],
	)
	// validateServeConfig has already refused to start on this combination
	// (wildcard host + no auth + no introspect token), so reaching here means
	// the operator either enabled auth, set a token, or bound loopback. The
	// remaining Info line records which posture is live for the audit trail.
	if isWildcardHost(cfg.Server.Host) && !authConfigured {
		log.Info("serve: wildcard bind is gated by introspect.token (security.auth_enabled is false)",
			"host", cfg.Server.Host)
	}

	// One shared audit sink for the actionHandler, so auth decisions and
	// destructive actions land in the same process log stream.
	auditLogger := ares_security.NewAuditLogger(slog.Default())

	// The actionHandler intercepts agent/chaos/tool/MCP routes BEFORE the
	// read-only control server (introspect.ControlServer), so it must carry
	// the same credentials and audit sink. JWT is enabled when configured.
	// authMW enforces WRITE on destructive endpoints; readAuthMW enforces
	// READ on the JSON read surfaces (introspect feed, tool inventories,
	// cost API) so enabling auth closes those too — not just the mutators.
	var authMW *ares_security.AuthMiddleware
	var readAuthMW *ares_security.AuthMiddleware
	if authConfigured {
		authMW = ares_security.NewAuthMiddleware([]byte(cfg.Security.JWTSecret), ares_security.PermWrite,
			ares_security.WithAudit(auditLogger))
		readAuthMW = ares_security.NewAuthMiddleware([]byte(cfg.Security.JWTSecret), ares_security.PermRead,
			ares_security.WithAudit(auditLogger))
	}
	handler := &actionHandler{
		inner: controlServer,
		// LLM cost dashboard — the SAME instance the client's
		// MetricsTracer records into, so /api/v1/observability/* reflects
		// real LLM cost attribution (single source of truth). The mux is
		// built here too: serveIntrospect dereferences costMux whenever
		// cost is set, so the pair must be wired atomically.
		cost:     comp.LLM.CostDashboard,
		costMux:  buildCostMux(comp.LLM.CostDashboard),
		mgr:      mgr,
		tools:    registry,
		apiKey:   serveAPIKey,
		auth:     authMW,
		readAuth: readAuthMW,
		audit:    auditLogger,
		// Introspect read-side bearer token (introspect.token, M-S1): a
		// lightweight credential for the panel read API when full JWT auth
		// is not wired — non-loopback clients must present it.
		introspectToken: cfg.Introspect.Token,
		// Peer runtime kernel: powers the POST /api/tasks submission endpoint
		// (submitPeerTask).
		kernel: peerKernel,
		// Chaos emergency-stop credential: POST /api/chaos/stop
		// requires a matching X-Chaos-Token header; empty disables the route.
		chaosStopToken: cfg.Kernel.Chaos.StopToken,
		// Runtime introspection panel (monitoring.md): UI + read API.
		intro: peerKernel.intro,
		// Evolution manual-approval gate (POST /api/evolution/approve).
		// Nil when the evolution pipeline is not wired — the endpoint then
		// reports 503 instead of silently swallowing approvals.
		lifecycle: evolutionLifecycleForServe(comp),
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
