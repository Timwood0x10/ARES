// package main — MCP code review service with unified API.
//
// API:
//
//	GET  /agents          — list all agents
//	POST /agents          — create & launch agent
//	GET  /agents/{id}     — agent detail + full LLM analysis
//	GET  /mcp             — list MCP servers with tools
//	GET  /mcp/{name}      — server detail
//	GET  /ws              — WebSocket for real-time updates
//	GET  /                — system overview
//
// Usage:
//
//	go run . -config ./config.yaml -target /path/to/project
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"goagentx/internal/dashboard"
	"goagentx/internal/events"
	"goagentx/internal/flight"
	"goagentx/internal/llm/output"
	"goagentx/internal/mcp"

	"github.com/google/uuid"

	"gopkg.in/yaml.v3"
)

func main() {
	var (
		configPath string
		targetDir  string
		interval   int
	)
	flag.StringVar(&configPath, "config", "./examples/mcp-dashboard/config.yaml", "Config file")
	flag.StringVar(&targetDir, "target", ".", "Project to analyze")
	flag.IntVar(&interval, "interval", 300, "Review interval in seconds (0 = no periodic)")
	flag.Parse()

	cfg, err := loadConfig(configPath)
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	addr := cfg.Dashboard.Addr
	if addr == "" {
		addr = ":8090"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── LLM ────────────────────────────────────
	slog.Info("connecting LLM", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)
	llmAdapter, err := output.CreateAdapter(ctx, cfg.LLM.Provider, &output.Config{
		Provider: cfg.LLM.Provider, Model: cfg.LLM.Model,
		BaseURL: cfg.LLM.BaseURL, APIKey: cfg.LLM.APIKey,
		MaxTokens: 4096, Timeout: cfg.LLM.Timeout,
	})
	if err != nil {
		slog.Error("LLM init failed", "error", err)
		os.Exit(1)
	}
	if _, err := llmAdapter.Generate(ctx, "Reply OK"); err != nil {
		slog.Error("LLM not reachable — is Ollama running?", "error", err)
		os.Exit(1)
	}
	slog.Info("LLM connected")

	// ── MCP ────────────────────────────────────
	slog.Info("connecting codegraph MCP")
	mcpClient := mcp.NewMCPClient(mcp.MCPClientConfig{
		ServerName: "codegraph", Timeout: 60 * time.Second,
	})
	mcpTransport := mcp.NewStdioTransport(mcp.StdioConfig{
		Command: cfg.MCP.Servers[0].Transport.Stdio.Command,
		Args:    cfg.MCP.Servers[0].Transport.Stdio.Args,
	})
	if err := mcpClient.Connect(ctx, mcpTransport); err != nil {
		slog.Error("MCP connect failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = mcpClient.Close() }()

	tools, _ := mcpClient.ListTools(ctx)
	slog.Info("MCP tools discovered", "count", len(tools))

	// ── Orchestrator ───────────────────────────
	hub := dashboard.NewWSHub()
	go hub.Run()
	defer hub.Stop()

	eventStore := events.NewMemoryEventStore()
	bridge := dashboard.NewEventBridge(eventStore, hub)
	_ = bridge.Start(ctx)
	defer bridge.Stop()

	orch := dashboard.NewOrchestrator(
		&mcpAdapter{client: mcpClient},
		&llmAdapterWrap{adapter: llmAdapter},
	)
	orch.SetTemplates(buildTemplates(tools))
	orch.SetHub(hub)
	orch.SetEventStore(eventStore)

	// ── Flight Recorder ─────────────────────────
	fr := flight.NewFlightRecorder(flight.FlightRecorderConfig{
		EventStore: eventStore,
	})
	if err := fr.Start(ctx); err != nil {
		slog.Error("flight recorder start failed", "error", err)
		os.Exit(1)
	}
	defer fr.Stop()
	orch.SetFlightRecorder(fr)
	slog.Info("flight recorder wired")

	mcpBridge := &mcpStatusBridge{tools: tools}

	// ── Unified API ────────────────────────────
	api := dashboard.NewAPIv2(orch, mcpBridge, hub)
	arenaAdapter := &arenaAdapter{orch: orch, eventStore: eventStore}
	api.SetArena(arenaAdapter)
	httpServer := &http.Server{Addr: addr, Handler: api.Handler()}

	go func() {
		slog.Info("api started", "url", "http://localhost"+addr)
		_ = httpServer.ListenAndServe()
	}()

	// ── Initial review ─────────────────────────
	slog.Info("running initial code review...")
	runReview(orch, tools)

	// ── Periodic review ────────────────────────
	if interval > 0 {
		slog.Info("periodic review", "interval_seconds", interval)
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					slog.Info("periodic review triggered")
					runReview(orch, tools)
				}
			}
		}()
	}

	slog.Info("service running", "api", "http://localhost"+addr)
	slog.Info("try these:")
	slog.Info("  curl http://localhost" + addr + "/agents")
	slog.Info("  curl http://localhost" + addr + "/mcp")
	slog.Info("  curl -X POST http://localhost" + addr + "/agents -d '{\"template_id\":\"tpl-structure\"}'")
	<-ctx.Done()
	_ = httpServer.Shutdown(context.Background())
}

func runReview(orch *dashboard.Orchestrator, tools []mcp.MCPToolDef) {
	m := make(map[string]string)
	for _, t := range tools {
		m[t.Name] = t.Name
		if i := strings.Index(t.Name, "_"); i >= 0 {
			m[t.Name[i+1:]] = t.Name
		}
	}
	r := func(n string) string { return m[n] }

	agents := []dashboard.AgentRequest{
		{
			Name: "Architecture Review",
			Steps: []dashboard.AgentStep{
				{Tool: r("files"), Args: nil},
				{Tool: r("search"), Args: map[string]any{"search": "func main|func New|func Start"}},
				{Tool: r("context"), Args: map[string]any{"task": "analyze package dependencies and module structure"}},
			},
			LLMPrompt: `You are a senior code architect. Analyze this project using the gathered data:
1. Architecture pattern and package organization
2. Dependency flow and coupling issues
3. Entry points and their responsibilities
4. Separation of concerns
5. Specific improvement suggestions

Data:
{{.raw_data}}`,
		},
		{
			Name: "Error Handling Review",
			Steps: []dashboard.AgentStep{
				{Tool: r("search"), Args: map[string]any{"search": "ErrXxx|errors.New|fmt.Errorf"}},
				{Tool: r("context"), Args: map[string]any{"task": "find all error handling: wrapping, sentinel errors, swallowed errors, panic usage"}},
				{Tool: r("callers"), Args: map[string]any{"symbol": "fmt.Errorf"}},
			},
			LLMPrompt: `You are a Go reliability engineer. Review error handling:
1. Are errors wrapped with context (fmt.Errorf %w)?
2. Any swallowed errors (_ = err without logging)?
3. Sentinel errors properly defined?
4. Any panic() in non-init code?
5. Specific violations with file:line

Data:
{{.raw_data}}`,
		},
		{
			Name: "Concurrency Review",
			Steps: []dashboard.AgentStep{
				{Tool: r("search"), Args: map[string]any{"search": "go func|errgroup|sync.Mutex|sync.RWMutex"}},
				{Tool: r("context"), Args: map[string]any{"task": "find all goroutine launches, mutex usage, channel patterns"}},
				{Tool: r("callers"), Args: map[string]any{"symbol": "errgroup.Group"}},
			},
			LLMPrompt: `You are a Go concurrency expert. Review:
1. Bare 'go' without errgroup/WaitGroup?
2. Potential goroutine leaks?
3. Shared state without mutex?
4. Channel misuse risks?
5. Race condition risks with specific locations

Data:
{{.raw_data}}`,
		},
		{
			Name: "API Surface Review",
			Steps: []dashboard.AgentStep{
				{Tool: r("search"), Args: map[string]any{"search": "type.*interface"}},
				{Tool: r("search"), Args: map[string]any{"search": "func New"}},
				{Tool: r("context"), Args: map[string]any{"task": "analyze public API consistency and interface design"}},
			},
			LLMPrompt: `Review the public API surface:
1. Are interfaces small and focused?
2. Is naming consistent across packages?
3. Are constructors following patterns?
4. Breaking change risks?

Data:
{{.raw_data}}`,
		},
		{
			Name: "Change Impact Analysis",
			Steps: []dashboard.AgentStep{
				{Tool: r("impact"), Args: map[string]any{"symbol": "Tool"}},
				{Tool: r("callers"), Args: map[string]any{"symbol": "Tool"}},
				{Tool: r("context"), Args: map[string]any{"task": "find all Tool interface implementations and usages"}},
			},
			LLMPrompt: `Analyze the impact of changing the Tool interface:
1. All implementations that would break
2. Test coverage gaps
3. Migration strategy
4. Risk assessment per package

Data:
{{.raw_data}}`,
		},
	}

	for _, req := range agents {
		id, err := orch.CreateAgent(req)
		if err != nil {
			slog.Error("create agent failed", "name", req.Name, "error", err)
			continue
		}
		slog.Info("agent launched", "id", id, "name", req.Name, "steps", len(req.Steps))
	}
}

// ── Templates ─────────────────────────────────

func buildTemplates(tools []mcp.MCPToolDef) []dashboard.AgentTemplate {
	m := make(map[string]string)
	for _, t := range tools {
		m[t.Name] = t.Name
		if i := strings.Index(t.Name, "_"); i >= 0 {
			m[t.Name[i+1:]] = t.Name
		}
	}
	r := func(n string) string { return m[n] }

	var ts []dashboard.AgentTemplate
	add := func(id, name, desc, tool string, args map[string]any, prompt string) {
		if t := r(tool); t != "" {
			ts = append(ts, dashboard.AgentTemplate{
				ID: id, Name: name, Description: desc,
				MCPTool: t, MCPArgs: args, LLMPrompt: prompt,
			})
		}
	}

	add("tpl-structure", "Architecture Review",
		"Analyze project structure and architecture",
		"files", nil,
		`You are a senior code architect. Analyze this project:
1. Architecture pattern and package organization
2. Dependency flow and coupling issues
3. Separation of concerns
4. Specific improvement suggestions

Data:
{{.raw_data}}`)

	add("tpl-error-review", "Error Handling Review",
		"Review error handling patterns",
		"context", map[string]any{"task": "find all error handling: wrapping, sentinel errors, swallowed errors, panic usage"},
		`You are a Go reliability engineer. Review error handling:
1. Are errors wrapped with context (fmt.Errorf %w)?
2. Any swallowed errors?
3. Sentinel errors properly defined?
4. Any panic() in non-init code?
5. Specific violations with file:line

Data:
{{.raw_data}}`)

	add("tpl-concurrency", "Concurrency Review",
		"Review goroutine safety and concurrency patterns",
		"context", map[string]any{"task": "find all goroutine launches, mutex usage, channel patterns, errgroup usage"},
		`You are a Go concurrency expert. Review:
1. Bare 'go' without errgroup/WaitGroup?
2. Potential goroutine leaks?
3. Shared state without mutex?
4. Channel misuse risks?
5. Race condition risks

Data:
{{.raw_data}}`)

	add("tpl-impact", "Change Impact Analysis",
		"Analyze impact of modifying core interfaces",
		"impact", map[string]any{"symbol": "Tool"},
		`Analyze the impact of changing the Tool interface:
1. All implementations that would break
2. Test coverage gaps
3. Migration strategy
4. Risk assessment per package

Data:
{{.raw_data}}`)

	add("tpl-api", "API Surface Review",
		"Review public API design and consistency",
		"search", map[string]any{"search": "type.*interface|func.*New"},
		`Review the public API surface:
1. Are interfaces small and focused?
2. Is naming consistent?
3. Are constructors following patterns?
4. Breaking change risks?

Data:
{{.raw_data}}`)

	return ts
}

// ── Adapters ──────────────────────────────────

type mcpAdapter struct{ client *mcp.MCPClient }

func (a *mcpAdapter) CallTool(ctx context.Context, name string, args map[string]any) (*dashboard.MCPToolResult, error) {
	r, err := a.client.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	blocks := make([]dashboard.MCPContentBlock, len(r.Content))
	for i, b := range r.Content {
		blocks[i] = dashboard.MCPContentBlock{Type: b.Type, Text: b.Text}
	}
	return &dashboard.MCPToolResult{Content: blocks, IsError: r.IsError}, nil
}

func (a *mcpAdapter) ListTools(ctx context.Context) ([]dashboard.MCPToolInfo, error) {
	tools, err := a.client.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]dashboard.MCPToolInfo, len(tools))
	for i, t := range tools {
		infos[i] = dashboard.MCPToolInfo{Name: t.Name, Description: t.Description}
	}
	return infos, nil
}

type llmAdapterWrap struct{ adapter output.LLMAdapter }

func (w *llmAdapterWrap) Generate(ctx context.Context, prompt string) (string, error) {
	return w.adapter.Generate(ctx, prompt)
}

func (w *llmAdapterWrap) GenerateStream(ctx context.Context, prompt string) (<-chan dashboard.StreamChunk, error) {
	src, err := w.adapter.GenerateStream(ctx, prompt)
	if err != nil {
		return nil, err
	}
	dst := make(chan dashboard.StreamChunk)
	go func() {
		defer close(dst)
		for c := range src {
			dst <- dashboard.StreamChunk{Content: c.Content, Done: c.Done, Err: c.Err}
		}
	}()
	return dst, nil
}

type mcpStatusBridge struct{ tools []mcp.MCPToolDef }

func (b *mcpStatusBridge) ListServers() []dashboard.MCPServerStatusView {
	views := make([]dashboard.MCPToolView, len(b.tools))
	for i, t := range b.tools {
		views[i] = dashboard.MCPToolView{Name: t.Name, Description: t.Description, ServerName: "codegraph"}
	}
	return []dashboard.MCPServerStatusView{{
		Name: "codegraph", Connected: true, ToolCount: len(b.tools), Version: "connected", Tools: views,
	}}
}

// ── Config ────────────────────────────────────

type appConfig struct {
	LLM struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
		BaseURL  string `yaml:"base_url"`
		APIKey   string `yaml:"api_key"`
		Timeout  int    `yaml:"timeout"`
	} `yaml:"llm"`
	MCP struct {
		Servers []struct {
			Transport struct {
				Stdio struct {
					Command string   `yaml:"command"`
					Args    []string `yaml:"args"`
				} `yaml:"stdio"`
			} `yaml:"transport"`
		} `yaml:"servers"`
	} `yaml:"mcp"`
	Dashboard struct {
		Addr string `yaml:"addr"`
	} `yaml:"dashboard"`
}

func loadConfig(path string) (*appConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg appConfig
	return &cfg, yaml.Unmarshal(data, &cfg)
}

// ── Arena Adapter ──────────────────────────────

type arenaAdapter struct {
	orch       *dashboard.Orchestrator
	eventStore *events.MemoryEventStore
	history    []dashboard.ArenaResult
	mu         sync.Mutex
}

func (a *arenaAdapter) Execute(action dashboard.ArenaAction) dashboard.ArenaResult {
	start := time.Now()

	// Actually execute the action.
	switch action.Type {
	case dashboard.ArenaActionKillLeader:
		// Find and cancel the leader (first agent).
		agents := a.orch.ListAgents()
		for _, ag := range agents {
			if ag.Status != "completed" && ag.Status != "failed" {
				a.orch.CancelAgent(ag.ID)
				slog.Info("arena: killed leader agent", "id", ag.ID)
				break
			}
		}
	case dashboard.ArenaActionKillAgent:
		if action.TargetID != "" {
			if a.orch.CancelAgent(action.TargetID) {
				slog.Info("arena: killed agent", "id", action.TargetID)
			} else {
				slog.Warn("arena: agent not found", "id", action.TargetID)
			}
		}
	case dashboard.ArenaActionRemoveNode, dashboard.ArenaActionRemoveEdge:
		// DAG operations — no-op in this demo (no MutableDAG wired).
		slog.Info("arena: DAG operation (no-op in demo)", "type", string(action.Type))
	}

	// Emit event to store.
	if a.eventStore != nil {
		ctx := context.Background()
		payload := map[string]any{
			"action":    string(action.Type),
			"target_id": action.TargetID,
			"source_id": action.SourceID,
		}
		evt := &events.Event{
			ID:        uuid.New().String(),
			StreamID:  "arena",
			Type:      "arena.action",
			Payload:   payload,
			Timestamp: time.Now(),
		}
		_ = a.eventStore.Append(ctx, "arena", []*events.Event{evt}, 0)
	}

	result := dashboard.ArenaResult{
		Success:  true,
		Action:   action,
		Duration: time.Since(start),
	}

	a.mu.Lock()
	a.history = append(a.history, result)
	a.mu.Unlock()

	slog.Info("arena: action executed",
		"type", string(action.Type),
		"target", action.TargetID,
		"duration", result.Duration,
	)

	return result
}

func (a *arenaAdapter) Stats() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()

	total := len(a.history)
	successful := 0
	for _, r := range a.history {
		if r.Success {
			successful++
		}
	}

	return map[string]any{
		"total_actions":      total,
		"successful_actions": successful,
		"failed_actions":     total - successful,
	}
}

func (a *arenaAdapter) History() []dashboard.ArenaResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]dashboard.ArenaResult, len(a.history))
	copy(result, a.history)
	return result
}
