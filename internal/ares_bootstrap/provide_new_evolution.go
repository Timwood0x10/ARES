// Package ares_bootstrap — New evolution system provider (Genome + Diff + Coordinator).
package ares_bootstrap

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"fmt"

	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	aresmemory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/evidence"
	evoparent "github.com/Timwood0x10/ares/internal/evolution"
	"github.com/Timwood0x10/ares/internal/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/evolution/diff"
	"github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/pipeline"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	provider_code "github.com/Timwood0x10/ares/internal/knowledge/provider/code"
	provider_memory "github.com/Timwood0x10/ares/internal/knowledge/provider/memory"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

// NewEvolutionComponents holds the new evolution system components.
type NewEvolutionComponents struct {
	EvidenceStore *evidence.MemoryStore
	GenomeReg     *genome.Registry
	DiffReg       *diff.Registry
	PatchReg      *patch.Registry
	Coordinator   *coordinator.EvolutionCoordinator
	// LLMAdapter parses natural-language LLM suggestions into PatchProposals
	// that the Coordinator can evaluate alongside GA/Chaos/AKF/Human sources.
	// Wired into the Coordinator's suggestion pipeline in wireGAEvolution when
	// an LLM client is available (LLM → Parse → PatchProposal → Coordinate.Evaluate).
	LLMAdapter *evoparent.LLMAdapter
	// StrategyStore persists the best-evolved strategy deployed by the GA
	// engine so the live agent can consume it at runtime. Set by the
	// bootstrap bridge after the store is created.
	StrategyStore evolution.StrategyStore

	// liveDAG holds the agent's live workflow DAG injected after bootstrap
	// so the evolution system's executors operate on real runtime state
	// instead of synthetic placeholders. Set via UpdateLiveDAG after agents
	// are created and their DAGs are registered with the runtime manager.
	liveDAG *engine.MutableDAG
}

// ProvideNewEvolution wires the new evolution system:
// Evidence Store → Genome Registry → Diff Registry → Patch Registry → Coordinator.
//
// Args:
//
//	dag - optional MutableDAG for WorkflowGenome and executors (may be nil).
//	rt  - optional KnowledgeRuntime for KnowledgePatchExecutor (may be nil).
//	memoryStore - optional MemoryConfigStore for MemoryPatchExecutor (may be nil).
//
// When dag, rt, or memoryStore is nil, their corresponding executors are skipped.
func ProvideNewEvolution(dag *engine.MutableDAG, rt *knowledgeruntime.KnowledgeRuntime, memoryStore aresmemory.MemoryConfigStore) (*NewEvolutionComponents, error) {
	// 1. Evidence Store — central logging for all runtime evidence.
	evStore := evidence.NewMemoryStore()

	// 2. Genome Registry — register all available genomes.
	genomeReg := genome.NewRegistry()
	if dag != nil {
		wfGenome := genome.NewWorkflowGenome(dag, genome.WorkflowGenomeConfig{
			MaxNodes:      20,
			InsertionRate: 0.3,
			PruneRate:     0.2,
			EvidenceStore: evStore,
		})
		if err := genomeReg.Register(wfGenome); err != nil {
			return nil, fmt.Errorf("register workflow genome: %w", err)
		}

		schedGenome := genome.NewSchedulerGenome(
			wfgraph.NewDefaultScheduler(),
			genome.SchedulerGenomeConfig{EvidenceStore: evStore},
		)
		if err := genomeReg.Register(schedGenome); err != nil {
			return nil, fmt.Errorf("register scheduler genome: %w", err)
		}

		recoveryGenome := genome.NewRecoveryGenome(
			&engine.RecoveryPolicy{Strategy: engine.RecoveryRetry, MaxAttempts: 3},
			genome.DefaultRecoveryGenomeConfig(),
		)
		if err := genomeReg.Register(recoveryGenome); err != nil {
			return nil, fmt.Errorf("register recovery genome: %w", err)
		}
	}

	// Always register the knowledge genome (it works with or without a runtime).
	knowledgeGenome := genome.NewKnowledgeGenome(nil, genome.KnowledgeGenomeConfig{
		MaxResults:      100,
		ReducerStrategy: "default",
		PlannerStrategy: "balanced",
		EvidenceStore:   evStore,
	})
	if err := genomeReg.Register(knowledgeGenome); err != nil {
		return nil, fmt.Errorf("register knowledge genome: %w", err)
	}

	// Planner genome — evolves planning strategy.
	plannerGenome := genome.NewPlannerGenome(genome.PlannerGenomeConfig{
		Strategy:      "balanced",
		MaxSources:    10,
		MinRelevance:  0.5,
		EvidenceStore: evStore,
	})
	if err := genomeReg.Register(plannerGenome); err != nil {
		return nil, fmt.Errorf("register planner genome: %w", err)
	}

	// Memory genome — evolves memory management parameters.
	memoryGenome := genome.NewMemoryGenome(genome.MemoryGenomeConfig{
		MaxHistory:            10,
		MaxSessions:           100,
		MaxDistilledTasks:     5000,
		UseStructuredCleaning: false,
		EvidenceStore:         evStore,
	})
	if err := genomeReg.Register(memoryGenome); err != nil {
		return nil, fmt.Errorf("register memory genome: %w", err)
	}

	// 3. Diff Registry — register all differs.
	diffReg := diff.NewRegistry()
	for _, d := range []diff.Differ{
		diff.NewWorkflowDiffer(),
		diff.NewSchedulerDiffer(),
		diff.NewKnowledgeDiffer(),
		diff.NewRecoveryDiffer(),
	} {
		if err := diffReg.Register(d); err != nil {
			return nil, fmt.Errorf("register differ %s: %w", d.Name(), err)
		}
	}

	// 4. Patch Registry — register all executors.
	patchReg := patch.NewRegistry()

	if dag != nil {
		// Graph executor — for workflow and scheduler patches.
		g, gErr := wfgraph.NewGraph("evolution-workflow")
		if gErr != nil {
			return nil, fmt.Errorf("create evolution graph: %w", gErr)
		}
		for _, step := range dag.Steps() {
			fn, fErr := wfgraph.NewFuncNode(step.ID, func(_ context.Context, _ *wfgraph.State) error { return nil })
			if fErr != nil {
				return nil, fmt.Errorf("create func node %s: %w", step.ID, fErr)
			}
			if _, nErr := g.Node(step.ID, fn); nErr != nil {
				return nil, fmt.Errorf("add node %s: %w", step.ID, nErr)
			}
		}
		for _, step := range dag.Steps() {
			for _, dep := range step.DependsOn {
				if _, eErr := g.Edge(dep, step.ID); eErr != nil {
					return nil, fmt.Errorf("add edge %s→%s: %w", dep, step.ID, eErr)
				}
			}
		}
		if len(dag.Steps()) > 0 {
			if _, sErr := g.Start(dag.Steps()[0].ID); sErr != nil {
				return nil, fmt.Errorf("set start node: %w", sErr)
			}
		}

		graphExec := wfgraph.NewGraphPatchExecutor(g)
		_ = patchReg.RegisterComponent(graphExec)
		_ = patchReg.Register("graph.scheduler", graphExec)

		// Recovery executor.
		recoveryExec := engine.NewRecoveryPatchExecutor(dag)
		_ = patchReg.RegisterComponent(recoveryExec)
		_ = patchReg.Register("recovery.max_attempts", recoveryExec)
		_ = patchReg.Register("recovery.replacement_agent", recoveryExec)
		_ = patchReg.Register("recovery.max_retries", recoveryExec)
	}

	// Knowledge executor — works with or without a real runtime.
	var knowledgeExec patch.RuntimeComponent
	if rt != nil {
		// Wire the KnowledgeRuntime to the PatchRegistry and EvidenceStore
		// so that runtime patches can dynamically update knowledge config and
		// evidence emitted during AKG execution is recorded centrally.
		rt.WithPatchRegistry(patchReg).WithEvidenceStore(evStore)
		knowledgeExec = knowledgeruntime.NewKnowledgePatchExecutor(rt)
	} else {
		// No runtime available — use a no-op executor for knowledge patches.
		knowledgeExec = &noopKnowledgeExecutor{}
	}
	_ = patchReg.RegisterComponent(knowledgeExec)
	_ = patchReg.Register("knowledge.planner.max_results", knowledgeExec)
	_ = patchReg.Register("knowledge.planner.reducer", knowledgeExec)
	_ = patchReg.Register("knowledge.planner.strategy", knowledgeExec)
	_ = patchReg.Register("knowledge.planner.summarizer", knowledgeExec)

	// Memory executor — wraps a MemoryConfigStore as a RuntimeComponent.
	// Accepts patches for memory configuration (history depth, TTL, task limits).
	// When memoryStore is nil, the executor is skipped.
	if memoryStore != nil {
		memoryExec := aresmemory.NewMemoryPatchExecutor(memoryStore)
		_ = patchReg.RegisterComponent(memoryExec)
		_ = patchReg.Register("memory.config.max_history", memoryExec)
		_ = patchReg.Register("memory.config.max_tasks", memoryExec)
		_ = patchReg.Register("memory.config.max_distilled_tasks", memoryExec)
		_ = patchReg.Register("memory.config.session_ttl", memoryExec)
	}

	// 5. Coordinator — decision engine for all patches.
	coord := coordinator.NewEvolutionCoordinator(coordinator.DefaultPolicy(), patchReg)

	return &NewEvolutionComponents{
		EvidenceStore: evStore,
		GenomeReg:     genomeReg,
		DiffReg:       diffReg,
		PatchReg:      patchReg,
		Coordinator:   coord,
		LLMAdapter:    evoparent.NewLLMAdapter(),
	}, nil
}

// UpdateLiveKnowledgeRuntime replaces the evolution system's isolated
// KnowledgeRuntime with the agent's live KnowledgeRuntime, so knowledge
// genome patches (ChangeBudget/ChangePlanner/ChangeReducer) are applied
// to the actual runtime used by the agent's knowledge tools.
func (c *NewEvolutionComponents) UpdateLiveKnowledgeRuntime(rt *knowledgeruntime.KnowledgeRuntime) {
	if rt == nil {
		log.Warn("new evolution: UpdateLiveKnowledgeRuntime called with nil, keeping existing")
		return
	}
	// Wire the live runtime to the patch registry and evidence store.
	rt.WithPatchRegistry(c.PatchReg).WithEvidenceStore(c.EvidenceStore)
	// Replace the KnowledgePatchExecutor in the patch registry.
	liveExec := knowledgeruntime.NewKnowledgePatchExecutor(rt)
	c.PatchReg.RegisterComponent(liveExec)
	c.PatchReg.Register("knowledge.planner.max_results", liveExec)
	c.PatchReg.Register("knowledge.planner.reducer", liveExec)
	c.PatchReg.Register("knowledge.planner.strategy", liveExec)
	c.PatchReg.Register("knowledge.planner.summarizer", liveExec)
	log.Info("new evolution: live KnowledgeRuntime injected into executors")
}

// ── noopKnowledgeExecutor ─────────────────────

// UpdateLiveDAG injects a live agent workflow DAG into the evolution system's
// executors after bootstrap, replacing the synthetic placeholder DAG. This
// ensures that workflow/scheduler/recovery patches generated by the genome
// evolution system are applied to the real runtime DAG instead of synthetic
// executors. Must be called after agents are created and their DAGs are
// registered with the runtime manager.
//
// The DAG is used to rebuild the graph executor and recovery executor in the
// patch registry. The genome registry's WorkflowGenome is NOT updated here
// because it needs a full re-registration; the live DAG is used downstream
// when the coordinator evaluates and applies patches.
func (c *NewEvolutionComponents) UpdateLiveDAG(dag *engine.MutableDAG) error {
	if dag == nil {
		return fmt.Errorf("live DAG must not be nil")
	}
	c.liveDAG = dag

	// Rebuild graph executor with the live DAG's steps.
	g, gErr := wfgraph.NewGraph("evolution-workflow")
	if gErr != nil {
		return fmt.Errorf("create evolution graph from live DAG: %w", gErr)
	}
	for _, step := range dag.Steps() {
		fn, fErr := wfgraph.NewFuncNode(step.ID, func(_ context.Context, _ *wfgraph.State) error { return nil })
		if fErr != nil {
			return fmt.Errorf("create func node %s: %w", step.ID, fErr)
		}
		if _, nErr := g.Node(step.ID, fn); nErr != nil {
			return fmt.Errorf("add node %s: %w", step.ID, nErr)
		}
	}
	for _, step := range dag.Steps() {
		for _, dep := range step.DependsOn {
			if _, eErr := g.Edge(dep, step.ID); eErr != nil {
				return fmt.Errorf("add edge %s→%s: %w", dep, step.ID, eErr)
			}
		}
	}
	if len(dag.Steps()) > 0 {
		if _, sErr := g.Start(dag.Steps()[0].ID); sErr != nil {
			return fmt.Errorf("set start node: %w", sErr)
		}
	}

	graphExec := wfgraph.NewGraphPatchExecutor(g)
	c.PatchReg.RegisterComponent(graphExec)
	c.PatchReg.Register("graph.scheduler", graphExec)

	// Rebuild recovery executor with the live DAG.
	recoveryExec := engine.NewRecoveryPatchExecutor(dag)
	c.PatchReg.RegisterComponent(recoveryExec)
	c.PatchReg.Register("recovery.max_attempts", recoveryExec)
	c.PatchReg.Register("recovery.replacement_agent", recoveryExec)
	c.PatchReg.Register("recovery.max_retries", recoveryExec)

	log.Info("new evolution: live DAG injected into executors",
		"steps", len(dag.Steps()))
	return nil
}

// used when no KnowledgeRuntime is available. It accepts all knowledge patches
// but does nothing — enabling the evolution pipeline to function without AKF.
type noopKnowledgeExecutor struct{}

func (e *noopKnowledgeExecutor) Name() string { return "knowledge.planner" }

func (e *noopKnowledgeExecutor) Snapshot(_ context.Context) (any, error) {
	return nil, fmt.Errorf("noop: no snapshot")
}

func (e *noopKnowledgeExecutor) Apply(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	return &patch.RuntimePatch{
		Type:   p.Type,
		Reason: "rollback: mimic original config",
	}, nil
}

func (e *noopKnowledgeExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	switch p.Type {
	case patch.PatchChangeBudget, patch.PatchChangePlanner, patch.PatchChangeReducer:
		return nil
	default:
		return fmt.Errorf("knowledge noop executor: unsupported patch type %s", p.Type)
	}
}

// Ensure noopKnowledgeExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*noopKnowledgeExecutor)(nil)

// BuildKnowledgeRuntime creates a KnowledgeRuntime for the evolution
// system with registered providers (memory, code) that work without an
// external database. This enables the KnowledgePatchExecutor to process
// knowledge/planner patches meaningfully instead of being a no-op.
func BuildKnowledgeRuntime() *knowledgeruntime.KnowledgeRuntime {
	knowPipe := knowledge.NewKnowledgePipeline(
		[]knowledge.Normalizer{&pipeline.DefaultNormalizer{MaxRawBytes: 10240}},
		[]knowledge.EntityMatcher{&pipeline.DefaultEntityMatcher{MatchThreshold: 0.6}},
		[]knowledge.Validator{&pipeline.DefaultValidator{}},
		[]knowledge.Summarizer{&pipeline.DefaultSummarizer{MaxSummaryLen: 200}},
	)

	reg := provider.NewProviderRegistry()
	// Register lightweight providers that work without an external database.
	// Memory provider — stores knowledge objects in-memory for the current session.
	if err := reg.Register(provider_memory.New("memory-default", nil)); err != nil {
		log.Warn("bootstrap: register memory provider for knowledge runtime", "error", err)
	}
	// Code provider — extracts knowledge from the local codebase (functions, types, etc.).
	if cp, err := provider_code.New("codebase", "."); err == nil {
		if err := reg.Register(cp); err != nil {
			log.Warn("bootstrap: register code provider for knowledge runtime", "error", err)
		}
	} else {
		log.Warn("bootstrap: create code provider for knowledge runtime", "error", err)
	}

	knowDiscovery := planner.NewSourceDiscovery(
		reg,
		planner.NewQueryPlanner(),
	)
	return knowledgeruntime.New(
		planner.NewKnowledgePlanner(),
		knowDiscovery,
		reg,
		knowPipe,
		[]knowledgeruntime.Linker{&knowledgeruntime.DefaultLinker{}},
		[]knowledgeruntime.Reducer{&knowledgeruntime.DefaultReducer{}},
	)
}

// buildMemoryManager creates a lightweight ProductionMemoryManager for the
// evolution system that works without a database pool. The MemoryPatchExecutor
// only needs the config field — it reads/writes memory configuration values
// (max_history, max_tasks, session_ttl, etc.) without touching the database.
func buildMemoryManager() *aresmemory.ProductionMemoryManager {
	return aresmemory.NewMinimalMemoryManager()
}
