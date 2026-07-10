// Full Cycle Demo — 全部 Genome + Diff Engine + Coordinator + 真实 Executor 的完整闭环
//
// 运行：go run examples/runtime_evolution/full/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/evolution/diff"
	"github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/pipeline"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
	"github.com/Timwood0x10/ares/internal/workflow/graph"
)

func main() {
	ctx := context.Background()
	fmt.Println("═══ ARES Runtime Evolution — Full Chain Demo ═══")
	fmt.Println()

	// ── 1. Build initial DAG ──
	steps := []*engine.Step{
		{ID: "A", Name: "Input", AgentType: "validator", Input: "validate"},
		{ID: "B", Name: "Process", AgentType: "processor", Input: "process", DependsOn: []string{"A"}},
		{ID: "C", Name: "Output", AgentType: "formatter", Input: "format", DependsOn: []string{"B"}},
	}
	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		log.Fatalf("create DAG: %v", err)
	}
	order, _ := dag.GetExecutionOrder()
	fmt.Printf("1. DAG: %d nodes, order=%v\n", dag.NodeCount(), order)

	// ── 2. Build graph.Graph for the real GraphPatchExecutor ──
	g, err := graph.NewGraph("full-demo")
	if err != nil {
		log.Fatalf("create graph: %v", err)
	}
	for _, step := range steps {
		fn, fErr := graph.NewFuncNode(step.ID, func(_ context.Context, _ *graph.State) error { return nil })
		if fErr != nil {
			log.Fatalf("func node %s: %v", step.ID, fErr)
		}
		_, _ = g.Node(step.ID, fn)
	}
	for _, step := range steps {
		for _, dep := range step.DependsOn {
			_, _ = g.Edge(dep, step.ID)
		}
	}
	_, _ = g.Start("A")

	// ── 3. Register all Genomes ──
	genomeReg := genome.NewRegistry()
	mustRegisterGenome(genomeReg, genome.NewWorkflowGenome(dag, genome.DefaultWorkflowGenomeConfig()))
	mustRegisterGenome(genomeReg, genome.NewSchedulerGenome(graph.NewDefaultScheduler(), genome.DefaultSchedulerGenomeConfig()))
	mustRegisterGenome(genomeReg, genome.NewKnowledgeGenome(nil, genome.DefaultKnowledgeGenomeConfig()))
	mustRegisterGenome(genomeReg, genome.NewRecoveryGenome(
		&engine.RecoveryPolicy{Strategy: engine.RecoveryRetry, MaxAttempts: 3},
		genome.DefaultRecoveryGenomeConfig(),
	))
	fmt.Printf("2. Registered genomes: %v\n", genomeReg.List())

	// ── 4. Register all Differs ──
	diffReg := diff.NewRegistry()
	mustRegisterDiffer(diffReg, diff.NewWorkflowDiffer())
	mustRegisterDiffer(diffReg, diff.NewSchedulerDiffer())
	mustRegisterDiffer(diffReg, diff.NewKnowledgeDiffer())
	mustRegisterDiffer(diffReg, diff.NewRecoveryDiffer())
	fmt.Printf("3. Registered differs: %v\n", diffReg.List())

	// ── 5. Register all Executors ──
	patchReg := patch.NewRegistry()

	// Graph executor: handles insert/remove/replace/add_edge/remove_edge on any node ID.
	graphExec := graph.NewGraphPatchExecutor(g)
	// Register for common patch targets.
	for _, target := range []string{"workflow.graph", "A", "B", "C",
		"B-parallel", "A-parallel", "C-parallel",
		"wf-mut-0", "wf-mut-1", "wf-mut-2", "wf-mut-3", "wf-mut-4"} {
		_ = patchReg.Register(target, graphExec)
	}

	// Scheduler executor: reuses GraphPatchExecutor for ChangeScheduler.
	_ = patchReg.Register("graph.scheduler", graphExec)

	// Recovery executor: handles all recovery-related patches.
	recoveryExec := engine.NewRecoveryPatchExecutor(dag)
	_ = patchReg.Register("recovery.strategy", recoveryExec)
	_ = patchReg.Register("recovery.max_attempts", recoveryExec)
	_ = patchReg.Register("recovery.replacement_agent", recoveryExec)
	_ = patchReg.Register("recovery.max_retries", recoveryExec)

	// Knowledge executor: handles knowledge/planner patches.
	// Build a minimal knowledge runtime for the executor.
	knowPipe := knowledge.NewKnowledgePipeline(
		[]knowledge.Normalizer{&pipeline.DefaultNormalizer{MaxRawBytes: 10240}},
		[]knowledge.EntityMatcher{&pipeline.DefaultEntityMatcher{MatchThreshold: 0.6}},
		[]knowledge.Validator{&pipeline.DefaultValidator{}},
		[]knowledge.Summarizer{&pipeline.DefaultSummarizer{MaxSummaryLen: 200}},
	)
	knowDiscovery := planner.NewSourceDiscovery(provider.NewProviderRegistry(), planner.NewQueryPlanner())
	knowRt := knowledgeruntime.New(
		planner.NewKnowledgePlanner(),
		knowDiscovery,
		provider.NewProviderRegistry(),
		knowPipe,
		[]knowledgeruntime.Linker{&knowledgeruntime.DefaultLinker{}},
		[]knowledgeruntime.Reducer{&knowledgeruntime.DefaultReducer{}},
	)
	knowledgeExec := knowledgeruntime.NewKnowledgePatchExecutor(knowRt)
	_ = patchReg.Register("knowledge.planner", knowledgeExec)
	_ = patchReg.Register("knowledge.planner.max_results", knowledgeExec)
	_ = patchReg.Register("knowledge.planner.reducer", knowledgeExec)
	_ = patchReg.Register("knowledge.planner.strategy", knowledgeExec)
	_ = patchReg.Register("knowledge.planner.summarizer", knowledgeExec)

	fmt.Println("4. Registered executors: Graph + Scheduler + Recovery + Knowledge")

	// ── 6. Coordinator ──
	coord := coordinator.NewEvolutionCoordinator(coordinator.DefaultPolicy(), patchReg)

	// ── 7. Take snapshots ──
	snapshots := make(map[string]diff.SnapshotPair)
	for _, name := range genomeReg.List() {
		gm, _ := genomeReg.Get(name)
		snap, _ := gm.Snapshot(ctx)
		snapshots[name] = diff.SnapshotPair{Old: snap}
	}
	fmt.Println("5. Snapshots taken")

	// ── 8. Run one evolution cycle for each genome ──
	type evolutionResult struct {
		genomeName string
		patches    []patch.RuntimePatch
	}

	var allResults []evolutionResult

	for _, name := range genomeReg.List() {
		gm, _ := genomeReg.Get(name)

		// Mutate.
		children, err := gm.Mutate(ctx, 3)
		if err != nil {
			log.Printf("  %s: mutate failed: %v", name, err)
			continue
		}

		// Pick best by fitness.
		var best genome.Genome
		var bestFit float64
		for _, child := range children {
			fit, _ := child.Fitness(ctx)
			if fit > bestFit {
				bestFit = fit
				best = child
			}
		}
		if best == nil {
			continue
		}

		// Snapshot the best child.
		newSnap, _ := best.Snapshot(ctx)

		// Diff.
		pair := snapshots[name]
		pair.New = newSnap
		patches, err := diffReg.DiffAll(ctx, map[string]diff.SnapshotPair{name: pair})
		if err != nil {
			log.Printf("  %s: diff failed: %v", name, err)
			continue
		}

		if len(patches) > 0 {
			allResults = append(allResults, evolutionResult{
				genomeName: name,
				patches:    patches,
			})
		}
	}

	fmt.Printf("6. Evolution cycle produced %d genome changes:\n", len(allResults))
	for _, res := range allResults {
		fmt.Printf("   %s: %d patches\n", res.genomeName, len(res.patches))
		for _, p := range res.patches {
			fmt.Printf("       • %s on %s\n", p.Type, p.Target)
		}
	}

	// ── 9. Submit all patches to coordinator ──
	var totalPatches int
	for _, res := range allResults {
		for _, p := range res.patches {
			coord.Submit(coordinator.PatchProposal{
				Patch:     p,
				Source:    coordinator.SourceGA,
				Reason:    fmt.Sprintf("evolution: %s improved", res.genomeName),
				Priority:  5,
				Timestamp: time.Now(),
			})
			totalPatches++
		}
	}
	fmt.Printf("7. Submitted %d patches to coordinator\n", totalPatches)

	// ── 10. Evaluate and apply ──
	coord.Evaluate(ctx)
	history := coord.PatchHistory()
	fmt.Printf("8. Applied %d patches:\n", len(history))
	var okCount, failCount int
	for _, r := range history {
		if r.Error != nil {
			failCount++
			fmt.Printf("   ❌ %s → %v\n", r.Proposal.Patch.Type, r.Error)
		} else {
			okCount++
			fmt.Printf("   ✅ %s → OK\n", r.Proposal.Patch.Type)
		}
	}

	// ── 11. Summary ──
	fmt.Println()
	fmt.Println("═══ Summary ═══")
	fmt.Printf("Genomes registered: %d\n", len(genomeReg.List()))
	fmt.Printf("Differs registered: %d\n", len(diffReg.List()))
	fmt.Printf("Patches proposed:  %d\n", totalPatches)
	fmt.Printf("Patches applied:   %d ✅\n", okCount)
	fmt.Printf("Patches failed:    %d ❌\n", failCount)
	fmt.Println()
	fmt.Println("═══ Done ═══")
}

func mustRegisterGenome(r *genome.Registry, g genome.Genome) {
	if err := r.Register(g); err != nil {
		panic(fmt.Sprintf("register genome: %v", err))
	}
}

func mustRegisterDiffer(r *diff.Registry, d diff.Differ) {
	if err := r.Register(d); err != nil {
		panic(fmt.Sprintf("register differ: %v", err))
	}
}
