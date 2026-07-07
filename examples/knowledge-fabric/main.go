// AKF demo — demonstrates the complete ARES Knowledge Fabric pipeline.
//
// Run:
//
//	go run examples/knowledge-fabric/main.go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

func main() {
	ctx := context.Background()

	fmt.Println("═══ ARES Knowledge Fabric Demo ═══")
	fmt.Println()

	// ── 1. Register data providers ─────────────────────────
	fmt.Println("1. Registering providers...")

	reg := provider.NewProviderRegistry()

	// Inject real knowledge from docs/articles/zh/ as KnowledgeObjects.
	// Each article becomes a KnowledgeObject with type, summary, tags, and confidence.
	_ = reg.Register(&memoryProvider{
		name: "docs-articles",
		objects: []*knowledge.KnowledgeObject{
			{
				ID: "wf-engine", Type: knowledge.ObjectArchitecture,
				Summary:    "工作流引擎：两套工作流系统。Workflow Engine（配置驱动 DAG，YAML 定义，热重载，HITL）和 Graph System（代码驱动 Fluent Builder，条件边，可插拔调度器）。数据流：YAML → WorkflowLoader → Workflow+Step → DAG → Executor → WorkflowResult",
				Confidence: 0.95,
				Tags:       []string{"workflow", "engine", "dag", "architecture"},
			},
			{
				ID: "wf-dag", Type: knowledge.ObjectDecision,
				Summary:    "为什么会有两套工作流系统：配置驱动的灵活性不够，需要代码动态加减节点、运行时修改拓扑结构。于是写了第二套 Graph System——Fluent Builder API、条件边、可插拔调度器。两套并行，服务不同用户群体。",
				Confidence: 0.92,
				Tags:       []string{"workflow", "dag", "decision", "architecture"},
			},
			{
				ID: "mem-distill", Type: knowledge.ObjectMemory,
				Summary:    "记忆蒸馏：Agent 学会遗忘和提炼。从词频分析起步，最终走向三层架构。对话历史 → RawExperience → Normalizer → KnowledgeObject。关键是提炼可复用的经验，不是简单存聊天记录。",
				Confidence: 0.94,
				Tags:       []string{"memory", "distillation", "architecture"},
			},
			{
				ID: "mem-normalizer", Type: knowledge.ObjectArchitecture,
				Summary:    "Memory Distillation 的标准化层：将原始记忆（对话、工具调用结果、运行时数据）统一为标准化经验。Normalizer → Classifier → Scorer → Distiller。目标是让不同来源的记忆可用同一套 Distiller 处理。",
				Confidence: 0.88,
				Tags:       []string{"memory", "normalizer", "pipeline", "architecture"},
			},
			{
				ID: "tool-calling", Type: knowledge.ObjectArchitecture,
				Summary:    "工具调用四条路径：Path1 LLM 驱动调用（parseToolCalls → CallTool → Registry.Execute），Path2 Planner 兜底（语义分析→能力规划→评分），Path3 Workflow Graph（ToolNode.Execute），Path4 MCP 外部工具。当 LLM 不靠谱时用确定性引擎兜底。",
				Confidence: 0.96,
				Tags:       []string{"tool", "calling", "planner", "mcp", "architecture"},
			},
			{
				ID: "tool-planner", Type: knowledge.ObjectDecision,
				Summary:    "Planner 兜底策略：当 LLM 选错工具或传错参数时，Planner 通过语义分析重新规划。Bridge.Execute → Planner.Plan → executeStepWithFallback。确定性引擎作为最后一道防线。",
				Confidence: 0.90,
				Tags:       []string{"tool", "planner", "decision", "fallback"},
			},
			{
				ID: "dag-conditional", Type: knowledge.ObjectArchitecture,
				Summary:    "DAG 条件边与动态路由（新增）：Step.Condition 在步骤执行前做条件跳过，Step.Router 在步骤执行后做动态路由。受控循环（LoopConfig）支持 MaxIterations/UntilCondition 有限次迭代。子图嵌套（Step.SubWorkflow）让工作流可递归组合。",
				Confidence: 0.91,
				Tags:       []string{"dag", "conditional", "router", "loop", "new-feature"},
			},
			{
				ID: "dag-checkpoint", Type: knowledge.ObjectDecision,
				Summary:    "状态检查点：WithCheckpointStore 每步完成后自动持久化 StepResult。支持 PostgreSQL/SQLite/Redis 后端。非阻塞写入，失败不中断执行。Graph 包也支持每节点后保存 executed 集合 + State 快照。",
				Confidence: 0.87,
				Tags:       []string{"dag", "checkpoint", "persistence", "new-feature"},
			},
			{
				ID: "evol-intro", Type: knowledge.ObjectDecision,
				Summary:    "自主进化（GA Pipeline）：TournamentSelection + UniformCrossover + 5 类 Mutation + TieredScorer + DreamCycle。Agent 可以自我改进策略参数和 Prompt。配合 Memory Distillation 使用，蒸馏出的 KnowledgeObject 影响进化方向。",
				Confidence: 0.85,
				Tags:       []string{"evolution", "ga", "pipeline", "memory"},
			},
			{
				ID: "chaos-intro", Type: knowledge.ObjectDocument,
				Summary:    "混沌工程（Arena）：13 种故障注入类型。Survival Mode（随机攻击+ResilienceScore），Scenario Mode（YAML 定义有序故障序列）。Agent 在混沌环境下验证鲁棒性。和市场做市（Market Making）的 Chaos 集成。",
				Confidence: 0.83,
				Tags:       []string{"chaos", "testing", "resilience", "arena"},
			},
		},
	})

	_ = reg.Register(&memoryProvider{
		name: "docs-cross-ref",
		objects: []*knowledge.KnowledgeObject{
			{
				ID: "cross-wf-mem", Type: knowledge.ObjectMemory,
				Summary:    "工作流引擎调用记忆蒸馏：当 DAG 步骤失败时，通过 Memory Distillation 查询类似历史，用蒸馏出的经验指导恢复策略。Workflow Executor 的 StepRecoveryHandler 会查询经验库，匹配成功后自动调整步骤配置。",
				Confidence: 0.80,
				Tags:       []string{"workflow", "memory", "recovery", "cross-ref"},
			},
			{
				ID: "cross-tool-mem", Type: knowledge.ObjectMemory,
				Summary:    "工具调用与记忆蒸馏集成：工具执行结果被蒸馏为 KnowledgeObject，后续同类请求直接关联经验。Tool Execution Bridge 在运行前查询相关记忆，减少 LLM 调用次数。",
				Confidence: 0.82,
				Tags:       []string{"tool", "memory", "distillation", "cross-ref"},
			},
		},
	})

	fmt.Printf("   Registered %d providers, %d articles loaded\n", len(reg.List()), 12)

	// ── 2. Set up planner + discovery + runtime ─────────────
	fmt.Println("2. Building knowledge pipeline...")

	pipeline := knowledge.NewKnowledgePipeline(nil, nil, nil, nil)
	qp := &simpleQueryPlanner{}
	sd := planner.NewSourceDiscovery(reg, qp)
	planr := planner.NewKnowledgePlanner()

	rt := runtime.New(
		planr, sd, reg, pipeline,
		[]runtime.Linker{&runtime.DefaultLinker{}},
		[]runtime.Reducer{&runtime.DefaultReducer{}},
	)

	// ── 3. Execute: Plan → Load → Link → Reduce → Graph ────
	fmt.Println("3. Executing knowledge pipeline...")
	fmt.Printf("   Goal: %q\n", "ARES 工作流引擎是如何设计的？有哪些增强？")

	start := time.Now()
	graph, err := rt.Execute(ctx, "ARES 工作流引擎设计，DAG 增强，条件边，循环，检查点",
		knowledge.TokenBudget{MaxTokens: 5000, ForGraph: 3000},
		&runtime.Config{MaxConcurrentProviders: 5},
	)
	if err != nil {
		panic(err)
	}
	elapsed := time.Since(start)

	fmt.Printf("   Built graph: %d nodes, %d edges (%v)\n\n",
		len(graph.Nodes), len(graph.Edges), elapsed)

	// Print the graph structure.
	fmt.Println("═══ Graph Structure ═══")
	for id, obj := range graph.Nodes {
		tagStr := ""
		if len(obj.Tags) > 0 {
			tagStr = fmt.Sprintf(" [%s]", obj.Tags[0])
			if len(obj.Tags) > 1 {
				tagStr = fmt.Sprintf(" [%s, ...]", obj.Tags[0])
			}
		}
		fmt.Printf("  ◉ %-20s %-12s %s\n", id, "("+string(obj.Type)+")", tagStr)
	}
	fmt.Println()
	for _, e := range graph.Edges {
		fmt.Printf("  %s ──[%s]──▶ %s  (score: %.2f)\n", e.From, e.Name, e.To, e.Score)
	}

	// ── 4. Compile: Graph → Prompt / JSON ───────────────────
	fmt.Println("\n4. Compiling context...")

	comp := compiler.NewDefaultCompiler()
	cfg := compiler.CompileConfig{
		Formats: []compiler.Format{compiler.FormatPrompt, compiler.FormatJSON},
	}
	compiled, compErr := comp.Compile(ctx, graph, cfg)
	if compErr != nil {
		panic(compErr)
	}

	fmt.Println("\n═══ Compiled Context (Prompt) ═══")
	fmt.Println(compiled.Formats[compiler.FormatPrompt])

	fmt.Println("\n═══ Compilation Stats ═══")
	fmt.Printf("   Input nodes:   %d\n", compiled.Metrics.InputNodes)
	fmt.Printf("   Input edges:   %d\n", compiled.Metrics.InputEdges)
	fmt.Printf("   Output tokens: ~%d\n", compiled.Metrics.OutputTokens)
	fmt.Printf("   Total time:    %v\n", elapsed)
}

// memoryProvider is a simple in-memory GraphProvider for demo purposes.
type memoryProvider struct {
	name    string
	objects []*knowledge.KnowledgeObject
}

func (p *memoryProvider) Name() string { return p.name }

func (p *memoryProvider) IntentMatch(_ knowledge.Intent) float64 {
	switch p.name {
	case "memory":
		return 0.9
	case "code":
		return 0.7
	default:
		return 0.5
	}
}

func (p *memoryProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	ch := make(chan *knowledge.KnowledgeObject, len(p.objects))
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		defer close(errCh)
		for _, obj := range p.objects {
			ch <- obj
		}
	}()
	return ch, errCh
}

// simpleQueryPlanner returns a fixed query plan for demo.
type simpleQueryPlanner struct{}

func (q *simpleQueryPlanner) PlanQuery(_ context.Context, req planner.KnowledgeRequirement, _, _ string) (*planner.QueryPlan, error) {
	return &planner.QueryPlan{
		Query:      "demo query",
		QueryType:  planner.QuerySQL,
		MaxResults: req.MaxResults,
	}, nil
}
