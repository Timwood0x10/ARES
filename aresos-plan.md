对。现在目标已经非常清楚了。

而且我建议你从现在开始，不要再把 ARES 描述成 Agent Framework。你真正要做的是：

ARES = Agent Operating System / Agent Runtime Kernel

Agent 不再是 Runtime 调用的函数，而是 Kernel 管理的认知计算实体。

Agent decides. Kernel schedules.

不过要注意一个关键边界：

Agent 类似 thread/process 一样被调度，但不是机械照搬 OS。

OS 调度 CPU instruction；ARES 调度 Agent 的 semantic execution quantum。

下面这份我建议直接作为 ARES 0.3.x → Agent OS 开发计划。

⸻

ARES Agent OS Development Plan

定位：Agent Operating System

ARES 将 Agent 从一个被 Leader 调用的执行函数，演进为由 Kernel 管理的独立认知计算实体。

Agent 可以自主产生 Task、Spawn Agent、通过 IPC 协作、持有独立 Cognitive State，并可以被调度、暂停、恢复、抢占、死亡和重新恢复。

Agent decides. Kernel enforces.

⸻

核心模型修正（2026-08-19 拍板）

> 本节是对上面「定位」与下面「0. 总体模型」的精确化。它不是新增阶段，而是
> 定义 ARES 模型的不可动摇的边界。所有阶段（P0-P6）的实现都必须符合本节。

1. Agent 本身没有等级

ARES 的模型里不存在 Leader / Worker / SubAgent 这些角色。所有 Agent 在 Kernel
看来完全平等，只有一行：

            Agent A
           ↙   ↓   ↘
     Agent B  Agent C  Agent D
           ↖   ↑   ↗
          Peer Network

它们拥有相同的原语能力：Think / Plan / Create Task / Spawn Agent / Send /
Request / Handoff / Acquire Task / Yield / Checkpoint。

Agent 之间的区别只来自：Capabilities、Skills、Context、Experience、
Current Load、Resources。

而不是：Role = Leader / Worker / Sub。

2. Agent 可以自主决定“我要怎么完成任务”

Kernel 从不告诉 Agent「先让 B 分析、再让 C 审查、最后你汇总」。Agent 自己认知
任务复杂度，自己决定是否需要协作、需要谁、怎么拆，然后自己发起 Spawn /
Request / Delegate。Kernel 不干预这个语义决策。

Agent 甚至可以完全独立完成一个复杂任务（「这个我自己能做」→ 自己执行）。
协作与否是 Agent 的认知产物，不是框架预定义。

3. Spawn 建立的是 provenance，不是 hierarchy

A ──spawn──> B 只意味着「B 是由 A 创建的」，绝不意味着「B 是 A 的下属」。

因此有两个完全不同的图：

Process / Provenance Graph          Scheduling / Task Graph
        A                                   Task X
        │                                      │
        B                                  ┌───┴───┐
        │                                 Task Y  Task Z
        C

- Provenance 图用于：lifecycle、debugging、audit。
- Task 图用于：调度（DAG 就绪集）。

同一对 Agent 可以出现在两个图里，但关系含义完全不同。

4. 协作关系是运行时动态形成的

传统 Multi-Agent：Team = { Planner, Researcher, Coder, Reviewer }，角色提前定义。

ARES：Agent A 接到任务 → 自己判断是否需要协作 →
- NO → 自己执行
- YES → 找 Agent B / Agent C，甚至 B 反过来指出 A 的假设有误、
  B 让 C 验证、C 再 Spawn D。

Agent topology 是运行时动态形成的，而不是架构预定义的。

5. Scheduler 只看能力，不看身份等级

Kernel 可以记录 Agent B 的 Parent = A，但该字段的用途只有 provenance /
lifecycle / debug / audit，绝不用于：authorization、scheduling priority、
communication privilege。

A spawn B 不导致 A > B；B spawn C 不导致 B > C。

调度只看能力匹配 + 负载 + 置信度。例：
- A: Capabilities = [rust, llvm]
- C: Capabilities = [rust, security]
- 任务 rust/unsafe-analysis → C score 0.96 > A score 0.91 → C 拿到任务。

C 拿到任务不是因为它是 Leader，也不是因为它是 Parent，只是因为 C 当前最适合。

6. Agent 可以自主产生新的工作

Agent 不只是 execute(task)，而是完整的认知循环：

observe → reason → decide → create task → spawn/request/delegate →
observe results → reason again

Task Graph 本身可以是 Agent cognition 的产物。Kernel 负责：
Task Created → Ready → Schedule → Acquire → Execute。

Kernel 不负责：「为什么应该创建这个 Task？」——这是 Agent 的认知问题。

7. 三个平面

ARES 的完整模型是三个正交平面：

            ARES
              │
     ┌────────┼────────┐
     │        │        │
  Cognition  Runtime   IPC
     │        │        │
   Agent    Kernel   Agent ↔ Agent
     │        │        │
     ▼        ▼        ▼
  WHY/WHAT  WHEN/WHO  HOW TO COOPERATE

- Agent / Cognition：Why? What? Should I split? Should I ask someone?
  Who should I ask? Should I continue alone?
- Kernel：Can you? When? With which resource? Is your lease valid?
  Can this task run? What happens if you die?
- IPC：Tell / Ask / Reply / Delegate / Handoff / Subscribe。

8. 因此 ARES 的核心定义修正为

之前：Agents are autonomous cognitive processes…

修正为：

ARES is a peer-agent operating system.

Every Agent is a first-class cognitive process. There are no privileged
agent roles. Agents independently plan their work, decide whether to
collaborate, create tasks, and communicate with peers. The Kernel provides
the mechanisms that make those decisions safe and executable: scheduling,
resource control, IPC, lifecycle, and recovery.

中文：

ARES 是一个面向 Peer Agent 的操作系统。

每个 Agent 都是一等认知进程，不存在 Leader、Worker、SubAgent 等固有等级。
每个 Agent 都可以独立规划任务、决定是否拆分任务、决定是否寻求其他 Agent
协作，并通过 Peer IPC 完成协同。

Kernel 不负责替 Agent 做这些认知决策，只负责提供这些决策能够安全执行所需的
机制：调度、资源、IPC、生命周期和恢复。

对 Evolution 的推论：未来 Evolution 优化的是「Peer Agent population +
scheduling policy」自身，而不是「哪个 Agent 当 Leader」。但这条先不做，
等 Kernel 真跑起来再谈。

9. OS 类比的边界：像到什么程度，不像到什么程度（2026-08-19 收敛）

「Agent ≈ thread」说的是**运行时抽象与生命周期关系**，不是把 LLM 执行做成
CPU thread 的硬实时实现：

    OS:                              ARES:
    Process / Thread                 Agent
         ↓                                ↓
    Kernel Scheduler                 ARES Kernel Scheduler
         ↓                                ↓
    CPU / Resource                   LLM / Tool / Resource

类比成立的部分（ARES 要做）：
- Agent 是被 Kernel 管理的可恢复执行实体（acquire / quantum / yield /
  checkpoint / resume）
- Agent 可以像进程一样死亡与恢复；Task 是 durable 的，Agent 是 disposable 的
- Scheduler 只看能力/负载/租约，不看身份
- 协作关系运行时动态形成，无预定义角色

类比到此为止的部分（ARES 明确不做）：
- ❌ 墙钟时间片抢占（hard timeslice）——Agent 是协作式（Cooperative Preemption）
- ❌ 硬抢占正在 inference 的 LLM（无 SIGKILL 式杀推理）
- ❌ CFS / 优先级调度算法
- ❌ PCB / 寄存器级上下文切换
- ❌ per-agent CPU 式 thread queue（共享 ReadyTasks 队列 + 并发 drain 已覆盖）
- ❌ CANCELLED / ZOMBIE 等进程状态（属后续可靠性需求，非 Agent OS 核心）

**Quantum 的精确定义（写进设计，避免被 CFS 挑刺）：**

    Quantum is a cognitive execution boundary, not a wall-clock CPU timeslice.

Quantum = 一轮认知/工具执行边界（如一轮 ReAct）。不是 50ms 时间片。
Agent 在 quantum 边界自行决定继续 / yield / checkpoint / 完成。

⸻

0. 总体模型

最终 ARES 不再是：

User
  │
  ▼
Leader / Planner
  │
  ├── Agent A
  ├── Agent B
  └── Agent C

而是：

                         ARES KERNEL
                              │
          ┌───────────────────┼───────────────────┐
          │                   │                   │
      Scheduler              IPC             Lifecycle
          │                   │                   │
     Task / Quantum      Peer Messages      Spawn / Kill
          │                   │              Suspend / Recover
          │                   │                   │
          └───────────────────┼───────────────────┘
                              │
                ┌─────────────┼─────────────┐
                │             │             │
             Agent A       Agent B       Agent C
                │             │             │
             private       private       private
            cognition      cognition      cognition

Agent 自己决定：

我要做什么？
我要不要拆任务？
我要不要 Spawn？
我要不要找其他 Agent？
我要不要 handoff？

Kernel 决定：

你能不能 Spawn？
你什么时候运行？
你拿到哪个 Task？
你有没有资源？
你的 Lease 是否有效？
你死了之后怎么恢复？

⸻

P0 — Task Kernel ✅

你现在已经完成。

目标

建立 ARES 最基础的 durable work substrate。

Task 不依附于 Agent。

Agent 💀
   │
   X
   │
Task ──────────► survives

已完成

Task
Lease
Epoch
CAS Acquire
Release
Checkpoint
Yield
Complete
Fail
Lease Expiry
Event

核心：

Acquire(taskID, agentID, ttl) (epoch, error)
Release(taskID, agentID, epoch) error
Yield(taskID, agentID, epoch, checkpoint)
Checkpoint(taskID, state) error

验收

竞争

Agent A ──┐
          ├── Acquire(Task X)
Agent B ──┘

只能一个成功。

fencing

A epoch=1
 ↓
lease expired
B epoch=2
 ↓
acquire
A late Release(epoch=1)
 ↓
REJECT

Recovery

Agent A 💀
    ↓
lease expired
    ↓
Task → READY
    ↓
Agent B

验收标准

* go test -race ./...
* CAS 正确
* Epoch fencing 正确
* Lease expiry 正确
* Event 可重建 Task State

⸻

P1 — Semantic Scheduler

这是现在最重要的一阶段。

目标不是调度 Agent。

目标是：

让 Agent 的执行第一次变成可让出的 Quantum。

⸻

P1.1 Execution Quantum

现在：

Agent.Run(Task)
       │
       └──────────────► 完成

改成：

Agent
 │
 ├── Quantum 1
 │
 ├── yield
 │
 ├── Quantum 2
 │
 ├── yield
 │
 └── Quantum N
        │
        ▼
      complete

一个 Quantum 可以是：

reason
→ tool call
→ observation
→ checkpoint

而不是 CPU instruction。

⸻

API

保持简单：

RunQuantum(
    taskID string,
    agentID string,
    epoch uint64,
    step QuantumStep,
) error

结果：

type QuantumResult struct {
    Checkpoint *Checkpoint
    Done       bool
    Err        error
}

语义：

Done
 ↓
COMPLETED
Err
 ↓
FAIL / retry
!Done
 ↓
SUSPENDED

⸻

P1.2 AgentQueue

建立最简单的：

AgentQueue

但注意：

这是 Agent 的执行队列，不是 AgentScheduler。

例如：

Agent A
 ├── Task 1
 ├── Task 5
 └── Task 8

Scheduler 从 Ready Tasks 中选择 Agent：

Task
 ↓
Capability match
 ↓
Load
 ↓
Confidence
 ↓
Priority
 ↓
Acquire
 ↓
Agent Queue

⸻

P1.3 Work Stealing

空闲 Agent：

Agent C idle

检查：

Agent A queue
Agent B queue

找到：

Task requires rust
Agent C supports rust

然后：

Steal
 ↓
Acquire
 ↓
execute

⸻

P1 验收

必须能跑出：

Task A
  │
  ├── Quantum #1
  │
  ├── checkpoint
  │
  ├── yield
  │
  ├── Quantum #2
  │
  └── complete

测试：

1. Quantum 未完成 → SUSPENDED
2. Quantum 完成 → COMPLETED
3. Quantum error → FAILED
4. stale epoch → reject
5. Capability Scheduler 选择正确 Agent
6. Work stealing 能偷
7. 无能力 Agent 不允许偷
8. go test -race ./...

P1 完成后的效果

这是第一个非常重要的里程碑：

ARES 不再是“Task → Agent → Run to completion”。

而变成：

Task → Agent → Quantum → Kernel → Agent → Quantum。

此时才真正开始像 Runtime。

⸻

P2 — Task Scheduling Kernel

这一阶段让 Scheduler 真正成为 Kernel 的核心。

⸻

P2.1 DAG Ready

Scheduler 不负责规划 DAG。

只负责：

IsReady(task)
ReadyTasks()

例如：

A
├── B
└── C
     │
     ▼
     D

初始：

A READY
B WAIT
C WAIT
D WAIT

A 完成：

B READY
C READY

B/C 都完成：

D READY

Scheduler 只问：

“现在谁 ready？”

⸻

P2.2 Cooperative Preemption

不是：

kill Agent

而是：

high priority Task
       ↓
Scheduler requests preempt
       ↓
Agent reaches quantum boundary
       ↓
checkpoint
       ↓
yield
       ↓
Task READY

例如：

Agent A
 └── low priority Task X

突然：

Task Y priority=100

Scheduler：

X → preempt
Y → acquire

Agent A 并没有被强制打断。

⸻

P2.3 Event Store

所有重要状态：

TaskCreated
TaskReady
TaskAcquired
TaskStarted
TaskYielded
TaskCheckpointed
TaskPreempted
TaskReleased
TaskCompleted
TaskFailed
TaskExpired
TaskStolen

进入 EventStore。

⸻

P2 验收

必须能证明：

Task A
 ↓
RUNNING
 ↓
checkpoint
 ↓
preempt
 ↓
READY
 ↓
Agent B
 ↓
RUNNING
 ↓
complete

同时：

Event log
    ↓
replay
    ↓
same state

P2 完成后的效果

ARES 已经拥有一个真正意义上的：

durable cooperative task scheduler

⸻

P3 — Agent Process Model

这是 ARES 从 Runtime → Agent OS 的真正转折点。

之前：

Agent = executor

现在：

Agent = cognitive process

⸻

P3.1 Agent Cognitive State

定义：

type CognitiveState struct {
    Context
    Observations
    WorkingMemory
    Decisions
    ToolState
    Checkpoint
}

重点：

Runtime 不保存 hidden CoT。

Runtime 只保存：

Agent 自己声明的、可恢复的 cognitive state。

例如：

Agent A
Goal:
    audit unsafe FFI
Working Memory:
    discovered 17 unsafe blocks
Observations:
    FFI boundary X suspicious
Decision:
    investigate X
Tool State:
    llvm-analysis completed
Checkpoint:
    ...

⸻

P3.2 Context 三层

严格分离：

Task Shared State
       │
       ├── goal
       ├── constraints
       ├── artifacts
       ├── decisions
       └── checkpoints
Agent Private State
       │
       ├── reasoning context
       ├── observations
       ├── hypotheses
       └── scratchpad
IPC
       │
       ├── Send
       ├── Request
       └── Reply

不要：

Agent A context == Agent B context

而是：

A.private ≠ B.private
A ──IPC──► B

⸻

P3.3 Spawn

这是你刚才提出的自动拆分复杂任务真正落地的地方。

定义：

type SpawnSpec struct {
    Task         TaskSpec
    Capabilities []string
    Context      ContextSpec
    Resources    ResourceSpec
}

Agent：

child, err := agent.Spawn(spec)

Kernel 做：

validate capability
validate resource
validate quota
create Agent
create Task
record provenance
Task → READY

但是：

spawn 不建立权力层级。

A spawn B
A ≡ B

⸻

P3.4 复杂任务拆分

最终：

User
 ↓
Agent A
 ↓
认知判断：
“这个任务太大”
 ↓
Spawn B
Spawn C
Spawn D
 ↓
Kernel
 ↓
Scheduler
 ↓
B/C/D

例如：

Agent A
“重构认证系统”
      ├── Spawn B
      │     “分析现有认证架构”
      │
      ├── Spawn C
      │     “设计新认证方案”
      │
      └── Spawn D
            “进行安全审计”

然后：

B ──┐
C ──┼── IPC ──► A
D ──┘

⸻

P3 验收

必须完成一个真实场景：

Task: 重构某个中型模块

Agent A 判断：

too complex

然后：

A
├── spawn B
├── spawn C
└── spawn D

要求：

* B/C/D 是同级 Agent
* A/B/C/D 可以互相 IPC
* B/C/D 有独立 Cognitive State
* B/C/D 可以独立 checkpoint
* A 死亡，B/C/D 不死亡
* B/C/D 可以继续执行
* Task 不因 A 死亡而消失
* 新 Agent 可以从 checkpoint 恢复

P3 完成意味着：

ARES 第一次真正成为 Agent OS。

⸻

P4 — Peer IPC

现在让 Agent 真正成为：

Peer

而不是：

Leader → Sub

⸻

IPC API

最小集合：

Send()
Request()
Reply()
Delegate()
Handoff()
Subscribe()

例如：

Agent A
 │
 │ Request:
 │ “帮我验证 LLVM ABI”
 ▼
Agent B
 │
 │ Reply:
 │ “发现两个 ABI mismatch”
 ▼
Agent A

⸻

Handoff

Agent A：

我无法继续

不是：

Leader 帮我找人

而是：

A
 │
 └── Handoff(Task X)
          │
          ▼
       Kernel
          │
          ▼
       Agent C

Kernel 负责：

lease
checkpoint
ownership
schedule

Agent 负责：

语义交接

⸻

P4.1 Leader 降级

旧：

Leader
 ├── planner
 ├── dispatch
 └── subAgent

新：

Agent
 │
 ├── cognition
 ├── spawn
 ├── IPC
 └── task production
Kernel
 │
 ├── Scheduler
 ├── Lifecycle
 └── IPC

Legacy Leader 可以暂时存在：

Legacy Leader Policy
       │
       ▼
Task Fabric
       │
       ▼
Scheduler
       │
       ▼
Agents

通过 feature flag 灰度。

⸻

P4 验收

必须证明：

A ≡ B ≡ C

也就是：

* A 可以给 B 发消息
* B 可以给 A 发消息
* B 可以给 C 发消息
* Child 可以与 Parent 通信
* Child 可以与非 Parent 通信
* Parent 死亡不影响 Child
* 不存在 Leader 权限绕过

然后：

Leader OFF

整个系统仍然可以完成任务。

这个验收非常关键：

关闭 Leader 后，ARES 仍然能工作。

这才说明 Leader 真正只是 Policy。

⸻

P5 — Recovery Kernel

现在才开始系统性解决：

Agent 为什么死了都没关系？

⸻

Recovery

Agent A
 │
 │ Task X
 │
 💀
 │
 ▼
Kernel detects failure
 │
 ▼
Lease expiry
 │
 ▼
Task X READY
 │
 ▼
Checkpoint
 │
 ▼
Agent B acquire
 │
 ▼
resume

⸻

P5.1 Agent Restart

如果：

Agent A 💀

Kernel 可以：

recover

创建：

Agent A'

从：

CognitiveState checkpoint

恢复。

注意：

A' != A

但：

CognitiveState(A')
≈
Checkpoint(A)

⸻

P5.2 Chaos

Chaos 不负责恢复。

它只负责：

故意杀。

例如：

kill Agent
expire lease
drop IPC
delay tool
crash execution

Recovery：

发现问题
 ↓
恢复

Chaos：

制造问题
 ↓
验证 Recovery

严格分开。

⸻

P5 验收

完整演示：

Task
 │
 ▼
Agent A
 │
 ├── checkpoint #1
 ├── spawn B
 ├── IPC C
 │
 💥 A dies
 │
 ▼
Kernel Recovery
 │
 ▼
Task survives
 │
 ▼
B/C continue
 │
 ▼
new Agent D
 │
 ▼
resume checkpoint
 │
 ▼
COMPLETED

如果这个测试稳定通过：

Agent 死亡 ≠ Task 死亡

就不再是架构宣言，而是事实。

⸻

P6 — Evolution

最后才把你原来 ARES 的 Evolution 接回来。

Evolution 不再只是：

优化 Agent

而是：

Runtime Adaptation

它可以优化：

Scheduling Policy
        ↓
Capability Matching
        ↓
Spawn Strategy
        ↓
Agent Population
        ↓
Task Graph

例如：

过去：
Rust Task
Agent A success rate = 60%
Evolution
     ↓
发现 Agent C = 94%
     ↓
Scheduler policy 更新

或者：

某类任务经常失败
       ↓
Evolution
       ↓
建议自动 spawn reviewer

但这里必须坚持：

Evolution 提供策略变化，Kernel 执行策略。

⸻

最终验收：Agent OS Demo

整个 ARES 0.3.x 最终应该能够完成一个这样的实验。

⸻

Scenario

用户：

“分析这个大型 Rust 项目中的 unsafe FFI 风险，并给出修复方案。”

⸻

Step 1

Agent A 获得 Task：

Analyze FFI risks

⸻

Step 2

A 自己判断：

Task too large

⸻

Step 3

A：

Spawn B → code structure analysis
Spawn C → FFI safety analysis
Spawn D → dependency analysis

⸻

Step 4

Kernel：

B/C/D → READY

Scheduler：

capability matching
load
confidence
priority

分别分配执行。

⸻

Step 5

B：

checkpoint
yield

Scheduler：

继续 / handoff / suspend

⸻

Step 6

C 发现：

interesting FFI boundary

通过 IPC：

C → A

⸻

Step 7

A 发现 D 的结果不足：

A → D Request

⸻

Step 8

B 突然死亡：

B 💥

Kernel：

lease expiry
checkpoint recovery

重新：

Task → READY

⸻

Step 9

E acquire：

E → resume B checkpoint

⸻

Step 10

最终：

A
├── B result
├── C result
└── D result

A 做 synthesis。

⸻

最终：

               USER
                 │
                 ▼
              Agent A
                 │
        ┌────────┼────────┐
        │        │        │
      spawn    spawn    spawn
        │        │        │
        ▼        ▼        ▼
      Agent B  Agent C  Agent D
        │        │        │
        └────IPC─┼────────┘
                 │
             Kernel
                 │
        ┌────────┴────────┐
        │                 │
    Scheduler          Recovery
        │                 │
        ▼                 ▼
     Quantum           Agent 💀
        │                 │
        └────────┬────────┘
                 ▼
             Agent E
                 │
              resume
                 │
                 ▼
             Agent A
                 │
              synthesis
                 │
                 ▼
               RESULT

这时候你就可以非常有底气地说：

ARES is an Agent Operating System.

⸻

开发顺序最终冻结为（2026-08-19 收敛）

> 收敛原则：**不要做成「能运行 LLM 的 Linux」**。硬实时 OS 项（墙钟时间片、
> 硬抢占 LLM、CFS、PCB/寄存器上下文切换、per-agent CPU 队列）**不在验收内**
> （见「核心模型修正」第 9 条）。阶段编号保留 P0-P6 作为里程碑，但**开发重心
> 按下面三档排序**：

核心档（当前要做的，按序）：

阶段	核心问题	完成后的能力	状态
P0	Task 能不能独立存在？	Durable Task + Lease + Recovery	✅ 已完成
P1	Agent 能不能被 Kernel 调度执行？	Quantum(=认知边界) + Checkpoint + Yield + Resume	✅ 已完成
P2	Agent 能不能自主拆任务？	Spawn + Peer IPC + Synthesis（**自主性验收，最重要**）	🟡 原语已落地，见下方「P2 收敛验收」
P3	Agent 死/资源受限系统还稳吗？	Token budget + Tool budget + Deadline + Lease expiry + Checkpoint recovery（**Agent Runtime resource governance，不是 cgroup**）	✅ 已实现（2026-08-19）
P4	没有 Leader 也能跑？	Peer IPC + 拆掉旧 Leader/Sub 抽象	🟡 进行中

次级档（已有，非当前重心）：

阶段	完成后的能力	状态
P5	Recovery + Chaos	✅ 已完成
P6	Evolution（population + scheduling policy 自进化）	🟡 已接线，先不做

明确不在验收内（不测）：

- CANCELLED / ZOMBIE 进程状态（后续可靠性需求）
- 墙钟时间片 / 硬抢占 LLM / CFS / PCB 级上下文切换 / per-agent CPU 队列
- 模拟 Linux CFS 调度

⸻

最重要的四条架构纪律

以后无论实现什么功能，都先问这四个问题：

① 这是 Agent 决定的，还是 Kernel 决定的？

WHAT / WHY / decomposition
        → Agent
WHEN / WHO / resource / safety
        → Kernel

② 这是 durable work 还是 disposable execution？

Task        → durable
Checkpoint  → durable
Experience  → durable
Agent       → disposable

③ 这是语义协作还是系统调度？

“帮我分析这个”
        → IPC
“谁现在执行它？”
        → Scheduler

④ 能不能不做？

如果只是为了：

“更像操作系统”

而不是解决真实 Agent runtime 问题：

不做。

⸻

我尤其建议你把这次 ARES 的核心愿景正式收敛成这一段，甚至可以放进 README：

ARES is an Agent Operating System.

Agents are autonomous cognitive processes, not functions invoked by an orchestrator. They independently create work, communicate as peers, maintain private cognitive state, and may spawn other agents. The ARES Kernel provides scheduling, synchronization, IPC, resource enforcement, lifecycle management, and recovery.

Agents decide. The Kernel enforces.

Agent death is an execution failure, not a task failure.

这比“Multi-Agent Framework”“Agent Orchestration Runtime”都更准确，也和你现在这套 Task Fabric → Scheduler → Agent Fabric → IPC → Recovery 的路线完全一致。


编码规范 plan/rules/code_rules_v2.md
⸻



## 附件：现状核对（2026-08-19 code review 对照）

> 本附件把本计划（aresos-plan.md）的每一阶段与代码库实际状态逐项核对，
> 标注已完成/部分/缺口，并列出 4 处需要决策或补做的差异点。
> 核对依据：`internal/taskfabric`、`internal/agentfabric`、`internal/agentipc`、
> `internal/aresrecovery`、`internal/ares_arena`、`cmd/ares`（kernel/scheduler/serve）。

### A. 逐阶段核对表

| 阶段 | 计划要求 | 代码库现状 | 结论 |
|---|---|---|---|
| P0 Task Kernel | Task/Lease/Epoch/CAS Acquire/Release/Checkpoint/Yield/Complete/Fail | `taskfabric`：`Acquire(taskID,agentID,ttl)(epoch,err)`、`Yield`、`Checkpoint`、epoch fencing、lease 过期→READY 全部存在 | ✅ 已完成 |
| P1.1 Execution Quantum | `RunQuantum(taskID,agentID,epoch,step)`，Done→COMPLETED / Err→FAIL / !Done→SUSPENDED | `taskfabric/quantum.go` `RunQuantum` 完全一致；测试覆盖 SUSPENDED/COMPLETED/FAILED/stale epoch 全验收。**2026-08-19 补齐调度器接线**：`taskExecutor` 新增 `ExecuteStep`（单轮 ReAct = 一个 quantum，`chatStepState` 为可序列化 PCB，§6.1 SchemaVersion + §6.2 TaskID 校验），`sub.Agent` 接口新增 `ExecuteStep`，`scheduler.go` step 闭包改为 `!Done → SUSPENDED(保留 checkpoint) → 下轮 drain 经 `ResumableTasks` 重新 acquire 恢复`；事件过滤器加 `task.yielded` 即时续跑。测试：`TestKernelSchedulerQuantumYieldResume`、`TestExecuteStepYieldsThenResumes` | ✅ 已完成 |
| P1.2 AgentQueue | 每个 Agent 一个执行队列（A→Task1/5/8） | **已删除**（`steal.go` 作为死代码清理，注释明确「per-agent 队列被共享 ReadyTasks 队列并发 drain 取代」） | ⚠️ 计划 vs 代码冲突 |
| P1.3 Work Stealing | 空闲 agent 偷别的队列任务（capability 匹配） | 共享队列 + 并发 drain（bounded goroutines）+ capability 打分；注释称「这就是 stealing substrate」；**无显式 per-agent steal 原语**。**2026-08-19 补验收 5/6/7**：`TestKernelSchedulerCapabilityPicksCorrectAgent`（能力选对）、`TestKernelSchedulerWorkStealingPicksIdleCapableAgent`（负载折扣→空闲 capable 接手）、`TestKernelSchedulerIncapableAgentCannotSteal`（无能力不允许偷） | ⚠️ 语义等效但形态不同 |
| P2.1 DAG Ready | IsReady/ReadyTasks，A 完成→B/C READY | `kernelScheduler` + event-driven DAG 完成（GAP6） | ✅ |
| P2.2 协作式抢占 | 高优任务→preempt 请求→agent 到 quantum 边界→checkpoint→yield→READY | `preemptLowerPriority` + `TestFabricPreemptPreservesCheckpoint` | ✅ |
| P2.3 Event Store | TaskCreated…TaskStolen 全量事件 | `ares_events.EventStore` + kernel 循环订阅 | ✅ |
| P3.1 Cognitive State | `CognitiveState{Context,Observations,WorkingMemory,Decisions,ToolState,Checkpoint}` | `agentfabric.CognitiveState` + `SetCognitiveState` + `RestartAgent` 从 checkpoint 恢复 | ✅ |
| P3.2 Context 三层 | Task Shared / Agent Private / IPC 严格分离 | `fabricTaskMeta` + UserProfile + checkpoint 机制部分覆盖（yield 恢复 UserProfile 已修）；「Agent Private State」概念上较弱 | ⚠️ 部分 |
| P3.3 Spawn | `SpawnSpec{Task,Capabilities,Context,Resources}`，Kernel 校验 capability/resource/quota→创建→READY，无权力层级 | `agentfabric.SpawnSpec` + `EvolutionAwareSpawner.Spawn`（配额/spawn 上限/演进约束）+ `RecordSpawn` provenance | ✅ |
| P3.4 复杂任务拆分 | Agent 自主判断「太大」→Spawn B/C/D→同级协作 | 原语齐备，但「Agent 运行中自主 spawn 子任务」的生产链路无端到端 demo | ⚠️ 原语有、场景未验证 |
| P4 Peer IPC | Send/Request/Reply/Delegate/Handoff/Subscribe | `agentipc.Bus` 六原语全有；collaboration 已接入生产（wireEvolutionIPC topic 分发） | ✅ |
| P4.1 Leader 降级 | Leader 只是 policy，feature flag 灰度 | `wireKernelPolicy`：非 legacy 即 flip 到 taskfabric（默认），`flipKernelToTaskFabric` 幂等 | ✅ |
| P5 Recovery | Agent 死→lease 过期→Task READY→新 agent 从 checkpoint 恢复；Chaos 只负责杀 | `aresrecovery.Recovery`（requeue-only + `RestartAgent` + CognitiveState 恢复）+ `ares_arena` chaos | ✅ |
| P6 Evolution | Runtime Adaptation：调度策略/capability/spawn/agent 群/任务图 | `EvolutionAdapter.AdaptPopulation` 已存在但**生产未接线**（仅库+测试） | ⚠️ 库有、未接线 |

### B. 需要决策/补做的 4 处差异

1. **P1.2/P1.3 AgentQueue + Work Stealing 与代码冲突**
   计划要求 per-agent 队列 + 显式 steal；代码选择「共享 ReadyTasks 队列 + 并发 drain」
   替代（并删了 `steal.go`）。并发 drain 天然有窃取效果，但 per-agent 队列的
   负载隔离语义没有了。
   **决策**：✅ 维持共享队列（现状）并发 drain 方案。共享 ReadyTasks + bounded
   goroutines 已覆盖 stealing 语义，scheduler.go 中 `TODO(tech-debt)` 注释已标注。
   仅在 profiling 显示 contention 时恢复 per-agent 队列。

2. **P3.2 Context 三层分离不完整**
   `CognitiveState` 已有，但「Agent Private State」与「Task Shared State」的严格
   边界在生产代码里较松散（主要通过 `fabricTaskMeta` 的 checkpoint 承载）。
   **状态**：✅ 已通过 `agentfabric.ContextView` + `SetTaskContext`/`SetPrivate`
   三层 API + 端到端测试 `TestP3_2_ContextThreeLayerSeparation` 落地。验证 Private
   State 不会泄漏到 Task Shared State、不同 Agent 的 Private State 相互隔离。

3. **P3.4 复杂任务拆分无端到端证明**
   Spawn 原语齐备，但「Agent 运行中自主 spawn 子任务」没有真实 demo/测试
   （计划 P3 验收要求跑通「重构中型模块」场景）。
   **状态**：✅ 已落地。端到端测试 `TestP3_4_EndToEndSpawnSynthesis` +
   `TestP3_4_ConcurrentSpawnSynthesis` + `TestP3_4_ParentDeathChildrenContinueTasks`
   在 `agentfabric/e2e_spawn_synthesis_test.go` 验证：A 判断任务太大 → Spawn B/C/D
   → B/C/D 独立 Cognitive State → A 死亡 B/C/D 存活 → E 从 checkpoint 恢复 →
   synthesis。IPC 端到端 `TestP3_4_P4_EndToEndSpawnIPC` 在
   `agentipc/e2e_spawn_ipc_test.go` 验证 peer 通信 + A 死亡后 B↔C 仍可通信。

4. **P6 Evolution 生产未接线**
   `AdaptPopulation` 是库代码，生产调度策略未由 evolution 驱动
   （计划要求「Evolution 提供策略变化，Kernel 执行策略」）。
   **状态**：✅ 已接线。新增 `aresrecovery.PopulationAdapter` +
   `PopulationPolicySource` 接口 + `RunKernelEvolutionLoop` 循环，在
   `cmd/ares/serve_routine.go` 中通过 `ares_bootstrap.NewPopulationPolicySource`
   从 evolution StrategyStore 读取 population.spawn / population.retire 策略参数，
   定期应用到 Agent Fabric。

### C. 建议执行顺序

1. **P3.4 端到端**（价值最高）：把「Agent 自主 Spawn 子任务 + IPC 汇总 + synthesis」
   做成可运行 demo（计划最终验收场景的简化版），同时驱动 P3.2 的 Context 三层落地。
   ✅ 已完成。
2. **P6 接线**：让 `EvolutionAdapter` 在 kernel 循环里驱动调度策略（改动不大）。
   ✅ 已完成。
3. **P1.2/P1.3 决策**：恢复 per-agent 队列（计划原教旨）或维持共享队列（现状），
   由架构负责人拍板。
   ✅ 已决策：维持共享队列并发 drain 方案。

> 备注：P0/P1.1/P2/P3.1/P3.3/P4/P5 均已在生产落地并有测试覆盖，无需重复实现。
> P3.4/P3.2/P6 差异已全部补做，端到端测试 + 生产接线全部落地。
> 「Agent death is an execution failure, not a task failure」的哲学已在
> recovery 链（requeue-only + CognitiveState 恢复）中体现。

⸻

### D. P2 收敛验收：Agent 自主性（2026-08-19 定稿）

> P2 验收**不测**「Scheduler 有没有模拟 Linux CFS」。P2 验收只测一件事：
> **Agent 有没有自主性**——它能自己决定怎么完成任务，而不是被 Leader/Planner
> 指挥。以下 4 个 Case 是 P2 的唯一验收标准（均已由端到端测试覆盖）。

**Case 1：Agent 独立完成**

    Task A → Agent A → 自己执行 → COMPLETE

Kernel 只负责调度，不干预 Agent 的决策。没有 Planner，没有 Leader。
（覆盖：`TestP3_4_EndToEndSpawnSynthesis` 中单 Agent 直接完成的路径）

**Case 2：Agent 自主拆分**

    Task A → Agent A → 判断任务复杂 → Spawn B / Spawn C
         → B、C 并行执行 → IPC 返回结果 → A 汇总 → A COMPLETE

没有 Planner，没有 Leader。拆分是 A 的认知决策，不是框架预定义。
（覆盖：`TestP3_4_EndToEndSpawnSynthesis`、`TestP3_4_ConcurrentSpawnSynthesis`）

**Case 3：父 Agent 死亡，Task 不死亡**

    A → Spawn B、C → A 💀
         → B 继续、C 继续 → Kernel: lease expired → Task READY
         → B/C/Anew acquire → checkpoint resume

证明「Agent death ≠ Task death」。恢复链（requeue-only + CognitiveState）是
唯一负责方，没有任何「Leader 接管」逻辑。
（覆盖：`TestP3_4_ParentDeathChildrenContinueTasks`、`TestP3_4_P4_EndToEndSpawnIPC`）

**Case 4：Agent 之间真正协作**

    A → B: "帮我验证 FFI 边界" → B → A: "发现 3 个风险"
    A → C: "帮我独立复核第二个风险" → C → A: "确认存在"

这是 Peer Agent System 的证明：B 可以反驳 A、B 可以找 C 验证、C 可以再 Spawn D，
**A ≡ B ≡ C ≡ D**，无任何权限差异。
（覆盖：`agentipc/e2e_spawn_ipc_test.go` + collaboration 生产接线）

⸻

### E. 当前唯一大闭环（做完 ARES 才算立住）

4 个 Case 各自通过还不够。最终需要一个**一个连续 E2E** 证明完整思想：

    User → Agent A 收到大任务
      → A 自主判断「任务太大」
      → A Spawn B / C / D（Peer，无 subAgent 身份）
      → B / C / D 并行工作（独立 Cognitive State）
      → 期间某 Agent 故障 → Kernel 恢复 Task → 新 Agent 从 checkpoint 接续
      → IPC 协作（B 反驳 / C 验证 / D 汇总）
      → A 做 synthesis → 返回最终结果

**状态：✅ 已落地（2026-08-19）**

- 连续测试：`internal/agentfabric/e2e_grand_loop_test.go`
  （`TestE2E_GrandLoop_CompleteAgentOS`）——把附件 D 的 4 个 Case 串成
  一个连续故事（spawn → 并行 → 父死子续 → IPC 协作 → 替代者接续 → synthesis）。
- 可运行 Demo：`examples/aresos-demo/main.go`（零依赖，`go run` 即出 7 步结果）；
  说明见 `examples/aresos-demo/README.md`。
- 两条都只复用公共 API（agentfabric / agentipc），**未扩展任何库层代码**，
  符合「Demo 是演示、不是新 API」的边界。

> 备注：Demo 的「agent 认知」是 demo 层决策函数（模拟一轮 ReAct），不是真实
> LLM。真实 LLM 版本见 `examples/26-runtime-scheduling-demo`（leader/sub 编排，
> 属 legacy 叙事，保留作对照）；本 Demo 是 peer-agent 模型的确定性验收。