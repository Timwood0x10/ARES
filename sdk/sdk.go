// Package ares provides the top-level, unified entry point for the ARES
// agent runtime. It wraps all internal components behind a simple,
// production-friendly API.
//
// Quick start:
//
//	import (
//	    "context"
//
//	    "github.com/Timwood0x10/ares/sdk"
//	)
//
//	func main() {
//	    ctx := context.Background()
//	    rt := sdk.NewRuntime(sdk.WithOpenAI("gpt-4o-mini"))
//	    defer rt.Close()
//
//	    agent := rt.NewAgent("assistant",
//	        sdk.WithInstruction("You are a helpful assistant."),
//	    )
//	    result, err := agent.Run(ctx, "Hello!")
//	    _ = result
//	    _ = err
//	}
package sdk

//nolint:errcheck // Close() only: shutdown paths use deliberate `_ =` assignments (drained errgroup, best-effort MCP/client closes) where a second failure during teardown has no recovery action
import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/agentsyscall"
	tools "github.com/Timwood0x10/ares/internal/apitools"
	ares_bootstrap "github.com/Timwood0x10/ares/internal/ares_bootstrap"
	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/kernel"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	khruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	mcp "github.com/Timwood0x10/ares/internal/mcpclient"
	memory "github.com/Timwood0x10/ares/internal/runtime/memory"
	aresexp "github.com/Timwood0x10/ares/internal/runtime/memory/experience"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
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
	// sslModeDisable is the SDK convention for local/dev PostgreSQL when no
	// ssl_mode is configured (empty means "disable").
	sslModeDisable = "disable"
)

// llmService is the subset of the LLM service the sdk uses. It is an
// unexported interface so tests can inject a mock LLM (see sdk_test.go)
// without spinning up a real provider. *llm.Service satisfies it; the field
// is assigned the concrete service in New().
type llmService interface {
	Generate(ctx context.Context, req *llmcore.GenerateRequest) (*llmcore.GenerateResponse, error)
	GetProvider() llmcore.LLMProvider
	GetModel() string
	Close()
}

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
// Create one with NewRuntime or New, then call NewAgent to build
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
//	// Minimal closed loop — register peer capability agents, submit tasks by capability.
//	ares.RegisterAgent("coder", sdk.WithInstruction("You fix code."))
//	result, _ := ares.Submit(ctx, sdk.Task{Capability: "coder", Input: "hello"})
//
//	// Or run an agent directly (Agent.Run stays as the fine-grained entry point).
//	agent := ares.NewAgent("assistant", sdk.WithInstruction("You are helpful."))
//	result, _ = agent.Run(ctx, "hello")
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
	// bootstrap holds the Bootstrap-assembled core components (Stage 8): when
	// non-nil, the SDK reuses the same EventStore / NewEvolution / System
	// Runtime instances as serve and start instead of a parallel graph, and
	// Close drains Bootstrap's background goroutines via WaitBackground.
	bootstrap *ares_bootstrap.Components
	// bootstrapCancel cancels the Bootstrap lifecycle context; stored so Close
	// stops Bootstrap's background goroutines before WaitBackground drains them.
	bootstrapCancel context.CancelFunc
	// evidencePool, when non-nil, is a PostgreSQL pool created for the
	// evidence store. Closed in Close() to prevent connection leaks.
	// Typed as *postgres.Pool (not io.Closer) so a nil pool stays a nil
	// pointer — assigning a nil *postgres.Pool to an interface would make
	// the interface non-nil and Close() would dereference a nil db.
	evidencePool *postgres.Pool
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
	// agentByCapability maps a capability to the agent registered to handle it
	// (minimal SDK scheduling surface — RegisterAgent/Submit). Guarded by agentMu.
	agentByCapability map[string]*Agent
	agentMu           sync.Mutex
	// ---- shared scheduler (SDK/kernel merge) ----
	// sdkExecutors is the scheduler's static-executor map, passed by
	// reference to kernel.New. Since the L2 convergence nothing populates
	// it — every task drains through the L2 router — and it remains only
	// as the constructor's compatibility slot.
	sdkExecutors map[string]kernel.CapabilityExecutor
	// sdkFabric is the runtime's own Task Fabric; sched is the shared
	// kernel.Scheduler driving submitted tasks (the SAME engine the
	// kernel uses). Lazily started on the first Submit; schedOnce guards it.
	sdkFabric   *taskfabric.Fabric
	sched       *kernel.Scheduler
	schedOnce   sync.Once
	schedCtx    context.Context
	schedCancel context.CancelFunc
	// schedDone closes when the scheduler drain goroutine has returned; nil
	// until the first Submit starts it. Close waits on it so a drain in
	// flight cannot touch executors/stores after they are torn down.
	schedDone chan struct{}
	// agentsFabric is the runtime's Agent Fabric, backing spawn_agent syscalls
	// (the SDK wires the same kernel syscalls as peer mode). Created in
	// ensureScheduler alongside sdkFabric; nil until the first Submit.
	agentsFabric *agentfabric.Fabric
	// l2Exec is the shared L2 execution core (agentruntime.Execution — the
	// same core serve/start use): session registry, compile coordinator,
	// L2 router, reaper, submitter. Built lazily by ensureL2 on first
	// use; nil until wired (and permanently nil when no LLM is configured).
	l2Exec *agentruntime.Execution
	// l2Binder is the runtime's shared tool binder for the L2 planner
	// (bridged from the tool registry including syscall tools). Kept so a
	// tool registered after the L2 peer was spawned can be re-synced into
	// the planner's view and the peer's tool/* capability set
	// (resyncL2Tools, run on the L2 submit path).
	l2Binder sub.ToolBinder
	// l2Once guards l2Exec construction (same pattern as schedOnce).
	l2Once sync.Once
	// gov is the cognitive-execution budget injected into the L2 peer's
	// SpawnSpec and syscall-spawned peers (WithAgentGovernance; zero =
	// unlimited). Enforced by the scheduler via sched.WithGovernance.
	//
	// govMu guards gov's post-construction mutation: NewAgent's
	// WithMaxTokens bridge (sdk.go) writes it while ensureL2's l2Once body
	// (l2.go) reads it, and NewAgent is legal to call concurrently with
	// another agent's first Run. Construction (builder.go) completes before
	// any handle exists, so reads before the first NewAgent are lock-free
	// by construction.
	govMu sync.Mutex
	gov   agentfabric.Governance
	// syscallTools are the LLM-facing spawn_agent/create_task definitions
	// appended to every agent's tool list so SDK users can autonomously
	// decompose tasks. Populated by wireSyscalls; nil before the first
	// Submit.
	syscallTools []llmcore.Tool
	// syscallKernel is the agentsyscall kernel built by wireSyscalls, kept so
	// the loop lifetime (WithLoopLifetime) wiring is observable and plan-loop
	// control (LivePlanLoops/StopPlanLoop) is reachable from the runtime. Nil
	// before the first Submit.
	syscallKernel *agentsyscall.Kernel
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
//	ares := sdk.NewRuntime(sdk.WithConfig("ares.yaml"))
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
	// Early misconfiguration signal: a hosted provider without a key only
	// surfaced as a provider-side 401 on the first Run, far from the option
	// call that caused it. Warn here so the gap is visible at construction;
	// construction itself stays non-fatal (key-less gateways are legitimate).
	if hint := providerKeyHint(cfg.llmCfg.Provider, cfg.llmCfg.APIKey); hint != "" {
		slog.Warn(hint)
	}

	// The heavy lifting lives in the sdkBuilder methods (builder.go); this
	// function only orders the phases, arms the failure-cleanup defer, and maps
	// phase errors to the caller.
	b, err := newSDKBuilder(cfg)
	if err != nil {
		return nil, err
	}
	// On any error path below, release everything created so far; on success
	// the builder sets bootstrapCancelTaken and hands ownership to Close().
	defer func() {
		if !b.bootstrapCancelTaken {
			b.cleanupOnError()
		}
	}()

	b.assembleCore()
	if err := b.wireMemoryPhase(); err != nil {
		return nil, err
	}
	if err := b.wireMCPPhase(); err != nil {
		return nil, err
	}
	if err := b.wireKnowledgePhase(); err != nil {
		return nil, err
	}
	if err := b.wireEvolutionPhase(); err != nil {
		return nil, err
	}
	b.wireRetrieversAndBridge()
	b.wireEventBackendPhase()

	rt := b.buildRuntime()
	// Transfer Bootstrap ctx ownership to the Runtime on the success path so
	// the deferred cancel above does not fire; Close owns cancellation now.
	b.bootstrapCancelTaken = true
	return rt, nil
}

// Close releases all resources held by the Runtime (LLM connections, memory
// store, MCP connections). Call once when the Runtime is no longer needed.
func (r *Runtime) Close() {
	// Stop the shared scheduler's drain loop first (SDK/kernel merge): it runs on
	// its own context so a Submit in flight is cancelled before the executor
	// agents and stores it depends on are torn down. The join is bounded so a
	// stuck quantum cannot hang Close forever; drain's wg.Wait honors ctx
	// cancellation, so the loop exits promptly under normal operation.
	if r.schedCancel != nil {
		r.schedCancel()
	}
	if r.schedDone != nil {
		select {
		case <-r.schedDone:
		case <-time.After(30 * time.Second):
			// Best-effort teardown must remain bounded: a drain wedged past
			// the budget is logged (visible, not silent) and the teardown
			// proceeds — the components below are closing either way.
			slog.Warn("sdk: scheduler drain did not exit within 30s; proceeding with teardown")
		}
	}
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
	// Stage 8 (SDK unification): when the Runtime is backed by the Bootstrap
	// core, cancel its lifecycle context FIRST (so Bootstrap's background
	// goroutines — distillation subscriber, GA ticker, LLM suggestion ticker —
	// exit on ctx.Done()), then drain them through the SAME lifecycle kernel as
	// serve/start — WaitBackground — so no goroutine outlives Close. Fallback
	// SDK wiring (sqlite/extra providers) has no Bootstrap core and is skipped.
	if r.bootstrap != nil {
		if r.bootstrapCancel != nil {
			r.bootstrapCancel()
		}
		r.bootstrap.WaitBackground()
	}
	// Close the evidence PostgreSQL pool to prevent connection leaks.
	// The pool is nil when no Postgres was configured, so this is a safe no-op.
	if r.evidencePool != nil {
		_ = r.evidencePool.Close()
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

// Snapshot returns the system-level component status snapshot from the
// Bootstrap core (Stage 1 observability). Returns an empty snapshot when
// the Runtime is not backed by Bootstrap (SDK-only options) or when
// Bootstrap failed before wiring completed — callers can always consume
// a valid value without nil guards.
func (r *Runtime) Snapshot() kernel.Snapshot {
	if r.bootstrap == nil {
		return kernel.Snapshot{}
	}
	return r.bootstrap.Snapshot()
}

// ToolRegistry returns the internal tool registry. Use this to register custom
// tools before creating agents. A tool registered after the first Submit is
// picked up by the L2 path on the next submission (resyncL2Tools re-bridges
// the registry and extends the L2 peer's tool/* capabilities); registering
// before the first Submit simply avoids that one-time re-sync.
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

// governanceSnapshot returns the current governance budget under govMu.
// Every post-construction read of r.gov goes through here: NewAgent's
// WithMaxTokens bridge writes concurrently, so a bare field read races
// (found in code review; locked pattern mirrors the once-body reads in
// ensureL2 and wireSyscalls).
func (r *Runtime) governanceSnapshot() agentfabric.Governance {
	r.govMu.Lock()
	defer r.govMu.Unlock()
	return r.gov
}

// NewAgent creates a new Agent bound to this Runtime. The agent carries a name,
// an optional system instruction, and an optional set of tools.
func (r *Runtime) NewAgent(name string, opts ...AgentOption) *Agent {
	ac := defaultAgentConfig()
	for _, o := range opts {
		o(ac)
	}
	// Bridge WithMaxTokens into the runtime's governance budget (Phase 7):
	// the shared L2 path enforces token limits exclusively through
	// Governance at quantum boundaries, so an agent-level WithMaxTokens
	// that stops at the agentConfig would be a silent no-op — exactly the
	// "stored but ignored bound" the option's old doc admitted to. The
	// bridge happens here, before the first Run/Submit can call ensureL2,
	// which stamps r.gov into the L2 peer ONCE (l2Once): a WithMaxTokens
	// applied after the first run of ANY agent on this runtime cannot take
	// effect — NewAgent is the last bridge point. Only a positive value
	// bridges; a later agent with a SMALLER budget would otherwise silently
	// tighten every other agent (documented: first positive value wins,
	// WithAgentGovernance remains the explicit runtime-level control).
	r.govMu.Lock()
	if ac.maxTokens > 0 && r.gov.TokenBudget <= 0 {
		r.gov.TokenBudget = ac.maxTokens
	}
	r.govMu.Unlock()
	// Custom tools attached via WithTools must reach the runtime registry:
	// the L2 planner only sees tools bridged from toolReg (resyncL2Tools),
	// so leaving them solely on the Agent made WithTools a silent no-op on
	// the L2 execution path. Registry.Register overwrites by name, so
	// re-creating an agent with the same tool name is idempotent.
	for _, t := range ac.tools {
		if err := r.toolReg.Register(t); err != nil {
			slog.Warn("sdk: register agent tool failed",
				"agent", name, "tool", t.Name(), "error", err)
		}
	}
	return &Agent{
		name:        name,
		instruction: ac.instruction,
		tools:       ac.tools,
		runtime:     r,
		humanInput:  ac.humanInput,
		maxIter:     ac.maxIter,
		maxTokens:   ac.maxTokens,
		timeout:     ac.timeout,
		discovery:   ac.discovery,
		toolSource:  ac.toolSource,
		selector:    ac.selector,
	}
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
	// On any failure, close every client already connected so a partial
	// connection is not leaked when New() returns the error.
	closeAll := func() {
		for _, c := range mcpClients {
			_ = c.Close()
		}
	}
	for _, conn := range cfg.mcpConns {
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 30*time.Second)
		client, err := mcp.ConnectStdio(connectCtx, conn.Name, conn.Command, conn.Args)
		connectCancel()
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("mcp %q: %w", conn.Name, err)
		}
		listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
		mcpTools, listErr := client.ListTools(listCtx)
		listCancel()
		if listErr != nil {
			_ = client.Close()
			closeAll()
			return nil, fmt.Errorf("mcp %q list tools: %w", conn.Name, listErr)
		}
		for _, t := range mcpTools {
			if err := toolReg.Register(mcpToolAdapter{
				name:   t.Name,
				desc:   t.Description,
				client: client,
			}); err != nil {
				_ = client.Close()
				closeAll()
				return nil, fmt.Errorf("mcp %q register %s: %w", conn.Name, t.Name, err)
			}
		}
		mcpClients = append(mcpClients, client)
	}
	return mcpClients, nil
}
