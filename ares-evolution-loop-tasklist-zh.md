# ARES 进化闭环 Task List

## 唯一目标（验收只认这一句）

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

## 代码核对结论（执行前必读）

已经成立、不要重做：

| 事项 | 位置 | 现状 |
|---|---|---|
| `PlanStep` 类型 | `internal/taskfabric/workflow_plan.go:15` | 已有 `ID/Capability/DependsOn/Priority/MaxRetries/Payload/Origin` |
| 环 + 悬空依赖检测 | `workflow_plan.go:70-80` + `detectPlanCycle` | 已实现且有测试 |
| DAG 版本号 | `engine.MutableDAG.Version()` `mutable_dag.go:439` | 已是 mutation counter，无需新增 |
| DAG 变更事件 | `SubscribeWithID()` `mutable_dag.go:483` | 已有 GraphEvent 订阅 |
| `EnableDreamCycle=false` | `bootstrap_steps.go:208` | 已关闭 |
| LLM 打分默认关 | `wireLLMScorer` `bootstrap_steps.go:693-696` | `Enabled=false` 时返回 nil，`gaCfg.Scorer` 保持 nil |
| kernelscheduler 架构红线 | `internal/kernelscheduler/architecture_test.go` | 已存在（禁 `ares_runtime`）；对 `workflow/engine` 当前 0 引用 |
| lifecycle 快照端点 | `introspect/control.go:196` `/api/evolution/lifecycle` | 已实现，C5 挂它即可 |
| `evolution status` 子命令 | `cmd/ares/evolution.go:35` | 已存在，只需扩输出 |

必须先解决的矛盾：

- **零 token 与 G2 互斥。** `bootstrap_steps.go:309` 的 `shadowGateMode(gaCfg.Scorer != nil, rollbackArmed)`：
  `Scorer == nil` 且 rollback armed ⇒ `Lifecycle.DisableShadowGate = true`，G2 不注册。
  所以「LLM 打分关掉」直接推出「没有 G2」。**C3.2 依赖 C2.6 先把 attribution 打分器注册成独立 scorer。**
- **`engine.Step` 无 `Priority` 字段**（`types.go:52-68`）。Priority 只能从 `Step.Metadata` 派生，不存在直接映射。
- **`PatchChangeRecoveryStrategy` 不改变 PlanStep**：它作用于 `Step.RecoveryPolicy`，而 PlanStep 不携带该字段。
  结构变化的断言只能用结构补丁（Insert/Remove/AddEdge）或 `PatchChangeMaxRetries`。
- **G1 目前可静默失效**：`buildEvolutionGuardrails`（`bootstrap_steps.go:662`）构造失败返回 nil，
  注释明确写「降级为所有检查通过」。
- **`RecordScore` 值域被夹在 [0,1]**（`ares_evolution/scheduler.go:240-249`）。attribution 聚合分必须先归一化，
  否则被 clamp 静默污染。
- **`ExecutionAttribution` 只有成功/失败计数**（`Record(agentID, capability, success bool)`，
  `aresrecovery/evolution_execution_feedback.go:63`）。时延 / 重试 / 恢复次数无数据源，需扩。

---

## 批次 C1 — 单一投影函数（本周）

- [x] **C1.1 建立唯一投影** `internal/planprojection/projection.go`
      `engine.Step → taskfabric.PlanStep`，映射规则固定为：
      | PlanStep | 来源 |
      |---|---|
      | `ID` | `Step.ID` |
      | `Capability` | `Step.AgentType` |
      | `DependsOn` | `Step.DependsOn` |
      | `MaxRetries` | `Step.RetryPolicy.MaxAttempts`（nil → 0，保留 kernel 默认 2） |
      | `Priority` | `Step.Metadata["priority"]` 解析失败或缺失 → 0 |
      | `Payload` | `{"input": Step.Input}` + `Step.Metadata` |
      | `Origin` | 不填，由 Kernel 打戳（`json:"-"`） |
      显式丢弃：`Interrupt`（HITL 冻结）、`Timeout`、`RecoveryPolicy`（归 RecoveryPatchExecutor）、
      `Name`、`Status/Output/Error/StartedAt/FinishedAt`（执行期状态）。
      注意：投影不能放在 `taskfabric` 包内 import `workflow/engine`（会反向污染 kernel 侧），
      放 `cmd/ares` 或新建 `internal/planprojection` 薄包，由 cmd 装配。
- [x] **C1.2 扩架构红线** 的 `banned` 从单值改切片，
      加入 `internal/workflow/engine`。当前 0 引用，本条是防回归而非重构。
- [x] **C1.3 双入口接线**（原清单只写了启动分支，会漏运行期）：
      - 启动一次：`cmd/ares/serve_agents.go:76` `UpdateLiveDAG` 成功分支 → 投影 → `Fabric.CompilePlan`
      - 运行期增量：`MutableDAG.SubscribeWithID()` 消费 GraphEvent → 重投影 → `CompilePlan`
      订阅必须配 `Unsubscribe`，否则 channel 泄漏。
- [x] **C1.4 记录编译世代**：每次 `CompilePlan` 落一条
      `{generation, dag_version=MutableDAG.Version(), compile_id, plan_ids, step_count}` 到 EventStore。
- [x] **C1.5 测试**：peer 种群 → 投影 → PlanStep 依赖序等价；
      断言投影层**不提前吞掉** `CompilePlan` 已有的环 / 悬空依赖错误（检测本身已实现，测的是不被静默）。

**验收**：`GraphPatchExecutor` 增删一个节点后，下一轮 Scheduler 候选任务集合随之增减，无需重启。

---

## 批次 C2 — 零 token 打分（Attribution → Strategy.Score）

- [x] **C2.1 扩 attribution 数据面**：`ExecutionAttribution.Record` 当前只收 `success bool`。
      新增时延 / 重试次数 / 恢复次数字段，记录点在 `kernelscheduler/scheduler.go:977` `endQuantumOutcome`
      （注意其 preemption fencing 分支必须继续走 NEUTRAL，不能计入失败）。
- [x] **C2.2 确定性打分器**：由扩后的 attribution 聚合出 `[0,1]` 分数，**不走 LLM**，固定权重、无随机源。
      归一化是硬要求（见核对结论）。
- [x] **C2.3 回写 Score**：`cmd/ares/peer_mode.go:194-200` 的 `EvolutionFeedbackLoop` 现只把置信度喂回
      `loadTracker`；新增一路写入 `mutation.Strategy.Score` / `StrategyStore`。
- [x] **C2.4 替掉常量分**：`internal/ares_evolution/scheduler.go:385-388` 现为
      `EventTaskCompleted → RecordScore(1.0)` / `EventTaskFailed → RecordScore(0.0)`（常量在 `:140-145`），
      改为上报 C2.2 的聚合值。
- [x] **C2.5 断言 LLM 计数为 0**：代码侧无需改动（`Enabled` 默认 false 已保证 `Scorer == nil`），
      只在闭环 e2e 加计数断言。
- [x] **C2.6 让打分器算「独立 scorer」**（C3.2 的前置）：把 C2.2 的打分器接进
      `shadowGateMode` 的 `hasScorer` 判定，使零 token 路径也能产生影子对照证据。
      否则 `bootstrap_steps.go:309` 会把 G2 直接摘掉。
- [x] **C2.7 测试**：注入两组不同任务分布，分数排序必须不同且可复现（固定种子）。

**验收**：`chaos kill` 一个 agent 后，受影响策略的 `Score` 在一个反馈周期内下降，且 LLM 调用数为 0。

---

## 批次 C3 — 门禁真实化（G1 必过 / G2 影子回放）

- [x] **C3.1 G1 改成真必过**：`buildEvolutionGuardrails`（`bootstrap_steps.go:662`）构造失败现返回 nil
      并降级为「全通过」。改为构造失败即 fail-closed（拒绝候选并计数），不再静默放行。
- [x] **C3.2 G2 影子回放**：`gaCfg.ShadowEvalConfig`（类型 `evolution.ShadowEvaluationConfig`，
      字段 `Enabled/MinSamples/MinWinRate/DeterministicScorer`，装配处 `bootstrap_steps.go:235-239`）
      改用**回放历史执行**做对照，不新增 LLM 调用。
      零 token 路径下 `DeterministicScorer=true`（`:296` 的现有告警保留），
      需确认 MinSamples 是被独立证据满足而非同分重复。**依赖 C2.6。**
- [x] **C3.3 gate 决策留痕**：每次 promote / reject 记录 `{generation, gate, reason, win_rate}`。
- [x] **C3.4 保持 fail-closed**：`shadow_gate_wiring.go:64-73` 三分支语义不变
      （无独立证据且无回滚网 → G2 注册且拒绝放行）。
- [x] **C3.5 测试**：构造必被 G1 拒 / 必被 G2 拒的候选，断言不落到 live DAG。

**验收**：任一候选的最终去向都能追到一条门禁记录，没有「静默放行」。

---

## 批次 C4 — 补丁落 live DAG 并驱动下一轮

- [x] **C4.1 patch → live DAG**：`internal/evolution/patch` 的结构补丁只作用于
      `ares_runtime.AgentDAGLiveKey` 注册的那张图（`serve_agents.go:75`）。
      核对 `graphExec.SetGraph` / `recoveryExec.SetDAG`（`provide_new_evolution.go:383-404`）
      指向的是同一张 live DAG，不是 bootstrap 占位图。
- [x] **C4.2 版本号接线**（非实现）：`MutableDAG.Version()` 已存在，把它带进 C1.4 的编译记录。
- [x] **C4.3 触发重编译**：复用 C1.3 的 GraphEvent 订阅路径，Scheduler 下一轮生效。
- [x] **C4.4 幂等 / 回滚**：同一补丁重复应用不产生重复节点；回滚触发时 DAG 与已编译计划一起回退。
- [x] **C4.5 测试**：断言对象改为**结构补丁**（Insert/Remove/AddEdge）或 `PatchChangeMaxRetries`。
      `PatchChangeRecoveryStrategy` 改 `Step.RecoveryPolicy`，PlanStep 不带该字段，
      拿它断言「PlanStep 同步变化」必然失败。
- [x] **C4.6 补一条 recovery 路径断言**：`PatchChangeRecoveryStrategy` 的生效证据看
      `RecoveryPatchExecutor` 的 DAG 快照，与 PlanStep 断言分开。

**验收**：结构补丁生效后 `runtime snapshot` 与 Scheduler 实际执行图一致（不再是两张图）。

---

## 批次 C5 — introspect 可归因

- [x] **C5.1 扩 `ares evolution status`**（`cmd/ares/evolution.go:35`，实现在 `showEvolutionStatus`）：
      输出当前代号、live `dag_version`、最近一次 `CompilePlan` 的 `compile_id` 与 id 集合。
- [x] **C5.2 归因链查询**：挂在已有的 `/api/evolution/lifecycle`（`introspect/control.go:196`）
      而非新建端点；给定一次调度变化，能回答「哪一代、哪道门、哪次 CompilePlan」。
- [x] **C5.3 指标**：generation、gate_pass/reject、compile_count、dag_version 暴露到 `/metrics`
      （gate skip 已有 `RecordEvolutionGateSkipped`，`bootstrap_steps.go:323`，复用同一命名族）。
- [x] **C5.4 测试**：e2e 断言三元组（generation / gate / compile_id）非空且相互可关联。

---

## 批次 C6 — 端到端验收脚本

- [x] **C6.1** `kill agent` 场景：拓扑或调度参数变化 + LLM 调用 0 + 三元组可查。
- [x] **C6.2** `换任务分布` 场景：同上。
- [x] **C6.3** 全程 `EnableDreamCycle=false`（`bootstrap_steps.go:208`）断言。
- [x] **C6.4** 冻结项回归：AKG / HITL / 子图执行器未被本闭环触发。
- [x] **C6.5** 断言 G2 实际注册（`Lifecycle.DisableShadowGate == false`）。
      零 token 下这是 C2.6 是否真做到的唯一证据，否则闭环少一道门却全绿。

---

## 依赖顺序（修正）

```
C1 ──► C4 ──► C5 ──► C6
 │       ▲             ▲
C2 ──────┘             │
 └──► C3 ──────────────┘
```

- C1 是硬前置（没有单一投影，后面全是两张图）。
- **C2 → C3 是硬依赖**，不能并行：C3.2 需要 C2.6 提供独立 scorer，
  否则 `shadowGateMode` 会在零 token 配置下把 G2 摘掉，C3.2 无法执行。
- C2.1 是 C2 内部的硬前置：现有 attribution 没有时延 / 重试 / 恢复次数数据。
