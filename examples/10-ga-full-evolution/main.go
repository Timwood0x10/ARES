// GA Full Evolution Demo — comprehensive GA evolution demonstration.
//
// Demonstrates:
//  1. Tool selection strategy evolution — optimal tool combinations per task type
//  2. Workflow DAG topology evolution — evolves node structure and execution order
//  3. Memory-guided mutation — historical experience biases mutation direction
//  4. Multi-objective fitness — quality + cost + latency combined scoring
//
// Run: go run examples/10-ga-full-evolution/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/evolution/diff"
	evogenome "github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
	"github.com/Timwood0x10/ares/internal/workflow/graph"
)

func main() {
	ctx := context.Background()
	fmt.Println("═══ GA Full Evolution Demo ═══")
	fmt.Println()

	// ── 1. Create base strategy (tools, params, prompt) ──
	base := &mutation.Strategy{
		ID:      "root-strategy",
		Version: 1,
		Params: map[string]any{
			"temperature":   0.7,
			"top_k":         40,
			"max_tokens":    4096,
			"tool_selector": "auto", // tool selection strategy: auto/manual/priority
			"search_depth":  3,      // search depth
			"batch_size":    5,      // batch size
		},
		PromptTemplate: "You are a helpful assistant. Complete the task efficiently.",
		Score:          -1,
		CreatedAt:      time.Now(),
	}

	// ── 2. Create mutator (param ranges + tool pool) ──
	mutator, err := mutation.NewMutator(
		mutation.WithParamRanges(mutableParams()),
		mutation.WithPromptPool(promptPool()),
		mutation.WithToolPool(toolPool()),
	)
	if err != nil {
		panic(err)
	}

	// ── 3. Create crossover (uniform/two_point/segment) ──
	crosser, err := genome.NewCrossover(
		genome.WithSeed(42),
		genome.WithCrossoverType(genome.CrossoverUniform),
	)
	if err != nil {
		panic(err)
	}

	// ── 4. Create population (GA core engine) ──
	pop, err := genome.NewPopulation(ctx, base, mutator,
		genome.WithPopulationSize(20),
		genome.WithEliteCount(3),
		genome.WithMutationRate(0.2),
		genome.WithSurvivalRate(0.6),
		genome.WithSelectionStrategy("tournament"),
		genome.WithTournamentSelection(3),
	)
	if err != nil {
		panic(err)
	}
	fmt.Printf("1. Population initialized with %d individuals\n", pop.Size)

	// ── 5. Create workflow DAG (evolvable topology) ──
	dag := buildInitialDAG()
	fmt.Printf("2. Initial DAG: %d nodes\n", dag.NodeCount())

	// ── 6. Register evolution components (Genome + Diff + Coordinator) ──
	coord, genomeReg, diffReg := registerEvolutionComponents(dag)
	fmt.Println("3. Evolution components registered")

	// ── 7. Create memory-guided provider (mock experience) ──
	hintProvider := &mockHintProvider{
		hints: []evolutionHint{
			{taskType: "code", tool: "search", confidence: 0.85},
			{taskType: "code", tool: "read", confidence: 0.72},
			{taskType: "data", tool: "calculate", confidence: 0.91},
			{taskType: "data", tool: "exec", confidence: 0.65},
		},
	}
	fmt.Println("4. Memory-guided provider loaded (4 experiences)")

	// ── 8. Run GA evolution (5 generations) ──
	fmt.Println("\n═══ Starting GA Evolution ═══")
	for gen := 0; gen < 5; gen++ {
		runGeneration(ctx, pop, mutator, crosser, hintProvider, gen)
		printGenerationStats(pop, gen)
	}

	// Step 9: Show evolution results — what GA learned.
	fmt.Println("\n═══ Evolution Results: What GA Learned ═══")
	best := pop.BestStrategy()
	if best != nil {
		fmt.Printf("✅ Tool selection: %v", best.Params["tool_selector"])
		if best.Params["tool_selector"] == "priority" {
			fmt.Println(" → prioritize high-frequency tools, reduce irrelevant calls")
		} else {
			fmt.Println("")
		}

		fmt.Printf("✅ Search depth: %v", best.Params["search_depth"])
		if d, ok := best.Params["search_depth"].(int); ok && d >= 3 {
			fmt.Println(" → deeper search provides more comprehensive information")
		} else {
			fmt.Println(" → shallow search suitable for simple tasks")
		}

		fmt.Printf("✅ Scheduler: %v", best.Params["scheduler_strategy"])
		if best.Params["scheduler_strategy"] == "priority" {
			fmt.Println(" → priority scheduling reduces critical path latency")
		} else {
			fmt.Println("")
		}

		fmt.Printf("✅ Memory threshold: %.2f", best.Params["memory_threshold"])
		if t, ok := best.Params["memory_threshold"].(float64); ok && t >= 0.7 {
			fmt.Println(" → high threshold recalls precise memories")
		} else {
			fmt.Println(" → low threshold recalls more candidates")
		}

		fmt.Printf("✅ Recovery strategy: %v", best.Params["recovery_strategy"])
		if best.Params["recovery_strategy"] == "retry" {
			fmt.Println(" → retry on failure, suitable for transient faults")
		} else {
			fmt.Println("")
		}

		if best.DimensionScores != nil {
			fmt.Printf("\n📊 Multi-objective scores:\n")
			for k, v := range best.DimensionScores {
				fmt.Printf("   %s: %.1f\n", k, v)
			}
		}
	}

	// ── 10. Export evolution history ──
	history := pop.ExportHistory()
	if history != nil {
		data, _ := json.MarshalIndent(history, "", "  ")
		fmt.Printf("\nEvolution history (%d generations):\n", len(history.Generations))
		fmt.Println(string(data))
	}

	// ── 11. Submit DAG evolution results to Coordinator ──
	fmt.Println("\n═══ Coordinator Decisions ═══")
	submitDAGEvolution(ctx, coord, genomeReg, diffReg, pop)

	// ── 12. Show final decision results ──
	decisions := coord.DecisionHistory()
	fmt.Printf("\nDecision history: %d entries\n", len(decisions))
	for _, d := range decisions {
		status := "✅"
		if d.Decision != coordinator.DecisionApply {
			status = "⏳"
		}
		fmt.Printf("  %s %s: %s (fitness: %.1f)\n",
			status, d.Decision, d.Reason, d.Proposal.Fitness)
	}

	fmt.Println("\n✅ GA full evolution demo completed")
}

// runGeneration runs one generation of GA evolution.
func runGeneration(ctx context.Context, pop *genome.Population,
	mutator genome.MutatorInterface, crosser genome.CrossoverInterface,
	hintProvider *mockHintProvider, gen int) {
	scorer := func(s *mutation.Strategy) float64 {
		return multiObjectiveScore(s, hintProvider)
	}

	// Score all agents
	pop.ScoreAgents(scorer)

	// Run one generation of evolution
	if err := pop.Evolve(ctx, mutator, crosser); err != nil {
		panic(err)
	}
}

// printGenerationStats prints statistics for a generation.
func printGenerationStats(pop *genome.Population, gen int) {
	stats := pop.Stats()
	best := pop.BestStrategy()
	toolSel := "auto"
	if best != nil {
		if v, ok := best.Params["tool_selector"]; ok {
			toolSel = fmt.Sprintf("%v", v)
		}
	}
	fmt.Printf("  Gen %d: best=%.1f, avg=%.1f, pop=%d, tool=%s\n",
		gen+1, stats.BestScore, stats.AvgScore, stats.Size, toolSel)
}

// multiObjectiveScore computes fitness from quality, cost, and latency.
func multiObjectiveScore(s *mutation.Strategy, hp *mockHintProvider) float64 {
	quality := scoreQuality(s)
	cost := scoreCost(s)
	latency := scoreLatency(s)

	// Memory-guided confidence bonus
	confidence := hp.confidenceForStrategy(s)
	quality += confidence * 5.0

	// Multi-objective aggregation: quality prioritized, cost/latency penalized
	finalScore := quality*0.6 - cost*0.25 - latency*0.15

	// Record dimension scores
	s.DimensionScores = map[string]float64{
		"quality": quality,
		"cost":    cost,
		"latency": latency,
	}

	return max(0, finalScore)
}

// scoreQuality estimates strategy quality based on params.
func scoreQuality(s *mutation.Strategy) float64 {
	score := 50.0
	if v, ok := s.Params["temperature"]; ok {
		if t := toFloat64(v); t >= 0.5 && t <= 0.8 {
			score += 20
		} else if t < 0.3 || t > 0.9 {
			score -= 10
		}
	}
	if v, ok := s.Params["search_depth"]; ok {
		if d := toInt(v); d >= 3 && d <= 5 {
			score += 15
		} else if d < 2 {
			score -= 10
		}
	}
	if sel, ok := s.Params["tool_selector"]; ok {
		switch fmt.Sprintf("%v", sel) {
		case "priority":
			score += 10
		case "manual":
			score += 5
		}
	}
	return min(100, max(0, score))
}

// scoreCost estimates computational cost of a strategy.
func scoreCost(s *mutation.Strategy) float64 {
	cost := 10.0
	if v, ok := s.Params["max_tokens"]; ok {
		cost += float64(toInt(v)) / 500
	}
	if v, ok := s.Params["search_depth"]; ok {
		cost += float64(toInt(v)) * 5
	}
	if v, ok := s.Params["batch_size"]; ok {
		cost += float64(toInt(v)) * 2
	}
	return min(100, cost)
}

// scoreLatency estimates execution latency of a strategy.
func scoreLatency(s *mutation.Strategy) float64 {
	latency := 5.0
	if v, ok := s.Params["search_depth"]; ok {
		latency += float64(toInt(v)) * 8
	}
	if v, ok := s.Params["max_tokens"]; ok {
		latency += float64(toInt(v)) / 1000
	}
	return min(100, latency)
}

// toFloat64 safely converts an any value to float64.
func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case string:
		f := 0.0
		_, _ = fmt.Sscanf(val, "%f", &f)
		return f
	default:
		return 0
	}
}

// toInt safely converts an any value to int.
func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		i := 0
		_, _ = fmt.Sscanf(val, "%d", &i)
		return i
	default:
		return 0
	}
}

// buildInitialDAG creates a simple workflow DAG.
func buildInitialDAG() *engine.MutableDAG {
	steps := []*engine.Step{
		{ID: "input", Name: "Input Parser", AgentType: "parser", Input: "parse input"},
		{ID: "search", Name: "Search Tool", AgentType: "search", Input: "search", DependsOn: []string{"input"}},
		{ID: "process", Name: "Process Data", AgentType: "processor", Input: "process", DependsOn: []string{"search"}},
		{ID: "output", Name: "Output Format", AgentType: "formatter", Input: "format", DependsOn: []string{"process"}},
	}
	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		panic(err)
	}
	return dag
}

// registerEvolutionComponents creates coordinator, genome registry, and diff registry.
func registerEvolutionComponents(dag *engine.MutableDAG) (
	*coordinator.EvolutionCoordinator,
	*evogenome.Registry,
	*diff.Registry,
) {
	// Genome registry
	genomeReg := evogenome.NewRegistry()
	wfGenome := evogenome.NewWorkflowGenome(dag, evogenome.DefaultWorkflowGenomeConfig())
	if err := genomeReg.Register(wfGenome); err != nil {
		panic(err)
	}
	schedGenome := evogenome.NewSchedulerGenome(
		graph.NewDefaultScheduler(),
		evogenome.DefaultSchedulerGenomeConfig(),
	)
	if err := genomeReg.Register(schedGenome); err != nil {
		panic(err)
	}

	// Diff registry
	diffReg := diff.NewRegistry()
	if err := diffReg.Register(diff.NewWorkflowDiffer()); err != nil {
		panic(err)
	}
	if err := diffReg.Register(diff.NewSchedulerDiffer()); err != nil {
		panic(err)
	}

	// Patch registry with executor
	patchReg := patch.NewRegistry()
	g, gErr := graph.NewGraph("ga-evolution")
	if gErr != nil {
		panic(gErr)
	}
	for _, step := range dag.Steps() {
		fn, fErr := graph.NewFuncNode(step.ID,
			func(_ context.Context, _ *graph.State) error { return nil })
		if fErr != nil {
			panic(fErr)
		}
		if _, nErr := g.Node(step.ID, fn); nErr != nil {
			panic(nErr)
		}
	}
	for _, step := range dag.Steps() {
		for _, dep := range step.DependsOn {
			if _, eErr := g.Edge(dep, step.ID); eErr != nil {
				panic(eErr)
			}
		}
	}
	if _, sErr := g.Start("input"); sErr != nil {
		panic(sErr)
	}
	graphExec := graph.NewGraphPatchExecutor(g)
	_ = patchReg.Register("workflow.graph", graphExec)
	_ = patchReg.Register("graph.scheduler", graphExec)
	recoveryExec := engine.NewRecoveryPatchExecutor(dag)
	_ = patchReg.Register("recovery.strategy", recoveryExec)

	// Coordinator
	coord := coordinator.NewEvolutionCoordinator(
		coordinator.DefaultPolicy(), patchReg)

	return coord, genomeReg, diffReg
}

// submitDAGEvolution submits DAG evolution results to the coordinator.
func submitDAGEvolution(ctx context.Context, coord *coordinator.EvolutionCoordinator,
	genomeReg *evogenome.Registry, diffReg *diff.Registry, pop *genome.Population) {
	for _, name := range genomeReg.List() {
		gm, err := genomeReg.Get(name)
		if err != nil {
			continue
		}
		oldSnap, _ := gm.Snapshot(ctx)
		children, mErr := gm.Mutate(ctx, 3)
		if mErr != nil {
			continue
		}
		for _, child := range children {
			newSnap, sErr := child.Snapshot(ctx)
			if sErr != nil {
				continue
			}
			patches, dErr := diffReg.DiffAll(ctx, map[string]diff.SnapshotPair{
				name: {Old: oldSnap, New: newSnap},
			})
			if dErr != nil {
				continue
			}
			for _, p := range patches {
				bestScore := 0.0
				if best := pop.BestStrategy(); best != nil {
					bestScore = best.Score
				}
				coord.Submit(coordinator.PatchProposal{
					Patch:     p,
					Source:    coordinator.SourceGA,
					Reason:    fmt.Sprintf("GA: %s evolved", name),
					Priority:  6,
					Fitness:   bestScore,
					Timestamp: time.Now(),
				})
			}
		}
	}
	coord.Evaluate(ctx)
}

// ── Memory-guided provider (mock experience) ──

type evolutionHint struct {
	taskType   string
	tool       string
	confidence float64
}

type mockHintProvider struct {
	hints []evolutionHint
}

func (m *mockHintProvider) confidenceForStrategy(s *mutation.Strategy) float64 {
	confidence := 0.0
	for _, h := range m.hints {
		if sel, ok := s.Params["tool_selector"]; ok {
			if sel == h.tool {
				confidence = max(confidence, h.confidence)
			}
		}
	}
	return confidence
}

// ── Config helper functions ──

func mutableParams() map[string]mutation.ParamRange {
	return map[string]mutation.ParamRange{
		"temperature": {
			Values: []any{0.1, 0.3, 0.5, 0.7, 0.9},
		},
		"top_k": {
			Values: []any{10, 20, 40, 60, 80, 100},
		},
		"max_tokens": {
			Values: []any{1024, 2048, 4096, 8192},
		},
		"tool_selector": {
			Values: []any{"auto", "manual", "priority"},
		},
		"search_depth": {
			Values: []any{1, 2, 3, 4, 5},
		},
		"batch_size": {
			Values: []any{1, 3, 5, 10},
		},
	}
}

func promptPool() []string {
	return []string{
		"You are a helpful assistant. Complete the task efficiently.",
		"You are an expert programmer. Write clean, efficient code.",
		"You are a data analyst. Analyze data thoroughly and report findings.",
		"You are a system architect. Design robust and scalable solutions.",
	}
}

func toolPool() []string {
	return []string{"search", "read", "write", "exec", "calculate", "code"}
}

// ── Default random seed ──

func init() {
	_ = rand.New(rand.NewSource(time.Now().UnixNano()))
}
