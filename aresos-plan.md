# ARES Agent OS 开发计划（修订版 v2，2026-08-20）

定位：Agent Operating System

ARES 将 Agent 从一个被 Leader 调用的执行函数，演进为由 Kernel 管理的独立认知计算实体。

Agent 可以自主产生 Task、Spawn Agent、通过 IPC 协作、持有独立 Cognitive State，并可以被调度、暂停、恢复、抢占、死亡和重新恢复。

Agent decides. Kernel enforces.

⸻

## 0. 修订说明（v2 为什么改这份计划）

v1（2026-08-19）把 P0-P6 定义为「库能力 + 局部验收」，据此项目完成度约 **80%~85%**。
但 v1 真正想要的结果——真实 LLM Agent 自主决定拆分、Kernel 调度 Task、Peer Agent 通过
IPC 协作、Agent 死亡后由新 Agent 从 checkpoint 继续执行、最终不依赖 Leader 完成任务——
目前完成度约 **60%~65%**。

差距的根源不是缺原语，而是**「旧 Leader 入口 + 新 Task Fabric 执行器」的混合架构仍然是
生产默认路径**。taskfabric / agentfabric / agentipc / aresrecovery 四个子系统各自完整，
但还没有被一条真实 Agent 执行的生产闭环串起来。v1 附件 A-E 把它们串了起来，只存在于
库层测试与确定性 demo，不构成生产 runtime 验收。

本修订（2026-08-20，依据全面现状核对，见 §3）做四件事：

1. 保留 v1 的「核心模型修正」（§1）、「总体模型」（§2）、「四条架构纪律」（§7）——
   它们是架构基石，不改。
2. 用诚实基线（§3）替换旧版附件 A-E 的过度乐观结论。
3. 把开发顺序从「P0-P6 阶段」改写为「W1-W5 五档工作」（§4-§6）——重心从补 API
   转移到把已有原语合并成真实闭环。
4. 重新定义最终验收（§6）：确定性 demo 降级为单元级验收，真实 runtime E2E 才是完成依据。

**一句话：v2 的剩余工作不是再添加一个 API，而是打通生产闭环。**

⸻

## 1. 核心模型修正（2026-08-19 拍板，保留不变）

> 本节是对上面「定位」与下面「0. 总体模型」的精确化。它不是新增阶段，而是
> 定义 ARES 模型的不可动摇的边界。所有阶段（P0-P6 / W1-W5）的实现都必须符合本节。

### 1.1 Agent 本身没有等级

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

### 1.2 Agent 可以自主决定"我要怎么完成任务"

Kernel 从不告诉 Agent「先让 B 分析、再让 C 审查、最后你汇总」。Agent 自己认知
任务复杂度，自己决定是否需要协作、需要谁、怎么拆，然后自己发起 Spawn /
Request / Delegate。Kernel 不干预这个语义决策。

Agent 甚至可以完全独立完成一个复杂任务（「这个我自己能做」→ 自己执行）。
协作与否是 Agent 的认知产物，不是框架预定义。

### 1.3 Spawn 建立的是 provenance，不是 hierarchy

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

### 1.4 协作关系是运行时动态形成的

传统 Multi-Agent：Team = { Planner, Researcher, Coder, Reviewer }，角色提前定义。

ARES：Agent A 接到任务 → 自己判断是否需要协作 →
- NO → 自己执行
- YES → 找 Agent B / Agent C，甚至 B 反过来指出 A 的假设有误、
  B 让 C 验证、C 再 Spawn D。

Agent topology 是运行时动态形成的，而不是架构预定义的。

### 1.5 Scheduler 只看能力，不看身份等级

Kernel 可以记录 Agent B 的 Parent = A，但该字段的用途只有 provenance /
lifecycle / debug / audit，绝不用于：authorization、scheduling priority、
communication privilege。

A spawn B 不导致 A > B；B spawn C 不导致 B > C。

调度只看能力匹配 + 负载 + 置信度。例：
- A: Capabilities = [rust, llvm]
- C: Capabilities = [rust, security]
- 任务 rust/unsafe-analysis → C score 0.96 > A score 0.91 → C 拿到任务。

C 拿到任务不是因为它是 Leader，也不是因为它是 Parent，只是因为 C 当前最适合。

### 1.6 Agent 可以自主产生新的工作

Agent 不只是 execute(task)，而是完整的认知循环：

observe → reason → decide → create task → spawn/request/delegate →
observe results → reason again

Task Graph 本身可以是 Agent cognition 的产物。Kernel 负责：
Task Created → Ready → Schedule → Acquire → Execute。

Kernel 不负责：「为什么应该创建这个 Task？」——这是 Agent 的认知问题。

### 1.7 三个平面

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

### 1.8 因此 ARES 的核心定义修正为

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
等 Kernel 真跑起来再谈（见 §6.4 W4）。

### 1.9 OS 类比的边界：像到什么程度，不像到什么程度（2026-08-19 收敛）

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

## 2. 总体模型（保留不变）

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

## 3. 现状基线（2026-08-20 全面核对）

> 核对方法：逐项对照 v1 计划（aresos-plan.md v1）与代码库实际状态。
> 覆盖：internal/taskfabric、internal/agentfabric、internal/agentipc、
> internal/aresrecovery、cmd/ares（kernel/scheduler/serve）、examples/aresos-demo。
> 测试复核：`go test -race ./internal/taskfabric ./internal/agentfabric
> ./internal/agentipc ./internal/aresrecovery ./cmd/ares` 全部通过（2026-08-20）。
>
> ⚠️ 本节为 W1-W5 开工前的基线快照；G1/G3/G4/G6 已由 W1-W4 闭环（见 §5-§9 完成状态），
> 当前进度以 §5-§9 为准。

### 3.1 已完成：库层基础设施

| 阶段 | 代码位置 | 状态 |
|---|---|---|
| P0 Task Kernel（Task/Lease/Epoch/CAS Acquire/Release/Checkpoint/Yield/Complete/Fail/Lease Expiry） | `internal/taskfabric` | ✅ |
| P1.1 Execution Quantum（RunQuantum/ExecuteStep/ReAct 单轮 checkpoint/yield/resume） | `internal/taskfabric/quantum.go`、`cmd/ares/scheduler.go` | ✅ |
| P1.2/P1.3 队列与 stealing（共享 ReadyTasks 并发 drain 取代 per-agent queue + 显式 steal） | `cmd/ares/scheduler.go` | ⚠️ 语义完成，形态不同（已决策保留） |
| P2.1 DAG Ready（依赖完成后任务进入 READY，event-driven drain） | `internal/taskfabric/dag.go`、`cmd/ares/scheduler.go` | ✅ |
| P2.2 协作式抢占（高优任务在 quantum 边界 preempt，checkpoint 保留） | `internal/taskfabric/preempt_test.go` | ✅ |
| P2.3 Event Store（生命周期事件写入 + 事件重建测试） | `internal/ares_events`、`internal/taskfabric/event_store_test.go` | ✅ 库层；持久化语义见 W3 |
| P3.1 Cognitive State（CognitiveState/checkpoint/恢复接口） | `internal/agentfabric` | ✅ |
| P3.2 Context 三层（ContextView/SetTaskContext/SetPrivate + 隔离测试） | `internal/agentfabric/context.go` | ✅ |
| P3.3 Spawn（SpawnSpec/资源预算校验/provenance） | `internal/agentfabric`、`internal/aresrecovery` | ✅ 原语完成；生产 syscall 见 W2 |
| P4 Peer IPC（Send/Request/Reply/Delegate/Handoff/Subscribe） | `internal/agentipc` | ✅ 原语 + 局部生产接线 |
| P5 Recovery（lease expiry/requeue/checkpoint/replacement agent） | `internal/aresrecovery` | ✅ 库层完成；生产接线见 W1 |
| P6 Evolution（population/quota/spawn policy 接线） | `cmd/ares/serve_routine.go`、`internal/aresrecovery` | 🟡 接线完成，无反馈闭环，见 W4 |

### 3.2 未完成：真实闭环（v2 要做的核心）

| # | 缺口 | 当前事实（代码证据） |
|---|---|---|
| G1 | 生产恢复不产生可执行 Agent | `cmd/ares/kernel.go` 恢复循环只 `RequeueExpiredLeases()`；`aresrecovery.RecoverTaskCheckpoint` 生成的 `agentfabric.Agent` 不是 scheduler 注册的 `sub.Agent` executor——kernel.go 注释原文称之为 phantom-agent bug（"nobody can execute the task; it stalls LEASED"）。`RecoverFromAgentDeath`/`RecoverTaskCheckpoint` 生产零调用。 |
| G2 | 真实 LLM 不会自主 Spawn / Create Task | 生产 LLM executor（`sub.Agent`）没有受 Kernel 校验的 spawn syscall/tool；`Spawn` 只出现在单元/E2E 测试、`examples/aresos-demo`、recovery/evolution adapter。生产路径是 Leader.Process → TaskPlanner → TaskDispatcher → fabric。 |
| G3 | Leader 仍是必经入口 | `cmd/ares/agents.go` 恒建 `leader.Agent`+`sub.Agent`+`TaskPlanner`+`TaskDispatcher`；`cmd/ares/serve_routine.go:212` 恒调用 `createAgents`；无 Leader OFF 启动模式；scheduler executor registry 是 `map[string]sub.Agent`。 |
| G4 | 事件持久化 best-effort | `internal/taskfabric/fabric.go:500`：store append 失败 `_ = err`。内存状态已变、EventStore 未写，重启后事件日志与内存状态可能不一致。 |
| G5 | checkpoint 无正式 schema | `taskfabric.Task.Checkpoint any`（task.go:23），经 `fabricTaskMeta` envelope 传递；Task Shared / Agent Private / IPC 三类数据边界未固化；无版本迁移策略。 |
| G6 | Evolution 无反馈闭环 | 三个 loop（`kernel.go:526/572/684`）只在 `evolution.enabled` 时接线（`ares.yaml` 当前为 false），且只把 StrategyStore 参数应用到 Kernel；无运行结果归因、无失败率/成功率回写 scheduler scoring。 |
| G7 | 文档/示例仍是旧叙事 | `README.md` 保留 Leader/sub/team；`examples/04-multi-agent`、`examples/26-runtime-scheduling-demo` 是 legacy 叙事；`ares.yaml` 仍是 `agents.leader` + `agents.sub[type]`。 |

### 3.3 与 v1 附件的差异裁决

1. v1 附件 B3/E 声称「P3.4 端到端 + grand loop 已落地」——**准确但仅限库层**。
   `internal/agentfabric/e2e_grand_loop_test.go` 等测试确实通过，但它们直接操作
   fabric/agentipc API，不经过生产路径。
2. v1 附件 E 声称「状态：✅ 已落地」——**demo 结果是硬编码的**
   （`examples/aresos-demo/main.go:203`：`decision := "report: 17 unsafe blocks, ..."`），
   且全程手动 `SetCognitiveState`/`Spawn`/`Kill`/`A2`。它能证明「API 组合逻辑」，
   不能证明「ARES 真实 Agent OS runtime 已完成」。
3. v1 附件 P5「requeue-only + scheduler 重新调度」被当作生产恢复——**与计划最初要求
   「新 Agent 从 checkpoint 恢复」有差别**：恢复的是 Task 执行，不是新的
   agentfabric.Agent 执行实体恢复（即 G1）。
4. v1 附件 P1.2/P1.3「共享队列并发 drain 已决策保留」——**维持**，除非 profiling
   证明共享队列成为瓶颈。

⸻

## 4. 修订后的开发计划：W1-W5 五档工作

### 4.0 总体顺序（2026-08-20 冻结）

| 档位 | 核心问题 | 完成后的能力 | 依赖 |
|---|---|---|---|
| W1 | 恢复能不能让**新 Agent** 真正接续执行？ | 生产级恢复闭环（phantom-agent 消除） | 无 |
| W2 | 真实 Agent 能不能自主拆任务 + 无 Leader 也完成任务？ | 自主性 + Leader OFF 验收 | W1（恢复为自主 spawn 的底） |
| W3 | 事件与 checkpoint 够不够 durable？ | 一致性语义 + 固化协议 | W1（replacement 消费 checkpoint） |
| W4 | Evolution 能不能用运行结果改变下一轮调度？ | 完整反馈闭环 | W2（真实执行数据来源） |
| W5 | 代码之外的世界还旧吗？ | 文档/示例/配置对齐 | 可并行 |

> 明确的非目标（不测，同 v1）：CANCELLED/ZOMBIE 进程状态、墙钟时间片/硬抢占 LLM、
> CFS/PCB 级上下文切换、per-agent CPU 队列、模拟 Linux CFS 调度。

⸻

## 5. W1 生产级恢复闭环（最优先） ✅ 已完成

> **完成状态 (2026-08-20，code review 修复后)**：W1 全部完成。初版实现（
> `newReplacementExecutor`/`replacementExecutor`/`TasksReadyForRecovery`）经 code
> review 发现两个致命缺陷后被替换：① 恢复循环对**所有** READY 任务注册替换 executor，
> 劫持新建任务并以罐头成功完成；② 生产替换 executor 是假实现，不读 checkpoint、不跑
> LLM。修复后的最终形态：
>
> - ✅ scheduler 支持 executor 动态注册/注销（`RegisterExecutor`/`UnregisterExecutor`，
>   线程安全，`execMu` 保护）
> - ✅ **任务绑定注册** `RegisterExecutorForTask(taskID, agentID, executor)`：替换
>   executor 只服务被恢复的那一个任务（`execute()` 对绑定任务只给该 executor 当候选，
>   绑定 executor 对其它任务一律排除）；任务到终态（COMPLETED/FAILED）自动
>   `UnregisterExecutor`，注册表不会无界增长
> - ✅ `CheckExpiredLeases()`/`RequeueExpiredLeases()` 改为返回**实际过期任务 ID 列表**
>   （`[]string`），恢复链只处理本次真正过期的任务，不再扫描全部 READY
> - ✅ 恢复循环升级为完整恢复链：requeue → 若无可用 executor 才 spawn replacement →
>   **绑定到具体任务** → 注册 → resume quantum；`hasCapableExecutor` 门控避免无谓 spawn
> - ✅ checkpoint 被真实 executor 消费：`toModelTask`/`extractTaskMeta` 解码
>   `fabricTaskMeta` 与 create_task 的 `FabricTaskMetaEnvelope`，StepCheckpoint 作为
>   `payload["checkpoint"]` 供续跑
> - ✅ **生产恢复使用真实 executor**：peer 模式恢复 factory 用 `newPeerExecutor`
>   （完整 sub.Agent + LLM + tools），从 checkpoint 真实续跑；leader 路径 requeue-only
>   （已有 sub-agent executor 直接续跑）
> - ❌ 已删除：`newReplacementExecutor`/`replacementExecutor`（罐头成功假实现）、
>   `TasksReadyForRecovery()`（返回全部 READY 的劫持源头）
> - ✅ E2E 测试 `TestW1RecoveryClosureE2E`：checkpoint 消费 + 绑定注册 + 真实 crash 路径
> - ✅ `TestW1RegisterExecutorDynamic` / `TestW1UnregisterExecutor`：动态注册/注销
> - ✅ `RecoverFromAgentDeath` / `RecoverTaskCheckpoint` 存在生产调用者
>   （`runKernelRecoveryLoop`）

### 5.1 目标

把 `aresrecovery` 从「库测试通过」升级为「生产恢复真实成立」：
Agent 死亡后，Kernel 创建新 Agent A'，A' 从旧 Agent 的 CognitiveState checkpoint
恢复，**注册为 scheduler 可执行的 executor**，并从 checkpoint 继续执行后续 quantum。

### 5.2 现状

- `kernelScheduler.executors map[string]sub.Agent`——scheduler 只能执行已注册的
  `sub.Agent`。
- `cmd/ares/kernel.go` 恢复循环只做 `RequeueExpiredLeases()`，新任务由 scheduler
  从**已有** executor 池重新选择（"旧 Agent 死亡 → Task READY → 已有 executor 接手"）。
- `aresrecovery.RecoverTaskCheckpoint` / `RestartAgent` 能生成 `agentfabric.Agent`，
  但它没有 `ExecuteStep`，也不是 scheduler 注册的 executor → phantom-agent bug：
  任务被 LEASED 却无人能执行，直到 lease 过期。

### 5.3 工作项

1. **scheduler 支持 executor 动态注册/注销**：`kernelScheduler` 增加
   `RegisterExecutor(agentID, executor)` / `UnregisterExecutor(agentID)`，
   线程安全，供恢复路径在运行时注入 replacement。
2. **让 replacement agent 具备 ExecuteStep 能力**：为 `agentfabric.Agent` 增加
   `ExecuteStep`（或建立 adapter 到 `sub.Agent` executor 接口），使恢复出的实体
   能被 scheduler 真正执行。
3. **replacement agent 注册进 scheduler**：`aresrecovery` 恢复链路完成后，把新
   agent 注册为 executor；恢复失败时回滚注册并释放 lease。
4. **checkpoint 被实际 executor 消费**：A' 的 `ExecuteStep` 输入必须包含 A 写入
   checkpoint 的 `StepCheckpoint`（`cmd/ares/scheduler.go` 的 `toModelTask` 已具备
   解码能力，恢复路径要复用同一解码逻辑）。
5. **kernel recovery loop 升级为完整恢复链**：`cmd/ares/kernel.go` 的
   `runKernelRecoveryLoop` 从「只 requeue」改为「requeue → spawn replacement →
   register executor → acquire → resume quantum」，消除 phantom-agent 降级路径。
6. **失败注入走真实路径**：新增 E2E 必须通过 lease expiry / crash path 触发恢复，
   禁止测试直接手动创建 A2。

### 5.4 验收

- 连续 E2E：Task → executor A 执行 quantum#1（写入 checkpoint）→ A 崩溃（真实
  crash/lease expiry）→ recovery 循环 → 新 executor A' 注册 → scheduler 调度 A'
  → A' 的 quantum#2 观察到 quantum#1 的中间状态 → COMPLETE。
- 断言 checkpoint 消费：A' 的 ExecuteStep 输入包含 A 保存的 StepCheckpoint，
  而不是从零开始。
- 断言 recovery 后任务由**新** executor 执行（A' 与 A 不是同一实体）。
- `RecoverFromAgentDeath` / `RecoverTaskCheckpoint` 存在生产调用者（不再是
  test-only）。
- 全量 `go test -race ./...` 通过，新增测试覆盖上述路径。

⸻

## 6. W2 真实 Agent 自主性与无 Leader 验收 ✅ 已完成

> **完成状态 (2026-08-20)**：W2 全部完成。
>
> - ✅ `internal/agentsyscall` 新包：`spawn_agent` / `create_task` Kernel syscall +
>   校验（capability / quota / provenance）+ `ToolSchemas` + `BindTools`，经共享
>   ToolBinder 暴露给真实 LLM executor（`sub.Agent`）
> - ✅ `create_task` 创建真实 Task Fabric Task（Create → READY，payload 经
>   `FabricTaskMetaEnvelope` 携带，`toModelTask`/`extractTaskMeta` 解码）
> - ✅ `spawn_agent` 经 `CognitionFactory`/ExecutorFactory 产出完整 sub.Agent 并注册为
>   scheduler executor（生产路径 `createPeerAgents` 注入真实 factory）
> - ✅ 无 Leader 启动模式：`kernel.leader_enabled: false`（`KernelConfig.LeaderEnabled`，
>   默认 nil→true 兼容）+ `createPeerAgents` + `createAndServeAgents` 按配置分发；
>   `serve.go` 在 Leader OFF 下跳过 leader 组装/DAG/autopilot
> - ✅ 调度以 capability 为核心：`CapabilityExecutor` 接口（ID/Type/ExecuteStep）取代
>   scheduler 对 `sub.Agent` 的强绑定；`minimalCapabilityExecutor` 编译期证明非 sub.Agent
>   也可调度（W2-5）
> - ✅ Peer IPC 协作原语（`agentipc`）+ `e2e_spawn_synthesis_test.go` 合成闭环
> - ✅ 验收测试：Case 1 独立完成 / Case 2 自主拆分 / Case 3 父死子续 / Case 4 真正协作 /
>   `TestW2LongTaskStability` / `TestW2LeaderOffConfig`
> - ✅ 旧 Leader 路径标记 legacy compat（`agents.go` 注释、`kernel.policy=legacy`）

### 6.1 目标

- 真实 LLM Agent 在运行中**自主判断**是否拆分任务，通过受 Kernel 校验的
  syscall/tool 创建 Task / Spawn Agent，而不是由 Leader 的 TaskPlanner 预先拆分。
- 关闭 Leader 后，一组平等 Peer Agent 直接注册到 Kernel，仍能完成任务。

### 6.2 现状

- 生产入口固定为：User → `leader.Agent.Process` → `TaskPlanner.Plan` →
  `leader.TaskDispatcher` → Task Fabric → `sub.Agent`。
- `agentfabric.Spawn` 的调用只出现在测试、`examples/aresos-demo`、
  recovery/evolution adapter；真实运行的 LLM executor 没有自主 Spawn 的闭环。
- `ares.yaml` 仍是 `agents.leader` + `agents.sub[type]`；无 Leader OFF 模式。

### 6.3 工作项

1. **给真实 Agent 暴露 spawn syscall/tool**：新增 `spawn_agent` / `create_task`
   tool（或等价 Kernel syscall），参数经 Kernel 校验（capability / resource /
   quota），校验通过才创建 Task 并进入 fabric。对真实 LLM executor（`sub.Agent`）
   与 `agentfabric.Agent` 一致暴露。
2. **Spawn 结果转换为 Task Fabric Task**：`spawn_agent` 创建真正的
   `taskfabric.Task`（Create → READY），进入 scheduler 的 ReadyTasks 队列。
3. **LLM 自主决定拆分**：拆分语义由 Agent cognition 产出（tool call 参数），
   `TaskPlanner` 不再作为默认拆分者；旧 planner 路径降级为 legacy。
4. **子任务进入 scheduler**：由 Kernel 统一调度 B/C/D，而不是由 Leader dispatcher
   管理。
5. **父 Agent 通过 IPC 收集结果并 synthesis**：A spawn B/C/D 后通过
   `agentipc`（Request/Reply）收集子结果，自行 synthesis。验证结果确实来自
   B/C/D 的输出，而不是写死。
6. **无 Leader 启动模式**：允许一组平等 Agent 直接注册到 Kernel（跳过
   `createAgents` 的 leader 组装）；`serve_routine.go` 增加 Leader OFF 分支。
7. **调度以 capability 为核心**：移除 scheduler 对 `sub.Agent` 类型/角色的强绑定，
   候选打分只依赖 capability + load + confidence + priority。
8. **旧 Leader 路径标记为 legacy compat**：`wireKernelPolicy` 的 legacy 分支明确
   标注为兼容层，不是默认心智模型。

### 6.4 验收

- **Case 1（独立完成）**：无 Planner、无 Leader，单个 Agent 自主完成任务 → COMPLETE。
- **Case 2（自主拆分）**：Agent A 判断任务复杂 → 调用 `spawn_agent` → Kernel 校验
  → 创建 Task → B/C 进入 scheduler 执行 → A 通过 IPC 收集结果 → A synthesis →
  COMPLETE。拆分是 A 的 tool call 决定，不是框架预定义。
- **Case 3（父死子续）**：A 死亡 → B/C 继续 → 被 A spawn 的任务不死亡 →
  recovery 链路接续（依赖 W1）。
- **Case 4（真正协作）**：A→B 请求、B→A 回复、B→C 验证、C→D spawn；A ≡ B ≡ C ≡ D，
  无权限差异。
- **Leader OFF 验收**：`agents.leader` 关闭后，用户任务仍能完成；调度全程无
  role/type 参与，只有 capability/load/confidence/priority。
- 上述 Case 必须通过**真实生产路径**（scheduler + fabric + executor + IPC），
  不能只靠库层 E2E 测试。

⸻

## 7. W3 Durability 与协议稳定性 🟡 部分完成

> **完成状态 (2026-08-20，code review 修复后)**：
>
> - ✅ EventStore 一致性语义：区分 must-persist 事件（TaskCreated/Checkpointed/
>   Completed/Failed/Expired）与 observability 事件
> - ✅ 关键事件失败策略：`isMustPersistEvent` + `record` 中 must-persist 事件 append
>   失败时 log（不再 `_ = err` 静默吞错）
> - ✅ store 故障测试：`TestW3MustPersistEventFailureIsLogged` /
>   `TestW3ObservabilityEventFailureIsSilent` / `TestW3StoreFailureDoesNotBreakStateMachine`
> - 🟡 checkpoint schema 固化（库层）：`CheckpointEnvelope` 带 `schema_version` +
>   `DecodeCheckpoint`/`EncodeCheckpoint`/`MarshalCheckpoint` + 测试（RoundTrip /
>   RejectsFutureVersion / NilAndRaw / MarshalWrapsRawValue）
> - 🟡 **统一解码**：scheduler 侧的解码统一在 `toModelTask`/`extractTaskMeta`
>   （兼容 `fabricTaskMeta` 与 create_task 的 `FabricTaskMetaEnvelope` 两种 envelope）；
>   库层 `taskfabric.DecodeCheckpoint` 尚未被 scheduler 采用（scheduler 未迁移到
>   `CheckpointEnvelope`，旧协议与新 schema 并存）——剩余迁移项

### 7.1 目标

把「best-effort 事件 + opaque checkpoint」升级为「可验证的 durable 协议」，
保证重启后事件日志能重建状态、checkpoint 能被 scheduler/recovery/executor
统一解码。

### 7.2 现状

- `internal/taskfabric/fabric.go:500`：store append 失败静默忽略。
- `taskfabric.Task.Checkpoint any`；scheduler/recovery 通过 `fabricTaskMeta`
  envelope 约定字段，无 schema、无版本。

### 7.3 工作项

1. **定义 EventStore 一致性语义**：区分「必须持久化的事件」（TaskCreated /
   Checkpointed / Yielded / Completed / Failed）与「可观测性事件」（Trace /
   观测指标）。
2. **关键事件失败策略**：must-persist 事件 append 失败时，至少告警 + 有限重试 +
   recovery 策略；禁止静默吞错。
3. **固化 checkpoint schema**：定义 Task Shared State / Agent Private State /
   IPC message state 三类数据边界；加入 `schema_version` 字段；定义版本迁移策略。
4. **统一解码**：scheduler、recovery、executor 对 checkpoint 使用同一解码函数
   （把 `cmd/ares/scheduler.go` 的 `toModelTask` 解码逻辑提升为共享库函数）。
5. **store 故障测试**：append 失败 → 不静默丢失 durable state；「写入失败后重启
   恢复」的测试（replay 结果与内存状态一致或可检测偏差）。

### 7.4 验收

- 文档化的一致性语义 + 关键事件清单。
- 强制失败路径测试：EventStore 注入故障 → 断言系统行为符合策略（告警/重试/阻断），
  内存状态与事件日志不一致可被检测。
- checkpoint schema 带版本字段，跨版本迁移有测试。
- 长期不再以裸 `any` 作为跨重启协议。

⸻

## 8. W4 Evolution 完整反馈闭环 ✅ 已完成

> **完成状态 (2026-08-20，code review 修复后)**：W4 全部完成。初版实现只有库层测试
> （`ExecutionAttribution` + `EvolutionFeedbackAdapter` + `RunEvolutionFeedbackLoop`），
> 但 `attribution.Record` 生产零调用、`RunEvolutionFeedbackLoop` 未启动。修复后：
>
> - ✅ 采集真实执行结果：`ExecutionAttribution` 结构体，per-agent + per-capability
>   成功/失败跟踪
> - ✅ **生产接线**：`scheduler.executeWithCandidates` 在 `loadTracker.end` 处调用
>   `attribution.Record(winner, taskCapability, success)`（`createPeerAgents` 装配
>   `WithAttribution`）
> - ✅ 回写 scheduler scoring：`EvolutionFeedbackAdapter` + `ConfidenceInjector` 接口 +
>   `loadTracker.SetAgentConfidence`
> - ✅ **反馈 loop 生产接线**：`createPeerAgents` 启动 `RunEvolutionFeedbackLoop(ctx,
>   adapter, 10s)`；leader 路径暂未接线（leader 路径的 feedback 是未来迁移项）
> - ✅ `loadTracker.Confidence` 支持 evolution override（`confidenceOverride` map）
> - ✅ 策略改变测试：`TestW4EvolutionFeedbackChangesSchedulerBehavior`（执行结果 →
>   confidence 更新 → scheduler 行为改变）
> - ✅ per-capability 归因测试：`TestW4CapabilityConfidenceAttribution`
> - ✅ 幂等性测试：`TestW4FeedbackAdapterApplyIsIdempotent`
> - ✅ nil 安全测试：`TestW4FeedbackAdapterNilSafe`

### 8.1 目标

Evolution 从「周期性应用策略参数」升级为「运行结果 → 经验统计 → 策略变化 →
下一轮调度可观测改变」的完整闭环。

### 8.2 现状

- `cmd/ares/kernel.go` 三个 loop（quota / population / spawn）已接线，但只把
  StrategyStore 参数应用到 Kernel。
- 无调度结果按 capability / agent / task 类型归因；无失败率/成功率回写
  scheduler scoring；无「policy update → scheduler behavior change」测试。
- `ares.yaml` `evolution.enabled: false`，生产默认不启用。

### 8.3 工作项

1. **采集真实执行结果**：从 fabric 完成/失败事件与 `loadTracker` 归因
   （capability、agent、task 类型、success/fail）。
2. **回写 scheduler scoring**：把归因结果写入 `loadTracker.Confidence` /
   capability 分数，使失败多的 agent/capability 在下一轮被降权。
3. **Evolution 修改 capability matching / spawn 策略**：策略变化必须实际改变
   `taskfabric.Score` 的参数（权重/置信度），而不是只改变 population 数量。
4. **Evolution 建议任务拆分策略**：如某类任务频繁失败 → 建议自动 spawn reviewer。
5. **测试证明策略改变运行行为**：一个 policy update → scheduler behavior change
   的端到端测试（更新前调度给 A，更新后调度给 C，且差异可断言）。

### 8.4 验收

- 归因数据落库 + 有查询接口。
- scheduler 打分随运行结果变化（测试断言）。
- Evolution 参数更新在下一轮调度产生可观测差异（测试断言）。
- `evolution.enabled` 打开时完整闭环生效。

⸻

## 9. W5 清理旧架构叙事 ✅ 已完成

> **完成状态 (2026-08-20)**：W5 完成（文档/示例层面）。
>
> - ✅ `examples/04-multi-agent/main.go`：标注为 LEGACY COMPATIBILITY
> - ✅ `examples/26-runtime-scheduling-demo/main.go`：标注为 LEGACY COMPATIBILITY
> - ✅ `docs/en/framework-comparison.md`：ARES 核心抽象更新为 Peer Agent + Kernel
> - ✅ `docs/framework-comparison-langchain-crewai-agentscope-goagent-en.md`：更新架构描述
> - ✅ `docs/zh/README.md`：Leader/Sub 标注为 (legacy)
> - ✅ `README.md`：已包含 Peer Agent 叙事（0.3.0 更新）
> - ✅ `examples/aresos-demo/README.md`：已为 Peer Agent 示例

### 9.1 目标

让代码、配置、文档、示例的叙事与 Peer Agent 模型对齐，避免架构方向漂移。

### 9.2 现状

- `README.md` 保留 Leader/sub orchestration、team、Leader Failover 等表述。
- `examples/04-multi-agent`、`examples/26-runtime-scheduling-demo` 是 legacy 叙事。
- `ares.yaml` 仍是 `agents.leader` + `agents.sub[type]`。
- `parentID` 被写入监控 metadata 并可能展示为角色关系。

### 9.3 工作项

1. `examples/04-multi-agent`、`examples/26-runtime-scheduling-demo` 标注为 legacy
   对照实现。
2. 新增 peer-agent runtime 示例（capability-based agents + 自主 spawn + IPC +
   synthesis），提升为默认示例。
3. 更新配置示例为 capability-based agents。
4. 更新 docs 中 Leader/Sub 的定位（legacy compatibility，不是默认模型）。
5. 明确 `parentID` 只有 provenance 语义，不是权限/调度等级/通信特权。

### 9.4 验收

- 文档与示例不再把 Leader/Sub 作为默认心智模型。
- 新 peer-agent 示例可运行，且不依赖 leader。

⸻

## 10. 最终验收：Agent OS Runtime E2E（重定义）

v1 的最终验收是 `examples/aresos-demo`（确定性模拟）。v2 把它重定义为**两层**：

### 10.1 单元级验收（保留 v1 demo，降级为基线）

`examples/aresos-demo` 继续保留，作为「API 组合逻辑」的确定性基线。它证明：
库原语齐全、手动组合可跑通 7 步故事。**它不再作为「Agent OS 已完成」的证据。**

### 10.2 runtime E2E（完成依据）

新增一个连续 runtime E2E，**至少使用 fake LLM**（可注入确定性输出），但真实经过
完整生产路径：

    User → 任务创建（fabric.Create / scheduler）
      → Agent A 执行 quantum#1（真实 ExecuteStep + checkpoint）
      → A 自主决定拆分 → spawn_agent tool → Kernel 校验 → Task B/C/D READY
      → scheduler 按 capability 调度 B/C/D（真实 Schedule/Acquire/RunQuantum）
      → B 在 quantum 边界 yield → 恢复 / 续跑
      → C 通过 IPC Request/Reply 与 A 协作
      → B 崩溃 → lease expiry → recovery 创建 B' 并注册 → B' 从 checkpoint 续跑
      → 各结果经 IPC 汇聚 → A synthesis（结果来自 B/C/D 输出，非写死）
      → 最终结果返回

覆盖的机制：Task Fabric（Create/Schedule/Acquire/Lease expiry）· Kernel Scheduler
（quantum 调度）· ExecuteStep · Checkpoint · Yield · Recovery（replacement
executor 注册）· Peer IPC · synthesis。

### 10.3 可选：真实 LLM demo

真实 LLM 版本可以存在（复用 `examples/26-runtime-scheduling-demo` 的 LLM 接入），
但**不把 LLM 输出作为唯一测试依据**——LLM 输出只作为补充演示，验收断言全部基于
fake LLM 的确定性路径。

### 10.4 synthesis 判定规则

测试不能通过比对写死的最终报告来判断 synthesis 成功。必须验证最终结果确实
**来源于 B/C/D 的输出**（例如：synthesis 输入 = B/C/D 的 ExecuteStep 产物，
断言结果字段与子 Agent 输出逐项对应）。

⸻

## 11. 最重要的四条架构纪律（保留不变）

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

"帮我分析这个"
        → IPC
"谁现在执行它？"
        → Scheduler

④ 能不能不做？

如果只是为了：

"更像操作系统"

而不是解决真实 Agent runtime 问题：

不做。

⸻

## 12. 愿景（不变）

ARES is an Agent Operating System.

Agents are autonomous cognitive processes, not functions invoked by an orchestrator. They independently create work, communicate as peers, maintain private cognitive state, and may spawn other agents. The ARES Kernel provides scheduling, synchronization, IPC, resource enforcement, lifecycle management, and recovery.

Agents decide. The Kernel enforces.

Agent death is an execution failure, not a task failure.

⸻

## 13. 附：v1 P0-P6 已完成项（基础设施，保留不再开发）

> 以下为 v1 已定义且已实现的库层原语。v2 不再把它们作为开发项，
> 仅在对应工作流（W1-W5）需要时引用。详细 API 定义见 v1 备份
> `aresos-plan.md.v1.bak`。

- **P0 Task Kernel**：Task、Lease、Epoch、CAS Acquire、Release、Checkpoint、
  Yield、Complete、Fail、Lease Expiry、Event。验收含竞争/fencing/recovery，
  已有测试。
- **P1.1 Execution Quantum**：`RunQuantum(taskID, agentID, epoch, step)`；
  Done→COMPLETED / Err→FAIL / !Done→SUSPENDED。已接入 scheduler
  `ExecuteStep` + `ResumableTasks` 续跑。
- **P1.2/P1.3 AgentQueue + Work Stealing**：已决策用共享 ReadyTasks 队列 +
  并发 drain 取代 per-agent queue + 显式 steal（steal.go 已删除）。
- **P2.1 DAG Ready**：IsReady / ReadyTasks / event-driven DAG 完成。
- **P2.2 Cooperative Preemption**：高优任务在 quantum 边界 preempt，checkpoint
  保留（preempt_test.go）。
- **P2.3 Event Store**：TaskCreated…TaskStolen 事件进入 EventStore，事件重建
  测试存在；持久化语义见 W3。
- **P3.1 Cognitive State**：`CognitiveState{Context,Observations,WorkingMemory,
  Decisions,ToolState,Checkpoint}` + 恢复接口。
- **P3.2 Context 三层**：ContextView / SetTaskContext / SetPrivate + 隔离测试。
- **P3.3 Spawn**：SpawnSpec + 资源/配额校验 + provenance；生产 syscall 见 W2。
- **P4 Peer IPC**：Send / Request / Reply / Delegate / Handoff / Subscribe。
- **P5 Recovery**：lease expiry → requeue → checkpoint resume → agent restart；
  chaos/sandbox 走 agent-fabric-as-executor 路径；生产接线见 W1。
- **P6 Evolution**：population / quota / spawn policy 接线；反馈闭环见 W4。

⸻

## 14. 修订记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2026-08-19 | 初始计划：P0-P6 阶段定义 + 附件 A-E 现状核对 |
| v2 | 2026-08-20 | 依据全面核对（见 §3）修正：承认库层完成但生产闭环未通；P0-P6 改写为 W1-W5 五档工作；最终验收升级为 runtime E2E；v1 备份于 `aresos-plan.md.v1.bak` |
| v2.1 | 2026-08-20 | W1-W4 落地 code review 修复后更新进度：W1 恢复闭环改任务绑定 + 真实 executor；W2 完成（agentsyscall + Leader OFF）；W3 更正为部分完成（schema 库层就绪、scheduler 未迁移）；W4 补生产接线 |
