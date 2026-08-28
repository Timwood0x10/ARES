# ARES 全量修复开发计划（合并版）

> 合并自：`code-audit-report.md`、`development-plan.md`、`full-audit-report.md`、`pseudo-wiring-audit-plan-zh.md`、`supplementary-audit.md`、`workflow-engine-wiring-plan-zh.md`
> 审计日期：2026-08-28 | 项目：`github.com/Timwood0x10/ares`（Go, `internal/` + `cmd/ares/` + `sdk/`）
> 每一修复项均标注：**问题 → 文件:方法 → 修复方案 → 验收标准**
> 原则：每个"建而未接"的子系统必须二选一——接线（wire it）或删除（kill it），不允许"挂着但没用"。

---

## 0. 架构铁律（最高优先级 · 所有修复项的前置约束）

> **本章优先级高于其余全部章节。任何与本章冲突的接线动作一律作废重做。**

### 0.1 目标架构 = 0.3.0 "no leader" Kernel 调度（内部代号 Agent OS）

权威依据：`docs/zh/architecture/ares-runtime.md:1-5`（设计文档·冻结）——
> *"ARES 从 Agent Orchestration Framework（leader+sub）演进为面向 Agent 的动态计算运行时——**Agents are not orchestrated. They are scheduled.**"*

职责分离（同文档 §二，替代 leader 的 WHAT+WHEN+WHO+HOW 混杂）：

```
Planner / Cognitive Compiler → WHAT   （产出 Task Graph）
Kernel Scheduler             → WHEN/WHO（谁在何时以何约束跑）
Agent                        → HOW    （怎么执行，disposable）
Evolution                    → BETTER （改进 graph / 调度策略 / 种群）
```

**Leader 不再是角色，只是 Scheduler 的一种 policy（Peer/WorkStealing/Priority/Capability/...）。**

### 0.2 一条红线

> **所有改造以 Kernel 调度架构为目标。接线绝不能"接回 0.3.0 之前的 leader-sub 编排模式"。**
> 具体：不得新增/复活"主管 agent 派发给子 agent"的控制流；一切执行都必须经由 `kernelscheduler.Scheduler` 的 `Schedule → Acquire → RunQuantum → finalize` 流水线调度。

### 0.3 新架构骨架（接线只能接到这些点 · 均带文件:行证据）

| 组件 | 类型/构造器 | 文件:行 | 角色 |
|------|-----------|---------|------|
| Kernel 调度器 | `kernelscheduler.Scheduler` / `New()` | `internal/kernelscheduler/scheduler.go:38` / `:158` | WHEN/WHO 调度核心 |
| 调度主循环 | `(*Scheduler).Run` | `scheduler.go:222` | `Schedule→Acquire→RunQuantum→finalize` |
| 执行契约 | `CapabilityExecutor` 接口 | `internal/kernelscheduler/executor.go:37` | 任何执行体的唯一接入契约（ID/Type/ExecuteStep）|
| 动态候选源 | `fabricExecutor` / `appendFabricCandidates` | `internal/kernelscheduler/fabric_executor.go:16` / `:76` | 候选来自 agentfabric 动态群体（B1）|
| 执行器注册表 | `RegisterExecutor` 等（`execMu` 保护）| `internal/kernelscheduler/executor_registry.go:18` | agentID→executor 注册 |
| 任务织入 | `taskfabric.Fabric` / `RunQuantum` | `internal/taskfabric` | durable intent，Task 生命周期 |
| 生产装配点 | `createPeerAgents` → `NewKernelScheduler` → `go sched.Run` | `cmd/ares/peer_mode.go:130` / `:250` | **kernel 真正组装处** |
| 任务提交（非派发）| `submitFabricTask`（只 Create 不执行）| `cmd/ares/kernel_bridge.go:92` | 单轨提交，执行归调度器 |

### 0.4 已被 0.3.0 取代的 leader-sub 残留（接线时"误接回老版本"高危区）

| 残留符号/包 | 文件:行/证据 | 处置 |
|------------|-------------|------|
| legacy leader track | `cmd/ares/kernel_bridge.go:12-20`（"leader track has been deleted"）| 已删，**禁止复活** |
| `agents` Handoff（主管→子交接）| 附录A/D4 | 删除（leader-sub 交接机制）|
| `agentfabric.NewSubAgentCognition` | 实测仅 executor_test.go | 删除；**勿作为"子 agent 认知"接回** |
| `internal/agentloop`（旧对话引擎）| 全库 0 import | 删除；新架构认知走 `CapabilityExecutor.ExecuteStep` |
| PluginBus + Router 体系（ares_runtime）| serve.go "bridge is gone" | 删除/experimental（旧编排桥）|

### 0.5 接线动作合规检查表（每个 W-项开工前必过）

1. 该接线是否经由 `kernelscheduler` / `CapabilityExecutor` / `taskfabric` 接入？→ 否则驳回。
2. 是否引入"某 agent 决定另一 agent 做什么"的派发控制流？→ 是则驳回（leader-sub 复辟）。
3. 装配点是否在 `cmd/ares/peer_mode.go` kernel 组装链，而非误接进 `ares_bootstrap.Bootstrap` 非 kernel 主线？
4. 新执行体是否实现 `CapabilityExecutor(ID/Type/ExecuteStep)` 而非自建执行循环？

---

## 目录

0. [架构铁律（最高优先级）](#0-架构铁律最高优先级--所有修复项的前置约束)
1. [审计结论速览](#1-审计结论速览)
1.5. [证据核验结果（B 阶段实测）](#15-证据核验结果b-阶段-findreferences-实测)
2. [Phase 0：决策登记（0.5 天）](#2-phase-0决策登记05-天)
3. [Phase 1：P0/P1 致命 bug 修复（第 1 周）](#3-phase-1p0p1-致命-bug-修复第-1-周)
4. [Phase 2：伪接线闭环——接线项（第 2-3 周）](#4-phase-2伪接线闭环接线项第-2-3-周)
5. [Phase 3：伪接线闭环——删除项（第 3-4 周）](#5-phase-3伪接线闭环删除项第-3-4-周)
6. [Phase 4：P2 级 bug 修复（与 Phase 2/3 并行）](#6-phase-4p2-级-bug-修复与-phase-23-并行)
7. [Phase 5：配置清理与死配置（第 4 周）](#7-phase-5配置清理与死配置第-4-周)
8. [Phase 6：防回归 CI 门禁（第 5 周）](#8-phase-6防回归-ci-门禁第-5-周)
9. [附录 A：死代码删除决策表](#9-附录-a死代码删除决策表)
10. [附录 B：未接入内核模块决策](#10-附录-b未接入内核模块决策)
11. [附录 C：workflow 计划层（CompilePlan + create_plan）](#11-附录-cworkflow-计划层compileplan--create_plan)
12. [附录 D：DLQ 可靠性闭环](#12-附录-ddlq-可靠性闭环)
13. [附录 E：里程碑与验收总表](#13-附录-e里程碑与验收总表)

---

## 1. 审计结论速览

| 类别 | 数量 |
|------|------|
| 完全未接入内核的模块（cmd/ares 不可达） | 13 个 |
| 已确认死代码符号（生产零调用） | **80+ 个** |
| 已确认空转子系统（有实现无触发） | 25+ 个 |
| 已确认 bug（P1+P2） | 50+ 项 |
| 死配置字段 | 40+ 个 |
| 接线良好的核心模块 | ~20 个 |

**最危险的 5 个发现**：
1. **SDK 并发 fatal**：`sdkExecutors` map 跨两把锁读写（`sdk/task.go:49` vs `kernelscheduler/executor_registry.go:185`）
2. **内存无界三连**：ares_flight 聚合结构无 cap + evidence MemoryStore + MemoryEventStore 从不 trim
3. **可观测性全线空转**：OTel/Prometheus/CostDashboard 三套全部未接线，生产唯一 Tracer 是 Noop
4. **混沌注入静默无效**：`RegisterAgent` 存裸 agent，`chaosWrappedAgent` 只包裹在 `StartAgent`/`RestartAgent`
5. **优雅停机可被跳过**：shutdown 总 ctx 与 phase 回调共享 30s 预算，耗尽后 SystemRuntime Stop 被跳过

---

## 1.5 证据核验结果（B 阶段 `findReferences` 实测）

> 本节用代码实测校正了源报告中的错误数据与自相矛盾项。**所有"删除"决策以本节为准。**
> 生产可达定义：`cmd/ares/` + `sdk/` 入口可达（排除 `_test.go`、`examples/`）。

### 1.5.1 已实测符号——生产调用真相

| 符号 | 源报告声称 | 实测结果（生产调用者） | 修正判定 |
|------|-----------|----------------------|----------|
| `NewPushService`（ares_memory/push） | in_degree 33 ↔ 0 矛盾 | 仅 `push/service_test.go`、`pipeline_test.go` | 源"33"**错误**；生产 0，测试专用 |
| `NewReportGenerator`（ares_memory/report） | 25 ↔ 0 矛盾 | 仅 `report/generator_test.go`、`pipeline_test.go` | 源"25"**错误**；生产 0，测试专用 |
| `NewPipeline`（ares_memory） | — | 仅 `pipeline_test.go` | 整条蒸馏流水线**测试专用**（见决策 X1） |
| `NewProductionMemoryManager`（ares_memory） | 删除 or 替换 | 仅 `ares_integration/failover_test.go`、`memory_test.go` | **决策点非死代码**（见决策 X2） |
| `NewMinimalMemoryManager`（ares_memory） | 生产在用 | `provide_new_evolution.go:519`、`cmd/ares/evolution.go:58` | **属实**，生产唯一记忆管理器 |
| `NewSubAgentCognition`（agentfabric） | 删除 | 仅 `executor_test.go` | **属实**，测试专用 |
| `NewProtocol`（ares_protocol/ahp） | DLQ 接线 ↔ 删除矛盾 | 仅 `ares_integration/protocol_test.go`、`ahp_test.go` | **整个 ahp Protocol 子系统生产不可达**（见决策 X3） |
| `NewDLQ`（ares_protocol/ahp） | W10 要接线 | 被 `NewProtocol`（测试专用）内部 + 测试调用 | 生产 0，随 Protocol 决策 |
| `knowledge/service` ServiceAdapter | 恒返回 nil | `Query`(adapter.go:86)、`Distill` 空输入(L99) 返回 nil；`CompileContext` 有真实逻辑 | **部分属实**（非全恒 nil） |
| `internal/agentloop` | 删除（被 ChatCognition 取代） | 全库 0 处 import | 删除安全；"仅 SDK 用"旧说法**已废** |
| `internal/detector` | 仅 SDK quickstart 使用 | 全库 0 处 import | "仅 SDK 用"**错误**，实为零引用 → 删除安全 |
| `CrossoverGenome`（evolution/genome） | 文档自述已移除 | 接口仍定义于 `genome.go:57`，仅自身注释引用 | "已移除"**错误**，接口尚在 → 删前查实现者 |

### 1.5.2 三个决策项——已完成深度核验，给出明确建议

> **状态更新**：X1/X2/X3 已通过阅读生产装配代码完成核验。**三条全部指向"删除/删死构造器"，无一需要新接线**——这印证了新架构（Kernel 调度 + 事件驱动蒸馏）已自洽，这些"建而未接"的符号多为 0.3.0 之前的平行旧实现。以下建议附决定性代码证据，待最终拍板。

#### 关键发现：生产已有一条"活的"事件驱动蒸馏回路（决定 X1/X2）

阅读 `internal/ares_bootstrap/provide_distillation.go` 证实，生产**已经**在跑经验蒸馏，且走的是新架构（事件驱动，非 leader 循环）：

```
TaskCompleted 事件
  → HandleTaskCompletedForDistillation (provide_distillation.go:215)
  → aresexp.DistillationService.Distill (:65, :238)
  → PG ExperienceRepository (:63, repositories.NewExperienceRepository)
  → FuncGuidanceProvider (:67) → 回流 GA 的 experience-guided mutation (:85 RecordFunc)
```

这条链依赖 `aresexp.NewDistillationService` + `postgres.Pool`(:52) + `repositories.ExperienceRepository`(:63)，**完全独立于** `internal/ares_memory` 包里的 `Pipeline`/`Push`/`Report`。即：`ares_memory` 那套是一份**平行的、被取代的旧蒸馏实现**，从未接线（实测仅测试引用）。

#### 决策结论表

| 决策 ID | 主题 | **最终建议** | 决定性证据 | 架构合规 |
|---------|------|-------------|-----------|----------|
| **X1** | 记忆蒸馏流水线 `Pipeline`/`Push`/`Report` | **删除（执行 D15）** | 生产蒸馏已由 `provideDistillation` 事件链承载（provide_distillation.go:215/238）；`ares_memory.Pipeline` 仅 `pipeline_test.go` 引用，是重复旧实现 | ✅ 删除无损；补强只能挂 EventStore 消费者，禁止独立后台循环 |
| **X2** | `NewProductionMemoryManager`（带 PG 参数构造器） | **删死构造器，保留类型 + `NewMinimalMemoryManager`** | 生产记忆走 `wireMemory→ProvideMemory→NewMemoryManager`，返回 `*memoryManager`（provide_memory.go:15），**根本不用** ProductionMemoryManager；`NewMinimalMemoryManager()` 返回 `*ProductionMemoryManager` 供 evolution MemoryPatchExecutor 读 config（provide_new_evolution.go:519，**活的勿动**）；带参 `NewProductionMemoryManager(...)`（production_manager.go:107）仅测试引用 | ✅ PG 记忆价值已被 provideDistillation 的 PG ExperienceRepository 实现，无需再切；架构中性 |
| **X3** | ahp Protocol 子系统（含 DLQ） | **删除（W10 作废，执行 D10）** | `NewProtocol` 生产零调用（W10"给它加 DLQ"前提不成立）；ahp 的 Protocol/Router 是 leader-sub 时代的 agent 间消息编排 | ⚠️ 接回=复辟老编排，违反 §0.2 no-leader 铁律 → **必须删除** |

#### X2 三个符号关系澄清（避免误删活代码）

| 符号 | 文件:行 | 生产状态 | 处置 |
|------|---------|---------|------|
| `NewMemoryManager` → `*memoryManager` | `manager_impl.go:125` | ✅ 生产 agent 记忆的真实实现（经 ProvideMemory） | 保留 |
| `NewMinimalMemoryManager` → `*ProductionMemoryManager` | `memory_patcher.go:69` | ✅ evolution MemoryPatchExecutor 在用（provide_new_evolution.go:519） | **保留（勿动）** |
| `NewProductionMemoryManager(pool, ...)` | `production_manager.go:107` | ❌ 仅测试引用 | **删除此构造器** |

#### X3 删除清单与前置检查

- 删除 `internal/ares_protocol/ahp` 的：`NewProtocol`/`NewDLQ`/`NewDLQProcessor`/`NewHeartbeatMonitor`/`NewMessageQueue`/`NewMessageRouter`/`NewDynamicRouter`/`NewCodecRegistry`/`NewJSONCodec`/`NewRateLimiter`。
- **删除前置**：`AHPMessage`/`SendMessage`/`queue` 类型删前必须 `findReferences` 确认 `agents/peer`、`evolution_ipc` 的实际引用——命中则保留该类型，仅删协议编排层。
- **W10 正式作废**，不再冻结待定；D10 解冻，可执行。

### 1.5.3 Phase 3 删除硬门（删除任何符号/包前必须执行）

1. 对目标符号跑 `findReferences`，确认 `cmd/ares/` + `sdk/` + `examples/` 无生产/示例引用；
2. 命中 `sdk/` 或 `examples/` → **降级**为"接线或文档化为 SDK 边界"，不得删除；
3. `CrossoverGenome` 等接口类型删前额外查 type-assertion 实现者；
4. 删除后 `go build ./... && go test ./... && make check` 必须全绿。

---

## 2. Phase 0：决策登记（0.5 天）

每个子系统标记"接线 / 删除 / 移入实验性"。

> **✅ 三个阻塞决策（X1/X2/X3）已完成深度核验，建议见 §1.5.2，全部指向"删除/删死构造器"。**

| 决策 ID | 待裁决项 | 建议 | 状态 |
|---------|---------|------|------|
| X1 | 记忆蒸馏流水线 Pipeline+Push+Report | **删除**（生产已有事件驱动蒸馏回路 provide_distillation.go:215） | ✅ 已核验，待拍板 |
| X2 | NewProductionMemoryManager（带参构造器） | **删死构造器，留类型 + NewMinimalMemoryManager** | ✅ 已核验，待拍板 |
| X3 | ahp Protocol 子系统含 DLQ → W10 vs D10 | **删除**（W10 作废，执行 D10；接回违反 §0.2） | ✅ 已核验，待拍板 |

| 子系统 | 建议决策 | 理由 |
|--------|---------|------|
| PluginBus + 11 插件 + 4 Router（ares_runtime） | **删除或移入 experimental/** | serve.go 已宣告 "bridge is gone" |
| OTel + Prometheus + CostDashboard | **接线** | 可观测性是已交付功能的数据源缺口 |
| JWT AuthMiddleware 中间件形态 | **删除** Wrap/WrapGin/FromContext | 生产已用手动链 checkAuth |
| PermAdmin / RBAC 分级 | **接线** | chaos 破坏性路由改 require PermAdmin |
| SlidingWindow/Semaphore limiter | **删除** | 无生产消费者 |
| ares_shutdown 的 SignalHandler/CallbackRegistry/CallbackChain/PhaseExecutor | **删除** | 生产自建了等价物 |
| agents Handoff | **删除** | 无文档化场景 |
| agents ProfileRegistry 写侧 | **接线** | 角色指令注入是文档化功能（Ch.10） |
| core/models 行为方法 | **删除** | 类型被用、方法全死 |
| EventSubTaskResult / outcome_recorder | **接线 emitter** | 静默失效最恶劣 |
| introspect insights 引擎 | **接线** | 已有 /api/insights 端点，只缺数据 |
| introspect Collab/Tasks/Decisions 三源 | **接线** | 演示代码已写好接法 |
| flight DecisionLog/Genealogy | **接线** | 统一 event 常量 + bootstrap 传 GenealogyCollector |
| knowledge/service | **删除** | 不可达；实测 Query/Distill 空输入返回 nil，CompileContext 有逻辑但无生产调用者 |
| knowledge/workflow | **删除** | 不可达 |
| knowledge/retriever | **删除** | 不可达 |
| knowledge/provider/postgres | **删除** | 不可达 |
| knowledge/store/sqlite | **删除** | 不可达 |
| knowledge/linker | **删除** | 不可达 |
| storage/memory | **删除** | 生产孤儿 |
| storage/postgres/query | **删除** | 完全孤儿（含测试 0 引用） |
| llmservice | **删除** | 已有 internal/llm + api/service/llm 两套等价 |
| ares_protocol/ahp 子系统（DLQ/Heartbeat/Queue/Router/Codec/Limiter） | **删除**（依赖 X3） | 实测 NewProtocol 生产零调用；删 AHPMessage/SendMessage/queue 前查 agents/peer、evolution_ipc 引用 |
| tools/toolsource | **删除** | CapabilitySelector/TagSelector 无人调用 |
| dead config 字段（40+） | 接线/删除/文档化 | 见 Phase 5 |

---

## 3. Phase 1：P0/P1 致命 bug 修复（第 1 周）

### P1-1 SDK 并发 fatal

| 项目 | 内容 |
|------|------|
| **问题** | `sdkExecutors` map 被两把锁并发读写：SDK 侧 `agentMu`（`sdk/task.go:49`、`sdk/scheduler.go:195`）写，调度侧 `execMu`（`kernelscheduler/executor_registry.go:185`）读，Go runtime 直接 fatal panic |
| **文件:方法** | `sdk/task.go:49` `RegisterAgent`、`sdk/scheduler.go:195` `ensureExecutor`、`kernelscheduler/executor_registry.go:185` `executorRegistry.Executors` |
| **修复方案** | 所有 SDK 侧写入改为调用 `sched.RegisterExecutor`（走 scheduler 自己的 `execMu`），删除 SDK 侧旁路写；或 Runtime 持有一把统一锁 |
| **验收标准** | `go test -race ./sdk/...` 全绿；并发 `RegisterAgent` + `Submit` 的 race 测试通过 |

### P1-2 内存无界三连

| 项目 | 内容 |
|------|------|
| **问题** | ① ares_flight 五个聚合结构（timeline/decision/diagnostics/graph/collector）无 ring cap；② evidence MemoryStore 无界增长；③ ares_events 生产压实配置 `EnableTrimming=false` + `trimStore=nil`，SummaryTTL 从不执行 |
| **文件:方法** | ① `internal/ares_flight/timeline.go:55-57`、`decision.go:33-36`、`diagnostics.go:45-48`、`graph.go:44-48`、`collector.go:30`；② `internal/evidence/memory_store.go`；③ `internal/ares_archive/store.go:45-47` `NewCompactableEventStore` |
| **修复方案** | ① 五个聚合结构加 ring cap（对齐 introspect 的 300/200），读端点分页；② evidence MemoryStore 加 cap 或默认要求 Postgres；③ 生产构造改 `EnableTrimming=true` + trimStore 接 ares_archive sink |
| **验收标准** | 12h 长跑 serve 内存曲线平稳；`/metrics` 暴露 dropped_events 计数 |

### P1-3 停机闭环

| 项目 | 内容 |
|------|------|
| **问题** | ① `StartShutdown` 防重入 `m.currentPhase != 0` 而 `PhasePreShutdown==0`（iota 首值）→ 第二阶段期间二次调用可重跑回调；② serve.go:104-110 shutdown 总 ctx 与 phase 回调共享 30s 预算，耗尽后 `shutdownSystemRuntime` 拿到过期 ctx 被跳过 |
| **文件:方法** | ① `internal/ares_shutdown/manager.go:118` `StartShutdown`；② `cmd/ares/serve.go:104-110` `shutdownSystemRuntime` |
| **修复方案** | ① 改用 `atomic.CompareAndSwap` 布尔哨兵，不依赖 phase 值；② 为 SystemRuntime Shutdown 保留独立预算（如 15s 专用于 stage-9） |
| **验收标准** | `kill -TERM` 后 MCP/Runtime/FlightRecorder Stop 日志必然出现；`-race` 测试通过 |

### P1-4 混沌注入闭环

| 项目 | 内容 |
|------|------|
| **问题** | `chaosWrappedAgent` 只在 `StartAgent`/`RestartAgent`/`RestoreAgent` 中包裹，生产 `RegisterAgent`（`serve_agents.go:62`）存裸 agent → `Manager.Start()` 直接从 `m.agents` launch → `chaosFault`/`chaosSlowDelay` 无读取者 |
| **文件:方法** | `internal/ares_runtime/manager_chaos.go:221-306` `chaosWrappedAgent`、`manager.go:231` `RegisterAgent`、`manager_lifecycle.go:54-61` `Start` |
| **修复方案** | `RegisterAgent` 路径同样包裹 `chaosWrappedAgent`，把包裹逻辑移到 Register 与 Start 共同路径 |
| **验收标准** | 注入 chaos config → agent 行为变化端到端可观测；集成测试通过 |

### P1-5 YAML memory.enabled:false 生效

| 项目 | 内容 |
|------|------|
| **问题** | `sdk/config.go:370-382` `ToOptions` 只在 true 时追加选项，false 时什么都不做，而 defaultConfig 默认 Enabled=true → 显式关闭被静默吞掉 |
| **文件:方法** | `sdk/config.go:370-382` `ToOptions` |
| **修复方案** | false 时追加 `WithoutMemory()`；同 PR 修 `knowledge.chunk_size/chunk_overlap/top_k` 三字段的消费（透传给 chunker/topK 检索） |
| **验收标准** | 设 `memory.enabled: false` 后 serve 启动 MemoryManager 不初始化 |

### P1-6 handleMemoryDistilled 类型断言

| 项目 | 内容 |
|------|------|
| **问题** | `evt.Payload["input_count"].(float64)` 恒失败，发射器写入 Go `int`，计数恒为 0 |
| **文件:方法** | `internal/ares_flight/collector.go` `handleMemoryDistilled` |
| **修复方案** | 统一 payload 写 int 或用 `fmt.Sprint` + `strconv.Atoi` 容错解析 |
| **验收标准** | 新增 `handleMemoryDistilled` 单测断言 count 正确 |

### P1-7 taskfabric Fabric.Create 指针别名

| 项目 | 内容 |
|------|------|
| **问题** | `Create` 直接把调用方 `*Task` 指针存入内部 map，绕过锁并发写 |
| **文件:方法** | `internal/taskfabric/fabric.go` `Fabric.Create` |
| **修复方案** | `Create` 内部对入参做深拷贝再存入 |
| **验收标准** | 新增并发 Create + 读测试（`-race`） |

### P1-8 ares_skills FetchHTTPManifest 远程技能路径

| 项目 | 内容 |
|------|------|
| **问题** | 远程技能 `Path` 未设置，`Load()` 时从 CWD 下错误路径读取 |
| **文件:方法** | `internal/ares_skills/http_source.go` `FetchHTTPManifest` |
| **修复方案** | 为 SourceRegistered 条目设置实际的 manifest/下载路径；或在加载时按 URL/ID 定位 |
| **验收标准** | 新增 FetchHTTPManifest → Load 端到端测试 |

### P1-9 arena 注入器空接线（workflow plan 7.A A1）

| 项目 | 内容 |
|------|------|
| **问题** | `cmd/ares/arena.go:201` `NewInjector(nil, nil)` → 全部故障注入 API 恒返回 `ErrRuntimeNil`/`ErrDAGNil` |
| **文件:方法** | `cmd/ares/arena.go:201` `NewInjector`；`internal/ares_arena/injector.go:64-67` |
| **修复方案** | 为 `RuntimeProvider`/`DAGProvider` 提供生产实现，把 kernelHandle 的 runtime 与 live DAG 适配进去 |
| **验收标准** | 至少一个注入动作（如 KillAgent）端到端生效 |

### P1-10 profile 注入断裂（workflow plan 7.A A2）

| 项目 | 内容 |
|------|------|
| **问题** | 消费端 `activeRoleInstructions` 在生产运行，但写入端 `ProfileRegistry.ApplyToContext` 生产零调用 → 角色指令注入形同虚设 |
| **文件:方法** | `internal/agents/profile.go:157` `ApplyToContext`；`chat_cognition.go:544`、`sub/executor.go:562` 消费端 |
| **修复方案** | 在 agent spawn / cognition 初始化处调用 `ApplyToContext` 写入选定 profile；把 `ProfileRegistry` 接入生产依赖 |
| **验收标准** | 端到端测试：生产路径下 `activeRoleInstructions` 非空 |

---

## 4. Phase 2：伪接线闭环——接线项（第 2-3 周）

### W1 可观测性接线

| 项 | 内容 |
|----|------|
| **问题** | `NewOTelTracer`/`NewPrometheusMetrics`/`NewCostDashboard` 生产零调用；`api/service/llm.Config` 无 Tracer 字段 |
| **文件:方法** | `api/service/llm/config.go` 加 Tracer 字段 → `toInternal()` 透传；`cmd/ares/serve.go` 构造 `NewPrometheusMetrics` 注册到 `/metrics`；`ares_bootstrap` 加 `provide_observability` step |
| **修复方案** | ① `api/service/llm.Config` 增 Tracer 字段；② `cmd/ares serve` 构造 `NewPrometheusMetrics` 并注册；③ CostDashboard 三端点挂到 introspect 路由 |
| **验收标准** | `curl /metrics` 出现 `ares_llm_calls_total` 等自定义指标 |

### W2 事件对齐 + 面板数据闭环

| 项 | 内容 |
|----|------|
| **问题** | ① `EventSubTaskResult` 有订阅者无发布者；② `introspect.insights` 从未写入；③ flight decision 事件无 emitter → decisions 恒空；④ Genealogy 未装配；⑤ timeline 事件映射错位（Tool/LLM 时长恒 0）；⑥ Collab/Tasks/Decisions 三 Source 恒 nil |
| **文件:方法** | ① `ares_events/types.go` 增补缺失常量；`agentloop/sub executor` 完成处 emit `EventSubTaskResult`；② `introspect/intel.go` 实现 `FeedIntel` → insights 生成；③ `ares_flight/collector.go` 修事件映射 `completed` → `EventToolResult/EventLLMResult`；④ `bootstrap.go` 传 `GenealogyCollector`；⑤ `serve_agents.go:154-157` 补 3 个 Source |
| **修复方案** | 见左侧 5 项 |
| **验收标准** | `/api/insights` 非空；面板任务板/决策页有数据；`/api/flight/summary` 的 Tool/LLM 时长 > 0 |

### W3 Evolution GA 参数接线

| 项 | 内容 |
|----|------|
| **问题** | `wireGAEvolution`（`bootstrap_steps.go:195-200`）用 `evolution.DefaultSystemConfig()` + 硬编码，无视 `cfg.Evolution` 14 个 GA 参数 |
| **文件:方法** | `internal/ares_bootstrap/bootstrap_steps.go:195-200` `wireGAEvolution` |
| **修复方案** | 读 `cfg.Evolution` 的 14 个参数构造 `SystemConfig`，ticker 读 `MinInterval` |
| **验收标准** | 改 yaml → 参数生效的等价性测试通过 |

### W4 ProfileRegistry 写侧

| 项 | 内容 |
|----|------|
| **问题** | 写入端 `ApplyToContext` 生产零调用（见 P1-10） |
| **文件:方法** | 同 P1-10 |
| **修复方案** | 同 P1-10 |
| **验收标准** | 同 P1-10 |

### W5 RBAC——PermAdmin 接线

| 项 | 内容 |
|----|------|
| **问题** | `PermAdmin` 从未被任何路由 `require`，`NewAuthMiddleware` 固定 `PermWrite` → chaos kill-all 对 operator 放行 |
| **文件:方法** | `internal/ares_security/rbac.go:36-38` `PermAdmin`；`cmd/ares/actions.go` chaos 路由 |
| **修复方案** | chaos kill-all/random-kill/recover 路由 require `PermAdmin`；API key 保持 read/write 分级 |
| **验收标准** | operator JWT 调 kill-all 返回 403；admin JWT 通过 |

### W6 SDK Close 闭环

| 项 | 内容 |
|----|------|
| **问题** | Bootstrap 成功路径的 cleanups 永不执行——MemoryManager、MCPManager、PG 连接池全部不关闭 |
| **文件:方法** | `sdk/sdk.go:394-440` `Close` |
| **修复方案** | Bootstrap 成功路径的 cleanups 存到 Runtime，Close 顺序执行（先 `eg.Wait` 再 cleanups 再 SDK 侧资源） |
| **验收标准** | `WithPostgres` 场景 Close 后连接池归零；无连接泄漏 |

### W7 Kernel.PollInterval / Server.Host 接线

| 项 | 内容 |
|----|------|
| **问题** | `Kernel.PollInterval` 从未注入调度器（sdk 硬编码 20ms）；`Server.Host` 被 serve_routine.go 忽略 |
| **文件:方法** | `internal/ares_config/config.go:88` `Kernel.PollInterval`；`cmd/ares/serve_routine.go:165` 绑定地址 |
| **修复方案** | kernel_loop/serve_routine 读 cfg 注入；或从 config 删除并从 yaml 示例移除 |
| **验收标准** | 改 yaml 的 `kernel.poll_interval` → 调度器 ticker 间隔变化 |

### W8 技能反馈闭环

| 项 | 内容 |
|----|------|
| **问题** | `SkillOutcomeRecorder` 从未启动；`CatalogTools` 5 个技能工具从未注册；`ExperienceConfidenceSource` 从未接入调度器 |
| **文件:方法** | `internal/ares_skills/outcome_recorder.go:62` `Start`；`tools.go:57` `CatalogTools`；`experience_confidence.go` `NewExperienceConfidenceSource` |
| **修复方案** | ① serve 中启动 `SkillOutcomeRecorder`（订阅 `EventSubTaskResult`）；② 注册 `CatalogTools` 5 个技能工具到 `internalReg`；③ 将 `ExperienceConfidenceSource` 注入 taskfabric 调度器的 `ConfidenceSource` |
| **验收标准** | 技能工具出现于工具列表；技能结果写回 experience |

### W9 workflow 计划层（CompilePlan + create_plan）

| 项 | 内容 |
|----|------|
| **问题** | workflow/engine 有 DAG 无执行器，`NewAgentExecutor` in_degree=0 |
| **文件:方法** | 见附录 C 完整方案 |
| **修复方案** | 新增 `internal/taskfabric/workflow_plan.go` `CompilePlan`；新增 `cmd/ares/workflow_plan.go` `projectWorkflow`；修改 `internal/agentsyscall/syscall.go` 注册 `create_plan` 工具 |
| **验收标准** | 端到端：create_plan → 全部 COMPLETED；`make check` 绿 |

### W10 DLQ 可靠性闭环 ⚠️冻结（依赖 X3 裁决）

| 项 | 内容 |
|----|------|
| **状态** | **冻结**——实测 `NewProtocol` 生产零调用（§1.5.1），原"给 NewProtocol 加 DLQProcessor"前提不成立 |
| **前置** | 必须先完成决策 X3：若选"接线"则整个 ahp Protocol 需先接入生产 agent 通信；若选"删除"则本项作废、转 D10 |
| **文件:方法** | 见附录 D（仅在 X3=接线 时生效） |
| **验收标准** | 端到端：发送失败 → 进 DLQ → 自动重投 → 耗尽终态 |

---

## 5. Phase 3：伪接线闭环——删除项（第 3-4 周）

### D1 删除 ares_runtime 插件生态

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_runtime/bus.go:47` `NewPluginBus`；`observer.go:22` `NewObserverPlugin`；`loop.go:36` `NewLoopPlugin`；`interrupt.go:23` `NewInterruptPlugin`；`tool.go:27` `NewToolPlugin`；`checkpoint.go:144` `NewCheckpointPlugin`；`evolution_plugin.go:64` `NewEvolutionPlugin`；`arena.go:42` `NewArenaPlugin`；`recovery.go:20` `NewBasicRecoveryPlugin`；`outcome_recorder.go:24` `NewOutcomeExperienceRecorder`；`router.go:58` `NewExpressionRouter`；`router_evolution.go:22` `NewEvolutionRouter`；`router_fallback.go:19` `NewFallbackRouter`；`router_memory.go:30` `NewMemoryRouter`；`collector.go:65` `NewExecutionCollector`；`state_snapshot.go:34/60` `SaveStateSnapshot/LoadStateSnapshot`；`events.go:7-13` 死事件常量 |
| **修复方案** | 删除上述符号及其实现文件（或移入 `internal/experimental/`） |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D2 删除 ares_shutdown 四个未接线组件

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_shutdown/signal.go:25` `SignalHandler` 全部导出面；`callbacks.go:10` `CallbackRegistry`/`RegisteredCallback`；`callbacks.go:177` `CallbackChain`；`phase.go:39` `PhaseExecutor` |
| **修复方案** | 删除上述 4 个组件文件（保留 `Manager` 主体：`NewManager`/`RegisterPhase`/`AddCallback`/`StartShutdown`） |
| **验收标准** | `go build ./...` 绿；serve 正常启停 |

### D3 删除 ares_ratelimit 三个未接线实现

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_ratelimit/sliding_window.go:20` `SlidingWindowLimiter`；`semaphore.go:18/131` `SemaphoreLimiter`/`WeightedSemaphoreLimiter`；`limiter.go:59/107/116` 工厂 `NewFactory`/`CreateLimiter`/`DefaultFactory`；`limiter.go:16` `Limiter.Reset`；`constants.go:13-38` 七个 Default* 常量 |
| **修复方案** | 删除上述符号（保留 `TokenBucket` 实现） |
| **验收标准** | `go build ./...` 绿 |

### D4 删除 agents Handoff

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/agents/handoff.go` 整文件（`NewHandoff`/`WithContext`/`WithArtifact`/...） |
| **修复方案** | 删除 `handoff.go` 文件 |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D5 删除 core/models 死行为方法

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/core/models/session.go` `NewSession`/`IsCompleted`/`AddTask`/`Progress`/`SetStatus`/`IsExpired`；`recommend.go` `NewRecommendResult`/`AddItem`/`CalculateScore`；`ParseAgentStatus`；`NewPriceRange`；`UserProfile.HasStyle`/`HasOccasion`；`UserFeedback.SetRating` |
| **修复方案** | 删除上述死方法（保留被 SQL scan 使用的类型定义） |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D6 删除 knowledge 不可达子包

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/knowledge/linker/`、`provider/postgres/`、`retriever/`、`service/`、`store/sqlite/`、`workflow/` 全部文件 |
| **修复方案** | 删除上述 6 个目录 |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D7 删除 storage 两个孤儿包

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/storage/memory/` 全部文件；`internal/storage/postgres/query/` 全部文件 |
| **修复方案** | 删除上述 2 个目录 |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D8 删除 llmservice

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/llmservice/` 全部文件（已有 `internal/llm` + `api/service/llm` 两套等价实现） |
| **修复方案** | 删除 `internal/llmservice/` 目录；确认 `api/service/llm` 和 `compat/` 不依赖 |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D9 删除 agents 子包死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/agents/sub/handler.go:40-49` `handleTaskMessage`/`handleAckMessage` 空实现；`internal/agents/sub/tools.go:171` `GetTool` 无锁读（需修复）；`internal/agents/sub/executor.go:172-188` `RegisterFallback`/`SetEventStore`/`SetCallbacks` 无锁写 |
| **修复方案** | 删除空 handler 实现；修复 `GetTool` 持锁拷贝；`Set*` 加锁或文档化构造期约定 |
| **验收标准** | `go build ./...` 绿；`-race` 测试通过 |

### D10 删除 ares_protocol/ahp 死子系统 ⚠️与 W10 互斥（依赖 X3 裁决）

| 项 | 内容 |
|----|------|
| **前置** | 仅在 X3=删除 时执行；与 W10 互斥，二者不可同时进行 |
| **实测** | `NewProtocol`/`NewDLQ` 生产零调用，仅测试引用（§1.5.1） |
| **文件:方法** | `internal/ares_protocol/ahp/dlq.go` `NewDLQ`/`NewDLQProcessor`；`heartbeat.go` `NewHeartbeatMonitor`；`queue.go` `NewMessageQueue`；`router.go` `NewMessageRouter`/`NewDynamicRouter`；`protocol.go` `NewProtocol`；`codec.go` `NewCodecRegistry`/`NewJSONCodec`；`ratelimit.go` `NewRateLimiter` |
| **修复方案** | 删除上述符号；**删 `AHPMessage`/`SendMessage`/`queue` 前先 `findReferences` 确认 `agents/peer`、`evolution_ipc` 引用**——命中则保留 |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D11 删除 tools/toolsource

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/tools/toolsource/` 全部文件（`CapabilitySelector`/`TagSelector` 无人调用） |
| **修复方案** | 删除 `internal/tools/toolsource/` 目录 |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D12 删除 llm/output 死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/llm/output/timeout.go` 全部导出函数（`WithLLMTimeout`/`WithLLMStructuredTimeout`/`WithDatabaseTimeout`/`WithDatabaseTransactionTimeout`）；`template.go` `NewTemplateEngine`/`NewTemplateRegistry`/`ParseOutput`/`NewSchema`/`NewSchemaGenerator`/`NewTimeout`/`RenderTemplate` |
| **修复方案** | 删除上述符号或 unexport |
| **验收标准** | `go build ./...` 绿 |

### D13 删除 llm 死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/llm/client.go` `NewClientFromEnv`/`NewFailoverScorer`/`WithRateLimiter`/`WithRetryPolicy`/`IsOpen`/`IsHalfOpen`；`config.go` `Config.Extra` 字段 |
| **修复方案** | 删除上述符号；`Config.Extra` 从 config schema 移除 |
| **验收标准** | `go build ./...` 绿 |

### D14 删除 ares_evolution 仅测试组件

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_evolution/genome/` `NewMetaController`/`NewHypothesisGenerator`/`NewLLMReflector`/`NewPGStrategyStore`/`NewKnowledgeDistiller`/`NewNondominatedSortingSelection`/`NewTruncationSelection`/`NewPopulationGenealogyRecorder`；`NewEvidenceAggregatorProvider`；`HintsForTask`/`RecordStrategyOutcome` |
| **修复方案** | 删除上述符号（核心进化管线保留） |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D15 删除 ares_memory 旧蒸馏管线（X1 已裁决=删除）

| 项 | 内容 |
|----|------|
| **决策依据** | X1 已核验：生产蒸馏由 `provide_distillation.go:215` 事件链承载，`ares_memory` 这套 Pipeline/Push/Report 是被取代的平行旧实现，仅测试引用 |
| **文件:方法** | `internal/ares_memory/pipeline.go` `NewPipeline`；`.../push/` `NewPushService`；`.../report/` `NewReportGenerator`；`internal/ares_memory/` `NewDistillationRepo`/`NewKnowledgeRetrieverAdapter`；`experienceadapters/` `SearchByVector`/`GetByMemoryType` |
| **修复方案** | 删除上述符号及其专属 test 文件；连带删带参 `NewProductionMemoryManager`（production_manager.go:107，X2）——**保留** `ProductionMemoryManager` 类型与 `NewMinimalMemoryManager`（memory_patcher.go:69，evolution 在用） |
| **前置门** | 删除前对每个符号跑 `findReferences` 复核 `cmd/ares/`+`sdk/`+`examples/` 无引用（§1.5.3） |
| **验收标准** | `go build ./... && go test ./...` 全绿；`make check` 绿；`ares serve` 蒸馏功能不受影响（事件链仍活） |

### D16 删除 aresrecovery 仅测试组件

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/aresrecovery/` `WithCognitionFactory`；`GlobalTracer.TraceTask`/`TraceAgent`/`ByKind`；`Sandbox.Simulate`；`Recovery.RecoverTaskCheckpoint`/`RecoverFromAgentDeath`；`ChangeAttributor.AttributeTrajectory`；`EvolutionAdapter.NewEvolutionAdapter` |
| **修复方案** | 删除上述符号 |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D17 删除 workflow/graph 死 setter

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/workflow/graph/graph.go` `SetPluginBus`/`SetRouter`/`SetTracer`/`SetExecutionCollector`/`SetLimiter`/`SetCheckpointStore`/`SetScheduler`/`NewGraphWithTracer`/`NewAgentNode`/`NewToolNode`/`Clear`/`RemoveEdge`/`RemoveNode` |
| **修复方案** | 删除上述符号（保留 `NewGraph`/`Node`/`Edge`/`NewFuncNode`/`Start`） |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D18 删除 workflow/engine 死组件

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/workflow/engine/` `NewAgentExecutor`/`NewHITLFeedbackPlugin`/`NewMemoryInterruptStore`/`NewOutputStore`/`NewWorkflowReloader`/`NewAgentRegistry`/`ListPending` |
| **修复方案** | 删除上述符号（保留 `NewMutableDAG`/`Set`/`NewRecoveryPatchExecutor` 等核心） |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D19 删除 ares_flight 死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_flight/` `NewGenealogyCollector`、`AutoDiagnose`、`SuggestFix(DiagConcurrencyError)`、`Replay`、`FilterByType`、`ExportJSON` |
| **修复方案** | 删除上述符号（保留 `NewFlightRecorder`/`NewCollector` 核心） |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D20 删除 ares_archive 死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_archive/` `summarizeFileChange` output 参数 |
| **修复方案** | 删除未使用的 `output` 参数 |
| **验收标准** | `go build ./...` 绿 |

### D21 删除 ares_security 死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_security/middleware.go` `Wrap`/`WrapGin`/`PrincipalFromGin`/`FromContext`；`rbac.go` `HasPermission`；`sanitizer.go` `SanitizeLog`/`SafeLogger`/`NewSafeLogger`/`NewSanitizerWithOptions` |
| **修复方案** | 删除上述符号（保留 `Verify`/`checkAuth`/`NewAuthMiddleware`/`NewAuditLogger` 核心） |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

### D22 删除 ares_arena 死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_arena/metrics.go` 三个废弃 `RecordRecovery`/`RecordFailover`/`RecordConsistency`；`scenario.go` `ScenarioConfig.ParallelActions`/`MaxConcurrent`/`DependsOn` 解析逻辑 |
| **修复方案** | 删除上述废弃 Record* 方法；删除 ParallelActions/MaxConcurrent/DependsOn 的解析逻辑（或实现） |
| **验收标准** | `go build ./...` 绿 |

### D23 删除 ares_ctxutil 死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_ctxutil/` `BackgroundStats` |
| **修复方案** | 删除或 unexport |
| **验收标准** | `go build ./...` 绿 |

### D24 删除 ares_bootstrap 死代码

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_bootstrap/bootstrap.go:193` `Components.ComponentStatus`；`:202` `IsSystemReady`；`provide_mcp.go:75` `SetupMCP`；`provide_llm.go:49/54/59` `NewCallbackRegistry`/`NewLLMClientWithCallbacks`/`WireTaskExecutorCallbacks` |
| **修复方案** | 删除上述符号 |
| **验收标准** | `go build ./...` 绿；`make check` 绿 |

---

## 6. Phase 4：P2 级 bug 修复（与 Phase 2/3 并行）

### B1 ares_security maskString 非 ASCII 截断

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_security/sanitizer.go` `maskString` |
| **修复方案** | 用 `utf8.RuneCountInString` + rune 索引切片，保证非 ASCII 不截断 |
| **验收标准** | 输入中文字符 → 掩码后仍为有效 UTF-8 |

### B2 ares_security sanitizeValue json.Number 死分支

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_security/sanitizer.go` `sanitizeValue` json.Number case |
| **修复方案** | 删除死分支或改用 `UseNumber` 解码器 |
| **验收标准** | 单测通过 |

### B3 tools/discovery CommandTool 内存检查

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/tools/discovery/discover.go` `CommandTool.Execute` |
| **修复方案** | 用 `exec.Command` + `bytes.Buffer` 限量读取，超限直接 kill，`Output()` 在检查之后 |
| **验收标准** | 大输出命令被截断而非 OOM |

### B4 tools/planner extractor 死 import

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/tools/planner/extractor.go` `var _ = math.Round` |
| **修复方案** | 直接移除未用的 `math` import |
| **验收标准** | `go vet ./internal/tools/...` 绿 |

### B5 tools/resources/builtin text_processor domain 标签

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/tools/resources/builtin/builtin.go` `RegisterGeneralTools` 中 `text_processor` 的 `domain:"math"` |
| **修复方案** | 修正为 `"text"` 或移除错误标签 |
| **验收标准** | 工具描述与能力一致 |

### B6 introspect spawnAgent 错误丢弃

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/introspect/dashboard.go` `spawnAgent` 中 `bus.Register` 错误被 `_ =` 丢弃 |
| **修复方案** | 检查 `bus.Register` 返回错误，失败时跳过该 agent 并告警 |
| **验收标准** | 重复 agent ID 注册返回错误并记录日志 |

### B7 ares_flight graph.AddNode 孤儿节点

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_flight/graph.go` `AddNode` |
| **修复方案** | 父节点缺失时建立待挂链表，父节点加入后回补 Child 关系 |
| **验收标准** | 乱序到达事件 → 最终全部正确挂在 DAG 中 |

### B8 ares_flight timeline handleAgentEnd ParentID

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_flight/timeline.go` `handleAgentEnd` |
| **修复方案** | 为 `TimelineEvent` 设置 `ParentID`（来自 agent start 的 id） |
| **验收标准** | Timeline 父子关系完整 |

### B9 agentloop parseArgs 静默吞错误

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/agentloop/engine.go` `parseArgs` |
| **修复方案** | 解析失败返回错误并由调用方反馈给 LLM |
| **验收标准** | JSON 解析失败 → 日志有错误记录 |

### B10 agentloop FriendlyErr 错误链断裂

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/agentloop/engine.go` `FriendlyErr` |
| **修复方案** | 用 `errors.New` 包裹，保留错误链 |
| **验收标准** | `errors.Is` 可匹配 |

### B11 kernelscheduler PreemptLowerPriority 生产空转

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/kernelscheduler/scheduler.go` `PreemptLowerPriority` |
| **修复方案** | 守卫改为同时检查 fabric 可用 agent 数，生产模式也能触发 |
| **验收标准** | 生产路径下低优先级 agent 被正确抢占 |

### B12 kernelscheduler executeUnbound / HasCapableExecutor

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/kernelscheduler/scheduler.go` `executeUnbound`；`executor_registry.go` `HasCapableExecutor` |
| **修复方案** | 清理冗余条件，明确静态 executor 在生产模式的角色（或删除） |
| **验收标准** | 代码逻辑清晰，无冗余判断 |

### B13 llmservice Generate / GenerateEmbedding 错误语义

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/llmservice/service.go` `Generate`、`GenerateEmbedding` |
| **修复方案** | 修正错误语义与哨兵错误统一（若包未删除，见 D8） |
| **验收标准** | 错误类型符合语义 |

### B14 llmservice buildPrompt O(n²)

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/llmservice/service.go` `buildPrompt` |
| **修复方案** | 用 `strings.Builder`，并对角色分隔符做转义/白名单（若包未删除） |
| **验收标准** | 性能无退化 |

### B15 ares_archive summarizeFileChange 未用参数

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_archive/extract.go` `summarizeFileChange` |
| **修复方案** | 使用 `output` 参数或删除参数 |
| **验收标准** | 参数声明被使用或移除 |

### B16 agentipc 原语 bug

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/agentipc/primitives.go` |
| **修复方案** | ① `Request` 校验 `timeout<=0`（取默认值）；② 超时后 handler goroutine 加 ctx 取消；③ `Subscribe` 去重；④ 迟到 reply 关闭孤儿 channel |
| **验收标准** | 并发测试通过；`-race` 绿 |

### B17 ares_archive 提取器事件形状不匹配

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_archive/extract.go` `extractFileChanges`/`extractFilePath` |
| **修复方案** | `extractToolArgs` 支持 args 为 JSON 字符串（对齐 agentloop emitter），或 emitter 侧统一改传 map |
| **验收标准** | 生产事件流中 `RoundRecord.Files` 非空 |

### B18 ares_events Append goroutine 开销

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_events/compactable_store.go:143-211` `Append` |
| **修复方案** | 合并每次 Append 派生的 2 个 goroutine |
| **验收标准** | 同负载下 goroutine 数减半 |

### B19 ares_events 共享 *Event 竞态

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_events/memory_store.go:53-98` `Append` |
| **修复方案** | 共享 `*Event` 传订阅者前深拷贝或文档化只读约定 |
| **验收标准** | `-race` 测试通过 |

### B20 ares_runtime 生命周期竞态

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_runtime/manager_lifecycle.go:37` `Start` 持锁重赋值 `m.g/m.gctx`；`manager.go:614/622` 无锁读 |
| **修复方案** | 统一用 `sync.Map` 或原子指针 |
| **验收标准** | `-race` 测试通过 |

### B21 ares_runtime Stop 持锁 I/O

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_runtime/manager_lifecycle.go:120-146` `Stop` |
| **修复方案** | Snapshot+Save I/O 移出写锁范围 |
| **验收标准** | 大状态时 Stop 不阻塞其他生命周期操作 |

### B22 ares_runtime bus 订阅清理缺陷

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_runtime/bus.go:257-268` |
| **修复方案** | `PluginBus.Stop` 时清理所有订阅 |
| **验收标准** | Stop 后无残留 goroutine |

### B23 cmd/ares handleAction 错误映射

| 项 | 内容 |
|----|------|
| **文件:方法** | `cmd/ares/actions.go:310-318` `handleAction` |
| **修复方案** | 500/400/404 分流，不回传原始 `err.Error()` |
| **验收标准** | 500 错误返回通用错误信息 |

### B24 cmd/ares POST 无 body 上限

| 项 | 内容 |
|----|------|
| **文件:方法** | `cmd/ares/actions.go` 全部 POST 端点 |
| **修复方案** | 加 `http.MaxBytesReader`（建议 1MB） |
| **验收标准** | 超 body 请求返回 413 |

### B25 cmd/ares 审计恒 ok=true

| 项 | 内容 |
|----|------|
| **文件:方法** | `cmd/ares/actions.go:406-413/437-446` legacy kill-all/recover |
| **修复方案** | 按实际结果写 `ok` |
| **验收标准** | 全部失败时审计记录 `ok=false` |

### B26 cmd/ares mcp_null 挂死

| 项 | 内容 |
|----|------|
| **文件:方法** | `cmd/ares/mcp_null.go:70-76` `server.Serve` 返回 nil 时 `sigEg.Wait()` 永久阻塞 |
| **修复方案** | 正常返回 nil 时退出 Wait |
| **验收标准** | MCP 关闭后进程退出 |

### B27 cmd/ares db_migrate 端口不一致

| 项 | 内容 |
|----|------|
| **文件:方法** | `cmd/ares/db_migrate.go:22` 文档 5432 vs `:34` 代码 5433；`:47-50` 未调用 `ensureDatabase` |
| **修复方案** | 统一端口或纠正文档；调用 `ensureDatabase` |
| **验收标准** | 文档与代码一致；库不存在时 migrate 自动创建 |

### B28 cmd/ares main.go 使用说明

| 项 | 内容 |
|----|------|
| **文件:方法** | `cmd/ares/main.go:17/21` |
| **修复方案** | 删除不存在的 `ares workflow run` 与 `ares db setup-test`；补列已存在的 `auth token` |
| **验收标准** | 使用说明与实际命令一致 |

### B29 aresrecovery 反馈环吞 panic

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/aresrecovery/evolution_feedback.go:321-330` |
| **修复方案** | 记日志而非静默吞 |
| **验收标准** | 反馈环中 panic 被日志记录 |

### B30 aresrecovery RestartAgent 失败扣预算

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/aresrecovery/recovery.go:232-241` `RestartAgent` |
| **修复方案** | spawn 成功后才扣预算 |
| **验收标准** | spawn 失败不消耗 MaxRestarts |

### B31 provide_llm.go 统一 openai.New

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_bootstrap/provide_llm.go:36-40` |
| **修复方案** | 按 provider 分发构造器（ollama/anthropic/openrouter 各自适配或显式只支持 openai-compatible 并文档化） |
| **验收标准** | 改 provider 为 ollama → 实际调用 ollama 适配器 |

### B32 SDK 各类小 bug

| 项 | 内容 |
|----|------|
| **文件:方法** | `sdk/scheduler.go:127-130` `Task.Timeout` 文档矛盾（`<=0=no limit` vs 实现 5min）；`sdk/task.go:45-50` `sdkFabric` 无界增长；`sdk/options.go:498-507` `WithAKGEmbedding` baseURL 参数死；`sdk/options.go:266-274` `WithLLMConfig` 覆盖陷阱；`sdk/graph_run.go:58-61` `MaxIterations` 无锁直读 |
| **修复方案** | 逐一按上述修复 |
| **验收标准** | 各单测覆盖 |

### B33 system_runtime Shutdown goroutine 泄漏

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/system_runtime/orchestrator.go:197-207/244-259/283-299` |
| **修复方案** | 30s 超时后放弃等待的 goroutine 加泄漏说明或可取消机制 |
| **验收标准** | 进程生命周期内无有界泄漏 |

### B34 kernelscheduler 主 ticker 无防护

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/kernelscheduler/scheduler.go:227` 主 ticker 直接 `time.NewTicker(s.PollInterval)` |
| **修复方案** | 用 `preemptInterval` 同款防护（≤0 panic） |
| **验收标准** | PollInterval=0 时不 panic |

### B35 introspect POST /api/evolution/feedback 落在只读面

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/introspect/control.go:168-169` |
| **修复方案** | 移出只读面或改挂 actionHandler（带鉴权） |
| **验收标准** | POST feedback 有鉴权 |

---

## 7. Phase 5：配置清理与死配置（第 4 周）

### C1 Memory 死配置

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_config/config.go:426-443` `Memory.SessionMemory`/`UserProfile`/`TaskDistillation`/`MaxHistory`/`EnableDistillation`/`DistillationThreshold` |
| **修复方案** | 接入 bootstrap wireMemory（映射到 ares_memory 配置）或移除 |
| **验收标准** | 字段被消费或从 schema 删除 |

### C2 Tools 死配置

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_config/config.go` `Tools.Defaults`/`Tools.Agents` |
| **修复方案** | 接入工具选择或从 config schema 移除 |
| **验收标准** | 字段被消费或删除 |

### C3 Evolution 14 个 GA 参数

| 项 | 内容 |
|----|------|
| **文件:方法** | `internal/ares_config/config.go:699-760` `Evolution.PopulationSize`/`EliteCount`/`SurvivalRate`/... 共 14 个 |
| **修复方案** | 见 W3 |
| **验收标准** | 见 W3 |

### C4 其他死配置字段

| 项 | 内容 |
|----|------|
| **文件:方法** | `Server.Host`、`LLM.Extra`、`Sub.Category/Triggers/Timeout/Model/Provider`、`Prompts.ProfileExtraction/StyleAnalysis`、`Output.Format/ItemTemplate/SummaryTemplate`、`Validation.Enabled/MaxRetries/CustomSchema`（含下游 Schema/Field/SchemaConfig/Property 整棵类型树）、`Workflow.DefinitionPath/AutoReload/ReloadInterval`、`Storage.PGVector.Enabled/Dimension/TableName`、`Knowledge.TopK`、`Embedding.RedisAddr/Dimension`、`Kernel.PollInterval`、`Kernel.Policy` |
| **修复方案** | 分三类：接线 / 删除 / 文档化为"仅展示" |
| **验收标准** | 无无效配置字段 |

### C5 ares.yaml 同步

| 项 | 内容 |
|----|------|
| **文件:方法** | `ares.yaml` 中 `memory.enable_distillation`/`memory.distillation_threshold`/`reflection` |
| **修复方案** | 移除无效字段或接上实际门控逻辑 |
| **验收标准** | ares.yaml 每个字段均有消费代码 |

---

## 8. Phase 6：防回归 CI 门禁（第 5 周）

### G1 可达性门禁

| 项 | 内容 |
|----|------|
| **文件:方法** | CI 脚本（新增） |
| **修复方案** | CI 增加 `go list -deps ./cmd/ares/... ./sdk/...` 与 `go list ./internal/...` 差集检查——新增"从生产入口不可达"的包必须出现在白名单 |
| **验收标准** | 白名单外零不可达包 |

### G2 config 契约测试

| 项 | 内容 |
|----|------|
| **文件:方法** | CI 脚本（新增） |
| **修复方案** | 反射遍历 cfg 字段，grep 不到消费点的字段在 CI 报错 |
| **验收标准** | 死配置字段数为 0 |

### G3 事件契约测试

| 项 | 内容 |
|----|------|
| **文件:方法** | CI 脚本（新增） |
| **修复方案** | `ares_events` 常量表 vs 全库 emitter/subscriber 字面量的对齐测试 |
| **验收标准** | 订阅的事件必须有 emitter |

### G4 -race 长期运行

| 项 | 内容 |
|----|------|
| **文件:方法** | CI 脚本（新增） |
| **修复方案** | nightly 12h serve 内存曲线与 goroutine 数基线告警 |
| **验收标准** | 内存平稳，无泄漏 |

---

## 9. 附录 A：死代码删除决策表

| 符号 | 所属包 | 决策 | 理由 |
|------|--------|------|------|
| `NewPluginBus` + 11 插件 + 4 Router + Collector + StateSnapshot | ares_runtime | 删除/移入实验 | serve.go 已宣告 |
| `SignalHandler`/`CallbackRegistry`/`CallbackChain`/`PhaseExecutor` | ares_shutdown | 删除 | 生产自建等价物 |
| `SlidingWindowLimiter`/`SemaphoreLimiter`/`Limiter.Reset`/工厂/7 常量 | ares_ratelimit | 删除 | 无生产消费者 |
| `NewHandoff` 整文件 | agents | 删除 | 无文档化场景 |
| core/models 死行为方法（`NewSession`/`IsCompleted`/`AddTask`/...） | core/models | 删除 | 类型被用、方法全死 |
| knowledge/linker/、provider/postgres/、retriever/、service/、store/sqlite/、workflow/ | knowledge | 删除 | 不可达 |
| storage/memory/、storage/postgres/query/ | storage | 删除 | 孤儿 |
| llmservice/ 全部 | llmservice | 删除 | 第三套等价实现 |
| DLQ/HeartbeatMonitor/MessageQueue/MessageRouter/DynamicRouter/Protocol/CodecRegistry/JSONCodec/RateLimiter | ares_protocol/ahp | 删除 | 子系统死 |
| tools/toolsource/ 全部 | tools/toolsource | 删除 | 无人调用 |
| `NewClientFromEnv`/`NewFailoverScorer`/`WithRateLimiter`/`WithRetryPolicy`/`IsOpen`/`IsHalfOpen`/`Config.Extra` | llm | 删除 | 死代码 |
| timeout.go 全部导出 / `NewTemplateEngine`/`NewTemplateRegistry`/`ParseOutput`/`NewSchema`/`NewSchemaGenerator`/`NewTimeout`/`RenderTemplate` | llm/output | 删除或 unexport | 死代码 |
| `NewMetaController`/`NewHypothesisGenerator`/`NewLLMReflector`/`NewPGStrategyStore`/`NewKnowledgeDistiller`/`NewNondominatedSortingSelection`/`NewTruncationSelection`/`NewPopulationGenealogyRecorder`/`NewEvidenceAggregatorProvider`/`HintsForTask`/`RecordStrategyOutcome` | ares_evolution | 删除 | 仅测试 |
| `NewPushService`/`NewReportGenerator`/`NewDistillationRepo`/`NewKnowledgeRetrieverAdapter`/`SearchByVector`/`GetByMemoryType` | ares_memory | 删除（依赖 X1） | ✅实测生产 0，仅测试；源报告 in_degree 33/25 已证伪 |
| `WithCognitionFactory`/`TraceTask`/`TraceAgent`/`ByKind`/`Sandbox.Simulate`/`RecoverTaskCheckpoint`/`RecoverFromAgentDeath`/`AttributeTrajectory`/`NewEvolutionAdapter` | aresrecovery | 删除 | 仅测试 |
| `SetPluginBus`/`SetRouter`/`SetTracer`/`SetExecutionCollector`/`SetLimiter`/`SetCheckpointStore`/`SetScheduler`/`NewGraphWithTracer`/`NewAgentNode`/`NewToolNode`/`Clear`/`RemoveEdge`/`RemoveNode` | workflow/graph | 删除 | 无人调用 |
| `NewAgentExecutor`/`NewHITLFeedbackPlugin`/`NewMemoryInterruptStore`/`NewOutputStore`/`NewWorkflowReloader`/`NewAgentRegistry`/`ListPending` | workflow/engine | 删除 | 无人调用（有 DAG 无执行器） |
| `NewGenealogyCollector`/`AutoDiagnose`/`SuggestFix`/`Replay`/`FilterByType`/`ExportJSON` | ares_flight | 删除 | 死代码 |
| `summarizeFileChange` output 参数 | ares_archive | 删除 | 未用 |
| `Wrap`/`WrapGin`/`PrincipalFromGin`/`FromContext`/`HasPermission`/`SanitizeLog`/`SafeLogger`/`NewSafeLogger`/`NewSanitizerWithOptions` | ares_security | 删除 | 仅测试 |
| `RecordRecovery`/`RecordFailover`/`RecordConsistency` | ares_arena | 删除 | 废弃 |
| `BackgroundStats` | ares_ctxutil | 删除或 unexport | 无人调用 |
| `ComponentStatus`/`IsSystemReady`/`SetupMCP`/`NewCallbackRegistry`/`NewLLMClientWithCallbacks`/`WireTaskExecutorCallbacks` | ares_bootstrap | 删除 | 仅测试 |
| `NewSubAgentCognition` | agentfabric | 删除 | ✅实测仅 executor_test.go |
| `NewLease`/`WithConfidenceSource` | taskfabric | 删除 | 无人调用 |
| `NewProductionMemoryManager`（带参构造器）| ares_memory | **删除构造器（X2 已裁决）** | ✅仅测试引用；生产走 NewMemoryManager；保留 ProductionMemoryManager 类型 + NewMinimalMemoryManager |
| `CrossoverGenome` 接口 | ares_evolution/genome | 删前核实 | ⚠️实测接口仍定义于 genome.go:57，"已移除"说法错误；删前查实现者 |

---

## 10. 附录 B：未接入内核模块决策

| 模块 | cmd/ares 可达性 | 决策 | 说明 |
|------|----------------|------|------|
| `internal/agentloop` | ❌ 不可达 | 删除 | ✅实测全库 0 处 import（含 sdk/examples）；"仅 SDK 用"旧说法已废，删除安全 |
| `internal/detector` | ❌ 不可达 | 删除 | ✅实测全库 0 处 import；源报告"仅 SDK quickstart 使用"错误 |
| `internal/knowledge/linker` | ❌ 不可达 | 删除 | 4 个连接器从未构建 |
| `internal/knowledge/provider/postgres` | ❌ 不可达 | 删除 | PG 知识提供者从未注册 |
| `internal/knowledge/retriever` | ❌ 不可达 | 删除 | 检索器从未构建 |
| `internal/knowledge/service` | ❌ 不可达 | 删除 | ✅实测 Query/Distill 空输入返回 nil；CompileContext 有逻辑但无生产调用者 |
| `internal/knowledge/store/sqlite` | ❌ 不可达 | 删除 | SQLite 知识存储从未构建 |
| `internal/knowledge/workflow` | ❌ 不可达 | 删除 | 知识工作流从未构建 |
| `internal/llmservice` | ❌ 不可达 | 删除 | 第三套 LLM 客户端，字段丢失 |
| `internal/storage/memory` | ❌ 不可达 | 删除 | 内存向量存储从未构建 |
| `internal/storage/postgres/query` | ❌ 不可达 | 删除 | 完全孤儿（含测试 0 引用） |
| `internal/tools/toolsource` | ❌ 不可达 | 删除 | 工具选择器从未构建 |

---

## 11. 附录 C：workflow 计划层（CompilePlan + create_plan）

### Step 1 — 新建转换适配层

| 项 | 内容 |
|----|------|
| **新增文件** | `internal/taskfabric/workflow_plan.go` |
| **新增类型** | `PlanStep`（`ID`, `Capability`, `DependsOn`, `Priority`, `MaxRetries`, `Payload`） |
| **新增方法** | `func (f *Fabric) CompilePlan(steps []PlanStep) ([]string, error)` |
| **修复方案** | 编译期做拓扑校验（环检测）；原子性：遍历中途 `Create` 失败则回滚本批已创建任务 |
| **验收标准** | 单测覆盖线性/菱形/带环 DAG 的编译；编译原子回滚；`make check` 绿 |

### Step 2 — cmd 层投影函数

| 项 | 内容 |
|----|------|
| **新增文件** | `cmd/ares/workflow_plan.go` |
| **新增方法** | `func projectWorkflow(wf *engine.Workflow) ([]taskfabric.PlanStep, error)` |
| **修复方案** | 拒绝动态控制流（Condition/Router/SubWorkflow/LoopConfig）；`RetryPolicy.MaxAttempts → PlanStep.MaxRetries` 换算；`Priority` 给默认值或从 Metadata 解析 |
| **验收标准** | 动态 workflow 被明确拒绝（`errDynamicWorkflowUnsupported`） |

### Step 3 — 新 syscall

| 项 | 内容 |
|----|------|
| **修改文件** | `internal/agentsyscall/syscall.go` |
| **新增方法** | `func (k *Kernel) CreatePlan(ctx context.Context, args CreatePlanArgs) (*CreatePlanResult, error)` |
| **修复方案** | 注册 `create_plan` 工具 + `ToolSchemas`；`Origin` 对整批统一盖章 |
| **验收标准** | 端到端：create_plan → 真实 Schedule → Acquire → RunQuantum → 全部 COMPLETED |

### Step 4 — introspect 面板计划视图

| 项 | 内容 |
|----|------|
| **修改文件** | `internal/introspect/dashboard.go` |
| **修复方案** | 新增只读快照：把一批同 Origin 的任务聚合成"Plan"视图，展示 DAG 拓扑 + 节点状态 |
| **验收标准** | 面板可见 DAG 拓扑与节点状态 |

---

## 12. 附录 D：DLQ 可靠性闭环

| 项 | 内容 |
|----|------|
| **问题** | `SendMessage` 失败时 `p.dlq.Add()` 写入死信，但消费/重投 `DLQProcessor.StartAutoRetry` 生产零调用 |
| **修改文件 1** | `internal/ares_protocol/ahp/protocol.go` |
| **修复方案** | `NewProtocol` 时创建 `DLQProcessor`，暴露访问器；`SendMessage` 失败改用 `AddWithMaxRetries`（预算如 3） |
| **修改文件 2** | `cmd/ares/serve_agents.go`（或对应 bootstrap 接线处） |
| **修复方案** | 用后台 goroutine 启动 `DLQProcessor.StartAutoRetry(ctx, interval)`，注册默认重投 handler；超预算条目进终态并发 `dlq.exhausted` 事件 |
| **修改文件 3** | `internal/introspect/dashboard.go` + `api.go` |
| **修复方案** | 新增 DLQ 只读快照页（死信条数、按 agent/session 聚合、重试次数） |
| **验收标准** | 端到端：发送失败 → 进 DLQ → 自动重投 → 耗尽终态 |

---

## 13. 附录 E：里程碑与验收总表

| 里程碑 | 阶段 | 内容 | 验收标准 |
|--------|------|------|----------|
| **M1**（第 1 周末） | Phase 1 | 10 个 P1 bug 修复 | `go test -race ./...` 全绿；12h 内存平稳；`kill -TERM` 停机日志完整；chaos 注入可观测生效 |
| **M2**（第 3 周末） | Phase 2 | W1-W10 接线项完成 | `/metrics` 有 ARES_* 指标；`/api/insights` 非空；面板任务板/决策页有数据；GA 参数改 yaml 生效；SDK Close 无连接泄漏；create_plan 端到端；DLQ 自动重投 |
| **M3**（第 4 周末） | Phase 3 + 5 | D1-D24 删除 + C1-C5 配置清理 | `go build ./...` && `make check` 全绿；白名单外零不可达包；死配置字段数为 0 |
| **M4**（第 5 周） | Phase 4 + 6 | B1-B35 bug 修复 + G1-G4 CI 门禁 | 全部 bug 修复单测覆盖；CI 门禁生效；nightly 12h 无回归 |

**总预估工作量**：Phase 1（1 周）+ Phase 2（2 周）+ Phase 3（1.5 周）+ Phase 4（并行）+ Phase 5（0.5 周）+ Phase 6（0.5 周）= **约 4-5 周 / 15-20 人日**。
