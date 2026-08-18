# ARES Runtime 设计（0.3.0 Kernel）

> 状态：设计文档（冻结）。本文是 Task Fabric / Agent Fabric / Scheduler 的权威模型。
> 定位：ARES 从 "Agent Orchestration Framework"（leader+sub）演进为
> **"面向 Agent 的动态计算运行时"**——Agents are not orchestrated. They are scheduled.

## 一、核心命题

| 对象 | 一句话定义 | Durability |
|------|-----------|------------|
| **Task** | durable intent（任务意图，持有 capability/state/lease/checkpoint） | durable |
| **Agent** | disposable execution（可调度计算实体，持有 context/tools/skills/experience） | disposable |
| **Checkpoint** | durable progress（进度恢复点） | durable |
| **Experience** | durable learning（skill 相关度先验） | durable |
| **Event Stream** | durable history（全状态可重建） | durable |
| **Runtime** | 组织以上对象的生命系统 | — |

**Agent 死亡 ≠ Task 死亡。** Agent 是 disposable 的，Task/State/Evidence 才是 durable 的。

## 二、三个职责分离（替代 leader 的 WHAT+WHEN+WHO+HOW 混杂）

```
Planner / Cognitive Compiler   = WHAT（要做什么 → 产出 Task Graph）
Runtime Scheduler              = WHEN / WHO（谁在何时以何资源约束跑）
Agent                          = HOW（怎么执行）
Evolution                      = BETTER（改进 graph / 调度策略 / agent 种群）
```

Leader 不再是一种角色，只是 **Execution Strategy / Policy** 之一：
`Peer Scheduling / Work Stealing / Priority / Capability / Cooperative Preemption / DAG Scheduling`
——全部是 Scheduler 的 policy，不是架构本身。

## 三、核心对象模型

### Task（durable intent）

```go
type Task struct {
    ID           string            // 稳定 ID
    Capability   string            // 所需能力（如 "rust/unsafe-analysis"）
    State        TaskState         // READY / LEASED / RUNNING / SUSPENDED / COMPLETED / FAILED
    Priority     int               // 抢占决策依据
    Owner        string            // 当前逻辑执行者（"" = 无人）——Lease 是持有权证明，不是持有权本身
    Lease        *Lease            // TaskLease（TTL 租约 = ownership 的有效性证明；Epoch 即 fencing token）
    Checkpoint   *Checkpoint       // durable progress
    Dependencies []string          // DAG 前置（is_ready 判定）
    Resources    map[string]any    // 资源需求
    Deadline     time.Time
    RetryPolicy  RetryPolicy       // 重试/失效策略
    // Executions 为长期模型预留边界：Task → Executions[]（Execution #1/#2/#3，
    // 每次执行绑定一个 Agent）。retry / handoff / stealing / preemption / crash
    // recovery / 性能度量都挂在 Execution 层。
    Executions []any               // 预留：[]*Execution
}
```

**Owner ≠ Lease（修正 1）**：Owner 是"当前逻辑执行者"，Lease 是"Owner 对该 Task 的暂时持有权
证明"。lease 过期只意味着所有权证明失效（Task 回 READY 可被重新 acquire），不改变"谁在逻辑上
执行"。所有带所有权的操作必须携带 **leaseEpoch（fencing token）** 校验，防止经典的
"A lease 过期 → B acquire → A 迟到 Release/Complete" 误把 B 的任务释放掉。

### Agent（disposable execution）

```go
type Agent struct {
    Identity     string            // 稳定标识
    Capabilities []string          // 声明能力
    State        AgentState        // IDLE / RUNNING / SUSPENDED / RETIRED
    Load         float64           // 当前负载（scheduler 依据）
    Confidence   float64           // 经验置信（Experience 提供）
    Context      any               // 对话上下文
    Tools        []string          // 已注册工具
    Skills       []string          // 已激活技能
}
```

### Resource（能力/租约/上下文的一等公民）

TaskLease / ResourceLease / CapabilityLease —— 全部从现有 `SessionLease`
（internal/agents/lease）抽象而来：`Acquire(id, owner, ttl) / Renew / Release`，TTL 过期自动失效。

## 四、Task 状态机（Cooperative，非硬抢占）

```
READY ──acquire()──▶ LEASED ──start()──▶ RUNNING
                                          │
                    ┌─────────────────────┤
                    ▼                     ▼
                SUSPENDED ◀──yield/checkpoint── RUNNING
                    │                     │
                    │ resume/preempt      ├─complete()──▶ COMPLETED
                    ▼                     ├─fail()──────▶ FAILED
                 LEASED                   └─preempt()──▶ READY（lease 释放，checkpoint 保留）
```

关键决策：**Cooperative Preemption**，不是 OS 硬抢占。LLM Agent 无法在任意 instruction
上被打断（inference/tool call/shell 中途不可停）——只在 **Quantum 边界**（yield/checkpoint）
切换。这与 Actor Runtime 的 work-stealing 现实一致。

**Yield 是 execution boundary，不是 state transition（修正 2）**：`yield()` 只是把执行权交回
Runtime，真正进入哪个状态由 Scheduler 决策：

```
RUNNING ──yield()──▶ [Scheduler 决策]
                        ├─ continue ──▶ RUNNING
                        ├─ suspend  ──▶ SUSPENDED（checkpoint 保留）
                        ├─ preempt  ──▶ READY（lease 释放，checkpoint 保留，可被他人 acquire）
                        ├─ handoff  ──▶ READY（同 preempt，语义为交接）
                        └─ complete ──▶ COMPLETED
```

否则每个 quantum 都会造成 RUNNING→SUSPENDED→LEASED→RUNNING 的怪异状态机。

## 五、执行 Quantum（不要 Task→Agent 一口气跑完）

```
Task ──▶ Execution Quantum ──▶ Agent Step（reasoning → tool call → observation）
                                    │
                              checkpoint（durable）
                                    │
                              yield() ──▶ Runtime Scheduler
                                    │
                              continue / handoff / suspend / split / cancel
```

每个 quantum 结束 Agent 向 Runtime `yield()`，Runtime 决定下一步。

## 六、四个核心原语（Runtime 地基）

```go
Acquire(taskID, agentID, ttl) (epoch, error) // CAS owner=agentID，READY→LEASED；返回 fencing token（epoch）
Release(taskID, agentID, epoch) error         // 归还，LEASED/RUNNING→READY；校验 epoch，防 "A 过期→B acquire→A 迟到 Release" 误杀 B
Yield(taskID, agentID, epoch, checkpoint)     // Quantum 边界：仅交回执行权；状态由 Scheduler 决策（continue/suspend/preempt/handoff/complete）
Checkpoint(taskID, state) error               // durable progress 落盘/事件
// 扩展原语：
Steal(taskID, fromAgent) error                // capability-aware work stealing
Preempt(taskID, reason) error                 // 高优先级抢占（cooperative）
```

所有带所有权的操作（Release/Yield/Complete/Fail/Preempt）都必须携带 **leaseEpoch**：
操作仅当 `task.Lease.Epoch == 传入 epoch` 且 owner 匹配时生效——epoch 是 ownership 的
fencing token，杜绝过期持有者的迟到操作。

Agent 只表达：`我具备 X 能力 / 我空闲 / 我申请 Y / 我执行 Y / 我释放 Y`。
它不知道"谁是 leader"。

## 七、事件升级（全状态可重建）

现有 `EventSubTaskScheduled / EventSubTaskResult` 升级为完整生命周期事件：

```
TaskCreated / TaskReady / TaskAcquired / TaskStarted / TaskYielded /
TaskCheckpointed / TaskPreempted / TaskReleased / TaskCompleted /
TaskFailed / TaskExpired / TaskStolen
```

Event Stream 单一来源（SEDA）重建：Scheduler State / Task State / Agent State /
Lease State / Action Audit——延续 Evidence-Driven 路线。

## 八、Capability-aware Work Stealing（与 Skill-First 打通）

传统 work stealing：`who is idle?` → 偷。
ARES：`who is the best executor for this task?` → 偷 + 能力匹配评分：

```
score = capability_overlap × (1 - load) × confidence
Agent C（rust+llvm, load 0.4, conf 0.97）→ steal Task(capability=rust) → 0.96
```

Capability Fabric（SkillCatalog / Experience / skill_activate）直接成为评分的
能力/经验来源——Skill-first 设计被真正用起来。

## 九、DAG 作为调度源（不再需要 leader 分发）

```
Task A completed ──▶ B ready / C ready ──▶ Scheduler
                                              ├── Agent X acquires B
                                              └── Agent Y acquires C
B ─┐
   ├─▶ D READY
C ─┘
```

Scheduler 只问 `is_ready(task)`（依赖满足 + 资源可用），拓扑由 MutableDAG / Evolution
驱动，无主从分发。

## 十、与现有积木映射（不推倒重来）

| 现有资产 | Runtime 角色 |
|----------|--------------|
| Event Stream（ares_events） | durable history + 状态重建（事件类型升级） |
| MutableDAG / live DAG | Task Graph（依赖拓扑即调度源） |
| Agent Registry（peer） | Agent Fabric 发现（spawn/suspend/resume/retire/clone） |
| SessionLease（agents/lease） | 抽象为通用 Lease（TaskLease/ResourceLease/CapabilityLease） |
| Capability Fabric / Experience | Capability-aware scheduling 评分来源 |
| skill_activate / 信任门控 | Agent 能力装配（spawn 时绑定技能） |
| ActionLog | 执行事实审计（Task 执行记录） |
| Chaos（ares_runtime arena） | **Failure Injection + Recovery Verification**（故意制造死亡，验证 Runtime 能活下来）；lease expiry / requeue / checkpoint recovery / agent restart 属 **Runtime Recovery**（独立职责，非 Chaos） |
| Evolution | 修改 Task Graph / 调度策略 / Agent 种群（spawn/retire/clone） |

## 十一、核心模型修正（Kernel 模型）

> 修正：Planner 不再是 Runtime 的中央组件。Runtime 收敛为 **ARES Kernel**——
> 不替 Agent 思考，只负责让 Agent 能安全地思考、通信、创建子进程、竞争资源、
> 被调度、死亡和恢复。Agent 是**同级认知进程**（A ≡ B ≡ C），父子仅 spawn
> provenance，不构成权限等级。
>
> **Agent decides; Kernel enforces.**

### Kernel 三支柱

```
              ARES KERNEL
                    │
    ┌───────────────┼───────────────┐
    │               │               │
Scheduler          IPC          Lifecycle
    │               │               │
Acquire Steal  Send Request   Spawn Suspend
Lease  Preempt  Reply Handoff  Resume Kill
                 Delegate Subscribe    Recover
```

Kernel 管"能不能、怎么活、怎么跑"；Agent 管"我要做什么、要不要生孩子、怎么协作"。
Runtime（Kernel）只负责调度与生命周期；Agent 进行任务处理。

### Agent = 同级认知进程

- 每个 Agent 独立持有 **Cognitive State**（Context / Observation / Working Memory /
  Decision / Tool State / Checkpoint）——Runtime 不依赖 hidden CoT，只依赖可 checkpoint 的状态。
- **spawn establishes provenance, not hierarchy**：A creates B 只建立生命周期来源，
  创建后 A ≡ B（同级），可互相通信、竞争任务。父子关系是语义关系，不是生命依赖
  （Parent 死，Child 活；Task 由 Runtime 回收重派）。
- **Process Tree ≠ Scheduling Graph**：spawn 因果关系树（Lifecycle 看）与 Task 依赖图
  （Scheduler 看）两张图并存不混——父子关系不形成新的 leader/sub 权力结构。

### Context 三层（不共享脑子）

| 层 | 内容 |
|----|------|
| Task Shared State | task goal / constraints / artifacts / decisions / dependencies / checkpoints（客观、需共同知道） |
| Agent Private State | working context / observations / hypotheses / tool history / scratchpad（每 Agent 独立） |
| IPC Messages | "我发现 X" / "帮我验证 Y" / "你的结论与我冲突"（Send / Request / Reply / Delegate / Handoff） |

### spawn 是 syscall，不是 orchestration API

```go
type SpawnSpec struct {
    Task         TaskSpec
    Capabilities []string
    Context      ContextSpec // parent 的 snapshot / selected projection
    Resources    ResourceSpec
}
Agent.Spawn(spec) // Kernel 校验 quota/capability/resource/policy 后创建 Agent+Task+父子关系
```

Planner 降级为 **Agent 的 cognitive capability**（可选），不再是 Runtime 中央组件——
与 Skill-first / Capability Fabric 一致。

### 修正后的核心定义

> ARES is a runtime where autonomous agents independently maintain cognition,
> communicate as peers, and cooperatively execute durable tasks under
> kernel-level scheduling and recovery.
>
> 中文：每个 Agent 独立持有自己的认知状态，通过 Peer IPC 协同完成持久任务，
> Runtime Kernel 负责调度、资源、生命周期与故障恢复。

### 架构不变量（不可随意破坏）

1. Agent 是同级认知进程——A ≡ B ≡ C；parent/child 只有 **spawn provenance**，没有权限层级。
2. Task 是 durable，Agent 是 disposable——**Agent 死亡 ≠ Task 死亡**。
3. Kernel 不负责思考——**Agent decides; Kernel enforces**。
4. Scheduler 负责"谁 / 何时 / 何约束下运行"；Agent 决定"做什么 / 是否 spawn / 如何协作"。
5. 每个 Agent 独立 Cognitive State——不共享一个"大脑"。
6. Context 三层分离——Task Shared State / Agent Private State / IPC Messages。
7. **Process Tree ≠ Scheduling Graph**——spawn 因果关系树与 Task 依赖图并存不混。
8. spawn 是 syscall，不是 orchestration API。
9. 抢占是 cooperative——不做假 OS 硬抢占。
10. 不提前设计——Auction / 分布式 Scheduler / 完整 Actor / Execution 实体 等继续暂缓。
    **v0.4.0 修订（2026-08-17）**：以下项从「暂缓」提升为「已排期」——
    多 Agent 协作模式（委托/流水线/编排）、进化驱动的 spawn 决策（自动 spawn/clone 策略）、
    进化驱动的资源分配（复杂资源调度）、进化驱动的 IPC 协议（消息格式/压缩）。
    详见「十二、v0.4.0 高级特性主线」。

### Task decomposition = Agent cognition

> Task decomposition is an Agent responsibility, not a Runtime responsibility.
> Agent may decide that a Task exceeds its effective execution scope and invoke
> spawn to create additional Tasks/Agents. The Kernel does not plan, decompose,
> or coordinate semantic work; it only validates and schedules the resulting
> execution entities.

中文：任务拆分属于 Agent 的认知职责，而非 Runtime 的调度职责。Agent 根据任务
复杂度、自身能力与当前上下文判断是否需要拆分，通过 spawn 创建新的 Task/Agent。
Kernel 不理解任务语义、不拆解任务——只校验 spawn 请求、调度产生的 Task、提供
IPC 与生命周期管理。**Runtime 不拆任务，Agent 拆任务。**

Agent 不直接操纵 Scheduler（禁止 `agent.scheduler.Schedule(...)` /
`agent.scheduler.Preempt(...)`）；只表达意图（`agent.Spawn / Send / Request /
Yield`），Kernel 决定执行——**Agent decides; Kernel enforces**。

**核心定理：Agents decide the work. Kernel schedules the work.**
（Agents decide what work should exist and how it should be solved; the Kernel
decides when, where, and under what constraints that work executes.）

### SUSPENDED 语义锁定（避免概念混淆）

`RunQuantum` 的 SUSPENDED 明确理解为：**Agent 本次 execution quantum 结束，但
Task 的 durable intent 尚未完成**——不是"Agent 被暂停了"。

三个概念不混：
- **Task suspended**：durable intent 未完成，Task 状态 SUSPENDED（checkpoint 保留，可被他人 acquire）
- **Agent suspended**：Agent 生命周期状态（Lifecycle 支柱）
- **Execution yielded**：本次 quantum 结束交回执行权（execution boundary，Scheduler 决策下一状态）

## 十二、v0.4.0 高级特性主线（2026-08-17 拍板）

> 核心 Runtime（P0-P5 + 生产接线）已完成。v0.4.0 聚焦高级特性，围绕既有三支柱
> （Scheduler / IPC / Lifecycle）扩展，不改变核心不变量（§11）。完整路线图与
> 落地计划见 `docs/analysis-reports/v0.4.0-feature-suggestions-corrected.md`。

### 优先级矩阵

| 方向 | 难度 | 价值 | 优先级 | 状态 |
|------|------|------|--------|------|
| M1 多 Agent 协作模式 | 中 | 高 | ⭐⭐⭐ P2 必做 | ✅ 已实现（agentipc/collaboration.go） |
| M2 Evolution-Runtime 深度集成 | 中高 | 高 | ⭐⭐⭐ P2 必做 | 🔄 进行中（M2-1 已实现） |
| M3 可解释性与人工反馈 | 中 | 高 | ⭐⭐⭐ P2 推荐 | ⏳ 待做 |
| M4 全局观测与调试 | 低 | 中 | ⭐⭐ P3 可选 | ⏳ 待做 |

### M1 多 Agent 协作模式（P2 必做）——兑现「同级认知进程」愿景

基于 IPC 原语（Send/Request/Reply/Delegate/Handoff/Subscribe）的**组合层**，
不引入中央编排（Coordinator 是 Agent 层协调者，非 Kernel 调度器）：
- **委托模式**：Leader → Specialist 任务委派 + 结果回传（`DelegateToSpecialist`）
- **流水线模式**：A → B → C 有序执行，数据经 IPC 传递（`Pipeline`）
- **编排模式**：Coordinator 并行协调多个 Worker + 失败重试（`Orchestrate`）

### M2 Evolution-Runtime 深度集成（P2 必做）

进化从"只影响策略参数"扩展到运行时决策维度（**Evolution decides; Kernel enforces**）：
- **M2-1 进化影响 spawn 决策**：Spawn 时机 / 数量（population 上限）/ 能力类型偏好
  （`aresrecovery.EvolutionAwareSpawner` + `SpawnPolicySource` 消费方接口）
- **M2-2 进化影响资源分配**：CPU/内存配额权重动态调整（配额来自活跃策略参数）
- **M2-3 进化影响 IPC 协议**：消息格式 / 压缩率优化（策略驱动编码选择）

### M3 可解释性与人工反馈（P2 推荐）

- 进化轨迹可视化（Dashboard：最佳策略路径 / 突破性变更 / 退化点）
- 人工反馈 API（`POST /api/evolution/feedback`：评分 + 批准 + 归因）
- 变更归因分析（每项变更的影响预估）

### M4 全局观测与调试（P3 可选）

- 跨 Fabric 追踪（Task / Agent / Message span）
- 仿真沙箱（Replay 历史事件验证恢复逻辑 + Simulate 未来场景）
- 性能基准测试（协作模式 / 追踪 / 沙箱）

### 与「不提前实现」清单的关系（§11 不变量 #10 修订）

以下项从暂缓提升为已排期（2026-08-17）：多 Agent 协作模式、自动 spawn/clone 策略、
复杂资源分配（配额权重）、新消息格式（IPC 压缩）。继续暂缓不变：Auction/bidding、
Agent migration、分布式/多级 Scheduler、Actor 模型完整实现、Execution 实体、新数据库。
