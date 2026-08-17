# ARES Runtime — 设计冻结（0.3.0 核心）

> 状态：设计冻结 + **P0-P5 已实施**。本文是 Task Fabric / Agent Fabric / Scheduler 的权威模型。
> 定位：ARES 从 "Agent Orchestration Framework"（leader+sub）演进为
> **"面向 Agent 的动态计算运行时"**——Agents are not orchestrated. They are scheduled.

## 实施进度矩阵（2026-08-16 更新）

> 与代码实际进度对齐。P0-P5 积木层与生产接线层（cmd/ares Kernel 组装入口、
> planner→DAG 接线、live mid-run flip、P5 资源强制）全部已实现并有测试守护。

| 阶段 | 内容 | 实现包 | 完成度 | 说明 |
|------|------|--------|--------|------|
| **P0** | Task Fabric 核心原语（状态机/Lease/fencing/事件） | `internal/taskfabric/` | ✅ 100% | fabric/task/state/lease/events，7 状态 + epoch 校验 |
| **P1** | Quantum + Capability Scheduler + Work Stealing + ConfidenceSource | `internal/taskfabric/` + `internal/ares_skills/experience_confidence.go` | ✅ 100% | RunQuantum / Schedule(Score=cap×load×conf) / Steal / Skill-first 接线 |
| **P2** | DAG 调度源 + 抢占 + 事件升级全量 | `internal/taskfabric/` + `internal/ares_events/` | ✅ 100% | IsReady/ReadyTasks / Preempt(priority+cooperative) / task.* 事件持久化 |
| **P3** | Agent Fabric（spawn/suspend/resume/retire/kill/recover + Process Tree + Cognitive State + Context 三层） | `internal/agentfabric/` | ✅ 100% | spawn=provenance 非 hierarchy；Parent 死 ≠ Child 死 |
| **P4** | IPC 升级（Send/Request/Reply/Delegate/Handoff/Subscribe）+ 双轨迁移（PolicyFlag + DualTrackDispatcher） | `internal/agentipc/` | ✅ 100% | peer 原语全齐；cmd/ares Kernel 组装入口 + live mid-run flip 均已接线 |
| **P5** | Recovery + Chaos + Evolution | `internal/aresrecovery/` | ✅ 100% | RequeueExpiredLeases/RecoverTaskCheckpoint/RestartAgent/FullRecoveryChain + 资源强制（agentfabric spawn 配额校验） |
| **§8** | Capability-aware scheduling 与 Skill-first 打通 | `internal/ares_skills/experience_confidence.go` | ✅ 100% | Experience BestMatch → ConfidenceSource，编译期断言 |

**接线状态（0.3.0 生产路径）**：
- 🟢 **cmd/ares Kernel 组装入口**：`wireKernelDispatcher` 组装 `PolicyFlag + DualTrackDispatcher`（shadow=on）并包进 leader 实际 dispatcher——**shadow 在生产路径转起来**（Mismatches 可观测）；**flag 翻转已接线**：`cfg.Kernel.Policy="taskfabric"` 时 `wireKernelPolicy` 翻转 flag、把 shadow 评分器替换为真实 `executeFabricTask`（Create→Schedule→Acquire→RunQuantum）、关闭 shadow（防双跑）、启动 `kernelScheduler` 接管 `ReadyTasks` 消费。`agentipc` 新增 `SetShadow`/`SetNewPath`/`NewPath` 支持运行时切换。config `kernel.policy` 默认 `legacy`（安全）。**已实现、测试覆盖（-race）**。`make fmt && make check` 全绿。
- 🟢 **planner → DAG 接线**：`SubAgentConfig.Dependencies`（config `subagents[].dependencies`）→ leader planner 产出 `models.Task.Context.Dependencies`（sub-agent ID 解析为 task ID，未选中依赖丢弃防死锁）→ kernel 派发 payload 透传 → `executeFabricTask` 带依赖 Create + `IsReady` 门控（依赖未完成只注册不执行，`kernelScheduler` 的 `ReadyTasks` 在依赖完成后接管）——DAG 成为真实调度源，leader 不再决定 "B now"。
- 🟢 **live mid-run flip**：`flipKernelToTaskFabric(ctx, kernel, subAgents)` 幂等入口——关 shadow → 换真实执行器 → 翻转 flag → 启动 scheduler；顺序保证翻转竞态无双跑、在途 legacy 派发同步完成不 orphan（测试：dispatch 与翻转并发 60 任务零丢失零双跑，-race）。`DualTrackDispatcher.Dispatch` 改快照读（修 `SetShadow`/`SetNewPath` 并发竞态）。
- 🟢 **P5 资源强制**：`agentfabric` 新增 `WithResourceBudget` 命名配额（如 `{"cpu": 8}`），`Spawn` 准入校验（超配额 `ErrResourceQuotaExceeded`，先校验后落盘），`Kill`/`Retire` 释放配额（双重释放幂等），并发 spawn 不超卖（-race 测试）。
- 🟢 全部积木包测试全绿：`go test ./internal/taskfabric/ ./internal/agentfabric/ ./internal/agentipc/ ./internal/aresrecovery/ ./internal/ares_skills/`。

---

## 一、核心命题

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
    // recovery / 性能度量都挂在 Execution 层。P0 不实现 Execution 实体，只留边界。
    Executions []any               // 预留：[]*Execution（P1+ 填充）
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
TaskLease / ResourceLease / CapabilityLease —— 全部从现有 `SessionLease`（internal/agents/lease）
抽象而来：`Acquire(id, owner, ttl) / Renew / Release`，TTL 过期自动失效。

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

## 六、四个核心原语（0.3.0 Runtime 地基）

```go
Acquire(taskID, agentID, ttl) (epoch, error) // CAS owner=agentID，READY→LEASED；返回 fencing token（epoch）
Release(taskID, agentID, epoch) error         // 归还，LEASED/RUNNING→READY；校验 epoch，防 "A 过期→B acquire→A 迟到 Release" 误杀 B
Yield(taskID, agentID, epoch, checkpoint)     // Quantum 边界：仅交回执行权；状态由 Scheduler 决策（continue/suspend/preempt/handoff/complete）
Checkpoint(taskID, state) error               // durable progress 落盘/事件
// P1 扩展：
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
能力/经验来源——之前的 Skill-first 设计被真正用起来。

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

## 十一、分阶段实施路线

- **P0（已完成）**：Task Fabric 核心原语——Task/Lease/状态机/Acquire·Release·Yield·Complete·Fail·CheckExpiredLeases/事件日志 + fencing token（epoch 校验）。验收：CAS 竞争 / lease 过期回收 / 状态机非法转换拒绝 / 事件日志重建 / make check 全绿。
- **P1（已完成）**：Quantum 执行（RunQuantum：done→COMPLETE / err→FAIL / 未完成→SUSPENDED，D1）+ Capability Scheduler 接入（Schedule = Pick + Acquire，D2）+ Work Stealing（per-agent 队列 + capability-aware Steal，D2）+ ExperienceConfidenceSource 适配器（ares_skills.Experience → taskfabric.ConfidenceSource，Skill-first 落地）。验收：Quantum 三态 + StaleEpoch / Schedule 选优 C 胜 + 无能力拒绝 + 已持有拒绝 / Steal 能力过滤 + 原队列完整 / go test -race 全绿 / gofmt+vet+build 全绿。
- **P2（已完成）**：DAG 作为调度源（IsReady / ReadyTasks，D3 手工构造 Dependencies）+ 任务抢占（priority + cooperative）+ 事件升级全量（接入 ares_events EventStore.Append）。验收：DAG 依赖门控 A→B→C 逐级 ready / 抢占后 checkpoint 保留 + B 可 acquire / 事件落库跨重启重建 / go test -race 全绿。
- **P3（已完成）**：Agent Fabric——spawn / suspend / resume / retire / kill / recover（**spawn establishes provenance, not hierarchy**）+ 进程树（Process Tree ≠ Scheduling Graph）+ Agent Cognitive State（可独立 checkpoint）+ Context 三层（Task Shared / Agent Private / IPC Messages）。验收：spawn 不建层级 / Parent 死 ≠ Child 死 / 认知状态可独立恢复 / Context 三层不混 / go test -race 全绿。
- **P4（已完成）**：IPC 升级——Send / Request / Reply / Delegate / Handoff / Subscribe（peer 直发升级）+ leader 降级为 policy（Legacy Leader Policy → Task Fabric → Scheduler → Agent，并行 + feature flag 渐进切换）。验收：IPC 消息往返 request/reply / Handoff 任务交接 / flag 下新旧双轨等价 / go test -race 全绿。
- **P5（已完成）**：Recovery 子系统（lease expiry → requeue / checkpoint 恢复 / agent restart）+ Chaos（Failure Injection + Recovery Verification，复用 ares_runtime arena）+ Evolution（Runtime Adaptation：改调度策略 / agent 种群 / spawn 决策）+ Context 分层接线 + 文档/基准。验收：完整“Agent 死亡 ≠ Task 死亡”链路（注入→恢复→完成）/ Chaos 注入可恢复 / Evolution 可改调度/种群 / go test -race 全绿。

### P1 敲定实施方案（2026-08-16 用户拍板；P0 验收后执行，P1-P4 暂不提前实现）

| 决策点 | 敲定 |
|--------|------|
| D1 Quantum 语义 | 未完成统一经 SUSPENDED（cooperative 语义）；yield = execution boundary，continue 由 Scheduler 决策 |
| D2 Work Stealing | Scheduler 统一编排：ReadyTasks → Schedule（Pick+Acquire）→ 入队；空闲 agent Steal（capability-aware）→ Acquire |
| D3 DAG 数据源 | P1/P2 用 `Task.Dependencies` 手工构造；planner / live DAG 接入放 P4 迁移一起做 |
| D4 leader 迁移 | 并行 + feature flag 渐进（Legacy Leader Policy → Task Fabric → Scheduler → Agent），验证稳定后再降级为 policy |
| D5 Chaos 接入 | 复用 `internal/ares_runtime`（arena 故障注入）；Chaos = Failure Injection + Recovery Verification，Recovery（lease expiry/requeue/checkpoint）独立职责 |

**明确不提前实现**（避免过度设计，留待自然长出）：Auction/bidding、Agent migration、
分布式/多级 Scheduler、Actor 模型完整实现、Agent population 优化、自动 spawn/clone 策略、
复杂资源分配、真正的硬抢占、Execution 实体、新数据库/新消息系统。

### 每阶段详细设计（2026-08-16 用户补充 Kernel 模型后完善；每阶段完成汇报用户处理后再继续）

#### P1（Scheduler 支柱·基础）——Quantum + Capability Scheduler + Work Stealing
- **目标**：Scheduler 支柱的最小闭环——单任务可被 quantum 化执行、按能力选优、空闲偷取。
- **API/数据结构**：
  - `RunQuantum(taskID, agentID, epoch, step QuantumStep) error`——`step` 返回 `(checkpoint, done, err)`；done→COMPLETE / err→FAIL（重试回 READY）/ 未完成→SUSPENDED（checkpoint 保留）。
  - `Schedule(taskID, candidates []Candidate, ttl) (winner, epoch, error)`——`Pick`（scheduler.go 已冻结的评分）→ `Acquire`；无能力候选 `ErrNoCapableCandidate`；CAS 保持。
  - `AgentQueue{AgentID; mu; tasks}` + `Steal(from, capabilities, capabilityOf) (taskID, ok)`——capability-aware 过滤（Score>0 才偷）。
- **测试**：Quantum 三态 + StaleEpoch；Schedule 选优（C 胜）/ 无能力拒绝 / 已持有拒绝；Steal 过滤 + 原队列完整。
- **验收**：未完成统一经 SUSPENDED（D1）；评分选优（D2）；偷取能力过滤（D2）；`go test -race` 全绿。
- **P1 范围锁定（2026-08-16 用户）**：只做 `RunQuantum` + Capability Scheduler + `AgentQueue` + capability-aware `Steal` + tests。**不碰 P2/P3/P4**——尤其不开始写 Agent Fabric / IPC / spawn / Process Tree / Cognitive State / Recovery / Evolution。先把 Scheduler 这个 Kernel 最小闭环跑起来，再让 Agent/Lifecycle/IPC 从真实运行行为中长出来。

#### P1（已完成）实现清单与验收（2026-08-16）
- **实现文件**：
  - `internal/taskfabric/quantum.go`——`QuantumStep` + `RunQuantum`：Start → step → Complete/Fail/Yield（D1 SUSPENDED 语义锁定）。
  - `internal/taskfabric/scheduler.go`——`Candidate` / `Score` / `Pick`（capability_overlap × (1-load) × confidence）+ `Fabric.Schedule`（Pick → Acquire，返回 fencing token；ConfidenceSource 填充未声明置信度）。
  - `internal/taskfabric/steal.go`——`AgentQueue` + capability-aware `Steal`（Score>0 才偷，原队列完整）。
  - `internal/taskfabric/confidence.go`——`ConfidenceSource` 接口（消费方定义）。
  - `internal/ares_skills/experience_confidence.go`——`ExperienceConfidenceSource` 适配器（Experience BestMatch SuccessRate → ConfidenceSource；编译期接口符合性保证 `var _ taskfabric.ConfidenceSource = (*ExperienceConfidenceSource)(nil)`）。
- **测试文件**：
  - `quantum_test.go`：Completes / Yields（SUSPENDED+checkpoint+事件）/ Fails（retry requeue）/ StaleEpoch。
  - `schedule_test.go`：PicksBestCapable（C 胜）/ NoCapableCandidate / RejectsOwnedTask。
  - `steal_test.go`：EnqueueLen / CapabilityAware / NothingWhenIncapable。
  - `scheduler_test.go`：ScoreCapabilityGating / PickBestExecutorWins / LoadDiscountsBusyAgents / CapabilityOverlapProportional。
  - `experience_confidence_test.go`：WithMatch / NoMatch / NilSafe。
- **验收**：未完成统一经 SUSPENDED（D1）；评分选优（D2）；偷取能力过滤（D2）；`go test -race ./internal/taskfabric/... ./internal/ares_skills/...` 全绿；`gofmt -l` / `go vet` / `go build ./...` 全绿；`git diff --check` 无空白错误。

#### P2（Scheduler 支柱·扩展）——DAG 调度源 + 抢占 + 事件升级
- **目标**：DAG 即调度源（Scheduler 只问 is_ready）；高优先级任务可协作抢占；事件持久化可重建。
- **API/数据结构**：
  - `IsReady(id) (bool, error)` + `ReadyTasks() []string`——`Task.Dependencies` 全 COMPLETED 即 ready（D3：P1/P2 手工构造；planner/live DAG 接入 P4）。
  - `Preempt(taskID, agentID, epoch, reason)`——priority 比较 + cooperative（在 yield/checkpoint 边界）；preempt → READY（checkpoint 保留，可被他人 acquire）。
  - 事件升级：task.* 事件（created/acquired/started/yielded/checkpointed/preempted/released/completed/failed/expired/stolen）注册进 `ares_events.EventType` + `EventStore.Append` 持久化（纯新增，向后兼容）。
- **测试**：DAG 依赖门控（A→B→C 逐级 ready）；抢占后 checkpoint 保留 + B 可 acquire；事件落库跨重启重建。
- **验收**：is_ready 正确；抢占协作化（无硬打断）；事件全量可重建状态。

#### P2（已完成）实现清单与验收（2026-08-16）
- **实现文件**：
  - `internal/taskfabric/dag.go`——`IsReady(id)` + `ReadyTasks()`（Task.Dependencies 全 COMPLETED 即 ready；D3 手工构造 Dependencies）。
  - `internal/taskfabric/fabric.go` `Preempt(taskID, agentID, epoch, reason)`——协作抢占（RUNNING→READY，checkpoint 保留；fencing token 校验）。
  - `internal/taskfabric/fabric.go` `record` + `WithEventStore`——task.* 事件全量注册进 `ares_events.EventType` + `EventStore.Append` 持久化（纯新增，向后兼容）。
  - `internal/ares_events/types.go`——`EventTaskReady/Acquired/Started/Yielded/Checkpointed/Preempted/Released/Expired/Stolen` 全量新增。
- **测试文件**：
  - `dag_test.go`：IsReady（依赖门控）/ ReadyTasksDAG（A→B→C 逐级 ready）。
  - `preempt_test.go`：Cooperative（preempt→READY+B 可 acquire）/ PreservesCheckpoint / Fencing（非 owner/stale epoch 拒绝）/ DecisionByPriority。
  - `event_store_test.go`：EventsPersistToStore（跨重启重建）/ NoStoreKeepsInMemoryLog / TaskEventTypeMapping（全量映射）。
- **验收**：`go test -race ./internal/taskfabric/... ./internal/ares_events/...` 全绿；`gofmt -l` / `go vet` / `go build ./...` 全绿。

#### P3（Lifecycle 支柱）——Agent Fabric + spawn + Cognitive State + Context 三层（Kernel 模型核心）
- **目标**：Agent 成为 Runtime 管理的可调度进程；spawn 建立 provenance 不建层级；认知状态可独立存活。
- **API/数据结构**：
  - `Agent{Identity; Capabilities; State; Load; Confidence; CognitiveState}`——Agent = 同级认知进程（A ≡ B ≡ C）。
  - 生命周期原语：`spawn / suspend / resume / retire / kill / recover`（Kernel 提供机制，Agent 决策）。
  - `Agent.Spawn(SpawnSpec{Task, Capabilities, Context, Resources})`——syscall 语义：Kernel 校验（quota/capability/resource/policy）后创建 Agent+Task+父子关系；**spawn establishes provenance, not hierarchy**。
  - 进程树：Process Tree（spawn 因果关系，Lifecycle 看）≠ Scheduling Graph（Task 依赖，Scheduler 看）——两张图独立，父子不形成权力结构。
  - Cognitive State：Context / Observation / Working Memory / Decision / Tool State / Checkpoint——可独立 checkpoint（Runtime 不依赖 hidden CoT）。
  - Context 三层：Task Shared State（goal/constraints/artifacts/decisions/dependencies）+ Agent Private State（reasoning/observations/hypotheses/scratchpad）+ IPC Messages。
- **测试**：spawn 建父子关系但双方同级可竞争同一 task；Parent 死亡 → Child 存活 + Task 由 Runtime 回收；Cognitive State checkpoint → 新 Agent resume；Context 三层隔离（Private 不串）。
- **验收**：spawn 不建层级；Parent 死 ≠ Child/Task 死；认知状态可独立恢复；Context 三层不混。

#### P3（已完成）实现清单与验收（2026-08-16）
- **实现文件**（新建 `internal/agentfabric/` 包）：
  - `doc.go`——包职责与边界（Kernel Lifecycle pillar；不调度不做 IPC）。
  - `agent.go`——`Agent{Identity; Capabilities; State; Load; Confidence; Parent; SpawnedAt; cognitive; privateContext; taskContext}` + `AgentState`（IDLE/RUNNING/SUSPENDED/RETIRED）+ `CognitiveState`（Context/Observation/WorkingMemory/Decision/ToolState/Checkpoint）+ 哨兵错误。
  - `fabric.go`——`Fabric`（registry + Process Tree `children` + EventSink + 注入时钟）+ `AgentEvent` / `AgentEventType`（spawned/suspended/resumed/retired/killed/recovered）+ `Get` / `Agents` / `Children`（provenance only）。
  - `lifecycle.go`——`SpawnSpec` + `Spawn`（syscall：校验 → 创建 Agent + 父子 provenance；不调度）+ `Suspend` / `Resume` / `Retire` / `Kill` / `Recover` + `SetRunning` / `SetIdle`（Scheduler 内部）。
  - `context.go`——Context 三层：`SetTaskContext` / `TaskContext`（Task Shared）+ `SetPrivate` / `Private`（Agent Private）+ `ContextView`（隔离验证）+ `CognitiveState` / `SetCognitiveState` / `CheckpointCognitive`（独立 checkpoint）。
- **测试文件**：
  - `fabric_test.go`：SpawnEstablishesProvenanceNotHierarchy / ParentDeathChildSurvives / CognitiveStateCheckpointResume / ContextThreeLayersIsolation / LifecycleSuspendResumeRetireKill / SpawnAutoID / SpawnDuplicateRejected / RecoverRevivesSuspendedAgent / RetiredAgentRejectsOperations / SetTaskContextDoesNotMutateCaller / ConcurrentSpawnIsSafe。
- **验收**：`go test -race ./internal/agentfabric/...` 全绿；`gofmt -l` / `go vet` / `go build ./...` 全绿。

#### P4（IPC 支柱 + leader 降级）——IPC 原语 + Legacy Leader → Policy
- **目标**：Agent 之间真正的 peer IPC；leader 从架构概念降级为执行策略。
- **API/数据结构**：
  - IPC 升级：`Send / Request / Reply / Delegate / Handoff / Subscribe`（NotifyPeer 通知级升级为完整消息原语）；父子关系不限制通信。
  - leader 降级：Legacy Leader Policy → Task Fabric → Scheduler → Agent——planner 降为 Agent 的 cognitive capability（可选），不再是 Runtime 中央组件；serve.go `createAgents` 改造，**并行 + feature flag 渐进切换**（新旧并存灰度）。
- **测试**：IPC 消息往返（request/reply）；Handoff 任务交接；flag 下新旧双轨等价。
- **验收**：peer IPC 可用；leader 降级后全链路行为等价；Planner 无中央职责。

#### P4（已完成）实现清单与验收（2026-08-16）
- **实现文件**（新建 `internal/agentipc/` 包）：
  - `doc.go`——包职责与边界（Kernel IPC pillar；同级别认知进程；Context layer 3）。
  - `bus.go`——`Message{ID; From; To; Topic; CorrelationID; Payload; At}` + `Handler` + `Bus`（handlers + subscribers + pending reply channels + pendingErr）+ `Register` / `Unregister`。
  - `primitives.go`——`Send`（fire-and-forget）/ `Request`（同步 request/reply + correlation id + 超时 / async reply）/ `Reply`（异步回复）/ `Delegate`（转发请求）/ `Handoff`（任务交接 + context snapshot）/ `Subscribe` + `Broadcast`（订阅 + fan-out）/ `stashError` / `popError`。
  - `policy.go`——`ExecutionPolicy`（LegacyLeader / TaskFabric）+ `PolicyFlag`（原子 flag，live flip）+ `Dispatcher` 接口 + `DualTrackDispatcher`（双轨 + shadow 模式等价验证 + Mismatches 计数）。
- **测试文件**：
  - `bus_test.go`：SendDeliversToTarget / SendUnknownAgent / RequestReplyRoundTrip / RequestAsyncReply / RequestTimeout / HandoffTaskTransfer / SubscribeBroadcast / BroadcastMultipleSubscribers / DelegateForwards / PolicyFlagDefaults / DualTrackDispatcherRoutesByFlag / DualTrackDispatcherShadowEquivalence / DualTrackDispatcherShadowMismatch / ConcurrentRequestsAreSafe。
- **生产接线（2026-08-16）**：`cmd/ares/kernel.go` 建立 Kernel 组装入口——`wireKernelDispatcher` 组装 `PolicyFlag`（默认 Legacy）+ `DualTrackDispatcher`（shadow=true）：legacy 端包装现有 `leader.TaskDispatcher`（真实派发，行为不变），newPath 端用 `taskfabric.Score/Pick` 做能力评分模拟（不 Acquire，避免双跑），Mismatches 可观测。flag 翻转后走 Task Fabric 路径（P4 D4 渐进割接）。
- **验收**：`go test -race ./internal/agentipc/...` 全绿；`gofmt -l` / `go vet` / `go build ./...` 全绿。

#### P5（Recovery + Chaos + Evolution）——存活保证 + 自我适应
- **目标**：Agent 死亡 ≠ Task 死亡（完整验证）；Chaos 证明 Runtime 能活下来；Evolution 改进 Runtime 自身。
- **API/数据结构**：
  - Recovery 子系统（独立于 Chaos）：lease expiry → requeue / checkpoint 恢复 / agent restart。
  - Chaos：Failure Injection + Recovery Verification（复用 `internal/ares_runtime` arena 故障注入基座）——故意制造死亡，验证 Runtime 能恢复。
  - Evolution：Runtime Adaptation——改调度策略 / agent 种群（spawn/retire 决策）/ spawn 策略。
- **测试**：注入故障（kill agent）→ lease 过期 → Task READY → B acquire → checkpoint resume；Evolution 修改调度策略后生效。
- **验收**：完整“Agent 死亡 ≠ Task 死亡”链路（注入→恢复→完成）；Chaos 注入可恢复；Evolution 可改调度/种群。

#### P5（已完成）实现清单与验收（2026-08-16）
- **实现文件**（新建 `internal/aresrecovery/` 包）：
  - `doc.go`——包职责与边界（Recovery 独立于 Chaos；Chaos breaks，Recovery fixes）。
  - `recovery.go`——`Recovery` 子系统：`RequeueExpiredLeases`（lease expiry → requeue）/ `RecoverTaskCheckpoint`（checkpoint 恢复 + 替换 agent）/ `RestartAgent`（crash 替换 + 认知状态恢复 + RestartPolicy budget）/ `RecoverFromAgentDeath`（全链路闭环）。
  - `chaos.go`——`Chaos` 故障注入 + 验证：`InjectFailure`（kill/suspend）/ `VerifyRecovery`（触发 Recovery 闭环）/ `EvolutionAdapter`（Runtime Adaptation：spawn/retire population 变更）。
  - `internal/taskfabric/fabric.go` `WithClock`（跨包时钟注入）+ `CheckExpiredLeases` 扩展（SUSPENDED 也回收：死 agent 的 suspended task 回 READY）。
- **测试文件**：
  - `recovery_test.go`：RequeueExpiredLeases / RecoverTaskCheckpoint / RestartAgent / RestartBudgetExhausted / FullRecoveryChain（注入→恢复→完成）/ EvolutionAdaptPopulation / ChaosSuspendFailure。
- **验收**：`go test -race ./internal/aresrecovery/... ./internal/taskfabric/...` 全绿；`gofmt -l` / `go vet` / `go build ./...` 全绿。

#### P0（已完成）参考
Task Fabric 核心原语 + fencing token；验收：CAS 竞争 / lease 过期回收 / 状态机拒绝 / 事件日志重建 / make check 全绿。

## 十二、验收基线（P0）

1. 两个 agent 并发 acquire 同一 task，仅一个成功（CAS），另一被拒绝。
2. Lease 过期（TTL 未续）后 task 自动回 READY，其他 agent 可 acquire。
3. 状态机非法转换被拒绝（如 READY 直接 complete 报错）。
4. 每个状态转换发出对应事件，从事件日志可完整重建 task 状态。
5. `make fmt && make check` 全绿。

## 十三、核心模型修正（Kernel 模型，2026-08-16 用户拍板）

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

### 架构不变量（不可随意破坏，2026-08-16 用户锁定）

1. Agent 是同级认知进程——A ≡ B ≡ C；parent/child 只有 **spawn provenance**，没有权限层级。
2. Task 是 durable，Agent 是 disposable——**Agent 死亡 ≠ Task 死亡**。
3. Kernel 不负责思考——**Agent decides; Kernel enforces**。
4. Scheduler 负责"谁 / 何时 / 何约束下运行"；Agent 决定"做什么 / 是否 spawn / 如何协作"。
5. 每个 Agent 独立 Cognitive State——不共享一个"大脑"。
6. Context 三层分离——Task Shared State / Agent Private State / IPC Messages。
7. **Process Tree ≠ Scheduling Graph**——spawn 因果关系树与 Task 依赖图并存不混。
8. spawn 是 syscall，不是 orchestration API。
9. 抢占是 cooperative——不做假 OS 硬抢占。
10. 不提前设计——Auction / 分布式 Scheduler / 完整 Actor / Execution 实体 / 复杂资源调度 / population optimization 等全部暂缓。

### Task decomposition = Agent cognition（2026-08-16 用户锁定）

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

### SUSPENDED 语义锁定（避免概念混淆，2026-08-16）

`RunQuantum` 的 SUSPENDED 明确理解为：**Agent 本次 execution quantum 结束，但
Task 的 durable intent 尚未完成**——不是"Agent 被暂停了"。

三个概念不混：
- **Task suspended**：durable intent 未完成，Task 状态 SUSPENDED（checkpoint 保留，可被他人 acquire）
- **Agent suspended**：Agent 生命周期状态（Lifecycle 支柱，P3 才出现）
- **Execution yielded**：本次 quantum 结束交回执行权（execution boundary，Scheduler 决策下一状态）
6. 遵守plan/rules/code_rules_v2.md 的编码规范