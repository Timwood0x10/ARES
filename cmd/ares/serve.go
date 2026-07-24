package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
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

	api_tools "github.com/Timwood0x10/ares/api/tools"
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

	// --- Context with signal handling ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownMgr, sigCh := setupSignalHandler()

	// Declare httpSrv here so the signal handler below can reference it.
	// The actual server is constructed after bootstrap/agent setup is complete.
	var httpSrv *http.Server

	g, ctx := errgroup.WithContext(ctx)

	// Add signal handler goroutine to the main errgroup.
	g.Go(func() error {
		select {
		case <-sigCh:
			fmt.Println("\nShutting down...")
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()
			if err := shutdownMgr.StartShutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "graceful shutdown error: %v\n", err)
			}
			cancel()
		case <-ctx.Done():
		}
		return nil
	})

	// --- Bootstrap: infrastructure components via single wiring hub ---
	// Uses internal/ares_bootstrap for EventStore, Runtime, Memory.
	// MCP setup is handled separately below for registry bridging.
	comp, err := ares_bootstrap.Bootstrap(ctx, cfg, nil)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	store := comp.EventStore
	memMgr := comp.Memory
	mgr := comp.Runtime

	// Attach event store to memory for event-driven memory operations
	memMgr.SetEventStore(store, "memory")

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

	// --- MCP servers via ares_bootstrap.SetupMCP (handles registry bridging) ---
	internalReg, err := setupMCP(ctx, cfg, registry)
	if err != nil {
		return fmt.Errorf("MCP setup: %w", err)
	}

	// Register AKF (Knowledge Fabric) tools into the internal registry using
	// the shared KnowledgeRuntime from bootstrap.
	registerAKFTools(comp, internalReg)

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
	strategySrc := ares_bootstrap.NewStrategySource(comp.NewEvolution.StrategyStore)
	leaderAgent, subAgents, err := createAgents(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc, comp.KnowledgeRetriever)
	if err != nil {
		return fmt.Errorf("create agents: %w", err)
	}

	// Register agents with runtime manager (from Bootstrap)
	leaderFactory := func() base.Agent {
		a, _ := createLeaderAgent(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc, comp.KnowledgeRetriever)
		return a
	}
	mgr.RegisterAgent(leaderAgent, leaderFactory)

	for _, sa := range subAgents {
		subAgent := sa
		subFactory := func() base.Agent {
			_, subs, _ := createAgents(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc, comp.KnowledgeRetriever)
			for _, s := range subs {
				if s.ID() == subAgent.ID() {
					return s
				}
			}
			return subAgent
		}
		mgr.RegisterAgent(subAgent, subFactory)
	}

	// --- PluginBus + MonitorPlugin ---
	plugin, bus := setupMonitorPlugin(mgr, registry)
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

	// --- Start runtime ---
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("start runtime: %w", err)
	}

	// Inject the live agent DAGs into the evolution system's executors,
	// replacing the synthetic placeholder DAG created at bootstrap time.
	// This ensures workflow/scheduler/recovery patches hit real runtime state.
	injectLiveDAGs(comp, mgr, leaderAgent)

	// --- Submit real tasks ---
	g.Go(func() error {
		submitTasks(ctx, leaderAgent)
		return nil
	})

	// --- HTTP server ---
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	fmt.Println("=== ARES Console — Live Runtime ===")
	fmt.Printf("Console:  http://localhost%s/console/\n", addr)
	fmt.Printf("LLM:      %s / %s\n", cfg.LLM.Provider, cfg.LLM.Model)
	fmt.Printf("Tools:    %v\n", toolBinder.ListTools())
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	server := monitoring.NewHTTPServer(plugin)
	handler := &actionHandler{inner: server, mgr: mgr, tools: registry}

	httpSrv = &http.Server{
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
		if httpSrv != nil {
			return httpSrv.Shutdown(ctx)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("register http shutdown hook: %w", err)
	}
	if err := shutdownMgr.AddCallback(ares_shutdown.PhaseGraceful, comp.MCP.Stop); err != nil {
		return fmt.Errorf("register mcp shutdown hook: %w", err)
	}
	if err := shutdownMgr.AddCallback(ares_shutdown.PhaseGraceful, func(ctx context.Context) error {
		return mgr.Stop()
	}); err != nil {
		return fmt.Errorf("register runtime shutdown hook: %w", err)
	}

	// Wait for all goroutines to complete (signal handler, bridge, tasks, HTTP).
	return g.Wait()
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

// liveDAGPatchExecutor is a patch.RuntimeComponent that directly mutates the
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

func newLiveDAGPatchExecutor(mgr *ares_runtime.Manager, agentID string) *liveDAGPatchExecutor {
	return &liveDAGPatchExecutor{mgr: mgr, agentID: agentID}
}

func (e *liveDAGPatchExecutor) Name() string { return "live_dag" }

func (e *liveDAGPatchExecutor) Snapshot(_ context.Context) (any, error) {
	return nil, patch.ErrNoSnapshot
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
		return nil, nil //nolint:nilnil // no rollback needed

	case patch.PatchReplaceNode:
		step := &engine.Step{ID: p.Target, Name: p.Target, AgentType: "processor"}
		if err := dag.RemoveNode(ctx, p.Target); err != nil {
			return nil, fmt.Errorf("live DAG: replace (remove) node %s: %w", p.Target, err)
		}
		if err := dag.AddNode(ctx, step); err != nil {
			return nil, fmt.Errorf("live DAG: replace (add) node %s: %w", p.Target, err)
		}
		return nil, nil //nolint:nilnil // no rollback needed

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
		return nil, nil //nolint:nilnil // no rollback needed

	case patch.PatchChangeScheduler:
		// Store the scheduler type on the live DAG so the agent's runtime
		// scheduler selection reads the evolved config instead of the default.
		schedType := fmt.Sprintf("%T", p.Value)
		dag.SchedulerType = schedType
		log.Printf("live DAG: scheduler change for agent %s: %s", e.agentID, schedType)
		return nil, nil //nolint:nilnil // no rollback needed

	default:
		return nil, fmt.Errorf("live DAG executor: unsupported patch type %s", p.Type)
	}
}

// Ensure liveDAGPatchExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*liveDAGPatchExecutor)(nil)

// setupSignalHandler creates the signal handler and shutdown manager.
// Returns the shutdown manager and the signal channel for later goroutine wiring.
func setupSignalHandler() (*ares_shutdown.Manager, chan os.Signal) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	shutdownMgr := ares_shutdown.NewManager(30 * time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhasePreShutdown, 5*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseGraceful, 20*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseForce, 5*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseDone, 1*time.Second)

	return shutdownMgr, sigCh
}

// injectLiveDAGs injects the live agent DAGs into the evolution system's executors,
// replacing the synthetic placeholder DAG created at bootstrap time.
func injectLiveDAGs(comp *ares_bootstrap.Components, mgr *ares_runtime.Manager, leaderAgent base.Agent) {
	if comp.NewEvolution == nil {
		return
	}
	for _, id := range []string{leaderAgent.ID()} {
		dag, ok := mgr.GetAgentDAG(id)
		if !ok || dag == nil {
			continue
		}
		liveDAG, dagOk := dag.(*engine.MutableDAG)
		if !dagOk {
			continue
		}
		// Register a LiveDAGPatchExecutor that directly mutates the
		// agent's live MutableDAG instead of a private noop graph.
		liveExec := newLiveDAGPatchExecutor(mgr, id)
		_ = comp.NewEvolution.PatchReg.RegisterComponent(liveExec)
		_ = comp.NewEvolution.PatchReg.Register("graph.scheduler", liveExec)
		comp.NewEvolution.PatchReg.SetFallback(liveExec)

		if err := comp.NewEvolution.UpdateLiveDAG(liveDAG); err != nil {
			log.Printf("serve: update live DAG failed: agent_id=%s error=%v", id, err)
		}

		if wfGenome, gErr := comp.NewEvolution.GenomeReg.Get("workflow"); gErr == nil {
			if setter, ok := wfGenome.(interface{ SetDAG(*engine.MutableDAG) }); ok {
				setter.SetDAG(liveDAG)
				log.Printf("serve: WorkflowGenome updated with live DAG for agent %s (%d steps)", id, len(liveDAG.Steps()))
			}
		}
	}
	comp.NewEvolution.UpdateLiveKnowledgeRuntime(comp.KnowledgeRuntime)
}

// registerAKFTools registers AKF (Knowledge Fabric) tools into the internal registry.
func registerAKFTools(comp *ares_bootstrap.Components, internalReg *core_tools.Registry) {
	if comp.KnowledgeRuntime == nil {
		return
	}
	akfSvc := akf_mcp.NewAKFService(comp.KnowledgeRuntime, &compiler.DefaultCompiler{})
	for _, akfTool := range akfSvc.Tools() {
		t := akfTool
		adapted := &akfToolAdapter{name: t.Name, desc: t.Description, fn: t.Execute}
		if err := internalReg.Register(adapted); err != nil {
			log.Printf("AKF: failed to register tool %q: %v", t.Name, err)
		}
	}
	log.Printf("AKF tools registered with shared KnowledgeRuntime: %d", len(akfSvc.Tools()))
}

// setupMonitorPlugin creates the monitor plugin with tabs and adapters.
// Returns the plugin and the PluginBus for event bridging.
func setupMonitorPlugin(mgr *ares_runtime.Manager, registry *api_tools.Registry) (*monitoring.MonitorPlugin, *ares_runtime.PluginBus) {
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
	return plugin, bus
}
