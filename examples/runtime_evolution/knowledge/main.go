// Knowledge Evolution Demo — KnowledgeGenome 参数进化 + KnowledgePatchExecutor
//
// 运行：go run examples/runtime_evolution/knowledge/main.go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/evolution/diff"
	"github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/pipeline"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

func main() {
	ctx := context.Background()
	fmt.Println("═══ Knowledge Evolution Demo ═══")
	fmt.Println()

	// 1. Create a minimal KnowledgeRuntime for the demo.
	// In production, this would be fully configured with real providers.
	pipe := knowledge.NewKnowledgePipeline(
		[]knowledge.Normalizer{&pipeline.DefaultNormalizer{MaxRawBytes: 10240}},
		[]knowledge.EntityMatcher{&pipeline.DefaultEntityMatcher{MatchThreshold: 0.6}},
		[]knowledge.Validator{&pipeline.DefaultValidator{}},
		[]knowledge.Summarizer{&pipeline.DefaultSummarizer{MaxSummaryLen: 200}},
	)
	discovery := planner.NewSourceDiscovery(provider.NewProviderRegistry(), planner.NewQueryPlanner())
	rt := knowledgeruntime.New(
		planner.NewKnowledgePlanner(),
		discovery,
		provider.NewProviderRegistry(),
		pipe,
		[]knowledgeruntime.Linker{&knowledgeruntime.DefaultLinker{}},
		[]knowledgeruntime.Reducer{&knowledgeruntime.DefaultReducer{}},
	)
	_ = rt // used by KnowledgePatchExecutor

	fmt.Println("1. Created KnowledgeRuntime")

	// 2. Create patch registry with real KnowledgePatchExecutor.
	patchReg := patch.NewRegistry()
	knowledgeExec := knowledgeruntime.NewKnowledgePatchExecutor(rt)
	if err := patchReg.Register("knowledge.planner", knowledgeExec); err != nil {
		log.Fatalf("register knowledge executor: %v", err)
	}
	if err := patchReg.Register("knowledge.planner.max_results", knowledgeExec); err != nil {
		log.Fatalf("register max_results executor: %v", err)
	}
	if err := patchReg.Register("knowledge.planner.reducer", knowledgeExec); err != nil {
		log.Fatalf("register reducer executor: %v", err)
	}
	if err := patchReg.Register("knowledge.planner.strategy", knowledgeExec); err != nil {
		log.Fatalf("register strategy executor: %v", err)
	}

	// 3. Create KnowledgeGenome.
	kgCfg := genome.DefaultKnowledgeGenomeConfig()
	kgCfg.MaxResults = 100
	kgCfg.ReducerStrategy = "default"
	kgCfg.PlannerStrategy = "balanced"

	wfGenome := genome.NewKnowledgeGenome(nil, kgCfg)

	// 4. Take snapshot.
	oldSnap, err := wfGenome.Snapshot(ctx)
	if err != nil {
		log.Fatalf("snapshot: %v", err)
	}
	fmt.Printf("2. Initial config: MaxResults=%d, Reducer=%s, Planner=%s\n",
		kgCfg.MaxResults, kgCfg.ReducerStrategy, kgCfg.PlannerStrategy)

	// 5. Mutate the knowledge genome.
	children, err := wfGenome.Mutate(ctx, 4)
	if err != nil {
		log.Fatalf("mutate: %v", err)
	}
	fmt.Printf("3. Generated %d candidate knowledge genomes\n", len(children))

	// 6. Evaluate and pick the best.
	var bestChild genome.Genome
	var bestFit float64
	for _, child := range children {
		fit, _ := child.Fitness(ctx)
		kgChild := child.(*genome.KnowledgeGenome)
		fmt.Printf("   Candidate fitness=%.2f  MaxResults=%d Reducer=%s Planner=%s\n",
			fit, kgChild.Config().MaxResults, kgChild.Config().ReducerStrategy, kgChild.Config().PlannerStrategy)
		if fit > bestFit {
			bestFit = fit
			bestChild = child
		}
	}

	// 7. Diff.
	newSnap, err := bestChild.Snapshot(ctx)
	if err != nil {
		log.Fatalf("new snapshot: %v", err)
	}

	diffReg := diff.NewRegistry()
	diffReg.Register(diff.NewKnowledgeDiffer())

	patches, err := diffReg.DiffAll(ctx, map[string]diff.SnapshotPair{
		"knowledge": {Old: oldSnap, New: newSnap},
	})
	if err != nil {
		log.Fatalf("diff: %v", err)
	}
	fmt.Printf("4. Diff Engine produced %d patches:\n", len(patches))
	for _, p := range patches {
		fmt.Printf("   • %s on %s (value: %v)\n", p.Type, p.Target, p.Value)
	}

	// 8. Apply patches through coordinator.
	coord := coordinator.NewEvolutionCoordinator(coordinator.DefaultPolicy(), patchReg)
	for _, p := range patches {
		coord.Submit(coordinator.PatchProposal{
			Patch:    p,
			Source:   coordinator.SourceAKF,
			Reason:   "knowledge evolution: config drift detected",
			Priority: 6,
		})
	}
	fmt.Printf("5. Submitted %d patch proposals\n", len(patches))

	coord.Evaluate(ctx)
	history := coord.PatchHistory()
	fmt.Printf("6. Applied %d patches:\n", len(history))
	for _, r := range history {
		status := "OK"
		if r.Error != nil {
			status = fmt.Sprintf("ERROR: %v", r.Error)
		}
		fmt.Printf("   • %s → %s\n", r.Proposal.Patch.Type, status)
	}

	fmt.Println()
	fmt.Println("═══ Demo Complete ═══")
}
