# Production Readiness Gap Analysis

> 分析日期：2026-06-25
> 基准代码：`dev` branch, commit 0820529
> 分析范围：`internal/ares_evolution` Phase 5 生产安全模块

---

## 总体状态

当前代码处于 **Phase 4 → Phase 5 过渡**。Phase 5 新增/修改 ~1000 行，但关键路径仍有断点。

| Phase | 描述 | 完成度 |
|---|---|---|
| 1 | Feedback-Driven Trait Refinement | 100% |
| 2 | Soft Deletion / Decay | 100% |
| 3 | Genetic Refactoring | 100% |
| 4 | Experience-Guided Genetic Search | 100% |
| 5a | FeedbackRecorder | 100% |
| 5b | RollbackPolicy + ActiveStrategyManager | 100% |
| 5c | ShadowEvaluator | 100% |
| 5d | EvolutionGuardrails | ~60% |
| 5e | Selection Diversity Pressure | ~50% |
| 5f | Wiring (genome_wiring.go) | 100% |
| **Phase 5 整体** | | **~85%** |

## 安全架构概览

```
部署前                         部署中                        部署后
┌──────────────┐      ┌────────────────────┐      ┌───────────────────┐
│ ShadowEval   │ ──→  │ ActiveStrategyMgr  │ ──→  │ RollbackPolicy    │
│ (影子评估)    │      │ (策略部署/晋级)     │      │ (异常回滚)         │
└──────────────┘      └────────────────────┘      └───────────────────┘
                              │                           │
                              ▼                           ▼
                     ┌────────────────────────────────────────┐
                     │   EvolutionGuardrails (全局哨兵)        │
                     │   - 停滞检测 / 基线回归 / 多样性崩塌    │
                     │   - lineage 过度集中 / 分数衰减         │
                     └────────────────────────────────────────┘
```

架构分层设计合理，但 guardrail 层是**纯探测器，没有执行器**。

---

## 缺口 1：Guardrail 强制执行（P0）

### 现状

`EvolutionGuardrails` 能正确检测 6 种异常条件并设置 `GuardrailResult.ShouldStop`，但没有任何消费者读这个字段：

- `EvolutionScheduler.RunOnce()` 不看 guardrail 结果
- `DreamCycle` 不看 guardrail 结果
- `ActiveStrategyManager.PromoteStrategy()` 不看 guardrail 结果

### 需要做的事

```go
// 注入点：
// 1. scheduler.go — RunOnce 中每次 evaluate 后调用 guardrails, ShouldStop=true 时中止循环
// 2. dream_cycle.go — deploy 前调用 guardrails, ShouldStop=true 时跳过 deploy
// 3. rollback_policy.go — guardrail 触发 critical 时自动触发回滚（而非仅记录）
```

### 涉及文件

- `genome_wiring.go` — 注入 guardrail 实例到 scheduler/dream-cycle
- `evolution_scheduler.go` — 消费 ShouldStop
- `dream_cycle.go` — 消费 ShouldStop

### 工作量估算

~150-250 行，纯逻辑注入，不涉及新类型。

---

## 缺口 2：集成测试（P1）

### 现状

各组件有独立 unit test，但 `WiredSystem()`（~500 行装配函数）没有任何测试。Phase 5 新增 ~1000 行代码，0 行集成测试覆盖。

### 需要做的事

```go
// 测试用例：
// 1. WiredSystem creates all components correctly
// 2. ShadowEvaluator blocks low-win-rate candidates from promotion
// 3. ActiveStrategyManager rolls back degraded strategies
// 4. Guardrails detect stagnation and set ShouldStop=true
// 5. FeedbackRecorder correctly routes success/failure to ExperienceService
```

### 涉及文件

- `genome_wiring_test.go`（新文件）
- 各组件 mock：`mockStrategyStore`, `mockExperienceRepo`, `mockArenaTester`

### 工作量估算

~300-500 行。

---

## 缺口 3：端到端集成 — DreamCycle 断链（P1）

### 现状

`WiredSystem()` 创建 DreamCycle 时传了 3 个 nil：

```go
dreamCycle, err := NewDreamCycle(
    nil,   // Scheduler（可选，单独 attach）
    mutationAdapter,
    nil,   // Tester — 核心缺失！没有它无法 evaluate mutation 结果
    genealogy,
    ...
)
```

`ShadowEvaluator` 和 `ActiveStrategyManager` 虽然被 wired，但没有注入到 DreamCycle 中作为 deploy gate。

### 需要做的事

1. Tester 接口定义或复用 arena 的 evaluation 接口
2. DreamCycle 在 deploy 前调用 ShadowEvaluator 验证
3. DreamCycle 在 deploy 时走 ActiveStrategyManager.PromoteStrategy()

### 涉及文件

- `dream_cycle.go` — 加 tester 接口 + deploy gate
- `genome_wiring.go` — 注入 ASM 和 ShadowEvaluator 到 dream cycle
- 可能 `api/arena/` — 暴露 evaluation 接口供 DreamCycle 使用

### 工作量估算

~400-700 行。

---

## 缺口 4：StrategyStore 持久化（P1）

### 现状

`StrategyStore` 接口定义在 `interfaces.go`：

```go
type StrategyStore interface {
    GetActive(ctx context.Context) (*Strategy, error)
    SetActive(ctx context.Context, strategy *Strategy) error
    GetHistory(ctx context.Context, id string, n int) ([]*Strategy, error)
}
```

但没有实现。重启后丢失：
- Active strategy
- Rollback state（rollback window 清空）
- Guardrail events（MaxEvents=1000 但全在内存）

### 需要做的事

1. Postgres/Redis 实现 `StrategyStore` 接口
2. 数据库 migration（strategies 表 + rollback_events 表）
3. Guardrail events 可选持久化（可容忍丢失但不应该）

### 涉及文件

- `internal/storage/postgres/models/` — Strategy model
- `internal/storage/postgres/repositories/` — StrategyRepository
- `internal/storage/postgres/migrations/` — migration
- 接口适配层

### 工作量估算

~300-500 行 + migration。

---

## 缺口 5：混沌防御（P2）

### 现状

没有任何防御性机制：

- Mutation 没有 context timeout — 若 LLM 驱动的变异卡住，整个循环 hang
- FeedbackRecorder 没有熔断 — repo 连续报错时仍会持续调用
- Mutation 输出没有大小限制 — 极端 LLM 输出可能生成爆炸性策略
- ShadowEvaluator 不是真"影子" — 它和 active strategy 共享同一数据源，不是独立流量镜像

### 需要做的事

| 防御 | 实现方式 | 行数 |
|---|---|---|
| Mutation timeout | 给 `Mutator.Mutate()` 加 `context.Context` 参数 | ~50 |
| FeedbackRecorder 熔断 | N 次连续错误后跳过，N 秒后恢复 | ~100 |
| Mutation 大小上限 | `maxStrategySize` 检查，超限拒绝 | ~50 |
| Shadow 独立评估 | 从 arena 获取独立 evaluation，而非共享 outcome | ~200 |

### 工作量估算

~400-600 行。

---

## 缺口 6：可观测性（P2）

### 现状

当前只有零散的 `slog.Info` 调用。没有任何 metrics 或 audit trail。

### 需要做的事

```
必须（最简方案，~150-200 行）：

指标                              类型         标签
─────────────────────────────────────────────────────────
evolution.strategy.deploy         counter      status={success,rollback}
evolution.strategy.active_switch  counter      原因
evolution.guardrail.trigger       counter      code={6种错误码}
evolution.shadow.promotion        counter      result={promoted,rejected}

日志（关键事件必须有完整上下文）：

事件                    必须包含的字段
─────────────────────────────────────────
策略部署                strategy_id, score, previous_score
策略回滚                strategy_id, reason, previous_window_avg
Guardrail 触发          code, score, threshold, generation
影子晋级/拒绝           candidate_id, active_id, win_rate, threshold
演化循环启动/停止        generation, population_size, trigger

可选（~100-150 行）：
- Health check endpoint
- /metrics 端点暴露 Prometheus 格式
```

### 工作量估算

~300-500 行。

---

## 优先级排序

```
当前 （Phase 5 补完）
  │
  ├── P0  Guardrail 强制执行        [150-250 行]  2-3h
  │
  ├── P1  集成测试                  [300-500 行]  4-6h
  ├── P1  端到端集成（填 nil）       [400-700 行]  6-8h
  ├── P1  StrategyStore 持久化      [300-500 行]  4-6h
  │
  ├── P2  混沌防御                  [400-600 行]  4-6h
  └── P2  可观测性                  [300-500 行]  4-6h
────────────────────────────────────────────
总计                               ~2000-3500 行  24-35h
```

### 最小可跑方案（~12-17h）

只做 P0 + 集成测试 + 端到端集成：
1. Guardrail 强制执行 — 让探测真正有保护作用
2. 集成测试 — 确保装配不散架
3. 端到端填 nil — 让 DreamCycle 核心循环跑通

这三项做完后 system 能从"架构正确但跑不通"变成"可跑可测"。

持久化和可观测性可以等需要时再加。混沌防御建议在部署到生产环境前补齐。
