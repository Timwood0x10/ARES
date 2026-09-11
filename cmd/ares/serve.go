// serve — the `ares serve` command: runServe assembly skeleton, event store,
// config/LLM/tool wiring, agent + peer registry setup, control plane, and
// HTTP start. The chaos wiring lives in serve_chaos_domain.go, the L1/Live
// DAG builders in serve_live_dag.go, and the arena CLI in serve_arena.go —
// all split from the former merged serve.go (M-C2), which in turn came from
// serve.go, serve_routine.go, serve_agents.go, serve_chaos.go,
// serve_live_dag.go, arena.go.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/peer"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	api_tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/introspect"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	akf_mcp "github.com/Timwood0x10/ares/internal/knowledge/mcp"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/logger"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/Timwood0x10/ares/internal/runtime/archive"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	flight "github.com/Timwood0x10/ares/internal/runtime/observability/flight"
	"github.com/Timwood0x10/ares/internal/runtime/protocol/ahp"
	ares_skills "github.com/Timwood0x10/ares/internal/runtime/protocol/skills"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// log is the package-level structured logger for the ares serve command.
var log = logger.Module("ares")

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start full agent monitoring with LLM + MCP + dashboard",
	Long: `Starts the full ARES peer-agent runtime with LLM integration,
MCP tools, and the monitoring dashboard.

Flags:
  --config  Path to config YAML (default: ares.yaml)
  --port    HTTP port for dashboard (overrides config)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServe()
	},
}

var (
	serveConfigPath string
	serveHost       string
	servePort       int
	serveLLMURL     string
	serveLLMKey     string
	serveLLMModel   string
)

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVarP(&serveConfigPath, "config", "c", "", "Path to config YAML (optional; use --llm-url instead for minimal setup)")
	serveCmd.Flags().StringVar(&serveHost, "host", "", "HTTP bind address (overrides config; default 127.0.0.1 — use 0.0.0.0 to expose, requires auth)")
	serveCmd.Flags().IntVarP(&servePort, "port", "p", 0, "HTTP port for dashboard (overrides config)")
	serveCmd.Flags().StringVar(&serveLLMURL, "llm-url", "", "LLM endpoint URL — minimal setup, no config file needed")
	serveCmd.Flags().StringVar(&serveLLMKey, "llm-api-key", "", "LLM API key (minimal setup)")
	serveCmd.Flags().StringVar(&serveLLMModel, "llm-model", "", "LLM model name (optional, provider default when empty)")
}

//nolint:gocyclo // runServe is the serve assembly hub; each step is extracted.
func runServe() error {
	// --- Config ---
	cfg, err := loadServeConfig()
	if err != nil {
		return err
	}
	if err := validateServeConfig(cfg); err != nil {
		return err
	}

	// --- Context with signal handling ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Graceful shutdown coordinator (internal/ares_shutdown). Real teardown
	// hooks (HTTP server, MCP, runtime) are registered below once those
	// components are initialized.
	shutdownMgr := ares_shutdown.NewManager(30 * time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhasePreShutdown, 5*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseGraceful, 20*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseForce, 5*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseDone, 1*time.Second)

	g, ctx := errgroup.WithContext(ctx)
	// comp is assigned by Bootstrap below; the signal goroutine references it
	// for shutdown (WaitBackground + snapshot). The pointer is exchanged via
	// atomic.Store/Load so the goroutine never races with the Bootstrap
	// assignment on the main goroutine.
	var compPtr atomic.Pointer[ares_bootstrap.Components]
	wiringServeSignalWatch(g, sigCh, ctx, cancel, shutdownMgr, &compPtr)

	// --- EventStore + Bootstrap ---
	comp, store, mgr, err := wiringServeEventStore(ctx, cfg)
	if err != nil {
		return err
	}
	compPtr.Store(comp)
	// Assembly-phase exit check — if a shutdown signal arrived during the
	// (potentially long) Bootstrap, abort the startup instead of proceeding
	// to wire components and start the runtime on a canceled context.
	if err := ctx.Err(); err != nil {
		log.Info("serve: shutdown was requested during assembly; aborting startup", "err", err)
		return normalizeShutdownErr(err)
	}

	// --- Runtime config store + hot-reload watcher + startup snapshot ---
	cfgStore := wiringServeCfgStoreWatch(ctx, g, cfg, comp)

	// --- LLM adapter with fallback ---
	llmAdapter, err := createLLMAdapterWithFallback(cfg)
	if err != nil {
		return fmt.Errorf("create llm adapter: %w", err)
	}

	// --- ChatClient for native tool calling ---
	chatClient, err := createChatClient(cfg)
	if err != nil {
		return fmt.Errorf("create chat client: %w", err)
	}
	log.Info("chat client created", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)

	// --- Tools + MCP + binder ---
	toolBinder, registry, internalReg, err := wiringServeToolchain(ctx, cfg, comp)
	if err != nil {
		return err
	}

	// --- Create + register agents with the runtime manager ---
	subAgents, peerKernel, err := createAndServeAgents(ctx, cfg, internalReg, llmAdapter, chatClient, toolBinder, comp, mgr)
	if err != nil {
		return err
	}

	// --- Peer registry: enable direct agent-to-agent messaging ---
	// setupPeerRegistry builds the registry; the kernel handle powers
	// collaboration-topic execution through the fabric DAG. The
	// registry is retained on the kernel handle so it stays reachable for
	// direct peer messaging / capability discovery instead of being discarded.
	reg, err := setupPeerRegistry(ctx, g, subAgents, comp, peerKernel)
	if err != nil {
		return err
	}
	if peerKernel != nil {
		peerKernel.peerRegistry = reg
		log.Info("serve: peer registry retained on kernel (agents)", "count", len(reg.IDs()))
	}

	// --- Runtime introspection control plane:
	// intelligence engine + read-only control server (extracted to
	// setupServeControlPlane to keep runServe's cyclomatic complexity within
	// gocyclo's 30 limit). The old MonitorPlugin/tabs/PluginBus bridge is gone.
	intelEngine, controlServer, err := setupServeControlPlane(ctx, g, cfg, cfgStore, store, peerKernel, comp.Dashboard, comp.FlightRecorder, evolutionLifecycleForServe(comp))
	if err != nil {
		return err
	}

	// --- Start runtime ---
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("start runtime: %w", err)
	}

	// Sub-agents are execution units only (ares-runtime: agents are not
	// orchestrated, they are scheduled). The Kernel owns dispatch: the
	// kernelScheduler drives each task through RunQuantum →
	// sub.Agent.ExecuteStep; agents never subscribe to the event stream and
	// self-dispatch (self-dispatch was removed).

	// --- Dashboard APIv2 server (observability read side) ---
	// The old standalone dashboard :8090 server was removed; the
	// observability providers now feed introspect.ControlServer below.

	// --- HTTP server + graceful-shutdown hooks (extracted to keep runServe
	// cyclomatic complexity within lint limits) ---
	if _, err := startServeHTTPAndHooks(ctx, g, cfg, cfgStore, controlServer, intelEngine, mgr, registry, toolBinder, shutdownMgr, comp, peerKernel); err != nil {
		return err
	}

	// Wait for all goroutines to complete (signal handler, bridge, tasks, HTTP).
	// A context cancellation (SIGINT/SIGTERM → graceful shutdown) surfaces as
	// context.Canceled from the errgroup; that is a NORMAL exit, not an error —
	// normalized to nil so `ares serve` exits 0 on Ctrl-C.
	return normalizeShutdownErr(g.Wait())
}

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
	// not post-Bootstrap here. validateServeConfig has already enforced that
	// the full agent-serving entry point has its required Memory component.

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

// normalizeShutdownErr treats context cancellation (graceful shutdown) as a
// clean exit: Ctrl-C is not a failure. Extracted so runServe stays within the
// cyclomatic-complexity limit.
func normalizeShutdownErr(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
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

// akfToolAdapter adapts an AKF MCP tool (func(ctx, input string) -> string)
// to the core_tools.Tool interface so it can be registered in the internal
// tool registry and used by agents through the ToolBinder. This is the wiring
// that makes knowledge genome patches affect the agent's knowledge tools —
// because both share the same comp.KnowledgeRuntime instance.
type akfToolAdapter struct {
	name string
	desc string
	fn   func(ctx context.Context, input string) (string, error)
}

// Name returns the tool name.
func (a *akfToolAdapter) Name() string { return a.name }

// Description returns the tool description.
func (a *akfToolAdapter) Description() string { return a.desc }

// Category returns the tool category.
func (a *akfToolAdapter) Category() core_tools.ToolCategory { return core_tools.CategoryKnowledge }
func (a *akfToolAdapter) Capabilities() []core_tools.Capability {
	return []core_tools.Capability{core_tools.CapabilityKnowledge}
}
func (a *akfToolAdapter) Parameters() *core_tools.ParameterSchema { return nil }
func (a *akfToolAdapter) Execute(ctx context.Context, params map[string]interface{}) (core_tools.Result, error) {
	input, _ := params["input"].(string)
	if input == "" {
		// Serialize the whole params map as JSON input.
		b, _ := json.Marshal(params)
		input = string(b)
	}
	out, err := a.fn(ctx, input)
	if err != nil {
		return core_tools.NewErrorResult(err.Error()), nil
	}
	return core_tools.NewResult(true, map[string]interface{}{"output": out}), nil
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
	if isWildcardHost(cfg.Server.Host) && !authConfigured {
		log.Info("WARNING: server.host binds all interfaces while security.auth_enabled is false — the unauthenticated introspect read API (/api/v1/introspect/*) is reachable from the network; set security.auth_enabled, introspect.token, or bind localhost", "host", cfg.Server.Host)
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

func loadServeConfig() (*ares_config.Config, error) {
	// Minimal setup: the user provides only the LLM endpoint (--llm-url) and
	// optionally the API key / model. Everything else — agents, memory, tools,
	// storage, kernel policy — is assembled by the runtime from defaults, so no
	// config file is required.
	if serveLLMURL != "" {
		cfg := ares_config.NewMinimalConfig(serveLLMURL, serveLLMKey, serveLLMModel)
		if serveHost != "" {
			cfg.Server.Host = serveHost
		}
		if servePort > 0 {
			cfg.Server.Port = servePort
		}
		log.Info("serve: minimal config (llm-url only); runtime defaults for all subsystems")
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
	// CLI flags win over env (SERVER_HOST/SERVER_PORT) and YAML: the explicit
	// argument is the most specific intent.
	if serveHost != "" {
		cfg.Server.Host = serveHost
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
		log.Info("LLM adapter created", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)
		return adapter, nil
	}
	log.Warn("primary LLM failed, trying fallbacks", "err", err)

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
			log.Info("LLM fallback adapter created", "provider", fbCfg.Provider, "model", fbCfg.Model)
			return adapter, nil
		}
		log.Warn("fallback LLM failed", "provider", fbCfg.Provider, "err", err)
	}
	// Last resort: local ollama — but ONLY when the config did not
	// explicitly name any LLM. Silently switching an explicitly configured
	// hosted provider (bad key, wrong base URL) to a local llama hides the
	// misconfiguration and changes model behavior without notice; that is
	// a hard error now. An empty/unset LLM config is the legitimate
	// "local dev" case where the ollama default is a real convenience.
	llmConfigured := cfg.LLM.Provider != "" || len(cfg.LLM.Fallbacks) > 0
	if !llmConfigured {
		log.Info("no LLM configured, defaulting to local ollama (llama3.2)")
		ollamaCfg := &output.Config{
			Provider:  "ollama",
			BaseURL:   "http://localhost:11434",
			Model:     "llama3.2",
			Timeout:   120,
			MaxTokens: 2048,
		}
		adapter, err = factory.Create("ollama", ollamaCfg)
		if err == nil {
			return adapter, nil
		}
		// Wrap the sentinel so callers can errors.Is(err, ErrNoLLMAdapter)
		// while still retaining the underlying adapter-creation error.
		return nil, fmt.Errorf("no LLM adapter available: %w (last attempt: %v)", ErrNoLLMAdapter, err)
	}
	// The user configured LLM provider(s) and every one of them failed:
	// surface that instead of papering over it with an unrequested local
	// model.
	return nil, fmt.Errorf("all configured LLM providers failed (primary %q + %d fallback(s)): %w",
		cfg.LLM.Provider, len(cfg.LLM.Fallbacks), ErrNoLLMAdapter)
}

// ErrNoLLMAdapter is the sentinel returned by createLLMAdapterWithFallback when
// every configured provider (primary, fallbacks, and the local ollama last
// resort) fails to produce an adapter. Callers that need to distinguish "no
// LLM available" from other serve failures should use errors.Is(err,
// ErrNoLLMAdapter) — e.g. to surface a degraded-mode warning instead of a hard
// crash. (Prefer typed errors over string matching.)
var ErrNoLLMAdapter = errors.New("serve: no LLM adapter available")

// defaultServeHost is the fallback bind host when the config leaves
// server.host empty (a hand-built Config may skip setDefaults). The explicit
// loopback IP, not the "localhost" name — the bind must not depend on
// hosts-file resolution (M-S1).
const defaultServeHost = "127.0.0.1"

// serverBindAddr resolves the HTTP listen address from the server config.
// The host is the real bind address (default "localhost"); empty falls back
// rather than silently widening to a wildcard bind.
func serverBindAddr(host string, port int) string {
	if host == "" {
		host = defaultServeHost
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// isWildcardHost reports whether host selects all network interfaces
// (the "0.0.0.0" wildcard; IPv6's "::" is also a wildcard form).
func isWildcardHost(host string) bool {
	switch host {
	case "0.0.0.0", "::":
		return true
	default:
		return false
	}
}

// displayServeHost picks the host to print on the startup console: a wildcard
// bind prints the loopback probe address, because connecting to 0.0.0.0
// directly does not work on every platform and the panel URL must be usable.
func displayServeHost(host, addr string) string {
	if isWildcardHost(host) {
		return "localhost:" + strconv.Itoa(portOf(addr))
	}
	return addr
}

// portOf extracts the numeric port from a host:port address.
func portOf(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

// createAndServeAgents builds and registers the flat peer-agent population with
// the runtime manager. This is the ONLY production serve path (the leader is
// removed): the configured peers spawn into the Agent Fabric as
// the dynamic population, the scheduler queries the fabric for candidates,
// and the spawn_agent / create_task syscalls are wired into the tool binder for
// autonomous decomposition. The peer kernel is returned so the serve HTTP layer
// can expose the task-submission endpoint (POST /api/tasks → submitPeerTask).
func createAndServeAgents(
	ctx context.Context,
	cfg *ares_config.Config,
	internalReg *core_tools.Registry,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	comp *ares_bootstrap.Components,
	mgr *runtime.Manager,
) ([]sub.Agent, *kernelHandle, error) {
	// The Bootstrap experience repo (nil when distillation is not wired) feeds
	// the spawn prior. The StrategySource closes the GA strategy loop: the
	// evolution system deploys the best-evolved strategy into
	// NewEvolution.StrategyStore, and the planner cognition reads it on every
	// growth quantum (the planner is the strategy actuator) — without this
	// bridge the deployed strategies were consumed by nothing.
	var strategySrc agents.StrategySource
	if comp.NewEvolution != nil {
		strategySrc = ares_bootstrap.NewStrategySource(comp.NewEvolution.StrategyStore)
		if strategySrc != nil {
			log.Info("serve: evolution strategy source wired into agents (GA deploy → runtime read)")
		}
	}

	// Inject the L1 ToolClass capability graph into the evolution
	// system BEFORE creating peer agents so the plannerCognition can
	// read enabled/budget/prior at L2 growth time. The L1 graph is NOT
	// compiled into taskfabric — it is a capability catalog, not an
	// execution plan (L1 ≠ L2).
	injectToolClassDAG(comp, toolBinder)

	subAgents, peerKernel, err := createPeerAgents(ctx, cfg, comp, llmAdapter, chatClient, toolBinder, comp.EventStore, strategySrc, comp.ExpRepo)
	if err != nil {
		return nil, nil, fmt.Errorf("create peer agents: %w", err)
	}
	// Register agents with the runtime manager.
	for _, sa := range subAgents {
		factory := func() base.Agent { return sa }
		mgr.RegisterAgent(sa, factory)
	}
	log.Info("serve: peer agents registered directly to Kernel", "count", len(subAgents))

	// Live-DAG injection (closes the evolution structure-patch loop): the
	// configured agent population IS the live workflow topology. Register it
	// on the runtime manager (under the shared live-DAG key) and swap it
	// into the evolution executors — without this, workflow/recovery patches
	// mutated the synthetic input→process→output bootstrap DAG forever and
	// "live promotion" was unobservable.
	if comp.NewEvolution != nil {
		liveDAG, dagErr := buildLiveAgentDAG(cfg)
		switch {
		case dagErr == nil:
			mgr.RegisterAgentDAG(runtime.AgentDAGLiveKey, liveDAG)
			if err := comp.NewEvolution.UpdateLiveDAG(liveDAG); err != nil {
				log.Warn("serve: live DAG injection failed (evolution keeps placeholder)", "err", err)
			} else {
				log.Info("serve: live agent DAG injected into evolution executors (nodes)", "count", len(liveDAG.Steps()))
			}

			// Wire the compile coordinator so DAG mutations
			// are projected into PlanSteps and compiled into the task
			// fabric — the single projection path closes the "two
			// graphs" gap. The coordinator subscribes to GraphEvents
			// so structural patches (Insert/Remove/AddEdge) trigger
			// recompilation without restart.
			if peerKernel != nil && peerKernel.fabric != nil {
				peerKernel.compileCoord = planprojection.NewCompileCoordinator(
					peerKernel.fabric, comp.EventStore,
				)
				if _, err := peerKernel.compileCoord.CompileDAG(ctx, liveDAG); err != nil {
					log.Warn("serve: initial DAG compile failed", "err", err)
				} else {
					log.Info("serve: live DAG compiled into task fabric")
				}
				peerKernel.compileCoord.SubscribeGraphEvents(ctx, liveDAG)

				// Wire the compile coordinator into the strategy
				// lifecycle so /api/evolution/lifecycle carries the
				// attribution triplet (generation, gates, compile_id).
				// The CompileCoordinator satisfies the
				// evolution.CompileInfoProvider interface directly (it
				// has CompileID/DAGVersion/CompileCount methods).
				if comp.NewEvolution != nil && comp.NewEvolution.Lifecycle != nil {
					comp.NewEvolution.Lifecycle.SetCompileInfoProvider(
						peerKernel.compileCoord,
					)
				}
			}
		case errors.Is(dagErr, errNoLiveAgentDAG):
			log.Info("serve: no peers configured; evolution keeps placeholder DAG")
		default:
			log.Warn("serve: live agent DAG build failed (evolution keeps placeholder)", "err", dagErr)
		}
	}

	// Evolution-aware quota loop: "Evolution decides; Kernel enforces".
	// The GA strategy store publishes a
	// quota.budget param; the quota manager pushes it into the Agent Fabric's
	// resource admission budget on a fixed cadence. Without this the
	// deployed budget was consumed by nothing — the fabric kept its startup
	// config budget forever. The loop is best-effort: a nil evolution store
	// yields a nil policy source, so Apply is a no-op that leaves the
	// configured cfg.Kernel.Resources budget untouched (backward compatible).
	if peerKernel != nil && peerKernel.agents != nil && comp.NewEvolution != nil {
		quotaSrc := ares_bootstrap.NewQuotaPolicySource(comp.NewEvolution.StrategyStore, cfg.Kernel.Resources)
		if quotaSrc != nil {
			quotaMgr := aresrecovery.NewEvolutionAwareQuotaManager(peerKernel.agents, quotaSrc)
			runBackground(ctx, comp, "evolution-quota", func(loopCtx context.Context) error {
				runKernelQuotaLoop(loopCtx, quotaMgr, parseKernelLoopConfig(cfg))
				return nil
			})
			log.Info("serve: evolution quota loop wired (GA budget → fabric P5 admission)")
		}

		// Evolution-aware spawn gate: "Evolution decides; Kernel enforces". The GA strategy store publishes
		// spawn.{enabled,max_concurrent,preferred_capabilities}; the spawner
		// enforces them so every RECOVERY replacement spawn honors the evolved
		// timing gate and capability preference (the population cap is bypassed
		// for recovery — a self-healing spawn must not be stranded by quota).
		// Without this, the deployed spawn policy was consumed by nothing and
		// recovery always used the plain fabric spawn. Best-effort: a nil store
		// yields a nil source, so WithSpawner is skipped (plain spawn).
		if peerKernel.recovery != nil {
			spawnSrc := ares_bootstrap.NewSpawnPolicySource(comp.NewEvolution.StrategyStore)
			if spawnSrc != nil {
				spawner := aresrecovery.NewEvolutionAwareSpawner(peerKernel.agents, spawnSrc)
				peerKernel.recovery.WithSpawner(spawner)
				log.Info("serve: evolution spawn gate wired (GA policy → recovery spawn enforcement)")
			}
		}

		// Evolution-aware population loop (runtime adaptation).
		// "Evolution decides; Kernel enforces": the GA
		// strategy store publishes population.{spawn,retire}; the adapter
		// applies the desired delta through the Agent Fabric's spawn/retire
		// primitives on a fixed cadence (idempotent — an empty policy is a
		// no-op). This is the missing top-level growth/shrink path: the spawn
		// gate (stage-2) only shapes RECOVERY replacements, whereas this loop
		// grows or shrinks the live population per the evolved topology.
		// Best-effort: a nil store yields a nil source, so the loop is skipped.
		popSrc := ares_bootstrap.NewPopulationPolicySource(comp.NewEvolution.StrategyStore)
		if popSrc != nil {
			popAdapter := aresrecovery.NewPopulationAdapter(peerKernel.agents, popSrc)
			runBackground(ctx, comp, "evolution-population", func(loopCtx context.Context) error {
				aresrecovery.RunKernelEvolutionLoop(loopCtx, popAdapter, 0, 0)
				return nil
			})
			log.Info("serve: evolution population loop wired (GA topology → fabric spawn/retire)")
		}
	}

	// Runtime introspection panel: a pull-only
	// collector refreshes the latest-wins store every 2s; actionHandler serves
	// the embedded UI at GET /introspect and JSON at /api/v1/introspect/*.
	// The chaos status source is a shared reporter the chaos loops
	// update — created here so the collector and wireChaos see the same frame.
	//
	// SECURITY: the introspect handler is unauthenticated and its eventstream
	// endpoint exposes raw event payloads (task inputs, checkpoints). Only
	// expose it on localhost/an internal network or behind an authenticating
	// reverse proxy — never bind it directly to a public address.
	chaosStatus := introspect.NewChaosReporter()
	if peerKernel.scheduler != nil && peerKernel.fabric != nil && peerKernel.agents != nil {
		store := &introspect.Store{}
		collabReporter := introspect.NewCollabReporter()
		collector := introspect.NewCollector(introspect.Sources{
			Kernel:    peerKernel.scheduler.Snapshot,
			Fabric:    peerKernel.fabric.LeaseSnapshot,
			Agents:    peerKernel.agents.AgentsView,
			Chaos:     chaosStatus.Snapshot,
			Tasks:     peerKernel.fabric.TaskSnapshot,
			Decisions: peerKernel.scheduler.DecisionsSnapshot,
			// Collab: no producer records collaboration edges today; the
			// reporter yields an empty graph. Wire a producer (e.g. the
			// spawn/collaboration IPC path) before enabling the panel tab.
			Collab: collabReporter.Snapshot,
		})
		peerKernel.intro = introspect.NewHandler(store).WithEventStore(comp.EventStore).
			// The panel snapshot also carries the System Runtime
			// component graph (kernel pillars + bootstrap infrastructure),
			// so a "false Ready" kernel is visible on the read surface. The
			// provider is read-only; the endpoint is read-gated like the
			// rest of the JSON feed.
			WithSystemRuntime(func() any { return comp.Snapshot() })
		sink := introspect.NewSink(store).WithCollab(collabReporter)
		comp.GoBackground(ctx, "introspect-sink", func(ctx context.Context) error {
			return sink.Run(ctx, comp.EventStore)
		})
		comp.GoBackground(ctx, "introspect-collector", func(ctx context.Context) error {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					store.Set(collector.Collect())
				}
			}
		})
		log.Info("serve: introspect panel wired (GET /introspect)")
	}

	// Wire the chaos subsystem. Default is shadow sandbox
	// (production zero-impact); live mode requires explicit config plus the
	// wired GA generation probe for the quiet window. The shared chaos status
	// reporter bridges the loops into the introspection panel.
	// comp is passed so the chaos loops run as managed background loops.
	wireChaos(ctx, comp, cfg, peerKernel, func() bool {
		if comp.NewEvolution == nil {
			return false
		}
		return comp.NewEvolution.GAGenerationActive()
	}, chaosStatus)

	// Adopt the six kernel pillars into the System Runtime so the
	// component graph, the readiness snapshot and the reverse-topological
	// shutdown cover the kernel too — not just the Bootstrap infrastructure.
	if err := peerKernel.adopt(ctx, comp.SystemRuntime); err != nil {
		return nil, nil, err
	}

	return subAgents, peerKernel, nil
}

// buildPeerRegistry registers the peer agents' message senders into a
// peer.Registry so agents can exchange messages directly without routing
// through a privileged orchestrator (primitive 2: peer-to-peer agent
// messaging). Agents that do not expose SendMessage (interface assertion) are
// skipped, not an error.
//
// TODO(tech-debt): no production agent exposes the SendMessage surface any
// more — it was removed with the sub.Agent message queue — so this registry
// is always empty and non-evolution ask_agent sends fail loud with "not
// registered" (equivalent to the previous always-failing nil-queue
// delivery). Kept for the discovery contract; removing it means
// restructuring the non-evolution ask_agent branch.
func buildPeerRegistry(subAgents []sub.Agent) *peer.Registry {
	reg := peer.NewRegistry()
	for _, sa := range subAgents {
		if sender, ok := sa.(interface {
			SendMessage(context.Context, *ahp.AHPMessage) error
		}); ok {
			_ = reg.Register(sa.ID(), sender.SendMessage)
		}
	}
	return reg
}

// setupPeerRegistry builds the peer-to-peer messaging registry. When the
// evolution system is wired, the peer channel is bridged through the
// evolution-aware IPC; otherwise the plain direct peer channel
// is used.
func setupPeerRegistry(
	ctx context.Context,
	g *errgroup.Group,
	subAgents []sub.Agent,
	comp *ares_bootstrap.Components,
	kernel *kernelHandle,
) (*peer.Registry, error) {
	var reg *peer.Registry
	switch {
	case comp.NewEvolution != nil:
		bridge, err := wireEvolutionIPC(subAgents, comp.NewEvolution.StrategyStore, comp.Observability.GlobalTracer, kernel)
		if err != nil {
			return nil, fmt.Errorf("wire evolution IPC: %w", err)
		}
		reg = bridge.reg
		// Dead-letter observability (RUNTIME.md #10 closure): the bus records
		// every undeliverable/failed request, but the store had no reader —
		// failures vanished at the error-return boundary. A background loop
		// surfaces the count (and a sample of recent reasons) periodically so
		// messaging failures are operator-visible. Deliberately observe-only:
		// auto-redelivery of handler-rejected messages would retry
		// non-transient failures — redelivery stays an operator decision.
		g.Go(func() error {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			last := 0
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					cur := bridge.DeadLetterCount()
					if cur > 0 && cur != last {
						dl := bridge.ipc.Bus().DeadLetters().Snapshot()
						reasons := make(map[string]int, 4)
						for _, e := range dl {
							reasons[e.Reason]++
						}
						slog.WarnContext(ctx, "peer mode: IPC dead letters retained (undeliverable/failed requests)",
							"count", cur, "reasons", reasons)
					}
					last = cur
				}
			}
		})
		// Arm the collaboration perception channel. Attaching here
		// (rather than inside wireEvolutionIPC) keeps the bridge builder free
		// of an evolution-observer parameter, and this is the only production
		// site where the bus and the recorder are both in scope. A nil recorder
		// (channel not armed — the default) leaves the bus unobserved.
		if rec := comp.NewEvolution.ChannelFeedback; rec.CollaborationArmed() {
			bridge.ipc.Bus().WithCollaborationObserver(rec)
			log.Info("serve: collaboration feedback channel armed (evolution reads collaboration receipts)")
		}
		// Wire ask_agent to ipc.Send. The syscall Kernel is built
		// in peer_mode before the bridge exists, so the collaboration primitive
		// is injected here once the bridge is ready. Reusing ipc.Send means the
		// ask_agent attempt lands in the SAME "collaboration" feedback source as
		// bridge-routed collaboration — no new observation point (reuse
		// existing components unless none exists).
		if kernel != nil && kernel.syscalls != nil {
			ipc := bridge.ipc
			// dispatchCtx is the serve-lifetime context: the detached ask_agent
			// work is parented to it (and bounded by collabTimeout) so shutdown
			// aborts in-flight collaboration instead of leaking it to exit.
			dispatchCtx := ctx
			kernel.syscalls.SetAskAgent(func(_ context.Context, from, to, topic string, payload any) error {
				// Fire-and-forget per the syscall contract ("acceptance is not
				// an answer"): ipc.Send runs the collaboration handler
				// SYNCHRONOUSLY, and that handler drives a full L2 session
				// (executeAskViaSession, up to collabTimeout) whose reply the
				// syscall discards. Blocking the caller's quantum on a result
				// nobody reads would stall scheduling, so run the send on a
				// detached context and return acceptance immediately. The
				// session releases itself on completion/timeout; any left by a
				// cancelled serve ctx are swept by the existing reaper/idle-TTL
				// loops.
				//
				// runBackground (not a bare goroutine, per code rules 4.1):
				// the managed path adds a panic boundary around Send itself —
				// safeInvokeHandler only recovers HANDLER panics — and joins
				// the work at shutdown.
				runBackground(dispatchCtx, comp, "ask-agent-dispatch", func(bgCtx context.Context) error {
					asyncCtx, cancel := context.WithTimeout(bgCtx, collabTimeout)
					defer cancel()
					if err := ipc.Send(asyncCtx, from, to, topic, payload); err != nil {
						log.Warn("serve: ask_agent detached delivery failed",
							"from", from, "to", to, "topic", topic, "error", err)
					}
					// Never propagate: one failed collaboration must not
					// cancel the shared background group.
					return nil
				})
				return nil
			})
			log.Info("serve: ask_agent syscall wired to evolution-aware IPC (collaboration path)", "count", len(reg.IDs()))
		}
		log.Info("peer registry wired through evolution-aware IPC: agents registered", "count", len(reg.IDs()))
	default:
		reg = buildPeerRegistry(subAgents)
		// The ask_agent tool is advertised on the binder in every serve
		// path, so the default (non-evolution) branch must also wire the
		// collaboration primitive — otherwise the tool is advertised but
		// every call fails loud. Route through the plain peer registry
		// (no evolution observation here; the collaboration channel stays
		// disarmed until the evolution branch is taken).
		if kernel != nil && kernel.syscalls != nil {
			plainReg := reg
			// Left synchronous on purpose: this registry has no registered
			// sender (no production agent exposes SendMessage), so Send fails
			// immediately with "not registered" — a fast, useful diagnostic
			// the LLM should see, not a blocking wait to detach.
			kernel.syscalls.SetAskAgent(func(ctx context.Context, from, to, topic string, payload any) error {
				body := map[string]any{"topic": topic}
				if m, ok := payload.(map[string]any); ok {
					body["payload"] = m
				} else if payload != nil {
					body["payload"] = payload
				}
				msg := ahp.NewTaskMessage(from, to, "", "", body)
				return plainReg.Send(ctx, to, msg)
			})
			log.Info("serve: ask_agent syscall wired to plain peer registry (agents)", "count", len(reg.IDs()))
		}
		log.Info("peer registry wired: agents registered", "count", len(reg.IDs()))
	}
	// Retain the registry on the kernel handle at construction time (the
	// return value was previously discarded by callers). serve.go also assigns
	// it as a defensive second write; the retention contract must not depend
	// on a single call site.
	if kernel != nil {
		kernel.peerRegistry = reg
	}
	return reg, nil
}

// injectToolClassDAG builds the L1 ToolClass capability graph from the tool
// binder's schemas and injects it into the evolution system. Called
// BEFORE peer agents are created so the plannerCognition (constructed inside
// createPeerAgents when the DAG gate is open) can read enabled/budget/prior
// at L2 growth time. The L1 graph is NOT compiled into taskfabric — it is a
// capability catalog, not an execution plan (L1 ≠ L2).
func injectToolClassDAG(comp *ares_bootstrap.Components, toolBinder sub.ToolBinder) {
	if comp.NewEvolution == nil {
		return
	}
	l1DAG, err := buildToolClassDAG(toolBinder.GetToolSchemas())
	switch {
	case err == nil:
		comp.NewEvolution.SetToolClassDAG(l1DAG)
		log.Info("serve: L1 ToolClass DAG injected into evolution (nodes)", "count", len(l1DAG.Steps()))
	case errors.Is(err, errNoToolSchemas):
		log.Info("serve: no tool schemas; L1 ToolClass DAG skipped (constraints default to permissive)")
	default:
		log.Warn("serve: L1 ToolClass DAG build failed (constraints default to permissive)", "err", err)
	}
}

// errNoLiveAgentDAG is returned when no peers are configured: the caller
// keeps the bootstrap placeholder rather than injecting an empty graph.
var errNoLiveAgentDAG = errors.New("no peer agents configured for a live DAG")

// errNoToolSchemas is returned when the tool binder exposes no tool schemas:
// the L1 capability graph would be empty, so the caller keeps the bootstrap
// placeholder instead of injecting an empty graph.
var errNoToolSchemas = errors.New("no tool schemas available for a ToolClass DAG")

// buildLiveAgentDAG materializes the configured agent population as a real
// MutableDAG: one node per peer (AgentType = primary capability), dependency
// edges from the legacy agents.sub entries' Dependencies when present.
//
// This is the live topology the evolution system's structure patches act on.
// Historically serve never called UpdateLiveDAG, so workflow/recovery
// patches mutated the synthetic input→process→output bootstrap DAG forever —
// the "live runtime" promotion affected nothing observable. The returned DAG
// is registered on the runtime manager AND injected into the evolution
// executors so graph/recovery patches land on the agent graph actually shown
// in the runtime snapshot.
//
// Returns (nil, errNoLiveAgentDAG) when no peers are configured — the caller
// matches on that sentinel and keeps the bootstrap placeholder rather than
// injecting an empty graph.
func buildLiveAgentDAG(cfg *ares_config.Config) (*engine.MutableDAG, error) {
	peers := normalizedPeers(cfg)
	if len(peers) == 0 {
		return nil, errNoLiveAgentDAG
	}

	// Legacy sub entries may declare Dependencies between agents; carry them
	// over so older configs keep their declared topology.
	legacyDeps := make(map[string][]string, len(cfg.Agents.Sub))
	for _, s := range cfg.Agents.Sub {
		if len(s.Dependencies) > 0 {
			legacyDeps[s.ID] = append([]string(nil), s.Dependencies...)
		}
	}

	steps := make([]*engine.Step, 0, len(peers))
	seen := make(map[string]bool, len(peers))
	for _, p := range peers {
		if p.ID == "" || seen[p.ID] {
			continue // defensive: NewMutableDAG rejects duplicate ids anyway
		}
		seen[p.ID] = true
		typ := ""
		if len(p.Capabilities) > 0 {
			typ = p.Capabilities[0]
		}
		step := &engine.Step{
			ID:        p.ID,
			Name:      p.ID,
			AgentType: typ,
			Input:     fmt.Sprintf("capability:%s", typ),
		}
		if deps, ok := legacyDeps[p.ID]; ok {
			step.DependsOn = deps
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, errNoLiveAgentDAG
	}

	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		return nil, fmt.Errorf("build live agent DAG: %w", err)
	}
	return dag, nil
}

// L1 metadata keys for ToolClass evolution constraints. The L1 graph's
// Metadata is string-only (engine.Step.Metadata), so budget/prior are stored
// as their string representations.
const (
	l1MetaEnabled = "enabled"
	l1MetaBudget  = "budget"
	l1MetaPrior   = "prior"
)

// buildToolClassDAG constructs the L1 capability graph: one node per
// ToolClass (toolName + "#" + argShape), with Metadata carrying the evolution
// constraints enabled/budget/prior. The argShape is the sorted set of
// parameter key names from the tool's schema — this normalizes by type
// signature, not by value, so "read_file(path=foo.txt)" and
// "read_file(path=bar.txt)" collapse into one ToolClass node.
//
// The L1 graph is the evolution system's stable action surface: genome
// patches mutate enabled/budget/prior on L1 nodes, the planner reads them
// before growing L2 tool nodes, and L2 execution
// statistics flow back as fitness. The L1 graph is NOT compiled into
// taskfabric — it is a capability catalog, not an execution plan.
//
// Returns (nil, errNoToolSchemas) when the binder exposes no tools.
func buildToolClassDAG(schemas []core_tools.ToolSchema) (*engine.MutableDAG, error) {
	if len(schemas) == 0 {
		return nil, errNoToolSchemas
	}

	steps := make([]*engine.Step, 0, len(schemas))
	seen := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		if s.Name == "" {
			continue
		}
		nodeID := core_tools.ToolClassID(s.Name, core_tools.ToolArgShape(s))
		if seen[nodeID] {
			continue // defensive: same tool+shape deduplicated
		}
		seen[nodeID] = true
		step := &engine.Step{
			ID:        nodeID,
			Name:      s.Name,
			AgentType: "tool/" + s.Name,
			Input:     s.Description,
			Metadata: map[string]string{
				l1MetaEnabled: "true",
				l1MetaBudget:  "0", // 0 = unlimited
				l1MetaPrior:   "",
			},
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, errNoToolSchemas
	}

	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		return nil, fmt.Errorf("build toolclass DAG: %w", err)
	}
	return dag, nil
}
