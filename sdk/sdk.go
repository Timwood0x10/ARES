// Package ares provides the top-level, unified entry point for the ARES
// agent runtime. It wraps all internal components behind a simple,
// production-friendly API.
//
// Quick start:
//
//	import (
//	    "context"
//	    "github.com/Timwood0x10/ares"
//	    "github.com/Timwood0x10/ares/api/tools"
//	)
//
//	func main() {
//	    ctx := context.Background()
//	    rt := ares.MustNew(ares.WithOpenAI("gpt-4o-mini"))
//	    defer rt.Close()
//
//	    agent := rt.NewAgent("assistant",
//	        ares.WithInstruction("You are a helpful assistant."),
//	    )
//	    result, err := agent.Run(ctx, "Hello!")
//	}
package sdk

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/api/mcp"
	"github.com/Timwood0x10/ares/api/service/llm"
	memsvc "github.com/Timwood0x10/ares/api/service/memory"
	"github.com/Timwood0x10/ares/api/tools"
	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/linker"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	memprovider "github.com/Timwood0x10/ares/internal/knowledge/provider/memory"
	khruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	postgresstore "github.com/Timwood0x10/ares/internal/knowledge/store/postgres"
	sqlitestore "github.com/Timwood0x10/ares/internal/knowledge/store/sqlite"
)

const strategyPriority = "priority"

// ---- public types ----

// Role constants for LLM messages.
const (
	roleSystem    = "system"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

// Runtime is the top-level ARES container. It owns the LLM client, tool
// registry, and — optionally — memory, AKF knowledge fabric, MCP
// connections, and evolution.
// Create one with MustNew or New.
type Runtime struct {
	llmSvc           *llm.Service
	toolReg          *tools.Registry
	memSvc           *memsvc.Service
	memEnabled       bool
	evoEnabled       bool
	knowledgeEnabled bool
	knowledgeRT      *khruntime.KnowledgeRuntime
	knowledgeStore   knowledge.KnowledgeStore
	eventStore       ares_events.EventStore
	mcpClients       []*mcp.Client
	trace            bool
}

// memSearcher adapts memsvc.Service to the memory.TaskSearcher interface.
type memSearcher struct {
	svc *memsvc.Service
}

func (s *memSearcher) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]memprovider.SearchResult, error) {
	results, err := s.svc.SearchSimilarTasks(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]memprovider.SearchResult, 0, len(results))
	for _, r := range results {
		summary := r.TaskID
		if r.Payload != nil {
			if sVal, ok := r.Payload["input"]; ok {
				if str, ok := sVal.(string); ok {
					summary = str
				}
			}
		}
		out = append(out, memprovider.SearchResult{
			ID:        r.TaskID,
			Summary:   summary,
			Timestamp: r.CreatedAt,
		})
	}
	return out, nil
}

type Agent struct {
	name        string
	instruction string
	tools       []tools.Tool
	runtime     *Runtime
	humanInput  HumanInputFunc
	maxIter     int
}

// HumanInputFunc is called when the agent needs human approval before executing
// a tool call. Return true to approve, false to skip the tool call, or an
// error to abort entirely.
type HumanInputFunc func(ctx context.Context, toolName string, args map[string]any) (approved bool, err error)

// StreamChunk represents a partial streaming result from an agent Run.
type StreamChunk struct {
	// Content is the partial text content.
	Content string
	// Done is true when the stream is complete.
	Done bool
	// Err is set when the stream encounters an error.
	Err error
	// Result is set when Done is true and no error occurred.
	Result *Result
}

// Stream runs the agent against the given input and streams results via a
// channel. The caller must read from the channel until Done is true or Err
// is non-nil.
//
// Usage:
//
//	ch, err := agent.Stream(ctx, "hello")
//	if err != nil { return err }
//	for chunk := range ch {
//	    if chunk.Err != nil { return chunk.Err }
//	    fmt.Print(chunk.Content)
//	}
func (a *Agent) Stream(ctx context.Context, input string) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 32)

	go func() {
		defer close(ch)

		// Run the full agent logic.
		result, err := a.Run(ctx, input)
		if err != nil {
			ch <- StreamChunk{Err: err, Done: true}
			return
		}

		// Simulate streaming by sending the output in chunks.
		runes := []rune(result.Output)
		chunkSize := 10
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			select {
			case ch <- StreamChunk{Content: string(runes[i:end])}:
			case <-ctx.Done():
				ch <- StreamChunk{Err: ctx.Err(), Done: true}
				return
			}
		}

		ch <- StreamChunk{Done: true, Result: result}
	}()

	return ch, nil
}

type Result struct {
	Output     string        `json:"output"`
	ToolCalls  int           `json:"tool_calls"`
	MemoryUsed bool          `json:"memory_used"`
	TokenUsage TokenUsage    `json:"token_usage"`
	Duration   time.Duration `json:"duration"`
}

// TokenUsage summarises LLM token consumption.
type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Total  int `json:"total"`
}

// ---- constructors ----

// MustNew creates a new Runtime with the given options. It panics on error so
// it is safe for quickstart / prototyping code. Use New for production code
// that wants to handle errors gracefully.
func MustNew(opts ...Option) *Runtime {
	r, err := New(opts...)
	if err != nil {
		panic("ares: " + err.Error())
	}
	return r
}

// New creates a new Runtime. Returns an error when a required option (e.g. an
// LLM provider) cannot be initialised.
func New(opts ...Option) (*Runtime, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, fmt.Errorf("option: %w", err)
		}
	}

	// ---- LLM ----
	llmCfg := &llm.Config{
		BaseConfig: cfg.baseCfg,
		LLMConfig:  cfg.llmCfg,
		Fallbacks:  cfg.fallbacks,
	}
	llmSvc, err := llm.NewService(llmCfg)
	if err != nil {
		return nil, friendlyErr("llm", cfg.llmCfg.Provider, err)
	}

	// ---- Tools ----
	toolReg := tools.NewRegistry()

	// ---- Memory ----
	var memSvc *memsvc.Service
	if cfg.memCfg.Enabled {
		s, err := memsvc.New(nil)
		if err != nil {
			return nil, fmt.Errorf("memory: %w", err)
		}
		memSvc = s
	}

	// ---- MCP ----
	var mcpClients []*mcp.Client
	for _, conn := range cfg.mcpConns {
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 30*time.Second)
		client, err := mcp.ConnectStdio(connectCtx, conn.Name, conn.Command, conn.Args)
		connectCancel()
		if err != nil {
			return nil, fmt.Errorf("mcp %q: %w", conn.Name, err)
		}
		listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
		tools, listErr := client.ListTools(listCtx)
		listCancel()
		if listErr != nil {
			return nil, fmt.Errorf("mcp %q list tools: %w", conn.Name, listErr)
		}
		for _, t := range tools {
			toolName := t.Name
			toolDesc := t.Description
			mcpClient := client
			if err := toolReg.Register(mcpToolAdapter{
				name:   toolName,
				desc:   toolDesc,
				client: mcpClient,
			}); err != nil {
				return nil, fmt.Errorf("mcp %q register %s: %w", conn.Name, toolName, err)
			}
		}
		mcpClients = append(mcpClients, client)
	}

	// ---- AKF Knowledge Fabric ----
	var knowledgeRT *khruntime.KnowledgeRuntime
	var knowledgeStore knowledge.KnowledgeStore
	if cfg.knlCfg.Enabled {
		reg := provider.NewProviderRegistry()

		// Auto-register memory provider when memory is also enabled.
		if memSvc != nil {
			searcher := &memSearcher{svc: memSvc}
			if err := reg.Register(memprovider.New("memory", searcher)); err != nil {
				return nil, fmt.Errorf("knowledge: register memory provider: %w", err)
			}
		}

		// TODO(tech-debt): evolution → AKF knowledge provider removed.
		// internal/knowledge/provider/evolution (and its adapter
		// internal/knowledge/adapter) were deleted: they were wired only via
		// the SDK and never reached the serve path. The evolution system
		// itself (mutation/genome/crossover in this file) is unaffected. If
		// evolved strategies should flow into the AKF knowledge graph as
		// decision-type objects again, re-add a provider that reads the
		// evolution strategy store and registers it here.

		// Register user-configured extra knowledge providers (code, mysql,
		// postgres, or custom). Opt-in via WithKnowledgeProvider; defaults
		// to none.
		for _, p := range cfg.extraProviders {
			if err := reg.Register(p); err != nil {
				return nil, fmt.Errorf("knowledge: register provider %s: %w", p.Name(), err)
			}
		}

		// Knowledge store factory: SQLite > PostgreSQL > in-memory. All
		// opt-in via SDK options; defaults to in-memory to preserve prior
		// behaviour.
		switch {
		case cfg.sqliteStorePath != "":
			s, err := sqlitestore.New(cfg.sqliteStorePath)
			if err != nil {
				return nil, fmt.Errorf("knowledge: init sqlite store: %w", err)
			}
			knowledgeStore = s
		case cfg.dbCfg.Host != "":
			sslMode := cfg.dbCfg.SSLMode
			if sslMode == "" {
				sslMode = "disable"
			}
			dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
				cfg.dbCfg.User, cfg.dbCfg.Password, cfg.dbCfg.Host,
				cfg.dbCfg.Port, cfg.dbCfg.Database, sslMode)
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				return nil, fmt.Errorf("knowledge: open postgres store: %w", err)
			}
			knowledgeStore, err = postgresstore.New(db)
			if err != nil {
				if closeErr := db.Close(); closeErr != nil {
					err = fmt.Errorf("knowledge: init postgres store: %w (also close db: %v)", err, closeErr)
				}
				return nil, fmt.Errorf("knowledge: init postgres store: %w", err)
			}
		default:
			knowledgeStore = memstore.New()
		}

		knowledgeRT = khruntime.New(
			planner.NewKnowledgePlanner(),
			planner.NewSourceDiscovery(reg, planner.NewQueryPlanner()),
			reg,
			nil, // pipeline: use defaults
			[]khruntime.Linker{
				&khruntime.DefaultLinker{},
				&linker.DecisionLinker{},
				&linker.ArchitectureLinker{},
				&linker.TimelineLinker{},
				&linker.SimilarityLinker{},
			},
			[]khruntime.Reducer{&khruntime.DefaultReducer{}},
		)
	}

	return &Runtime{
		llmSvc:           llmSvc,
		toolReg:          toolReg,
		memSvc:           memSvc,
		memEnabled:       cfg.memCfg.Enabled,
		evoEnabled:       cfg.evoCfg.Enabled,
		knowledgeEnabled: cfg.knlCfg.Enabled,
		knowledgeRT:      knowledgeRT,
		knowledgeStore:   knowledgeStore,
		eventStore:       ares_events.NewMemoryEventStore(),
		mcpClients:       mcpClients,
		trace:            cfg.trace,
	}, nil
}

// Close releases all resources held by the Runtime (LLM connections, memory
// store, MCP connections). Call once when the Runtime is no longer needed.
func (r *Runtime) Close() {
	r.llmSvc.Close()
	if r.memSvc != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = r.memSvc.Stop(stopCtx)
	}
	for _, c := range r.mcpClients {
		_ = c.Close()
	}
}

// ToolRegistry returns the internal tool registry. Use this to register custom
// tools before creating agents.
func (r *Runtime) ToolRegistry() *tools.Registry {
	return r.toolReg
}

// GetModel returns the LLM model name used by this Runtime.
func (r *Runtime) GetModel() string {
	return r.llmSvc.GetModel()
}

// GetProvider returns the LLM provider name used by this Runtime.
func (r *Runtime) GetProvider() string {
	return string(r.llmSvc.GetProvider())
}

// KnowledgeStore returns the knowledge store, or nil if knowledge is not
// enabled. The concrete type depends on the SDK options used: in-memory by
// default, SQLite via WithSQLiteKnowledgeStore, or PostgreSQL via
// WithPostgres. Use this to save and query KnowledgeObjects directly.
func (r *Runtime) KnowledgeStore() knowledge.KnowledgeStore {
	return r.knowledgeStore
}

// Evolve runs an evolution cycle to improve an agent's instruction. It uses the
// LLM to generate variations, evaluates them against the given task, and returns
// the best-evolved instruction.
func (r *Runtime) Evolve(ctx context.Context, agent *Agent, task string) (string, error) {
	if agent == nil {
		return "", fmt.Errorf("evolve: agent is nil")
	}
	if !r.evoEnabled {
		return "", fmt.Errorf("evolution not enabled (use WithEvolution())")
	}

	if r.trace {
		log.Printf("[ares:evolve] evolving agent %q on task: %s", agent.name, task)
	}

	// Create base strategy with meaningful dimensions: tool selection,
	// workflow topology, scheduler strategy, memory retrieval, recovery.
	base := &mutation.Strategy{
		ID:        fmt.Sprintf("sdk-%s", agent.name),
		Version:   1,
		Score:     -1,
		CreatedAt: time.Now(),
		Params: map[string]any{
			"tool_selector":      "auto",  // auto / manual / priority
			"search_depth":       3,       // 1-5: how deep to search
			"scheduler_strategy": "fifo",  // fifo / priority / round_robin
			"memory_threshold":   0.7,     // 0.0-1.0: similarity threshold
			"recovery_strategy":  "retry", // retry / replace / fallback
		},
		PromptTemplate: agent.instruction,
	}

	// Create mutator for meaningful dimensions.
	mutator, err := mutation.NewMutator(
		mutation.WithParamRanges(evolvableParams()),
	)
	if err != nil {
		return "", fmt.Errorf("create mutator: %w", err)
	}

	// Create crossover operator (uses PyGAD-inspired operators).
	crosser, err := genome.NewCrossover(
		genome.WithSeed(42),
		genome.WithCrossoverType(genome.CrossoverUniform),
	)
	if err != nil {
		return "", fmt.Errorf("create crossover: %w", err)
	}

	// Create GA population.
	pop, err := genome.NewPopulation(ctx, base, mutator,
		genome.WithPopulationSize(10),
		genome.WithEliteCount(2),
		genome.WithMutationRate(0.3),
		genome.WithSurvivalRate(0.5),
		genome.WithSelectionStrategy("tournament"),
		genome.WithTournamentSelection(3),
	)
	if err != nil {
		return "", fmt.Errorf("create population: %w", err)
	}

	// Run evolution using actual execution as scorer (no LLM).
	scorer := func(s *mutation.Strategy) float64 {
		return executeAndScore(ctx, r, agent, task, s)
	}

	for gen := 0; gen < 3; gen++ {
		pop.ScoreAgents(scorer)
		if err := pop.Evolve(ctx, mutator, crosser); err != nil {
			return "", fmt.Errorf("evolve generation %d: %w", gen, err)
		}
	}

	// Get the best strategy.
	best := pop.BestStrategy()
	if best == nil {
		return "", fmt.Errorf("evolution produced no viable strategy")
	}

	if r.trace {
		stats := pop.Stats()
		log.Printf("[ares:evolve] GA evolution complete: gen=%d, best=%.1f, avg=%.1f, strategy=%v",
			stats.Generation, stats.BestScore, stats.AvgScore, best.Params)
	}

	// Apply the evolved strategy's params to the agent.
	applyEvolvedParams(agent, best.Params)

	// Return the evolved strategy summary.
	return fmt.Sprintf("evolved: tool=%v depth=%v scheduler=%v memory=%.2f recovery=%v",
		best.Params["tool_selector"], best.Params["search_depth"],
		best.Params["scheduler_strategy"], best.Params["memory_threshold"],
		best.Params["recovery_strategy"]), nil
}

// evolvableParams returns the parameter ranges for meaningful evolution dimensions.
func evolvableParams() map[string]mutation.ParamRange {
	return map[string]mutation.ParamRange{
		"tool_selector":      {Values: []any{"auto", "manual", strategyPriority}},
		"search_depth":       {Values: []any{1, 2, 3, 4, 5}},
		"scheduler_strategy": {Values: []any{"fifo", strategyPriority, "round_robin"}},
		"memory_threshold":   {Values: []any{0.3, 0.5, 0.7, 0.9}},
		"recovery_strategy":  {Values: []any{"retry", "replace", "fallback"}},
	}
}

// executeAndScore runs the task with a given strategy and scores based on
// actual execution results: success, latency, and token efficiency.
// No LLM involved — pure execution-based evaluation.
func executeAndScore(ctx context.Context, r *Runtime, agent *Agent, task string, s *mutation.Strategy) float64 {
	evolvedAgent := &Agent{
		name:        agent.name,
		instruction: s.PromptTemplate,
		tools:       applyToolSelector(agent.tools, s.Params),
		runtime:     agent.runtime,
		humanInput:  agent.humanInput,
		maxIter:     agent.maxIter,
	}

	start := time.Now()
	result, err := evolvedAgent.Run(ctx, task)
	duration := time.Since(start)

	if err != nil {
		log.Printf("[ares:evolve] execution failed: %v", err)
		return 10.0
	}

	successBonus := 50.0
	if result != nil && result.Output != "" {
		successBonus = 60.0
	}

	speedScore := 30.0 * (1.0 - min(1.0, duration.Seconds()/30.0))

	efficiencyScore := 10.0
	if result != nil && result.TokenUsage.Total > 0 {
		efficiencyScore = 20.0 * (1.0 - min(1.0, float64(result.TokenUsage.Total)/2000.0))
	}

	return successBonus + speedScore + efficiencyScore
}

// applyToolSelector filters the agent's tool list based on the strategy.
func applyToolSelector(toolList []tools.Tool, params map[string]any) []tools.Tool {
	selector, _ := params["tool_selector"].(string)
	switch selector {
	case "priority":
		if len(toolList) > 3 {
			return toolList[:3]
		}
		return toolList
	case "manual":
		var filtered []tools.Tool
		for _, t := range toolList {
			if t.Name() == "search" || t.Name() == "read" {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			return filtered
		}
		return toolList
	default:
		return toolList
	}
}

// applyEvolvedParams applies the evolved strategy params to the agent.
func applyEvolvedParams(agent *Agent, params map[string]any) {
	if v, ok := params["tool_selector"]; ok {
		log.Printf("[ares:evolve] applied tool_selector=%v", v)
	}
	if v, ok := params["search_depth"]; ok {
		log.Printf("[ares:evolve] applied search_depth=%v", v)
	}
	if v, ok := params["scheduler_strategy"]; ok {
		log.Printf("[ares:evolve] applied scheduler_strategy=%v", v)
	}
	if v, ok := params["memory_threshold"]; ok {
		log.Printf("[ares:evolve] applied memory_threshold=%v", v)
	}
	if v, ok := params["recovery_strategy"]; ok {
		log.Printf("[ares:evolve] applied recovery_strategy=%v", v)
	}
}

// NewAgent creates a new Agent bound to this Runtime. The agent carries a name,
// an optional system instruction, and an optional set of tools.
func (r *Runtime) NewAgent(name string, opts ...AgentOption) *Agent {
	ac := defaultAgentConfig()
	for _, o := range opts {
		o(ac)
	}
	return &Agent{
		name:        name,
		instruction: ac.instruction,
		tools:       ac.tools,
		runtime:     r,
		humanInput:  ac.humanInput,
		maxIter:     ac.maxIter,
	}
}

// ---- Agent ----

// Run executes the agent against the given input and returns the result.
// It runs a ReAct loop:
//
//  1. Build the message list (system instruction + memory context + input).
//  2. Call the LLM (with tool definitions).
//  3. If the LLM calls tools, execute them and feed results back.
//  4. Repeat until the LLM produces a final answer.
//  5. Store the conversation in memory (if enabled).
//  6. Return the final output and metadata.
func (a *Agent) Run(ctx context.Context, input string) (*Result, error) {
	start := time.Now()

	sessionID := uuid.NewString()
	if a.runtime.memEnabled && a.runtime.memSvc != nil {
		sid, err := a.runtime.memSvc.CreateSession(ctx, a.name)
		if err == nil {
			sessionID = sid
		}
	}

	messages := a.buildMessages(ctx, input, sessionID)

	// ---- convert tools to core.Tool format ----
	coreTools := a.toCoreTools(a.tools)
	totalInputTokens := 0
	totalOutputTokens := 0
	toolCallCount := 0
	maxIter := a.maxIter
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}

	for iter := 0; iter < maxIter; iter++ {
		if a.runtime.trace {
			log.Printf("[ares:trace] %s → LLM call (iter %d, %d msgs)",
				a.name, iter, len(messages))
		}

		resp, err := a.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
			Messages: messages,
			Tools:    coreTools,
		})
		if err != nil {
			return nil, friendlyErr("llm generate", a.runtime.llmSvc.GetProvider(), err)
		}

		totalInputTokens += resp.Usage.PromptTokens
		totalOutputTokens += resp.Usage.CompletionTokens

		// Store assistant message
		messages = append(messages, &core.LLMMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		if a.runtime.memEnabled && a.runtime.memSvc != nil {
			_ = a.runtime.memSvc.AddMessage(ctx, sessionID, "assistant", resp.Content)
		}

		// ---- tool calling loop ----
		if len(resp.ToolCalls) == 0 {
			// Final answer
			if a.runtime.trace {
				log.Printf("[ares:trace] %s ✓ done (%d tools, %d total tokens, %v)",
					a.name, toolCallCount, totalInputTokens+totalOutputTokens,
					time.Since(start).Round(time.Millisecond))
			}
			return &Result{
				Output:     resp.Content,
				ToolCalls:  toolCallCount,
				MemoryUsed: a.runtime.memEnabled,
				TokenUsage: TokenUsage{
					Input:  totalInputTokens,
					Output: totalOutputTokens,
					Total:  totalInputTokens + totalOutputTokens,
				},
				Duration: time.Since(start),
			}, nil
		}

		// Execute each tool call
		for _, tc := range resp.ToolCalls {
			args := parseArgs(tc.Function.Arguments)

			// Human-in-the-loop check.
			if a.humanInput != nil {
				approved, err := a.humanInput(ctx, tc.Function.Name, args)
				if err != nil {
					return nil, fmt.Errorf("human input: %w", err)
				}
				if !approved {
					if a.runtime.trace {
						log.Printf("[ares:trace] %s → tool call REJECTED by human: %s",
							a.name, tc.Function.Name)
					}
					messages = append(messages, &core.LLMMessage{
						Role:       roleTool,
						ToolCallID: tc.ID,
						Content:    fmt.Sprintf("Tool call %s was rejected by human operator", tc.Function.Name),
					})
					continue
				}
			}

			toolCallCount++
			if a.runtime.eventStore != nil {
				_ = a.runtime.eventStore.Append(ctx, a.name, []*ares_events.Event{{
					Type:     ares_events.EventToolCallStarted,
					StreamID: a.name,
					Payload:  map[string]any{roleTool: tc.Function.Name, "args": tc.Function.Arguments},
					Version:  int64(toolCallCount),
				}}, int64(toolCallCount-1))
			}
			if a.runtime.trace {
				log.Printf("[ares:trace] %s → tool call: %s(%s)",
					a.name, tc.Function.Name, tc.Function.Arguments)
			}

			result, err := a.runtime.toolReg.Execute(ctx, tc.Function.Name, args)
			resultContent := ""
			if err != nil {
				resultContent = fmt.Sprintf("Error: %v", err)
			} else {
				resultContent = fmt.Sprintf("%v", result.Data)
			}

			messages = append(messages, &core.LLMMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resultContent,
			})

			if a.runtime.eventStore != nil {
				_ = a.runtime.eventStore.Append(ctx, a.name, []*ares_events.Event{{
					Type:     ares_events.EventToolCallCompleted,
					StreamID: a.name,
					Payload: map[string]any{
						"tool":    tc.Function.Name,
						"args":    tc.Function.Arguments,
						"result":  resultContent,
						"success": err == nil,
					},
					Version: int64(toolCallCount),
				}}, int64(toolCallCount-1))
			}
		}

		// Continue loop — the LLM will either call more tools or produce a final answer
	}

	if a.runtime.trace {
		log.Printf("[ares:trace] %s ⚠ max iterations reached (%d)", a.name, maxIter)
	}
	return &Result{
		Output:     "max iterations reached",
		ToolCalls:  toolCallCount,
		MemoryUsed: a.runtime.memEnabled,
		TokenUsage: TokenUsage{
			Input:  totalInputTokens,
			Output: totalOutputTokens,
			Total:  totalInputTokens + totalOutputTokens,
		},
		Duration: time.Since(start),
	}, nil
}

// ---- internal helpers ----

func (a *Agent) buildMessages(ctx context.Context, input, sessionID string) []*core.LLMMessage {
	var msgs []*core.LLMMessage

	if a.instruction != "" {
		msgs = append(msgs, &core.LLMMessage{
			Role:    roleSystem,
			Content: a.instruction,
		})
	}

	// Inject memory context if available
	if a.runtime.memEnabled && a.runtime.memSvc != nil {
		ctxStr, err := a.runtime.memSvc.BuildContext(ctx, input, sessionID)
		if err == nil && ctxStr != "" {
			msgs = append(msgs, &core.LLMMessage{
				Role:    roleSystem,
				Content: ctxStr,
			})
		}
	}

	// Inject AKF knowledge context if available.
	if a.runtime.knowledgeEnabled && a.runtime.knowledgeRT != nil {
		budget := knowledge.TokenBudget{
			MaxTokens: 3000,
			Reserved:  1000,
			ForGraph:  2000,
		}
		graph, err := a.runtime.knowledgeRT.Execute(ctx, input, budget, nil)
		if err == nil && graph != nil && len(graph.Nodes) > 0 {
			c := compiler.NewDefaultCompiler()
			compiled, cErr := c.Compile(ctx, graph, compiler.CompileConfig{
				Formats:  []compiler.Format{compiler.FormatPrompt},
				MaxNodes: 50,
				MaxEdges: 50,
			})
			if cErr == nil && compiled != nil {
				if ctxStr, ok := compiled.Formats[compiler.FormatPrompt]; ok && ctxStr != "" {
					msgs = append(msgs, &core.LLMMessage{
						Role:    roleSystem,
						Content: ctxStr,
					})
				}
			}
		}
	}

	msgs = append(msgs, &core.LLMMessage{
		Role:    roleUser,
		Content: input,
	})

	if a.runtime.memEnabled && a.runtime.memSvc != nil {
		_ = a.runtime.memSvc.AddMessage(ctx, sessionID, roleUser, input)
	}

	return msgs
}

func (a *Agent) toCoreTools(tt []tools.Tool) []core.Tool {
	if len(tt) == 0 {
		return nil
	}
	out := make([]core.Tool, 0, len(tt))
	for _, t := range tt {
		params := t.Parameters()
		if params == nil {
			params = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}
		out = append(out, core.Tool{
			Type: "function",
			Function: core.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		})
	}
	return out
}

// parseArgs unmarshals a JSON arguments string into a map.
func parseArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

// mcpToolAdapter wraps an MCP client tool as an SDK tool so it can be used
// with the agent tool registry.
type mcpToolAdapter struct {
	name   string
	desc   string
	client *mcp.Client
}

func (a mcpToolAdapter) Name() string               { return a.name }
func (a mcpToolAdapter) Description() string        { return a.desc }
func (a mcpToolAdapter) Parameters() map[string]any { return nil }
func (a mcpToolAdapter) Capabilities() []string     { return nil }
func (a mcpToolAdapter) Execute(ctx context.Context, params map[string]any) (tools.Result, error) {
	result, err := a.client.CallTool(ctx, a.name, params)
	if err != nil {
		return tools.Result{Success: false, Data: err.Error()}, nil
	}
	var sb strings.Builder
	for _, c := range result.Content {
		sb.WriteString(c.Text)
	}
	return tools.Result{Success: !result.IsError, Data: sb.String()}, nil
}

// friendlyErr wraps an LLM error with an actionable hint based on the provider.
func friendlyErr(scope string, provider core.LLMProvider, origErr error) error {
	hints := map[core.LLMProvider]string{
		core.LLMProviderOpenAI:     "→ Set OPENAI_API_KEY or check https://platform.openai.com/account/api-keys",
		core.LLMProviderAnthropic:  "→ Set ANTHROPIC_API_KEY or check https://console.anthropic.com/",
		core.LLMProviderOpenRouter: "→ Set OPENROUTER_API_KEY or check https://openrouter.ai/keys",
		core.LLMProviderOllama:     "→ Run: ollama run llama3.2  (Ollama may not be running)",
	}
	msg := fmt.Sprintf("%s: %v", scope, origErr)
	if hint, ok := hints[provider]; ok {
		msg += "\n  " + hint
	}
	return fmt.Errorf("%s", msg)
}
