# Promoter 冠军/挑战者晋升系统——五状态状态机详解

> 本文深入解析 GoAgent 进化系统中晋升系统（Promotion System）的实现细节，涵盖五状态状态机、12 参数晋升标准、证据评分公式以及滚动改进检测。所有代码片段均来自真实源码。

## 1. 系统概述

Promoter 是进化系统中管理"候选策略 → 冠军策略"晋升流程的核心组件。它的设计灵感来自 **Champion/Challenger** 模式：系统中同时存在一个已知优秀的"冠军"（Champion）和多个试图超越它的"挑战者"（Challenger），通过严格的评估流程决定策略的晋升与降级。

```
┌─────────────────────────────────────────────────────┐
│                  Promoter 系统架构                      │
│                                                       │
│   ┌──────────┐    ┌──────────┐    ┌──────────┐      │
│   │          │    │          │    │          │      │
│   │  候选策略 ├───→│ 影子策略  ├───→│ 冠军策略  │      │
│   │  (25选5) │    │  (竞争)   │    │  (执行)   │      │
│   │          │    │          │    │          │      │
│   └──────────┘    └──────────┘    └──────────┘      │
│        │                             │               │
│        ▼                             ▼               │
│   ┌──────────┐                  ┌──────────┐        │
│   │  已降级   │                  │  已退役   │        │
│   │ (冷却中)  │                  │ (保留记录) │        │
│   └──────────┘                  └──────────┘        │
└─────────────────────────────────────────────────────┘
```

源码路径：`internal/ares_evolution/promotion/`

## 2. 五状态状态机

### 2.1 状态定义

```go
// File: internal/ares_evolution/promotion/types.go
type StrategyState int

const (
    // 候选状态：策略刚创建，需要收集样本
    StrategyCandidate StrategyState = iota  // 0
    // 影子状态：正在与冠军竞争的中间状态
    StrategyShadow                           // 1
    // 冠军状态：当前最优策略
    StrategyChampion                         // 2
    // 降级状态：曾经是冠军但被超越
    StrategyDemoted                          // 3
    // 退役状态：永久退出竞争
    StrategyRetired                          // 4
)
```

五个状态的完整生命周期：

```
StrategyCandidate (0)
    │
    │ 收集 ≥10 样本 + 成功率 ≥50%
    ▼
StrategyShadow (1)
    │
    │ MeetsPromotionCriteria() + MinAbsoluteImprovement ≥ 0.5
    ▼
StrategyChampion (2)
    │
    ├── 滚动改进持续为正 → 保持冠军
    ├── 滚动改进为负 + 超过 ChampionHoldPeriod → StrategyDemoted (3)
    └── MaxChampionTenure = 20 代 → 强制降级到 StrategyShadow (1)
                                        │
                                        ▼
                                  StrategyDemoted (3)
                                        │
                                        │ 冷却 ChampionHoldPeriod×2 代
                                        ▼
                                  StrategyRetired (4)
```

### 2.2 状态转换验证：显式的有向图

```go
// File: internal/ares_evolution/promotion/types.go
// CanPromoteTo 和 CanDemoteTo 使用显式的转换映射表
// 这种设计比隐式的 if-else 链更安全、更可维护

// 晋升转换映射（key = 当前状态）
var promotionTransitions = map[StrategyState][]StrategyState{
    StrategyCandidate: {StrategyShadow},
    StrategyShadow:    {StrategyChampion},
}

// 降级转换映射（key = 当前状态）
var demotionTransitions = map[StrategyState][]StrategyState{
    StrategyChampion: {StrategyDemoted, StrategyShadow},
    StrategyDemoted:  {StrategyRetired},
}
```

设计要点：
- 转换映射是**有向无环图**（DAG），不存在循环依赖
- 每个状态的转换目标是有限的（最多 2 个）
- `StrategyShadow → StrategyChampion` 是系统中最关键的转换

## 3. PromotionCriteria：12 参数晋升标准

### 3.1 数据结构

```go
type PromotionCriteria struct {
    MinSampleCount         int     // 最小样本数：100
    MinSuccessRate         float64 // 最小成功率：0.85（85%）
    MaxErrorRate           float64 // 最大错误率：0.15（15%）
    MaxLatencyP95          int64   // 最大 P95 延迟：5000ms
    MinConfidence          float64 // 最小置信度：0.7

    ChampionHoldPeriod     int     // 冠军保持期：5 代
    DemotionThreshold      float64 // 降级阈值：0.3
    CoolDownGenerations    int     // 冷却代数：3 代
    MinAbsoluteImprovement float64 // 最小绝对改进：0.5
    MinRollingImprovement  float64 // 最小滚动改进：0.1
    ImprovementWindow      int     // 改进窗口：3 代
    MaxChampionTenure      int     // 最大冠军任期：20 代
}
```

### 3.2 默认值

```go
func DefaultPromotionCriteria() PromotionCriteria {
    return PromotionCriteria{
        MinSampleCount:         100,
        MinSuccessRate:         0.85,
        MaxErrorRate:           0.15,
        MaxLatencyP95:          5000,
        MinConfidence:          0.7,
        ChampionHoldPeriod:     5,
        DemotionThreshold:      0.3,
        CoolDownGenerations:    3,
        MinAbsoluteImprovement: 0.5,
        MinRollingImprovement:  0.1,
        ImprovementWindow:      3,
        MaxChampionTenure:      20,
    }
}
```

### 3.3 晋升条件检查

```go
// MeetsPromotionCriteria 检查策略是否满足晋升条件
// 同时检查 5 个阈值，全部通过才允许晋升
func MeetsPromotionCriteria(info StrategyInfo, criteria PromotionCriteria) bool {
    // 1. 有足够的样本
    if info.SampleCount < criteria.MinSampleCount {
        return false
    }
    // 2. 成功率 ≥ 85%
    if info.SuccessRate < criteria.MinSuccessRate {
        return false
    }
    // 3. 错误率 ≤ 15%
    if info.ErrorRate > criteria.MaxErrorRate {
        return false
    }
    // 4. P95 延迟 ≤ 5000ms
    if info.LatencyP95 > criteria.MaxLatencyP95 {
        return false
    }
    // 5. 置信度 ≥ 0.7
    if info.Confidence < criteria.MinConfidence {
        return false
    }
    return true
}
```

## 4. EvidenceScore：加权证据评分

### 4.1 评分公式

```go
// CalculateEvidenceScore 计算策略的证据加权评分
// 权重分配：
//   - 成功率: 40%
//   - 低错误率: 30%
//   - 置信度: 20%
//   - 低延迟: 10%
func CalculateEvidenceScore(info StrategyInfo) float64 {
    normalizedLatency := 1.0
    if info.LatencyP95 > 0 {
        normalizedLatency = 1.0 - float64(info.LatencyP95)/10000.0
        if normalizedLatency < 0 {
            normalizedLatency = 0
        }
    }
    return info.SuccessRate*0.4 +
        (1-info.ErrorRate)*0.3 +
        info.Confidence*0.2 +
        normalizedLatency*0.1
}
```

**权重设计背后的考量**：
- **成功率（0.4）**：权重最高，因为策略的核心价值就是完成任务的成功率
- **低错误率（0.3）**：次高权重，高成功率但伴随高错误率不可取
- **置信度（0.2）**：中等权重，样本量越大置信度越高
- **低延迟（0.1）**：最低权重，延迟虽然是重要指标，但可牺牲一些延迟换取更高的成功率

## 5. Promoter 核心评估流程

### 5.1 DefaultPromoter 结构

```go
type DefaultPromoter struct {
    mu sync.RWMutex  // 保护所有内部状态

    criteria    PromotionCriteria  // 晋升标准
    strategies  map[string]*StrategyInfo  // strategyID → 策略信息
    champions   []string  // 当前冠军列表
    candidatePool []string  // 候选池
    shadowPool    []string  // 影子池
    generation    int  // 当前代数
}
```

### 5.2 Evaluate() 主调度

```go
func (p *DefaultPromoter) Evaluate(ctx context.Context, name string, info StrategyInfo) (Action, error) {
    p.mu.Lock()
    defer p.mu.Unlock()

    // 根据当前状态分发到不同的评估函数
    // 每个状态有不同的晋升/降级逻辑
    switch info.CurrentState {
    case StrategyCandidate:
        return p.evaluateCandidate(name, info)
    case StrategyShadow:
        return p.evaluateShadow(name, info)
    case StrategyChampion:
        return p.evaluateChampion(name, info)
    case StrategyDemoted:
        return p.evaluateDemoted(name, info)
    case StrategyRetired:
        // 退役状态不再处理
        return ActionNoop, nil
    default:
        return ActionNoop, nil
    }
}
```

### 5.3 Candidate → Shadow 晋级

```go
// 候选 → 影子：达到最低样本要求即可
// 门槛较低，目的是让足够多的候选策略进入竞争池
func (p *DefaultPromoter) evaluateCandidate(name string, info StrategyInfo) (Action, error) {
    if info.SampleCount >= 10 && info.SuccessRate >= 0.5 {
        // 满足条件，晋升到影子状态
        action := Action{
            Name:       name,
            FromState:  StrategyCandidate,
            ToState:    StrategyShadow,
            ActionType: ActionPromote,
            Reason:     "candidate has sufficient samples and success rate",
        }
        return action, nil
    }
    return ActionNoop, nil
}
```

Candidate → Shadow 的门槛（10 样本 + 50% 成功率）故意设置得较低，目的是：
- 让更多策略进入影子池参与竞争
- 避免过早淘汰有潜力的策略
- 收集更多数据后再做更严格的选择

### 5.4 Shadow → Champion 晋升

```go
// 影子 → 冠军：最严格的晋升路径
// 必须同时满足：
//   1. MeetsPromotionCriteria（5 项阈值检查）
//   2. MinAbsoluteImprovement ≥ 0.5（相对于当前冠军）
//   3. 已做滚动改进检查
func (p *DefaultPromoter) evaluateShadow(name string, info StrategyInfo) (Action, error) {
    if !MeetsPromotionCriteria(info, p.criteria) {
        return ActionNoop, nil
    }
    // 检查绝对改进度
    improvement := info.EvidenceScore - info.BaselineScore
    if improvement < p.criteria.MinAbsoluteImprovement {
        return ActionNoop, nil
    }
    // 晋升为冠军
    action := Action{
        Name:       name,
        FromState:  StrategyShadow,
        ToState:    StrategyChampion,
        ActionType: ActionPromote,
        Reason:     fmt.Sprintf("promotion criteria met, improvement=%.2f", improvement),
    }
    return action, nil
}
```

### 5.5 Champion 评估：三个出口

冠军策略有三种可能的命运：

```go
func (p *DefaultPromoter) evaluateChampion(name string, info StrategyInfo) (Action, error) {
    // 评估 1：检查任期是否超过 MaxChampionTenure
    // 如果是，强制降级到影子（给其他策略机会）
    if info.GenerationCount > p.criteria.MaxChampionTenure {
        return Action{
            Name:       name,
            FromState:  StrategyChampion,
            ToState:    StrategyShadow,
            ActionType: ActionDemote,
            Reason:     fmt.Sprintf("max tenure (%d) exceeded", p.criteria.MaxChampionTenure),
        }, nil
    }

    // 评估 2：检查保持期是否足够
    if info.GenerationCount < p.criteria.ChampionHoldPeriod {
        return ActionNoop, nil
    }

    // 评估 3：检查滚动改进
    rollingImprovement := p.calculateRollingImprovement(info)
    if rollingImprovement < p.criteria.DemotionThreshold {
        return Action{
            Name:       name,
            FromState:  StrategyChampion,
            ToState:    StrategyDemoted,
            ActionType: ActionDemote,
            Reason: fmt.Sprintf("rolling improvement (%.2f) below threshold (%.2f)",
                rollingImprovement, p.criteria.DemotionThreshold),
        }, nil
    }

    return ActionNoop, nil
}
```

**三个出口的设计逻辑**：
1. **任期出口**（MaxChampionTenure=20）：防止"安全但停滞"的策略永久占据冠军位置。20 代后即使表现还可以，也要降级给新策略机会
2. **保持期锁定**（ChampionHoldPeriod=5）：新冠军有 5 代保护期，不会被立即降级，给足够时间证明自己
3. **改进不足出口**（DemotionThreshold=0.3）：保持期结束后，如果滚动改进低于 0.3，降级

### 5.6 Demoted → Retired 冷却期

```go
func (p *DefaultPromoter) evaluateDemoted(name string, info StrategyInfo) (Action, error) {
    // 冷却期为 ChampionHoldPeriod × 2 = 10 代
    if info.GenerationCount > p.criteria.ChampionHoldPeriod*2 {
        return Action{
            Name:       name,
            FromState:  StrategyDemoted,
            ToState:    StrategyRetired,
            ActionType: ActionDemote,
            Reason:     "demoted period expired, transitioning to retired",
        }, nil
    }
    return ActionNoop, nil
}
```

## 6. 滚动改进检测

Promoter 使用滑动窗口计算改进趋势，这是判断冠军是否应该退位的关键指标。

```go
// calculateRollingImprovement 计算滑动窗口内的平均评分增量
// ImprovementWindow = 3（默认）
// 取最近 3 代评分的变化量平均值
func (p *DefaultPromoter) calculateRollingImprovement(info StrategyInfo) float64 {
    history := info.ScoreHistory
    if len(history) < 2 {
        return 1.0 // 没有足够数据时假设为正向改进
    }

    window := p.criteria.ImprovementWindow
    if window <= 0 {
        window = 3
    }

    start := len(history) - window
    if start < 0 {
        start = 0
    }

    var totalDelta float64
    count := 0
    for i := start; i < len(history)-1; i++ {
        delta := history[i+1] - history[i]
        totalDelta += delta
        count++
    }

    if count == 0 {
        return 1.0
    }
    return totalDelta / float64(count)
}
```

**滚动改进 vs 绝对改进**的区别：
- **绝对改进**（AbsoluteImprovement）：当前评分与基线评分之差，用于 Shadow→Champion 晋升
- **滚动改进**（RollingImprovement）：最近 N 代评分变化量的滑动平均，用于 Champion 状态保持判断

## 7. 冷却期与转换保护

```go
// canTransition 检查策略是否可以转换状态
// 新策略（GenerationCount=0）可以立即转换
// 已有历史的策略必须满足冷却代数
func (p *DefaultPromoter) canTransition(info StrategyInfo) bool {
    if info.GenerationCount == 0 {
        return true // 新策略立即生效
    }
    return info.GenerationCount >= p.criteria.CoolDownGenerations
}
```

**CoolDownGenerations=3**：一个状态转换后，至少在当前状态停留 3 代，防止频繁震荡。

## 8. 关键参数调整指南

| 参数 | 默认值 | 调大 | 调小 |
|------|--------|------|------|
| MinSampleCount | 100 | 更保守，减少误晋升 | 更快晋升，但噪音大 |
| MinSuccessRate | 0.85 | 质量更高，但晋升少 | 更多策略能晋升 |
| MaxErrorRate | 0.15 | 容忍更多错误 | 要求更精确 |
| MaxLatencyP95 | 5000ms | 容忍更慢的策略 | 要求更快响应 |
| ChampionHoldPeriod | 5 | 冠军更稳定 | 冠军更替更频繁 |
| DemotionThreshold | 0.3 | 更容易降级冠军 | 冠军更稳定 |
| MinAbsoluteImprovement | 0.5 | 要求更大改进 | 允许小幅晋升 |
| MaxChampionTenure | 20 | 冠军可以长期执政 | 冠军更替更频繁 |

## 9. 总结

Promoter 的五状态状态机是进化系统的**质量控制层**，它确保只有经过充分验证的策略才能成为冠军：

1. **Candidate→Shadow** 的低门槛（10 样本/50% 成功率）保证了池子的流动性
2. **Shadow→Champion** 的严格标准（5 项阈值 + 0.5 绝对改进）保证了冠军质量
3. **Champion** 的滚动改进检测防止了策略停滞（DemotionThreshold=0.3）
4. **MaxChampionTenure=20** 是安全阀，防止冠军永久锁定
5. **冷却期（CoolDownGenerations=3）** 防止状态震荡

这套系统保证了进化过程中"**适者生存，但不让胜者永居**"——既给优秀策略足够的展示时间（保持期 5 代），又防止它们因历史优势而阻碍创新（最大任期 20 代）。