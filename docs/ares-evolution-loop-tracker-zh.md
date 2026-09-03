# ARES 进化闭环跟踪（Evolution Loop Tracker）

> 整合自：`ares-evolution-loop-closure-plan-zh.md`（E1–E6 计划）、`ares-evolution-loop-tasklist-zh.md`（C1–C6 任务清单）、
> `REVIE-2026-09-02.md`、`REVIEW-2026-09-02-deep.md`（Review 审查报告）
> 整合时间：2026-09-03
> 分支：`dev`　`VERSION 0.3.1`

---

## 0. 唯一目标

```
chaos / quantum 结果
  → 零 token 打分（Attribution → Strategy.Score）
  → Genome GA（选择/交叉/变异，DreamCycle 关闭）
  → 门禁（G1 必过，G2 影子回放）
  → 补丁落在 live MutableDAG
  → 投影编译成 taskfabric.PlanStep
  → Scheduler 下一轮按新图跑
  → 结果写回分数
```

**通过条件**：杀 Agent 或换任务分布后，**不调用 LLM**，拓扑或调度参数发生变化，且 `introspect` 能指出「哪一代、哪道门、哪次 CompilePlan」。

**冻结**：AKG、DreamCycle、HITL、子图执行器。`workflow/engine` 只当可变异的规划基因，执行只走 Kernel。

---

## 1. 状态总览

| 组件 | 状态 | 备注 |
|------|------|------|
| C1 单一投影 | ✅ 完成 | `planprojection/projection.go` |
| C2 零 token 打分 | ✅ 完成 | Attribution → Strategy.Score |
| C3 门禁真实化 | ✅ 完成 | G1 真必过 + G2 影子回放 |
| C4 补丁落 live DAG | ✅ 完成 | 结构补丁 + 重编译 |
| C5 introspect 归因 | ✅ 完成 | 三元组可查 |
| C6 端到端验收 | ✅ 完成 | kill agent / 换任务分布 |
| E1 strategy_id 归因 | ✅ 完成 | checkpoint v2 + 任务级打标 |
| E2 G2 条件注册 | ✅ 完成 | ReplayScorer 作为零 token 独立 scorer |
| E3 决策证据消费方 | ✅ 完成 | 知识图谱可检索 promote/rollback |
| E4 文档修正 | ⏳ 未做 | 设计文档门链表述需更新 |
| E5 per-task A/B | ⏳ 0.4.x | 流量分配器 + 异步 G2 |
| E6 观测面 | ✅ 完成 | 指标 + 快照字段 |
| W1 Review 修复 | ✅ 完成 | P0-1 集成测试、P0-2/B-3 平局死锁、B-1 Attempts 时序、P1-3 注释 |
| W2 配置化 | ✅ 完成 | replayWindowSpan / replayQueryLimit 可配置（本次新增） |
| W2 审计 → CHANGELOG | ⏳ 未做 | G3/G4 零 token 行为需记录 |
| W3–W4 双系统收敛 | ⏳ 未做 | 共享验证流水线 `evolution/verify/` |
| W5+ cmd 下沉 | ⏳ 未做 | 三循环下沉 `internal/system_runtime` |

---

## 2. 已完成工作

### 2.1 C1–C6 任务清单（`ares-evolution-loop-tasklist-zh.md` 原记录）

全部 32 项验收完成，覆盖：

| 批次 | 内容 | 关键位置 |
|------|------|----------|
| C1 | 单一投影函数 | `internal/planprojection/projection.go` |
| C2 | 零 token 打分（Attribution → Strategy.Score） | `kernelscheduler/scheduler.go` C2.1、`aresrecovery/deterministic_scorer.go` C2.2 |
| C3 | 门禁真实化（G1 必过 / G2 影子回放） | `shadow_sampler.go`、`replay_scorer.go`、`bootstrap_steps.go` |
| C4 | 补丁落 live DAG 并驱动下一轮 | `evolution/patch`、`MutableDAG` |
| C5 | introspect 可归因 | `cmd/ares/evolution.go`、`introspect/control.go` |
| C6 | 端到端验收脚本 | kill agent / 换任务分布场景 |

### 2.2 E1–E6 闭环计划（`ares-evolution-loop-closure-plan-zh.md` 原记录）

| 项 | 级别 | 状态 | 说明 |
|----|------|------|------|
| E1 | P0 | ✅ 完成 | `strategy_id` 任务级归因标记（checkpoint v2，所有提交入口打标） |
| E2 | P0 | ✅ 完成 | G2 从 fail-closed 改为 ReplayScorer 作为独立 scorer（实现路径与 plan 的"诚实缺席"不同，但更优） |
| E3 | P1 | ✅ 完成 | P2-3 决策证据接上知识图谱消费方 |
| E4 | P1 | ⏳ 未做 | G1 定性修正 + G4 不对称登记（纯文档，`ga-runtime-evolution-design-zh.md` §4④ 需更新） |
| E5 | P1 | ⏳ 0.4.x | per-task 真 A/B 采样（G2 从 Submit 内联移到 watch loop） |
| E6 | P2 | ✅ 完成 | 观测面：新 metrics + lifecycle 快照补字段 |

### 2.3 Review 修复（2026-09-02 审查，W1 优先级）

| 编号 | 内容 | 文件 | 改动 |
|------|------|------|------|
| P0-1 | 端到端零-LLM 晋升集成测试 | `zero_llm_closure_test.go` | 新增，正反例 |
| P0-2/B-3 | 冷启动平局死锁 | `replay_scorer.go`、`shadow_evaluator.go` | 长期均值回退 + 平局不计入 TotalComparisons |
| B-1 | kernelscheduler Attempts 时序 | `scheduler.go` | 改为 RunQuantum 后读取 |
| P1-3 | lifecycle.go 注释歧义 | `lifecycle.go` | 文件头注释修正 |
| P0-2 | ConstantScorer 兜底显式化 | `genome_wiring_system.go` | 增加 WarnContext 告警 |

### 2.4 W2 配置化（本次新增）

| 字段 | 原值 | 现配置入口 | 位置 |
|------|------|-----------|------|
| `replayWindowSpan` | 硬编码 10min | `evolution.shadow.replay_window_span` | `ares_config.EvolutionShadowConfig` |
| `replayQueryLimit` | 硬编码 200 | `evolution.shadow.replay_query_limit` | `ares_config.EvolutionShadowConfig` |

接线路径：YAML → `ares_config.EvolutionShadowConfig` → `bootstrap_steps.go` → `evolution.ShadowEvaluationConfig` → `NewShadowSampler(WithReplayWindowSpan)` / `NewReplayScorer(WithReplayQueryLimit)`。

---

## 3. 未完成跟踪

### 3.1 W2 审计 → CHANGELOG

- **内容**：G3（eval suite）和 G4（deployment staging）在零 token 默认配置下的真实行为写进 CHANGELOG
- **判据**：每级门控在默认配置下的行为明确记录
- **状态**：⏳ 未做

### 3.2 W3–W4 双进化系统收敛

- **内容**：先抽共享验证流水线（`evolution/verify/`：G1–G4 门控语义 + evidence 读写 + promotion），再统一命名空间
- **判据**：两套系统（v1 `ares_evolution` 56.7k 行 / v2 `evolution` 9k 行）复用同一门控语义
- **状态**：⏳ 未做

### 3.3 W5+ cmd 循环下沉

- **内容**：三个内核循环（recovery sweep、quota apply、evolution apply）从 `cmd/ares/` 下沉到 `internal/system_runtime`
- **判据**：SDK 可复用编排逻辑
- **状态**：⏳ 未做

### 3.4 E5 per-task A/B 执行路径（0.4.x）

- **内容**：`ABStrategySource` 可选接口 + 流量分配器 + G2 异步移到 watch loop
- **判据**：候选运行真实任务后产生自身证据
- **状态**：⏳ 0.4.x

### 3.5 E4 文档修正

- **内容**：`ga-runtime-evolution-design-zh.md` §4④ 门链定义改为三层表述（G1 明确为 cycle precondition），G4 不对称登记
- **判据**：设计文档与实现一致
- **状态**：⏳ 待做

---

## 4. 代码核对结论

已成立、不要重做：

| 事项 | 位置 | 现状 |
|------|------|------|
| `PlanStep` 类型 | `internal/taskfabric/workflow_plan.go:15` | 已有完整字段 |
| 环 + 悬空依赖检测 | `workflow_plan.go:70-80` + `detectPlanCycle` | 已实现且有测试 |
| DAG 版本号 | `engine.MutableDAG.Version()` | 已是 mutation counter |
| DAG 变更事件 | `SubscribeWithID()` | 已有 GraphEvent 订阅 |
| `EnableDreamCycle=false` | `bootstrap_steps.go:208` | 已关闭 |
| LLM 打分默认关 | `wireLLMScorer` | 默认 false |
| kernelscheduler 架构红线 | `architecture_test.go` | 禁 `ares_runtime` |
| lifecycle 快照端点 | `introspect/control.go:196` | 已实现 |
| `evolution status` 子命令 | `cmd/ares/evolution.go:35` | 已存在 |

---

## 5. 验收基线

```bash
gofmt -l .                                   # 无输出
go build ./... && go vet ./...
golangci-lint run --timeout=5m               # 0 issues
go test -count=1 ./...
go test -race -count=1 ./...
make check                                    # lint + test
make gate                                     # G1/G2/G3 + G4 closure
```

---

## 6. 相关文档索引

| 文档 | 说明 | 状态 |
|------|------|------|
| `docs/zh/architecture/evolution-enhancement-plan-v2.md` | 进化增强计划 v2 | 已归档 |
| `docs/zh/architecture/ares-runtime.md` | 运行时设计 | 活跃 |
| `docs/zh/architecture/ares-architecture.md` | 整体架构 | 活跃 |
| `CHANGELOG.md` | 版本变更记录 | 活跃 |

---

## 7. 变更记录

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-09-03 | 整合 | 四份旧文档合并为本跟踪文档，原文件已删除 |
| 2026-09-03 | W2 完成 | `replayWindowSpan` / `replayQueryLimit` 配置化落地 |