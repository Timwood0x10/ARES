# GA 进化系统深度解析 — 当策略自己学会交配

> 先声明一下，这是 GA 进化的完整介绍，不是零散的实战记录。它的副标题是"从 1 个策略到 1 个种群，再到 7 种选择策略、3 种交叉方式、4 种变异类型——GA 是怎么从玩具变成生产级引擎的"。我想通过自己两次重写 GA 系统的经历，分享一些架构上的思考。

---

## 一、一个天真的想法：单亲繁殖就够了

最开始写 GA 的时候，我觉得这事很简单。

我从进化系统（DreamCycle）那边已经有了 Mutator——从一个 parent 变异出若干子代，挑最好的那个替换上去。思路非常朴素：

```
Parent → Mutate → [Child A, Child B, Child C] → Arena PK → Best Child → 替换 Parent
```

每次只保留一个最优解，简单高效。我当时拒绝做种群的理由很充分："种群有什么用？每次只上一个策略上线，保留一堆次优策略浪费内存。"

跑了几天之后，问题暴露了。

第一次进化，temperature 从 0.7 变成了 0.3（赢了）。第二次进化，temperature 只能在 0.3 的基础上继续变异——如果 0.3 其实是个局部最优呢？你已经丢了 0.7 这个基因，再也找不回来了。

这就是经典的**遗传漂变（Genetic Drift）**——小种群 + 强选择压力 = 基因库快速收缩。生物学里种群数量低于某个阈值后，等位基因会因为随机抽样而丢失。我的系统里种群 = 1，基因丢失是必然的。

所以我决定重写——从"单亲繁殖"升级为"种群+交配"。保留一群幸存者，让它们互相交配产生后代，好的基因在不同个体间流动，不会因为某一代的偶然失误而永久丢失。

这是**第一次升级**：引入 Population、Crossover、Selection。

---

## 二、核心洞察：不是越复杂越好，是越多样越好

第二次升级的触发点更微妙。

第一次升级之后，GA 能跑了。种群 20，精英保留，锦标赛选择，均匀交叉——看起来一切正常。但跑了上百代之后，我发现了另一个问题：

**种群确实不会丢失基因了，但它会收敛得太快。**

Gen 1-5 的 diversity 从 35% 掉到 12%，Gen 10 以后稳定在 8% 左右。所有个体都长成了一个样子——参数趋同、prompt 趋同、工具选择趋同。进化变成了局部微调。

这不是 GA 的 bug，这是 GA 的天性：**选择压力越大，收敛越快。** 但收敛快不一定是好事——你收敛到的那个点可能只是局部最优。

我当时的反应是加参数：增加变异率、降低生存率、引入精英保留——但效果有限。直到我意识到问题不在参数，而在**机制**：

- **选择算子**只有锦标赛一种。不同的场景需要不同的选择压力。
- **交叉方式**只有均匀交叉一种。有时候你需要保留基因块（两点交叉），有时候你需要大段替换（分段交叉）。
- **没有多样性保护**。适应度共享（fitness sharing）、拥挤距离（crowding distance）这些经典机制一个都没有。

所以**第二次升级**的核心不是加配置项，而是搭了一个可插拔的算子架构，让进化策略可以根据场景组合。

现在的 GA 引擎有 **7 种选择算子、3 种交叉类型（含 3 种 prompt 继承模式）、4 种变异类型（含自适应分布）、多目标 NSGA-II 优化**——这些都是实打实的可切换策略，不是配置参数。

---

## 三、系统架构总览

GA 进化系统分三层，边界清晰：

```
┌─────────────────────────────────────────────────────────────────────┐
│                     api/evolution/ (公共 API)                        │
│    Population, DreamCycle, Mutator, Promoter 接口 + 适配器           │
│    外部模块和 AI 助手严禁导入 internal/                              │
└──────────────────────┬──────────────────────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────────────────────┐
│              internal/ares_evolution/ (核心 GA 引擎)                 │
│                                                                      │
│  ┌──────────┐   ┌──────────────┐   ┌────────────┐   ┌───────────┐  │
│  │ mutation │──▶│ genome       │──▶│ scheduler  │──▶│ 经验系统   │  │
│  │ .Strategy │   │ .Population  │   │ .Scheduler  │   │ experience│  │
│  │ .Mutator  │   │ .Selection[] │   │ .DreamCycle │   │ .Evidence  │  │
│  │ .Types    │   │ .Crossover[] │   │             │   │ .Store     │  │
│  └──────────┘   └──────────────┘   └────────────┘   └───────────┘  │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │  WiredEvolutionSystem (工厂+适配器)                             │  │
│  │  串联 Scheduler + DreamCycle + Population + 评分 + 谱系 + 管理  │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────┬──────────────────────────────────────────────┘
                       │ Phase 6 桥接
┌──────────────────────▼──────────────────────────────────────────────┐
│              internal/evolution/ (运行时进化引擎)                     │
│                                                                      │
│  genome/Registry  ──▶  diff/Registry  ──▶  coordinator/Coordinator  │
│  (Genome 接口)         (Differ 接口)       (补丁决策)                 │
│  MemoryGenome          DiffAll()           Apply/Reject/Delay        │
│  PlannerGenome                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

**设计原则：API 层只暴露接口，不暴露实现。** 外部使用者通过 `api/evolution.Population` 操作 GA，不需要知道内部有 7 种选择算子。AI 助手写代码时也只允许引用 `api/evolution` 下的包，不能触碰 `internal/`。

---

## 四、Population：种群的骨架

`internal/ares_evolution/genome/population.go` 定义了核心数据结构：

```go
type Population struct {
    Agents     []*mutation.Strategy  // 当前种群的个体
    Size       int                   // 目标种群大小（跨代不变）
    Generation int                   // 当前代数
    cfg        PopulationConfig      // 配置快照
    rng        *rand.Rand            // 确定性随机源，实验可复现

    bestScore  float64               // 历史最高分
    bestEver   *mutation.Strategy    // 历史最优个体
    paretoFront []*mutation.Strategy // NSGA-II Pareto 前沿
    stagnantGens int                 // 停滞计数
    currentMutationRate float64      // 自适应变异率
}
```

几个设计决策：

**读写锁保并发**：`Best()` 和 `Stats()` 用读锁，`doEvolve()` 用写锁。进化频率远低于查询频率，读写分离合理。

**配置即不可变快照**：`NewPopulation()` 时一次性确定配置，之后不能再改。进化的参数不应该被随意篡改。

**确定性随机源**：用 `time.Now().UnixNano()` 种子初始化。注释里标记了 `#nosec G404`——GA 不需要密码学安全的随机数。固定种子可以复现实验。

### 默认配置

```go
func DefaultPopulationConfig() PopulationConfig {
    return PopulationConfig{
        Size:              20,       // 种群大小
        EliteCount:        3,        // 精英数量
        MutationRate:      0.2,      // 变异率
        SurvivalRate:      0.6,      // 每代存活率
        SelectionStrategy: "tournament",
        BreedingPoolRatio: 0.3,      // 繁殖池比例
    }
}
```

20 是 GA 领域的一个经验值——太小（<10）容易遗传漂变，太大（>50）收敛太慢。配合 0.6 的存活率，每代保留 12 个个体，产生 8 个新后代。

### 进化流水线

```go
func (p *Population) doEvolve(ctx context.Context, mut MutatorInterface, cross CrossoverInterface) error {
    // 1. 排序（按 Score 降序）
    // 2. 选择（如锦标赛：洗牌 → 随机取 k 个 → 取最优）
    // 3. 精英保留
    // 4. 交叉（均匀/两点/分段 + prompt 继承模式）
    // 5. 变异（按变异率）
    // 6. 组装新种群
    // 7. 代数 +1
}
```

额外机制：

- **稳态 GA** (`EvolveSteadyState`)：每代只替换种群的一部分（由 `replaceRate` 控制），适合在线生产环境。`replaceRate` 被限制在 [0.1, 0.5]，防止替换过快导致种群震荡。
- **停滞恢复**：当 Best 连续多代不变，自动提高变异率、注入新鲜突变体、驱逐高龄代理。
- **适应度共享**：通过 `SelectionScore` 对拥挤区域个体施加惩罚，Sigma=0.3，NicheRadius=0.15。精英个体豁免。

---

## 五、选择算子：7 种策略，各有主场

`internal/ares_evolution/genome/selection.go`（876 行）实现了 7 种选择策略。这是第二次升级的核心交付——不是"加一两个选项"，而是搭了一个完整的策略枚举 + 工厂模式架构。

| 算子 | 机制 | 选择压力 | 何时用 |
|------|------|---------|--------|
| **Tournament** | Fisher-Yates 洗牌 → 随机选 k 个取最优 | 中（k 越大压力越大） | **默认推荐**。兼顾多样性与收敛速度。k=3 是经验值。 |
| **Rank** | 线性排名加权，best=N, worst=1 | 低 | 早期探索阶段，不想过早收敛。对异常值不敏感。 |
| **SUS** (Stochastic Universal Sampling) | 均匀间隔采样，N 个指针等距排列 | 中 | 需要最小化采样偏差时。比轮盘赌方差更小（一个个体不会被反复选中）。 |
| **RouletteWheel** | 比例选择，概率与分数成正比 | 高 | 分数差距大时能快速放大优势。但容易早熟收敛。 |
| **Truncation** | 只保留 top N% | 最高 | 确定性场景，你知道 top 区间就是好区间。但多样性最差。 |
| **LineageRank** | 谱系多样性惩罚，`penaltyThreshold` + `penaltyStrength` | 自适应 | Wired 模式下的特色算子。惩罚同谱系个体，鼓励探索不同血脉。 |
| **NondominatedSorting** | NSGA-II 非支配排序 + 拥挤距离 | 多目标 | **多目标优化专用**。见第七节。 |

### Tournament Selection 为什么是默认

代码实现非常简洁：

```go
func (s *Selection) TournamentSelection(population []*mutation.Strategy, numToSelect int) ([]*mutation.Strategy, error) {
    shuffled := make([]*mutation.Strategy, len(population))
    copy(shuffled, population)
    s.rng.Shuffle(len(shuffled), func(i, j int) {
        shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
    })

    selected := make([]*mutation.Strategy, 0, numToSelect)
    for len(selected) < numToSelect {
        best := shuffled[0]
        tournamentSize := s.tournamentSize
        for i := 1; i < tournamentSize && i < len(shuffled); i++ {
            if shuffled[i].Score > best.Score {
                best = shuffled[i]
            }
        }
        selected = append(selected, best)
    }
    return selected, nil
}
```

为什么选中它而不是更复杂的方法？三个原因：

1. **计算简单**：O(n·k)，k 是 tournament size。没有排序，没有全局比较。
2. **并行友好**：每一轮 tournament 都是独立的，天然适合 `errgroup` 并发。
3. **选择压力可控**：k 越大，选到高分的概率越大。k=3 是中等压力，k=10 相当于截断选择。

### LineageRank：Wired 模式的特色算子

这是 Wired 模式下独有的选择策略。核心思路：**如果两个个体来自同一个谱系（血缘相近），给它们一个惩罚项，降低同时被选中的概率。**

```go
penalty := 1.0 - (lineageSimilarity * penaltyStrength)
effectiveScore := score * penalty
```

这样做的目的是维护种群多样性——在 Wired 模式下，每个线程独立进化自己的谱系，如果某个谱系特别强势（出了很多高分个体），会压制其他谱系的探索空间。LineageRank 通过惩罚同谱系个体来平衡选择压力。

---

## 六、交叉与变异：基因重组的工程实现

### 三种交叉类型

GA 的交叉算子决定"下一代怎么从父代继承基因"：

```go
CrossoverUniform   // 均匀交叉：每个参数独立地随机从任一父代继承（50/50）
CrossoverTwoPoint  // 两点交叉：两个切割点，交换中间段
CrossoverSegment   // 分段交叉：从父 B 取一段连续块，其余来自父 A
```

**Uniform** 是默认模式，因为参数之间没有天然顺序关系（temperature 和 top_k 互不依赖），均匀随机继承是最合理的选择。

**TwoPoint** 适合参数有隐式顺序的场景。比如 `[search_depth, temperature, top_k, batch_size]` ——如果 search_depth 和 batch_size 都是性能相关参数，两点交叉可以保留这个"性能块"同时交换"行为块"（temperature, top_k）。

**Segment** 适合已知某组参数之间存在依赖关系。比如 `tool_selector` 和 `batch_size` 在真实场景中可能存在交互效应，分段交叉可以整块替换。

### Prompt 继承：三种模式

Prompt 的交叉比参数更微妙——它不是结构化数据，是一段自然语言文本。我实现了三种继承模式：

```go
PromptInherit    // 从高分父代继承完整 prompt（保守模式）
PromptHalfSplit  // rune-aware 前半/后半拆分（支持中文！）
PromptUniform    // 随机从任一方选择（最高多样性）
```

**为什么 PromptHalfSplit 需要 rune-aware？** 中文场景下，按字节切割会把一个汉字切两半。`PromptHalfSplit` 使用 `utf8.RuneCountInString` 做长度计算，确保切割点落在字符边界上。

### 四种变异类型

```go
MutationParameter  // 参数值变异（temperature, top_k 等）
MutationPrompt     // PromptTemplate 变异
MutationTool       // 工具配置变异
MutationCrossover  // 交叉产生的策略（标记来源）
MutationRoot       // 初始策略
```

变异算子的核心是 **AdaptiveDistribution**——它根据历史效果动态调整各变异类型的概率：

```go
type AdaptiveDistribution struct {
    params  map[MutationType]float64  // 当前概率
    history map[MutationType][]float64 // 历史成功率
    window  int                        // 滑动窗口大小
}
```

如果最近几代 Prompt 变异产生的子代平均分数更高，`MutationPrompt` 的概率会自动上调。反之如果 Parameter 变异一直表现平庸，它的权重会逐渐降低。

### 代码中的 Mutator

通过 `api/evolution/mutation` 子包暴露给外部：

```go
mutator, err := pubmutation.NewMutator(pubmutation.MutatorConfig{
    ParamRanges: map[string][]any{
        "temperature":   {0.1, 0.3, 0.5, 0.7, 0.9},
        "top_k":         {10, 20, 40, 60, 80, 100},
        "max_tokens":    {1024, 2048, 4096, 8192},
        "tool_selector": {"auto", "manual", "priority"},
        "search_depth":  {1, 2, 3, 4, 5},
        "batch_size":    {1, 3, 5, 10},
    },
    PromptPool: []string{
        "You are a helpful assistant. Complete the task efficiently.",
        "You are an expert programmer. Write clean, efficient code.",
        "You are a data analyst. Analyze data thoroughly and report findings.",
        "You are a system architect. Design robust and scalable solutions.",
    },
    ToolPool:          []string{"search", "read", "write", "exec"},
    ParamMutationProb: 0.4,
    PromptMutationProb: 0.2,
})
```

---

## 七、多目标优化：不止一个"最好"

第二次升级中最实用的功能——**NSGA-II 多目标优化**。

### 问题

GA 的默认模式是"单目标最大化"：Score 越高越好。但在真实场景中，策略质量不是一个维度的问题：

- **成功率越高越好**，但可能更贵
- **输出质量越高越好**，但可能更慢
- **成本越低越好**，但可能牺牲质量
- **延迟越低越好**，但可能限制复杂处理

单目标优化要求你把这些维度人工加权成一个分数——但这个权重怎么定？不同任务类型可能需要不同的权重。

### NSGA-II 方案

`internal/ares_evolution/genome/multi_objective.go` 实现了非支配排序 + 拥挤距离：

```go
// Pareto 支配：a 在 >=1 个维度上严格优于 b，且所有维度都不差于 b
func ParetoDominance(a, b *mutation.Strategy) bool {
    betterInAny := false
    for _, dim := range DimensionOrder {
        aVal := a.DimensionScores[dim]
        bVal := b.DimensionScores[dim]
        dir := DimensionDirection[dim] // maximize / minimize
        if dir == Maximize && aVal > bVal { betterInAny = true }
        if dir == Maximize && aVal < bVal { return false }
        if dir == Minimize && aVal < bVal { betterInAny = true }
        if dir == Minimize && aVal > bVal { return false }
    }
    return betterInAny
}
```

四个优化维度及默认权重：

| 维度 | 方向 | 权重 | 含义 |
|------|------|------|------|
| `success_rate` | 最大化 | 0.40 | 成功率越高越好 |
| `quality` | 最大化 | 0.25 | 输出质量越高越好 |
| `cost` | 最小化 | 0.20 | API 成本越低越好 |
| `latency` | 最小化 | 0.15 | 响应时间越低越好 |

选择流水线：**非支配排序 → 分配 Pareto 等级 (0=最佳前沿) → 前沿内按拥挤距离排序 → 边界点无限大距离保证保留**

传入 `"nsga2"` 或 `"nondominated"` 作为选择策略字符串即可激活。权重仅用于报告需要单一标量分数时；选择过程本身基于完整的 Pareto 排序，不依赖权重。

### 实际体验

跑 NSGA-II 和跑单目标的最大区别是：**不再有"Best Score"这个说法。** 每个个体有多个维度的分数，你得到的是一个 Pareto 前沿。前沿上的所有策略都是"最优"的——只是最优在不同的维度上。

这对产品经理们来说可能是灾难性的（"到底该用哪个？"），但对工程来说这是诚实的——真实世界的策略质量就是多维的，强行压缩成一维只是在隐藏复杂性。

---

## 八、WiredEvolutionSystem：中央工厂

前两次升级完成了算子的可插拔化。但还有一个工程问题没解决：**谁来把这些组件串起来？**

Population、Scheduler、DreamCycle、Genealogy、StrategyStore、Scorer、Experience、Diff 引擎、Coordinator——10 多个组件，每个有自己的生命周期和依赖关系。如果每次用 GA 都要手动实例化这些组件，没人会用。

所以写了 `NewWiredEvolutionSystem`——一个中央工厂函数：

```go
type WiredEvolutionSystem struct {
    Scheduler     *EvolutionScheduler
    DreamCycle    *DreamCycle
    PopAdapter    *GenomePopulationAdapter
    Population    *genome.Population
    Genealogy     *PopulationGenealogyRecorder
    StrategyStore StrategyStore
    ActiveStrategyManager *ActiveStrategyManager
    ShadowEvaluator       *ShadowEvaluator
    FeedbackRecorder      *FeedbackRecorder
    TieredScorer          *scoring.TieredScorer
    ScoreCache            *scoring.ScoreCache
    Metrics               *ares_observability.PrometheusMetrics
    Reflector             *genome.LLMReflector
    HypothesisGen         *genome.HypothesisGenerator
    MetaCtrl              *genome.MetaController
    DiffReg               *diff.Registry
    Coordinator           *coordinator.EvolutionCoordinator
    GenomeReg             *evogenome.Registry
    AfterGeneration       func(ctx, gen int, system) error  // 代后钩子
    AfterRun              func(ctx, system) error            // 运行后钩子
}
```

`RunIdleEvolution` 方法运行完整的 N 代进化循环：

```
评分 → 进化 → 谱系记录 → LLM 反射 → 元控制 → 差分引擎 → 协调器 → 代后钩子
```

评分流水线是分层设计的，`GenomePopulationAdapter` 负责组装：

```
1. ScoreCache 命中检查（避免重复评分）
2. TieredScorer → LLM 预算门控
3. 启发式回退（LLM 不可用时）
4. MemoryAwareScorer（基于经验证据调整评分）
5. BatchScorer 批量预填充
```

这种设计的好处是：**每层的关注点不同，耦合度低。** ScoreCache 只管缓存，不管评分逻辑；MemoryAwareScorer 只管经验调整，不管怎么调用 LLM。改其中一层不影响其他层。

---

## 九、经验系统：从数据到进化指导

GA 的变异需要方向，否则就是随机搜索。经验系统负责把历史观测数据转化成进化指导信号。

流水线：

```
ToolCallRecord → ToolCallExperienceCollector → Normalizer → MemoryExperienceStore → AggregateEvidence → EvolutionHint
```

### ToolCallRecord

捕获每次工具调用的详细指标：

```go
type ToolCallRecord struct {
    StrategyID        string
    TaskType          string
    ToolName          string
    LatencyMs         int
    Success           bool
    RetryCount        int
    ResultSizeBytes   int
    ErrorCode         string
    CalledAt          time.Time
}
```

### 置信度计算

经验的质量取决于样本量：

- 样本 < 10：`confidence = count/10 * 0.5`（最高 0.5）
- 样本 10-1000：`confidence = 0.5 + (count-10)/(1000-10) * 0.5`
- 样本 >= 1000：`confidence = 1.0`

只有置信度 >= 0.7 的经验才会被用于指导进化。

### EvolutionHint

从经验到进化指导的桥梁：

```go
type EvolutionHint struct {
    TaskType    string
    Problem     string
    Solution    string
    ToolHint    string
    ParamHints  map[string]any
    PromptHint  string
    Confidence  float64
}
```

```go
evHint := guidance.HintsForTask("data")
// 返回：{"tool": "calculate", "confidence": 0.91}
// 效果：GA 在选择工具变异时，calculate 的权重 +0.91
```

在实践中，如果某个策略在特定任务类型上持续失败（比如数据库 schema 迁移），经验系统捕获失败模式 → 规范化成提示（"DDL 操作使用显式事务块"）→ 提供给 GA 的突变算子 → 未来的突变偏向避免已知失败模式的方案。

---

## 十、两次升级的故事

回顾一下两次升级分别做了什么：

### 第一次升级：从"单亲"到"种群"

| 项目 | 升级前 | 升级后 | 为什么 |
|------|--------|--------|--------|
| 个体数 | 1 | 20 | 避免遗传漂变 |
| 交配 | 无（只有变异） | 交叉 + 变异 | 基因流动 |
| 选择 | 无（只有排序） | 锦标赛/排名/截断 | 可控选择压力 |
| 部署 | 直接替换 | 谱系记录 + 策略存储 | 可追溯 |

### 第二次升级：从"单一算子"到"可插拔算子架构"

| 项目 | 升级前 | 升级后 | 为什么 |
|------|--------|--------|--------|
| 选择算子 | 仅锦标赛 | 7 种 | 不同场景需要不同选择压力 |
| 交叉类型 | 仅均匀 | 3 种 + 3 种 prompt 模式 | 参数结构不同，继承方式不同 |
| 变异类型 | 仅参数 | 4 种 + 自适应分布 | 变异类型需要根据效果动态调整 |
| 优化目标 | 单目标 | 多目标 NSGA-II | 真实世界的权衡是多维的 |
| 评分 | 硬编码 | 分层流水线 | 缓存/LLM/启发式解耦 |
| 运行时 | 仅策略参数 | Memory/Planner 基因组 | GA 不只调优策略 |

---

## 十一、公共 API：给 AI 助手的安全边界

GA 系统的公共 API 在 `api/evolution/` 下：

```go
// Population — GA 核心
type Population interface {
    Agents() []Agent
    Size() int
    CurrentGeneration() int
    BestScore() float64
    BestStrategy() *Strategy
    ScoreAgents(scorer ScorerFunc)
    Evolve(ctx context.Context) error
}

// Mutator — 变异算子（在 mutation 子包）
// 通过 MutatorConfig 配置参数范围、prompt 池、工具池

// Promoter — 晋升/降级系统
type Promoter interface {
    Evaluate(ctx context.Context, strategyID string, successRate, confidence float64) (string, error)
    Promote(ctx context.Context, strategyID string) error
    Demote(ctx context.Context, strategyID string) error
}

// DreamCycle — 进化调度封装
type DreamCycle interface {
    Run(ctx context.Context, data CallbackData) error
    SetEnabled(enabled bool)
    IsEnabled() bool
    TaskCount() int64
}
```

完整的使用流程在 `examples/10-ga-full-evolution/main.go` 中，展示：

1. **工具选择策略进化**：6 个参数范围（temperature, top_k, max_tokens, tool_selector, search_depth, batch_size）
2. **记忆引导变异**：历史经验偏置评分（`hintProvider.confidenceForStrategy`）
3. **多目标适应度**：`quality*0.6 - cost*0.25 - latency*0.15`
4. **5 代进化**：锦标赛选择，种群大小 20，精英 3
5. **Promoter 评估**：冠军决策（promote/demote）

核心流程代码（去掉 mock 和展示逻辑后约 60 行实际逻辑）：

```go
// 1. 创建基础策略
base := &pubmutation.Strategy{
    ID: "root", Params: map[string]any{
        "temperature": 0.7, "top_k": 40, "max_tokens": 4096,
        "tool_selector": "auto", "search_depth": 3, "batch_size": 5,
    },
}

// 2. 创建变异器
mutator, _ := pubmutation.NewMutator(cfg) // 配置参数范围 + prompt 池 + 工具池

// 3. 创建种群
population, _ := pubevolution.NewPopulation(base, popCfg) // 20 个体，锦标赛选择

// 4. 进化循环
for gen := 0; gen < 5; gen++ {
    population.ScoreAgents(multiObjectiveScorer) // 多目标评分
    population.Evolve(ctx)                       // 一代进化
}

// 5. 评估冠军
promoter.Evaluate(ctx, best.ID, bestScore, confidence)
```

---

## 十二、Phase 6：运行时进化引擎

第三次升级（严格说是 Phase 6 的桥接）将 GA 扩展到了运行时组件层面。

除了调优策略参数，GA 现在还能进化：

- **MemoryGenome**：记忆参数（MaxHistory [3,50], MaxSessions [20,500], MaxDistilledTasks [500,20000], UseStructuredCleaning）
- **PlannerGenome**：规划参数（Strategy "balanced"/"architecture-first"/"memory-first", MaxSources [3,30], MinRelevance [0.1,0.9]）

Diff 引擎负责将基因组差异转化为可部署的补丁：

```go
Genome (old) ──┐
                ├──→ Diff Engine ──→ []RuntimePatch
Genome (new) ──┘
```

协调器负责决定补丁的命运：

- 适应度 >= 60.0 → Apply（立即部署）
- 适应度 < 30.0 → Reject（丢弃）
- 两者之间 → Delay（等待更多证据）

---

## 十三、写在最后

GA 进化系统从最初的"1 个策略反复变异"到现在的"7 种选择 × 3 种交叉 × 4 种变异 × 多目标优化"，我最大的感受不是技术上的——技术上的东西都是已知的经典算法。

最大感受是：**弹性的可插拔架构比"猜哪个配置最好"重要得多。**

第一次升级时，我坚信 Tournament Selection 是最好的选择策略。第二次升级时，我发现在不同场景下不同选择策略各有优势。如果我当初把锦标赛硬编码死，现在的 GA 引擎就没法适应不同的进化场景。

这也是整个 ares 系统的一个设计哲学：**不替用户做选择，给用户选择的工具。**

你不会在 GA 引擎里找到一个"最佳配置"预设。你会找到 7 种选择策略、3 种交叉类型、可以自定义的参数范围和评分函数——它们组合在一起，可以应对从"快速收敛"到"广泛探索"到"多目标权衡"的几乎任何进化场景。

GA 不再是一个"调参工具"。它是一个**策略生成器**——能自动发现人想不到的参数组合，能持续适应变化的任务分布，能基于历史经验不断优化自己的变异方向。

---

## 附录

[A] 这篇文章覆盖的代码位置：

| 模块 | 路径 |
|------|------|
| 公共 API | `api/evolution/evolution.go`, `api/evolution/mutation/mutator.go` |
| 核心引擎 | `internal/ares_evolution/genome/population.go` |
| 选择算子 | `internal/ares_evolution/genome/selection.go` |
| 交叉算子 | `internal/ares_evolution/genome/crossover.go` |
| 多目标优化 | `internal/ares_evolution/genome/multi_objective.go` |
| 变异算子 | `internal/ares_evolution/mutation/mutator.go` |
| Wired 系统 | `internal/ares_evolution/genome_wiring_system.go` |
| Wired 适配器 | `internal/ares_evolution/genome_wiring.go` |
| 经验系统 | `internal/ares_evolution/experience/` |
| 调度器 | `internal/ares_evolution/scheduler.go` |
| 运行时进化 | `internal/evolution/genome/`, `internal/evolution/diff/`, `internal/evolution/coordinator/` |
| 完整示例 | `examples/10-ga-full-evolution/main.go` |

[B] 如果你想手动试试 GA 进化，运行：

```bash
go run examples/10-ga-full-evolution/main.go
```

[C] 这篇文章没有覆盖的内容：
- TieredScorer 的 LLM 预算管理（一篇单独的 scorer 文章可能会讲）
- 晋升系统的冠军/挑战者详细逻辑（已经在 promoter 子系统里）
- 谱系记录和家谱树的具体实现（可以在 genealogy 文章里展开）
- 每个选择算子的 Benchmark 性能对比（可以作为单独的评测报告）

如果需要这些内容，可以在后续文章中展开。