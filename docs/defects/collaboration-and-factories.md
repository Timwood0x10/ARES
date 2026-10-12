# 缺陷与边界记录 · 协作（IPC）/ 任务工厂 / agent 工厂

> 本文档**只记"会咬你"的坑与已知边界**。三个模块强耦合（协作会触发新任务、
> spawn 会建新 agent），所以合在一份里，分 I / T / A 三节。
> 每条带源码锚点（v0.3.3 行号可能随 dev 分支漂移，定位以符号为准）。
> 性质标记：`缺陷`（建议修）/ `隐患`（规模/并发下暴露）/ `取舍`（设计权衡，知边界即可）/ `已做`（有保护，确认非缺陷）。
>
> **一句话总纲**：协作（IPC）是**单进程内存总线**、任务/agent 工厂是**"先建后跑"**，
> 都够用；坑集中在 —— **①协作是"触发"不是"对话"（回答不回写）**、
> **②依赖/能力不做存在性校验（悬挂引用静默卡死）**、
> **③资源 quota 是 claim/release（漏释放 = 池子假死）**。

---

## I · 协作（IPC，`internal/agentipc` + `internal/agentsyscall`）

**前提共识**：IPC 总线（`Bus`）是**进程内**的消息中枢，提供 Send / Request / Reply /
Delegate / Handoff / Subscribe 六原语。`ask_agent` 是 LLM 能调的"跨 agent 提问"工具。

### I1 · 协作是"触发"，不是"对话"：回答不回写 asker 〔缺陷 · 核心〕

`ask_agent` 是 **fire-and-forget**（C-1 死锁修复）：`ErrAskAgentYielding`（`syscall.go:141`）
→ asker 的量子**不阻塞**、立即返 `{Accepted:true, Status:"pending"}`。
**但回答没有回写回 asker 的认知状态或 task payload**（`syscall.go:120` 注释明说：
"A future iteration (C-1-a)... not written back to the asker"）。

- **坑**：LLM 问完"你验一下"，**拿到的永远是 pending**，答案它**看不到**——除非答案恰好
  被下一轮 drain 的某个量子从 fabric 里读到（但那需要额外设计，现在没有）。
- **后果**："协作"实际只实现了"**触发对方干一件事**"，没实现"**等对方回话再决策**"。
  强依赖"问→答→据答再决定"的任务会卡在半空。
- **建议**：落 C-1-a（把回答写进 asker 的 task payload / checkpoint，下一量子自然读到），
  或在工具描述里**明确告知 LLM"这是问出去、拿不到答"**,别让它误以为能等回话。

### I2 · 去重窗口 30s，但窗口外重问会"再触发一次" 〔取舍 + 隐患〕

`askDedupeWindow = 30s`（`syscall.go:546`）：同一问法（同 caller/target/topic/payload）
**30s 内且仍 pending** → 吞掉、返 `duplicate:true`（防"retry 放大器"，`syscall.go:162`）。

- **代价**：30s 是**硬窗口**——目标 agent 需要 >30s 才答完，且 asker 第 31s 再问一遍，
  **会被当成新协作重新触发**（因为上一个 30s 已过期，dedupe 不再命中）。
- **含义**：慢回答场景下，dedupe **保不住**"等上一个答完"，反而在 30s 边界产生重复触发。
  这是"火即忘 + 30s 记忆"的固有边界，不是 bug，但要知道**慢协作会踩到**。

### I3 · 单进程 / 单租户边界 〔取舍 · 明确边界〕

`primitives.go` 包注释明标："单租户、单进程 v1 部署... 这不是跨信任域可用的性质。"

- **含义**：Bus 是**进程内 goroutine 通信**，不是 P2P 网络。多节点部署时 **Bus 不跨节点**
  （agent 在不同进程就互相 ask 不到）。"协作"目前只在**单进程内**成立。
- **建议**：要跨节点协作，Bus 之上得加一层（消息走共享 EventStore / PG，而非内存 channel）；
  现在没有。文档/README 要写清"协作 = 单进程内"。

### I4 · dead-letter 是有界内存，重启即失 〔隐患〕

`Bus` 的 `DeadLetterStore` 是**有界的**（`bus.go:72/91`，`NewDeadLetterStore(0)`），
且**在进程内存里**（`collaboration_observer.go`："recorded delivery FAILURES into the
dead-letter"）。

- **坑**：① 满载时死信被**驱逐**（有界）；② **进程重启 = 所有死信全丢**（非持久）。
- **后果**：想知道"哪些协作请求从没被处理/失败了"，只在**当次进程活着**时能查，
  重启后无痕迹。排障"某 agent 一直收不到消息"时，死信这条路**跨重启不可审计**。
- **建议**：若要可审计，把 dead-letter 落到 EventStore（append-only）而非进程内存。

### I5 · Unregister 摘除 agent = 其 in-flight 请求"立即失败"而非"等待" 〔取舍〕

`Bus.Unregister`（`bus.go:146`）："Pending requests to this agent will fail with
`ErrAgentNotRegistered`"（`bus.go:40`）。

- **含义**：agent 被 Kill/Retire → 从 Bus 摘除 → **发给它还没完成的请求直接判失败**，
  不等它"把手头的事干完"。对一个"正在算到一半被摘"的 agent，**半路的结果丢弃**。
- **建议**：若协作请求有"可中断性"要求，摘除前要 drain in-flight（或给 in-flight 一个
  短 grace 期）；现在是无 grace。

---

## T · 任务工厂（`taskfabric.Create` / `CompilePlan`）

**前提共识**：任务工厂负责把"任务 + 依赖边"建进 fabric。`Create`（`fabric_lifecycle.go:20`）
建单任务，`CompilePlan`（`workflow_plan.go:121`）批量建一张 DAG。

### T1 · LLM 扩编路径不校验"依赖是否存在" → 悬挂引用静默卡死 〔缺陷 · 最该修〕

**先分清两条建任务的路径**（这决定缺陷到底在谁身上）：

| 路径 | 入口 | 依赖存在性校验 | 环检测 |
|---|---|---|---|
| **operator 提交协作图** | `POST /api/graphs` → `CompilePlan` | ✅ `resolveDependencies`（`workflow_plan.go:34`） | ✅ `detectPlanCycle`（`:38`） |
| **LLM 运行时扩编** | `create_task`/`create_plan` → **裸 `k.fabric.Create`**（`syscall.go:475/721`） | ❌ 只 copy slice（`fabric_lifecycle.go:53`） | ❌ 无 |

- **坑（只发生在 LLM 路径）**：`Create` 对 `Dependencies` **只 copy slice、不校验被依赖的
  task 是否真的在 fabric 里**。LLM 在量子里"我造个子任务 X，它依赖 Y"，若 **Y 拼错 /
  没建成 / 已被 GC** → X 的依赖门**永远不满足** → X **永远不 READY**，**无任何报错/告警**
  （"依赖不存在"和"依赖还没跑完"在 gate 里长得一样）。
- **后果**：LLM 侧一个悬挂依赖能把**整条下游链**永久卡死在等待，且静默。比 maxPlanDepth
  （D1）**更隐蔽**的"任务永远起不来"。
- **注意**：**operator 走 `CompilePlan` 的路径是安全的**（有 `resolveDependencies`）——所以
  "任务工厂整体不校验"是**错的**，缺陷精确在**"LLM 直连 `Create` 这条路"**。
- **建议**：给 LLM 的 `create_task`/`create_plan`（`syscall.go`）**补依赖存在性校验**（复用
  `resolveDependencies` 那套，对"依赖已存在的 task ID"做 `existing` 检查，不存在 → 拒绝/告警），
  或在 sweep 时对"依赖 N 分钟仍全未满足"打 `unresolvable_dependency` 指标。

### T2 · LLM 扩编路径无环检测 → 依赖成环永不 READY 〔缺陷〕

同样分两条路：**operator 的 `CompilePlan` 有环检测**（`detectPlanCycle`，DFS 三色，
`workflow_plan.go:38`），但 **LLM 的 `create_task` 逐个裸 `Create`（`syscall.go:721` 循环调
`CreateTask`）不环检测**。

- **坑（只发生在 LLM 路径）**：A 依赖 B、B 依赖 A（或三角环）经 LLM 逐个 `Create` 进来 →
  三者**永远到不了"依赖全完成"** → 永远不 READY，静默。
- **为什么 LLM 路径更容易出环**：operator 提交的是**一次性定好的 DAG**（编译时就能查），
  LLM 是**边跑边长节点**、多量子多次 `Create`，环可能是**跨量子分次长出来的**——单次
  `Create` 的局部视角根本看不到"全图已闭合"。
- **建议**：LLM 扩编路径在每次 `create_task` 后做一次**增量环检查**（或至少"目标能否被
  拓扑完成"的可达性探测），把环挡在建图时而非留给运行时。

### T3 · "依赖 gate 全靠 ReadyTasks 过滤，Acquire 不查依赖" 〔取舍 · 需配套告警〕

`Acquire` **故意不查依赖**（`fabric_lifecycle.go:111` "Dependencies are deliberately NOT
checked here — 'what may run' is [gated elsewhere by] dependencySatisfied"）。这是好设计
（Acquire 只做 ownership claim，依赖 gate 归 `ReadyTasks`/`ResumableTasks`）。

- **但配合 T1/T2（LLM 路径）的副作用**：正因为 Acquire 不查，一个 LLM 造出的悬挂/成环依赖
  **不会被 Acquire 拦下报错**，而是**任务永远进不了 ReadyTasks** → 表现为"任务卡着没人领"。
- **建议**：这本身不用改，但要**给"卡住的任务"配可观测**（T1 的告警）——否则"好设计"会被
  T1/T2 的静默缺陷拖累成"任务莫名不动"。

> **更正（重要）**：任务工厂**并非"整体不校验"**——operator 的 `CompilePlan` 路径有
> `resolveDependencies`（依赖存在性）+ `detectPlanCycle`（环检测）。缺陷**精确在"LLM 直连
> `Create` 这条路"（`syscall.go:475/721`）绕过了这两个校验**。不要把 T1/T2 理解成"任务工厂缺
> 环检测"，而是"LLM 运行时扩编这条旁路没复用 CompilePlan 的校验"。

### T4 · 协作图（`collab-`）GC 与悬挂依赖的交互 〔隐患〕

提交的协作图（`collab-<runID>-<node>` 前缀）跑完由**后台 GC** 清理（`agent_routes_tasks.go`），
"正在跑的受保护不碰"。

- **坑**：GC 清掉已完成的协作图节点后，若有**别的任务依赖了某个被 GC 的 collab 节点 ID**，
  又踩到 T1（依赖目标已消失）→ 悬挂。GC 的"清理"和依赖的"引用"之间**没有保护**。
- **建议**：GC 删除前**确认无其他任务依赖它**（或有引用计数），否则"图被清理"会把别家任务变悬挂。

---

## A · agent 工厂（`agentfabric.Spawn` / quota）

**前提共识**：agent 工厂（`Spawn`，`lifecycle.go:83`）负责建 agent 进池；
LLM 通过 `spawn_agent`（`agentsyscall`）触发，或 operator/内核预建。

### A1 · `Spawn` 只建不派活 〔取舍〕

`Spawn`（`lifecycle.go:73` 注释）："Spawn does **NOT schedule** the new agent"。

- **含义**：spawn 出来的 agent 进池 = `IDLE`，但要等**下次 drain** 才可能领到任务。
  "我 spawn 了个 specialist 想让它马上干活" 是有**一次调度延迟**的。
- 配合 I1（协作是触发不等待）：**"问一个刚 spawn 的 agent 一个问题"基本拿不到即时响应**
  （它可能还没被 drain 起来 + 就算起来了回答也不回写）。

### A2 · 能力为空的 agent = "能管不能跑" 〔隐患 · 易配错〕

`Spawn` 允许 `Capabilities` 为空 → 注释（`lifecycle.go:121/131`）："Without a cognitive
capability, the spawned agent has **no execution capability** and cannot run a quantum
(it can still be managed by [lifecycle])"。

- **坑**：误配（LLM 调 `spawn_agent` 没带能力 / 带错能力名）→ 建出一个**永远排不上调度**的
  agent（`CapabilityOverlap=0`，score 恒 0）。它**占着池子 quota 但不干活**，且**不报错**。
- **建议**：spawn 时**非空校验能力**（有 parent 时注释已要求 "non-empty capabilities when a
  parent is present"，但 top-level spawn 似乎可空）；或对"建成但 0 能力"的 agent 打 warn。

### A3 · 资源 quota 是 claim/release，漏释放 = 池子假死 〔隐患〕

`Spawn` 在创建前 **claim** 资源配额，`kill/retire` 时 **release**（`lifecycle.go:72`
"the claim is recorded and released on kill/retire"；`ErrResourceQuotaExceeded`，`:117`）。

- **坑**：如果某 agent **既没被 Kill 也没被 Retire**（卡住 / 复活路径 / 异常分支），
  它的 quota **不释放** → 池子"满了" → 后续所有 Spawn 撞 `ErrResourceQuotaExceeded`，
  **agent 池假死**（明明没那么多 agent 在跑，但 quota 额度没回来）。
- **建议**：给 quota claim 加**TTL / 孤儿回收**（周期扫描"引用了 quota 但 agent 已不在池"的
  泄漏 claim 并回收），别只靠 kill/retire 两个显式出口。

### A4 · `Retire` 不 kill 子 agent（invariant #1）→ 可能累积孤儿 〔取舍 · 需意识〕

`Retire`（`lifecycle.go:258` "does **NOT kill its children** (invariant #1:
parent death ≠ child death)"）——父 agent 退役，子 agent **继续活着**。

- **性质**：这是**特性**（因果/溯源要保留，子不该因父死而死），但副作用是：
  反复 spawn→retire 父 agent 时，**子 agent 会累积**（没人退它们），池子逐渐堆
  "孤儿 agent"，占 quota（接 A3）。
- **建议**：retire 时至少**标记**子 agent 的 parent 已 gone（`f.children` 保留溯源，但加个
  "parent retired" 提示），或让 reaper 对"父已 gone 且长期无任务"的子做**回收**。

### 已做（确认非缺陷）

| 保护 | 现状 | 锚点 |
|---|---|---|
| **operator 建图已做依赖存在性校验** | `CompilePlan` → `resolveDependencies` | `workflow_plan.go:34` |
| **operator 建图已做环检测** | `CompilePlan` → `detectPlanCycle`（DFS 三色） | `workflow_plan.go:38` |
| spawn 前校验 spec（重复 ID / 空能力 / 非法） | `ErrAgentExists`/`ErrInvalidSpawnSpec` | `lifecycle.go:82` |
| quota claim 记录 + kill/retire 释放 | 防"无限 spawn 撑爆" | `lifecycle.go:72` |
| L2 能力校验（spawn 只出 L2 能力） | 防"造出永久空转的 agent" | `agentsyscall` spawn 门 |

> 结论：**"任务工厂缺校验"只在 LLM 直连 `Create` 这条旁路上成立**（T1/T2）；
> operator 经 `CompilePlan` 提交协作图的路径是**有保护的**。修 T1/T2 的正确姿势是
> **让 LLM 扩编复用 `CompilePlan` 的校验（或等价增量校验）**，而不是"给任务工厂加环检测"
> （它已经有了）。

---

## 三节共因

```
协作是"触发"非"对话"（I1）
   + 触发会产生新任务（T1/T2：LLM 直连 Create 绕过 CompilePlan 的依赖/环校验 → 悬挂/成环静默卡死）
   + 触发会 spawn 新 agent（A2 空能力 / A3 quota 泄漏 / A4 孤儿累积）
```

**真正的一类根因**：三个模块都是**"引用式建/连"**（依赖 ID、spawn、协作目标），
但"被引用的东西到底存不存在 / 还活没活 / 有没有被清掉"的**一致性校验，在 operator 路径
（CompilePlan）有，在 LLM 运行时直连路径（裸 `Create`）没有**。所以最该补的是一族
**"引用完整性"守卫**——重点是把 operator 路径已有的校验（`resolveDependencies` +
`detectPlanCycle`）**移植到 LLM 扩编路径**：
- T1 依赖存在性校验（LLM `create_task`）
- T2 增量环检测（LLM 跨量子长图）
- T4 GC 前引用计数
- A3 quota 孤儿回收 + TTL
- I4 dead-letter 落持久（可审计）

**修好这一族，"协作/工厂"的静默卡死就没了**——比逐个补单点更根本。

## 性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| **I1 ask 不回写** | 缺陷 | 高（协作"对话"缺半截） |
| **T1 悬挂依赖** | 缺陷 | 高（静默卡死整链） |
| **T2 依赖成环** | 缺陷 | 高（`CompilePlan` 加拓扑校验） |
| A3 quota 泄漏假死 | 隐患 | 中（加 TTL/孤儿回收） |
| A4 孤儿 agent 累积 | 取舍 | 中（reaper 回收） |
| I4 dead-letter 非持久 | 隐患 | 中（落 EventStore） |
| T4 GC 悬挂 | 隐患 | 中（GC 前引用计数） |
| A2 空能力 agent | 隐患 | 低（spawn 校验 + warn） |
| I2 去重窗口 / I5 Unregister / A1 只建不派 | 取舍 | 知边界 |
| 单进程边界 I3 | 取舍 | 知即可 |
