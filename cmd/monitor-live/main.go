// monitor-live: Real agent monitoring with LLM + MCP tools.
//
// Wires real leader + sub agents with:
//   - Real LLM (sensenova → stepfun → ollama fallback chain)
//   - Real MCP tools (codegraph + codebase-memory-mcp)
//   - Monitoring dashboard showing live activity
//
// Usage:
//
//	go run ./cmd/monitor-live
//	LLM_API_KEY=sk-xxx LLM_MODEL=gpt-4o go run ./cmd/monitor-live
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	ares_runtime "github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/adapter"
	"github.com/Timwood0x10/ares/internal/monitoring/data"
	"github.com/Timwood0x10/ares/internal/monitoring/tabs"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func main() {
	// --- Config ---
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		// Try project root first, then CWD
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
		log.Fatalf("load config: %v", err)
	}
	if err := ares_config.LoadFromEnv(cfg); err != nil {
		log.Fatalf("load env: %v", err)
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

	// --- MCP servers (codegraph + codebase-memory-mcp) ---
	registry := core.NewRegistry()
	registerBuiltinTools(registry)
	setupMCP(ctx, cfg, registry)

	// --- ToolBinder: built-in tools + MCP tools ---
	toolBinder := newToolBinder()
	bridgeRegistryToToolBinder(toolBinder, registry)
	slog.Info("tools registered", "count", len(toolBinder.ListTools()), "tools", toolBinder.ListTools())

	// --- Memory manager (required by leader) ---
	memConfig := memory.DefaultMemoryConfig()
	memMgr, err := memory.NewMemoryManager(memConfig)
	if err != nil {
		log.Fatalf("create memory manager: %v", err)
	}
	memMgr.SetEventStore(store, "memory")

	// --- Create agents ---
	leaderAgent, subAgents := createAgents(cfg, llmAdapter, toolBinder, memMgr, store)

	// --- Runtime manager ---
	mgr := ares_runtime.New(nil, store, nil)

	// Register leader
	leaderFactory := func() base.Agent {
		a, _ := createLeaderAgent(cfg, llmAdapter, toolBinder, memMgr, store)
		return a
	}
	mgr.RegisterAgent(leaderAgent, leaderFactory)

	// Register sub agents
	for _, sa := range subAgents {
		subAgent := sa
		subFactory := func() base.Agent {
			_, subs := createAgents(cfg, llmAdapter, toolBinder, memMgr, store)
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

	// Runtime adapter: wires kill/resume/retry buttons to real runtime
	rtAdapter := adapter.NewRuntimeAdapter(&runtimeAdapterShim{mgr})

	// MCP adapter: exposes tools via /api/mcp/tools endpoint
	mcpMgr := newMCPAdapter(registry)

	plugin := monitoring.NewConsole(
		monitoring.WithAgentTracker(tracker),
		monitoring.WithTraceLinkerOption(linker),
		monitoring.WithTabMap(tabMap),
		monitoring.WithRuntimeManager(rtAdapter),
		monitoring.WithMCP(mcpMgr),
	).(*monitoring.MonitorPlugin)

	if err := plugin.Start(ctx, bus); err != nil {
		log.Fatalf("start monitor plugin: %v", err)
	}

	// --- Bridge: EventStore → PluginBus (with agent metadata enrichment) ---
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
		log.Fatalf("start runtime: %v", err)
	}

	// --- Submit real tasks ---
	go submitTasks(ctx, leaderAgent)

	// --- HTTP server with real kill/resume/retry ---
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	fmt.Println("=== ARES Console — Live Runtime ===")
	fmt.Printf("Console:  http://localhost%s/console/\n", addr)
	fmt.Printf("LLM:      %s / %s\n", cfg.LLM.Provider, cfg.LLM.Model)
	fmt.Printf("Tools:    %v\n", toolBinder.ListTools())
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	server := monitoring.NewHTTPServer(plugin)
	handler := &actionHandler{inner: server, mgr: mgr}

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// createLLMAdapterWithFallback creates an LLM adapter with fallback chain.
// Tries primary → fallbacks → ollama (local).
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
		slog.Info("LLM adapter created", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)
		return adapter
	}
	slog.Warn("primary LLM failed, trying fallbacks", "error", err)

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
			slog.Info("LLM fallback adapter created", "provider", fbCfg.Provider, "model", fbCfg.Model)
			return adapter
		}
		slog.Warn("fallback LLM failed", "provider", fbCfg.Provider, "error", err)
	}

	// Last resort: ollama local
	slog.Warn("all remote LLMs failed, falling back to local ollama")
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
	slog.Info("LLM fallback to ollama", "model", "llama3.2")
	return adapter
}

// setupMCP connects to MCP servers and registers their tools in the registry.
func setupMCP(ctx context.Context, cfg *ares_config.Config, registry *core.Registry) {
	if len(cfg.MCP.Servers) == 0 {
		slog.Info("no MCP servers configured")
		return
	}

	mcpMgr, err := ares_bootstrap.SetupMCP(ctx, &cfg.MCP, registry)
	if err != nil {
		slog.Warn("MCP setup failed", "error", err)
		return
	}
	if mcpMgr != nil {
		slog.Info("MCP manager started", "servers", len(cfg.MCP.Servers))
	}
}

// runtimeAdapterShim adapts ares_runtime.Manager to adapter.RuntimeManager.
// The two packages define AgentInfo with the same fields but different types,
// so this shim bridges them.
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
