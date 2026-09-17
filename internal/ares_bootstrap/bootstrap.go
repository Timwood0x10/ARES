// Package ares_bootstrap orchestrates component assembly.
package ares_bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/kernel"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/knowledge/skills"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
	ares_memory "github.com/Timwood0x10/ares/internal/runtime/memory"
	aresexp "github.com/Timwood0x10/ares/internal/runtime/memory/experience"
	"github.com/Timwood0x10/ares/internal/runtime/observability"
	flight "github.com/Timwood0x10/ares/internal/runtime/observability/flight"
	ares_mcp "github.com/Timwood0x10/ares/internal/runtime/protocol/mcp"
	ares_skills "github.com/Timwood0x10/ares/internal/runtime/protocol/skills"
	"github.com/Timwood0x10/ares/internal/storage"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// DAG step identifiers used in the minimal evolution graph.
const dagStepProcess = "process"

// Components holds all assembled system components.
type Components struct {
	MCP          *ares_mcp.MCPManager
	Dashboard    *ObservabilityProviders
	LLM          *LLMComponents
	Evolution    *EvolutionComponents
	NewEvolution *NewEvolutionComponents
	Runtime      *runtime.Manager
	Memory       ares_memory.MemoryManager
	EventStore   ares_events.EventStore
	Distillation *aresexp.DistillationService
	// SkillsRegistry is the progressive-disclosure skill index seeded by
	// wireSkills. It powers two readers: the memory
	// manager's resident "Available skills" block (attached via
	// SetSkillsRegistry) and the environment-capability searcher (envcap) in
	// serve, which exposes skills as searchable tool capabilities. Nil when
	// memory is disabled or skill wiring was skipped.
	SkillsRegistry *skills.Registry
	// SkillCatalog is the live catalog handle: serve registers its
	// CatalogTools into the agent tool registry and wires the experience
	// confidence source from it. Nil when skills are disabled.
	SkillCatalog *ares_skills.Catalog
	// Discovery holds the optional service discovery engine. It is nil when
	// cfg.Discovery.Enabled is false (the default), preserving prior behavior.
	Discovery *DiscoveryComponents
	// KnowledgeRuntime is the shared knowledge runtime used by the evolution
	// system's KnowledgePatchExecutor and the agent's AKF tools. It is
	// created once during bootstrap and reused so that knowledge genome
	// patches (ChangeBudget/ChangePlanner/ChangeReducer) affect the actual
	// runtime used by the agent's knowledge tools.
	KnowledgeRuntime *knowledgeruntime.KnowledgeRuntime
	// VectorStore backs the knowledge runtime's VectorProvider (semantic
	// search over embedded documents). It is nil when distillation/vector
	// storage is not wired, in which case the runtime skips the vector
	// provider entirely.
	VectorStore storage.VectorStore
	// KnowledgeStore backs the AKG read/write loop: the DistillBridge
	// (write side) persists AKG facts here and the knowledge runtime's
	// StoreProvider / the leader's KnowledgeRetriever (read side) recall
	// them. In-memory by default; PostgreSQL when storage is configured.
	// Nil when AKG is not enabled (cfg.Knowledge.RetrievalEnabled).
	KnowledgeStore knowledge.KnowledgeStore
	// AKGBridge distills conversations into KnowledgeStore on task
	// lifecycle events (write side of the AKG loop). Nil when AKG or its
	// write dependencies (embedding client, experience repo) are
	// unavailable.
	AKGBridge *adapter.DistillBridge
	// FlightRecorder is the single shared flight recorder (collector
	// subscribes to comp.EventStore and emits workflow/scheduler/recovery
	// fitness evidence into the shared evidence store). It is created and
	// started by Bootstrap independently of ProvideEvolution so the fitness
	// write loop works even when the legacy evolution deps (ExpRepo) are
	// absent; ProvideEvolution and the serve launcher reuse it instead of
	// building their own. Nil when the event store is unavailable.
	FlightRecorder *flight.FlightRecorder
	// ExpRepo is the experience repository used by distillation writes
	// and — in the Agent Fabric runtime — as the spawn-prior source
	// (distillation output → experience repo query → spawn injection).
	// It is the deps.ExpRepo when provided, or the repository created by
	// wireDistillation when PostgreSQL distillation is enabled; nil otherwise
	// (callers treat nil as "no prior", never as an error).
	ExpRepo repositories.ExperienceRepositoryInterface
	// EvidenceStore is the shared evidence store used by the flight recorder
	// and (when enabled) the GA genomes. Always set, even when evolution is
	// disabled, so downstream consumers (cmd/ares serve, tests) can
	// reference it without nil guards.
	EvidenceStore evidence.Store
	// SystemRuntime is the system-level control plane: an
	// orchestrator that observes the assembled component graph and provides
	// lifecycle states, a shared root context, and status snapshots. It is
	// created at the end of Bootstrap; nil when wiring is skipped on failure.
	SystemRuntime *kernel.Orchestrator
	// SystemRegistry backs SystemRuntime with one entry per constructed
	// component, enabling dependency-aware lookup and snapshot queries.
	SystemRegistry *kernel.Registry
	// Observability holds the shared observability components:
	// the evolution trajectory tracer, the human-feedback store, and the
	// cross-Fabric tracer. All three are created together once in Bootstrap
	// and shared by the dashboard (read side) and the runtime write hooks
	// (GA generation recording, task/agent lifecycle tracing), so the
	// dashboard endpoints show live data. Non-nil whenever Bootstrap
	// completed; nil only when wiring never ran.
	Observability *ObservabilityComponents
	// ExpiryCleaners lists repositories that own TTL/decay purges.
	// Subsystems append entries when they construct a repo with retention
	// columns; startExpiryCleanupWorker purges them hourly on bgGroup. Empty
	// by default (no cleaners wired = no worker goroutine).
	ExpiryCleaners []NamedExpiryCleaner
	// bgGroup manages all Bootstrap background goroutines (distillation
	// subscriber, GA evolution ticker, LLM suggestion ticker) via errgroup
	// (no bare goroutines). WaitBackground blocks on it during shutdown.
	bgGroup errgroup.Group
}

// ObservabilityComponents groups the shared observability surfaces, constructed
// together as one subsystem — Bootstrap creates all three unconditionally and
// the dashboard reads them via provider adapters, so the flat Components struct
// stays scannable.
type ObservabilityComponents struct {
	// EvolutionTracer is the shared evolution trajectory tracer. Shared
	// by the dashboard (read side: /evolution/trajectory) and the GA
	// wiring (write side: Record after each generation).
	EvolutionTracer *aresrecovery.EvolutionTracer
	// FeedbackStore is the shared human-feedback store. Written by POST
	// /evolution/feedback; read by the evolution scoring path.
	FeedbackStore *aresrecovery.FeedbackStore
	// GlobalTracer is the shared cross-Fabric tracer. It is shared
	// shared by the dashboard (read side: /observability/spans) and the
	// kernel wiring (write side: task/agent lifecycle hooks). Nil when the
	// dashboard observability wiring is skipped.
	GlobalTracer *aresrecovery.GlobalTracer
}

// GoBackground runs fn as an errgroup-managed background goroutine on the
// Bootstrap group (no bare goroutines). A panic or a non-nil return is
// recovered, logged, and the worker is RESTARTED after a bounded backoff —
// a recover that merely logs and exits would leave the subsystem silently
// dead for the rest of the process while looking healthy. The loop stops
// only when ctx is cancelled or fn returns nil (a clean, intentional exit).
func (c *Components) GoBackground(ctx context.Context, name string, fn func(ctx context.Context) error) {
	c.bgGroup.Go(func() (err error) {
		const (
			initialBackoff = time.Second
			maxBackoff     = 30 * time.Second
		)
		backoff := initialBackoff
		for {
			err = runBackgroundOnce(ctx, name, fn)
			if err == nil || ctx.Err() != nil {
				return err
			}
			slog.Warn("bootstrap: background worker failed; restarting",
				"name", name, "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return err
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	})
}

// runBackgroundOnce invokes fn under a panic-recover boundary, converting a
// panic into an error so the GoBackground supervisor can restart the worker.
func runBackgroundOnce(ctx context.Context, name string, fn func(ctx context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("bootstrap: background worker panicked",
				"name", name, "panic", r)
			err = fmt.Errorf("background %s panicked: %v", name, r)
		}
	}()
	return fn(ctx)
}

// WaitBackground blocks until all background goroutines started by Bootstrap
// (distillation event subscriber, GA evolution ticker, LLM suggestion ticker)
// have exited. It must be called after the bootstrap context is cancelled;
// each goroutine exits on ctx.Done() and this ensures no goroutine is left
// running across a graceful shutdown.
func (c *Components) WaitBackground() {
	if c == nil {
		return
	}
	if err := c.bgGroup.Wait(); err != nil {
		log.Warn("bootstrap: background group error during shutdown", "error", err)
	}
}

// Snapshot returns the system-level component status snapshot.
// It returns an empty snapshot when the System Runtime
// registry is not wired (Bootstrap failed before wiring completed), so
// callers can always consume a valid value without nil guards.
func (c *Components) Snapshot() kernel.Snapshot {
	if c == nil || c.SystemRegistry == nil {
		return kernel.Snapshot{}
	}
	return c.SystemRegistry.Snapshot()
}

// ComponentStatus returns the status of one managed component by name.
// The bool is false when the component is not registered.
func (c *Components) ComponentStatus(name string) (kernel.ComponentStatus, bool) {
	if c == nil || c.SystemRegistry == nil {
		return kernel.ComponentStatus{}, false
	}
	return c.SystemRegistry.GetStatus(name)
}

// IsSystemReady reports whether all Required components reached Ready and no
// component is Failed. Returns false when the registry is not wired.
func (c *Components) IsSystemReady() bool {
	if c == nil || c.SystemRegistry == nil {
		return false
	}
	return c.SystemRegistry.IsReady()
}

// LLMComponents holds LLM client and callback registry.
type LLMComponents struct {
	Client      interface{}
	CallbackReg *ares_callbacks.Registry
	// CostDashboard is the cost surface served at
	// /api/v1/observability/cost*; fed by the LLM client's MetricsTracer.
	CostDashboard *observability.CostDashboard
}

// BootstrapDeps holds optional external dependencies for full wiring.
type BootstrapDeps struct {
	EventStore ares_events.EventStore
	ExpRepo    repositories.ExperienceRepositoryInterface
	LLMClient  eval.LLMClient
}

// Bootstrap assembles all components from config and optional dependencies.
// It is the single wiring hub — used by cmd/ares serve, and tests.
// On partial failure, already-created components are cleaned up in reverse
// order before returning the error. The heavy lifting lives in the
// bootstrapBuilder methods (bootstrap_builder.go); this function only orders
// the phases and maps phase errors to the caller.
func Bootstrap(ctx context.Context, cfg *ares_config.Config, deps *BootstrapDeps) (*Components, error) {
	if deps == nil {
		deps = &BootstrapDeps{}
	}

	// bctx scopes every background worker Bootstrap starts (bgGroup
	// goroutines, event subscribers, tickers). It is a child of the caller's
	// ctx, so on the SUCCESS path its cancellation semantics are identical:
	// the caller cancels ctx at shutdown and the workers observe it. The
	// derived context exists for the FAILURE path — runCleanups cancels it
	// so workers started before a late wiring error stop immediately instead
	// of lingering until the caller happens to cancel a ctx it may keep
	// alive (e.g. a serve loop that retries bootstrap).
	bctx, bcancel := context.WithCancel(ctx)

	comp := &Components{}
	b := &bootstrapBuilder{
		ctx:     ctx,
		cfg:     cfg,
		deps:    deps,
		comp:    comp,
		bctx:    bctx,
		bcancel: bcancel,
	}
	if err := b.assembleCore(); err != nil {
		return nil, err
	}
	if err := b.assembleExperience(); err != nil {
		return nil, err
	}
	if err := b.assembleEvolutionDAG(); err != nil {
		return nil, err
	}
	if err := b.assembleNewEvolution(); err != nil {
		return nil, err
	}
	if err := b.assembleLegacyEvolution(); err != nil {
		return nil, err
	}
	b.wireEvolutionWiring()
	if err := b.wirePlatform(); err != nil {
		return nil, err
	}
	return comp, nil
}

// wireMemory constructs the memory manager when cfg.Memory.IsEnabled() is true.
// Disabled = no goroutine, no event subscription, no store
// writes, so the gate is honored here instead of constructing unconditionally.
// The event store is wired during construction, eliminating
// the post-Bootstrap SetEventStore bypass in serve.go. Returns nil when disabled.
//
// (nil, nil) on the disabled path is the documented contract: every caller
// (Bootstrap and tests) probes `comp.Memory != nil` / `mem != nil` for the
// disabled state and nil-checks before optional SetSkillsRegistry-style
// wiring — none treats nil as an error.
//
//nolint:nilnil // nil manager + nil error is the documented "disabled" contract.
func wireMemory(cfg *ares_config.Config, eventStore ares_events.EventStore) (ares_memory.MemoryManager, error) {
	if !cfg.Memory.IsEnabled() {
		log.Info("bootstrap: memory disabled (cfg.Memory.IsEnabled()=false), skipping construction")
		return nil, nil
	}
	memCfg := ares_memory.DefaultMemoryConfig()
	if cfg.Memory.EnableRAG {
		memCfg.EnableRAG = true
		if cfg.Memory.RAGTopK > 0 {
			memCfg.RAGTopK = cfg.Memory.RAGTopK
		}
		if cfg.Memory.RAGMinScore > 0 {
			memCfg.RAGMinScore = cfg.Memory.RAGMinScore
		}
	}
	mem, err := ProvideMemory(memCfg)
	if err != nil {
		return nil, err
	}
	if eventStore != nil {
		mem.SetEventStore(eventStore, "memory")
	}
	return mem, nil
}

// buildEvolutionDAG builds the minimal mutable DAG used by the evolution system
// (workflow/scheduler/recovery genomes evolve against it). Returns nil when
// evolution is disabled so no graph is constructed behind the config's back.
//
// (nil, nil) on the disabled path is the documented contract: Bootstrap's
// continuation explicitly nil-checks (`comp.Runtime != nil && dag != nil`)
// before RegisterAgentDAG, so a nil DAG means "nothing to register", never
// an error state.
//
//nolint:nilnil // nil DAG + nil error is the documented "disabled" contract.
func buildEvolutionDAG(enabled bool) (*engine.MutableDAG, error) {
	if !enabled {
		return nil, nil
	}
	dagSteps := []*engine.Step{
		{ID: "input", Name: "Input", AgentType: "parser", Input: "parse input"},
		{ID: dagStepProcess, Name: "Process", AgentType: "processor", Input: dagStepProcess, DependsOn: []string{"input"}},
		{ID: "output", Name: "Output", AgentType: "formatter", Input: "format", DependsOn: []string{dagStepProcess}},
	}
	dag, err := engine.NewMutableDAG(dagSteps)
	if err != nil {
		return nil, fmt.Errorf("create mutable dag: %w", err)
	}
	return dag, nil
}

// resolveLiveMemoryStore returns the live memory config store from the
// constructed memory manager. Both *memoryManager and *ProductionMemoryManager
// implement MemoryConfigStore; when memory is disabled or the type assertion
// fails, the minimal manager is used so evolution still has a config store.
func resolveLiveMemoryStore(mem ares_memory.MemoryManager) ares_memory.MemoryConfigStore {
	if mem != nil {
		if store, ok := mem.(ares_memory.MemoryConfigStore); ok {
			return store
		}
	}
	return buildMemoryManager()
}

// wireNewEvolution constructs the runtime evolution system (Genome + Diff +
// Coordinator) when evolution is enabled, and always returns the shared
// evidence store: when disabled, a standalone store keeps the flight recorder's
// fitness evidence flowing without a NewEvolution instance.
//
// (nil components + nil error) on the disabled path is the documented
// contract: callers gate on `comp.NewEvolution != nil` (deployment wiring,
// wireGAEvolution) and the evidence store is ALWAYS non-nil, so nothing
// downstream can mistake the disabled state for a failure.
//
//nolint:nilnil // nil components + nil error is the documented "disabled" contract.
func wireNewEvolution(
	enabled bool,
	dag *engine.MutableDAG,
	rt *knowledgeruntime.KnowledgeRuntime,
	memoryStore ares_memory.MemoryConfigStore,
	evStore evidence.Store,
) (*NewEvolutionComponents, evidence.Store, error) {
	if !enabled {
		return nil, evidence.NewMemoryStore(), nil
	}
	newEvol, err := ProvideNewEvolution(dag, rt, memoryStore, evStore)
	if err != nil {
		return nil, nil, err
	}
	return newEvol, newEvol.EvidenceStore, nil
}

// wireLegacyEvolution wires the legacy evolution system when it is enabled and
// all required deps are present; otherwise it is skipped (nil), preserving
// prior behavior. Gated by cfg.Evolution.Enabled so the legacy scheduler
// cannot start behind the config's back.
//
// (nil, nil) on the skipped path is the documented contract: Bootstrap's
// continuation nil-checks (`if evol != nil`) before arming the scheduler
// shutdown goroutine, and bootstrap_steps.go re-asserts the scheduler type
// — the absence of a legacy scheduler is a supported configuration
// (wired-scheduler fallback), not a failure.
//
//nolint:nilnil // nil components + nil error is the documented "disabled" contract.
func wireLegacyEvolution(
	ctx context.Context,
	cfg *ares_config.Config,
	deps *BootstrapDeps,
	comp *Components,
) (*EvolutionComponents, error) {
	if !cfg.Evolution.Enabled || deps.EventStore == nil || deps.ExpRepo == nil {
		return nil, nil
	}
	return ProvideEvolution(ctx, &cfg.Evolution,
		comp.EventStore, deps.ExpRepo,
		deps.LLMClient,
		comp.FlightRecorder,
	)
}
