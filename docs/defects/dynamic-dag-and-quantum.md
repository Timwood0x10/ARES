# 缺陷与边界记录 · 动态 DAG + 量子化

> 本文档**只记"会咬你"的坑与已知边界**，不重复"怎么工作"。
> 每条带源码锚点（v0.3.3 行号可能随 dev 分支漂移，定位以符号为准）。
> 性质标记：`取舍`（设计权衡，知道边界即可）/ `缺陷`（建议修）/ `隐患`（规模/并发下才暴露）。

---

## 一、量子化（quantum）

量子化 = agent 做"一小步"就交回队列，进度存 `Task.Checkpoint`（`StepCheckpoint`，`internal/fabric/agent/executor.go:42`），挂了换人从 checkpoint 续跑。

### Q1 · "想/做"跨量子，工具结果延迟一整轮 〔取舍〕

planner（想，长节点）和 tool（做，执行）是**两类量子，中间隔一次调度**。
工具 A 的输出，要等**下一个 plan 量子**才喂给 LLM（结果存 fabric → drain → 下个 quantum 读前驱）。

- **代价**：ReAct 里 LLM 看到工具结果**立刻**决定下一步；这里绕一圈，"实时试错"型任务变笨、变慢。
- **结论**：这是"用结构化换鲁棒性"的固有税，**不是 bug**，但做"快速试错"场景要比经典 ReAct 笨重。
- 锚点：`plannerCognition`"never executes tools, only grows the graph"（`planner_cognition.go` 头注释）；结果回流靠 `fabric` envelope + `ResumableTasks`。

### Q2 · 小步放大 LLM 调用次数 / token 〔取舍〕

一个量子"只做一小步"。一个 N 步任务 ≈ N 次 planner 调 LLM + N 次工具，每量子单独记 token（`quantumUsage`，`scheduler_quantum.go:19`），每次 quantum 重新组 context。

- **代价**：比 ReAct"一次 LLM 决定多个工具"更烧钱。
- **刹车就是 `agent_budget`**：钱包小 → 频繁 yield 让路（慢但省）；钱包大 → 快但贵。**钱包是这个税的调节旋钮。**

### Q3 · 换人是"有损续跑"：checkpoint 是 PCB，不是"脑子" 〔缺陷〕

`Task.Checkpoint` 存的是**做到第几步 + 前驱输出 + token 累计**（`CheckpointEnvelope`，`task.go`），**不存 LLM 的推理过程（CoT / 为什么这么做）**。

- **后果**：默认换人路径（`RecoverTaskCheckpoint`，`recovery.go`）里，replacement 拿到的是"工具 A、B 跑了，结果这些"，**拿不到"我为什么先 A 后 B"的思路**。
- 只有**原地复活**（`RestartAgent` + death snapshot，`recovery.go`）才把 `CognitiveState`（认知/脑子）带过去。
- **坑**：默认路径是**有损续跑**——"接着做" ≠ "接着想"。对"步骤可重放"任务没问题；对"推理链依赖上下文"的任务，replacement 可能走偏。**这是"agent 可丢弃"的隐形成本。**
- **建议**：把关键推理步骤纳入 checkpoint（哪怕摘要），或对"强依赖上下文"的任务强制走原地复活而非换人。
- **核实（2026-10-12）**：机制成立，且边界需精确化（生产恢复是 **requeue**、根本不是认知转移；
  L2 图上"先 A 后 B"可由**图结构**推出，丢的是模型**自述理由**）。方案 A/B 与判断详见
  `disposable-agent.md` A1「核实 / 修复方案」段。

### Q4 · 抢占粒度粗 + 单 agent 串行 〔取舍〕

`maxConcurrentPerAgent=1`（认知不可重入）+ **只在量子边界抢占**（`PreemptLowerPriority` 打回 SUSPENDED）。

- **代价**：一个 agent 上的长任务串行占着，高优任务只能等它**交出一个量子**才插队。
- 这是"一个 agent 同时只做一个 quantum"的架构级保证（防重入），不是可选项。

### Q5 · "谁在跑"的 map 别名（已修，属易错区） 〔隐患-已修〕

`withExecutingAgent`（`executor.go:130`）：payload 的 map **与 checkpoint envelope 是同一引用**（`DecodeCheckpoint` 不拷贝），原地写会把某量子的 executor 盖到 envelope 上，**把后续量子误归到已死/被抢占的 agent**。靠"每量子 copy 一份"修掉。

- **含义**：这类"共享引用"在量子反复 yield→resume 时极易再爆，后续改 checkpoint 结构要盯紧"引用 vs 拷贝"。

---

## 二、动态 DAG（L2 session graph）

动态 DAG = LLM 边做边往 per-session 的 `MutableDAG`（L2）长 plan/tool/answer 节点，每个节点即时编译成 fabric task，调度器 drain 执行。

### D1 · `maxPlanDepth=10` → 复杂任务撞墙"静默提前交差" 〔缺陷-需可观测〕

L2 增长深度默认上限 **10 轮**（`DefaultMaxPlanDepth = 10`，`planner_cognition.go:26`）。撞墙就**强制出 answer 节点**，`ForcedAnswers`（`planner_cognition.go:109`）统计"多少次因深度耗尽被强塞答案"。

- **坑**：真需要 15 轮 plan-tool 的复杂任务，在第 10 轮被**逼着出 answer——计划没长完就交差**。
- **现状（v0.3.3 实测，比初稿好一点）**：撞墙时**有** `logger.Warn("planner: max plan depth reached, forcing answer", ...)`（`planner_cognition.go:266`），并把 `truncated=true` 写进结果 metadata；`ForcedAnswers()`（`:123`）作为 process-wide canary 计数被 canary 测试消费。**真正缺的是"面板 / 告警规则"**——这条 Warn 不会自动变成告警、也不进 metrics。
- **性质**：10 是防 runaway 的保护，但也**封顶了合法复杂度**。
- **建议**：把 `ForcedAnswers` 接进 metrics/面板并配阈值告警（现在只有 canary 计数 + 一条 Warn 日志），让"深度掐断"在面板上可见。

**修复（2026-10-12，已核实：缺的是"可见性"，不是机制）**

- **实证与本文一致**：撞墙**已有** `logger.Warn` + `forcedAnswers` canary 计数 + `truncated=true` 进结果
  metadata（`planner_cognition.go`）。**缺的是"外部调用方能不能看出来"** —— 之前只有日志。
- **修法**：答案节点新增 `answer_cause` 参数（`l2graph.go`），深度守卫盖 `depth_capped`；
  `answerCognition` 复制进终态结果 metadata，与既有 `truncated` 并列。
- **没有自造 metrics 设施**：全仓无 metrics 包（`internal/metrics` 不存在）。按规范 §10（不引依赖）
  与 §0.4（不自造机制），"可见化"落在**仓库既有机制**上 —— 终态结果 metadata + 日志，这正是
  `truncated` 一直以来的做法。面板/告警规则由消费该 metadata 的一侧决定，不再需要新管道。
- **测试**：深度守卫用例（`planner_cognition_test.go`）新增"成因必须落到答案节点 payload"断言。

### D2 · L1 约束全拦 → 会话"正常地"啥都没跑 〔缺陷-需可观测〕

`growToolNodes` 返回 `grown==0`（LLM 想调的工具**全被 L1 的 `enabled=false`/`budget` 拦掉**）→ **不新长 plan，直接强制出 answer**（`planner_cognition.go:343` 注释 "All tool calls were skipped by L1 constraints... Force an answer so the session terminates"）。

- **坑**：L1 配错（把关键工具误设 `enabled=false`），会话会**"看起来正常出了个答案"，其实一个工具都没跑**。
- **与 D1 同源**：都是"提前交差且静默"。
- **建议**：出 answer 时**标明成因**（"被 L1 拦" vs "正常完成" vs "深度掐断"），别混进正常 `COMPLETED`。

**修复（2026-10-12，已核实缺陷成立 → 已修）**

- **实证**：`grown == 0` 分支出答案时**不带任何标记**（`planner_cognition.go`），与"模型正常给出答案"
  在终态结果上**完全无法区分** —— 本文描述准确。
- **修法**：该分支盖 `answer_cause = l1_blocked`，并补一条**聚合告警**（"every tool call skipped by L1
  constraints"，带 requested 计数）：`growToolNodes` 的逐工具跳过日志各报一条，**没有任何一条**说
  "这一轮一个工具都没跑成"。
- **设计要点 —— 两把键，别压成一个**：实现中一度把"为什么停"与"有没有答案"合成单一标签，结果
  "深度掐断 + 合成失败 → 只剩 gap body"会把成因**覆盖掉**（被新测试当场抓到）。现拆为：
  - `answer_cause`：`planned` / `depth_capped` / `l1_blocked` / `unlabeled`（非 planner 生产者未盖章）
  - `answer_body_source`：`supplied` / `synthesized` / `gap`（是否真的产出了答案）
  两者**正交**：强制结束仍可能产出真答案（合成），正常结束也可能完全没答案（gap）。
- **测试**：`answer_cause_test.go`（两把键 × 5 场景，含"`depth_capped` 不因 gap 而丢失"这条回归）+
  `l1_constraint_test.go` 断言被强制出的答案节点带 `l1_blocked`。
- **未做（属 G1）**：没有把 gap/forced 答案在 **outcome 层**降级为非 success —— 那是 P0-1 的
  "正确率信号"决策（改 fitness 语义），本轮只做"可见"。`answer_body_source` 正是 G1 要读的信号。

### D3 · Reconcile 全量对账：快，但 O(节点) + 拿锁 + RUNNING desync 〔隐患-规模化〕

漏事件时的兜底是全量拓扑重投影 `Reconcile`（`coordinator.go:377`）：

- **250ms standing tick** 轮询 drop counter（`reconcilePollInterval`，`:233`）；
- 一次全量 Reconcile **30s 超时**（`reconcileTimeout`，`:240`）；
- **Reconcile 持有 fabric 锁**，且**不能收 RUNNING 任务**（"a full rebuild cannot reclaim a RUNNING task"，`coordinator.go:93`）。

- **坑（规模上来时）**：热会话增长快、图事件缓冲溢出丢事件 → 触发全量重投影 = **O(节点数) + 锁竞争**，且期间 RUNNING 节点**可能短暂 desync**（等下个 tick 收敛）。这是热路径上最贵的操作。
- **建议**：规模一大前，优化 Reconcile（增量收敛而非全量 / 不持全局锁重投影 / 对 RUNNING 节点单独对账）。

### D4 · 事件"至少一次" + 250ms tick 可能追不上 〔取舍〕

图事件是"至少投递一次"，靠 drop counter + DAG version 双信号兜底（F-21）。热路径 LLM 高频长节点时，**250ms 的 standing tick 可能滞后**——增量 `ApplyChange` 快，但"漏了"要等下一次 Reconcile 才修。

- **含义**：事件驱动是"快速路径 + 兜底全量"的组合，不是"严格有序无丢"；高频场景要接受"最坏 250ms + 一次 Reconcile"的收敛延迟。

### D5 · L2 热路径冻结 HITL 〔取舍/已知边界〕

`planprojection` 编译 L2 节点→任务时**显式丢弃** `Interrupt` / `RecoveryPolicy` / `Name`（`projection.go:55` 注释 "HITL frozen or execution-time state"）。

- **后果**：动态 DAG 这条热路径**不拦"等人批"**——L2 图长得再漂亮，HITL 只在 SDK / 全功能引擎那层有效；L2 编译出的任务不带 approval gate。
- **注意区分**：0.3.3 新增的 **task 层 approval gate**（`Task.RequiresApproval`，`fabric_lifecycle.go`）是**另一条路**（提交时/计划步声明），和"编译 L2 动态图时冻结 Interrupt"不是一回事。
- **性质**：刻意的（热路径追求"不被阻塞"），但要知道"L2 动态图里写了 interrupt 不会生效"。

### D6 · `stableRound` 幂等的隐藏前提：输入必须不变 〔隐患-并发〕

planner 靠 `stableRound` 保证重放派生**相同节点 ID**（防 fork 出重复分支）。但成立前提是**两次 quantum 之间前驱输出不变**。

- **坑**：若某前驱输出在两次 quantum 之间被**并发更新**（多 agent / 多 session 触碰同一 L2 图），"稳定"推导可能派生**不同子节点**，稳定假设被破坏。
- **建议**：多 agent 并发改同一 session 图前，加前驱输出的**一致性校验 / 版本戳**，否则 stableRound 的"幂等"是脆的。

---

## 三、共因（三个"静默提前交差"是一回事）

Q/D 里最危险的一类是 **"任务看着完成了，其实没收好"且无告警**：

| 触发 | 表现 | 现状 |
|---|---|---|
| D1 深度掐断（maxPlanDepth） | 第 10 轮强制出 answer | `ForcedAnswers` canary 计数 + 一条 Warn 日志（`planner_cognition.go:266`），**无面板/告警规则** |
| D2 L1 全拦 | grown==0 强制出 answer | 出正常 COMPLETED，**不标记成因** |
| Q3 有损续跑 | 换人丢了推理链 | 静默，**答案可能偏** |

**共同建议**：出 answer 时**统一带上"成因标签"**（正常 / 深度掐断 / L1 拦截 / 降级输入 / 有损续跑），并在面板/日志对"非正常成因"打告警。这是把三处静默缺陷一次性治理的方向。

---

## 四、性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| Q1 想/做跨量子延迟 | 取舍 | 无需修，知边界 |
| Q2 小步烧 token | 取舍 | 靠 agent_budget 调 |
| **Q3 换人有损续跑** | 缺陷 | 高（上下文依赖型任务） |
| Q4 抢占粗/串行 | 取舍 | 架构级，不改 |
| Q5 map 别名 | 隐患(已修) | 后续改 checkpoint 时盯 |
| **D1 maxPlanDepth 静默** | 缺陷 | 高（补面板/告警规则；已有 Warn+计数） |
| **D2 L1 全拦静默** | 缺陷 | 高（答案带成因标签） |
| **D3 Reconcile 全量拿锁** | 隐患 | 中（规模化前优化） |
| D4 事件至少一次 | 取舍 | 知边界 |
| D5 L2 冻结 HITL | 已知边界 | 知即可（与 task 层 gate 区分） |
| D6 stableRound 前提 | 隐患 | 中（并发前加校验） |
