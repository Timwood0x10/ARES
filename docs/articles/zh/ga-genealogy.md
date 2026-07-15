# 谱系记录与家谱树——从父代追溯到进化源头

> 本文深入解析 GoAgent 进化系统中两套并行的谱系记录系统：Agent 家谱树（事件驱动）和策略谱系（GA 驱动），涵盖 PopulationGenealogyRecorder、LineageRankSelection、谱系多样性测量、MetaController 动态切换以及交叉祖先追踪。所有代码均来自真实源码。

## 1. 两套谱系系统

GoAgent 的进化系统维护**两套并行的谱系记录**，服务于不同的目的：

```
┌──────────────────────────────────────────────────────────────┐
│                      谱系记录系统架构                            │
│                                                                │
│  ┌─────────────────────┐    ┌─────────────────────────────┐   │
│  │  系统 A：Agent 家谱树  │    │  系统 B：策略谱系（GA）      │   │
│  │  (ares_flight)       │    │  (ares_evolution)          │   │
│  ├─────────────────────┤    ├─────────────────────────────┤   │
│  │ 记录：Agent 生命周期   │    │ 记录：策略继承关系           │   │
│  │ 事件：Spawn/Death/    │    │ 结构：StrategyLineage       │   │
│  │       Resurrection   │    │ 存储：10000 条环形缓冲区      │   │
│  │ 结构：树形（有父节点）   │    │ 结构：扁平列表（有序）        │   │
│  │ 用途：Agent 可视化     │    │ 用途：多样性控制 + 选择      │   │
│  └─────────────────────┘    └─────────────────────────────┘   │
│                                                                │
│                        ┌──────────────┐                       │
│                        │  LineageRank  │                      │
│                        │  Selection    │                       │
│                        │  MetaController│                      │
│                        │  Guardrails   │                       │
│                        └──────────────┘                       │
└──────────────────────────────────────────────────────────────┘
```

## 2. StrategyLineage：核心数据结构

### 2.1 进化包定义

```go
// File: internal/ares_evolution/interfaces.go:191-224
type StrategyLineage struct {
    ParentID         string  `json:"parent_id"`          // 父策略 ID
    ChildID          string  `json:"child_id"`           // 子策略 ID
    MutationType     string  `json:"mutation_type"`      // 变异类型
    WinRate          float64 `json:"win_rate"`           // 胜率
    ScoreImprovement float64 `json:"score_improvement"`  // 分数改进
    ParentScore      float64 `json:"parent_score"`       // 父代分数
    ChildScore       float64 `json:"child_score"`        // 子代分数
    ImprovementSignificant bool  `json:"improvement_significant"` // 显著改进标记
    Timestamp        int64   `json:"timestamp"`          // 时间戳
}
```

### 2.2 GenealogyRecorder 接口

```go
// File: internal/ares_evolution/interfaces.go:226-228
type GenealogyRecorder interface {
    Record(ctx context.Context, lineage StrategyLineage) error
}
```

极简的接口设计——只有一个 `Record()` 方法。任何实现了该接口的类型都可以作为谱系记录器，方便测试 mock 和未来替换。

## 3. PopulationGenealogyRecorder：核心实现

### 3.1 数据结构

```go
// File: internal/ares_evolution/genome_wiring.go:846-960
type PopulationGenealogyRecorder struct {
    mu          sync.RWMutex
    lineages    []StrategyLineage
    maxLineages int // 默认 10000
    scoreHistory map[string]*ScoreRollingWindow // agentID → 滚动窗口
}
```

**maxLineages=10000**：这是一个环形缓冲区风格的截断策略。当谱系记录超过 10000 条时，丢弃最旧的记录。这保证了内存使用有上界，同时保留足够多的历史数据用于多样性分析。

### 3.2 Record() 方法

```go
func (r *PopulationGenealogyRecorder) Record(ctx context.Context, lineage StrategyLineage) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    r.lineages = append(r.lineages, lineage)

    // 超过上限时，丢弃最旧的记录
    if len(r.lineages) > r.maxLineages {
        excess := len(r.lineages) - r.maxLineages
        r.lineages = r.lineages[excess:]
    }

    return nil
}
```

### 3.3 ScoreRollingWindow：3 代滑动平均

```go
// File: internal/ares_evolution/genome_wiring.go:805-840
type ScoreRollingWindow struct {
    scores  []float64
    maxSize int // 默认 3
}

func (w *ScoreRollingWindow) Add(score float64) {
    if len(w.scores) >= w.maxSize {
        w.scores = w.scores[1:] // 移除最旧的
    }
    w.scores = append(w.scores, score)
}

func (w *ScoreRollingWindow) Mean() float64 {
    if len(w.scores) == 0 {
        return 0
    }
    var sum float64
    for _, s := range w.scores {
        sum += s
    }
    return sum / float64(len(w.scores))
}
```

**为什么用滚动平均而非单点分数？** 进化过程中策略的分数可能因对手不同而波动。3 代滑动平均可以平滑噪声，提供更稳定的基线用于计算改进度。

## 4. RecordPopulationLineage：GA 谱系记录的核心桥接

这是连接 `genome.Population` 与 `GenealogyRecorder` 的核心函数，逐代执行。

```go
// File: internal/ares_evolution/genome_wiring.go:968-1071
func RecordPopulationLineage(
    ctx context.Context,
    pop *genome.Population,
    recorder GenealogyRecorder,
    parentSnapshot []*mutation.Strategy,
    generation int,
) (int, error) {
```

### 4.1 执行流程

```
1. 空值保护 → pop == nil || recorder == nil → 直接返回
2. pop.Snapshot() → 获取当前代所有 agent 的线程安全副本
3. 构建 parentScores 查找表（map[string]float64）
4. 遍历当前代 agent：
   a. 跳过 ParentID == "" 或 Version <= 1
   b. 去重：同一 (parentID, childID) 对只记录一次
   c. 查找父代分数
   d. 如果 ParentID 包含 "\u00d7"（交叉）→ 两个父代分数取平均
   e. 优先使用滚动平均作为基线
   f. 计算 scoreDelta = child.Score - baselineScore
   g. 记录谱系
```

### 4.2 交叉祖先处理

```go
// 交叉处理：当 ParentID 包含 "\u00d7" 分隔符时
// 表示该策略是交叉产生的，有两个父代
if parts := strings.Split(agent.ParentID, "\u00d7"); len(parts) == 2 {
    if ps1, ok1 := parentScores[parts[0]]; ok1 {
        if ps2, ok2 := parentScores[parts[1]]; ok2 {
            parentScore = (ps1 + ps2) / 2  // 两个父代分数取平均
            ok = true
        }
    }
}
```

### 4.3 滚动平均基线

```go
// 优先使用滚动平均而非单点父代分数
// 原因是单点分数可能有噪声，3 代平均更稳定
baselineScore := parentScore
if useRolling {
    if rolling := historyRecorder.RollingMeanScore(agent.ParentID); rolling > 0 {
        baselineScore = rolling
    }
}
```

### 4.4 改进度计算

```go
scoreDelta := child.Score - baselineScore
improvementSignificant := scoreDelta > 0

lineage := StrategyLineage{
    ParentID:         agent.ParentID,
    ChildID:          agent.ID,
    MutationType:     string(agent.StrategyMutationType),
    ScoreImprovement: scoreDelta,
    ParentScore:      baselineScore,
    ChildScore:       child.Score,
    ImprovementSignificant: improvementSignificant,
    Timestamp:        time.Now().UnixMilli(),
}
```

## 5. 谱系多样性测量

谱系多样性是进化系统健康度的重要指标。GoAgent 在多个层面测量谱系多样性。

### 5.1 measureLineageDiversityLocked()

```go
// File: internal/ares_evolution/genome/adaptive.go:321-355
func (p *Population) measureLineageDiversityLocked() (float64, float64) {
    n := len(p.Agents)
    if n < 2 {
        return 1.0, 1.0
    }

    parentCount := make(map[string]int, n)
    for _, a := range p.Agents {
        pid := a.ParentID
        if pid == "" {
            pid = "(root)"  // 标准化空父代
        }
        parentCount[pid]++
    }

    maxCount := 0
    for _, c := range parentCount {
        if c > maxCount {
            maxCount = c
        }
    }

    // lineageDiv: 独特父代数 / 总 agent 数（归一化到 [0, 1]）
    // 1.0 = 每个 agent 都有不同的父代
    // 0.0 = 所有 agent 共享同一个父代
    lineageDiv := float64(len(parentCount)) / float64(n)

    // dominantShare: 最常见父代占比
    // 0.2 = 最常见父代占 20%
    // 1.0 = 所有 agent 来自同一个父代
    dominantShare := float64(maxCount) / float64(n)

    return lineageDiv, dominantShare
}
```

**返回值解读**：
- `lineageDiv = 0.5`：50% 的 agent 有独特的父代，其余 50% 共享父代
- `dominantShare = 0.6`：60% 的 agent 来自同一个父代

### 5.2 DiversityReport 结构

```go
// File: internal/ares_evolution/service/types.go:461-480
type DiversityReport struct {
    Numeric              float64 `json:"numeric"`                // 数值多样性
    Categorical          float64 `json:"categorical"`            // 类别多样性
    Lineage              float64 `json:"lineage"`                // 谱系多样性（1.0 = 全部不同父代）
    DominantLineageShare float64 `json:"dominant_lineage_share"` // 最常见父代占比
}
```

## 6. LineageRankSelection：谱系感知选择算子

当谱系多样性降低时，系统需要一种方式来主动选择来自不同谱系的策略。LineageRankSelection 正是为此而生。

### 6.1 配置

```go
// File: internal/ares_evolution/genome/selection.go:434-460
type LineageRankSelection struct {
    rng              *rand.Rand
    penaltyThreshold float64 // 默认 0.4——超过此比例则惩罚
    penaltyStrength  float64 // 默认 0.5——惩罚强度
}
```

### 6.2 核心算法：computeLineageRankWeights

```go
// File: internal/ares_evolution/genome/selection.go:609-658
func (s *LineageRankSelection) computeLineageRankWeights(sorted []*mutation.Strategy) []float64 {
    // 第 1 步：统计谱系分布
    lineageCount := make(map[string]int)
    for _, agent := range sorted {
        pid := agent.ParentID
        if pid == "" {
            pid = "(root)"
        }
        lineageCount[pid]++
    }
    total := float64(len(sorted))

    // 第 2 步：计算每个策略的权重
    // baseWeight = 排名（降序：最佳 = N, 最差 = 1）
    weights := make([]float64, len(sorted))
    for i, agent := range sorted {
        pid := agent.ParentID
        if pid == "" {
            pid = "(root)"
        }
        share := float64(lineageCount[pid]) / total
        rankWeight := float64(len(sorted) - i) // 最佳 = N, 最差 = 1

        // 第 3 步：对过度代表的谱系应用惩罚
        if share > s.penaltyThreshold {
            // excess = (share - 0.4) / 0.6，归一化到 [0, 1]
            excess := (share - s.penaltyThreshold) / (1.0 - s.penaltyThreshold)
            // penalty = 0.5 * excess，最大 0.5
            penalty := s.penaltyStrength * excess
            rankWeight *= (1.0 - penalty)
        }

        weights[i] = rankWeight
    }

    return weights
}
```

**算法效果示例**：

| 场景 | 谱系分布 | penaltyThreshold | 权重变化 | 效果 |
|------|---------|-----------------|---------|------|
| 均匀分布 | 5 个谱系各占 20% | 0.4 | 无惩罚 | 正常选择 |
| 集中分布 | 1 个谱系占 60%，其余 40% | 0.4 | 60% 谱系权重 × (1 - 0.5×0.33) = ×0.83 | 优势谱系略微削弱 |
| 极端集中 | 1 个谱系占 90% | 0.4 | 90% 谱系权重 × (1 - 0.5×0.83) = ×0.58 | 优势谱系显著削弱 |

### 6.3 Select() 流程

```go
func (s *LineageRankSelection) Select(pop *Population) ([]*mutation.Strategy, error) {
    // 1. 按分数降序排序
    sorted := pop.SortByScore()

    // 2. 计算谱系排名权重
    weights := s.computeLineageRankWeights(sorted)

    // 3. 加权轮盘选择
    selected := make([]*mutation.Strategy, pop.Params.SurvivorCount)
    totalWeight := 0.0
    for _, w := range weights {
        totalWeight += w
    }

    for i := 0; i < len(selected); i++ {
        r := s.rng.Float64() * totalWeight
        cumulative := 0.0
        for j, w := range weights {
            cumulative += w
            if r <= cumulative {
                selected[i] = sorted[j].Clone()
                break
            }
        }
    }

    return selected, nil
}
```

## 7. MetaController：动态策略切换

MetaController 是进化系统的"元决策层"，它根据谱系多样性指标动态选择选择算子。

### 7.1 切换逻辑

```go
// File: internal/ares_evolution/genome/meta_evolution.go:215-226
func (mc *MetaController) selectBestStrategy(div, lineageDiv float64) string {
    // 条件：谱系多样性和整体多样性都低于 0.3
    // 说明种群已经严重同质化
    if lineageDiv < 0.3 && div < 0.3 {
        return "lineage_rank"  // 切换到谱系感知选择
    }
    // ... 其他策略选择逻辑
}
```

**双重条件 lineageDiv < 0.3 && div < 0.3** 的设计意图：
- `lineageDiv < 0.3`：少于 30% 的 agent 有独特父代，说明谱系集中
- `div < 0.3`：整体多样性（数值/类别）也偏低
- 两者同时满足时，基本可以确定种群陷入了同质化陷阱

### 7.2 选择器工厂

```go
// File: internal/ares_evolution/genome/population.go:545-560
case "lineage_rank":
    return NewLineageRankSelection(
        WithLineageRankSeed(seed),
        WithLineagePenaltyThreshold(0.4),
        WithLineagePenaltyStrength(0.5),
    )
```

## 8. 多样性恢复机制

当谱系多样性过低时，系统会主动注入新鲜突变体。

### 8.1 DominantLineageShare > 0.6 触发注入

```go
// File: internal/ares_evolution/genome/population.go:405
if report.Overall < p.cfg.DiversityThreshold || report.DominantLineageShare > 0.6 {
    p.injectFreshMutantsLocked(len(elites))
}
```

当最常见的父代占比超过 60% 时，意味着超过半数的策略来自同一个祖先，系统会注入随机生成的突变体来增加多样性。

### 8.2 每个谱系精英保留

```go
// File: internal/ares_evolution/genome/population_guard.go:138-217
func (p *Population) preservePerLineageElites(survivors []*mutation.Strategy) []*mutation.Strategy {
    // 找出每个谱系的最佳策略
    lineageBest := make(map[string]int)
    for i, s := range survivors {
        pid := s.ParentID
        if pid == "" {
            pid = "(root)"
        }
        existingIdx, ok := lineageBest[pid]
        if !ok || s.Score > survivors[existingIdx].Score {
            lineageBest[pid] = i
        }
    }

    // 先保留每个谱系的前 1 名
    for _, idx := range lineageBest {
        elites = append(elites, survivors[idx].Clone())
    }

    // 剩余位置从全局 top 填充
    // ...
}
```

**配置**：
```go
// File: internal/ares_evolution/genome/population_config.go:111-150
PerLineageElites     bool `json:"per_lineage_elites"`      // 默认 true
PerLineageEliteCount int  `json:"per_lineage_elite_count"` // 默认 1
```

### 8.3 Guardrails 谱系集中度警告

```go
// File: internal/ares_evolution/guardrails.go:355-396
if maxShare > g.MaxLineageShare { // 默认 0.8
    event := GuardrailEvent{
        Level:           GuardrailWarning,
        Rule:            "lineage_concentration",
        ErrorCode:       ErrCodeLineageConcentration,
        Message:         fmt.Sprintf("lineage concentration %.2f exceeds threshold %.2f", maxShare, g.MaxLineageShare),
        SuggestedAction: "increase selection pressure or introduce external diversity",
    }
}
```

## 9. 交叉谱系追踪

交叉操作产生的子代有两个父代，这给谱系追踪带来了挑战。

### 9.1 ParentID 编码

```go
// File: internal/ares_evolution/genome/crossover.go:523-525
func formatParentIDs(idA, idB string) string {
    return idA + "\u00d7" + idB // Unicode 乘法符号 ×
}
```

交叉产生的子代，其 `ParentID` 字段格式为 `"parentA_ID×parentB_ID"`，使用 Unicode 乘法符号 `\u00d7` 分隔。

### 9.2 子代 ID 生成

```go
// File: internal/ares_evolution/genome/crossover.go:250-260
child := &mutation.Strategy{
    ID:       c.generateChildID(a.ID, b.ID),
    ParentID: formatParentIDs(a.ID, b.ID),
    Version:  maxVersion(a.Version, b.Version) + 1,
    StrategyMutationType: mutation.MutationCrossover,
}
```

### 9.3 谱系记录中的交叉分数处理

在 `RecordPopulationLineage` 中，交叉子代的父代分数取两个父代的平均值：

```go
if parts := strings.Split(agent.ParentID, "\u00d7"); len(parts) == 2 {
    if ps1, ok1 := parentScores[parts[0]]; ok1 {
        if ps2, ok2 := parentScores[parts[1]]; ok2 {
            parentScore = (ps1 + ps2) / 2
        }
    }
}
```

## 10. 系统生命周期中的谱系记录

### 10.1 RunIdleEvolution 集成

```go
// File: internal/ares_evolution/genome_wiring_system.go:681-710
// 演化前捕获父代快照（用于后续分数查找）
var parentSnapshot []*mutation.Strategy
if system.Genealogy != nil {
    parentSnapshot, _ = system.Population.Snapshot()
}

// 演化
if err := system.PopAdapter.Run(ctx); err != nil {
    // ...
}

// 谱系记录
if system.Genealogy != nil {
    _, err := RecordPopulationLineage(
        ctx, system.Population, system.Genealogy,
        parentSnapshot, gen,
    )
    // ...
}
```

### 10.2 DreamCycle 中的谱系记录

**ES 路径**：
```go
// File: internal/ares_evolution/dream_cycle.go:461-475
lineage := StrategyLineage{
    ParentID:         parent.ID,
    ChildID:          winner.strategy.ID,
    MutationType:     "dream_cycle",
    WinRate:          winner.winRate,
    ScoreImprovement: winner.scoreImprovement,
    ParentScore:      parent.Score,
    ChildScore:       winner.scoreImprovement + parent.Score,
    Timestamp:        time.Now().Unix(),
}
```

**GA 路径**（注意：不记录 ParentID—因为 GA 种群覆盖了上一代）：
```go
// File: internal/ares_evolution/dream_cycle_ga.go:96-106
lineage := StrategyLineage{
    ChildID:          best.ID,
    MutationType:     "ga_evolution",
    WinRate:          best.Score,
    ScoreImprovement: winner.scoreImprovement,
    ChildScore:       best.Score,
    Timestamp:        time.Now().Unix(),
}
```

## 11. 谱系多样性控制全景图

```
谱系多样性下降过程（不利方向）：
    lineageDiv ↓  →  dominantShare ↑
        │                 │
        ▼                 ▼
    MetaController    population.go
    lineageDiv < 0.3  DominantLineageShare > 0.6
        │                 │
        ▼                 ▼
    "lineage_rank"    injectFreshMutants()
        │                 │
        ▼                 ▼
    LineageRankSelection  新鲜突变体注入
    (惩罚优势谱系)        (增加新谱系)
        │                 │
        └───────┬─────────┘
                ▼
          谱系多样性恢复 ↑
                │
                ▼
          Guardrails 监控
          MaxLineageShare = 0.8
```

## 12. 总结

谱系记录系统是 GoAgent 进化框架中**多样性控制**的核心基础设施：

1. **两套并行系统**：Agent 家谱树（事件驱动，树形结构）和策略谱系（GA 驱动，扁平列表）服务于不同的可视化与分析需求
2. **PopulationGenealogyRecorder**：10000 条上限的环形缓冲区，配合 ScoreRollingWindow（size=3）提供噪声鲁棒的改进度基线
3. **LineageRankSelection**：penaltyThreshold=0.4 时开始惩罚过度代表的谱系，penaltyStrength=0.5 控制惩罚力度
4. **MetaController**：在 lineageDiv < 0.3 && div < 0.3 时自动切换到谱系感知选择
5. **多样性恢复**：DominantLineageShare > 0.6 触发新鲜突变体注入，每个谱系保留 1 个精英
6. **交叉祖先**：使用 `\u00d7` 分隔符编码双亲，谱系记录中取两个父代分数的平均值为基线

这套系统保证了进化算法不会过早收敛到局部最优——通过追踪"谁是谁的后代"并主动惩罚同质化谱系，维持了种群的遗传多样性。