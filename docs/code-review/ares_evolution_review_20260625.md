# ares_evolution 系统代码 Review 报告

**日期**: 2026-06-25  
**Review 范围**: 10+1 个文件  
**整体质量评级**: A- (良好)

---

## 文件清单

| # | 文件 | 行数 | 类型 |
|---|------|------|------|
| 1 | `internal/ares_evolution/dream_cycle.go` | ~600+ | 核心逻辑 |
| 2 | `internal/ares_evolution/feedback_recorder.go` | ~120 | 核心逻辑 |
| 3 | `internal/ares_evolution/genome_wiring.go` | ~1500+ | 核心逻辑 |
| 4 | `internal/ares_evolution/genome_wiring_test.go` | ~1150 | 测试 |
| 5 | `internal/ares_evolution/scheduler.go` | ~500+ | 核心逻辑 |
| 6 | `internal/ares_evolution/pg_strategy_store.go` | ~130 | 存储层 |
| 7 | `internal/ares_evolution/regression_tester.go` | ~110 | 核心逻辑 |
| 8 | `internal/dashboard/api.go` | ~400+ | API 层 |
| 9 | `internal/observability/prometheus.go` | ~300 | 可观测性 |
| 10 | `internal/storage/postgres/migrate.go` | ~250 | 数据库迁移 |
| 11 | `internal/storage/postgres/repositories/strategy_repository.go` | ~270 | 数据访问层 |

---

## 架构总览

整个系统是一个自主进化（autonomous evolution）框架，核心流程为：

```
Scheduler (事件触发)
    ↓
GenomePopulationAdapter.Run()
    ↓
genome.Population.EvolveAfterScoring()
    ↓ (评分闭环)
TieredScorer → LLM Scorer / Heuristic Scorer / MemoryAwareScorer
    ↓
记录: Genealogy / AdaptiveDistribution / FeedbackRecorder
    ↓ (可选)
DreamCycle (深度进化) + ShadowEvaluator (灰度评估) + ActiveStrategyManager (部署/回滚)
```

---

## 逐文件 Review

### 1. `dream_cycle.go` — Dream Cycle 编排器

**质量评级: A- (良好)**

**功能**: 当 scheduler 检测到 agent 空闲时，触发一轮"深度进化"——使用更多 mutation 组合、回归测试、对比 baseline，产生高质量 offspring。

**优点**:
- 与 scheduler 双向引用，通过 `SetDreamCycle()` / `SetScheduler()` 解耦
- 冷启动保护：`firstRun` 标志确保至少有一次基线评分
- 完整的 cooldown 机制避免频繁触发
- Shadow evaluator 集成：deploy 前先灰度 shadow evaluation

**问题**:
- `GuardrailEvents` 暴露在结构中但未在 `Run()` 中检查触发后的额外动作

---

### 2. `feedback_recorder.go` — 反馈记录器

**质量评级: A (优秀)**

**功能**: 将策略进化结果记录到 experience feedback 系统。

**优点**:
- 代码简洁，职责单一
- 使用 `FeedbackService` 接口而非具体实现，可测试性高
- 错误处理得当（日志警告而非中断流程）

---

### 3. `genome_wiring.go` — 基因组适配器 & 系统组装

**质量评级: B+ (良好)**

**功能**: 核心文件。包含 `GenomePopulationAdapter`、`NewWiredEvolutionSystem`、`RunIdleEvolution`、`guidanceAdapter`。

**优点**:
- 大量的 `With*` option 模式，配置灵活
- `TieredScorer` → `MemoryAwareScorer` 的 pipeline 设计清晰
- `recordOutcomesLocked()` 处理了交叉繁衍（`×` 分隔的双亲）的评分加权
- `scorerWarningOnce` 使用 `sync.Once` 避免重复日志
- 完整的 guardrails pre/post 检查

**问题**:
- P1: `NewWiredEvolutionSystem` 函数过长（~250行），建议拆分
- P2: 直接访问 `system.DreamCycle.scheduler` 未导出字段
- P7: 注释中混用 `EvolveOnIdle` 和 `EvolveAfterScoring`

---

### 4. `genome_wiring_test.go` — 测试

**质量评级: A (优秀)**

**覆盖场景**:
- `NewGenomePopulationAdapter` 创建
- `Run()` 单轮进化循环
- `GenomeMutatorAdapter` 适配
- `GenealogyRecorder` 血缘记录
- `WiredEvolutionSystem` 全生命周期
- `RunIdleEvolution` 多代进化
- Scheduler 事件触发集成
- 真实 mutator + prompt mutation 的完整集成

**优点**:
- 使用 `waitForGeneration` 轮询替代 `time.Sleep`，避免 flaky
- 模拟了多种真实场景

**不足**:
- 缺少 guardrails 的测试用例
- 缺少 `AdaptiveDistribution` 的测试
- 缺少 `MemoryAwareScorer` 的集成测试

---

### 5. `scheduler.go` — 进化调度器

**质量评级: A- (良好)**

**功能**: 基于 callback 的事件驱动进化调度器。

**优点**:
- 事件驱动架构，支持多种 trigger 模式（`TriggerOnIdle`, `TriggerOnScoreDrop`）
- `minInterval` 防抖保护
- 与 DreamCycle 双向引用 setup 合理

---

### 6. `pg_strategy_store.go` — PG 策略存储

**质量评级: A (优秀)**

**功能**: PG 策略存储 wrapper，实现 `StrategyStore` 接口。

**优点**:
- 简洁，职责清晰
- 正确实现了 `StrategyStore` 接口
- 错误包装得当

---

### 7. `regression_tester.go` — 回归测试器

**质量评级: B+ (良好)**

**功能**: 比较 candidate 和 baseline 策略的回归测试器。

**问题**:
- P5: `rand.Float64()` 多线程不安全，应使用独立 `rand.Rand` 实例
- `evolutionToMutationStrategy` 函数在同一文件中被调用但未在同文件定义（来自 mutation 包）

---

### 8. `dashboard/api.go` — Dashboard API

**质量评级: B (一般)**

**功能**: HTTP API 接口用于查看进化状态。

**建议**:
- 增加结构化错误响应
- 没有重大设计问题

---

### 9. `prometheus.go` — Prometheus 指标

**质量评级: A- (良好)**

**功能**: 定义全部 Prometheus 指标。

**优点**:
- 指标定义完整（LLM calls、tools、errors、evolution 指标等）
- 所有方法的 `nil` 接收者保护（`if m == nil { return }`）
- 清晰的 metrics HTTP handler 注册

**问题**:
- P6: 全局 `prometheus.Register` 在测试多实例场景下通过 `AlreadyRegisteredError` 降级处理，但建议使用独立 registry

---

### 10. `migrate.go` — 数据库迁移

**质量评级: A (优秀)**

**功能**: DDL 定义，包括 `evolution_strategies` 和 `evolution_rollback_events` 表。

**优点**:
- 清晰的 DDL 定义
- 索引覆盖常见查询模式

---

### 11. `strategy_repository.go` — 策略数据访问层

**质量评级: B+ (良好)**

**功能**: 提供 Postgres 策略持久化。

**问题**:
- P3: `SetActive` 中类型断言 `r.db.(*sql.DB)` 决定是否使用事务，脆弱且语义不一致
- P4: `setActiveTx`/`setActiveNoTx` 中 SQL 使用 `NOW()` 替代 `$10`，导致 `createdAt` 参数被忽略

---

## 重点问题汇总

| 优先级 | 文件 | 问题描述 |
|--------|------|----------|
| P1 | `genome_wiring.go` | `NewWiredEvolutionSystem` 函数过长（~250行），可拆分为子函数 |
| P2 | `genome_wiring.go` | 直接访问 `system.DreamCycle.scheduler` 未导出字段，应使用 setter |
| P3 | `strategy_repository.go` | `SetActive` 中类型断言 `*sql.DB` 决定是否使用事务，脆弱 |
| P4 | `strategy_repository.go` | SQL 使用 `NOW()` 替代 `$10`，导致 `createdAt` 参数被忽略 |
| P5 | `regression_tester.go` | `rand.Float64()` 多线程不安全 |
| P6 | `prometheus.go` | 全局 `prometheus.Register` 在测试多实例场景下有约束 |
| P7 | `genome_wiring.go` | 注释中混用 `EvolveOnIdle` 和 `EvolveAfterScoring` |
| P8 | `scheduler.go`/`dream_cycle.go` | guardrails event 回调后缺少额外 action/logic |

---

## 总体评价

**整体质量评级: A- (良好)**

**优点**:
- 清晰的架构分层：进化引擎、评分流水线、策略存储、可观测性、API 分层清晰
- 良好的 Go 实践：option 模式、接口抽象、错误包装、context 传播
- 完整的功能覆盖：基础进化 + DreamCycle + 灰度评估 + 自动部署/回滚 + 经验强化
- 充分的测试覆盖：多场景集成测试，轮询替代 sleep 避免 flaky

**主要改进方向**:
1. `NewWiredEvolutionSystem` 函数体过大，重构拆分为子函数
2. 数据库 repository 层的事务处理可更健壮
3. 多线程安全的随机数使用
4. `createdAt` 参数被 SQL 中 `NOW()` 覆盖的问题
