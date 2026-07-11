// Package ares_bootstrap — New evolution system provider (Genome + Diff + Coordinator).
package ares_bootstrap

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/evolution/diff"
	"github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
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
}

// ProvideNewEvolution wires the new evolution system:
// Evidence Store → Genome Registry → Diff Registry → Patch Registry → Coordinator.
//
// Args:
//
//	dag - optional MutableDAG for WorkflowGenome and executors (may be nil).
//	rt  - optional KnowledgeRuntime for KnowledgePatchExecutor (may be nil).
//
// When dag or rt is nil, their corresponding genomes/executors are skipped.
func ProvideNewEvolution(dag *engine.MutableDAG, rt *knowledgeruntime.KnowledgeRuntime) (*NewEvolutionComponents, error) {
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

	// 5. Coordinator — decision engine for all patches.
	coord := coordinator.NewEvolutionCoordinator(coordinator.DefaultPolicy(), patchReg)

	return &NewEvolutionComponents{
		EvidenceStore: evStore,
		GenomeReg:     genomeReg,
		DiffReg:       diffReg,
		PatchReg:      patchReg,
		Coordinator:   coord,
	}, nil
}

// ── noopKnowledgeExecutor ─────────────────────

// noopKnowledgeExecutor is a no-op implementation of patch.RuntimeComponent
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
