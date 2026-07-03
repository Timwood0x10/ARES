package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	ares_runtime "github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/adapter"
	"github.com/Timwood0x10/ares/internal/monitoring/data"
	"github.com/Timwood0x10/ares/internal/monitoring/tabs"
	"github.com/spf13/cobra"
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
		return fmt.Errorf("load config: %w", err)
	}
	if err := ares_config.LoadFromEnv(cfg); err != nil {
		return fmt.Errorf("load env: %w", err)
	}

	if servePort > 0 {
		cfg.Server.Port = servePort
	}

	// --- Context with signal handling ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	// --- Shared event store ---
	store := ares_events.NewMemoryEventStore()

	// --- LLM adapter with fallback ---
	llmAdapter := createLLMAdapterWithFallback(cfg)

	// --- Tool registry (public API) ---
	registry, err := newToolRegistry()
	if err != nil {
		return fmt.Errorf("create tool registry: %w", err)
	}

	// --- MCP servers via bootstrap ---
	internalReg, err := setupMCP(ctx, cfg, registry)
	if err != nil {
		return fmt.Errorf("MCP setup: %w", err)
	}

	// --- ToolBinder for agents ---
	toolBinder := newToolBinder(internalReg)
	log.Printf("tools registered: %d", len(toolBinder.ListTools()))

	// --- ChatClient for native tool calling ---
	chatClient, err := createChatClient(cfg)
	if err != nil {
		return fmt.Errorf("create chat client: %w", err)
	}
	log.Printf("chat client created: provider=%s model=%s", cfg.LLM.Provider, cfg.LLM.Model)

	// --- Memory manager ---
	memConfig := memory.DefaultMemoryConfig()
	memMgr, err := memory.NewMemoryManager(memConfig)
	if err != nil {
		return fmt.Errorf("create memory manager: %w", err)
	}
	memMgr.SetEventStore(store, "memory")

	// --- Create agents ---
	leaderAgent, subAgents := createAgents(cfg, llmAdapter, chatClient, toolBinder, memMgr, store)

	// --- Runtime manager ---
	mgr := ares_runtime.New(nil, store, nil)

	leaderFactory := func() base.Agent {
		a, _ := createLeaderAgent(cfg, llmAdapter, chatClient, toolBinder, memMgr, store)
		return a
	}
	mgr.RegisterAgent(leaderAgent, leaderFactory)

	for _, sa := range subAgents {
		subAgent := sa
		subFactory := func() base.Agent {
			_, subs := createAgents(cfg, llmAdapter, chatClient, toolBinder, memMgr, store)
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
	go bridgeEvents(ctx, store, bus, meta)

	// --- Start runtime ---
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("start runtime: %w", err)
	}

	// --- Submit real tasks ---
	go submitTasks(ctx, leaderAgent)

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

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		return err
	}
	return nil
}

// createLLMAdapterWithFallback creates an LLM adapter with fallback chain.
func createLLMAdapterWithFallback(cfg *ares_config.Config) output.LLMAdapter {
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
		return adapter
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
			return adapter
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
		log.Fatalf("no LLM adapter available: %v", err)
	}
	log.Printf("LLM fallback to ollama: model=llama3.2")
	return adapter
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
