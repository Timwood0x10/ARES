// Runtime Evolution Demo — 展示 WorkflowGenome + Diff Engine + Coordinator 完整进化闭环
//
// 运行：go run examples/runtime_evolution/basic/main.go
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
	"github.com/Timwood0x10/ares/internal/workflow/engine"
	"github.com/Timwood0x10/ares/internal/workflow/graph"
)

func main() {
	ctx := context.Background()
	fmt.Println("═══ ARES Runtime Evolution Full Demo ═══")
	fmt.Println()

	// ── 1. Build initial DAG: A → B → C ──────────
	steps := []*engine.Step{
		{ID: "A", Name: "Input Validator", AgentType: "validator", Input: "validate request"},
		{ID: "B", Name: "Business Logic", AgentType: "processor", Input: "process", DependsOn: []string{"A"}},
		{ID: "C", Name: "Output Formatter", AgentType: "formatter", Input: "format", DependsOn: []string{"B"}},
	}
	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		log.Fatalf("create DAG: %v", err)
	}
	order, err := dag.GetExecutionOrder()
	if err != nil {
		log.Fatalf("get execution order: %v", err)
	}
	fmt.Printf("1. Initial DAG: %d nodes, order=%v\n", dag.NodeCount(), order)

	// ── 2. Create Genome Registry with real WorkflowGenome ──
	genomeReg := genome.NewRegistry()
	wfGenome := genome.NewWorkflowGenome(dag, genome.DefaultWorkflowGenomeConfig())
	if err := genomeReg.Register(wfGenome); err != nil {
		log.Fatalf("register workflow genome: %v", err)
	}

	// Register a SchedulerGenome too.
	schedGenome := genome.NewSchedulerGenome(graph.NewDefaultScheduler(), genome.DefaultSchedulerGenomeConfig())
	if err := genomeReg.Register(schedGenome); err != nil {
		log.Fatalf("register scheduler genome: %v", err)
	}

	fmt.Printf("2. Registered genomes: %v\n", genomeReg.List())

	// ── 3. Create Diff Registry with real Differ implementations ──
	diffReg := diff.NewRegistry()
	if err := diffReg.Register(diff.NewWorkflowDiffer()); err != nil {
		log.Fatalf("register workflow differ: %v", err)
	}
	if err := diffReg.Register(diff.NewSchedulerDiffer()); err != nil {
		log.Fatalf("register scheduler differ: %v", err)
	}
	fmt.Printf("3. Registered differ: %v\n", diffReg.List())

	// ── 4. Create Patch Registry with real GraphPatchExecutor ──
	// Build a graph.Graph from the same DAG steps so the executor can apply patches.
	evolvedGraph, err := graph.NewGraph("demo-evolution")
	if err != nil {
		log.Fatalf("create evolution graph: %v", err)
	}
	for _, step := range steps {
		fn, fErr := graph.NewFuncNode(step.ID, func(_ context.Context, _ *graph.State) error { return nil })
		if fErr != nil {
			log.Fatalf("create func node %s: %v", step.ID, fErr)
		}
		_, fErr = evolvedGraph.Node(step.ID, fn)
		if fErr != nil {
			log.Fatalf("add node %s: %v", step.ID, fErr)
		}
	}
	for _, step := range steps {
		for _, dep := range step.DependsOn {
			if _, eErr := evolvedGraph.Edge(dep, step.ID); eErr != nil {
				log.Fatalf("add edge %s→%s: %v", dep, step.ID, eErr)
			}
		}
	}
	_, err = evolvedGraph.Start("A")
	if err != nil {
		log.Fatalf("set start node: %v", err)
	}

	patchReg := patch.NewRegistry()
	graphExec := graph.NewGraphPatchExecutor(evolvedGraph)
	if err := patchReg.Register("workflow.graph", graphExec); err != nil {
		log.Fatalf("register graph executor: %v", err)
	}
	// The SchedulerPatchExecutor re-uses GraphPatchExecutor for ChangeScheduler.
	if err := patchReg.Register("graph.scheduler", graphExec); err != nil {
		log.Fatalf("register scheduler executor: %v", err)
	}

	// ── 5. Create Coordinator ──
	coord := coordinator.NewEvolutionCoordinator(coordinator.DefaultPolicy(), patchReg)
	fmt.Println("5. Created coordinator")

	// ── 6. Take snapshot of current state ──
	oldSnapshot, err := wfGenome.Snapshot(ctx)
	if err != nil {
		log.Fatalf("snapshot: %v", err)
	}
	oldSchedSnapshot, err := schedGenome.Snapshot(ctx)
	if err != nil {
		log.Fatalf("scheduler snapshot: %v", err)
	}

	// ── 7. Mutate the workflow genome (retry until at least one mutation produces a change) ──
	var children []genome.Genome
	for attempt := 0; attempt < 10; attempt++ {
		children, err = wfGenome.Mutate(ctx, 5)
		if err != nil {
			log.Fatalf("mutate: %v", err)
		}

		// Check if any child has a different topology.
		for _, child := range children {
			cs, _ := child.Snapshot(ctx)
			cDAG := cs.(*engine.DAG)
			if len(cDAG.Nodes) != len(oldSnapshot.(*engine.DAG).Nodes) {
				goto foundMutation
			}
		}
	}
	log.Fatalf("mutate: failed to produce a change after 10 attempts")
foundMutation:
	fmt.Printf("6. Generated %d candidate workflow genomes (after mutation)\n", len(children))

	// ── 8. Evaluate candidates and pick the best ──
	var bestChild genome.Genome
	var bestFit float64
	for _, child := range children {
		fit, _ := child.Fitness(ctx)
		fmt.Printf("   Candidate %q fitness: %.2f\n", child.Name(), fit)
		if fit > bestFit {
			bestFit = fit
			bestChild = child
		}
	}

	// ── 9. Diff the best child vs original ──
	newSnapshot, err := bestChild.Snapshot(ctx)
	if err != nil {
		log.Fatalf("new snapshot: %v", err)
	}

	// Also mutate the scheduler for demo.
	schedChildren, _ := schedGenome.Mutate(ctx, 1)
	bestSched := schedChildren[0]
	newSchedSnapshot, _ := bestSched.Snapshot(ctx)

	snapshots := map[string]diff.SnapshotPair{
		"workflow":  {Old: oldSnapshot, New: newSnapshot},
		"scheduler": {Old: oldSchedSnapshot, New: newSchedSnapshot},
	}

	patches, err := diffReg.DiffAll(ctx, snapshots)
	if err != nil {
		log.Fatalf("diff all: %v", err)
	}
	fmt.Printf("7. Diff Engine produced %d patches:\n", len(patches))
	for _, p := range patches {
		fmt.Printf("   • %s on %s (value: %v)\n", p.Type, p.Target, p.Value)
	}

	// ── 10. Submit patches as proposals to Coordinator ──
	for _, p := range patches {
		coord.Submit(coordinator.PatchProposal{
			Patch:     p,
			Source:    coordinator.SourceGA,
			Reason:    "GA evaluation: fitness improved",
			Priority:  5,
			Timestamp: time.Now(),
		})
	}
	fmt.Printf("8. Submitted %d patch proposals to coordinator\n", len(patches))

	// ── 11. Evaluate and apply ──
	coord.Evaluate(ctx)
	history := coord.PatchHistory()
	fmt.Printf("9. Applied %d patches\n", len(history))
	for _, r := range history {
		status := "OK"
		if r.Error != nil {
			status = fmt.Sprintf("ERROR: %v", r.Error)
		}
		fmt.Printf("   • %s from %s → %s\n", r.Proposal.Patch.Type, r.Proposal.Source, status)
	}

	// ── 12. Show final state ──
	decisions := coord.DecisionHistory()
	fmt.Printf("10. Decision history: %d total\n", len(decisions))
	for _, d := range decisions {
		fmt.Printf("   • %s: %s\n", d.Decision, d.Reason)
	}

	fmt.Println()
	fmt.Println("═══ Demo Complete ═══")
}
