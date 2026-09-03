# Agent OS 端到端闭环深化计划（实现纲领）

> 版本：draft v1 · 2026-09-03 · 配套代码分支 `dev`（HEAD `c5f36d45`）
> 目标：回答一个问题——**"进化从能跑，变有意义"**。由三个互相咬合的验收主张驱动：
>
> 1. **每条业务链路端到端**：从 \`任务落 → 内核执行 → evidence 写入 → 进化归因 → 门控判定 → 回流」整条链，每一跳都有测试与可观测点。
> 2. **候选有自身证据**：一个候选策略被判定，凭的是**它自己的执行结果**，而非回退到在线策略历史 / 全局先验 / 常量 50。
> 3. **进化有意义**：G2/G3 的判定能真实读出一个候选"更好/更差"，且这个信号最终会改变线上行为（promote / 回滚 / 部署）。

本计划**严格基于当前代码现状**（文件:行号均为实测），只补洞、不重写架构、不引入新依赖。每步含**验收命题**，可独立落地。

***

## 0. 现状盘点（代码核实）

### 0.1 两条进化系统（当前事实）

| <br />       | v1 `internal/ares_evolution`                                    | v2 `internal/evolution`                                                      |
| ------------ | --------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| 进化对象         | 策略 `mutation.Strategy`（temperature/top\_k/prompt…）              | 工作流 DAG / knowledge / memory（`RuntimePatch`）                                 |
| 门控链          | G1 guardrail → G2 shadow → G3 eval → G4 staging（`lifecycle.go`） | CandidateVerifier 三选网（G1 结构/G2 evidence/G3 regression）+ Coordinator+Deployer |
| 证据来源         | `evidence.Store` 的 `KindFitness`（记策略自身历史）                       | Coordinator 评估回调（当前可能喂占位分）                                                   |
| 现有"证据"是否候选特异 | **否**（见 0.2）                                                    | **部分**（候选已被征用，但回流安全链路待验）                                                     |

两套都在线上跑、都不能删 → 收敛是 0.4.x 的单独工作；**本计划不合并它们，先各自把端到端跑实**。

### 0.2 v1 最大的洞：G2 判定不是候选特异（已核实）

`internal/ares_evolution/shadow_sampler.go:45-50` 自述：

> "A never-executed candidate has no records and falls back to the cold-start prior… It's verdict is then 'current fleet quality vs the active strategy's measured history' — a real signal, but **not a candidate-specific one**. Per-task A/B execution (running the candidate on live traffic) is what would make it candidate-specific; **that path does not exist yet**."

同文件 `:164-167` `TODO(tech-debt)` 登记同一结论。且 `shadow_evaluator.go:438 Evaluate` 只用 `shadowScorer` 做**纯函数计算**（`replay_scorer.Score` 读库、`LLM scorer` 调 API）——**从未把候选真的丢给 executor 跑一遍**。

**结论**：当前 G2 语义 = "线上策略是否拖累质量"，而非"候选是否更好"。这是 0.x 回滚仍不可用的根因（`CHANGELOG [0.3.1] Known limitations` 也点名）。

### 0.3 v2 最大的洞：回流链未端到端验证（已核实）

`plan/evolution-closure-plan.md`（我司内旧计划）已核对三处断点：

- ② `genome_wiring_system.go` `generateDiffPatches` 只 `Snapshot()` 不 `Mutate()` → diff 恒空

- ① `bootstrap.go:152-162` 喂手搓假 DAG / 假 KnowledgeRuntime

- ③ 新 patch 打到假 DAG，agent 只读旧 `memStore` 的 `mutation.Strategy`，改不动 live

v2 的 `evolution/coordinator/coordinator.go` 已有 `Submit → PatchDecision → Deployer` 骨架（`SetDeployer` `:248`），但 **Deployer 的实现、staging/影子评估、自动回滚通路** 是否为代码实存、是否接线到 live agent，需按本计划 Step B 逐项核验。

### 0.4 六支柱业务链路（本计划"每条链路端到端"的清单）

属于"Agent OS 内核"、且已能被 `system_runtime/orchestrator.go` 编排的组件（`Adopt:397` / `Start:79` / `Shutdown:188` / `Snapshot:570`）：

1. **任务/调度**：`taskfabric` + `kernelscheduler`（executeWithCandidates → RunQuantum → attribution）
2. **进化归因**：`aresrecovery` DeterministicScorer / ExecutionAttribution（`RecordWithMetrics` 吃 latency/retry/recover）
3. **证据存储**：`evidence.Store`（Memory / Postgres）+ `RuntimeObserver.writeEvidence`（`observer.go:285` 记 `source="strategy"`）
4. **门控判定**：`StrategyLifecycle` G1–G4（`lifecycle.go`）+ `ShadowEvaluator`/`ReplayScorer`
5. **DAG/工作流编排**：`system_runtime` + `taskfabric.PlanLoop`（GAP-2 / K1–K5）
6. **预报回滚**：`RollbackPolicy` + `bootstrap` recovery loop

***

## 1. 总体主张与验收矩阵

| #  | 陈形（必须成立）                                                | 现况                                  | 对应 Step |
| -- | ------------------------------------------------------- | ----------------------------------- | ------- |
| E1 | 候选在被 G2 判定前，**确实执行过**、且有自己的一笔 evidence                  | 否（replay 只读历史）                      | Step A  |
| E2 | G2 的"更好"由候选自身的执行结果产生                                    | 否                                   | Step A  |
| E3 | v2 的 patch 能从真实 live 工件产生、经 staging 评估、promote/回滚到 live | 部分（骨架在，回流未验）                        | Step B  |
| E4 | 六支柱链路每跳有测试与可观测点                                         | K1–K5 已覆盖编排层，但"归因→门控→回流"缺失接缝测试      | Step C  |
| E5 | G3/G4 在零-token 与满配置下行为明确、不可橡皮图章                         | G3 无 registry 时有意缺失；G4 staging 默认未验 | Step D  |

***

## 2. 实施步骤

### Step A — v1：给候选装上"自身证据"（real-execution A/B）

**目标**：让 `ShadowSampler` 不仅回读历史，还能把候选**影子执行**到真实任务上，产出一笔 `strategy_id==candidate.ID` 的 evidence。

**A.0 复刻采样桩（当前版）**：先写一个确定性测试钉死现状——候选从不执行、回退 prior。作为改造前基线。

**A.1 设计影子执行器接口**：

```go
// internal/ares_evolution/abexec/ab_executor.go（新包）
// ShadowExecutor 在影子命名空间执行候选策略一次，只记录、不影响 live 响应。
type ShadowExecutor interface {
    // Execute runs candidate against a frozen task snapshot and writes one
    // KindFitness evidence record attributed to candidate.ID.
    Execute(ctx context.Context, candidate *mutation.Strategy, task *taskfabric.Task) error
}
```

- **为什么冻结任务快照**：候选绝不能改 live 状态。执行发生在任务副本上；结果经 `RuntimeObserver.writeEvidence` 的同一路径写 evidence（复用 `observer.go:285` 的 `source="strategy"` 与 payload `strategy_id`），但 `strategy_id` 填候选 ID——这一步就让 E2 成立。

- **接线点**：替换 / 加塞 `shadow_evaluator.go:438 Evaluate` 的 `shadowScorer` 调用。当 `ShadowExecutor` 已注入时，`Evaluate` 先触发一次影子执行得到候选自身分数，再与 active 分数比较——否则回退到现在的 `ReplayScorer`（保留退化路径）。

**A.2 把影子执行喂进数据源**：

- 复用 `kernelscheduler.executeWithCandidates` 的 `RunQuantum` 骨架（`scheduler.go:838-857`），但以"影子模式"执行：结果写入 evidence，不 promote、不回调 `endQuantumOutcome` 的线上 attribution。

- **冷启动策略**：候选执行前也可能无任务可跑 → 保留现有 prior fallback（发布在 Step A.4 里正名）。

**A.3 影子闸开关**：

```yaml
# ares.yaml 新增,默认关闭（不影响现有 fail-closed 语义）
evolution:
  shadow_execution:
    enabled: false        # 显式打开才启用 real-execution A/B
    max_per_submit: 10    # 单次 Submit 最多影子执行几次
```

- 理由：A/B 会消耗真实算力，必须显式开启（同 `evolution.llm_scoring` 的保守默认）。

**A.4 更新失败语义**：

- 影子执行失败 → 记 0 胜即一票"无信息"，不计入 `TotalComparisons`（沿用上轮 B-3 平局语义：`shadow_evaluator.go ShouldDeployLoose` 只计 decisive）。

- 若候选执行后仍无自身 evidence（首次、无任务）→ 显式日志"候选无自身证据，退化到 replay prior"，不得静默。

**A.5 验收**：

- 新单测：注入 fake `ShadowExecutor`，候选执行后 `evidence.Query(strategy_id==candidate.ID)` 返回 ≥1 条，且 G2 报告中该候选出现 "shadow executed" 标记。

- 集成：`cmd/ares` F1/g1 层加一条"候选真执行并 promote"的用例，复用 `e2e_*` 基建。

- 覆盖：`shadow_sampler.go:93` 构造路径、`shadow_evaluator.go:438` 达到 ≥80%。

**A.6 失败退出**：若发现 taskfabric 无干净的任务快照接口，先给 `Fabric` 加一个只读 `CopyTaskForShadow(id)` 助手（不写状态），再做 A.1。

***

### Step B — v2：让 live 工件 → patch → promote/回滚 真闭环

**目标**：把 v2 那条 DAG 变异流水线（现被手搓假 DAG 拴死）接到真实工件上，并让选中 patch 安全回流。

**B.0 复刻现状**：写测试钉死 `evolution-closure-plan.md` 三断点（② `Snapshot` 不 `Mutate` 空 diff、① 假 DAG、③ 不回流）。

**B.1 让 Phase 6 真 Mutate**（修断点②）：

- `internal/ares_evolution/genome_wiring_system.go` `generateDiffPatches`：对每个 genome 先 `g.Mutate(ctx, n)` 生成候选，再与上一代快照 `differ.Diff`，把非空 patch 交 Coordinator 评估。删死逻辑。

- 验收：调一次后 Coordinator 收到 >0 候选，`go test ./internal/ares_evolution/...` 绿。

**B.2 喂 live 副本**（修断点①）：

- `internal/ares_bootstrap/bootstrap.go:152-162`：把 agent 真实 DAG / live KnowledgeRuntime / MemoryManager **snapshot 成可变副本** 传进 `ProvideNewEvolution`，替代手搓假构建。

- 验收：单测构造真实 DAG 副本，Phase 6 后确认某真实 step 被 InsertNode/Parallelize 改到。

**B.3 打通 Deployer + staging + rollback**（修断点③）：

- 用 v2 现有 `EvolutionCoordinator.SetDeployer`（`coordinator.go:248`）实现一个真实 `PatchDeployer`：

  - 选中 top-k patch → 写入带版本号 + 前镜像的**staging 队列**

  - agent 消费：staging 应用 + 影子评估（用 `evaluation/` 跑真实任务）→ 无回归 promote，有回归回滚前镜像

  - 独立 `RuntimePatch` 通道，**不写** `memStore` 的 `mutation.Strategy`（不与 v1 temperature 进化互踩）

- 验收：端到端——注入有益 patch（串行改并行 + 评估通过）→ live 下次执行用新拓扑；注入有害 patch → 自动回滚、live 不变。

**B.4 收敛边界（防乱）**：v1/v2 各自闭环，本计划不合并两套 genome/G3；合并留给 `internal/evolution/verify/` 共享层（0.4.x）。

***

### Step C — 六支柱链路"接缝"端到端测试

**目标**：不是测单点，而是测**链**。补五条跨组件接缝测试（当前缺失）：

| 链        | 起点 → 终点                                                    | 缺失断言                                                                                             |
| -------- | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| C1 任务→归因 | `taskfabric` 完成 → `ExecutionAttribution.RecordWithMetrics` | latency/retry/recover 真实进 deterministic score（上轮 B-1 已修时序，补回归）                                   |
| C2 归因→证据 | attribution → `RuntimeObserver.writeEvidence`              | `source="strategy"` / payload `strategy_id` 一致（`replay_scorer.go:35` 的 `observerEvidenceSource`） |
| C3 证据→门控 | evidence 空/有 → G2 pass/fail                                | `zero_llm_closure_test.go` 已有正反例，扩展：**候选自身 evidence** 场景                                         |
| C4 门控→回流 | G2→必然 promote / rollback                                   | 一次性门控全通过 + 回滚恢复 active                                                                           |
| C5 全链    | 真实任务跑一轮 Mutate→Eval→Promote/Rollback                       | `cmd/ares` e2e 增加"进化前后指标正向"                                                                      |

**验收**：`make gate` 新增 4 条链级用例；`go test -race ./...` 无 race。

***

### Step D — G3 / G4 行为明确化（防橡皮图章）

- **G3**（`gate_eval.go`）：无 `EvaluatorRegistry` = 有纪律的"less G3"，但要把"轮空即跳过"写进日志 + 指标（`evolution.gate.g3.skipped`），避免误判为通过。

- **G4**：确认 deployment staging 在默认配置是否已接线；未接线则与 G3 同理显式标注。

- 验收：CHANGELOG 明确写出 G3/G4 在默认/满配置下的真实行为。

***

### Step E — 收敛前置（0.4.x 界线）

- 抽 `internal/evolution/verify/`：G1–G4 门控语义 + evidence 读写 + promote/rollback 为共享层，v1/v2 复用（`ares-evolution-loop-closure-plan-zh.md` 已就此事立项）。

- 本计划只做到 Step A–D 让两套各自跑实；Step E 是 clear 0.4.x 的收尾，可并行启动但不同石头同时推。

***

## 3. 每步实施顺序与退出判据（DoorCheck）

| 顺序 | Step             | 退出判据（做了才继续）                           |
| -- | ---------------- | ------------------------------------- |
| 1  | A.1–A.2 + A.5 单测 | `ShadowExecutor` 使候选获得自身 evidence；单测绿 |
| 2  | A.4 失败语义         | 无自身证据时显式标注而非静默                        |
| 3  | B.1–B.2          | v2 从真实工件产生 >0 真实 patch                |
| 4  | B.3              | staging→promote/rollback 端到端测试绿       |
| 5  | C                | make gate 含 4 条链级用例，-race 全绿          |
| 6  | D                | G3/G4 行为写进 CHANGELOG 与指标              |
| 7  | E（0.4.x）         | 共享 verify 层立项                         |

**顺序理由**：A 给 G2 装上真证据（E1/E2 达成）→ B 让 v2 真回流（E3）→ C 把链路钉实（E4）→ D 补门控纪律（E5）。A 是最小改动且收益最大，先做。

***

## 4. 明确不做（防过度设计）

- 不重写 `mutation.Strategy` / `Coordinator` / kernel 架构。

- 不删旧 GA（v1 管 temperature 等，保留 baseline 对照）。

- 不引入新外部依赖 / 新进化库。

- `shadow_execution` / v2 patch 回流默认关闭，需配置显式打开。

## 5. 风险与回滚

- Step A：影子执行只写 evidence 副本，不动 live → 风险低。

- Step B.3：唯一有 live 影响的步骤，强制 staging + 影子评估 + 自动回滚。

- 任何 Step 失败：先用 A.0/B.0 复刻测试钉住现状再改，改动皆可回退。

## 6. 与现有文档的关系

- `plan/evolution-closure-plan.md`：v2 断点问题的**前情**，本计划 Step B 是它的落地执行版。

- `CHANGELOG.md [0.3.1] Known limitations`：本计划完成后，需将"候选无自身证据 / 不可 A/B"从已知限制转为"已实现"。

- `docs/ares-evolution-loop-tracker-zh.md`：作为进度追踪表，每 Step 完成后勾选。

***

## 附录 A：当前证据链关键代码锚点（实测）

```
internal/ares_evolution/shadow_sampler.go:45-50  候选无自身证据 → 回退 prior（自述）
internal/ares_evolution/shadow_evaluator.go:438  Evaluate 仅 shadowScorer 纯计算，无实跑
internal/ares_evolution/replay_scorer.go:35      observerEvidenceSource = "strategy"（读写一致）
internal/ares_evolution/observer.go:285          writeEvidence 记 source="strategy"
internal/kernelscheduler/scheduler.go:838-857    RunQuantum + endQuantumOutcome(..., latency, retries)（B-1已修）
internal/evolution/coordinator/coordinator.go:248 SetDeployer（回流骨架）
plan/evolution-closure-plan.md                   v2 三断点（②①③）前情
```

## 附录 B：验收矩阵速查

```
E1 候选执行过        ← Step A.1/A.2 + A.5   ✅/❌
E2 候选自身证据       ← Step A.2 + EvidenceStore 断言
E3 v2 真实回流        ← Step B.1–B.3
E4 链路每跳有测试      ← Step C
E5 G3/G4 行为明确      ← Step D
```

