我认真看了你现在 ARES 的 Evolution 架构（README 已经很完整了），包括：

* Population
* Scoring Pipeline
* Crossover
* Mutation
* Dream Cycle（Trigger → Mutate → Evaluate → Adopt → Record Lineage）
* Bandit Feedback
* Flight Recorder
* ExperienceCheckpoint
* ExecutionCollector
* EvolutionPlugin
* Genome / Mutation / Scoring 包的拆分

整体来说，它已经不是”遗传算法 Demo”，而是真正开始往 Runtime Evolution 发展了。 

⸻

但是，我觉得Evolution 现在还是”串联”的。

我反而建议你变成：

Algorithm Evolution + LLM Evolution 双核架构。

不是两步。

而是两个引擎。

例如：

                Evolution Engine
                       │
        ┌──────────────┴──────────────┐
        │                             │
Algorithm Engine              LLM Engine
        │                             │
Mutation                     Reflection
Selection                    Self Critique
Fitness                      Improvement
Genome                        Prompt
Policy                        Reasoning

它们共同操作一个对象：

type Genome struct {
    RuntimeGenes
    WorkflowGenes
    PromptGenes
    PolicyGenes
    MemoryGenes
}

Genome 只有一个。

只是：

Algorithm

负责探索（Exploration）

LLM

负责解释（Explanation）

⸻

为什么？

因为遗传算法最大的问题一直都是：

不知道为什么。

例如：

Generation 15
Fitness
0.81
↓
Generation 16
Fitness
0.93

为什么？

不知道。

GA 不知道。

⸻

而 LLM 正好相反。

LLM 最擅长：

总结原因。

例如：

Flight Recorder：

Workflow
A -> B -> C
耗时：
2.4s
Tool2 Timeout
Retry
成功
Memory Hit
0
Cost
$0.31

Algorithm：

Fitness
0.64

LLM：

可以生成：

Tool2 是瓶颈。

Retry 太保守。

Planner 可以提前过滤。

于是：

LLM 输出：

{
  "reason":"Tool timeout",
  "suggestion":"increase timeout",
  "confidence":0.82
}

然后：

Mutation

不用随机。

而是：

Guided Mutation。

⸻

所以 Mutation 我建议拆成两类。

第一类

Classic Mutation

例如：

随机：
Tool
A
↓
B

或者：

Temperature
0.6
↓
0.8

就是传统 GA。

⸻

第二类

Guided Mutation

来源：

LLM。

例如：

Failure Pattern
↓
Reflection
↓
Mutation Hint

例如：

LLM：

建议：
Planner
增加
Search
步骤。

Mutation：

就只变：

Planner。

不是全 Genome。

搜索空间瞬间缩小。

⸻

然后 Fitness 也别只有一个数字。

我觉得应该是：

Fitness
=
Algorithm Score
+
LLM Score

例如：

Algorithm
Success Rate
Latency
Cost
Recovery
Chaos
↓
0.81

LLM：

Code Quality
Reasoning
Coherence
Goal Completion
↓
0.89

最终：

Fitness
0.85

这样：

GA

负责：

Objective。

LLM

负责：

Subjective。

⸻

我最喜欢改的是 Crossover。

目前：

GA：

Parent A
Parent B
↓
Child

但是：

LLM

可以：

Semantic Crossover。

例如：

Parent A：

Prompt
很好

Parent B：

Workflow
很好

GA：

不知道哪个好。

LLM：

可以说：

保留 Prompt。

保留 Workflow。

Child：

就是：

Prompt(A)
+
Workflow(B)

而不是随机切片。

⸻

然后我觉得最值得加入的是：

Evolution Memory

很多 GA：

每代结束：

就没了。

ARES

已经有：

Memory Distillation。

为什么不用？

例如：

每代：

生成：

{
 "mutation":"replace planner",
 "fitness":"+12%",
 "reason":"tool timeout"
}

进入：

Evolution Memory。

以后：

LLM：

检索：

过去：
Tool Timeout
怎么解决？

Memory：

Generation
42
成功：
Planner
提前过滤

LLM：

直接建议：

不用重新探索。

⸻

我甚至觉得 Dream Cycle 可以升级。

现在：

Observe
↓
Mutate
↓
Evaluate
↓
Adopt

我建议：

Observe
      │
      ▼
Feature Extraction（纯算法）
      │
      ▼
Pattern Detection（统计）
      │
      ▼
LLM Reflection（解释）
      │
      ▼
Mutation Proposal（建议）
      │
      ▼
GA Mutation（生成候选）
      │
      ▼
Arena Evaluation（验证）
      │
      ▼
Selection（选择）
      │
      ▼
Knowledge Distillation（经验沉淀）
      │
      ▼
Lineage（记录谱系）

注意：

LLM

不是最后评分。

也不是直接生成。

而是：

站在 Algorithm 前面。

Algorithm：

负责：

搜索。

LLM：

负责：

缩小搜索空间。

⸻

我认为整个 Evolution 最好的分工应该是这一句话：

算法负责”找到更好的解”，LLM 负责”理解为什么这个解更好，并据此指导下一轮搜索”。

也就是说，两者不是串行替代关系，而是形成一个闭环：

* 算法层：负责可验证、可复现、低成本的大规模探索（Mutation、Crossover、Selection、Arena 回归测试、统计显著性）。
* LLM 层：负责从 Flight Recorder、ExecutionCheckpoint、Lineage 和 Evolution Memory 中提炼模式，生成可解释的 Reflection 和 Mutation Hint，并不断积累可复用的进化经验。

这样，ARES 的遗传算法就不会停留在”随机搜索 + 适应度”这一层，而会逐步演化成一种Experience-Guided Evolution：搜索仍由算法主导，但搜索方向越来越受到历史经验和语义理解的引导。这也与你现有的 Memory Distillation、Flight Recorder、ExecutionCollector 等模块天然契合，不需要推翻现有架构，而是在其上增加一个真正有价值的 LLM Evolution 层。



-----


我觉得这个开发计划不能写成 “实现 Reflection → 实现 Evolution” 这种工程任务。

ARES 的 Evolution 应该是一条技术路线（Technical Roadmap），而不是 Feature List。

如果让我来设计，我会这样规划。

⸻

ARES Evolution v2 Roadmap

Experience-Guided Runtime Evolution

Algorithm explores. LLM understands. Runtime evolves.

⸻

Vision

ARES 的目标不是优化 Prompt，也不是自动写 Workflow。

ARES 的目标是：

让整个 Runtime 成为一个可以持续学习、持续进化、持续验证的自治系统。

因此：

LLM 只是 Runtime 的一个组件。

遗传算法不是 LLM 的执行器。

二者共同驱动 Runtime Evolution。

⸻

整体架构

                   Experience
                        │
                        ▼
             Observation Layer
────────────────────────────────────────────
Flight Recorder
Execution Trace
Metrics
Memory Distillation
Event Stream
Checkpoint
────────────────────────────────────────────
                        │
                        ▼
            Feature Extraction Engine
────────────────────────────────────────────
Failure Pattern
Latency
Cost
Tool Usage
Workflow Statistics
Memory Hit Rate
────────────────────────────────────────────
                        │
                        ▼
               Search Layer
────────────────────────────────────────────
Classic Genetic Algorithm
Bandit
Population
Selection
Mutation
Crossover
↓
Candidate Population
────────────────────────────────────────────
                        │
                        ▼
             Validation Layer
────────────────────────────────────────────
Arena
A/B Test
Regression Test
Replay
Safety Constraint
↓
Fitness
────────────────────────────────────────────
                        │
                        ▼
           Intelligence Layer
────────────────────────────────────────────
Reflection
Hypothesis Generation
Knowledge Distillation
Policy Optimization
Evolution Memory
────────────────────────────────────────────
                        │
                        ▼
          Adaptive Evolution Policy
────────────────────────────────────────────
Mutation Strategy
Selection Strategy
Workflow Policy
Scheduler Policy
Tool Policy
Memory Policy
────────────────────────────────────────────
                        │
                        ▼
              Next Generation

⸻

Phase 1 —— Observation Layer

目标

建立可学习的数据基础。

没有数据，就没有进化。

新增模块

ExecutionCollector
↓
FeatureExtractor
↓
EvolutionDataset

采集：

* Success Rate
* Cost
* Token
* Tool Latency
* Retry Count
* Memory Hit
* Workflow DAG
* Failure Reason
* MCP Statistics
* Scheduler Metrics

最终形成：

Evolution Sample

例如：

type EvolutionSample struct {
    WorkflowID
    Success
    Cost
    Duration
    ToolUsage
    RetryCount
    MemoryHitRate
    FailurePattern
    Fitness
}

⸻

Phase 2 —— Search Engine（纯算法）

保持 Algorithm 独立。

这一层：

不依赖 LLM。

包括：

* Population
* Mutation
* Crossover
* Tournament
* Bandit
* Multi-objective Fitness

这里继续强化：

Mutation Strategy
Selection Strategy
Fitness Function

所有：

Candidate

都是：

Algorithm

生成。

不是：

LLM

生成。

⸻

Phase 3 —— Intelligence Layer（LLM）

这一层：

不直接修改 Runtime。

它只负责：

理解。

新增：

Reflection

例如：

为什么：
Fitness
下降？

输出：

Reflection

⸻

新增：

Hypothesis

不是：

修改。

而是：

Hypothesis
增加：
Search
步骤
可能：
成功率提升。

注意：

只是：

Hypothesis。

不是：

事实。

⸻

新增：

Knowledge Distillation

例如：

Generation
28
↓
Planner
提前过滤
↓
Cost
下降
18%

形成：

Evolution Knowledge

以后：

检索。

⸻

Phase 4 —— Guided Evolution

这是：

真正融合。

Mutation：

分成：

Classic Mutation
+
Guided Mutation

Classic：

随机。

Guided：

根据：

Reflection

History

Knowledge

生成：

Mutation Hint。

例如：

{
  "target":"planner",
  "mutation":"insert_search_step",
  "confidence":0.81
}

GA：

负责：

验证。

不是：

相信。

⸻

Phase 5 —— Meta Evolution

这是：

ARES 最大创新。

LLM：

开始：

优化：

Evolution。

例如：

动态调整：

Mutation Rate
Population Size
Selection Strategy
Bandit Weight
Fitness Weight
Exploration Ratio

例如：

Mutation
20%
↓
10%

原因：

Population
收敛。

或者：

Tournament
↓
Roulette

因为：

 Diversity
下降。

这里：

LLM：

优化：

GA。

不是：

Workflow。

⸻

Phase 6 —— Runtime Evolution

开始：

优化：

整个 Runtime。

包括：

Workflow
Scheduler
Plugin
MCP
Memory
Prompt
Policy
Checkpoint
Recovery

例如：

自动：

Planner
↓
Coder
↓
Reviewer

演化成：

Planner
↓
Search
↓
Coder
↓
Reviewer

然后：

Arena

验证。

⸻

Phase 7 —— Scientific Evolution

这是：

最终目标。

Evolution：

不是：

Reflection。

而是：

Observation
↓
Hypothesis
↓
Experiment
↓
Evidence
↓
Knowledge
↓
Policy
↓
Evolution

每一次：

进化：

都有：

Evidence。

不是：

Prompt Engineering。

⸻

CLI

建议：

ares evolve observe
ares evolve analyze
ares evolve reflect
ares evolve mutate
ares evolve validate
ares evolve replay
ares evolve lineage
ares evolve policy
ares evolve rollback
ares evolve benchmark

⸻

新增目录建议

runtime/evolution/
    collector/
    feature/
    search/
        genetic/
        bandit/
        crossover/
        mutation/
    intelligence/
        reflection/
        hypothesis/
        distillation/
    validation/
        arena/
        replay/
        benchmark/
    policy/
    memory/
    lineage/

⸻

核心数据流

Execution
      │
      ▼
Observation
      │
      ▼
Feature Extraction
      │
      ▼
Classic Search (GA)
      │
      ▼
Candidate Population
      │
      ▼
Arena Validation
      │
      ▼
Fitness + Evidence
      │
      ▼
LLM Reflection
      │
      ▼
Hypothesis
      │
      ▼
Guided Mutation
      │
      ▼
Policy Adaptation
      │
      ▼
Next Generation

⸻

我认为这个 Roadmap 最大的变化

其实只有一句话：

把 LLM 从”决策者”降级为”研究员”，把遗传算法从”执行器”提升为”探索者”。

整个系统的职责会变得非常清晰：

* 算法负责探索（Explore）：持续产生候选方案，并通过 Arena、A/B Test、Replay 等机制获得客观证据。
* LLM 负责理解（Understand）：分析证据、总结规律、提出假设、沉淀知识，并影响下一轮探索策略。
* Runtime 负责演化（Evolve）：基于验证后的结果更新工作流、策略、调度器、记忆系统以及进化引擎自身。

最后，再加上一个我认为应该写进 ARES 文档首页的理念，它比单纯说”Autonomous Evolution”更能体现你的设计哲学：

Algorithm discovers possibilities. LLM explains evidence. Runtime evolves itself.

---

## Phase 1 Action Plan: Experience-Guided Dream Cycle

目标：Dream Cycle 从纯随机 Mutation 改为 Experience-Guided Mutation，利用现有的 FlightRecorder 和 ExecutionCollector 数据生成 Mutation Hint。

### 具体任务清单

#### Task 1: 创建 `internal/ares_evolution/mutation/llm_hint_provider.go`
创建 `LLMHintProvider`，实现 `HintProvider` 接口。核心逻辑：
- 收集 `DiagnosticRecords`（来自 ExecutionCollector / FlightRecorder）
- 调用 LLM 生成 `MutationHints`（target, action, confidence, reasoning）
- 使用 template-based prompt 调用 LLM Chat API
- 支持错误重试和 fallback

#### Task 2: 修复 `internal/ares_evolution/genome/genome_wiring_system.go`
当前 `newDreamMutator()` 使用的是 `rawMutator`（纯随机），应改为使用 `genomeMut`（带有 ExperienceGuidedMutator）。这样 DreamCycle 的突变就能利用 LLMHintProvider 生成的 Hint。

#### Task 3: 修改 `internal/ares_evolution/dream_cycle.go`
在 Step 1（getCurrentStrategy）和 Step 2（Mutate）之间新增分析步骤：
- 从 ExecutionCollector / FlightRecorder 收集近期诊断数据
- 构造 `DiagnosticRecords` 传给 LLMHintProvider
- 在日志中记录分析结果

#### Task 4: 修改 Service 层配置
在 `internal/ares_evolution/service/types.go` 和 `service.go` 中添加：
- `DreamCycleModes` 配置项（classic / guided）
- `LLMHintProviderConfig` 配置结构
- Wiring LLMHintProvider 到 SystemConfig

#### Task 5: 代码审查
- 运行 gofmt、go vet、golangci-lint
- 确保无 lint 问题

#### Task 6: 单元测试
- 编写 `llm_hint_provider_test.go`
- Mock LLM Client 验证 Hint 生成逻辑

#### Task 7: 集成验证
- 使用 `go test -race -cover` 运行现有测试确保无回归

### Phase 1 模块关系

```
ExecutionCollector / FlightRecorder
          │
          ▼
    DiagnosticRecords
          │
          ▼
  LLMHintProvider ──► LLM API
          │              │
          ▼              ▼
    []MutationHint    (LLM response)
          │
          ▼
  ExperienceGuidedMutator
          │
          ▼
  MutationStrategy (GenomeMutator)
          │
          ▼
     DreamCycle.Run()
```

---

## Phase 1 实现完成报告 (2026-06-30)

### 已完成工作

#### Task 1: `internal/ares_evolution/mutation/llm_hint_provider.go`
- [x] 创建 `LLMHintProvider`，实现 `mutation.HintProvider` 接口
- [x] 定义本地 `LLMClient` 接口：`Generate(ctx, prompt) (string, error)`
- [x] LLM 提示词基于 Go template，支持自定义模板
- [x] 环形缓冲区存储最近 outcomes（默认 10 条）
- [x] JSON 解析：支持 markdown code fence、缺失 confidence 的默认值
- [x] 优雅降级：LLM 调用失败返回空 hints，不阻塞流程
- [x] 线程安全（sync.RWMutex）

#### Task 2: 修复 genome_wiring_system.go
- [x] `buildDreamCycle` 中 `rawMutator` → `genomeMut`（line 441）
- [x] 保证 DreamCycle 使用 ExperienceGuidedMutator（当配置启用时）

#### Task 3: 修改 dream_cycle.go
- [x] `DreamCycle` 结构体新增 `hintProvider mutation.HintProvider` 字段
- [x] 新增 `WithDreamCycleHintProvider()` 选项函数
- [x] Step 7（deploy 成功）后记录 `StrategyOutcome`
- [x] `recordFailure()` 中记录失败 outcome

#### Task 3.5: 在 genome_wiring_system.go 中 wiring hint provider
- [x] `DependencyConfig` 新增 `HintProvider mutation.HintProvider` 字段
- [x] `buildDreamCycle()` 中通过 `WithDreamCycleHintProvider()` 传递

#### Task 4: 修改 Service 层
- [x] `SystemConfig` 新增 `EnableLLMHints`、`MaxHintHistory`、`LLMClient` 字段
- [x] `createWiredSystem()` 中自动构造 `LLMHintProvider` 并注入
- [x] 新增 `llmClientAdapter` 桥接 service/mutation 两个包的同名接口

### 验证
- [x] `go build ./internal/ares_evolution/...` — 通过
- [x] 所有新增代码与现有架构一致（桥接模式、适配器模式、选项模式）

### 待完成（后续 Phase）
- [x] Task 5: 代码审查（gofmt、go vet、golangci-lint）
- [x] Task 6: 单元测试（`llm_hint_provider_test.go` 及 DreamCycle outcome 验证）
- [x] Task 7: 集成验证测试
