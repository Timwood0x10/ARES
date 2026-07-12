# 遗传算法（GA）开发计划：接线 + 优化

## 一、核心发现：活跃路径不是 GA

### 当前活跃路径（DreamCycle.Run）

```
getCurrentStrategy()          → 取 1 个 active parent
mutator.Mutate(parent, 3)     → 变异出 3 个候选（λ=3）
findWinner(candidates, baseline) → 逐个 arena 测试，按 win rate 排序
stateManager.Deploy(winner)   → 挑最好的部署
```

**这是 (1+λ) 进化策略（Evolution Strategy），不是遗传算法。**

| GA 组件 | 活跃路径实际行为 | 状态 |
|---------|----------------|------|
| 种群 Population | 多个体共进化，N=20 默认 | ✅ |
| 适应度 Fitness | arena 测试 + tiered scorer 驱动选择 | ✅ |
| 选择 Selection | 6 种选择算子（tournament/rank/roulette/sus/truncation/nsga2） | ✅ |
| 交叉 Crossover | 3 种交叉（uniform/two_point/segment） | ✅ |
| 变异 Mutation | 6 种变异算子（param/prompt/tool + swap/inversion/scramble） | ✅ |
| 精英 Elitism | EliteCount 机制保留最优个体 | ✅ |
| 代际替换 | 完整 t→t+1 代际循环 + 稳态模式 | ✅ |
| 终止条件 | MaxGenerations + TargetFitness 双条件 | ✅ |
| 可配置旋钮 | 16 个参数，统一 config.yaml | ✅ |

### 死代码：genome.Population（内部 GA 引擎）

`internal/ares_evolution/genome/` 里有完整的 GA 实现，但 DreamCycle 根本不走这条路：

```
genome/population.go          → 种群管理、进化循环
genome/selection.go           → 6 种选择算子
genome/crossover.go           → 交叉算子
genome/adaptive.go            → 自适应变异率、多样性恢复
genome/multi_objective.go     → Pareto 多目标优化
genome/meta_evolution.go      → 元进化参数调优
genome/spatial_index.go       → 空间索引加速
genome/knowledge.go           → 知识库学习
genome/reflection.go          → LLM 反思分析
genome/guided_pipeline.go     → 反思→假设→知识蒸馏
```

**一句话：GA 引擎已写好，但没接进主循环。**

---

## 二、总体架构

```
┌─────────────────────────────────────────────────────────────────┐
│  DreamCycle（编排器）                                            │
│                          ┌──────────────────────────────┐      │
│  Run() {                │  genome.Population (GA 引擎)   │      │
│    ...                  │                              │      │
│    ┌─ 当前: (1+λ)ES ── │  ├─ Agents[N] 种群           │      │
│    │  mutator.Mutate() │  ├─ Evolve() 代际循环        │      │
│    │  findWinner()     │  ├─ Selection 6种算子        │      │
│    │  Deploy()         │  ├─ Crossover 交叉           │      │
│    └─────────────────── │  ├─ Mutation 变异            │      │
│                         │  ├─ Adaptive 自适应          │      │
│    ┌─ 目标: GA 路径 ── │  └─ MultiObjective 多目标    │      │
│    │  pop.Evolve()     │                              │      │
│    │  pop.ScoreAgents()│  └──────────────────────────────┘      │
│    │  pop.Best()       │                                        │
│    └───────────────────│                                        │
│  }                     │                                        │
└─────────────────────────────────────────────────────────────────┘
```

---

## 三、阶段一：GA 接线（Wiring）— 把 genome.Population 接入 DreamCycle

这是最核心的任务。把已有的 GA 引擎从"死代码"变成"活跃路径"。

### 任务 GA-1.1：DreamCycle 持有 genome.Population

**目标：** DreamCycle 内部维护一个 `*genome.Population`，而不是只持有一个 `parent Strategy`。

**改动点：**
- `internal/ares_evolution/dream_cycle.go` — `DreamCycle` 结构体已有 `population *genome.Population` 字段（第 99 行），但 `Run()` 方法中并未使用它来执行进化。需要改为：
  1. 初始化时用 `genome.NewPopulation()` 创建种群
  2. `Run()` 中调用 `population.Evolve()` 而不是 `mutator.Mutate()`
  3. 进化后取 `population.BestStrategy()` 部署

**文件：** `internal/ares_evolution/dream_cycle.go`, `genome_wiring.go`

### 任务 GA-1.2：评分闭环

**目标：** 在 `Evolve()` 之前给所有个体评分，形成选择→交叉→变异→评分的完整闭环。

**当前问题：** `genome.Population.Evolve()` 要求调用前所有个体已评分（`ensureEvaluatedBeforeSelection()` 守卫），但 DreamCycle 没有评分逻辑。

**改动点：**
- `DreamCycle.Run()` 中：
  1. 调用 `population.ScoreAgents(scorer)` 扫描所有个体
  2. 调用 `population.Evolve(ctx, mutator, crosser)` 执行一代进化
  3. 调用 `population.BestStrategy()` 取最优部署
- 评分函数 `scorer` 复用已有的 arena tester 逻辑

**文件：** `internal/ares_evolution/dream_cycle.go`, `genome_wiring.go`

### 任务 GA-1.3：Selection 配置

**目标：** 让 DreamCycle 可配置选择算子类型。

**改动点：**
- `DreamCycleConfig` 新增 `SelectionStrategy` 字段（支持 tournament/rank/roulette/sus/truncation）
- 初始化 population 时传入 `genome.WithSelectionStrategy()`
- 默认使用 tournament（size=3）

**文件：** `internal/ares_evolution/dream_cycle.go`, `genome/population_options.go`

### 任务 GA-1.4：Crossover 接入

**目标：** 在 `generateOffspring` 中调用交叉算子，而不是只变异。

**当前问题：** `genome.Population.generateOffspring()` 已经调用了 `crosser.Crossover()`（第 434 行），但 DreamCycle 传入的 crosser 是 nil。

**改动点：**
- `DreamCycle` 初始化时创建 `genome.NewCrossover()`
- 传入 `population.Evolve()` 的 crosser 参数

**文件：** `internal/ares_evolution/dream_cycle.go`, `genome_wiring.go`

### 任务 GA-1.5：PopSize & Elite 配置

**目标：** DreamCycle 可配置种群大小、精英数、变异率、生存率。

**改动点：**
- `DreamCycleConfig` 新增 `PopulationSize`, `EliteCount`, `MutationRate`, `SurvivalRate` 字段
- 初始化 population 时传入配置

**文件：** `internal/ares_evolution/dream_cycle.go`

### 任务 GA-1.6：终止条件

**目标：** 支持 max_generations 和 target_fitness 两种终止条件。

**改动点：**
- `DreamCycleConfig` 新增 `MaxGenerations`, `TargetFitness` 字段
- `Run()` 中检查是否达到终止条件，达标则停止进化

**文件：** `internal/ares_evolution/dream_cycle.go`

### 接线阶段测试计划

| 测试 | 方法 |
|------|------|
| DreamCycle 持有 population | 验证 `NewDreamCycle()` 后 population 非 nil |
| 评分闭环 | mock scorer，验证 `ScoreAgents` 被调用 |
| 代际推进 | 验证 `Evolve()` 后 `CurrentGeneration()` 增加 |
| 最优部署 | 验证 `BestStrategy()` 返回的个体被部署 |
| 终止条件 | 验证 `MaxGenerations` 达到后停止进化 |

---

## 四、阶段二：GA 优化（Optimization）— 基于 PyGAD 增强

### 任务 GA-2.1：新增交叉算子

**目标：** 在 `genome/crossover.go` 中新增 two_point、segment 交叉。

| 类型 | 说明 | 优先级 |
|------|------|--------|
| TwoPoint | 两个切分点，中间段来自 parent B，两侧来自 parent A | P0 |
| Segment | 随机连续块来自 parent B，其余来自 parent A | P1 |

**文件：** `internal/ares_evolution/genome/crossover.go`

### 任务 GA-2.2：新增变异算子

**目标：** 在 `mutation/mutator.go` 中新增 swap、inversion、scramble 变异。

| 类型 | 说明 | 优先级 |
|------|------|--------|
| Swap | 交换两个参数的值 | P0 |
| Inversion | 反转参数序列中一段的顺序 | P1 |
| Scramble | 随机打乱参数序列中一段 | P1 |

**文件：** `internal/ares_evolution/mutation/mutator.go`

### 任务 GA-2.3：稳态 GA 模式（Steady-State）

**目标：** 每次只替换部分个体（如 2 个），而非整个世代。

**当前：** 每次 Evolve() 替换整个种群。
**目标：** 新增 `EvolveSteadyState()` 方法，每次只生成少量 offspring，替换最差的个体。

**文件：** `internal/ares_evolution/genome/population.go`

### 任务 GA-2.4：完整 NSGA-II 多目标选择

**目标：** 实现基于非支配排序 + 拥挤距离的 NSGA-II 选择算子。

**当前：** 有 Pareto 排序和拥挤距离计算，但没有 NSGA-II 选择算子。
**目标：** 新增 `NondominatedSortingSelection` 选择算子。

**文件：** `internal/ares_evolution/genome/selection.go`, `multi_objective.go`

### 任务 GA-2.5：回调系统

**目标：** 支持 on_generation、on_fitness 回调。

**改动点：**
- `PopulationConfig` 新增 `Callbacks` 字段
- `doEvolve()` 在每代结束时调用回调
- 回调类型：`OnGenerationFunc`, `OnFitnessFunc`

**文件：** `internal/ares_evolution/genome/population.go`, `population_config.go`

### 任务 GA-2.6：历史追踪增强

**目标：** 每代记录完整统计数据，支持导出。

**当前：** `GenerationHistoryEntry` 已定义，但未在 DreamCycle 中暴露。
**目标：** 新增 `History()` 方法返回完整历史，支持 JSON 序列化。

**文件：** `internal/ares_evolution/genome/population.go`, `api/evolution/evolution.go`

### 任务 GA-2.7：适应度曲线可视化数据

**目标：** 导出适应度/多样性/基因值的时序数据，供外部绘图。

**文件：** 新增 `api/evolution/viz.go`

---

## 五、优先级排序与里程碑

### 里程碑 M1：GA 通跑（已完成 ✅）

```
GA-1.1  DreamCycle 持有 population
GA-1.2  评分闭环
GA-1.4  Crossover 接入
GA-1.5  PopSize & Elite 配置
```

### 里程碑 M2：GA 可配置（已完成 ✅）

```
GA-1.3  Selection 配置
GA-1.6  终止条件
```

### 里程碑 M3：GA 增强（已完成 ✅）

```
GA-2.1  新增交叉算子
GA-2.2  新增变异算子
GA-2.3  稳态 GA 模式
```

### 里程碑 M4：GA 高级功能（已完成 ✅）

```
GA-2.4  完整 NSGA-II
GA-2.5  回调系统
GA-2.6  历史追踪
GA-2.7  可视化数据
```

---

## 六、风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 评分成为瓶颈 | 每代需对所有个体评分，LLM 调用量大 | 复用 tiered scorer + budget + cache |
| 种群切换导致部署抖动 | 连续部署低分个体 | 精英保留 + shadow evaluation |
| 交叉策略不可预测 | 参数组合爆炸 | 从简单 uniform 开始，逐步开放 |
| (1+λ) ES 用户迁移 | 现有用户依赖当前行为 | 兼容模式：DreamCycle 可切换 ES/GA 模式 |

---

## 七、架构决策记录

### ADR-1：DreamCycle 同时支持 ES 和 GA 模式

**决策：** `DreamCycleConfig` 增加 `EvolutionMode` 字段，可选 `ModeEvolutionStrategy`（当前行为）和 `ModeGeneticAlgorithm`（GA 路径）。

**理由：** 用户可能依赖当前 (1+λ) ES 行为，GA 模式作为增强选项，不影响现有用户。

### ADR-2：GA 评分复用 arena tester

**决策：** GA 模式的评分函数使用已有 arena tester 的 `TesterInterface.Run()` 方法。

**理由：** 避免重复实现评分逻辑，tester 已有 quick-reject 和 adaptive batch 优化。

### ADR-3：genome.Population 作为内部状态，不暴露给外部

**决策：** DreamCycle 持有 genome.Population 作为内部字段，不直接暴露给 API 调用者。

**理由：** 封装实现细节，API 层只看到 `BestStrategy()` 和 `Stats()`。
## 八、PyGAD 功能对比与借鉴

[PyGAD](https://github.com/ahmedfgad/GeneticAlgorithmPython) 是 Ahmed F. Gad 开发的 Python 遗传算法库，本项目参考了其设计。

### 功能对比

| PyGAD 功能 | 本项目现状 | 差距 |
|-----------|-----------|------|
| `num_parents_mating` | 硬编码在 `Evolve()` 里 | 可配置参数 |
| `keep_parents` | 已有 `EliteCount` | 已覆盖 |
| `allow_duplicate` | 无 | 防止基因重复 |
| `gene_type` / `init_range` | 只有 `ParamRange` | 基因空间约束 |
| 回调 `on_generation` / `on_fitness` | 无 | 扩展性 |
| 可视化 `plot_fitness` | 无 | 调试/分析 |
| 自适应变异 `mutation_type="adaptive"` | 已有 `adaptive_distribution.go` | 已覆盖 |
| 稳态选择 `parent_selection_type="sss"` | 只有配置，没实现方法 | 需实现 `EvolveSteadyState()` |

### 已借鉴的设计

- **交叉算子类型**：uniform（默认）、two_point、segment — 对应 PyGAD 的 `crossover_type`
- **变异算子**：swap、inversion、scramble — 对应 PyGAD 的 `mutation_type`
- **选择算子**：tournament、rank、roulette、sus、truncation — 对应 PyGAD 的 `parent_selection_type`
- **多目标优化**：Pareto 排序 + 拥挤距离 — 对应 PyGAD 的 NSGA-II 支持

### 差距最大的三个功能（待实现）

1. **回调系统** — PyGAD 的 `on_generation`/`on_fitness`，任何框架级 GA 标配
2. **可视化数据导出** — 适应度序列、基因值分布，调试必备
3. **稳态 GA 模式** — `EvolveSteadyState()` 方法，PyGAD 的 "sss" 选择

---

## 九、配置文件参考

以下是一个完整的 evolution 配置示例，展示所有可配置参数及其含义：

```yaml
evolution:
  enabled: true                     # 是否启用进化管道（默认 false）
  population_size: 20               # 每代种群个体数（默认 20）
  elite_count: 2                    # 每代保留的最优个体数（默认 2）
  survival_rate: 0.6                # 每代存活比例 [0.0, 1.0]（默认 0.6）
  mutation_rate: 0.2                # 基因变异概率 [0.0, 1.0]（默认 0.2）
  min_mutation_rate: 0.05           # 自适应变异率下限（默认 0.05）
  max_mutation_rate: 0.5            # 自适应变异率上限（默认 0.5）
  generations: 15                   # 最大进化代数，0=无限（默认 15）
  breeding_pool_ratio: 0.5          # 用于交叉的种群比例 [0.0, 1.0]（默认 0.5）
  min_interval: "5m"                # 调度器最小运行间隔（默认 5m）
  selection_strategy: "tournament"  # 选择算子：tournament/rank/roulette/sus/truncation/random
  tournament_size: 3                # 锦标赛选择参赛人数（默认 3）
  crossover_type: "uniform"         # 交叉类型：uniform/two_point/segment
  target_fitness: 0                 # 目标适应度，0=不限制（默认 0）
  steady_state: false               # 是否启用稳态 GA（默认 false）
  steady_state_replace_rate: 0.3    # 稳态模式下每代替换比例 [0.0, 1.0]（默认 0.3）
```

### 参数说明

| 参数 | 说明 | 调优建议 |
|------|------|---------|
| `population_size` | 种群越大搜索越广，但每代计算成本越高 | 20-50 适合大多数场景 |
| `elite_count` | 保证最优解不丢失，值越大收敛越快但多样性降低 | 2-5 |
| `survival_rate` | 越高保留越多低分个体，增加多样性但减慢收敛 | 0.5-0.8 |
| `mutation_rate` | 越高探索越强，但可能破坏已有成果 | 0.1-0.3 |
| `selection_strategy` | 不同选择策略影响选择压力 | tournament 最常用 |
| `tournament_size` | 越大选择压力越大，收敛越快 | 2-5 |
| `crossover_type` | 影响参数重组方式 | uniform 最通用 |
| `steady_state` | 每次只替换部分个体，适合在线学习 | 稳定场景建议开启 |
