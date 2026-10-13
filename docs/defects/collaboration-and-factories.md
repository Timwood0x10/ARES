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

### T1 · LLM 的 `create_task` 不校验"依赖是否存在" → 悬挂引用静默卡死 〔缺陷 · 最该修〕

**先分清三条建任务的路径**（这决定缺陷到底在谁身上）：

| 路径 | 入口 | 依赖存在性校验 | 环检测 |
|---|---|---|---|
| **operator 提交协作图** | `POST /api/graphs` → `runCollabGraph` → **裸 `k.fabric.Create`**（`agent_routes_tasks.go:645`） | ✅ **自带**边存在性检查（`:611-614`） | ✅ **自带** `findCycle`（Kahn 拓扑，`:617`，定义 `:738`） |
| **LLM `create_task`** | `create_task` → **裸 `k.fabric.Create`**（`syscall.go:489`） | ❌ 只 copy slice（`fabric_lifecycle.go:53`） | ❌ 无 |
| **LLM `create_plan`** | `create_plan` → `k.fabric.CompilePlan`（`plan.go:164`） | ✅ `resolveDependencies`（`workflow_plan.go:154`，定义 `:302`） | ✅ `detectPlanCycle`（`:158`，定义 `:332`） |

- **坑（只发生在 `create_task` 这条路）**：`Create` 对 `Dependencies` **只 copy slice、不校验被依赖的
  task 是否真的在 fabric 里**。LLM 在量子里"我造个子任务 X，它依赖 Y"，若 **Y 拼错 /
  没建成 / 已被 GC** → X 的依赖门**永远不满足** → X **永远不 READY**，**无任何报错/告警**
  （"依赖不存在"和"依赖还没跑完"在 gate 里长得一样）。
- **后果**：LLM 侧一个悬挂依赖能把**整条下游链**永久卡死在等待，且静默。比 maxPlanDepth
  （D1）**更隐蔽**的"任务永远起不来"。
- **注意（重要 · 与初稿不同）**：**`create_plan` 与 operator 两条路都是有保护的**——
  - `create_plan`（`agentsyscall/plan.go:164`）走的是 `k.fabric.CompilePlan`，**有**
    `resolveDependencies`（`:154`）+ `detectPlanCycle`（`:158`）；
  - operator 的 `POST /api/graphs` 走 `runCollabGraph` 的**裸 `Create`**，但它**自己的**边存在性检查
    （`agent_routes_tasks.go:611-614`）+ `findCycle`（`:617`，Kahn）挡住了悬挂/成环——
    **注意它并不经过 `CompilePlan`**（初稿把这条路径归给 `CompilePlan/resolveDependencies/detectPlanCycle`，是错的）。
  - 所以缺陷**精确在 `create_task` 这条"单任务"旁路**上，**不要把 `create_plan` 也算进来**。
- **建议**：给 LLM 的 `create_task`（`syscall.go:489` 之前）**补依赖存在性校验**（复用
  `CompilePlan` 的 `resolveDependencies`，对"依赖已存在的 task ID"做 `existing` 检查，不存在 → 拒绝/告警），
  或在 sweep 时对"依赖 N 分钟仍全未满足"打 `unresolvable_dependency` 指标。

**修复（2026-10-12，已核实缺陷成立 → 已修）**

- **实证（机制被 fabric 自己的注释承认）**：`ReferencedDependencies`（`fabric_deps.go`）写明
  "**depsSatisfiedLocked treats a missing dependency as unsatisfied forever**, so deleting a referenced
  predecessor would strand its dependents permanently"；reaper（`reaper.go`）正是为此跳过被引用任务。
  而 `Create`→`createLocked` 对 `Dependencies` **只 copy slice、零校验**（`fabric_lifecycle.go`）。
  两处合起来 = 悬挂边**永久静默不满足**，与本文描述一致。
- **修法（分层，不破坏 batch）**：`Fabric.Create` 增加**显式严格选项** `WithResolvedDependencies()`
  （`fabric_lifecycle.go`）：拒绝"依赖 id 不存在 / 空 id / 自引用"，语义为
  `ErrDependencyUnresolved`（新哨兵错误，`fabric.go`）；检验与插入**同一临界区**，避免
  "校验通过 → 并发 Delete 依赖 → 边悬挂"的 TOCTOU。`create_task` 启用该选项（`syscall.go`）。
- **为什么默认必须保持宽松（关键约束，勿"顺手统一"）**：`runCollabGraph` 按**提交顺序**建节点
  （`cmd/ares/agent_routes_tasks.go`），图内**前向引用合法** —— 在 `Create` 基础路径上无条件校验会
  打掉它。所以严格性是**每个调用点显式选择**，而非新的默认。
- **未改且非缺陷**：`SetDependencies`（`fabric_deps.go`）**故意**允许未解析 id，供 L2 增量增长
  （`workflow_plan_incremental_test.go:101-102` 把 `"c"` 不存在锁成合法行为）。它是同类静默风险的
  另一个入口，但不是漏 —— 见 T2 残留。
- **测试**：`fabric/task/fabric_dependency_resolution_test.go`（严格模式 6 例 + **锁死默认宽松路径**
  的前向引用行为）、`agentsyscall/create_task_deps_test.go`（悬挂拒绝且 fabric 不留痕 + 合法链式
  依赖仍可用且被 gate）。
- **复核结论（2026-10-12 自审补充）**：
  1. **同轮链式调用不会被误拒**：`growToolNodes` 把一轮内的多个工具节点**串行链式**生长
     （`prev = nodeID`，代码内有显式设计注释），所以"先 `create_task A`、再 `create_task B(依赖 A)`"
     在 B 执行时 A 必已落地。**但**模型无法知道同轮 A 的 id（id 由 Kernel 铸造、只在结果里返回），
     所以真实契约是"**依赖必须来自模型被告知过的 id**"。
  2. **拒绝的爆破半径是"整条下游级联失败"，不是"带洞继续"**：工具报错会**判量子失败**
     （不是把错误当工具结果回给模型）；又因 L2 投影任务**不是** `AllowPartial`，
     `cascadeFailureLocked` 会把下游子图一并标 FAILED（各自记 `FailedDependency`），
     最终由 `internal/agentruntime/await.go` 呈现给调用方。**取舍**：缺陷本是"永久静默卡死",
     换成"**可见失败**"是对的，代价是这一轮分支作废（不静默、也不假装成功）。
  3. **爆破半径已消除（2026-10-12 续修）**：把该拒绝在**模型边界**转成"可读结果"，
     既不判量子失败、也不动 `AllowPartial`/syscall 契约：
     - `CreateTaskResult` 新增 `Rejected`（闭合取值，首个值
       `CreateTaskRejectedUnresolvedDeps = "unresolved_dependencies"`）与 `MissingDependencies`；
     - `BindTools` 的 `create_task` 闭包用 `errors.As` 识别
       `*taskfabric.DependencyResolutionError` 并**返回结果（nil error）**，其余错误照旧返回；
       `Kernel.CreateTask` 本身的错误契约**不变**（程序化调用方仍能区分"没建成"）；
     - 失败原因**一次报全**：fabric 侧改为收集**所有**不可解析 id（新增类型化错误
       `DependencyResolutionError{TaskID, Missing}`，`errors.Is(ErrDependencyUnresolved)` 仍成立），
       因为面向模型重试时"一次只报一个"会每次浪费一个量子；
     - 工具描述与 `dependencies` 参数说明补上规则："依赖必须是更早的 `create_task` 返回过的
       task_id —— id 由服务端铸造，你没拿到过的 id 不存在"（照 `ask_agent` 的 `no-reply` 先例：
       可预期的结果走 result，不走 error）。
     - **测试**：`TestBindToolsCreateTaskSurfacesUnresolvedDependencies`（经 binder 调用：无 error、
       `Rejected` 正确、缺失 id 报全、fabric 不留痕、`Kernel.CreateTask` 仍报错、合法调用仍建任务）
       —— 这正是上一轮自审标出的**覆盖缺口**；fabric 侧新增
       `TestCreate_WithResolvedDependencies_ReportsEveryMissingID`。

### T2 · LLM 的 `create_task` 无环检测 → 依赖成环永不 READY 〔缺陷〕

同样看路径：**`create_plan` 走 `CompilePlan` 有环检测**（`detectPlanCycle`，DFS 三色，
`workflow_plan.go:158/:332`），**operator 协作图有自己的 `findCycle`**（Kahn，
`agent_routes_tasks.go:738`），但 **LLM 的 `create_task` 是裸 `Create`（`syscall.go:489`）**
——**单任务入口根本没有"整图"概念，自然无环检测**。

- **坑（只发生在 `create_task` 路径）**：A 依赖 B、B 依赖 A（或三角环）经 LLM 逐个 `create_task` 进来 →
  三者**永远到不了"依赖全完成"** → 永远不 READY，静默。
- **为什么 `create_task` 路径更容易出环**：operator 提交的是**一次性定好的 DAG**（提交时就能查），
  `create_plan` 也是**一批**一起编译；只有 `create_task` 是**边跑边逐个长节点**、多量子多次调用，
  环可能是**跨量子分次长出来的**——单次 `Create` 的局部视角根本看不到"全图已闭合"。
- **建议**：`create_task` 建图后做一次**增量环检查**（或至少"目标能否被拓扑完成"的可达性探测），
  把环挡在建图时而非留给运行时。

**修复（2026-10-12，已核实 → 由 T1 的修复结构性覆盖，未加独立环检测）**

- **可达性更正（重要）**：本文描述的"跨量子成环"A→B→A **今天确实可达，但前提正是 T1 的缺口**
  （`create_task` 接受指向**尚不存在** id 的边，所以两个节点可以互相指向对方，各自等一个"还不存在"
  的对方）。存在性校验落地后：新节点**每条边都必须指向已存在任务** ⇒ **创建序本身就是拓扑序**，
  经新节点无法闭合任何环。
- **唯一残留形态 = 自引用**（任务把自身 id 写进依赖）：`create_task` 的 id 由 Kernel 铸造，理论上
  只有"猜中未来 id"能命中 —— 已由同一校验**显式拒绝**（`resolvedDependenciesLocked` 检查 `dep == t.ID`）。
- **故未新增独立环检测**：在 T1 修复后，`create_task` 路径上的图遍历是**不可达代码**（规范 §0.2
  禁"空壳/假机制"、§0.4 不做假想路径的机制）。本文原建议的"增量环检查/可达性探测"在此路径被
  存在性校验覆盖。
- **真正需要环检测的是 `SetDependencies`（残留，未修）**：它能给**已存在**任务改边，因此可以闭合环
  （`SetDependencies(X, [Y])` 且 Y 已依赖 X），且它**故意**允许未解析 id（见 T1"未改且非缺陷"）。
  L2 增量增长确实需要"先建边后落节点"的宽松性，但"可闭合环"不是它需要的宽松性 —— 要收紧需先确认
  `planprojection` 协调器的调用顺序（`coordinator.go` 四处调用点），属**新增校验 + 需评估影响面**，
  未在本轮动。

### T3 · "依赖 gate 全靠 ReadyTasks 过滤，Acquire 不查依赖" 〔取舍 · 需配套告警〕

`Acquire` **故意不查依赖**（`fabric_lifecycle.go:111` "Dependencies are deliberately NOT
checked here — 'what may run' is [gated elsewhere by] dependencySatisfied"）。这是好设计
（Acquire 只做 ownership claim，依赖 gate 归 `ReadyTasks`/`ResumableTasks`）。

- **但配合 T1/T2（LLM 路径）的副作用**：正因为 Acquire 不查，一个 LLM 造出的悬挂/成环依赖
  **不会被 Acquire 拦下报错**，而是**任务永远进不了 ReadyTasks** → 表现为"任务卡着没人领"。
- **建议**：这本身不用改，但要**给"卡住的任务"配可观测**（T1 的告警）——否则"好设计"会被
  T1/T2 的静默缺陷拖累成"任务莫名不动"。

> **更正（重要 · 二次修订）**：三条路径的校验现状要分清——
> - **`create_plan`**（`plan.go:164`）走 `k.fabric.CompilePlan`，**有** `resolveDependencies`
>   （`workflow_plan.go:154`）+ `detectPlanCycle`（`:158`）；
> - **operator 协作图**（`agent_routes_tasks.go:645`）走**裸 `Create`**，但**自带**边存在性检查
>   （`:611-614`）+ `findCycle`（`:617`，Kahn）——**它并不经过 `CompilePlan`**；
> - **`create_task`**（`syscall.go:489`）走**裸 `Create`**，**没有任何**依赖存在性/环校验。
>
> 所以 T1/T2 的缺陷**只精确在 `create_task` 这一条**；不要把 `create_plan` / operator 也算进来，
> 也**不要**再说成"operator 走 `CompilePlan` 所以安全"（安全的原因不同：是有自己的 `findCycle`）。

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

`Spawn` 允许 `Capabilities` 为空 → 注释（`lifecycle.go:42-44`，`SpawnSpec.CognitionFactory`）：
"Without a cognitive capability, the spawned agent has **no execution capability** and cannot
run a quantum (it can still be managed by [lifecycle])"。（初稿锚点 `:121/131` 不准；`:131` 是近似注释。）

- **坑**：误配（LLM 调 `spawn_agent` 没带能力 / 带错能力名）→ 建出一个**永远排不上调度**的
  agent（`CapabilityOverlap=0`，score 恒 0）。它**占着池子 quota 但不干活**，且**不报错**。
- **实证（v0.3.3）**：`validateSpawnSpec`（`lifecycle.go:176-188`）**只**校验 `Identity` 含空格
  （→ `ErrInvalidSpawnSpec`），**完全不校验 capabilities**——所以能力为空对**所有** spawn
  （不只有 parent 的）都放行；`lifecycle.go:69` 那句 "non-empty capabilities when a parent is
  present" **只是注释、代码从未强制**。
- **建议**：spawn 时**非空校验能力**；或对"建成但 0 能力"的 agent 打 warn。

### A3 · 资源 quota 是 claim/release，漏释放 = 池子假死 〔隐患〕

`Spawn` 在创建前 **claim** 资源配额，`kill/retire` 时 **release**（`lifecycle.go:72`
"the claim is recorded and released on kill/retire"；`ErrResourceQuotaExceeded`，`:117`）。

- **坑**：如果某 agent **既没被 Kill 也没被 Retire**（卡住 / 复活路径 / 异常分支），
  它的 quota **不释放** → 池子"满了" → 后续所有 Spawn 撞 `ErrResourceQuotaExceeded`，
  **agent 池假死**（明明没那么多 agent 在跑，但 quota 额度没回来）。
- **建议**：给 quota claim 加**TTL / 孤儿回收**（周期扫描"引用了 quota 但 agent 已不在池"的
  泄漏 claim 并回收），别只靠 kill/retire 两个显式出口。

**核实 + 修复（2026-10-13）——症状成立，但根因与本文描述不同（重要）**

1. **计数器在构造上是平衡的，不是记账 bug**：`f.allocated` 只由 `allocateLocked`/`releaseLocked`
   维护；`Spawn` 在**注册 agent 的同一临界区**内 allocate（中间无失败路径），`Kill`（删注册表 +
   release）与 `Retire`（release + 置 RETIRED，幂等）各释放一次。**不存在**"claim 还在、agent 已不在池"
   的状态——所以本文建议里"扫描引用了 quota 但 agent 已不在池的 claim"**打不到目标**。
2. **真机制：生产从不释放**。`Kill` 的调用方只有 `chaos.go` / `sandbox.go` /
   `agent_routes_chaos.go`；`Retire` 的唯一调用方是 `chaos.go`。即**生产只 spawn、永不 kill/retire**
   → 每个 agent 的 claim 终生占着，且全仓无回收（与本文"无孤儿回收/TTL"一致）。
3. **后果按配置分两档**：`resourceBudget` 默认空且仓库内无装配点 → 默认部署不会撞
   `ErrResourceQuotaExceeded`；但一旦运维配上预算（想给池子上限时必然要配），第 N 次 spawn 后
   **永久假死**。**与配额无关的另一半更硬**：`f.agents` 随运行无界增长，每个 agent 自带一份
   Cognition（LLM 客户端/工具束）→ 内存与句柄的持续泄漏，默认配置下同样发生。
4. **同源路径（panic 不 kill）**：`scheduler_execute.go` 的 panic 恢复分支注释写明"the load slot is
   released so the agent stays schedulable"——**刻意保活**、不 kill、配额不释放 → 崩溃过的 agent
   变成"带配额的僵尸"，下次还会被调度再 panic（白烧任务 retry 预算）。

**已实施（A + C）**

- **A · idle 回收 sweep**：`internal/fabric/agent/reclaim.go` 新增 `AgentReclaimPolicy`
  （`IdleTTL` 0=关闭 / `KeepPerCapability`）与 `Fabric.ReclaimIdle`：
  - 只回收 **IDLE + 非 busy + 最后活跃超过窗口**的 agent（新增 `Agent.lastActiveAt`，
    在 `ExecuteStep` 的唯一入口盖戳，时钟取自 fabric 的 `WithClock` 以便测试注入；`SpawnedAt`
    **刻意不用**——"很久以前创建"不等于"现在空闲"）；
  - **能力保底**：不回收任一能力的最后一个持有者，且按 id 确定的贪心顺序决策——
    否则会把"配额泄漏"换成"任务永远没有候选"（正是 A-F3/P2-4 那类静默饥饿）；
  - 退休走**既有 `Retire`**（唯一释放路径、幂等、事件一致），sweep 只是多一个触发器，
    不引入第二套记账；**扫描 + 保底决策 + 活动性复核 + 释放收敛在同一 `f.mu` 临界区**
    （第二轮复核 F2 已改；锁序 f.mu → a.mu，与 `IsIdle` 一致。原文此处写"不嵌锁、先扫后
    unlock 再 Retire"是初版说法，已被 F2 取代，与本节 F2 行一致）。
- **默认开**：`RestartPolicy.IdleAgentTTL` 默认 = `DefaultAgentReclaimPolicy().IdleTTL`（10 分钟，
  保底 1）；0 = 关闭。10 分钟远大于一个量子（秒级）、远小于进程寿命。
- **接线**：`Recovery.ReclaimIdleAgents(ctx)` → `runSweepChain` **最后且无条件**执行
  （原实现在 `requeued == 0` 时提前 return，会让回收被跳过；已改为条件日志 + 继续），
  并把回收数量与 id 打进既有 sweep 日志。
- **C · 去静默**：新增 `Fabric.QuotaSnapshot()`（allocated/budget 快照，拷贝返回）；
  `Spawn` 撞额度时错误信息**带上 `requested/allocated/budget`**（此前是裸错误，读起来像"池子坏了"）。
- **测试**：`reclaim_test.go`（额度返还可再 spawn 的**真回归**、能力保底、跳过活跃/busy、
  0 窗口关闭）+ `reclaim_default_test.go`（锁"默认开"）。

**未做（B，独立小修，未动）**：panic 恢复分支改为 kill（顺带喂 `StopCrashed` 快照给 revival 路径）。
它只治"僵尸"，不治"只 spawn 不 retire"，且牵动 revival 语义，**单列**。

**第二轮复核（2026-10-13，针对回收实现自审）：三条真问题 + 修**

| # | 问题（真） | 证据 | 修法 |
|---|---|---|---|
| F1 | **只还额度、不还内存**：`Retire` 只清 `resources`，**不清 `cognition`** → 我的 sweep 修了"池假死"，但每个退休 agent 的 Cognition（LLM 客户端 + 工具束）**仍被永久引用**，`f.agents` 里的墓碑持续增长 | `lifecycle.go` 的 Retire 正文逐行核对 | `Retire` 拆出 `retireLocked`，在置 RETIRED 的同一 `a.mu` 临界区内**释放 `cognition` 与 `cognitive`**。安全性有据：候选条件要求 `StateIdle && !busy`（`Fabric.IsIdle`），而 Retire 是终态，故退休 agent **永不再执行**；且 `Retire` 本就 `snapshots.clear`，复活被拒（`Recover` 对 RETIRED 有守护） |
| F2 | **决策与退休跨两个临界区（TOCTOU）**：扫描后解锁、再调 `Retire` 重取锁 → 期间任务可被派发到该 agent → **把正在运行的 agent 退休掉，其额度却已放回池子可被新 spawn 领走**（配额超卖） | 原实现 `f.mu.Unlock()` 后才逐个 `Retire` | ① 扫描 + 保底 + `a.mu` 复核活动性 + 释放**全部收敛到同一个 `f.mu` 临界区**（锁序沿用 `IsIdle` 的 f.mu → a.mu）；② 新增 `inUse` 参数：由 `Recovery` 用 task fabric 的 `LeaseSnapshot()`（**只含非终态**租约）构造 owner 集合传入 → **持有活租约者永不回收**，覆盖"已派发、量子未启动"这个 busy 标志看不见的窗口 |
| F3 | **保底基线漏算 inUse 者**（**被我自己新写的测试当场抓到**）：同能力下"一个在用 + 一个空闲"时，空闲者先被处理、看到空基线而**保留**，结果两个都不回收 | `TestReclaimIdle_SkipsAgentsInUse` 首版失败 | 把 inUse 的候选**从基线起就计入**保底（它们确定存活且不会被回收），而非等循环走到才计入 |

- **附带（linter 抓到）**：`retireLocked` 原返回 `(nil, nil)` 表达"已退休"被 `nilnil` 判为语义含糊
  → 改为返回哨兵 `ErrAgentRetired`，公开 `Retire` 仍保持**幂等返回 nil**（契约不变）。
- **测试**：`TestRetireReleasesRuntimeState`（退休后 `ExecuteStep` 必须 `ErrAgentNotExecutable`
  且额度归零，**同时**记录仍在）、`TestReclaimIdle_SkipsAgentsInUse`；既有 `TestRetire*` 三条全绿
  （证明丢弃 cognition 未破坏既有契约）。
**第三轮（2026-10-13，三处残余全部处理）**

| # | 残余 | 结论与修法 |
|---|---|---|
| A | `f.agents` 墓碑无回收器 | **已修**：新增 `Fabric.ReapRetired(ctx, retention)`（`reap.go`，task fabric `reaper.go` 的对称物）+ 策略位 `RetiredAgentRetention`（默认 1h，零值**禁用**=`ReapRetired` 的安全方向）+ sweep 第五步。**状态转移表写进函数 doc**（§11）：`(absent) --Spawn--> IDLE --Retire--> RETIRED --ReapRetired(retention)--> (absent)`；年龄基准是**退休时刻**（`retiredAt`）而非 spawn 时刻，未盖章的墓碑**永不回收**（否则零值会被读成"开天辟地时就退休了"）。`f.children` 的 provenance 边**故意保留**（与 `Kill` 现有语义一致，invariant #1/#7）。测试：`reap_test.go`（保留期/零值禁用/未盖章保护） |
| B | `Kill` 死亡快照"无 TTL" | **更正 + 补齐**：先核实到它**本来就有界** —— `snapshotStore` 自带上限 `maxDeathSnapshots=1024`（`snapshot.go`），按 `DiedAt` 淘汰最旧，**且已有测试锁定**（`TestSnapshotStoreBounded`）。**我上一轮"无 TTL"的说法不准确。** 但 cap 只限**条数不限字节**，而每条含一份认知状态 → 补上**年龄上限** `defaultDeathSnapshotRetention = 1h`（`save` 内按最新死亡时刻为基准裁剪），语义安全：复活消费发生在死亡后数秒内，且 `findByCapability` 偏好最新。测试：新增 `TestSnapshotStoreRetention`；同时把 `TestSnapshotStoreBounded` 的时间间隔从分钟改秒，**否则它会被年龄裁剪接管、悄悄不再测 cap** |
| C | 派发窗口残余 | **已修（含调度器侧，非仅 fabric）**：置 busy 从无条件写改为**守卫取用** `Agent.TryBeginQuantum()`（IDLE 且非 busy 才成功，与 `Retire` 争同一把 `a.mu`，**单赢家**）；调度器新增准入闸 `Scheduler.admitWinner`（抽函数，顺带把 `executeWithCandidates` 的圈复杂度从 32 压回门禁内），取用失败即 `EndNeutral` 释放 slot + `Release` 租约 → 下一轮 drain 重新调度到活 agent。配合既有 `inUse` 活租约过滤，窗口**结构性关闭**：要么 sweep 先退休（取用返回 false），要么调度器先取用（sweep 看到 busy 而跳过）。测试：`quantum_claim_test.go`（IDLE/BUSY/RETIRED/SUSPENDED 穷尽表） |

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
| **operator 建图已做依赖存在性校验** | `runCollabGraph` 自带边存在性检查（**非** `CompilePlan`） | `agent_routes_tasks.go:611-614` |
| **operator 建图已做环检测** | `runCollabGraph` 自带 `findCycle`（Kahn，**非** `detectPlanCycle`） | `agent_routes_tasks.go:617/:738` |
| **`create_plan` 已做依赖+环校验** | 走 `k.fabric.CompilePlan` → `resolveDependencies` + `detectPlanCycle` | `plan.go:164`；`workflow_plan.go:154/158` |
| spawn 前校验 spec（重复 ID / 非法 ID） | `ErrAgentExists`/`ErrInvalidSpawnSpec`；**注意：不校验空能力**（见 A2） | `lifecycle.go:108-110/183` |
| quota claim 记录 + kill/retire 释放 | 防"无限 spawn 撑爆" | `lifecycle.go:115/162`（claim）、`:280/:325`（release） |
| L2 能力校验（spawn 只出 L2 能力） | 防"造出永久空转的 agent" | `syscall.go:359-361` |

> 结论：**"任务工厂缺校验"只在 LLM `create_task` 这条"单任务"旁路上成立**（T1/T2）；
> `create_plan`（走 `CompilePlan`）与 operator 协作图（自带 `findCycle`）**都是有保护的**。
> 修 T1/T2 的正确姿势是**让 `create_task` 复用 `create_plan`/operator 已有的那类校验（或等价增量校验）**，
> 而不是"给任务工厂加环检测"（`CompilePlan` 已经有了，operator 也有自己的）。

---

## 三节共因

```
协作是"触发"非"对话"（I1）
   + 触发会产生新任务（T1/T2：LLM `create_task` 裸 Create，绕过 CompilePlan 的依赖/环校验 → 悬挂/成环静默卡死）
   + 触发会 spawn 新 agent（A2 空能力 / A3 quota 泄漏 / A4 孤儿累积）
```

**真正的一类根因**：三个模块都是**"引用式建/连"**（依赖 ID、spawn、协作目标），
但"被引用的东西到底存不存在 / 还活没活 / 有没有被清掉"的**一致性校验分布不均**：
- **operator 协作图**有（但走的是**自己的**边存在性检查 + `findCycle`，**不是** `CompilePlan`）；
- **LLM `create_plan`** 有（走 `CompilePlan` → `resolveDependencies` + `detectPlanCycle`）；
- **LLM `create_task`（单任务）** ❌ **没有**。

所以最该补的是一族**"引用完整性"守卫**——重点是给**单任务的 `create_task` 这条路**补上
`create_plan`/operator 已有的那类校验（或等价增量校验）：
- T1 依赖存在性校验（LLM `create_task`）
- T2 增量环检测（LLM `create_task` 跨量子长图）
- T4 GC 前引用计数
- A3 quota 孤儿回收 + TTL
- I4 dead-letter 落持久（可审计）

**修好这一族，"协作/工厂"的静默卡死就没了**——比逐个补单点更根本。

## 性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| **I1 ask 不回写** | 缺陷 | 高（协作"对话"缺半截） |
| **T1 悬挂依赖** | 缺陷 | 高（静默卡死整链） |
| **T2 依赖成环** | 缺陷 | 高（`create_task` 补增量环检测） |
| A3 quota 泄漏假死 | 隐患 | 中（加 TTL/孤儿回收） |
| A4 孤儿 agent 累积 | 取舍 | 中（reaper 回收） |
| I4 dead-letter 非持久 | 隐患 | 中（落 EventStore） |
| T4 GC 悬挂 | 隐患 | 中（GC 前引用计数） |
| A2 空能力 agent | 隐患 | 低（spawn 校验 + warn） |
| I2 去重窗口 / I5 Unregister / A1 只建不派 | 取舍 | 知边界 |
| 单进程边界 I3 | 取舍 | 知即可 |
