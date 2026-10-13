# 缺陷与边界记录 · 恢复 / 容错（agent 失败 + task 失败 + 调度恢复）

> 本文档**只记"会咬你"的坑与已知边界**，不重复"恢复怎么工作"（lease 过期→requeue→换人续跑；
> agent 挂了→原地复活）。
> 前提共识（前面几份已覆盖）：
> - **agent 层容错**（一直有）：`lease` 过期 → `RequeueExpiredLeases` → 换人从 checkpoint 续；
>   agent 崩 → `RestartAgent` 用 death snapshot 同 ID 原地复活（lifetime budget + 指数 backoff）。
> - **task 层容错**（0.3.3 新增）：`classifyFailure` 判可不可重试 + retry backoff +
>   `AllowPartial`/`DegradedInputs` 降级 + `Task.Deadline` 绝对截止。
>
> 每条带源码锚点（v0.3.3 行号可能随 dev 分支漂移，定位以符号为准）。
> 性质标记：`缺陷`（建议修）/ `隐患`（规模/并发/时序下暴露）/ `取舍`（设计权衡，知边界即可）/ `已做`（有保护，确认非缺陷）。
>
> **一句话总纲**：恢复链的**骨架是对的**（sweep 顺序 deadline→approval→lease、30s 单次超时防堆叠、
> lifetime 累积预算防循环、stale-winner 即时 reclaim），
> 但坑集中在 —— **①`classifyFailure`"默认可重试"太宽（真·永久错白烧预算）**、
> **②lease 过期靠 1s 轮询不是事件（恢复有粒度延迟）**、
> **③requeue 是"必要非充分"（没活人接，任务就静默坐着）**、
> **④两个预算（agent 复活 / task 重试）互不知晓**。

---

## 一、task 层（`failure.go` + `Task` 新字段）

### T-F1 · "默认可重试"边界太宽 → 真·永久错白烧重试预算 〔缺陷 · 最该修〕

`classifyFailure`（`failure.go:77`）的判定顺序（注释 `:66-76` 写死）：

```
1. 显式标记赢：MarkPermanent / MarkRetryable（产生错误的那层最懂）
2. ErrTaskDeadlineExceeded → 终态（过了 deadline 别烧重试）
3. 其余一律 retryable  ← "exactly 0.3.2's behaviour"
```

**坑（第 3 条）**：**任何没被显式 `MarkPermanent` 的错都当可重试**。注释原话
"a misclassification costs a retry rather than a task"——**这是对的（对瞬态故障），
但对"真·永久性错误"是错的**：

- 一个"**任务引用了个已经不存在的工具**""**输入非法（4xx）**""**上游返回 404**"这类
  **重试一万次也不会好**的错误，只要 executor **忘了 `MarkPermanent`**，就会**白烧满
  `RetryPolicy.MaxRetries` 次**（每次还走 backoff，见 T-F2）才终态。
- **责任在"产生错误的那层要记得 MarkPermanent"**——这是**约定而非强制**，漏一处就退化成
  "永久错也傻重试"。
- **和 G1（GA 往糊弄漂）同族**：G1 是"优化目标缺正确率"，T-F1 是"重试判定缺永久错识别"。
  两个都是"**默认偏向乐观、代价由漏配的一方承担**"。

> **更正（v0.3.3 实测）**：识别**不是完全没有**。`markPermanentStepCause`
> （`internal/kernel/scheduler_quantum.go:45-50`，接线在 `:129`）已把
> `apperrors.ErrInvalidArgument`（"输入非法"类，含 LLM 超长 prompt，见 `internal/llm/generate.go:42-44`）
> **自动升为 `MarkPermanent`**，量子收尾即终态、不烧重试预算（测试
> `internal/kernel/permanent_cause_test.go` 锁定）。它的注释自述这是**后来**补的生产入口
> （"`MarkPermanent` had zero production callers"）。
> 所以本条的**准确表述**是：**"输入非法（InvalidArgument）已被覆盖；但'引用不存在的工具 / 上游 404'
> 这类仍未被识别，忘 `MarkPermanent` 就白烧满预算"**，而非"没有任何永久错识别"。

**建议**：给 `classifyFailure` 加**已知永久错的识别**（如 4xx 类、"引用不存在"、
`context.Canceled` 的特定子类），或至少**在"重试次数 > N 仍未好"时自动升级告警**
（"这个任务重试了 5 次还挂，八成是永久错，别再烧了"），别静默烧满预算。

### T-F2 · bare `context.DeadlineExceeded` 保持可重试（有意，但要知道代价） 〔取舍〕

`failure.go:13-15` 注释明说："a bare `context.DeadlineExceeded` from a single LLM or tool
call is usually a **transient** timeout, so it keeps the legacy retry behaviour — classifying
it as permanent would silently retire retry budget"。

- **性质**：**有意为之**——单个 LLM/工具调用超时通常可重试；只有**任务级** `Task.Deadline`
  （`ErrTaskDeadlineExceeded`）才是终态。
- **代价/边界**：如果某任务**反复**卡在同一个 LLM 调用超时（比如某 provider 一直 30s 超时），
  每次 bare deadline 都被当"可重试"→ 烧满 MaxRetries + backoff 才终态。
  这是"保守重试"的固有代价，不是 bug，但**provider 持续超时 + 高 MaxRetries = 慢吞吞失败**。
- **配合 §LLM 失败切换（§27）**：若 LLM failover 也切不动（所有 provider 429 冷却），
  任务侧还会再套一层 task retry/backoff——**两层重试叠加**，故障时恢复很慢。

### T-F3 · 两个预算（agent 复活 / task 重试）互不知晓 〔隐患〕

- **agent 复活预算**（`RestartPolicy`，lifetime 累积，`recovery.go:42`）：一个 agent 一生 5 次。
- **task 重试预算**（`RetryPolicy.MaxRetries`，`task.go`）：一个任务最多重试 N 次。
- **坑**：两者是**独立计数器**。一个**又挂 agent 又做错的活任务**：
  - agent 挂了 → 吃 agent 复活预算（或 requeue 换人不吃）；
  - 换人的新 agent 又把任务做错 → 吃 task 重试预算；
  - **两个预算各自耗尽，系统抛出两个不同的 "exhausted"，没有任何"这任务+这 agent 组合
    已经反复失败"的联合视图** → 排障时看到 "recovery_exhausted" + "task retry exhausted"
    两个码，得自己拼"哦是同一个活反复出事"。
- **建议**：加一个**任务级"故障计数"维度**（这个 task 在 N 次重试里触发了多少次 agent 故障），
    让"反复失败的活"能被单独识别/告警，而非两个预算各报各的。

---

## 二、agent 层 + 调度恢复（`recovery.go` + `kernel_loop.go` + `scheduler_recovery.go`）

### A-F1 · lease 过期靠 **1s 轮询**，不是事件 → 恢复有粒度延迟 〔隐患〕

`kernel_loop.go:16` 注释明说："**lease expiry is detected by a sweep, not by [events]**"，
默认 `recoverySweepInterval = 1s`（`:19`）。

- **含义**：agent 真死了（进程没了，不是 chaos 显式 Kill），它的任务要等
  **lease TTL 到期 + 最多 1s 的 sweep** 才被 requeue。
- **对比**：
  - chaos **显式 Kill** → 有 death snapshot → 可**即时** `RestartAgent`（不走 TTL）；
  - 普通**进程死亡**（无 snapshot 路径命中）→ 只能等 **TTL + 1s sweep**。
- **坑**：把"agent 挂了多久任务才重新动起来"估成"TTL"是**低估**了——实际是 **TTL + 一轮
  sweep（≤1s）**。排障"为什么这个任务卡了 5 分钟"时要算上这个 sweep 粒度，别只看 TTL。
- **已做**：`recoverySweepTimeout = 30s`（`:24`）限住单次 sweep——**慢存储不会让 sweep 堆叠**
  （一个 sweep 卡 30s 就放弃，下一轮重来）。这条是**好的**，别当成缺陷。

### A-F2 · stale-winner 只救"当前正在 drain 的那个"任务 〔取舍 + 隐患〕

stale-winner 恢复分两处：**hint 接线**在 `scheduler_recovery.go`（`WithRecoveryHint`/`notifyRecovery`/`hasRecoveryHint`，`:19/28/42`），**真正的 reclaim 逻辑 `handleStaleWinner` 在 `internal/kernel/scheduler_execute.go:144-174`**（初稿把它归到 `scheduler_recovery.go`，是错的）：
- 触发点在 **scheduler drain 某个任务时**发现"winner 没活 executor"→ `handleStaleWinner`
  即时 `ReleaseReason(..., agentfabric.StopUnfit.String())`（`scheduler_execute.go:146/158`；
  `StopUnfit = "capability_unfit"`，`stopreason.go:61`）（见 §4.3 那份）。
- **坑（边界）**：这是"**per-drained-task**"的即时救——**只救"恰好这一轮 drain 到的任务"**。
  一个 winner 死了、但它的任务**此刻没被 drain 到**（不是最高优）→ **不会即时 reclaim**，
  仍然**等 lease TTL + sweep（A-F1）**。
- **配合 §4.3**：`StopUnfit` 即时 reclaim 是"热路径顺带做的"，**不是"死 agent 的专用回收器"**；
  真正的兜底还是 sweep。**别把即时 reclaim 理解成"agent 一死任务立刻被接走"——只有正在被
  调度的任务才即时，其余等 TTL+sweep。**

### A-F3 · requeue 是"必要非充分"：没活人接，任务静默坐着 〔隐患 · 需可观测〕

`RequeueExpiredLeases`（`recovery.go:171` = `CheckExpiredLeases`）只做一件事：**把过期任务
回 READY**。它**不负责"谁来做"**。

- **坑**：agent 死了 → requeue → 但**此时所有 agent 都超预算**（`StopBudgetExhausted`/
  `StopDeadlineExceeded`，见 §4.3 `filterBudgetAffordable`）→ 任务回 READY 后**没人能接**
  （drain 撞 `ErrNoAffordableCandidate`）→ **任务静默坐在 READY**，既没做完也没告警说
  "requeue 了但没活人接"。
- **性质**：这不是 bug（requeue 本就该只管"回队"），但**"requeue 成功"≠"任务会前进"**。
  两个 sentinel（§4.3：`NoCapable` vs `NoAffordable`）就是用来分辨"没能力的人"和"全超预算"
  的——**但 requeue 这条日志本身不知道下游会撞哪个**。
- **建议**：requeue 后**紧跟一次 drain 试探**（或至少记一条"requeued but N capable agents
  all inadmissible"的关联日志），别让"requeue 了"给人"恢复了"的错觉。

> **更正（v0.3.3 实测）**："完全静默"要打折扣：`runSweepChain` 在 requeue 后**有**日志
> `"requeued expired task(s)"`（`kernel_loop.go:277-279`），并**紧跟** `bindReplacements(requeued)`
> （`:280`）——peer 模式下会探测 `hasCapableExecutor` 并给任务绑 replacement executor
> （`:369-435`）。所以"requeue 后紧跟一次试探"这条建议**已部分实现**（只是不是 drain 试探）。
> 缺陷内核仍成立：**"没活人接"这件事本身没有单独告警**，日志不知道下游会撞 `NoCapable` 还是 `NoAffordable`。

### A-F4 · checkpoint 续跑是有损的（接 A1/Q3） 〔缺陷 · 跨文档〕

`RecoverTaskCheckpoint`（`recovery.go:222`）让 replacement **从 checkpoint 续**，但 checkpoint
**没有 LLM 推理链**（同 §disposable-agent A1、§dynamic-dag Q3）。

- **更正（v0.3.3 实测）**：checkpoint **并非只有 `StepCheckpoint`**——`CheckpointEnvelope`
  还带 `UserProfile/Payload/UsedExperienceID/StrategyID/SessionID/InputTokens/OutputTokens/
  TenantID/LastError`（`internal/fabric/task/checkpoint_schema.go:67-133`）。所以"只有 StepCheckpoint"
  的措辞不准；**但对的部分是**：整份 envelope 里**没有任何 reasoning 字段**，恢复的进度内容即
  `StepCheckpoint`，所以"续的是进度不是思路"成立。

- **坑**：agent 层"换人续跑"这条最常用路径，**续的是"进度"不是"思路"**。对"强依赖推理链"的
  任务，换人后可能**走偏**（前驱输出在，但"为什么这么排"丢了）。
- **建议**：同 A1/Q3——**把关键推理步骤结构化进认知/checkpoint**，别让它默认有损。
- **核实（2026-10-12）**：`RecoverTaskCheckpoint` 实为 **TEST/CHAOS-ONLY**（`cmd/` 无调用方），
  envelope 亦确认无 reasoning 字段。**待拍板方案 A/B** 及判断见 `disposable-agent.md` A1「核实 /
  修复方案」段。

---

## 三、sweep 顺序（确认对的，别误判成缺陷）

`runSweepChain`（`kernel_loop.go`）顺序是**刻意的**（每步注释都标了）：

```
1. ExpireTaskDeadlines   ← 先判"该死的"（过了绝对 deadline）
2. ExpireApprovals       ← R1/R2：approval 超时自动拒（R2: 让 sweep log 诚实）
3. RequeueExpiredLeases  ← 最后才 requeue（把前面两步放行的任务回队）
4. bindReplacements      ← 给 requeue 的绑 replacement executor
```

- **为什么 deadline 在 lease 之前**（`recovery.go:177` "a task past its deadline must stop
  (Agent death is a …)"）：一个**既过 deadline 又 lease 过期**的任务，若先 requeue 它会
  "复活"到 READY——**然后下一步又判它过 deadline**，等于白忙 + 状态抖动。**先判死再 requeue**
  是对的。
- **为什么 approval 在 lease 之前**（R2）：pending approval 任务**没有 lease**（它停在 READY
  等人，不 Acquire），lease requeue 会**跳过它**；单独 `ExpireApprovals` 才处理它，且让
  "自动拒了几个门"进 sweep 日志。
- **结论**：sweep 顺序是**对的设计**，列在这里是**防止读者把"deadline 先于 lease"误当 bug**。

---

## 四、已做（确认非缺陷，列出以防误判）

| 保护 | 现状 | 锚点 |
|---|---|---|
| **sweep 顺序 deadline→approval→lease** | 先判死再 requeue，防状态抖动 | `kernel_loop.go` runSweepChain |
| **单次 sweep 30s 超时** | 慢存储不堆叠 sweep | `kernel_loop.go:24` |
| **agent 复活预算 lifetime 累积 + 原子预留** | 防"反复挂循环"（成功不回退计数） | `recovery.go:42/289` |
| **指数 backoff 防风暴** | 1s/2s/4s…30s cap，防 crash-restart storm | `recovery.go` RestartPolicy |
| **stale-winner 即时 reclaim（热路径）** | 正在 drain 的活，winner 没 executor 立即回 READY（不等 TTL） | `scheduler_recovery.go` + §4.3 |
| **recovery-bound executor 不被 reconcile 误删** | replacement 故意活在 fabric 外，terminal 才摘 | `scheduler_recovery.go:72` |
| **checkpoint 持久（跨重启）** | 重启后 requeue 的活能从 checkpoint 续 | `recovery.go` RecoverTaskCheckpoint |
| **lease + epoch fencing 唯一写者** | 僵尸 holder 的写被 `ErrEpochMismatch` 拒 | 定义 `internal/fabric/task/fabric.go:22`，拒写 `ownerLocked` `fabric.go:367-377` |

---

## 五、性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| **T-F1 classifyFailure 默认可重试太宽** | 缺陷 | 高（加永久错识别 / 重试验证告警） |
| A-F3 requeue 必要非充分（没活人接静默坐） | 隐患 | 中（requeue 后关联 drain 结果日志） |
| T-F3 两个预算互不知晓 | 隐患 | 中（加任务级联合故障计数） |
| A-F1 lease 过期靠 1s sweep（非事件） | 隐患 | 低（知边界；TTL+≤1s） |
| A-F2 stale-winner 只救当前 drain 的 | 取舍 | 知即可（其余等 TTL+sweep） |
| A-F4 checkpoint 续跑有损 | 缺陷 | 高（同 A1/Q3，认知结构化） |
| T-F2 bare deadline 保守重试 | 取舍 | 知边界（配合 LLM failover 两层叠加） |
| sweep 顺序 / 30s 超时 / 预算 / fencing | ✅ 已做 | 无需动 |

**恢复这块"骨架是对的"，真正的三类洞**：
1. **T-F1 + T-F3**："重试"和"复活"各自乐观，**漏配/联合失败时静默烧预算**——要可观测；
2. **A-F3**：requeue ≠ 恢复，**"没活人接"是静默的**——要关联 drain；
3. **A-F4**：续跑**丢了脑子**（同 A1/Q3，认知持久化的根因）——要结构化。

**面试串法**：
> "恢复我分两层：agent 层（lease 过期 requeue + 原地复活，一直有）+ task 层（0.3.3 加
> classifyFailure/backoff/AllowPartial/deadline）。**骨架对**（sweep 顺序、30s 超时、
> lifetime 预算、stale-winner 即时 reclaim 都在）。**但四个真洞**：
> ① `classifyFailure` **默认可重试**，真·永久错忘 `MarkPermanent` 就白烧满预算；
> ② requeue **只管回队不管谁接**，全 agent 超预算时任务**静默坐着**；
> ③ 两个预算（agent 复活 / task 重试）**各报各的**，没联合视图；
> ④ checkpoint **续的是进度不是思路**（和 agent 可丢弃同根因）。"
