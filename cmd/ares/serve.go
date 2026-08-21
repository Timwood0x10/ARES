package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_archive"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	akf_mcp "github.com/Timwood0x10/ares/internal/knowledge/mcp"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start full agent monitoring with LLM + MCP + dashboard",
	Long: `Starts the full ARES runtime with leader/sub agents, LLM integration,
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
	servePort       int
	serveLLMURL     string
	serveLLMKey     string
	serveLLMModel   string
	serveAutopilot  bool
)

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVarP(&serveConfigPath, "config", "c", "", "Path to config YAML (optional; use --llm-url instead for minimal setup)")
	serveCmd.Flags().IntVarP(&servePort, "port", "p", 0, "HTTP port for dashboard (overrides config)")
	serveCmd.Flags().StringVar(&serveLLMURL, "llm-url", "", "LLM endpoint URL — minimal setup, no config file needed")
	serveCmd.Flags().StringVar(&serveLLMKey, "llm-api-key", "", "LLM API key (minimal setup)")
	serveCmd.Flags().StringVar(&serveLLMModel, "llm-model", "", "LLM model name (optional, provider default when empty)")
	serveCmd.Flags().BoolVar(&serveAutopilot, "autopilot", false, "Enable the built-in demo task injector (submitTasks); off by default")
}

func runServe() error {
	// --- Config ---
	cfg, err := loadServeConfig()
	if err != nil {
		return err
	}
	if err := validateServeConfig(cfg); err != nil {
		return err
	}
	// --autopilot flag opts into the demo task injector (off by default).
	if serveAutopilot {
		cfg.Kernel.Autopilot = true
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
	g.Go(func() error {
		select {
		case <-sigCh:
			fmt.Println("\nShutting down...")
			// Run the registered shutdown phases (HTTP → MCP → runtime) with a
			// bounded overall timeout. cancel() afterwards stops background
			// goroutines (event bridge, task submission) that wait on ctx.
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()
			if err := shutdownMgr.StartShutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "graceful shutdown error: %v\n", err)
			}
			shutdownSystemRuntime(&compPtr, shutdownCtx)
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
			log.Printf("system_runtime snapshot (shutdown): %s", string(snapJSON))
		}
		// Wait for Bootstrap's background goroutines (distillation subscriber,
		// GA evolution ticker, LLM suggestion ticker) to exit after the
		// context is cancelled, so none outlives the graceful shutdown.
		comp.WaitBackground()
		return nil
	})

	// --- EventStore (archive-enabled, shared pipeline) ---
	// Build the archive-enabled store once and inject it into Bootstrap so
	// `ares serve` uses the same construction path as `ares start`
	// (ares_archive.NewCompactableStoreWithArchive is the single source).
	// Archive defaults to on; disable via memory.archive.enabled: false.
	// The raw *MemoryEventStore is unused here — serve consumes the store via
	// the EventStore interface only — so it is discarded.
	compactableStore, _, err := ares_archive.NewCompactableStoreWithArchive(cfg.Memory.Archive)
	if err != nil {
		return fmt.Errorf("create event store: %w", err)
	}

	// --- Bootstrap: infrastructure components via single wiring hub ---
	// Uses internal/ares_bootstrap for EventStore, Runtime, Memory.
	// MCP setup is handled separately below for registry bridging. The store
	// is passed via deps so Bootstrap wires Runtime/Memory against the real
	// archive-enabled store instead of creating a throwaway MemoryEventStore.
	comp, err := ares_bootstrap.Bootstrap(ctx, cfg, &ares_bootstrap.BootstrapDeps{
		EventStore: compactableStore,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	// Publish the assembled components to the signal goroutine via the atomic
	// pointer so the shutdown snapshot/WaitBackground reads never race.
	compPtr.Store(comp)
	store := comp.EventStore
	mgr := comp.Runtime

	// --- Runtime config store + hot-reload watcher (P1) ---
	// The store holds the last-good config and its reload history, served via
	// /runtime/config on the console HTTP server. When the serve command was
	// started with an explicit config file, an fsnotify watcher hot-reloads it
	// on change (failed reloads keep the previous config). With no config file
	// (minimal --llm-url mode) the watcher is skipped — the store still serves
	// the effective config snapshot.
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

	// Stage 3 fix (B01): EventStore is wired into Memory during Bootstrap,
	// not post-Bootstrap here. validateServeConfig has already enforced that
	// the full agent-serving entry point has its required Memory component.

	// Stage 1 observability: report the System Runtime component snapshot
	// (names, modes, lifecycle states) so operators can confirm which
	// components were assembled and reached Ready at startup.
	if snapJSON, snapErr := comp.Snapshot().JSON(); snapErr == nil {
		log.Printf("system_runtime snapshot (startup): %s", string(snapJSON))
	} else {
		log.Printf("system_runtime snapshot unavailable: %v", snapErr)
	}

	// --- LLM adapter with fallback ---
	llmAdapter, err := createLLMAdapterWithFallback(cfg)
	if err != nil {
		return fmt.Errorf("create llm adapter: %w", err)
	}

	// --- Tool registry (public API) ---
	registry, err := newToolRegistry()
	if err != nil {
		return fmt.Errorf("create tool registry: %w", err)
	}

	// --- MCP servers: reuse the manager started by Bootstrap (single manager,
	// single set of connections; its Stop hook is registered below) and bridge
	// its tools into the internal + public registries. ---
	internalReg, err := setupMCP(ctx, comp.MCP, registry, ares_bootstrap.ToolDepsFromComponents(comp))
	if err != nil {
		return fmt.Errorf("MCP setup: %w", err)
	}

	// Register AKF (Knowledge Fabric) tools into the internal registry using
	// the shared KnowledgeRuntime from bootstrap. This is the critical wiring
	// that makes knowledge genome patches (ChangeBudget/ChangePlanner/
	// ChangeReducer) affect the actual runtime used by the agent's knowledge
	// tools — because both the evolution system's KnowledgePatchExecutor and
	// the agent's AKF tools share the same comp.KnowledgeRuntime instance.
	if comp.KnowledgeRuntime != nil {
		akfSvc := akf_mcp.NewAKFService(comp.KnowledgeRuntime, &compiler.DefaultCompiler{})
		for _, akfTool := range akfSvc.Tools() {
			t := akfTool // capture
			adapted := &akfToolAdapter{name: t.Name, desc: t.Description, fn: t.Execute}
			if err := internalReg.Register(adapted); err != nil {
				log.Printf("AKF: failed to register tool %q: %v", t.Name, err)
			}
		}
		log.Printf("AKF tools registered with shared KnowledgeRuntime: %d", len(akfSvc.Tools()))
	}

	// --- ToolBinder for agents ---
	// Primitive 7 wiring: probe host commands from the ARES_NATIVE_TOOLS
	// allowlist and register them into the internal registry (command -v +
	// --help; security boundary = allowlist only). Registered tools flow into
	// GetLLMTools naturally; SetActiveTools lets the runtime narrow the active
	// subset per task (progressive disclosure), and serve keeps the full set
	// active by default (zero-value behavior, no change to LLM tool injection).
	if err := registerNativeTools(ctx, internalReg); err != nil {
		return fmt.Errorf("register native tools: %w", err)
	}
	toolBinder := newToolBinder(internalReg)
	log.Printf("tools registered: %d", len(toolBinder.ListTools()))

	// --- Capability Planner bridge for agent tool fallback ---
	if bridge := newPlannerBridge(internalReg); bridge != nil {
		toolBinder.WithPlannerBridge(bridge)
		log.Println("planner bridge: attached")
	}

	// --- ChatClient for native tool calling ---
	chatClient, err := createChatClient(cfg)
	if err != nil {
		return fmt.Errorf("create chat client: %w", err)
	}
	log.Printf("chat client created: provider=%s model=%s", cfg.LLM.Provider, cfg.LLM.Model)

	// --- Create + register agents with the runtime manager ---
	leaderAgent, subAgents, peerKernel, err := createAndServeAgents(ctx, cfg, internalReg, llmAdapter, chatClient, toolBinder, comp, mgr)
	if err != nil {
		return err
	}

	// --- Peer registry: enable direct agent-to-agent messaging ---
	// setupPeerRegistry builds the registry AND attaches it to the leader
	// (SetPeerRegistry) when one exists; the registry itself is only needed
	// by downstream wiring that currently does not use it, so discard the
	// handle.
	if _, err := setupPeerRegistry(leaderAgent, subAgents, comp); err != nil {
		return err
	}

	// --- PluginBus + MonitorPlugin (extracted to setupServeMonitoring to keep
	// runServe's cyclomatic complexity within gocyclo's 30 limit) ---
	plugin, err := setupServeMonitoring(ctx, g, cfg, mgr, registry, store)
	if err != nil {
		return err
	}

	// F04 (Stage 8): build the leader's real workflow DAG from the configured
	// sub-agents and register it with the runtime manager BEFORE mgr.Start, so
	// wireEvolutionLiveDAGs binds workflow/scheduler/recovery executors to the
	// live DAG instead of the bootstrap synthetic placeholder.
	if leaderAgent != nil {
		liveDAG, dagErr := buildLeaderLiveDAG(cfg)
		if dagErr != nil {
			return fmt.Errorf("build leader live dag: %w", dagErr)
		}
		mgr.RegisterAgentDAG(leaderAgent.ID(), liveDAG)

		// Inject the live agent DAGs into the evolution system's executors before
		// mgr.Start, replacing the synthetic placeholder DAG created at bootstrap
		// time. The leader's live DAG is now registered above, so the binding is
		// real (F04 closed) rather than a no-op.
		wireEvolutionLiveDAGs(comp, mgr, leaderAgent.ID())
	}

	// --- Start runtime ---
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("start runtime: %w", err)
	}

	// Sub-agents are execution units only (ares-runtime.md: agents are not
	// orchestrated, they are scheduled). The Kernel owns dispatch: in the
	// taskfabric policy the kernelScheduler drives each task through
	// RunQuantum → sub.Agent.ExecuteStep; agents never subscribe to the event
	// stream and self-dispatch (self-dispatch was removed in v0.3.0).

	// --- Submit real tasks (opt-in demo injector; off unless autopilot) ---
	if leaderAgent != nil {
		runAutopilotInjector(ctx, g, cfg, leaderAgent)
	}

	// --- HTTP server + graceful-shutdown hooks (extracted to keep runServe
	// cyclomatic complexity within lint limits) ---
	if _, err := startServeHTTPAndHooks(ctx, g, cfg, cfgStore, plugin, mgr, registry, toolBinder, shutdownMgr, comp, peerKernel); err != nil {
		return err
	}

	// Wait for all goroutines to complete (signal handler, bridge, tasks, HTTP).
	// A context cancellation (SIGINT/SIGTERM → graceful shutdown) surfaces as
	// context.Canceled from the errgroup; that is a NORMAL exit, not an error —
	// normalized to nil so `ares serve` exits 0 on Ctrl-C (code_rules_v2 §3.1).
	return normalizeShutdownErr(g.Wait())
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
