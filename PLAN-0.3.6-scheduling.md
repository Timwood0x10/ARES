# 0.3.6 开发计划：调度的确定性、工作保持、公平与背压 ＋ L1 写入方

> 0.3.4（AKG 转正）与 0.3.5（可运营性 + 收敛）之后，**调度循环自 0.3.x 起没有实质改动**。
> 本计划处理的是它剩下的五个结构性缺口：一个批次屏障、一次只在纸面上生效的抢占、
> 一个顺序随机的待选集合、一个不设上限的提交侧，以及一条**从未有人写入的 L1 元数据**。

---

## 0. 一句话结论（先说结论，后附证据）

| # | 问题 | 今天的真实行为 |
|---|---|---|
| 1 | **drain 是批屏障** | `drain` 末尾 `wg.Wait()`，而 `Run` 的 select 循环**同步**调用它 ⇒ 一个 30s 量子会把**调度循环本身**卡住，空闲 agent 无法接新任务 |
| 2 | **抢占不中断在飞调用，但已会让步** | `Preempt` 只做 `transition(StateReady)`（无 ctx 取消）；**而量子在步骤边界检查 `PreemptRequested` 并丢弃该步结果**（`quantum.go:92-112`）⇒ 在飞的 LLM 调用**确实跑完**（计费照付），但**不会继续派发下一步**。这一条要做的不是实现，而是**验证 + 文档化 + 补可观测** |
| 3 | **顺序不确定** | `ReadyTasks`/`ResumableTasks` 遍历 `map[string]*Task` ⇒ 无 FIFO、无 aging、无 per-session fairness |
| 4 | **提交侧无背压** | 全仓无 `maxReady/queueLimit/ErrQueueFull` ⇒ READY 可无限增长 |
| 5 | **L1 metadata 无写入方** | 规划器**读** `enabled`/`budget`/`prior`，但 `SetNodeMetadata(` 只有两处真实调用（工作流 DAG、patcher 持有的 DAG），**没有一处写 L1 工具类图** |

---

## 1. 现状与证据（逐条回源码）

### 1.1 批屏障

```
internal/kernel/scheduler_dispatch.go:72   var wg sync.WaitGroup
internal/kernel/scheduler_dispatch.go:108  wg.Wait()
internal/kernel/scheduler.go:355           case <-ticker.C:  s.safeDrain(ctx)
internal/kernel/scheduler.go:367           case <-ev:        s.safeDrain(ctx)   ← 同一 goroutine
```

`drain` 内部用 `sem`（容量 `drainLimit()`）限制并发，但**返回前必须等整批**。于是：

- 批内最慢的量子决定循环的下一次迭代时间；
- 批期间到达的 READY 任务**无法**被已经空出来的 agent 拾取；
- 事件分支（依赖完成事件）也被 `wg.Wait` 挡住，只能等 ticker 下一轮。

> **不能简单删 `wg.Wait()`**：`scheduler.go:291` 的注释写明了它存在的原因——`Run` 返回后会清 `s.running`，
> 若不等在飞量子，关停会先"完成"，而 goroutine 仍握着 lease 改 fabric 状态。**等待的职责要从"派发"移到"关停"。**

### 1.2 抢占：状态立刻变，让步发生在步骤边界（**已实现**）

```
internal/fabric/task/fabric_schedule.go:99    t.PreemptRequested = true
internal/fabric/task/quantum.go:100           if t, ok := f.tasks[taskID]; ok && t.PreemptRequested { ... }
internal/fabric/task/quantum.go:104           t.PreemptRequested = false
```

`quantum.go` 的注释已写明真实语义：*"the in-flight LLM call completed naturally (not cancelled), but its
result is fenced out"*，该步的 checkpoint/错误被**丢弃**，任务保持 READY 等下次 drain。

⇒ **"抢占只改 durable state"这个判断不准确**：它改状态**并且**在步骤边界让步。不成立的部分只有一个——
**一次已经发出的 LLM 请求不会被取消**（成本照付）。本计划据此把 M5 从"实现"降级为"验证 + 文档 + 指标"。

### 1.3 顺序不确定

```
internal/fabric/task/dag.go:43   for id, t := range f.tasks { ... }      // ReadyTasks
internal/fabric/task/dag.go:73   func (f *Fabric) ResumableTasks() ...   // 同样遍历 map
```

Go 的 map 迭代顺序**每次不同**，因此：同一 READY 集合两次派发的顺序可以不同；没有 `ready_at` 参与排序；没有会话维度的占位。

### 1.4 无背压

`grep maxReady|queueLimit|ErrQueueFull` 只命中 `scheduler_dispatch.go:193` 的**优先级比较变量**，与容量无关。
提交路径（`POST /api/tasks`、`create_task`、`spawn_agent`）都没有上限。

### 1.5 L1 metadata 无写入方

```
internal/fabric/agent/planner_cognition.go:63   L1DAG *engine.MutableDAG      // 规划器读
cmd/ares/peer_assembly.go:331                    L1DAG: l1DAG                  // 生产注入
internal/runtime/evolution/genome/workflow_genome.go:324   dag.SetNodeMetadata(...)   // 工作流 DAG
internal/fabric/task/workflow/engine/dag_patcher.go:198    dag.SetNodeMetadata(...)   // patcher 持有的 DAG
```

⇒ **没有一条生产路径把 GA 的 patch 应用到 L1 工具类图**。这解释了为什么 `prior` 长期是"声明值"，以及
为什么 README 的 Honest limits 里这条一直挂着。

---

## 2. 目标 / 非目标

### 目标（可判真假的陈述）

1. 一个慢量子在飞时，**空闲 agent 能在 1s 内拾取新 READY 任务**（今天的等待上界是"整批结束"）；
2. 同一 READY 集合、同一 `priority`，**两次派发顺序一致**；
3. 多会话下，一个会话**不能**连续占用超过 `N` 个派发名额（`N` 由 aging/权重决定，可配置）；
4. 提交超过上限时返回**可识别错误**（而非静默堆积）；
5. GA 产出的 L1 元数据 patch 能**按 `ProposalKey` 审计**地应用到 L1 图，并被**下一个量子**读到。

### 非目标（0.3.6 明确不做）

- **不中断在飞的 LLM 调用**（见 §5 边界说明）；
- 不做分布式/多副本调度；不承诺跨进程 exactly-once；
- 不改 `maxConcurrentPerAgent=1` 的值（但**语义要从"批屏障隐含"变成"租约显式保证"**，见 §5）；
- 不做 token 级抢占；不做会话内并行工具；
- 不为 L1 引入新的图类型——沿用现有 `engine.MutableDAG`。

---

## 3. 里程碑

### M0 · 基线（必须先做，1.5d）

**为什么第一**：0.3.5 我们反复踩到"测试空转"——没有基线，"更快/更公平"都不可证伪。

| 项 | 内容 |
|---|---|
| 指标 | `ARES_scheduler_ready_to_dispatch_seconds`（histogram，`task.ready → Acquire`）、`ARES_scheduler_work_conserving_gap_seconds`（慢量子在飞时其他任务的派发延迟）、`ARES_scheduler_ready_age_seconds`（派发时的任务年龄）。**前缀必须用 `ARES_`**，与现有的 `ARES_evolution_*` 一致——写成小写会让看板和 `Makefile` 里的 `grep ARES_` 都对不上 |
| 现状 | **三个都没有**（`grep ready_to_dispatch|work_conserving|ready_age` 为空） |
| 落点 | `internal/runtime/observability/prometheus.go` + `kernel/scheduler_dispatch.go` 的派发点 |
| 验收 | 一次人工构造的慢量子，能在指标里看到 ≥30s 的 `work_conserving_gap`（今天它必然很大） |

### M1 · 确定性派遣（1.5d）

| 项 | 内容 |
|---|---|
| 改动 | `Fabric` 增加 `readySince map[string]time.Time`（`StateReady` 时写入，离开时删除）；`ReadyTasks`/`ResumableTasks` 返回**排序后**的 id：`(priority DESC, readySince ASC, id ASC)` |
| 为什么带 `id` | 前两键全等时仍需要**全序**，否则"确定性"在并列时失效 |
| 验收 | 同一 READY 集合、**同一注入时钟**，打乱插入顺序 100 次 ⇒ 派发序列**完全一致** |
| 时钟契约 | 排序函数接收 `now`（不直接读 `time.Now()`）。否则 M3 的 aging 会让顺序随时间漂移，M1 的确定性断言就变成不可判定的 |
| 反证 | 去掉排序 ⇒ 100 次里至少一次不同（用固定 seed 的乱序插入放大） |

### M2 · work-conserving pump（3d）

| 项 | 内容 |
|---|---|
| 改动 1 | `drain` 拆成 **pump（不等待）+ tracker（等待）**：pump 起 goroutine 后立即返回；在飞量子登记到 `inflight` tracker |
| 改动 2 | `Run` 的循环不再被阻塞：ticker 与事件分支都能在上一批未完成时再次 pump（由 `drainLimit` 与 tracker 容量共同约束） |
| 改动 3 | **关停路径**改为 `tracker.Wait()`，语义与今天的 `wg.Wait` 等价（不降低诚实性） |
| 验收 | 一个 30s 量子在飞 + 空闲 agent ⇒ 新 READY 的 `work_conserving_gap < 1s`；关停仍然等到所有在飞量子**真正退出**（用 goleak 断言） |
| 反证 | 恢复 `wg.Wait()` ⇒ `work_conserving_gap` 回到"整批时长"，测试必失败 |

> **最容易做错的地方**：把 `drainLimit` 的语义从"每批并发"变成"全进程并发"会悄悄降低吞吐。必须保持
> "同 agent 单量子"由**租约**保证，而不是由"一次只 pump 一批"保证。

### M3 · 公平 / aging（1.5d）

| 项 | 内容 |
|---|---|
| 策略 | 每会话维护 `lastDispatchedAt`；排序键增加**老化项**：`effectivePriority = priority + agingWeight × waitSeconds` |
| 备选 | Deficit Round Robin（按会话轮转，权重=优先级）——若 aging 调参成本高，改 DRR |
| 验收 | 会话 A 有 100 个 ready、B 有 1 个、`drainLimit=N` ⇒ **B 的 `ready_age` 不超过 `ceil(2 × K)` 个派发周期**（K 为会话数，界由 aging 权重推导，不写死"5 次"） |
| 反证 | 改回纯 FIFO/纯 priority ⇒ B 的 `ready_age` 持续增长（断言其 p99 超过阈值） |

### M4 · 提交侧背压（1d）

| 项 | 内容 |
|---|---|
| 策略决策（需拍板） | **拒绝**（返回 `ErrQueueFull`/429）还是**延迟**（阻塞到有位）还是**降级**（接受但标记低优先级） |
| 建议 | **拒绝 + 可配置上限**（`kernel.max_ready_tasks`，默认 0 = 不限，保持现有部署行为不变） |
| 接线 | `POST /api/tasks`、`create_task`、`spawn_agent` 三个入口统一走一个 `admit()` |
| 验收 | 上限=10，提交第 11 个 ⇒ 明确错误；错误体含当前深度与上限 |
| 反证 | 去掉上限 ⇒ 连续提交 10k 个，READY 深度单调增长（断言失败） |

### M5 · 抢占边界：验证与可观测（0.2d，**不是实现**）

| 项 | 内容 |
|---|---|
| 现状 | 步骤边界让步**已实现**（`quantum.go:92-112`）：被抢占时丢弃该步结果、保持 READY；在飞调用不取消 |
| 本里程碑做什么 | ① 补一条**端到端测试**证明被抢占的任务不会继续派发下一步；② 加一个计数器 `ARES_scheduler_preemptions_total{outcome}`；③ 把"一次 LLM 调用不可中断、其成本照付"写进 README 的 Honest limits |
| **不做** | 不取消在飞的 provider 请求（见 §5.1） |
| 反证 | 去掉 `quantum.go` 的 `preempted` 分支 ⇒ 被抢占任务仍跑完全部步骤，测试必失败 |

### M6 · L1 metadata 写入方（2d）

| 项 | 内容 |
|---|---|
| 现状 | 规划器读 L1（`enabled`/`budget`/`prior`），但**没有生产写入方**；`PatchSetNodeMetadata` 的 executor 只作用于它持有的 DAG |
| 设计 | 新增 **L1 patch executor**：把 GA 的 `PatchSetNodeMetadata` 应用到 `peer_assembly.go:331` 注入的那个 `*engine.MutableDAG`；沿用 `UngatedPatcher` 的决策链与 `ProposalKey` 审计 |
| 落点修正（评审发现） | patch registry 的 **fallback 已绑定 live DAG**（`provide_new_evolution.go:468-480`，`c.dagExec.SetDAG(dag)`），所以问题不是"没有 executor"，而是**L1 节点没有归属**：一个 targeting L1 节点 ID 的 patch 会被 fallback 打到工作流 DAG 上并因 target 不存在而失败。M6 要补的是**按 target 路由到 L1 executor**，而不是再加一个 fallback |
| 前提 | 必须与 0.3.5 的 `objective=runtime|both` + `allow_live` 门保持一致：**默认 `strategy` 下这条路径不存在** |
| 回滚 | patch 自带 `Rollback`（已有字段）；写入前快照旧值 |
| 验收 | 一次 L1 patch ⇒ 下一个量子广告集合变化；审计 API 能按 `ProposalKey` 找到它；`grep SetNodeMetadata` 出现**第三个**调用点（L1） |
| 反证 | 去掉 executor 接线 ⇒ 广告集合不变（今天的现状），测试必失败 |

---

## 4. 顺序与依赖

```
M0 基线 ──> M1 确定性 ──> M2 pump ──> M3 fairness
                 │            │
                 │            └────> M5 抢占边界
                 └──────────────────> M4 背压
                                      M6 L1 写入方（独立，可与 M3/M4 并行）
```

- **M1 必须在 M2 之前**：顺序随机时，pump 的所有"派发顺序"断言都在抖；
- **M0 必须在所有之前**：否则无法证明改善；
- **M6 独立**：它与调度循环解耦，可并行，但它依赖 0.3.5 的 `objective` 门。

---

## 5. 风险与边界（必须写进文档的诚实条款）

### 5.1 抢占不中断在飞 LLM 调用

真正中断一次已发出的请求需要 provider 侧取消 + 流式断连 + **部分响应的重放语义**（算不算一次实验？
计费谁付？）。这会把调度问题变成分布式语义问题。

**0.3.6 的边界**：步骤边界让步**已实现**（`quantum.go:92-112`），本计划只补它的测试、指标与文档；
在飞的调用自然结束、结果被 fencing 丢弃，**该成本被记录在案**，不假装解决。

### 5.2 `maxConcurrentPerAgent=1` 的语义迁移

今天这条约束部分由"批屏障 + 每 agent 串行"隐含保证。pump 之后，**必须由租约/认领显式保证**，
否则会出现同一 agent 并发跑两个量子——这是本计划**最容易造成的回归**。
验收里必须有：同一 agent 的两条 READY 任务，任意时刻只有一个处于 `RUNNING`。

### 5.3 热路径改动

调度循环是内核最热路径。要求：

- 每次改动都跑 `-race` + **soak**（现有六路负载，需再加一路"高频提交 + 慢量子"）；
- 任何"提高吞吐"的说法都必须带 M0 指标的前后对比，否则不予采信。

---

## 6. 工作量

| 里程碑 | 人天 |
|---|---|
| M0 基线 | 1.5 |
| M1 确定性 | 1.5 |
| M2 pump | 3.0 |
| M3 fairness | 1.5 |
| M4 背压 | 1.0 |
| M5 抢占验证（非实现） | 0.2 |
| M6 L1 写入方 | 2.0 |
| **合计** | **10.7**（不含 soak 与回滚演练，约 +1.5）；M5 原估 1.0 已扣除，因为该行为已存在 |

---

## 7. 需要先拍板的三件事

1. **背压策略**：拒绝 / 延迟 / 降级？（建议：拒绝 + 默认不限，保持现有行为）
2. **公平目标**：aging 调参 vs DRR？（建议：先 aging，参数可配，用 `ready_age_p99` 判定）
3. **L1 写入方的开关**：是否复用 `objective=runtime|both`，还是需要一个独立的
   `evolution.l1_patches.enabled`？（建议复用 `objective` + `allow_live`，避免出现第二个"绕不过去"的门）

---

## 8. 与已完成工作的关系

- 0.3.5 的运营性计划 v2 已把 **M1（调度）/ M2（可观测）整块移入 0.3.6**（该文件本身已随 0.3.5 收尾删除，口径见 CHANGELOG 的 0.3.5 段），本计划是那两块的具体化；
- 0.3.5 已落地的 `objective` 单路径、审计 API、PostDeploy 观测窗、四个审计计数器，
  是 M6 与 M0 的**直接前置**（审计链已存在，只差 L1 executor 与调度指标）；
- 本计划**不**触碰 0.3.5 已冻结的语义（`objective` 默认值、`allow_live` 双键、审计响应形状）。
