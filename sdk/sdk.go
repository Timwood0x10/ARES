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
//	    ares := sdk.NewRuntime(sdk.WithOpenAI("gpt-4o-mini"))
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
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/api/core"
	apiembed "github.com/Timwood0x10/ares/api/embedding"
	"github.com/Timwood0x10/ares/api/mcp"
	"github.com/Timwood0x10/ares/api/service/llm"
	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/agentloop"
	ares_bootstrap "github.com/Timwood0x10/ares/internal/ares_bootstrap"
	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	ares_evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	aresexp "github.com/Timwood0x10/ares/internal/ares_experience"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/tools/toolsource"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/linker"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	evoprovider "github.com/Timwood0x10/ares/internal/knowledge/provider/evolution"
	memprovider "github.com/Timwood0x10/ares/internal/knowledge/provider/memory"
	storeprovider "github.com/Timwood0x10/ares/internal/knowledge/provider/store"
	khruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	postgresstore "github.com/Timwood0x10/ares/internal/knowledge/store/postgres"
	sqlitestore "github.com/Timwood0x10/ares/internal/knowledge/store/sqlite"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

const strategyPriority = "priority"

// akgNamespace is the default namespace assigned to AKG-distilled
// KnowledgeObjects and used to filter recall in the StoreProvider. It matches
// ares_events.DefaultTenantID so AKG facts are visible to the same
// single-tenant consumers that read distilled experiences.
const akgNamespace = "default"

// ---- public types ----

// Role constants for LLM messages.
const (
	roleSystem    = "system"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

// Runtime is the top-level container for an ARES agent system (a "new ARES runtime").
//
// It owns and manages:
//   - LLM client (OpenAI, Ollama, Anthropic, OpenRouter, or custom)
//   - Tool registry (built-in, custom, MCP-discovered, AKF tools)
//   - Memory & distillation engine (session history, experience distillation, RAG)
//   - AKG / AKF Knowledge Fabric (knowledge graph compilation + retrieval)
//   - Strategy evolution (GA-based optimisation of agent behaviour)
//   - MCP server connections (stdio-based external tools)
//   - Event-driven distillation (TaskCompleted → auto-distill pipeline)
//
// Create one with NewRuntime or New, then call NewAgent / NewTeam to build
// agents. Close must be called once when the Runtime is no longer needed to
// release LLM connections, stop background goroutines, and close MCP clients.
//
// Quick start:
//
//	cfg, _ := sdk.LoadConfigFile("ares.yaml")
//	opts, _ := cfg.ToOptions()
//	ares := sdk.NewRuntime(opts...)       // ares = new ARES runtime
//	defer ares.Close()
//
//	agent := ares.NewAgent("assistant",
//	    sdk.WithInstruction("You are helpful."),
//	)
//	result, _ := agent.Run(ctx, "hello")

// llmService is the subset of the LLM service the sdk uses. It is an
// unexported interface so tests can inject a mock LLM (see sdk_test.go)
// without spinning up a real provider. *llm.Service satisfies it; the field
// is assigned the concrete service in New().
type llmService interface {
	Generate(ctx context.Context, req *core.GenerateRequest) (*core.GenerateResponse, error)
	GetProvider() core.LLMProvider
	GetModel() string
	Close()
}

type Runtime struct {
	llmSvc           llmService
	toolReg          *tools.Registry
	memMgr           memory.MemoryManager
	distillCleanup   func()
	memEnabled       bool
	evoEnabled       bool
	knowledgeEnabled bool
	knowledgeRT      *khruntime.KnowledgeRuntime
	knowledgeStore   knowledge.KnowledgeStore
	evolutionStore   *memStrategyStore
	// evoComponents holds the new evolution system (genome/diff/patch/coordinator)
	// wired to the live KnowledgeRuntime so evolution patches can affect the
	// running knowledge engine. Nil when evolution or knowledge is disabled.
	evoComponents *ares_bootstrap.NewEvolutionComponents
	eventStore    ares_events.EventStore
	mcpClients    []*mcp.Client
	trace         bool
	// ctx governs the lifetime of background goroutines (event-driven
	// distillation subscriber). Cancelled in Close so subscribers exit cleanly.
	ctx context.Context
	// cancel stops background goroutines started in New.
	cancel context.CancelFunc
	// eg tracks background goroutines so Close can wait for in-flight work.
	eg *errgroup.Group
	// distillSvc consumes TaskCompleted events and distills them into long-term
	// experiences. Nil when distillation is disabled or its deps are unavailable.
	distillSvc *aresexp.DistillationService
	// akgBridge distills conversations into AKG KnowledgeObjects and persists
	// them through the quality gate into the knowledge store. Triggered
	// best-effort from the event subscriber alongside distillSvc. Nil when the
	// AKG distiller or knowledge store is unavailable.
	akgBridge *adapter.DistillBridge
}

// memSearcher adapts memory.MemoryManager to the memory.TaskSearcher
// interface. It converts the manager's []*models.Task results into the
// memprovider.SearchResult shape expected by the AKF memory provider.
type memSearcher struct {
	svc memory.MemoryManager
}

// SearchSimilarTasks delegates to the MemoryManager and converts each
// *models.Task into a memprovider.SearchResult. The Task.TaskID maps to
// SearchResult.ID; the "input" payload field (set by SearchSimilarTasks
// on the manager) maps to Summary. Tasks without an input payload fall
// back to the TaskID as the summary.
//
// SearchResult.Score is intentionally left 0: models.Task has no
// similarity-score field today, so there is no real query-relevance signal
// to forward. The MemoryProvider's relevanceFromScore handles Score=0 by
// deriving a rank-based Relevance from result ordering (first result → 1.0,
// decaying to a 0.1 floor), which is a honest signal rather than a fake
// constant. If a future Task revision adds a score field, populate it here
// and the provider will use it automatically.
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
			// Score intentionally 0: see method comment.
		})
	}
	return out, nil
}

// memStrategyStore is an in-memory store that records evolved strategies
// and implements evoprovider.StrategyStore so the AKF knowledge fabric can
// consume them as decision-type KnowledgeObjects.
type memStrategyStore struct {
	mu      sync.Mutex
	active  *ares_evolution.Strategy
	history []*ares_evolution.Strategy
}

func newMemStrategyStore() *memStrategyStore {
	return &memStrategyStore{}
}

func (s *memStrategyStore) GetActive(_ context.Context) (*ares_evolution.Strategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active, nil
}

func (s *memStrategyStore) GetHistory(_ context.Context, _ string, n int) ([]*ares_evolution.Strategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > len(s.history) {
		n = len(s.history)
	}
	return s.history[:n], nil
}

// save records a new evolved strategy as both the active strategy and appends
// it to the history for lineage tracking.
type Agent struct {
	name        string
	instruction string
	tools       []tools.Tool
	runtime     *Runtime
	humanInput  HumanInputFunc
	maxIter     int
	// discovery gates runtime tool discovery (see WithToolDiscovery). When
	// false, Agent.Run is byte-for-byte identical to the legacy path.
	discovery bool
	// toolSource is the discovery source; nil means default (RegistrySource
	// over the Runtime registry). Only consulted when discovery is true.
	toolSource toolsource.ToolSource
	// selector narrows the available pool before each run; nil means
	// AllSelector. Only consulted when discovery is true.
	selector toolsource.ToolSelector
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

// NewRuntime creates and returns a new ARES Runtime — the top-level container that
// owns the LLM client, tool registry, memory/distillation engine, AKG knowledge
// fabric, evolution system, and MCP connections.
//
// It panics on error so it is safe for quickstart / prototyping code.
// Use New for production code that wants to handle errors gracefully.
//
// Quick start:
//
//	ares := sdk.NewRuntime(sdk.WithConfigFromEnv())
//	defer ares.Close()
//	agent := ares.NewAgent("assistant")
//	result, _ := agent.Run(ctx, "hello")
func NewRuntime(opts ...Option) *Runtime {
	r, err := New(opts...)
	if err != nil {
		panic("ares: " + err.Error())
	}
	return r
}

// knowledgeWiring bundles the outputs of wireKnowledge so New() can unpack
// a single struct instead of juggling four return values.
type knowledgeWiring struct {
	rt             *khruntime.KnowledgeRuntime
	store          knowledge.KnowledgeStore
	evolutionStore *memStrategyStore
}

// wireKnowledge constructs the AKF Knowledge Fabric runtime, store, and
// evolution strategy store from the SDK config. When knowledge is disabled,
// it returns a zero-value wiring (all nil). Extracted from New() to keep
// the constructor under the 100-line limit.
//
// Args:
//
//	cfg      - fully applied SDK config; knlCfg/evoCfg/dbCfg/sqliteStorePath are read.
//	memMgr   - memory manager; when non-nil, a memory provider is auto-registered
//	           into the knowledge provider registry so past tasks surface in the AKG.
//	embClient - embedding service used by the StoreProvider for vector recall;
//	            nil signals lexical-only search.
//	embModel - embedding model name selecting which Representation the store
//	           compares against; empty is valid when embClient is nil.
//
// Returns:
//
//	*knowledgeWiring - rt/store/evolutionStore are nil when knowledge is disabled.
//	error             - wrapped error if a knowledge store or provider fails to init.
func wireKnowledge(
	cfg *config,
	memMgr memory.MemoryManager,
	embClient apiembed.EmbeddingService,
	embModel string,
) (*knowledgeWiring, error) {
	if !cfg.knlCfg.Enabled {
		return &knowledgeWiring{}, nil
	}

	reg := provider.NewProviderRegistry()

	if err := registerKnowledgeProviders(reg, cfg, memMgr); err != nil {
		return nil, err
	}

	store, err := buildKnowledgeStore(cfg)
	if err != nil {
		return nil, err
	}

	// Register the StoreProvider so AKG-distilled facts written to the store
	// by the DistillBridge are readable by the KnowledgeRuntime as a
	// KnowledgeObject source. This closes the 0.2.9 write→read loop.
	if store != nil {
		sp := storeprovider.New("akg_store", store, embClient, embModel, akgNamespace)
		if err := reg.Register(sp); err != nil {
			return nil, fmt.Errorf("knowledge: register store provider: %w", err)
		}
	}

	var evoStore *memStrategyStore
	if cfg.evoCfg.Enabled {
		evoStore = newMemStrategyStore()
	}

	rt := khruntime.New(
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

	return &knowledgeWiring{rt: rt, store: store, evolutionStore: evoStore}, nil
}

// registerKnowledgeProviders registers the memory, evolution, and
// user-configured extra providers into the registry. Extracted to keep
// wireKnowledge under 100 lines.
func registerKnowledgeProviders(reg *provider.ProviderRegistry, cfg *config, memMgr memory.MemoryManager) error {
	if memMgr != nil {
		searcher := &memSearcher{svc: memMgr}
		if err := reg.Register(memprovider.New("memory", searcher)); err != nil {
			return fmt.Errorf("knowledge: register memory provider: %w", err)
		}
	}

	if cfg.evoCfg.Enabled {
		evoStore := newMemStrategyStore()
		if err := reg.Register(evoprovider.New("evolution", evoStore)); err != nil {
			return fmt.Errorf("knowledge: register evolution provider: %w", err)
		}
	}

	for _, p := range cfg.extraProviders {
		if err := reg.Register(p); err != nil {
			return fmt.Errorf("knowledge: register provider %s: %w", p.Name(), err)
		}
	}
	return nil
}

// buildKnowledgeStore selects the knowledge store backend: SQLite >
// PostgreSQL > in-memory. All opt-in via SDK options; defaults to
// in-memory to preserve prior behaviour.
func buildKnowledgeStore(cfg *config) (knowledge.KnowledgeStore, error) {
	switch {
	case cfg.sqliteStorePath != "":
		s, err := sqlitestore.New(cfg.sqliteStorePath)
		if err != nil {
			return nil, fmt.Errorf("knowledge: init sqlite store: %w", err)
		}
		return s, nil
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
		store, err := postgresstore.New(db)
		if err != nil {
			if closeErr := db.Close(); closeErr != nil {
				err = fmt.Errorf("knowledge: init postgres store: %w (also close db: %v)", err, closeErr)
			}
			return nil, fmt.Errorf("knowledge: init postgres store: %w", err)
		}
		return store, nil
	default:
		return memstore.New(), nil
	}
}

// resolveAKGEmbeddingModel picks the embedding model for the AKG loop. The
// AKG-specific model (cfg.knlCfg.EmbeddingModel) takes precedence so users can
// pin a different model for fact distillation than the one used by the memory
// distiller. Falls back to the base embedding service model when unset.
func resolveAKGEmbeddingModel(cfg *config) string {
	if cfg.knlCfg.EmbeddingModel != "" {
		return cfg.knlCfg.EmbeddingModel
	}
	return cfg.embedCfg.Model
}

// buildAKGBridge constructs the DistillBridge that distills conversations into
// AKG KnowledgeObjects and persists them through the quality gate. Returns nil
// (no-op) when either the distiller or the store is unavailable, so the caller
// can unconditionally assign the result. The quality gate falls back to the
// knowledge package default when left at zero, matching WithAKGQualityGate's
// documented "zero value = default" contract.
func buildAKGBridge(
	cfg *config,
	distiller adapter.ConversationDistiller,
	store knowledge.KnowledgeStore,
	embClient apiembed.EmbeddingService,
	embModel string,
) *adapter.DistillBridge {
	if distiller == nil || store == nil {
		return nil
	}
	gate := cfg.knlCfg.QualityGate
	if gate.MinFinalScore == 0 {
		gate = knowledge.DefaultQualityGateConfig()
	}
	return adapter.NewDistillBridgeWithGate(
		distiller, nil, store, embClient,
		gate, knowledge.NewRelationExtractor(),
		akgNamespace, embModel,
	)
}

// wireEvolutionHotUpdate wires the live KnowledgeRuntime into the evolution
// patch system so knowledge patches affect the running engine. Returns nil
// (no-op) when evolution or knowledge is disabled, or when wiring fails
// (non-fatal: a warning is logged). Extracted from New() to keep the
// constructor under the 100-line limit.
func wireEvolutionHotUpdate(cfg *config, knowRt *khruntime.KnowledgeRuntime) *ares_bootstrap.NewEvolutionComponents {
	if !cfg.evoCfg.Enabled || knowRt == nil {
		return nil
	}
	comps, err := ares_bootstrap.ProvideNewEvolution(nil, knowRt, nil)
	if err != nil {
		slog.Warn("sdk: evolution hot-update wiring failed; knowledge runtime not patchable",
			"error", err)
		return nil
	}
	slog.Info("sdk: evolution hot-update wired (knowledge runtime patchable by evolution)")
	return comps
}

// wireMCPClients connects to each configured MCP server, lists its tools, and
// registers them into the SDK tool registry. Extracted from New() to keep the
// constructor under the 100-line limit.
//
// Args:
//
//	cfg     - fully applied SDK config; mcpConns is read.
//	toolReg - the SDK tool registry; MCP tools are registered by name.
//
// Returns:
//
//	[]*mcp.Client - one client per configured MCP connection (empty when none).
//	error         - wrapped with context if a connection, list, or register fails.
func wireMCPClients(cfg *config, toolReg *tools.Registry) ([]*mcp.Client, error) {
	var mcpClients []*mcp.Client
	for _, conn := range cfg.mcpConns {
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 30*time.Second)
		client, err := mcp.ConnectStdio(connectCtx, conn.Name, conn.Command, conn.Args)
		connectCancel()
		if err != nil {
			return nil, fmt.Errorf("mcp %q: %w", conn.Name, err)
		}
		listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
		mcpTools, listErr := client.ListTools(listCtx)
		listCancel()
		if listErr != nil {
			return nil, fmt.Errorf("mcp %q list tools: %w", conn.Name, listErr)
		}
		for _, t := range mcpTools {
			if err := toolReg.Register(mcpToolAdapter{
				name:   t.Name,
				desc:   t.Description,
				client: client,
			}); err != nil {
				return nil, fmt.Errorf("mcp %q register %s: %w", conn.Name, t.Name, err)
			}
		}
		mcpClients = append(mcpClients, client)
	}
	return mcpClients, nil
}

// New creates and returns a new ARES Runtime. It wires the LLM client, tool
// registry, memory/distillation engine, RAG retrievers, AKG knowledge fabric,
// MCP connections, evolution system, and event-driven distillation.
//
// Returns an error when a required option (e.g. an LLM provider) cannot be
// initialised. Use NewRuntime for quickstart code that panics on error instead.
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
		return nil, agentloop.FriendlyErr("llm", cfg.llmCfg.Provider, err)
	}

	toolReg := tools.NewRegistry()

	// ---- Memory (production MemoryManager: compression + RAG + distillation) ----
	var memMgr memory.MemoryManager
	var distillCleanup func()
	var embClient apiembed.EmbeddingService
	var expRepo repositories.ExperienceRepositoryInterface
	var distillSvc *aresexp.DistillationService
	var akgDistiller adapter.ConversationDistiller
	if cfg.memCfg.Enabled {
		w, err := wireMemory(context.Background(), cfg)
		if err != nil {
			return nil, fmt.Errorf("memory: %w", err)
		}
		memMgr = w.mgr
		embClient = w.embClient
		expRepo = w.expRepo
		distillCleanup = w.cleanup
		distillSvc = w.distillSvc
		akgDistiller = w.akgDistiller
	}

	// ---- MCP ----
	mcpClients, err := wireMCPClients(cfg, toolReg)
	if err != nil {
		return nil, err
	}

	// ---- AKF Knowledge Fabric ----
	embModelForAKG := resolveAKGEmbeddingModel(cfg)
	kw, err := wireKnowledge(cfg, memMgr, embClient, embModelForAKG)
	if err != nil {
		return nil, err
	}

	// ---- AKF knowledge tools (auto-registered so the agent can call them) ----
	if cfg.knlCfg.Enabled && kw.rt != nil {
		if err := registerAKFTools(toolReg, kw.rt); err != nil {
			return nil, fmt.Errorf("akf tools: %w", err)
		}
	}

	// ---- Evolution hot-update (wires the live KnowledgeRuntime into the
	// evolution patch system so knowledge patches affect the running engine) ----
	evoComponents := wireEvolutionHotUpdate(cfg, kw.rt)

	// ---- RAG retriever wiring (best-effort, non-fatal) ----
	if cfg.memCfg.EnableRAG && memMgr != nil {
		wireSDKRetrievers(context.Background(), cfg, memMgr, embClient, expRepo,
			kw.rt, kw.store, embModelForAKG)
	}

	// ---- AKG DistillBridge (write loop: conversations → knowledge store) ----
	akgBridge := buildAKGBridge(cfg, akgDistiller, kw.store, embClient, embModelForAKG)

	rtCtx, rtCancel, eg, eventStore := newEventBackend(distillSvc, akgBridge)

	return &Runtime{
		llmSvc:           llmSvc,
		toolReg:          toolReg,
		memMgr:           memMgr,
		distillCleanup:   distillCleanup,
		memEnabled:       cfg.memCfg.Enabled,
		evoEnabled:       cfg.evoCfg.Enabled,
		knowledgeEnabled: cfg.knlCfg.Enabled,
		knowledgeRT:      kw.rt,
		knowledgeStore:   kw.store,
		evolutionStore:   kw.evolutionStore,
		evoComponents:    evoComponents,
		eventStore:       eventStore,
		mcpClients:       mcpClients,
		trace:            cfg.trace,
		ctx:              rtCtx,
		cancel:           rtCancel,
		eg:               eg,
		distillSvc:       distillSvc,
		akgBridge:        akgBridge,
	}, nil
}

// Close releases all resources held by the Runtime (LLM connections, memory
// store, MCP connections). Call once when the Runtime is no longer needed.
func (r *Runtime) Close() {
	// Stop background goroutines (event-driven distillation subscriber) first
	// and wait for in-flight work, so the subscriber stops accepting new events
	// before the stores/clients it depends on are torn down. Best-effort: the
	// subscriber returns nil on ctx cancellation.
	if r.cancel != nil {
		r.cancel()
	}
	if r.eg != nil {
		_ = r.eg.Wait()
	}
	r.llmSvc.Close()
	if r.memMgr != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = r.memMgr.Stop(stopCtx)
	}
	if r.distillCleanup != nil {
		r.distillCleanup()
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
		discovery:   ac.discovery,
		toolSource:  ac.toolSource,
		selector:    ac.selector,
	}
}

// ---- Agent ----

// Run executes the agent against the given input and returns the result.
// It builds the message list (system instruction + memory/knowledge context +
// input), creates the memory session, then delegates the ReAct loop
// (LLM call → tool execution → feed back) to agentloop.Engine. The engine is
// the single execution path; Run no longer inlines the loop.
//
//  1. Create the memory session (when memory is enabled).
//  2. Build the message list (system instruction + memory context + input).
//  3. Delegate the ReAct loop to agentloop.Engine.
//  4. Map the engine Result back into the sdk Result.
func (a *Agent) Run(ctx context.Context, input string) (*Result, error) {
	start := time.Now()

	sessionID := uuid.NewString()
	if a.runtime.memEnabled && a.runtime.memMgr != nil {
		sid, err := a.runtime.memMgr.CreateSession(ctx, a.name)
		if err == nil {
			sessionID = sid
		}
	}

	messages := a.buildMessages(ctx, input, sessionID)
	// resolveTools returns the LLM tool defs, the tool executor, and (when
	// discovery is on) a runtime tool expander. When discovery is OFF this is
	// byte-for-byte identical to the legacy path: (toCoreTools(a.tools),
	// a.runtime.toolReg, nil).
	llmTools, toolExecutor, toolExpander := a.resolveTools(ctx, input)

	eng := &agentloop.Engine{
		LLM:            a.runtime.llmSvc,
		Tools:          toolExecutor,
		Events:         a.runtime.eventStore,
		Memory:         a.runtime.memMgr,
		Tracer:         a.traceTracer(),
		MemEnabled:     a.runtime.memEnabled,
		DistillEnabled: a.runtime.distillSvc != nil,
	}
	res, err := eng.Run(ctx, &agentloop.Request{
		Messages:     messages,
		Tools:        llmTools,
		MaxIter:      a.maxIter,
		AgentName:    a.name,
		SessionID:    sessionID,
		Input:        input,
		HumanInput:   agentloop.HumanInputFunc(a.humanInput),
		ToolExpander: toolExpander,
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Output:     res.Output,
		ToolCalls:  res.ToolCalls,
		MemoryUsed: res.MemoryUsed,
		TokenUsage: TokenUsage{
			Input:  res.InputTokens,
			Output: res.OutputTokens,
			Total:  res.InputTokens + res.OutputTokens,
		},
		Duration: time.Since(start),
	}, nil
}

// traceTracer returns log.Printf when tracing is enabled, nil otherwise. The
// agentloop engine treats a nil Tracer as "no trace logging", so this preserves
// the original a.runtime.trace gating without the engine needing a trace bool.
func (a *Agent) traceTracer() func(format string, args ...any) {
	if a.runtime.trace {
		return log.Printf
	}
	return nil
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
	if a.runtime.memEnabled && a.runtime.memMgr != nil {
		ctxStr, err := a.runtime.memMgr.BuildContext(ctx, input, sessionID)
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

	if a.runtime.memEnabled && a.runtime.memMgr != nil {
		_ = a.runtime.memMgr.AddMessage(ctx, sessionID, roleUser, input)
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
