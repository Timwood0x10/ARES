package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_archive"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	ares_runtime "github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/dashboard"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	akf_mcp "github.com/Timwood0x10/ares/internal/knowledge/mcp"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/adapter"
	"github.com/Timwood0x10/ares/internal/monitoring/data"
	"github.com/Timwood0x10/ares/internal/monitoring/tabs"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start full agent monitoring with LLM + MCP + dashboard",
	Long: `Starts the full ARES runtime with leader/sub agents, LLM integration,
MCP tools, and the monitoring dashboard.

Flags:
  --config  Path to config YAML (default: cmd/monitor-live/config.yaml)
  --port    HTTP port for dashboard (overrides config)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServe()
	},
}

var (
	serveConfigPath string
	servePort       int
)

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVarP(&serveConfigPath, "config", "c", "", "Path to config YAML")
	serveCmd.Flags().IntVarP(&servePort, "port", "p", 0, "HTTP port for dashboard (overrides config)")
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
	memMgr := comp.Memory
	mgr := comp.Runtime

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

	// --- Create agents ---
	var feedbackSvc *experience.FeedbackService
	if comp.Evolution != nil {
		feedbackSvc = comp.Evolution.FeedbackService
	}
	// Wire the GA's deployed strategy into live agents so the running
	// agents read the active prompt/params at runtime. When evolution is
	// disabled (comp.NewEvolution == nil) no strategy source is injected,
	// so serve continues without GA strategy guidance.
	var strategySrc agents.StrategySource
	if comp.NewEvolution != nil {
		strategySrc = ares_bootstrap.NewStrategySource(comp.NewEvolution.StrategyStore)
	}
	leaderAgent, subAgents, err := createAgents(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc)
	if err != nil {
		return fmt.Errorf("create agents: %w", err)
	}

	// Register agents with runtime manager (from Bootstrap)
	leaderFactory := func() base.Agent {
		a, _ := createLeaderAgent(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc)
		return a
	}
	mgr.RegisterAgent(leaderAgent, leaderFactory)

	for _, sa := range subAgents {
		subAgent := sa
		subFactory := func() base.Agent {
			_, subs, _ := createAgents(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc)
			for _, s := range subs {
				if s.ID() == subAgent.ID() {
					return s
				}
			}
			log.Printf("ERROR: sub-agent factory: agent %q not found in live pool, resurrection impossible",
				subAgent.ID())
			return nil // returning nil prevents resurrection with a dead agent
		}
		mgr.RegisterAgent(subAgent, subFactory)
	}

	// --- PluginBus + MonitorPlugin ---
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
	).(*monitoring.MonitorPlugin)

	if err := plugin.Start(ctx, bus); err != nil {
		return fmt.Errorf("start monitor plugin: %w", err)
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
	meta := map[string]agentMeta{
		cfg.Agents.Leader.ID: {name: cfg.Agents.Leader.ID, role: "orchestrator", model: cfg.LLM.Model},
	}
	for _, s := range cfg.Agents.Sub {
		meta[s.ID] = agentMeta{
			name:     s.ID,
			role:     s.Category,
			model:    cfg.LLM.Model,
			parentID: cfg.Agents.Leader.ID,
		}
	}
	g.Go(func() error {
		bridgeEvents(ctx, store, bus, meta)
		return nil
	})

	// Attempt to inject the live agent DAGs into the evolution system's
	// executors before mgr.Start, replacing the synthetic placeholder DAG
	// created at bootstrap time. NOTE: the leader's live DAG is not yet
	// registered anywhere (only the synthetic "evolution" DAG exists), so
	// this is currently a no-op that logs a warning — the F04 gap remains
	// open until a live DAG supply chain is wired (Track C, deferred).
	wireEvolutionLiveDAGs(comp, mgr, leaderAgent.ID())

	// --- Start runtime ---
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("start runtime: %w", err)
	}

	// --- Submit real tasks ---
	g.Go(func() error {
		submitTasks(ctx, leaderAgent)
		return nil
	})

	// --- HTTP server + graceful-shutdown hooks (extracted to keep runServe
	// cyclomatic complexity within lint limits) ---
	if _, err := startServeHTTPAndHooks(ctx, g, cfg, plugin, mgr, registry, toolBinder, shutdownMgr, comp); err != nil {
		return err
	}

	// Wait for all goroutines to complete (signal handler, bridge, tasks, HTTP).
	return g.Wait()
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
	plugin *monitoring.MonitorPlugin,
	mgr *ares_runtime.Manager,
	registry *api_tools.Registry,
	toolBinder sub.ToolBinder,
	shutdownMgr *ares_shutdown.Manager,
	comp *ares_bootstrap.Components,
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
	if serveAPIKey != "" {
		server = monitoring.NewHTTPServer(plugin, monitoring.WithAPIKey(serveAPIKey))
	}
	handler := &actionHandler{inner: server, mgr: mgr, tools: registry, apiKey: serveAPIKey}

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
	// are initialized. Each hook performs a real teardown (no no-ops).
	if err := shutdownMgr.AddCallback(ares_shutdown.PhasePreShutdown, func(ctx context.Context) error {
		return httpSrv.Shutdown(ctx)
	}); err != nil {
		return nil, fmt.Errorf("register http shutdown hook: %w", err)
	}
	if err := shutdownMgr.AddCallback(ares_shutdown.PhaseGraceful, comp.MCP.Stop); err != nil {
		return nil, fmt.Errorf("register mcp shutdown hook: %w", err)
	}
	if err := shutdownMgr.AddCallback(ares_shutdown.PhaseGraceful, func(ctx context.Context) error {
		return mgr.Stop()
	}); err != nil {
		return nil, fmt.Errorf("register runtime shutdown hook: %w", err)
	}
	// Stop the flight recorder's collector goroutine explicitly on graceful
	// shutdown. It is also safe when Bootstrap built no recorder (nil guard):
	// the collector exits promptly because Stop cancels its internal context
	// before waiting on the loop goroutine.
	if comp.FlightRecorder != nil {
		if err := shutdownMgr.AddCallback(ares_shutdown.PhaseGraceful, func(ctx context.Context) error {
			comp.FlightRecorder.Stop()
			return nil
		}); err != nil {
			return nil, fmt.Errorf("register flight recorder shutdown hook: %w", err)
		}
	}

	return httpSrv, nil
}

// wireEvolutionLiveDAGs injects the live agent DAGs into the evolution
// system's executors, replacing the synthetic placeholder DAG created at
// bootstrap time. This ensures workflow/scheduler/recovery patches hit real
// runtime state. Extracted from runServe to keep its cyclomatic complexity
// within lint limits.
func wireEvolutionLiveDAGs(comp *ares_bootstrap.Components, mgr *ares_runtime.Manager, leaderID string) {
	if comp.NewEvolution == nil {
		return
	}
	for _, id := range []string{leaderID} {
		dag, ok := mgr.GetAgentDAG(id)
		if !ok || dag == nil {
			// Fail-loud: no live DAG is registered for this agent (the live
			// DAG supply chain is Track C, deferred), so workflow/scheduler/
			// recovery patches still hit synthetic executors. The warning is
			// expected on every startup until a live DAG is wired.
			log.Printf("serve: live DAG not registered for agent %q before Start; "+
				"workflow patches will hit synthetic executors (F04 gap, Track C deferred)", id)
			continue
		}
		liveDAG, dagOk := dag.(*engine.MutableDAG)
		if !dagOk {
			continue
		}
		// Register a LiveDAGPatchExecutor that directly mutates the agent's
		// live MutableDAG instead of a private noop graph.
		liveExec := newLiveDAGPatchExecutor(mgr, id)
		// Register as component AND as fallback so workflow structure patches
		// (insert/remove nodes/edges) with dynamic node ID targets are routed
		// to the live DAG executor.
		if err := comp.NewEvolution.PatchReg.RegisterComponent(liveExec); err != nil {
			log.Printf("serve: register live exec component: %v", err)
		}
		if err := comp.NewEvolution.PatchReg.Register("graph.scheduler", liveExec); err != nil {
			log.Printf("serve: register live exec graph.scheduler: %v", err)
		}
		comp.NewEvolution.PatchReg.SetFallback(liveExec)

		// Also update the existing graph executor for consistency.
		if err := comp.NewEvolution.UpdateLiveDAG(liveDAG); err != nil {
			log.Printf("serve: update live DAG failed: agent_id=%s error=%v", id, err)
		}

		// Update the WorkflowGenome's DAG reference so its evolution mutations
		// are based on the agent's real workflow topology instead of the
		// bootstrap 3-step placeholder. Without this, the genome generates
		// patches against the toy structure, so the content being evolved is
		// disconnected from reality.
		wfGenome, gErr := comp.NewEvolution.GenomeReg.Get("workflow")
		if gErr != nil {
			continue
		}
		setter, ok := wfGenome.(interface{ SetDAG(*engine.MutableDAG) })
		if !ok {
			continue
		}
		setter.SetDAG(liveDAG)
		log.Printf("serve: WorkflowGenome updated with live DAG for agent %s (%d steps)", id, len(liveDAG.Steps()))
	}
	// Replace the evolution system's isolated KnowledgeRuntime with the
	// agent's live KnowledgeRuntime. This ensures knowledge genome patches
	// (ChangeBudget/ChangePlanner/ChangeReducer) affect the actual runtime
	// used by the agent's knowledge tools, not the bootstrap placeholder.
	comp.NewEvolution.UpdateLiveKnowledgeRuntime(comp.KnowledgeRuntime)
}

// loadServeConfig resolves the config path (falling back to the bundled
// monitor-live config), loads it, applies environment overrides, and applies
// the --port flag. Extracted from runServe to keep its cyclomatic complexity
// within lint limits.
func loadServeConfig() (*ares_config.Config, error) {
	configPath := serveConfigPath
	if configPath == "" {
		for _, p := range []string{
			"cmd/monitor-live/config.yaml",
			"./cmd/monitor-live/config.yaml",
		} {
			if _, err := os.Stat(p); err == nil {
				configPath = p
				break
			}
		}
		if configPath == "" {
			configPath = "cmd/monitor-live/config.yaml"
		}
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
// serving entry point before Bootstrap starts any component. Memory is optional
// for the library/bootstrap layer, but the current Leader contract requires a
// MemoryManager, so disabling it here is a configuration error rather than a
// late nil dereference or a no-op substitute.
func validateServeConfig(cfg *ares_config.Config) error {
	if cfg == nil {
		return errors.New("serve: config is required")
	}
	if !cfg.Memory.Enabled {
		return errors.New("serve: memory.enabled must be true because the leader agent requires the Memory component")
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
		return nil, fmt.Errorf("no LLM adapter available: %w", err)
	}
	log.Printf("LLM fallback to ollama: model=llama3.2")
	return adapter, nil
}

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

// akfToolAdapter adapts an AKF MCP tool (func(ctx, input string) -> string)
// to the core_tools.Tool interface so it can be registered in the internal
// tool registry and used by sub-agents through the ToolBinder. This is the
// wiring that makes knowledge genome patches affect the agent's knowledge
// tools — because both share the same comp.KnowledgeRuntime instance.
type akfToolAdapter struct {
	name string
	desc string
	fn   func(ctx context.Context, input string) (string, error)
}

func (a *akfToolAdapter) Name() string                      { return a.name }
func (a *akfToolAdapter) Description() string               { return a.desc }
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

// liveDAGPatchExecutor applies workflow structure patches directly to the
// agent's live engine.MutableDAG held by the runtime manager. Unlike the
// synthetic GraphPatchExecutor (which operates on a private noop *wfgraph.Graph),
// this executor reads the live DAG from the manager's dagStore, applies the
// mutation, and writes it back — so genome evolution patches to workflow
// structure (insert/remove nodes/edges) actually change the DAG the agent
// reads at runtime.
type liveDAGPatchExecutor struct {
	mgr     *ares_runtime.Manager
	agentID string
}

// errNoSnapshot is returned by Snapshot to signal that this executor does
// not produce a serializable snapshot. Callers should treat it as "no diff
// available" rather than a real failure.
var errNoSnapshot = errors.New("live DAG executor: snapshot not supported")

// errNoRollback is returned by Apply when the patch succeeds but produces no
// rollback patch (the operation is its own inverse or is irreversible).
var errNoRollback = errors.New("live DAG executor: no rollback patch")

func newLiveDAGPatchExecutor(mgr *ares_runtime.Manager, agentID string) *liveDAGPatchExecutor {
	return &liveDAGPatchExecutor{mgr: mgr, agentID: agentID}
}

func (e *liveDAGPatchExecutor) Name() string { return "live_dag" }

func (e *liveDAGPatchExecutor) Snapshot(_ context.Context) (any, error) {
	return nil, errNoSnapshot
}

func (e *liveDAGPatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	// All patch types that GraphPatchExecutor supports are supported here.
	switch p.Type {
	case patch.PatchInsertNode, patch.PatchRemoveNode,
		patch.PatchReplaceNode, patch.PatchAddEdge,
		patch.PatchRemoveEdge, patch.PatchChangeScheduler:
		return nil
	default:
		return fmt.Errorf("live DAG executor: unsupported patch type %s", p.Type)
	}
}

func (e *liveDAGPatchExecutor) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	dagAny, ok := e.mgr.GetAgentDAG(e.agentID)
	if !ok || dagAny == nil {
		return nil, fmt.Errorf("live DAG executor: no DAG for agent %s", e.agentID)
	}
	dag, dagOk := dagAny.(*engine.MutableDAG)
	if !dagOk || dag == nil {
		return nil, fmt.Errorf("live DAG executor: DAG for agent %s is not a MutableDAG", e.agentID)
	}

	switch p.Type {
	case patch.PatchInsertNode:
		step := &engine.Step{ID: p.Target, Name: p.Target, AgentType: "processor"}
		if err := dag.AddNode(ctx, step); err != nil {
			return nil, fmt.Errorf("live DAG: insert node %s: %w", p.Target, err)
		}
		return &patch.RuntimePatch{
			Type:   patch.PatchRemoveNode,
			Target: p.Target,
			Reason: "rollback: remove inserted node",
		}, nil

	case patch.PatchRemoveNode:
		if err := dag.RemoveNode(ctx, p.Target); err != nil {
			return nil, fmt.Errorf("live DAG: remove node %s: %w", p.Target, err)
		}
		return nil, errNoRollback

	case patch.PatchReplaceNode:
		step := &engine.Step{ID: p.Target, Name: p.Target, AgentType: "processor"}
		if err := dag.RemoveNode(ctx, p.Target); err != nil {
			return nil, fmt.Errorf("live DAG: replace (remove) node %s: %w", p.Target, err)
		}
		if err := dag.AddNode(ctx, step); err != nil {
			return nil, fmt.Errorf("live DAG: replace (add) node %s: %w", p.Target, err)
		}
		return nil, errNoRollback

	case patch.PatchAddEdge:
		val, ok := p.Value.(map[string]string)
		if !ok {
			return nil, fmt.Errorf("live DAG: AddEdge value must be map[string]string")
		}
		from, to := val["from"], val["to"]
		if err := dag.AddEdge(ctx, from, to); err != nil {
			return nil, fmt.Errorf("live DAG: add edge %s→%s: %w", from, to, err)
		}
		return &patch.RuntimePatch{
			Type:   patch.PatchRemoveEdge,
			Value:  map[string]string{"from": from, "to": to},
			Reason: "rollback: remove added edge",
		}, nil

	case patch.PatchRemoveEdge:
		val, ok := p.Value.(map[string]string)
		if !ok {
			return nil, fmt.Errorf("live DAG: RemoveEdge value must be map[string]string")
		}
		from, to := val["from"], val["to"]
		if err := dag.RemoveEdge(ctx, from, to); err != nil {
			return nil, fmt.Errorf("live DAG: remove edge %s→%s: %w", from, to, err)
		}
		return nil, errNoRollback

	case patch.PatchChangeScheduler:
		// Store the scheduler type on the live DAG so the agent's runtime
		// scheduler selection reads the evolved config instead of the default.
		schedType := fmt.Sprintf("%T", p.Value)
		dag.SchedulerType = schedType
		log.Printf("live DAG: scheduler change for agent %s: %s", e.agentID, schedType)
		return nil, errNoRollback

	default:
		return nil, fmt.Errorf("live DAG executor: unsupported patch type %s", p.Type)
	}
}

// Ensure liveDAGPatchExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*liveDAGPatchExecutor)(nil)
