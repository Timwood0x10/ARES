// package bootstrap provides startup wiring for MCP and Dashboard components.
package ares_bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	ares_runtime "github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/dashboard"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"

	"golang.org/x/sync/errgroup"
)

// MCPDashboard holds the initialized MCP and Dashboard components.
type MCPDashboard struct {
	MCPManager *ares_mcp.MCPManager
	HTTPServer *http.Server
	hub        *dashboard.WSHub
	hubEG      *errgroup.Group // manages hub.Run() lifecycle; must call Wait() on shutdown.
	bridge     *dashboard.EventBridge
}

// SetupMCP initializes the MCP manager from ares_config and connects to servers.
func SetupMCP(ctx context.Context, cfg *ares_config.MCPConfig, registry *core.Registry) (*ares_mcp.MCPManager, error) {
	if cfg == nil || len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("bootstrap: MCP not configured")
	}

	managerConfig := &ares_mcp.MCPManagerConfig{
		Servers: make([]ares_mcp.MCPServerConfig, 0, len(cfg.Servers)),
	}

	for _, s := range cfg.Servers {
		sc := ares_mcp.MCPServerConfig{
			Name:      s.Name,
			Enabled:   s.Enabled,
			AutoStart: s.AutoStart,
			Timeout:   time.Duration(s.Timeout) * time.Second,
			Transport: ares_mcp.TransportConfig{
				Type: s.Transport.Type,
			},
		}

		switch s.Transport.Type {
		case "stdio":
			if s.Transport.Stdio != nil {
				sc.Transport.Stdio = &ares_mcp.StdioConfig{
					Command: s.Transport.Stdio.Command,
					Args:    s.Transport.Stdio.Args,
					Env:     s.Transport.Stdio.Env,
					WorkDir: s.Transport.Stdio.WorkDir,
				}
			}
		case "sse":
			if s.Transport.SSE != nil {
				sc.Transport.SSE = &ares_mcp.SSEConfig{
					URL:     s.Transport.SSE.URL,
					Headers: s.Transport.SSE.Headers,
					Timeout: time.Duration(s.Transport.SSE.Timeout) * time.Second,
				}
			}
		}

		managerConfig.Servers = append(managerConfig.Servers, sc)
	}

	manager, err := ares_mcp.NewMCPManager(managerConfig, registry)
	if err != nil {
		return nil, fmt.Errorf("create ares_mcp manager: %w", err)
	}

	if err := manager.Start(ctx); err != nil {
		return nil, fmt.Errorf("start ares_mcp manager: %w", err)
	}

	log.Info("bootstrap: ares_mcp manager started", "servers", len(cfg.Servers))
	return manager, nil
}

// SetupDashboard initializes the dashboard service, WebSocket hub, and HTTP server.
func SetupDashboard(
	ctx context.Context,
	cfg *ares_config.DashboardAppConfig,
	rt ares_runtime.Runtime,
	agents dashboard.AgentProvider,
	eventStore ares_events.EventStore,
	memMgr memory.MemoryManager,
	mcpMgr dashboard.MCPStatusProvider,
) (*MCPDashboard, error) {
	if cfg == nil || cfg.Addr == "" {
		return nil, fmt.Errorf("bootstrap: dashboard not configured")
	}

	dashConfig := &dashboard.DashboardConfig{
		Addr:           cfg.Addr,
		EnableAuth:     cfg.EnableAuth,
		WSPingInterval: time.Duration(cfg.WSPingInterval) * time.Second,
	}
	if dashConfig.WSPingInterval == 0 {
		dashConfig.WSPingInterval = 30 * time.Second
	}

	hub := dashboard.NewWSHub()

	// Start hub via errgroup so its lifecycle is managed by the caller's eg.Wait().
	hubGrp, hubCtx := errgroup.WithContext(ctx)
	hubGrp.Go(func() error {
		hub.Run()
		// hub.Run() blocks until hub.Stop() is called or all clients disconnect.
		// Return ctx error if cancelled while running.
		return hubCtx.Err()
	})

	// Start event bridge if event store is available.
	var bridge *dashboard.EventBridge
	if eventStore != nil {
		bridge = dashboard.NewEventBridge(eventStore, hub)
		if err := bridge.Start(ctx); err != nil {
			log.Warn("bootstrap: event bridge start failed", "error", err)
		}
	}

	api := dashboard.NewAPIv2(nil, mcpMgr, hub)

	httpServer := &http.Server{
		Addr:         cfg.Addr,
		Handler:      api.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Info("bootstrap: dashboard initialized", "addr", cfg.Addr)

	return &MCPDashboard{
		MCPManager: nil,
		HTTPServer: httpServer,
		hub:        hub,
		hubEG:      hubGrp,
		bridge:     bridge,
	}, nil
}

// StartDashboard starts the dashboard HTTP server via the provided errgroup.
// The caller's errgroup.Wait() will propagate any listen error (except ErrServerClosed).
func StartDashboard(ctx context.Context, md *MCPDashboard, eg *errgroup.Group) error {
	if md == nil || md.HTTPServer == nil {
		return nil
	}
	eg.Go(func() error {
		if err := md.HTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("dashboard server: %w", err)
		}
		return nil
	})
	return nil
}

// StopDashboard gracefully shuts down the dashboard HTTP server, hub, and bridge.
func StopDashboard(ctx context.Context, md *MCPDashboard) error {
	if md == nil {
		return nil
	}

	if md.bridge != nil {
		md.bridge.Stop()
	}

	// Wait for hub errgroup to finish before stopping the hub.
	if md.hubEG != nil {
		if err := md.hubEG.Wait(); err != nil && err != ctx.Err() {
			log.Warn("bootstrap: hub errgroup error", "error", err)
		}
	}

	if md.hub != nil {
		md.hub.Stop()
	}

	if md.HTTPServer != nil {
		return md.HTTPServer.Shutdown(ctx)
	}
	return nil
}

// NewCallbackRegistry creates a shared callback registry for lifecycle event hooks.
// This registry should be passed to LLM Client, TaskExecutor, and LeaderAgent
// via their respective option functions (WithCallbacks).
//
// Returns:
//
//	*ares_callbacks.Registry - a new registry instance ready for handler registration.
func NewCallbackRegistry() *ares_callbacks.Registry {
	return ares_callbacks.NewRegistry()
}

// NewLLMClientWithCallbacks creates an LLM client with callback emission enabled.
// The returned client will emit lifecycle ares_events (llm.start, llm.end, llm.error)
// to the provided registry during Generate and GenerateStream calls.
//
// Args:
//
//	ares_config - LLM client configuration (provider, API key, model, etc.).
//	reg - callback registry to receive lifecycle ares_events. May be nil to skip wiring.
//
// Returns:
//
//	*llm.Client - configured LLM client with ares_callbacks wired.
//	error - configuration or client creation error.
func NewLLMClientWithCallbacks(ares_config *llm.Config, reg *ares_callbacks.Registry) (*llm.Client, error) {
	client, err := llm.NewClient(ares_config)
	if err != nil {
		return nil, err
	}
	if reg != nil {
		llm.WithCallbacks(reg)(client)
	}
	return client, nil
}

// NewLLMClientWithFailover creates an LLM client with automatic provider failover.
// When fallbacks are provided, a FailoverClient is returned that tries each
// provider in order and cools down rate-limited providers for 60 seconds.
// When fallbacks is empty, a single Client is returned (same as NewLLMClientWithCallbacks).
//
// Args:
//
//	ares_config   - primary LLM client configuration.
//	fallbacks - fallback LLM configs; may be nil/empty for no failover.
//	reg      - callback registry to receive lifecycle ares_events. May be nil.
//
// Returns:
//
//	*llm.FailoverClient - failover-capable LLM client.
//	error - configuration or client creation error.
func NewLLMClientWithFailover(ares_config *llm.Config, fallbacks []*llm.Config, reg *ares_callbacks.Registry) (*llm.FailoverClient, error) {
	configs := append([]*llm.Config{ares_config}, fallbacks...)
	fc, err := llm.NewFailoverClient(configs, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	if reg != nil {
		for _, c := range fc.Clients() {
			llm.WithCallbacks(reg)(c)
		}
	}
	return fc, nil
}

// WireTaskExecutorCallbacks returns a TaskExecutorOption that injects a callback emitter.
// Pass this option to sub.NewTaskExecutorWithValidation() to enable lifecycle event
// emission (tool.start, tool.end, tool.error) during task execution.
//
// This is the type-safe alternative to ares_runtime interface assertion — ares_callbacks
// are wired at construction time rather than injected post-hoc via SetCallbacks.
//
// Args:
//
//	reg - callback registry to receive lifecycle ares_events. May be nil to return no-op option.
//
// Returns:
//
//	sub.TaskExecutorOption - option function for sub.NewTaskExecutorWithValidation().
func WireTaskExecutorCallbacks(reg *ares_callbacks.Registry) sub.TaskExecutorOption {
	if reg == nil {
		return nil
	}
	return sub.WithTaskExecutorCallbacks(reg)
}

// WireLeaderAgentCallbacks returns a LeaderOption that injects a callback emitter
// into a leader Agent. Pass this option to leader.New() to enable lifecycle event
// emission (agent.start, agent.end, agent.error) on the leader agent.
//
// Args:
//
//	reg - callback registry to receive lifecycle ares_events. May be nil to return no-op option.
//
// Returns:
//
//	leader.LeaderOption - option function for leader.New().
func WireLeaderAgentCallbacks(reg *ares_callbacks.Registry) leader.LeaderOption {
	if reg == nil {
		return nil
	}
	return leader.WithCallbacks(reg)
}

// EvolutionComponents holds the initialized evolution components for continuous learning.
type EvolutionComponents struct {
	Adapter    *evolution.FlightToExperienceAdapter
	Scheduler  *evolution.EvolutionScheduler
	DreamCycle *evolution.DreamCycle
}

// DreamCycleDeps holds optional dependencies for initializing the DreamCycle orchestrator.
type DreamCycleDeps struct {
	Mutator   evolution.MutatorInterface
	Tester    evolution.TesterInterface
	Genealogy evolution.GenealogyRecorder
}

// SetupEvolution initializes the evolution system that converts flight recorder
// diagnostics into experiences. It creates the FlightToExperienceAdapter and
// EvolutionScheduler, registers the scheduler with the callback registry,
// and optionally wires in a DreamCycle orchestrator for full autonomous evolution.
//
// Args:
//
//	ctx - operation context.
//	flightRecorder - the flight recorder for accessing diagnostics.
//	expRepo - the experience repository for persisting experiences.
//	callbackReg - the callback registry for registering event handlers.
//	dreamDeps - optional dependencies for dream cycle (mutator, tester, genealogy).
//	  When all three are non-nil, a DreamCycle is created and attached to the scheduler.
//	opts - optional scheduler configuration options.
//
// Returns:
//
//	*EvolutionComponents - the initialized evolution components, or nil if not configured.
//	error - any error encountered during initialization.
func SetupEvolution(
	ctx context.Context,
	flightRecorder *flight.FlightRecorder,
	expRepo evolution.ExperienceRepository,
	callbackReg *ares_callbacks.Registry,
	dreamDeps *DreamCycleDeps,
	cfg *ares_config.EvolutionConfig, // <-- 新增：GA 参数来源
	opts ...evolution.SchedulerOption,
) (*EvolutionComponents, error) {
	if flightRecorder == nil || expRepo == nil || callbackReg == nil {
		log.InfoContext(ctx, "bootstrap: evolution skipped (missing dependencies)")
		return nil, fmt.Errorf("bootstrap: evolution skipped (missing dependencies)")
	}

	// Create adapter wrapper to bridge flight recorder interfaces with evolution package.
	flightWrapper := &flightRecorderWrapper{recorder: flightRecorder}
	adapter := evolution.NewFlightToExperienceAdapter(flightWrapper, expRepo)

	// Build scheduler options from ares_config or use defaults.
	schedulerOpts := []evolution.SchedulerOption{
		evolution.WithEnabled(true),
	}
	if cfg != nil && cfg.MinInterval != "" {
		if d, err := time.ParseDuration(cfg.MinInterval); err == nil {
			schedulerOpts = append(schedulerOpts, evolution.WithMinInterval(d))
		} else {
			log.WarnContext(ctx, "bootstrap: invalid min_interval format, using default", "value", cfg.MinInterval, "error", err)
			schedulerOpts = append(schedulerOpts, evolution.WithMinInterval(5*time.Minute))
		}
	} else {
		schedulerOpts = append(schedulerOpts, evolution.WithMinInterval(5*time.Minute))
	}

	scheduler := evolution.NewEvolutionScheduler(callbackReg, adapter, schedulerOpts...)

	// Register scheduler handlers to callback registry.
	scheduler.Register()

	// Create dream cycle orchestrator if all required dependencies are provided.
	var dreamCycle *evolution.DreamCycle
	if dreamDeps != nil && dreamDeps.Mutator != nil && dreamDeps.Tester != nil {
		var err error
		dreamCycle, err = evolution.NewDreamCycle(
			scheduler,
			dreamDeps.Mutator,
			dreamDeps.Tester,
			dreamDeps.Genealogy, // may be nil; lineage recording will be skipped.
			evolution.WithDreamCycleConfig(evolution.DefaultDreamCycleConfig()),
		)
		if err != nil {
			return nil, fmt.Errorf("create dream cycle: %w", err)
		}
		// Wire dreamCycle into scheduler as the evolution handler.
		scheduler.SetDreamCycle(dreamCycle)

		log.InfoContext(ctx, "bootstrap: dream cycle initialized and attached to scheduler")
	}

	log.InfoContext(ctx, "bootstrap: evolution system initialized",
		"min_interval", schedulerOpts[1], // 使用实际配置的值
		"enabled", true,
		"dream_cycle", dreamCycle != nil)

	return &EvolutionComponents{
		Adapter:    adapter,
		Scheduler:  scheduler,
		DreamCycle: dreamCycle,
	}, nil
}

// SetupFeedbackService creates the FeedbackService for bandit experience reinforcement.
// It wires the experience repository to the feedback service so that task outcomes
// can update experience metrics (usage count / rank score).
//
// Args:
//
//	expRepo - the experience repository for persisting feedback data.
//
// Returns:
//
//	*experience.FeedbackService - the configured feedback service, or nil if not configured.
func SetupFeedbackService(expRepo repositories.ExperienceRepositoryInterface) *experience.FeedbackService {
	if expRepo == nil {
		log.Info("bootstrap: feedback service skipped (no experience repo)")
		return nil
	}

	svc := experience.NewFeedbackService(expRepo)
	log.Info("bootstrap: feedback service initialized")
	return svc
}

// SetupEvaluators creates and registers built-in evaluators for agent testing.
// It initializes the LLM-as-Judge evaluator and registers it with the provided registry.
// The llm.Client directly satisfies ares_eval.LLMClient (same Generate signature),
// so no adapter wrapper is needed.
//
// Args:
//
//	llmClient - the LLM client for judge model inference (must not be nil).
//	registry - the evaluator registry to register evaluators into.
//
// Returns:
//
//	error - nil on success, or error if evaluator creation/registration fails.
func SetupEvaluators(llmClient ares_eval.LLMClient, registry *ares_eval.EvaluatorRegistry) error {
	if llmClient == nil || registry == nil {
		log.Info("bootstrap: evaluators skipped (missing dependencies)")
		return nil
	}

	// Create LLM Judge evaluator with Chinese prompt and 1-10 scale.
	judge, err := ares_eval.NewLLMJudgeEvaluator(llmClient,
		ares_eval.WithChinesePrompt(),
		ares_eval.WithScale(ares_eval.ScaleOneToTen),
	)
	if err != nil {
		return fmt.Errorf("create llm judge: %w", err)
	}

	if err := registry.Register("llm_judge", judge); err != nil {
		return fmt.Errorf("register llm judge: %w", err)
	}

	log.Info("bootstrap: evaluators initialized",
		"evaluators", registry.Names(),
	)
	return nil
}

// SetupEvalSystem creates an EvaluatorRegistry, registers built-in evaluators,
// and returns the registry ready for injection into test runners.
// This is a convenience wrapper around SetupEvaluators that handles registry creation.
//
// Args:
//
//	llmClient - the LLM client for judge model inference (may be nil to skip LLM judge).
//
// Returns:
//
//	*ares_eval.EvaluatorRegistry - the populated registry, or empty registry if llmClient is nil.
//	error - any error during evaluator creation or registration.
func SetupEvalSystem(llmClient ares_eval.LLMClient) (*ares_eval.EvaluatorRegistry, error) {
	registry := ares_eval.NewEvaluatorRegistry()

	if llmClient != nil {
		if err := SetupEvaluators(llmClient, registry); err != nil {
			return registry, fmt.Errorf("setup evaluators: %w", err)
		}
	}

	return registry, nil
}

// flightRecorderWrapper wraps flight.FlightRecorder to implement evolution.FlightRecorder interface.
type flightRecorderWrapper struct {
	recorder *flight.FlightRecorder
}

// Diagnostics returns access to diagnostic reports.
func (w *flightRecorderWrapper) Diagnostics() evolution.DiagnosticsAccessor {
	return &diagnosticsAccessorWrapper{engine: w.recorder.Diagnostics()}
}

// EventStore returns the event store subscriber.
func (w *flightRecorderWrapper) EventStore() evolution.EventStoreSubscriber {
	return &eventStoreSubscriberWrapper{store: w.recorder.EventStoreRef()}
}

// diagnosticsAccessorWrapper wraps flight.DiagnosticsEngine to implement evolution.DiagnosticsAccessor.
type diagnosticsAccessorWrapper struct {
	engine *flight.DiagnosticsEngine
}

// Get retrieves the diagnostic report for a specific agent.
func (w *diagnosticsAccessorWrapper) Get(agentID string) *evolution.DiagnosticsReport {
	if w.engine == nil {
		return nil
	}

	records := w.engine.FilterByAgent(agentID)
	if len(records) == 0 {
		return nil
	}

	diagRecords := make([]evolution.DiagnosticRecord, len(records))
	for i, r := range records {
		diagRecords[i] = evolution.DiagnosticRecord{
			ID:         r.ID,
			AgentID:    r.AgentID,
			TaskID:     r.TaskID,
			Category:   string(r.Category),
			RootCause:  r.RootCause,
			Suggestion: r.Suggestion,
			Severity:   categorizeSeverity(r.Category),
		}
	}

	return &evolution.DiagnosticsReport{
		AgentID:   agentID,
		Records:   diagRecords,
		HasIssues: true,
	}
}

// categorizeSeverity converts DiagnosticCategory to a severity score (1-10).
func categorizeSeverity(cat flight.DiagnosticCategory) int {
	switch cat {
	case flight.DiagToolTimeout:
		return 5
	case flight.DiagLLMError:
		return 7
	case flight.DiagParseError:
		return 4
	case flight.DiagMemoryError:
		return 6
	case flight.DiagNetworkError:
		return 6
	case flight.DiagConfigError:
		return 3
	case flight.DiagConcurrencyError:
		return 8
	default:
		return 5
	}
}

// eventStoreSubscriberWrapper wraps ares_events.EventStore to implement evolution.EventStoreSubscriber.
type eventStoreSubscriberWrapper struct {
	store ares_events.EventStore
}

// Subscribe subscribes to ares_events from the underlying event store.
func (w *eventStoreSubscriberWrapper) Subscribe(ctx context.Context, filter ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	if w.store == nil {
		return nil, fmt.Errorf("event store is nil")
	}
	return w.store.Subscribe(ctx, filter)
}

// ============================================================
// Unified Wiring: WireAllEvolutionComponents
// ============================================================

// WiredComponents holds all evolution-related components produced by
// WireAllEvolutionComponents. Callers use these fields to inject
// ares_callbacks, feedback service, and evaluators into agents at construction time.
type WiredComponents struct {
	// CallbackReg is the shared callback registry for lifecycle ares_events.
	// Pass to llm.WithCallbacks(reg) and leader.WithCallbacks(reg).
	CallbackReg *ares_callbacks.Registry

	// FeedbackSvc is the bandit feedback service for experience reinforcement.
	// Pass to leader.WithFeedbackService(svc).
	FeedbackSvc *experience.FeedbackService

	// EvalRegistry is the evaluator registry with LLM Judge registered.
	// Pass to ares_eval.NewAgentTestRunner(executor).SetRegistry(reg).
	EvalRegistry *ares_eval.EvaluatorRegistry

	// ExperienceLocator is the planner option for UsedExperienceID tracking.
	// Pass to leader.NewTaskPlannerWithConfig(..., planner.WithExperienceLocator(wired.ExperienceLocator)).
	ExperienceLocator leader.ExperienceLocator

	// DistillerSetter wires the experience store adapter into an existing Distiller.
	// Call after distillation.NewDistiller() returns: wired.DistillerSetter(d).
	DistillerSetter func(*distillation.Distiller)

	// Evolution holds the evolution system components (adapter, scheduler, dream cycle).
	// May be nil if flight recorder or experience repo is not configured.
	Evolution *EvolutionComponents
}

// WireDependencies collects all dependencies required by WireAllEvolutionComponents.
// Fields marked as optional may be nil; the wiring function degrades gracefully.
type WireDependencies struct {
	// LLMClient is the LLM client used by the judge evaluator (required for evaluators).
	// Satisfied by both *llm.Client and *llm.FailoverClient.
	LLMClient ares_eval.LLMClient

	// FlightRecorder is the diagnostics recorder (required for evolution system).
	FlightRecorder *flight.FlightRecorder

	// ExpRepo is the experience repository for feedback and evolution persistence.
	ExpRepo repositories.ExperienceRepositoryInterface

	// EmbeddingService is the optional embedding service for experience retrieval.
	// When set, an ExperienceLocator is created and returned in WiredComponents.
	EmbeddingService EmbeddingService

	// Distiller is the optional distiller to wire with the experience store.
	// When set, WithExperienceStore is called on it to enable memory→experience sync.
	Distiller *distillation.Distiller

	// DreamDeps holds optional dream cycle dependencies (mutator, tester, genealogy).
	// When all three are non-nil, a DreamCycle orchestrator is created.
	DreamDeps *DreamCycleDeps
}

// EmbeddingService defines the interface for generating embeddings.
// This allows bootstrap to create ExperienceLocators for planner injection.
type EmbeddingService interface {
	// Embed generates a vector embedding for the given text.
	Embed(ctx context.Context, text string) ([]float64, error)
}

// evolutionExpRepoAdapter wraps repositories.ExperienceRepositoryInterface to satisfy
// evolution.ExperienceRepository. This bridges the postgres repository layer
// with the evolution package's domain-specific Experience type.
type evolutionExpRepoAdapter struct {
	repo repositories.ExperienceRepositoryInterface
}

// Create converts an evolution.Experience to storage_models.Experience and persists it.
func (a *evolutionExpRepoAdapter) Create(ctx context.Context, exp *evolution.Experience) error {
	if a.repo == nil {
		return fmt.Errorf("experience repo is nil")
	}

	storageExp := convertToStorageExperience(exp)
	return a.repo.Create(ctx, storageExp)
}

// convertToStorageExperience maps evolution domain Experience to persistence model.
// Field mapping:
//   - TenantID → TenantID (multi-tenancy isolation)
//   - Type → Type (experience category)
//   - Problem → Problem (DB 'input' column)
//   - Solution → Solution (DB 'output' column)
//   - Score → Score (importance 0-1)
//   - AgentID → AgentID + Metadata["agent_id"] (dual storage for queryability)
//   - Source → Metadata["source"] (origin traceability)
//   - Metadata → merged into existing Metadata
//   - Success → derived from Score > 0.5 threshold
//   - CreatedAt → current UTC timestamp
func convertToStorageExperience(exp *evolution.Experience) *models.Experience {
	if exp == nil {
		return nil
	}

	metadata := make(map[string]interface{}, len(exp.Metadata)+4)
	for k, v := range exp.Metadata {
		metadata[k] = v
	}
	metadata["agent_id"] = exp.AgentID
	metadata["source"] = exp.Source

	return &models.Experience{
		TenantID:  exp.TenantID,
		Type:      exp.Type,
		Problem:   exp.Problem,
		Solution:  exp.Solution,
		Score:     exp.Score,
		AgentID:   exp.AgentID,
		Metadata:  metadata,
		Success:   exp.Score > 0.5,
		CreatedAt: time.Now().UTC(),
	}
}

// ExperienceWireResult holds the wired options for injecting FeedbackService into
// LeaderAgent and ExperienceStore into Distiller.
//
// Usage:
//
//	result := bootstrap.WireExperienceSystem(expRepo)
//	agent := leader.New(..., result.FeedbackOption)  // pass at construction time
//	distiller := distillation.NewDistiller(...)
//	result.DistillerSetter(distiller)                 // call after creation
type ExperienceWireResult struct {
	// FeedbackOption is a leader.LeaderOption that injects FeedbackService.
	// Must be passed to leader.New() at construction time.
	FeedbackOption leader.LeaderOption

	// DistillerSetter wires the experience store adapter into an existing Distiller.
	// Call after distillation.NewDistiller() returns.
	DistillerSetter func(*distillation.Distiller)
}

// WireExperienceSystem creates and wires feedback service and experience store
// into their respective consumers (LeaderAgent + Distiller).
//
// This is a convenience wrapper that:
//  1. Creates a FeedbackService from the experience repository.
//  2. Creates an ExperienceStoreAdapter that bridges repositories.ExperienceRepositoryInterface
//     to distillation.ExperienceStore so the distiller can write distilled memories back
//     as experiences.
//
// Args:
//
//	expRepo - the experience repository for persisting feedback and experiences.
//
// Returns:
//
//	*ExperienceWireResult - containing setter functions, or nil if expRepo is nil.
func WireExperienceSystem(expRepo repositories.ExperienceRepositoryInterface) *ExperienceWireResult {
	if expRepo == nil {
		return nil
	}

	svc := SetupFeedbackService(expRepo)
	adapter := NewExperienceStoreAdapter(expRepo)

	return &ExperienceWireResult{
		FeedbackOption: leader.WithFeedbackService(svc),
		DistillerSetter: func(d *distillation.Distiller) {
			d.WithExperienceStore(adapter)
		},
	}
}

// experienceStoreAdapter adapts repositories.ExperienceRepositoryInterface to
// distillation.ExperienceStore so the distiller can persist distilled memories
// as experiences in the main experience table.
//
// The conversion maps distillation.StoredExperience fields to storage_models.Experience:
//   - TenantID → TenantID
//   - Type → Type
//   - Problem → Problem (stored in DB 'input' column)
//   - Solution → Solution (stored in DB 'output' column)
//   - Score → Score
//   - Source → Metadata["source"]
//   - Metadata → merged into existing Metadata
type experienceStoreAdapter struct {
	repo repositories.ExperienceRepositoryInterface
}

// NewExperienceStoreAdapter creates an adapter that implements distillation.ExperienceStore
// by delegating to a repositories.ExperienceRepositoryInterface with type conversion.
//
// Args:
//
//	repo - the experience repository for persisting data.
//
// Returns:
//
//	distillation.ExperienceStore - the adapter satisfying the distiller's store interface.
func NewExperienceStoreAdapter(repo repositories.ExperienceRepositoryInterface) distillation.ExperienceStore {
	return &experienceStoreAdapter{repo: repo}
}

// Create persists a distilled memory as an experience by converting StoredExperience
// to storage_models.Experience and delegating to the underlying repository.
//
// Args:
//
//	ctx - operation context.
//	exp - the distilled experience to persist.
//
// Returns:
//
//	error - nil on success, or error if persistence fails.
func (a *experienceStoreAdapter) Create(ctx context.Context, exp *distillation.StoredExperience) error {
	if exp == nil {
		return fmt.Errorf("experience must not be nil")
	}

	metadata := make(map[string]interface{}, len(exp.Metadata)+2)
	for k, v := range exp.Metadata {
		metadata[k] = v
	}
	// Source from StoredExperience is stored in metadata for traceability.
	if exp.Source != "" {
		metadata["source"] = exp.Source
	}

	model := &models.Experience{
		// TenantID: multi-tenancy isolation identifier.
		TenantID: exp.TenantID,
		// Type: experience category (solution, heuristic, strategy, etc.).
		Type: exp.Type,
		// Problem: the problem statement (stored in DB 'input' column).
		Problem: exp.Problem,
		// Solution: the solution approach (stored in DB 'output' column).
		Solution: exp.Solution,
		// Score: importance score (0-1) from distillation importance scorer.
		Score: exp.Score,
		// Success: derived from score threshold; distilled memories are considered successful.
		Success:   exp.Score > 0.5,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
	}

	return a.repo.Create(ctx, model)
}

// experienceLocatorCtxKey is the context key that holds the ExperienceLocator cache.
type experienceLocatorCtxKey struct{}

// locatorCache holds cached embedding results for ExperienceLocator lookups.
type locatorCache struct {
	mu       sync.RWMutex
	ctx      context.Context
	expRepo  repositories.ExperienceRepositoryInterface
	embedder EmbeddingService
}

// lookup queries the experience repository for the best matching experience ID.
// Results are cached per input text within the same context lifecycle.
func (lc *locatorCache) lookup(ctx context.Context, inputText string) string {
	lc.mu.RLock()
	cached, ok := lc.ctx.Value(experienceLocatorCtxKey{}).(string)
	lc.mu.RUnlock()
	if ok && cached != "" {
		return cached
	}

	// Generate embedding for the input text.
	vector, err := lc.embedder.Embed(ctx, inputText)
	if err != nil || len(vector) == 0 {
		log.DebugContext(ctx, "experience locator: embedding failed", "error", err)
		return ""
	}

	// Search for similar experiences.
	results, err := lc.expRepo.SearchByVector(ctx, vector, "", 1)
	if err != nil || len(results) == 0 {
		return ""
	}

	return results[0].ID
}

// NewExperienceLocator creates an ExperienceLocator that finds the best matching
// experience for a given input text using vector similarity search.
//
// The returned ExperienceLocator can be passed to the planner via
// planner.WithExperienceLocator() to enable UsedExperienceID tracking.
//
// Args:
//
//	ctx - operation context for embedding calls.
//	expRepo - the experience repository for vector search.
//	embedder - the embedding service for vectorizing input text.
//
// Returns:
//
//	leader.ExperienceLocator - the locator function, or nil if dependencies are missing.
func NewExperienceLocator(
	ctx context.Context,
	expRepo repositories.ExperienceRepositoryInterface,
	embedder EmbeddingService,
) leader.ExperienceLocator {
	if expRepo == nil || embedder == nil {
		return nil
	}

	cache := &locatorCache{
		ctx:      ctx,
		expRepo:  expRepo,
		embedder: embedder,
	}

	return func(inputText string) string {
		return cache.lookup(ctx, inputText)
	}
}

// WireAllEvolutionComponents is the single entry point for initializing the full
// evolution system. It creates and wires together all evolution-related components:
//
//  1. Creates shared CallbackRegistry for lifecycle event hooks.
//  2. Creates FeedbackService from experience repository.
//  3. Creates EvaluatorRegistry and registers LLMJudgeEvaluator.
//  4. Creates FlightToExperienceAdapter + EvolutionScheduler (+ optional DreamCycle).
//  5. Returns all wired components in a WiredComponents struct.
//
// This function should be called from main() after all base dependencies
// (LLM client, flight recorder, experience repo) are initialized.
// The returned WiredComponents fields are then used as construction options
// when creating agents (e.g., leader.WithCallbacks(wired.CallbackReg)).
//
// Args:
//
//	ctx - operation context for logging and cancellation.
//	deps - all required dependencies for wiring.
//
// Returns:
//
//	*WiredComponents - all wired components ready for use (never nil).
//	error - any error during wiring (evaluator creation or evolution setup failure).
func WireAllEvolutionComponents(
	ctx context.Context,
	deps *WireDependencies,
	cfg *ares_config.EvolutionConfig, // <-- 新增：可以是 nil（向后兼容）
) (*WiredComponents, error) {
	result := &WiredComponents{}

	// Step 1: Always create callback registry — it is the backbone for all event wiring.
	result.CallbackReg = NewCallbackRegistry()

	// Step 2: Create feedback service if experience repo is available.
	result.FeedbackSvc = SetupFeedbackService(deps.ExpRepo)

	// Step 3: Create evaluator registry and register built-in evaluators.
	result.EvalRegistry = ares_eval.NewEvaluatorRegistry()
	if deps.LLMClient != nil {
		if err := SetupEvaluators(deps.LLMClient, result.EvalRegistry); err != nil {
			return nil, fmt.Errorf("setup evaluators: %w", err)
		}
	} else {
		log.InfoContext(ctx, "bootstrap: wire evaluators skipped (no llm client)")
	}

	// Step 4: Create ExperienceLocator for UsedExperienceID tracking.
	// This enables the planner to record which experience was used for each task.
	result.ExperienceLocator = NewExperienceLocator(ctx, deps.ExpRepo, deps.EmbeddingService)

	// Step 5: Create DistillerSetter for memory→experience sync.
	// This wires the experience store adapter into the distiller when provided.
	if deps.ExpRepo != nil {
		expStore := NewExperienceStoreAdapter(deps.ExpRepo)
		result.DistillerSetter = func(d *distillation.Distiller) {
			d.WithExperienceStore(expStore)
		}
		// If a distiller was provided, wire it immediately.
		if deps.Distiller != nil {
			result.DistillerSetter(deps.Distiller)
		}
	}

	// Step 6: Create evolution system if explicitly enabled and dependencies available.
	if cfg != nil && cfg.Enabled {
		if deps.FlightRecorder == nil || deps.ExpRepo == nil {
			log.WarnContext(ctx, "bootstrap: evolution enabled but missing dependencies",
				"flight_recorder", deps.FlightRecorder != nil,
				"exp_repo", deps.ExpRepo != nil,
			)
			// Don't fail hard — just skip evolution with a warning.
			return result, nil
		}

		// Adapt postgres repo interface to evolution domain interface.
		evolutionRepo := &evolutionExpRepoAdapter{repo: deps.ExpRepo}

		evolutionComps, err := SetupEvolution(
			ctx,
			deps.FlightRecorder,
			evolutionRepo,
			result.CallbackReg,
			deps.DreamDeps,
			cfg, // <-- 新增参数
		)
		if err != nil {
			return nil, fmt.Errorf("setup evolution: %w", err)
		}
		result.Evolution = evolutionComps
		log.InfoContext(ctx, "bootstrap: evolution system initialized (ares_config-enabled)",
			"population_size", cfg.PopulationSize,
			"generations", cfg.Generations,
		)
	} else {
		reason := "disabled"
		if cfg == nil {
			reason = "no ares_config provided"
		}
		log.InfoContext(ctx, "bootstrap: wire evolution skipped",
			"reason", reason,
			"enabled", cfg != nil && cfg.Enabled,
		)
	}

	log.InfoContext(ctx, "bootstrap: WireAllEvolutionComponents completed",
		"callback_reg", result.CallbackReg != nil,
		"feedback_svc", result.FeedbackSvc != nil,
		"eval_registry", result.EvalRegistry != nil,
		"experience_locator", result.ExperienceLocator != nil,
		"distiller_setter", result.DistillerSetter != nil,
		"evolution", result.Evolution != nil,
	)

	return result, nil
}
