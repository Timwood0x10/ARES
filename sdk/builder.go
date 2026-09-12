package sdk

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	tools "github.com/Timwood0x10/ares/internal/apitools"
	ares_bootstrap "github.com/Timwood0x10/ares/internal/ares_bootstrap"
	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	apiembed "github.com/Timwood0x10/ares/internal/embedding"
	"github.com/Timwood0x10/ares/internal/kernel"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	llm "github.com/Timwood0x10/ares/internal/llmsvcapi"
	mcp "github.com/Timwood0x10/ares/internal/mcpclient"
	memory "github.com/Timwood0x10/ares/internal/runtime/memory"
	aresexp "github.com/Timwood0x10/ares/internal/runtime/memory/experience"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// sdkBuilder assembles a Runtime, holding the cross-cutting wiring state so the
// optional subsystems can be attached by small phase methods and the failure
// cleanup can release any resource created before a later phase errored. Method
// bodies are the original New() blocks moved verbatim; each reads/writes the
// builder fields under the original local names so bodies stay unchanged.
type sdkBuilder struct {
	cfg *config

	// Created up front (before the failure-cleanup defer is armed in New).
	llmSvc  *llm.Service
	toolReg *tools.Registry

	// Bootstrap lifecycle context: cancelled by Close on success, or by the
	// failure-cleanup path when a later phase errors.
	bootstrapCtx    context.Context
	bootstrapCancel context.CancelFunc
	// bootstrapCancelTaken is set on the success path so the failure-cleanup
	// defer leaves ownership with Runtime.Close.
	bootstrapCancelTaken bool

	// Mid-construction resources the failure cleanup must release.
	bootstrapComp  *ares_bootstrap.Components
	mcpClients     []*mcp.Client
	pgPool         *postgres.Pool
	knowStoreClose func()

	// Memory wiring outputs (nil-able: zero value when memory is disabled).
	memMgr         memory.MemoryManager
	distillCleanup func()
	embClient      apiembed.EmbeddingService
	expRepo        repositories.ExperienceRepositoryInterface
	distillSvc     *aresexp.DistillationService
	akgDistiller   adapter.ConversationDistiller

	// Cross-phase outputs not owned by a wiring struct.
	embModelForAKG string
	kw             *knowledgeWiring
	evoComponents  *ares_bootstrap.NewEvolutionComponents
	akgBridge      *adapter.DistillBridge
	eventStore     ares_events.EventStore
	rtCtx          context.Context
	rtCancel       context.CancelFunc
	eg             *errgroup.Group
}

// newSDKBuilder builds the LLM service and registry, then derives the Bootstrap
// lifecycle context. It returns an error only for the LLM construction (the one
// fallible step that runs before the failure-cleanup defer is armed).
func newSDKBuilder(cfg *config) (*sdkBuilder, error) {
	// ---- LLM ----
	llmCfg := &llm.Config{
		BaseConfig: cfg.baseCfg,
		LLMConfig:  cfg.llmCfg,
		Fallbacks:  cfg.fallbacks,
	}
	llmSvc, err := llm.NewService(llmCfg)
	if err != nil {
		return nil, FriendlyErr("llm", cfg.llmCfg.Provider, err)
	}
	toolReg := tools.NewRegistry()
	// The bootstrap ctx is cancelled in Close so Bootstrap's background
	// goroutines exit before WaitBackground drains them. Ownership is
	// transferred to the Runtime on the success path; on any error path the
	// deferred cancel prevents a context leak (vet lostcancel).
	bootstrapCtx, bootstrapCancel := context.WithCancel(context.Background())
	return &sdkBuilder{
		cfg:             cfg,
		llmSvc:          llmSvc,
		toolReg:         toolReg,
		bootstrapCtx:    bootstrapCtx,
		bootstrapCancel: bootstrapCancel,
	}, nil
}

// cleanupOnError releases everything created so far on a failure path. The
// success path sets bootstrapCancelTaken and hands ownership to Runtime.Close().
func (b *sdkBuilder) cleanupOnError() {
	b.bootstrapCancel()
	// Drain Bootstrap background goroutines (they exit on ctx.Done()) so
	// none outlives the failed construction, mirroring Runtime.Close().
	if b.bootstrapComp != nil {
		b.bootstrapComp.WaitBackground()
	}
	b.llmSvc.Close()
	for _, c := range b.mcpClients {
		_ = c.Close()
	}
	if b.distillCleanup != nil {
		b.distillCleanup()
	}
	if b.memMgr != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = b.memMgr.Stop(stopCtx)
	}
	if b.knowStoreClose != nil {
		b.knowStoreClose()
	}
	if b.pgPool != nil {
		_ = b.pgPool.Close()
	}
}

// assembleCore builds the Bootstrap-assembled core component graph (Stage 8):
// the SDK reuses the same EventStore / NewEvolution / System Runtime instances
// as serve and start. Falls back to SDK wiring when the config is not
// Bootstrap-capable or assembly fails, preserving prior behavior.
func (b *sdkBuilder) assembleCore() {
	b.bootstrapComp = newBootstrapCore(b.bootstrapCtx, b.cfg)
}

// wireMemoryPhase wires the production MemoryManager (compression + RAG +
// distillation) when memory is enabled.
func (b *sdkBuilder) wireMemoryPhase() error {
	if b.cfg.memCfg.Enabled {
		w, err := wireMemory(context.Background(), b.cfg)
		if err != nil {
			return fmt.Errorf("memory: %w", err)
		}
		b.memMgr = w.mgr
		b.embClient = w.embClient
		b.expRepo = w.expRepo
		b.distillCleanup = w.cleanup
		b.distillSvc = w.distillSvc
		b.akgDistiller = w.akgDistiller
	}
	return nil
}

// wireMCPPhase opens the configured MCP client connections.
func (b *sdkBuilder) wireMCPPhase() error {
	var err error
	b.mcpClients, err = wireMCPClients(b.cfg, b.toolReg)
	return err
}

// wireKnowledgePhase wires the AKF Knowledge Fabric, binds the Bootstrap
// NewEvolution's KnowledgePatchExecutor to the SDK runtime (Stage 9), and
// auto-registers the AKF knowledge tools.
func (b *sdkBuilder) wireKnowledgePhase() error {
	b.embModelForAKG = resolveAKGEmbeddingModel(b.cfg)
	kw, err := wireKnowledge(b.cfg, b.memMgr, b.embClient, b.embModelForAKG)
	if err != nil {
		return err
	}
	b.kw = kw
	if b.kw != nil && b.kw.store != nil {
		if closer, ok := b.kw.store.(interface{ Close() error }); ok {
			b.knowStoreClose = func() { _ = closer.Close() }
		}
	}
	// ---- Stage 9 (SDK unification): keep the SDK's own KnowledgeRuntime
	// (its providers carry the live memSearcher/embedding backends) and bind
	// the Bootstrap NewEvolution's KnowledgePatchExecutor to THAT instance via
	// UpdateLiveKnowledgeRuntime. This satisfies the sharing rule (KnowledgePatchExecutor
	// and AKF tools share one runtime) without replacing the SDK runtime with
	// the Bootstrap one, whose memory provider has no searcher.
	if b.bootstrapComp != nil && b.bootstrapComp.NewEvolution != nil && b.kw.rt != nil {
		b.bootstrapComp.NewEvolution.UpdateLiveKnowledgeRuntime(b.kw.rt)
	}
	// ---- AKF knowledge tools (auto-registered so the agent can call them) ----
	if b.cfg.knlCfg.Enabled && b.kw.rt != nil {
		if err := registerAKFTools(b.toolReg, b.kw.rt); err != nil {
			return fmt.Errorf("akf tools: %w", err)
		}
	}
	return nil
}

// wireEvolutionPhase wires the evolution hot-update + evidence store: reuse the
// Bootstrap-assembled NewEvolution when available, otherwise the SDK dual-track
// wiring fallback (wireSDKEvolution owns the evidence-persistence gating).
func (b *sdkBuilder) wireEvolutionPhase() error {
	var err error
	b.evoComponents, b.pgPool, err = wireSDKEvolution(b.cfg, b.kw, b.bootstrapComp)
	return err
}

// wireRetrieversAndBridge wires the RAG retrievers (best-effort, non-fatal) and
// the AKG DistillBridge write loop (conversations → knowledge store).
func (b *sdkBuilder) wireRetrieversAndBridge() {
	if b.cfg.memCfg.EnableRAG && b.memMgr != nil {
		wireSDKRetrievers(context.Background(), b.cfg, b.memMgr, b.embClient, b.expRepo,
			b.kw.rt, b.kw.store, b.embModelForAKG)
	}
	b.akgBridge = buildAKGBridge(b.cfg, b.akgDistiller, b.kw.store, b.embClient, b.embModelForAKG)
}

// wireEventBackendPhase sets up the event backend: when the Bootstrap core is
// available, subscribe distillation to Bootstrap's shared EventStore (single
// store across entry points) instead of a private SDK store; otherwise fall
// back to the SDK event backend.
func (b *sdkBuilder) wireEventBackendPhase() {
	b.rtCtx, b.rtCancel, b.eg, b.eventStore = wireSDKEventBackend(b.bootstrapComp, b.distillSvc, b.akgBridge)
}

// buildRuntime assembles the final Runtime from the builder's wiring outputs.
func (b *sdkBuilder) buildRuntime() *Runtime {
	return &Runtime{
		llmSvc:            b.llmSvc,
		toolReg:           b.toolReg,
		memMgr:            b.memMgr,
		distillCleanup:    b.distillCleanup,
		memEnabled:        b.cfg.memCfg.Enabled,
		evoEnabled:        b.cfg.evoCfg.Enabled,
		knowledgeEnabled:  b.cfg.knlCfg.Enabled,
		knowledgeRT:       b.kw.rt,
		knowledgeStore:    b.kw.store,
		evolutionStore:    b.kw.evolutionStore,
		evoComponents:     b.evoComponents,
		eventStore:        b.eventStore,
		mcpClients:        b.mcpClients,
		trace:             b.cfg.trace,
		gov:               b.cfg.gov,
		bootstrap:         b.bootstrapComp,
		bootstrapCancel:   b.bootstrapCancel,
		evidencePool:      b.pgPool,
		ctx:               b.rtCtx,
		cancel:            b.rtCancel,
		eg:                b.eg,
		distillSvc:        b.distillSvc,
		akgBridge:         b.akgBridge,
		agentByCapability: make(map[string]*Agent),
		sdkExecutors:      make(map[string]kernel.CapabilityExecutor),
	}
}
