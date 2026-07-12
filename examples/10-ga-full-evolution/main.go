// GA Full Evolution Demo — 综合演示完整 GA 进化能力
//
// 演示内容：
//  1. 工具选择策略进化 — 根据任务类型自动选择最优工具组合
//  2. 工作流 DAG 拓扑进化 — 演化工作流节点结构与执行顺序
//  3. 记忆引导变异 — 历史经验偏置变异方向
//  4. 多目标 fitness — 质量 + 成本 + 延迟 综合评分
//
// 运行：go run examples/10-ga-full-evolution/main.go
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

	// ── 1. 创建基础策略（含工具选择、参数、prompt）──
	base := &mutation.Strategy{
		ID:      "root-strategy",
		Version: 1,
		Params: map[string]any{
			"temperature":   0.7,
			"top_k":         40,
			"max_tokens":    4096,
			"tool_selector": "auto", // 工具选择策略：auto/manual/priority
			"search_depth":  3,      // 搜索深度
			"batch_size":    5,      // 批处理大小
		},
		PromptTemplate: "You are a helpful assistant. Complete the task efficiently.",
		Score:          -1,
		CreatedAt:      time.Now(),
	}

	// ── 2. 创建变异器（配置参数范围 + 工具池）──
	mutator, err := mutation.NewMutator(
		mutation.WithParamRanges(mutableParams()),
		mutation.WithPromptPool(promptPool()),
		mutation.WithToolPool(toolPool()),
	)
	if err != nil {
		panic(err)
	}

	// ── 3. 创建交叉器（支持 uniform/two_point/segment）──
	crosser, err := genome.NewCrossover(
		genome.WithSeed(42),
		genome.WithCrossoverType(genome.CrossoverUniform),
	)
	if err != nil {
		panic(err)
	}

	// ── 4. 创建种群（GA 核心引擎）──
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
	fmt.Printf("1. 种群初始化为 %d 个个体\n", pop.Size)

	// ── 5. 创建工作流 DAG（可进化拓扑）──
	dag := buildInitialDAG()
	fmt.Printf("2. 初始 DAG: %d 个节点\n", dag.NodeCount())

	// ── 6. 注册进化计算组件（Genome + Diff + Coordinator）──
	coord, genomeReg, diffReg := registerEvolutionComponents(dag)
	fmt.Println("3. 进化组件注册完成")

	// ── 7. 创建记忆引导提供者（模拟历史经验）──
	hintProvider := &mockHintProvider{
		hints: []evolutionHint{
			{taskType: "code", tool: "search", confidence: 0.85},
			{taskType: "code", tool: "read", confidence: 0.72},
			{taskType: "data", tool: "calculate", confidence: 0.91},
			{taskType: "data", tool: "exec", confidence: 0.65},
		},
	}
	fmt.Println("4. 记忆引导提供者已加载 (4 条经验)")

	// ── 8. 运行 GA 进化（5 代）──
	fmt.Println("\n═══ 开始 GA 进化 ═══")
	for gen := 0; gen < 5; gen++ {
		runGeneration(ctx, pop, mutator, crosser, hintProvider, gen)
		printGenerationStats(pop, gen)
	}

	// ── 9. 展示进化结果──
	fmt.Println("\n═══ 进化结果 ═══")
	best := pop.BestStrategy()
	if best != nil {
		fmt.Printf("最佳策略: %s (score: %.2f)\n", best.ID, best.Score)
		fmt.Printf("  参数: %v\n", best.Params)
		fmt.Printf("  prompt: %s\n", best.PromptTemplate)
		if best.DimensionScores != nil {
			fmt.Printf("  多目标评分: %v\n", best.DimensionScores)
		}
	}

	// ── 10. 导出进化历史──
	history := pop.ExportHistory()
	if history != nil {
		data, _ := json.MarshalIndent(history, "", "  ")
		fmt.Printf("\n进化历史 (%d 代):\n", len(history.Generations))
		fmt.Println(string(data))
	}

	// ── 11. 提交 DAG 进化结果到 Coordinator──
	fmt.Println("\n═══ Coordinator 决策 ═══")
	submitDAGEvolution(ctx, coord, genomeReg, diffReg, pop)

	// ── 12. 展示最终决策结果──
	decisions := coord.DecisionHistory()
	fmt.Printf("\n决策历史: %d 条\n", len(decisions))
	for _, d := range decisions {
		status := "✅"
		if d.Decision != coordinator.DecisionApply {
			status = "⏳"
		}
		fmt.Printf("  %s %s: %s (fitness: %.1f)\n",
			status, d.Decision, d.Reason, d.Proposal.Fitness)
	}

	fmt.Println("\n✅ GA 完整进化演示完成")
}

// runGeneration runs one generation of GA evolution.
func runGeneration(ctx context.Context, pop *genome.Population,
	mutator genome.MutatorInterface, crosser genome.CrossoverInterface,
	hintProvider *mockHintProvider, gen int) {
	scorer := func(s *mutation.Strategy) float64 {
		return multiObjectiveScore(s, hintProvider)
	}

	// 评分所有个体
	pop.ScoreAgents(scorer)

	// 执行一代进化
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
	fmt.Printf("  第 %d 代: best=%.1f, avg=%.1f, pop=%d, tool=%s\n",
		gen+1, stats.BestScore, stats.AvgScore, stats.Size, toolSel)
}

// multiObjectiveScore computes fitness from quality, cost, and latency.
func multiObjectiveScore(s *mutation.Strategy, hp *mockHintProvider) float64 {
	quality := scoreQuality(s)
	cost := scoreCost(s)
	latency := scoreLatency(s)

	// 记忆引导的置信度加成
	confidence := hp.confidenceForStrategy(s)
	quality += confidence * 5.0

	// 多目标聚合：质量优先，成本和延迟做惩罚
	finalScore := quality*0.6 - cost*0.25 - latency*0.15

	// 记录维度评分
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

// ── 记忆引导提供者（模拟历史经验）──

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

// ── 配置辅助函数 ──

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

// ── 默认随机数种子 ──

func init() {
	_ = rand.New(rand.NewSource(time.Now().UnixNano()))
}
