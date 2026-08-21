//go:build ignore

// This file is a backup of the original serve_routine.go (HEAD version,
// pre-refactor). It is excluded from builds via the ignore tag; keep it only
// as a reference. Do not edit alongside the live serve_routine.go.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/ares_skills"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/dashboard"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/adapter"
	"github.com/Timwood0x10/ares/internal/monitoring/data"
	"github.com/Timwood0x10/ares/internal/monitoring/tabs"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
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
	return plugin, nil
}

// runAutopilotInjector starts the built-in demo task injector (submitTasks)
// when autopilot is enabled. Off by default so a production `ares serve`
// does not burn LLM quota on synthetic work; enable it for local demos / UI
// development instead of the dedicated `ares demo` console.
func runAutopilotInjector(ctx context.Context, g *errgroup.Group, cfg *ares_config.Config, leaderAgent leader.Agent) {
	if !cfg.Kernel.Autopilot {
		return
	}
	g.Go(func() error {
		submitTasks(ctx, leaderAgent)
		return nil
	})
}

// createAndRegisterServeAgents builds the leader and sub agents, wires the
// GA strategy source into them, and registers them (with resurrection
// factories) on the runtime manager. Extracted from runServe to keep its
// cyclomatic complexity within lint limits.
//
// Deprecated: this is the legacy Leader ON wiring (aresos-agentos-plan C1:
// 废弃 leader-sub), reachable only via kernel.leader_enabled=true. The
// production path is the Peer Agent runtime (createPeerAgents) — a flat set
// of capability agents spawned into the Agent Fabric and scheduled from the
// fabric's live population. This function is retained for gray-scaling and
// must not be extended.
func createAndRegisterServeAgents(
	ctx context.Context,
	cfg *ares_config.Config,
	internalReg *core_tools.Registry,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	comp *ares_bootstrap.Components,
	mgr *ares_runtime.Manager,
) (leader.Agent, []sub.Agent, error) {
	memMgr := comp.Memory
	store := comp.EventStore

	// Wire the Capability Fabric (SkillCatalog) into the memory manager's
	// resident skill block and register its agent-facing tools. The catalog
	// indexes only declared sources (project .ares/skills + user ~/.ares/skills);
	// a failure is logged and serve continues without skills rather than
	// failing startup.
	skillCatalog := wireSkillCatalog(cfg, internalReg, toolBinder, memMgr, comp.MCP)
	// The skill locator closes the design §11 feedback loop on the record side:
	// it pre-fills task.UsedExperienceID with the best-matching skill for the
	// task input, so a task outcome can be attributed to a skill later. nil
	// when the catalog is unavailable (offline mode: tasks just carry no
	// skill association).
	var skillLocator leader.ExperienceLocator
	if skillCatalog != nil {
		skillLocator = func(inputText string) string {
			if rec, ok := skillCatalog.Experience().BestMatch(inputText); ok {
				return rec.Skill
			}
			return ""
		}
	}
	// The outcome recorder closes the record side of the design §11 loop: it
	// subscribes to EventSubTaskResult and persists {skill, task_pattern,
	// success} outcomes into the catalog's Experience store. It is best-effort
	// (failures are logged, never fatal) and decoupled from agent code — it
	// only observes the existing event stream, so it cannot affect task
	// execution or agent behavior.
	if skillCatalog != nil {
		recorder := ares_skills.NewSkillOutcomeRecorder(skillCatalog)
		if startErr := recorder.Start(ctx, comp.EventStore); startErr != nil {
			log.Printf("skill catalog: outcome recorder start failed: %v", startErr)
		}
	}

	// Wire the GA's deployed strategy into live agents so the running agents
	// read the active prompt/params at runtime. When evolution is disabled
	// (comp.NewEvolution == nil) no strategy source is injected, so serve
	// continues without GA strategy guidance.
	var feedbackSvc *experience.FeedbackService
	if comp.Evolution != nil {
		feedbackSvc = comp.Evolution.FeedbackService
	}
	var strategySrc agents.StrategySource
	if comp.NewEvolution != nil {
		strategySrc = ares_bootstrap.NewStrategySource(comp.NewEvolution.StrategyStore)
	}

	leaderAgent, subAgents, kernel, err := createAgents(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc, skillLocator)
	if err != nil {
		return nil, nil, fmt.Errorf("create agents: %w", err)
	}

	// Configure the dual-track kernel per config: the Task Fabric path is the
	// default and starts the scheduler unless kernel.policy == "legacy"
	// (explicit opt-out keeps the leader path live with Task Fabric in shadow;
	// ares-runtime.md P4 D4 gradual cutover). The shared EventStore is passed
	// so the event-driven recovery loop (Kernel Lifecycle pillar) can subscribe
	// to task lifecycle events.
	if kernel != nil && kernel.dual != nil {
		wireKernelPolicy(ctx, cfg, kernel, subAgents, store)
	}

	// Wire the evolution-aware spawn gate (v0.3.0 M2-1): the active evolution
	// strategy's spawn params (spawn.enabled / max_concurrent / preferred
	// capabilities) shape the recovery loop's replacement spawns through the
	// Kernel's Recovery subsystem — "Evolution decides; Kernel enforces".
	// Without an evolution store the gate is skipped and recovery spawns
	// plain, preserving prior behavior.
	if kernel != nil && kernel.recovery != nil && comp.NewEvolution != nil {
		spawner := aresrecovery.NewEvolutionAwareSpawner(
			kernel.agents,
			ares_bootstrap.NewSpawnPolicySource(comp.NewEvolution.StrategyStore),
		)
		kernel.recovery.WithSpawner(spawner)
		log.Printf("serve: evolution spawn gate wired (recovery spawns routed through evolution policy)")
	}

	// Wire the evolution-aware quota manager (v0.3.0 M2-2): the active
	// evolution strategy's quota.budget param replaces the Agent Fabric's
	// resource budget at runtime. A periodic loop pushes the latest policy so
	// a deployed budget takes effect without restarting serve; a nil policy
	// (or no quota param) falls back to the configured kernel resources.
	if kernel != nil && kernel.agents != nil && comp.NewEvolution != nil {
		quotaMgr := aresrecovery.NewEvolutionAwareQuotaManager(
			kernel.agents,
			ares_bootstrap.NewQuotaPolicySource(comp.NewEvolution.StrategyStore, cfg.Kernel.Resources),
		)
		go runKernelQuotaLoop(ctx, quotaMgr, parseKernelLoopConfig(cfg))
		log.Printf("serve: evolution quota manager wired (resource budget follows evolution policy)")
	}

	// Wire the evolution population adapter (P6: Runtime Adaptation — agent
	// population). The active evolution strategy's population.spawn /
	// population.retire params drive the Kernel's spawn/retire primitives
	// through a periodic loop — "Evolution decides; Kernel enforces". Without
	// an evolution store the adapter is skipped and the population is managed
	// manually (or by recovery spawns), preserving prior behavior.
	if kernel != nil && kernel.agents != nil && comp.NewEvolution != nil {
		popAdapter := aresrecovery.NewPopulationAdapter(
			kernel.agents,
			ares_bootstrap.NewPopulationPolicySource(comp.NewEvolution.StrategyStore),
		)
		loopCfg := parseKernelLoopConfig(cfg)
		go runKernelEvolutionLoop(ctx, popAdapter, loopCfg)
		log.Printf("serve: evolution population adapter wired (agent population follows evolution policy)")
	}

	// Feed the shared GlobalTracer from the Task Fabric's lifecycle events
	// (v0.3.0 M4-1): this is the write side of /observability/spans. Without
	// it the tracer stays empty and the dashboard span endpoint returns an
	// empty list despite the wiring.
	go runKernelTraceLoop(ctx, store, comp.Observability.GlobalTracer)

	// Register agents with runtime manager (from Bootstrap)
	leaderFactory := func() base.Agent {
		a, _ := createLeaderAgent(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc, skillLocator, nil)
		return a
	}
	mgr.RegisterAgent(leaderAgent, leaderFactory)

	for _, sa := range subAgents {
		subAgent := sa
		subFactory := func() base.Agent {
			_, subs, _, _ := createAgents(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc, skillLocator)
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

	return leaderAgent, subAgents, nil
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
		// (submitPeerTask). Nil on the legacy leader path (endpoint returns
		// 503 "peer runtime not active").
		kernel: peerKernel,
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

// buildLeaderLiveDAG constructs the leader's real workflow DAG from the
// configured sub-agents: input (leader) → one step per sub-agent → output
// (leader). This replaces the bootstrap synthetic 3-step placeholder so
// workflow/scheduler/recovery evolution patches hit the actual agent topology
// (F04, Stage 8).
//
// Args:
// cfg - fully resolved serve configuration; cfg.Agents.Sub is read.
//
// Returns:
// dag - the live MutableDAG, nil on error.
// err - error if a step ID is empty/duplicate or dependencies are invalid.
func buildLeaderLiveDAG(cfg *ares_config.Config) (*engine.MutableDAG, error) {
	steps := []*engine.Step{
		{ID: "input", Name: "Input", AgentType: cfg.Agents.Leader.ID, Input: "parse input"},
	}
	subIDs := make([]string, 0, len(cfg.Agents.Sub))
	for _, s := range cfg.Agents.Sub {
		stepID := strings.TrimSpace(s.ID)
		if stepID == "" {
			stepID = strings.TrimSpace(s.Type)
		}
		if stepID == "" {
			// Fail-loud instead of silently registering a broken DAG edge.
			return nil, fmt.Errorf("sub-agent has empty ID and empty type")
		}
		steps = append(steps, &engine.Step{
			ID:        stepID,
			Name:      s.Type,
			AgentType: s.Type,
			Input:     stepID,
			DependsOn: []string{"input"},
		})
		subIDs = append(subIDs, stepID)
	}
	// Output step depends on every sub-agent step (or just input when none).
	outputDeps := append([]string{"input"}, subIDs...)
	steps = append(steps, &engine.Step{
		ID:        "output",
		Name:      "Output",
		AgentType: cfg.Agents.Leader.ID,
		Input:     "format",
		DependsOn: outputDeps,
	})
	return engine.NewMutableDAG(steps)
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
// serving entry point before Bootstrap starts any component. Memory is optional
// for the library/bootstrap layer, but the current Leader contract requires a
// MemoryManager, so disabling it here is a configuration error rather than a
// late nil dereference or a no-op substitute.
func validateServeConfig(cfg *ares_config.Config) error {
	if cfg == nil {
		return errors.New("serve: config is required")
	}
	if !cfg.Memory.IsEnabled() {
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
