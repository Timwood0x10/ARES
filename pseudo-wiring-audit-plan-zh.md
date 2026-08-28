# ARES 伪接线与空转实现审计报告 + 闭环修复开发计划

> 审计日期：2026-08-28
> 审计范围：全仓库（重点 `internal/` 47 个子包、`sdk/`、`cmd/ares/`、`api/`）
> 审计方法：① `go list -deps` 从生产入口（cmd/ares、sdk、services）做传递可达性分析；② 逐包 grep 交叉验证每个导出符号的引用情况（排除 `*_test.go`，单独标注仅 examples/仅测试使用）；③ 逐文件人工阅读 4/9 模块组。
>
> **⚠️ 审计状态：4/9 模块组已完成深度审计，5/9 组未完成**（未完成清单见第 5 节）。已完成的组：内核与运行时、可观测性、SDK/Bootstrap/Config、安全/恢复/命令层。

---

## 目录

1. [结论速览（TL;DR）](#1-结论速览)
2. [机械化分析：孤儿包与不可达包](#2-机械化分析)
3. [已完成审计的模块详细发现](#3-模块详细发现)
4. [按严重度汇总的问题清单](#4-按严重度汇总)
5. [未审计模块清单](#5-未审计模块清单)
6. [闭环修复开发计划](#6-闭环修复开发计划)

---

## 1. 结论速览

整个项目**功能面看起来非常庞大，但存在一个系统性模式：大量子系统"建而未接"**——导出的构造函数全库零调用、插件生态无任何生产装配点、config 字段有默认值有校验但从不被消费、事件常量定义了但没有 emitter、监控三套指标一套都没接进生产。

核心数字：

| 类别 | 数量 |
|---|---|
| 从生产入口（cmd/ares + sdk + services）不可达的 internal 包 | 7 个 |
| 全库（含测试）零引用的完全孤儿 | 2 个（`storage/postgres/query`、`storage/memory`≈孤儿） |
| 确认"设置无效"的 config 字段 | 40+ 个 |
| 整体未接线的子系统 | PluginBus 插件生态（11 插件 + 4 Router）、OTel/Prometheus/CostDashboard 三套监控、JWT 中间件形态、限流器一半实现、ares_shutdown 四个组件、agents Handoff/ProfileRegistry |
| P1 级问题（并发 fatal / 内存无界 / 断链 / 静默失效） | 14 项 |
| P2 级问题 | 40+ 项 |

最危险的 5 个发现：

1. **SDK 并发 fatal**：`sdkExecutors` map 跨两把锁读写（sdk/task.go:49 vs kernelscheduler/executor_registry.go:185），并发 RegisterAgent 与调度 drain 会导致 Go runtime 直接 fatal panic。
2. **长驻进程内存无界**：ares_flight 全部聚合结构 + evidence MemoryStore + MemoryEventStore（压实从不 trim）三处线性增长，生产 serve 无条件启用。
3. **可观测性全线空转**：OTel、Prometheus（`/metrics` 只有 go_* 进程指标）、成本仪表盘三套全部未接线，生产唯一运行的 Tracer 是 Noop；introspect 的 insights 永不生成；flight 的 decisions/genealogy/时长三个读面恒空。
4. **混沌注入静默无效**：chaosConfig 只在 Start/Restart/Restore 路径包裹 agent，生产 `RegisterAgent` 注册的裸 agent 完全无视所有注入。
5. **优雅停机可被跳过**：shutdown 总 ctx 与 phase 回调共享 30s 预算，耗尽后 SystemRuntime（MCP/Runtime/FlightRecorder 的真实 Stop）拿到过期 context 被整体跳过；且 `StartShutdown` 防重入在 phase==0 时失效。

---

## 2. 机械化分析

### 2.1 从生产入口不可达的包（`go list -deps ./cmd/ares/... ./sdk/... ./services/...`）

| 包 | 非测试行数 | 实际被谁引用 | 定性 |
|---|---|---|---|
| `internal/ares_integration` | 3412（全部是 `_test.go`） | 无人 | **纯集成测试包**，非生产代码，不算缺陷，但注意它引用了 `storage/memory` |
| `internal/storage/postgres/query` | — | **无任何引用（含测试）** | 完全孤儿，建议删除或接线 |
| `internal/storage/memory` | — | 仅 `ares_integration/vector_store_test.go` | 生产孤儿（内存存储后端从未被装配） |
| `internal/knowledge/provider/postgres` | — | 仅 `examples/11-knowledge-import/akg` | 仅示例使用 |
| `internal/knowledge/retriever` | — | 仅 `internal/knowledge/e2e_test.go` | 生产孤儿 |
| `internal/knowledge/service` | — | 仅 `examples/21-ai-assistant-integration` | 仅示例使用（api/ARCHITECTURE.md 宣称它是 `api/knowledge` 的实现） |
| `internal/knowledge/workflow` | — | 仅 `examples/29-akf-graph-node` | 仅示例使用 |

### 2.2 各 internal 包外部非测试引用数（低引用 = 需人工核实，已在上表之外复核）

```
0  ares_integration(纯测试) ares_protocol core(仅子包models被引) tools(仅子包被引) workflow(仅子包被引)
1  detector(仅sdk/quickstart+测试) eval(仅ares_eval桥接) llmservice(仅api/service/llm)
2  agentsyscall ares_archive ares_ctxutil ares_eval ares_shutdown ares_skills
3  agentloop  4  ares_ratelimit discovery evolution kernelctx
5  ares_arena ares_mcp ares_security scoreutil
6  agentipc kernelscheduler storage
7  ares_observability  8  ares_callbacks ares_flight ares_runtime introspect
9  ares_bootstrap  10 agents  12 ares_experience  13 ares_memory truncate
15 aresrecovery  18 ares_evolution  19 agentfabric taskfabric  21 llm
27 ares_config evidence  49 knowledge  53 ares_events  54 logger  81 errors
```

> 注：`core`/`tools`/`workflow`/`ares_protocol` 根包显示 0 是因为它们只是子包的命名空间（`core/models`、`tools/discovery` 等），子包本身有引用。

### 2.3 工具限制

`golang.org/x/tools/cmd/deadcode` 在 Go 1.27 下 rta 分析 panic（工具自身 bug），符号级死代码改用 grep 逐符号验证。全库 TODO/FIXME/stub 标记扫描仅 23 处，说明问题主要不是"没写完的桩"，而是**写完了但没接**。

---

## 3. 模块详细发现

> 严重度定义：P0 = 崩溃/数据丢失；P1 = 并发 fatal、内存无界、功能静默失效、断链；P2 = 行为偏差、死配置、资源泄漏、死代码；P3 = 瑕疵。

### 3.1 internal/ares_runtime（问题最严重的包）

包职责：Agent 生命周期管理（注册/启动/停止/重启/复活/混沌）+ workflow 插件生态（PluginBus、checkpoint/router/memory/evolution/tool/interrupt/loop/arena 插件、执行收集器、状态快照）。

**伪接线（整个插件生态无任何生产装配点）**：

| 符号 | 位置 | 证据 |
|---|---|---|
| `NewPluginBus` | bus.go:47 | 全仓库无调用；cmd/ares/serve.go:292 注释明言 "PluginBus bridge is gone"；唯一包外引用是 workflow/graph 的 `SetPluginBus/RuntimePluginBus`——而这两个 setter 自身也无生产调用者 |
| `NewObserverPlugin/LoopPlugin/InterruptPlugin/ToolPlugin/CheckpointPlugin/EvolutionPlugin/OutcomeExperienceRecorder/BasicRecoveryPlugin/ArenaPlugin` | observer.go:22 / loop.go:36 / interrupt.go:23 / tool.go:27 / checkpoint.go:144 / evolution_plugin.go:64 / outcome_recorder.go:24 / recovery.go:20 / arena.go:42 | 全部仅本包 `*_test.go` 使用 |
| `NewExpressionRouter/EvolutionRouter/FallbackRouter/MemoryRouter` | router.go:58 / router_evolution.go:22 / router_fallback.go:19 / router_memory.go:30 | 仅测试使用 |
| `NewExecutionCollector` | collector.go:65 | 仅测试使用 |
| `SaveStateSnapshot/LoadStateSnapshot` | state_snapshot.go:34/60 | 仅测试使用 |
| `Manager.GetAgentDAG` | manager.go:189 | **只写不读**：serve_agents.go:76、bootstrap.go:515 写入，读侧只有 ares_bootstrap 测试；serve 实际走 `comp.NewEvolution.UpdateLiveDAG` 另一条路 |
| 事件常量 `EventWorkflowStarted` 等 | events.go:7-13 | 唯一发布者是未接线的 PluginBus → 生产中既不发布也无人订阅 |

**空转方法**（多为文档化 no-op，插件本体未被生产使用）：`ToolPlugin.Start/Stop/BeforeStep`（tool.go:68/71/74）、`InterruptPlugin.Stop/BeforeStep`（interrupt.go:55/58）、`BasicRecoveryPlugin.Start/Stop`（recovery.go:53/56）、`ExpressionRouter.Start/Stop`、`CheckpointPlugin.Stop`、`defaultEvolutionPlugin.Start`、`ArenaPlugin.Capabilities()` 返回 nil（arena.go:57）。

**潜在 bug**：

| 位置 | 级别 | 描述 |
|---|---|---|
| manager.go:130-159 + manager_lifecycle.go:53-66 + manager_chaos.go:217-225 | **P1** | **混沌注入对生产 agent 无效**：`chaosWrappedAgent` 只在 StartAgent/RestartAgent/RestoreAgent 中包裹；生产路径 serve_agents.go:62 `RegisterAgent` 存裸 agent，`Manager.Start` 直接 launch。ares_arena 注入器（SlowAgent/ToolTimeout/PartitionNetwork/CorruptMemory/DisconnectMCP/InjectLLMFailure）写入的 chaosConfig 没有任何读取者，注入静默无效 |
| router_memory.go:67-86 | P1（死代码中的 bug） | MemoryRouter 预取 goroutine 使用的 hook ctx 在 BeforeStep 返回即被 `defer cancel()` 取消，预取机制必然失效；prefetch 单槽并发互相覆盖 |
| manager_lifecycle.go:37 + manager.go:614/622 | P2 | `Start` 持锁重赋值 `m.g/m.gctx`，与 pre-start 复活 goroutine 的无锁读构成竞态 |
| manager_lifecycle.go:120-146 | P2 | `Stop` 持写锁做 Snapshot+Save I/O，慢存储阻塞所有生命周期操作 |
| checkpoint.go:169-175、loop.go:55-57、router_evolution.go:101-104 | P2 | `WithFlushInterval`/`Start` 无锁写字段，构造期外调用会 race |
| bus.go:257-268 | P2 | Subscribe 的清理 goroutine 只响应调用方 ctx 取消，`PluginBus.Stop` 不清理订阅 |
| bus.go:312-341 | P3 | invokeWithTimeout 超时后 fn goroutine 继续运行至自然结束 |
| manager_lifecycle.go:99-102 | P3 | `Stop` 在 `!isStarted` 时跳过 `DoneBackground`，计数泄漏一次 |

### 3.2 internal/agents（含 sub 子包）

包职责：Handoff 结构化移交、AgentProfile 角色注册表、ActiveStrategy 进化策略注入。

**伪接线（断链）**：

| 符号 | 位置 | 描述 |
|---|---|---|
| `ProfileRegistry.ApplyToContext`（写侧） | profile.go:46-153 | 生产零调用；读侧 `GetFromContext`（executor.go:562、chat_cognition.go:544）永远返回 nil → **Ch.10 角色切换注入指令机制整体断链**（读侧接线、写侧缺失） |
| `NewHandoff` 及全部 Handoff 方法 | handoff.go:58-122 | 全仓库仅 handoff_test.go 使用，"Agent 间结构化移交"整体未接线 |
| errors.go 全部哨兵 | errors.go:10-30 | 生产代码无引用 |
| `RegisterFallback` | sub/executor.go:172 | 生产无调用者 → `fallbackHandlers` 恒空 → executeByType 永远走 "no fallback handler" 空结果分支，LLM 失败兜底实际是空结果降级 |
| `base.Messenger`/`subAgent.SendMessage/ReceiveMessage` | sub/agent.go:298-311 + base/agent.go:68-73 | 消息队列路径生产未启用 |

**潜在 bug**：

| 位置 | 级别 | 描述 |
|---|---|---|
| sub/tools.go:171 | P2 | `GetTool` 在 RUnlock 之后读 `b.registry`，无锁读数据竞争（同函数 GetToolSchemas 则正确持锁拷贝） |
| sub/executor.go:172-188 | P2 | `RegisterFallback/SetEventStore/SetCallbacks` 无锁写，与 Execute/ExecuteStep 并发读竞争（SetEventStore 由 subAgent.Start 运行时调用） |
| sub/handler.go:40-49 | 空转 | `handleTaskMessage/handleAckMessage` 空实现直接 return nil（注释称协议级 ack）；`messageHandler.agentID` 字段从未使用 |
| profile.go:180 | P3 | `ErrProfileNotFound` 共享可变指针单例，可被外部污染 |

### 3.3 internal/system_runtime

包职责：组件注册表 + 依赖拓扑排序 + 生命周期编排（Bind→Start→Ready/Stop→Wait）+ 状态快照。

- **伪接线（文档化取舍）**：生产唯一的 `Component` 实现（ares_bootstrap/runtimeComponentAdapter）不实现 Bind/Start → 编排器的 Bind/Start 阶段对所有生产组件永远跳过，实际生效的只有状态记录、Ready/Degraded 报告和 Shutdown；`Registry.Get` 包外无生产调用者。包注释声明"observational（观察型）"设计，**属"明确不接线"类，不计为缺陷，但 Bind/Start 序列在生产是死路径**。
- **潜在 bug**：orchestrator.go:197-207、244-259、283-299 —— Shutdown/stopComponent/cleanupComponent 的 30s 超时后，等待 goroutine 仍永久阻塞在 `eg.Wait()`/`Waiter`（P2，进程生命周期内有界泄漏）；orchestrator.go:187-192 未 Start 就 Shutdown 会对未启动组件调 Stop（P3，依赖幂等）。

### 3.4 internal/kernelscheduler

- **潜在 bug**：scheduler.go:227 主 ticker 直接 `time.NewTicker(s.PollInterval)`，同文件 `preemptInterval()`（:305-311）专门防了 ≤0 panic 但主循环没用（P2，当前生产恒正值，潜伏路径）；scheduler.go:950-984 `Snapshot()` 无锁读配置字段，与 `With*` 并发配置会竞争（P2）；executor_registry.go:114 冗余判断（P3）。
- **死配置**：`Kernel.PollInterval`（ares_config/config.go:88）从未被注入调度器；sdk/scheduler.go:85 硬编码 20ms。

### 3.5 internal/core/models、kernelctx、agentsyscall、agentloop、agentipc

- **core/models**（1219 行）：**"类型被用、行为方法全死"**。`models.Task/TaskResult/UserProfile/RecommendItem` 被大量生产代码使用，但 `Session` 全部生命周期方法（NewSession/IsCompleted/AddTask/Progress/SetStatus/IsExpired）、推荐打分（NewRecommendResult/AddItem/CalculateScore）、`ParseAgentStatus`、`NewPriceRange`、`UserProfile.HasStyle/HasOccasion`、`UserFeedback.SetRating` 全部仅本包测试引用。行为逻辑本身无 bug，属**整包级伪接线**。
- **kernelctx**：未发现问题（WithCallerID 三处生产写入、syscall 两处读取，闭环）。
- **agentsyscall**：未发现问题。
- **agentloop**：engine.go:518-526 `emitLLMCall` 事件缺 ID/ModuleName/Timestamp（P3，对比 emitTaskCompleted 完整构造）；engine.go:400 借用常量 `"tool"` 作 payload key（P3 易碎）。
- **agentipc**：primitives.go:117-118 `Request` 不校验 `timeout<=0`（time.NewTimer(0) 立即超时，P2）；:93-115 超时后 handler goroutine 继续跑（有界泄漏+副作用，P2）；:257-264 Subscribe 不去重、同 topic 重复广播（P2）；:160-172 迟到 reply 写孤儿 channel（P3）。接线本身正常。

### 3.6 internal/ares_observability（伪接线规模最大的包之一）

包职责：Tracer（Noop/Log/OTel）、OTel+Prometheus 双指标、LLM 成本追踪与仪表盘。

**伪接线（三套监控全部未接线）**：

| 符号 | 位置 | 后果 |
|---|---|---|
| `NewOTelTracer/NewMetrics` | otel_tracer.go:70 / metrics.go:33 | 仅测试引用 → OTel 6 个指标 instrument 生产从未创建 |
| `NewPrometheusMetrics` | prometheus.go:51 | 无生产构造点；ares_evolution 只把类型当字段传递（genome_wiring_system.go:34/115），`cfg.Metrics` 判空分支永不为真 → **`ares serve` 的 `/metrics` 端点只有 go_*/process_* 默认指标，所有 ARES_* 指标从未注册**；dream_cycle.go:605/995 的 `RecordEvolutionDeploy` 等调用全在 nil 防护下空转 |
| `RecordCost/RecordLLMTokens/SetActiveAgents/...` | prometheus.go:240-334 | 除测试外零调用 |
| `CostDashboard/NewCostTracker/RegisterCostRoutes` 等 | cost.go | 仅测试引用 → `/api/v1/observability/cost*` 三个端点从未挂载 |
| Tracer 消费链 | llm/client.go:175 → llmservice/service.go:95 | 公共 API `api/service/llm` 的 Config **没有 Tracer 字段**，`toInternal()` 丢弃 → 生产无非 nil Tracer 被安装，`RecordLLMCall`（client.go:253）是死路径 |
| `workflow/graph` 的 tracer | graph.go:83/328 | 接受并保存 tracer 但整个 graph 包没有任何 `tracer.Record*` 调用 |

> **生产实际运行的 Tracer 只有 Noop**（workflow/graph 默认）。"LLM/工具/Agent 可观测性记录"在生产整体空转。

**潜在 bug**：prometheus.go:159-177 注册部分失败后已注册 collector 无注销路径、重试泄漏（P2）；cost.go:314-330 会话驱逐后孤儿 tracker 继续累计、成本从仪表盘"消失"（P2）；noop.go:59-62 traceID 是进程内计数器、可预测可重复（P2）；otel_tracer.go:272-276 零时长 root span 污染统计（P2）。

### 3.7 internal/introspect

- **伪接线**：`Engine.insights` 从未被写入（intel.go:79/342 无任何 append 点）→ **`/api/insights` 永远返回 count 0**，`AcknowledgeInsight` 永远匹配不到，`InsightCooldown` 死配置；`OnInsight` 回调赋值后零调用点（intel.go:165-167）；`FeedIntel` 认领的 `"error"/"tool.error"/"llm.error"/"agent.restarted"` 四个事件字面量在 ares_events 常量表中不存在、全库无 emitter（intel.go:587-588）→ 异常检测实际只覆盖 `task.failed` 和 `failover.completed`；`NewDashboard` 整个 dashboard.go 运行时仅 examples/30 使用；serve 路径（serve_agents.go:153-157）**Collab/Tasks/Decisions 三个 Source 恒为 nil** → 面板的协作图/任务板/调度决策页在生产永远为空（对应的 taskfabric/kernelscheduler 快照源是现成的，dashboard.go:169-177 演示了接法，serve 只是没接）。
- **潜在 bug**：`OnInsight` 无锁写（P2，当前因死代码未爆发）；control.go:168-169 `POST /api/evolution/feedback` 落在"声称 strictly read-only"的 ControlServer 上、无认证写端点（P2）。

### 3.8 internal/ares_flight

包职责：飞行记录器（Timeline/调用图/决策日志/诊断/谱系/记忆管线）。

**伪接线**：`NewGenealogyCollector` 零调用 + bootstrap.go:457 不传 Genealogy → **`/api/flight/genealogy` 永远输出 "No agents"**；`isDecisionEvent` 只认 `"decision."` 前缀而全库无任何 emitter（collector.go:435-438）→ **`/api/flight/decisions` 恒返回 `[]`**，DecisionType 五个常量无赋值点；Timeline 时长配对失效——`pairStartOf` 期待 `tool.result/llm.result` 但 collector 把 started/completed 都映射为 `EventToolCall`（timeline.go:71-114 vs collector.go:387-406）→ **`/api/flight/summary` 的 Tool/LLM 时长与占比恒为 0**；`MemoryPipeline` 记录了但 Pipeline()/Summary()/Stages() 无任何消费者；`Replay/AutoDiagnose/FilterByType/ExportJSON` 等仅测试使用。

**潜在 bug**：

| 位置 | 级别 | 描述 |
|---|---|---|
| timeline.go:55-57、decision.go:33-36、diagnostics.go:45-48、graph.go:44-48、collector.go:30 | **P1** | **全部聚合结构无 ring cap，内存无界增长**（对照 introspect 的 300/200 上限）；bootstrap 在任何有 EventStore 的生产路径无条件启动 recorder 且读端点返回全量拷贝 |
| collector.go:150-161 + bootstrap.go:640 + evidence.go:97-110 | **P1** | 每事件向 evidence store 写一条 ExecutionTrace，无 Postgres 时落到**无界** MemoryStore，双重线性增长 |
| graph.go:68-84 | P2 | 乱序事件产生永久孤儿节点；`g.root` 被每个无 ParentID 节点覆盖，root 语义漂移 |
| collector.go:69-95 | P2 | Start/Stop 无锁写读 `c.cancel` |
| collector.go:281-307 | P2 | 诊断依赖 payload `"error"` 键，两个主要 emitter 不提供 → RootCause 恒空 |

### 3.9 internal/ares_events

- **伪接线**：死事件类型 `EventHandoff/EventSubTaskScheduled/EventSubTaskStarted/EventMemoryFinalize`（types.go:47/50/51/74，全库无 emitter）；**`EventSubTaskResult`（types.go:52）被 ares_skills/outcome_recorder.go:83 订阅但全库无 emitter → 技能结果记录静默失效**；`NewPostgresEventStore/NewPgSummaryRepository/NewPgTrimStore` 全库零构造点（约 750 行 pg 代码仅测试触达，生产用 MemoryEventStore）；`Compactor.CompactAll/ForceCompact/CleanupSummaries/GetSummariesForStream/WithSummarizer` 仅测试调用 → **SummaryTTL（30 天）在生产从不执行**；`MemoryEventStore.Stats()/SubscriberCount()` 生产零调用 → 订阅通道满导致的事件丢弃被计数但无人读取。
- **潜在 bug**：
  - ares_archive/store.go:45-47 构造 `NewCompactableEventStore(mem, repo, nil, DefaultCompactionConfig())` —— trimStore 为 nil、`EnableTrimming=false` → **压实只生成摘要从不删除原始事件 + MemoryEventStore 无界增长（P1）**；summary.go:100-102 `MaxSummariesPerStream` 有默认值无执行点（P2）；compactable_store.go:143-211 每次 Append 派生 2 个 goroutine（P2 开销）；:289-294 归档 drain 失败仍继续压实，一旦开 EnableTrimming 会在未归档时删原始事件，违背持久性承诺（P2）；pg_store.go:399-430 订阅者不取消 ctx 时 pollEvents 泄漏（P2）；memory_store.go:53-98 Append 持写锁把共享 *Event 交给订阅者，消费方修改 Payload 与 Read 读者竞态（P2）。

### 3.10 internal/logger、internal/ares_archive

- **logger**：`ModuleWith` 零调用（仅测试）；logger.go:122-131 `Error` 允许 err==nil 时写 `"error": null` 字段（P2）。其余接线良好（Module/New 54+ 处生产使用）。
- **ares_archive**：提取器与生产事件形状不匹配——`extractFileChanges/extractFilePath` 期待 payload `args` 为 map 或顶层 `path/input` 键，但 agentloop/engine.go:427 传 **JSON 字符串**、sub/executor.go:807 与 chat_cognition.go:345 只传 tool_name/tool_call_id、workflow/graph/node.go:211 只传截断 summary → **生产事件流中 `RoundRecord.Files` 恒为空，verdict 系统性偏空**（P2 静默数据质量缺陷）；`ArchiveWriter.Flush` no-op 且零调用（预留桩）；reader.go:60-94 多流同 round 时 `Read` 返回不确定结果（recall CLI 路径，P2）；identifiers.go:17 commit 正则把任意 ≥7 位 hex token 识别为 commit（P2）；store.go:5 注释失实（无 `ares start` 子命令）。

### 3.11 internal/ares_config（40+ 死配置字段）

完整审计表（✅=真实生效 ❌=设置无效）：

| 字段 | 定义处 | 生效 |
|---|---|---|
| Server.Port | config.go:201 | ✅ |
| **Server.Host** | config.go:200 | ❌ serve_routine.go:165 绑定 `":port"` 忽略 Host，仅 status 展示 |
| LLM.Provider/APIKey/BaseURL/Model/Timeout/MaxTokens/MaxPromptLength/Fallbacks/ScorerAPIRate/Burst | config.go:226-236 | ✅ |
| **LLM.Extra** | config.go:233 | ❌ 传入 llm.Config.Extra 后无人读，端到端死 |
| Agents.Peers / Sub.ID/Type/Priority/MaxToolRounds/MaxRetries/Dependencies | config.go:248,276-297 | ✅ |
| **Sub.Category/Triggers/Timeout/Model/Provider** | config.go:279-284 | ❌ 无消费者 |
| Prompts.Recommendation | config.go:303 | ✅ |
| **Prompts.ProfileExtraction/StyleAnalysis** | config.go:302/304 | ❌ internal/llm/output/template.go:116/136 有自己的硬编码同名 prompt，不从 config 读 |
| **Output.Format/ItemTemplate/SummaryTemplate** | config.go:308-312 | ❌ 仅 defaults/validate 内部出现 |
| Validation.SchemaType/RetryOnFail/StrictMode | config.go:355-358 | ✅ |
| **Validation.Enabled/MaxRetries/CustomSchema**（含下游 Schema/Field/SchemaConfig/Property 整棵类型树 config.go:315-390） | config.go:354/357/359 | ❌ 全库零使用 |
| **Workflow.DefinitionPath/AutoReload/ReloadInterval** | config.go:393-397 | ❌ |
| Storage.Enabled/Type/Host/Port/Username/Password/Database/SSLMode | config.go:400-408 | ✅ |
| **Storage.PGVector.Enabled/Dimension/TableName** | config.go:409,413-417 | ❌ |
| Memory.Enabled / EnableRAG/RAGTopK/RAGMinScore / Archive.* | config.go:425,448-472 | ✅ |
| **Memory.SessionMemory.*/UserProfile.*/TaskDistillation.*/MaxHistory/EnableDistillation/DistillationThreshold** | config.go:426-443,502-525 | ❌ 13 个字段全死（ares_memory 侧是另一类型同名字段） |
| Knowledge.RetrievalEnabled/MinScore | config.go:489/498 | ✅ |
| **Knowledge.TopK** | config.go:493 | ❌ bootstrap 检索只读 MinScore |
| MCP.Servers | config.go:650-683 | ✅ |
| Evolution.Enabled/Deployment.*/LLMScoring.*/MinInterval | config.go:695-793 | ✅ |
| **Evolution.PopulationSize/EliteCount/SurvivalRate/MutationRate/MinMutationRate/MaxMutationRate/Generations/BreedingPoolRatio/SelectionStrategy/TournamentSize/CrossoverType/TargetFitness/SteadyState/SteadyStateReplaceRate** | config.go:699-760 | ❌ **14 个 GA 参数全死**——wireGAEvolution（bootstrap_steps.go:195-200）用 `evolution.DefaultSystemConfig()` + 硬编码 base strategy，GA ticker 硬编码 5 分钟（:251） |
| Embedding.Enabled/BaseURL/Model/Timeout | config.go:190-195 | ✅ |
| **Embedding.RedisAddr/Dimension** | config.go:193-194 | ❌ provide_distillation.go:61 显式传 nil cache |
| Discovery.Enabled/Interval/ProjectDir | config.go:179-183 | ✅ |
| Kernel.QuotaApply*/EvolutionApply*/RecoverySweep*/DispatchTimeout/LeaseTTL/MaxRestarts/Resources | config.go:93-125 | ✅ |
| **Kernel.PollInterval** | config.go:88 | ❌ 从未注入调度器 |
| **Kernel.Policy** | config.go:85 | ❌ 运行时硬编码 `PolicyTaskFabric`（kernel_bridge.go:28），仅 status 展示读取；注释宣称的 legacy 路径与 `wireKernelPolicy` 函数不存在 |
| Kernel.Chaos.*（9 字段）/ Security.JWT* | config.go:137-171,213-221 | ✅ |

**其他 config 问题**：`SetAllowedConfigDir`（config.go:28）全库唯一调用点是测试 → **Load 的路径穿越白名单防护 100% 空转**（P2 安全）；`LoadFromEnv`（config.go:577）宣称"环境变量覆盖 YAML"但全库无调用；config_validate.go:100-102 强制 SubAgent `Timeout>=1` 但 NewMinimalConfig 注入的默认 agent Timeout 全为 0 且不走 Validate，两套入口自相矛盾（P2）；store.go:79-92 Reload 浅拷贝共享底层 map/slice（P2）；redacted.go:17-47 Redacted() 浅拷贝可穿透（P2）。

### 3.12 internal/ares_bootstrap

**仅测试引用的导出符号**（全库零生产调用）：`Components.ComponentStatus`（bootstrap.go:193）、`Components.IsSystemReady`（:202）、`SetupMCP`（provide_mcp.go:75，自称 backward-compatible alias 但无 caller）、`NewCallbackRegistry/NewLLMClientWithCallbacks/WireTaskExecutorCallbacks`（provide_llm.go:49/54/59）。

**伪接线**：bootstrap.go:385-386 Memory 为 nil 时退回 `NewMinimalMemoryManager()`，此后 MemoryPatchExecutor 全部 patch 写进无人消费的迷你 manager（P2）；bootstrap_steps.go:165-317 `wireGAEvolution` 无视 cfg.Evolution 全部 14 个 GA 参数（见上）；provide_llm.go:36-40 **无论 provider 是 ollama/anthropic/openrouter 一律注册 `openai.New` 构造器**（P2）；provide_new_evolution.go:395-417 `noopKnowledgeExecutor` 桩（Apply 返回硬编码、Snapshot 永远报错，文档化诚实桩）；provide_new_evolution.go:202/231-246/370/380-385 `_ =` 丢弃 RegisterComponent/UpdateLiveDAG 错误，重名 key 静默失败（P2）。

**潜在 bug**：bootstrap.go:322-339+547-551 bgGroup goroutine 在后续步骤失败时不被 runCleanups 等待（P2）；bootstrap.go:441-447 evidence/distillation PG pool 的 Close 只挂失败路径，成功时依赖入口（P2）；bootstrap_steps.go:278-313 LLM suggestion ticker 每 15 分钟无条件烧 LLM 调用、无预算无开关（P2）；deployment_wiring.go:27 applyCount 普通 int 无锁（P2）；maintenance_worker.go:110-117 用 JWTSecret 哈希当 AES key（已注释，影响有限）。

### 3.13 sdk/（公开 SDK）

**P1**：

| 位置 | 描述 |
|---|---|
| task.go:49 / scheduler.go:195 vs kernelscheduler/executor_registry.go:185-193 | **数据竞争（可致进程 fatal）**：`RegisterAgent/ensureExecutor/registerGraphAgents` 在 `agentMu` 下写 `sdkExecutors`，共享 scheduler 用另一把 `execMu` 每 20ms 遍历**同一个 map**（按引用传入）。首次 Submit 启动 drain 后，并发 RegisterAgent = concurrent map iteration + write → Go runtime fatal |
| config.go:370-382 | **YAML `memory.enabled: false` 完全无效**：ToOptions 只在 true 时追加选项，false 时什么都不做，而 defaultConfig 默认 Enabled=true。显式关闭被静默吞掉 |
| sdk.go:394-440 | **Close 释放不完整（Bootstrap 路径）**：只 cancel+Wait，Bootstrap 成功路径注册的 cleanups 永不执行——bootstrap 侧 MemoryManager、MCPManager、storage.enabled 时的 evidence/distillation **PostgreSQL 连接池全部不关闭**，WithPostgres 长驻进程每次 Close 泄漏连接 |
| config.go:103-112 → memory_wiring.go:422 | YAML `knowledge.chunk_size/chunk_overlap/top_k` 校验严格、**应用为零**（消费点只有 MinScore） |

**P2（节选，全部经 grep 验证）**：

- sdk.go:129/371 `Runtime.evolutionStore` 只写不读；knowledge.go:166-169 vs 200-205 创建**两个不同的 `memStrategyStore`**，注册给 provider 的永远无人写入 → evolution→AKG 决策知识管道空转。
- sdk.go:133/340/371 `Runtime.evoComponents` 只写不读：fallback 路径 `WithEvolution()` 装出的 Genome/Diff/Patch/Coordinator 无任何驱动（无 ticker、无 StrategyStore 写入），注释宣称的"runtime tracks agent performance and can evolve instructions"实际由独立的 `Runtime.Evolve` 完成。
- knowledge.go:383 SDK fallback 给 `ProvideNewEvolution` 传 nil memoryStore → memory 维度 evolution patch 在 SDK 模式必然 "no executor" 失败。
- agent.go:77-110 `Agent.Stream` 是**模拟流式**（同步跑完 Run 后按 10 字符切片伪装 chunk），仓内零调用。
- agent.go:141-147 CreateSession 失败回退随机 uuid，随后 AddMessage 向不存在的 session 写消息，错误被吞（P2）。
- scheduler.go:127-130 vs task.go:23-24 `Task.Timeout` 注释 "<=0 = no limit"，实现 `<=0` 一律 5 分钟硬超时，文档矛盾。
- task.go:45-50 vs scheduler.go:184-197 先 Submit 后 RegisterAgent 同 capability 会覆盖 executor，与 "first agent wins" 承诺冲突。
- scheduler.go:110-155 `submitThroughScheduler` 完成后不 Delete fabric task → `sdkFabric` 每次 Submit 遗留 Task+Checkpoint（含完整 Result），长期运行无界增长（taskfabric.Delete 存在但 SDK 未用）。
- options.go:498-507 `WithAKGEmbedding` 的 baseURL 参数死（存入后全库无读取）；:266-274 `WithLLMConfig` 整体替换覆盖先前 APIKey/BaseURL，顺序敏感无告警；:293-298 `WithDefaultMemory` 文档化 no-op。
- bootstrap_runtime.go:30-51 buildBootstrapConfig 丢弃 SDK LLM 的 MaxTokens/Temperature/MaxPromptLength 及 memory/distill/knowledge 调优、MCP 连接固定空列表——bootstrap 核心与 SDK 实际配置保真度不一致；:38-41 失败仅 Warn 静默回退 SDK wiring，装配回归难以察觉。
- evolve.go:32-34 注释称 "uses the LLM to generate variations"，实现是纯 GA；population=10/generations=3/seed=42 硬编码；:162-197 30 次同步真实 LLM 调用无预算上限。
- graph_run.go:58-61 `MaxIterations` 无锁直读 vs 其他字段持 RLock 读，两套访问方式（P2）；:214 空 agentName 运行期才报错。
- quickstart.go:37 `MustNew` 及整条 quickstart 链全库零调用（仅 sdk_test）；cleaning.go:28 `NewContextCleaner` 仓内零调用；sdk.go:474 `KnowledgeStore()` 零调用；`Runtime.Evolve` 仅 examples 使用。

### 3.14 internal/ares_security

- **伪接线**：`AuthMiddleware.Wrap/WrapGin/PrincipalFromGin`（middleware.go:85/101/115）仅测试使用——生产 serve 未挂任何 HTTP 中间件，走 actionHandler.checkAuth 手动逐路由校验；`FromContext`（middleware.go:75）全库含测试无调用者，principal 从未进入 request context；`HasPermission`（rbac.go:78）仅测试；**`PermAdmin`（rbac.go:36-38）从未被任何路由 require**——`NewAuthMiddleware` 固定 `PermWrite`（serve_routine.go:186），因此 kill-all/random-kill/recover 对 operator JWT 与 API key（checkAuth 授予 RoleOperator，actions.go:90）全部放行，**声明的 "破坏性 chaos 仅 RoleAdmin" 策略从未接线（P2 安全）**；`SanitizeLog/SafeLogger/NewSafeLogger/NewSanitizerWithOptions`（sanitizer.go:465-477）仅测试。
- jwt.go 实现质量好（hmac.Equal 常量时间、固定 HS256、exp/iat 校验）。AuditLogger.Auth/Action 在生产链路上。

### 3.15 internal/ares_ratelimit

- **伪接线**：`SlidingWindowLimiter`（sliding_window.go:20）、`SemaphoreLimiter/WeightedSemaphoreLimiter`（semaphore.go:18/131）、`Limiter.Reset`（limiter.go:16）、工厂 `NewFactory/CreateLimiter/DefaultFactory`（limiter.go:59/107/116）全库无非测试调用者；`LimiterConfig.Timeout/RefillRate` 死配置（token bucket 用 rate 不用 RefillRate）；constants.go:13-38 七个 Default* 常量零引用；**`workflow/graph.Graph.limiter` 字段只写不读**——`SetLimiter/NewGraphWithLimiter` 零调用，且即便接了也没有任何执行路径读取 → Graph 维度限流拦截点从未接线。真正生效的拦截点是 internal/llm（generate.go:112、chat.go:53、client.go:371）和 serve_chaos.go:460。
- **潜在 bug**：sliding_window.go:100-102 `Rate()` 不持锁读（良性不一致）；semaphore.go:62-77 `Allow` 计入固定键 "default" 而 Acquire/Release 用调用方 key，混用会永久占用 slot（P2，无生产调用者暂为潜在面）。

### 3.16 internal/aresrecovery

- **伪接线**：`Recovery.RestartCount`（recovery.go:265）、`Chaos.InjectedFailures`（chaos.go:95）、`Sandbox.Simulate`（sandbox.go:184，生产与 examples 只用 Replay）、`EvolutionAwareIPC.Bus()`（evolution_ipc.go:175）、`WithCognitionFactory`（recovery.go:113——E1"注入可执行认知体"钩子从未安装，`cogFactory` 分支生产不可达）、`GlobalTracer.TraceTask/TraceAgent/Close`（global_tracer.go:102/107/173——生产唯一写路径是 TraceMessage，task/agent span 永远为空，而 dashboard_observability.go:153-164 持续读取空数据）。
- **潜在 bug**：evolution_feedback.go:321-330 反馈环 recover `_ = r` 静默吞 panic 无日志（P2）；recovery.go:232-241 RestartAgent 先扣预算再 spawn，spawn 失败也消耗 MaxRestarts（P2）；global_tracer.go:233-246 cloneSpan 浅拷贝 Detail map（P2）。
- 核心恢复循环（kernel_loop.go:223-355）接线完整正常。

### 3.17 internal/ares_shutdown

- **伪接线**：`SignalHandler`（signal.go:25 全部导出面）、`CallbackRegistry/RegisteredCallback`（callbacks.go:10）、`CallbackChain`（callbacks.go:177）、`PhaseExecutor`（phase.go:39）全库无非测试引用。生产实际只用了 `NewManager/RegisterPhase/AddCallback/StartShutdown`。
- **潜在 bug**：manager.go:118 **`StartShutdown` 防重入判断 `if m.currentPhase != 0` 而 PhasePreShutdown 恰为 iota 首值 0 → 第一阶段执行期间二次调用可并发重跑全部回调（P1 潜在，serve 目前靠"第二信号强制 os.Exit"掩护）**；manager.go:322-328 IsShutdown 在 PreShutdown 阶段返回 false（同根因）；signal.go:37-58 started TOCTOU；signal.go:134-136 SetContext 无锁覆盖且丢弃 cancel；phase.go:75-76/:128-129 retries/endTime 无锁写 vs Retries()/Duration() 持锁读（P2）。

### 3.18 cmd/ares（生产入口）

装配完整性：serve 主链路（Bootstrap→agents→kernel→scheduler→HTTP→shutdown 钩子）接线完整；逐一核对 actions.go:151-253 所有路由分支均有真实实现且经 checkAuth（只读 introspect/metrics 除外）。**未发现"注册了 handler 但 handler 空转"的路由**。

**伪接线**：

| 符号 | 位置 | 描述 |
|---|---|---|
| `kernelHandle.peerRegistry` | kernel.go:47、serve_agents.go:237、serve.go:285 | **双写零读**：peer 注册表构建并两次 "retained"，但无任何组件通过它发消息或查找能力（仅 reg.IDs() 打日志） |
| `kernelHandle.flipped` / `kernelHandle.flag` | peer_mode.go:141/120 | 只写不读 |
| `startServeHTTPAndHooks` 返回值 | serve.go:315 | 返回的 *http.Server 被丢弃；形参 cfgStore/intelEngine 函数体内零使用（参数冗余） |
| `cfg.Kernel.Policy` | kernel_bridge.go:28 | 运行时完全不受配置影响；status.go:536 对 legacy 显示 "Task Fabric in shadow" 与实际（shadow 关闭）不符 |
| db_check_rls.go 环境变量 | db_check_rls.go:19-22 vs :33-44 | 文档声称 DB_HOST 等 5 个环境变量，实际全部硬编码 127.0.0.1:5433/postgres |
| main.go:17/21 使用说明 | — | 列出不存在的 `ares workflow run` 与 `ares db setup-test` 命令；漏列已存在的 `auth token` |

**空转方法**：`validateServeConfig`（serve_routine.go:301-306）注释称 "enforces the dependencies"，实际仅 nil 检查；`knowledgeBuildCmd.RunE`（knowledge_cli.go:24-31）恒返回静态错误文本（设计性桩已注明）。

**潜在 bug（P2）**：serve.go:104-110 **shutdown 总 ctx 与 phase 回调共享 30s 预算，耗尽后 `shutdownSystemRuntime`（MCP/Runtime/FlightRecorder 的真实 Stop）拿到过期 context 被整体跳过**；actions.go:310-318 handleAction 把一切错误（含 500 类）统一映射 404 且原文回传 err.Error()（状态码失真+内部细节泄漏）；actions.go:406-413/437-446 legacy kill-all/recover 审计恒 ok=true 即使全部失败；actions.go:73-110 JWT deny 后 API key allow 不补记审计；所有 POST 端点无请求体大小上限（无 MaxBytesReader）；actions.go:550 `err.Error() != "EOF"` 字符串比较；serve_agents.go:146-149 + actions.go:129-149 `/introspect`、`/metrics`、eventstream 无鉴权且暴露原始事件负载（已文档化的部署边界风险）；dev.go:104-107 runDoctor 打印 API key 前 8 字符；mcp_null.go:70-76 server.Serve 正常返回 nil 时 sigEg.Wait() 永久阻塞；db_migrate.go:22 vs :34 文档端口 5432 代码默认 5433；db_migrate.go:47-50 从未调用 ensureDatabase，库不存在时 migrate 直接失败（与 Long 描述不符）。

---

## 4. 按严重度汇总

### P1（必须优先处理：崩溃 / 无界内存 / 静默失效 / 断链）

| # | 位置 | 问题 |
|---|---|---|
| 1 | sdk/task.go:49、sdk/scheduler.go:195 vs kernelscheduler/executor_registry.go:185 | sdkExecutors 跨锁并发读写 → 运行时 fatal |
| 2 | ares_flight（timeline/decision/diagnostics/graph/collector）+ collector.go:150-161 + evidence MemoryStore | 长驻进程内存无界（聚合结构无 cap + 每事件 evidence 写入无界放大） |
| 3 | ares_archive/store.go:45-47 + ares_events | 生产压实配置 EnableTrimming=false + MemoryEventStore 无界 + SummaryTTL 从不执行 → 事件存储无界 |
| 4 | ares_observability 全包 | OTel/Prometheus/CostDashboard 三套监控全部未接线，生产唯一 Tracer 是 Noop，`/metrics` 无 ARES_* 指标 |
| 5 | ares_runtime（bus.go 等 + workflow/graph setter） | PluginBus + 11 插件 + 4 Router + Collector + StateSnapshot 整体无生产装配 |
| 6 | ares_runtime manager.go:130 等 | 混沌注入对生产 RegisterAgent 路径静默无效 |
| 7 | agents/profile.go 写侧缺失 | ProfileRegistry ApplyToContext 生产零调用 → 角色指令注入断链；Handoff 机制整体仅测试 |
| 8 | ares_events types.go:52 + ares_skills/outcome_recorder.go:83 | EventSubTaskResult 有订阅者无发布者 → 技能结果记录静默失效 |
| 9 | introspect intel.go:79 | insights 引擎从未写入 → `/api/insights` 恒空，OnInsight 死回调 |
| 10 | ares_flight collector.go:435 + bootstrap.go:457 | decision 事件无 emitter → decisions 恒空；Genealogy 未装配 → genealogy 恒 "No agents" |
| 11 | ares_flight timeline.go vs collector.go:387-406 | 事件类型映射错位 → Tool/LLM 时长统计恒 0 |
| 12 | sdk/config.go:370-382 | YAML memory.enabled:false 静默失效 |
| 13 | ares_config config.go:699-760 | Evolution 14 个 GA 参数全死（wireGAEvolution 硬编码） |
| 14 | ares_shutdown manager.go:118 + cmd/ares serve.go:104-110 | StartShutdown 防重入 phase==0 失效（双跑回调）；shutdown 预算耗尽跳过 SystemRuntime 真实 Stop |

### P2（行为偏差 / 死配置 / 死代码 / 资源泄漏）

- config 死字段 40+（见 3.11 完整表）；`SetAllowedConfigDir` 路径防护空转；`LoadFromEnv` 未接线。
- sdk：Close 不释放 bootstrap 资源（PG 池泄漏）；knowledge chunk 参数死；双 memStrategyStore 空转；evoComponents/evolutionStore 只写不读；sdkFabric 无界增长；Task.Timeout 文档矛盾；WithAKGEmbedding baseURL 死参数；WithLLMConfig 覆盖陷阱；Agent.Stream 模拟流式；MustNew/quickstart/NewContextCleaner/KnowledgeStore 零调用。
- ares_bootstrap：7 个导出符号仅测试；provide_llm 一律 openai.New；noopKnowledgeExecutor 桩；`_ =` 丢弃注册错误；LLM suggestion ticker 无预算。
- cmd/ares：peerRegistry 双写零读；Kernel.Policy 运行时无效；handleAction 404 化一切错误+泄漏细节；审计恒 ok=true；无 body 上限；/introspect、/metrics 无鉴权；doctor 泄漏 key 前缀；mcp_null EOF 挂死；db 命令文档三处不符；main.go 使用说明列出不存在的命令。
- ares_security：PermAdmin 从未接线（kill-all 对 operator 放行）；中间件形态/FromContext/HasPermission/SanitizeLog/SafeLogger 仅测试。
- ares_ratelimit：SlidingWindow/Semaphore/Factory/Reset 生产零引用；Timeout/RefillRate 死配置；Graph.limiter 拦截点未接线。
- aresrecovery：WithCognitionFactory 未安装；GlobalTracer task/agent span 无写路径；Sandbox.Simulate/RestartCount/InjectedFailures/Bus 零引用；反馈环吞 panic。
- ares_shutdown：SignalHandler/CallbackRegistry/CallbackChain/PhaseExecutor 生产零引用；内部 TOCTOU/无锁竞态。
- ares_runtime：m.g/m.gctx 竞态；Stop 持锁 I/O；bus 订阅清理缺陷；scheduler 主 ticker 无防护；Snapshot 无锁读；system_runtime Shutdown 超时 goroutine 泄漏。
- agentipc：Request 零超时、订阅不去重、handler 有界泄漏。
- ares_archive：提取器与生产事件形状不匹配（Files 恒空）；Read 多流不确定；Flush 桩。
- introspect：FeedIntel 死事件字面量；Collab/Tasks/Decisions 三源 serve 未接；POST feedback 落在只读面。
- ares_events：Postgres 事件存储全链未接线；MaxSummariesPerStream 无执行点；Append 派生 goroutine 开销；归档失败仍压实。
- logger：ModuleWith 零调用；Error err==nil 写 null 字段。
- sub/tools.go GetTool 无锁读；sub/executor Set* 无锁写；RegisterFallback 未接线。

---

## 5. 未审计模块清单（5/9 组，因限流中止）

以下模块**尚未做逐方法深度审计**（仅完成第 2 节机械化可达性分析），结论待补：

| 组 | 包 | 规模 | 已知机械化线索 |
|---|---|---|---|
| ③ 记忆/知识/存储 | internal/ares_memory、internal/knowledge/**、internal/storage/**、internal/truncate、internal/ares_experience、services/embedding、api/core、api/embedding、api/knowledge、api/experience | ~15K 行 | knowledge/service、knowledge/workflow、knowledge/retriever、knowledge/provider/postgres、storage/memory、storage/postgres/query 从生产入口不可达（见 2.1）；其余待审 |
| ④ 演化/评估 | internal/ares_evolution、internal/evolution、internal/ares_eval、internal/eval、internal/evidence、internal/scoreutil、evaluation/、internal/ares_arena | ~20K 行 | eval 仅被 ares_eval 桥接引用；ares_arena 引用数 5；evidence.MemoryStore 已确认无界（3.8 联动）；其余待审 |
| ⑤ 工具/MCP | internal/tools/**（discovery、envcap、planner、resources、toolsource）、internal/ares_mcp、internal/discovery、internal/ares_skills、api/tools、api/mcp、compat/ | ~30K 行 | tools 子包被引 0（根包仅命名空间）；ares_skills 已确认一处断链（3.4 EventSubTaskResult）；其余待审 |
| ⑥ Workflow/任务编排 | internal/workflow/**（engine、graph）、internal/taskfabric、internal/agentfabric | ~30K 行 | 已知：engine 无执行器（见 workflow-engine-wiring-plan-zh.md，正在按该计划接线）；graph 的 SetPluginBus/SetTracer/SetLimiter 等 setter 无生产调用者（3.1/3.6/3.15 联动）；MutableDAG 唯一生产用途是演化拓扑载体；其余待审 |
| ⑧ LLM/基础设施 | internal/llm、internal/llmservice、internal/detector、internal/errors、internal/ares_ctxutil、internal/ares_protocol/ahp | ~25K 行 | llmservice 仅被 api/service/llm 引用且 Tracer 字段在公共 API 层被丢弃（3.6 联动）；detector 仅 sdk/quickstart（零调用链）+测试引用；ares_protocol 整包生产不可达（ahp 仅 2783 行）；llm 的 failover/provider 注册已发现一处问题（provide_llm.go:36 一律 openai.New）；其余待审 |

> 补审建议：按第 6 节 Phase 0 相同的模板（伪接线/空转/bug 三清单 + grep 验证）执行，预计 5 个审计批次。

---

## 6. 闭环修复开发计划

总原则：**每个"建而未接"的子系统必须做出二选一决策——接线（wire it）或删除（kill it），不允许第三种"挂着但没用"的状态**。删除优先于接线：接一个没人用的子系统 = 增加维护面；删一个伪接线子系统 = 缩小表面积。决策矩阵见 6.2。

### Phase 0：决策登记（0.5 天，无代码）

对第 3、4 节每个伪接线符号，在本文档追加一列"决策：接线 / 删除 / 文档化为内部实验 API"。建议默认值：

| 子系统 | 建议决策 | 理由 |
|---|---|---|
| PluginBus + 11 插件 + 4 Router（ares_runtime） | **删除或移入 experimental/**，保留 Manager 主体 | serve.go 注释已宣告 "bridge is gone"；workflow/graph 的对应 setter 一并删；与 workflow 编译计划（选项 A）冲突的旧执行模型 |
| OTel + Prometheus + CostDashboard | **接线**（优先级最高的接线项） | 可观测性是已交付功能（introspect 面板）的数据源缺口，接线成本低（见 Phase 2 W1） |
| JWT AuthMiddleware 中间件形态 | **删除** Wrap/WrapGin/FromContext，保留 Verify + checkAuth 手动链 | 生产已选手动链且工作正常；或反向：把 checkAuth 换成中间件，二选一，不要两套 |
| PermAdmin / RBAC 分级 | **接线**：chaos 破坏性路由改 require PermAdmin | 安全语义已声明未实现，接线成本小 |
| SlidingWindow/Semaphore limiter | **删除**（保留 TokenBucket）或补测试后保留 | 无生产消费者 |
| ares_shutdown 的 SignalHandler/CallbackRegistry/CallbackChain/PhaseExecutor | **删除** | 生产自建了等价物（serve.go signal.Notify + Manager） |
| agents Handoff + ProfileRegistry | ProfileRegistry **接线写侧**（sub executor 构造时 ApplyToContext）；Handoff **删除** | 角色注入是文档化功能（Ch.10），断链修复成本低；Handoff 无任何文档化场景 |
| core/models 行为方法 | **删除**死方法，保留被 SQL scan 使用的类型 | "类型被用、方法全死"是典型腐烂面 |
| EventSubTaskResult / outcome_recorder | **接线 emitter 或删除订阅**：若 sub-task 结果需要记录，在 agentloop/sub executor 完成处 emit；否则删 | 静默失效最恶劣 |
| introspect insights 引擎 | **接线**：实现 FeedIntel → insights 生成（异常检测规则已在），并接 OnInsight | 面板已有 /api/insights 端点，只缺数据 |
| introspect Collab/Tasks/Decisions 三源 | **接线**：serve_agents.go 照 dashboard.go:169-177 的现成接法补 3 个 Source | 演示代码里已写好接法 |
| flight DecisionLog/Genealogy | **接线**：统一 decision 事件常量 + bootstrap 传 GenealogyCollector；或删除对应读端点 | 与事件对齐（Phase 3）联动 |
| knowledge/service、knowledge/workflow、knowledge/retriever、provider/postgres、storage/memory、storage/postgres/query | 逐个决策：postgres provider 若是路线图项则**接线为可选后端**（storage.enabled 已有开关位），否则删 | 见 2.1 |
| ares_protocol/ahp | **删除或移出 internal/**（examples 用则移 api/ 或 examples 内联） | 生产零可达 |
| dead config 字段（40+） | 分三类：接线（Evolution GA 参数、knowledge chunk_*、Kernel.PollInterval——接线成本低收益大）/ 删除（Output.*、Validation.CustomSchema 类型树、PGVector、Sub.Category 等）/ 文档化为"仅展示" | 逐字段决策登记 |
| ares_integration | 保留（纯测试包），但把 storage/memory 的唯一引用改掉或删 | — |

### Phase 1：P1 修复（第 1 周，5 个 PR）

1. **PR-1 SDK 并发 fatal**：`sdkExecutors` 的所有写入改为调用 `sched.RegisterExecutor`（走 scheduler 自己的 execMu），删除 SDK 侧旁路写；或 Runtime 持有一把统一锁。补一个并发 RegisterAgent + Submit 的 race 测试（`-race` 必跑）。验收：`go test -race ./sdk/...` 绿。
2. **PR-2 内存无界三连**：
   - ares_flight 五个聚合结构加 ring cap（对齐 introspect 的 300/200 上限），读端点分页；
   - evidence.MemoryStore 加 cap 或默认要求 Postgres（无 PG 时 WARN 并限流写入）;
   - ares_events 生产构造改 `EnableTrimming=true` + trimStore 接 ares_archive sink（archive 已实现），或至少把 `Stats().dropped_events` 暴露到 /metrics。
   验收：12h 长跑 serve 内存曲线平稳。
3. **PR-3 停机闭环**：ares_shutdown 防重入改 `atomic.CompareAndSwap` 布尔（不依赖 phase 值）；cmd/ares serve 把 SystemRuntime Shutdown 从共享 shutdownCtx 改为独立预算（如保留 15s 专用于 stage-9）。验收：kill -TERM 后 MCP/FlightRecorder Stop 日志必然出现。
4. **PR-4 混沌注入闭环**：`RegisterAgent` 路径同样包裹 `chaosWrappedAgent`（把包裹逻辑移到 Register 与 Start 共同路径）；补 live chaos 注入后 agent 行为变化的集成测试。
5. **PR-5 YAML memory.enabled:false 生效**：`ToOptions` 在 false 时追加 `WithoutMemory()`；同 PR 修 knowledge chunk_size/chunk_overlap/top_k 三字段的消费（透传给 chunker/topK 检索）。

### Phase 2：伪接线闭环——接线项（第 2-3 周）

按收益排序：

- **W1 可观测性接线**（对应 P1-4）：
  1. `api/service/llm.Config` 增加 Tracer 字段并在 toInternal 透传；sdk.New 默认装 LogTracer（或 Noop 由用户显式选）；
  2. cmd/ares serve 构造 `NewPrometheusMetrics` 并注册到 /metrics（ares_bootstrap 加 provide_observability step），dream_cycle 等调用点自然生效；
  3. CostDashboard 三个端点挂到 introspect 路由或独立 mux（可选）。
  验收：`curl /metrics` 出现 `ares_llm_calls_total` 等指标。
- **W2 事件对齐 + 面板数据闭环**（对应 P1-8/9/10/11）：
  1. 在 ares_events/types.go 增补缺失事件常量；introspect FeedIntel 与 flight isDecisionEvent 改用常量而非裸字符串（单一事实来源）；
  2. 在 agentloop/sub executor 的任务完成路径 emit `EventSubTaskResult`（救活 outcome_recorder）；
  3. 修 collector 的事件映射：completed → `EventToolResult/EventLLMResult`，恢复时长统计；
  4. bootstrap 传 GenealogyCollector；
  5. serve_agents.go 补 introspect 的 Tasks/Decisions/Collab 三个 Source。
- **W3 Evolution GA 参数接线**（对应 P1-13）：`wireGAEvolution` 读 cfg.Evolution 的 14 个参数构造 SystemConfig，ticker 读 MinInterval；加一个"改 yaml → 参数生效"的等价性测试。
- **W4 ProfileRegistry 写侧**：sub executor / chat_cognition 构造时 ApplyToContext，打通角色指令注入；Read 侧已就绪。
- **W5 RBAC**：chaos kill-all/random-kill/recover 路由 require PermAdmin；API key 保持 read/write 分级文档。
- **W6 SDK Close 闭环**：Bootstrap 成功路径的 cleanups 存到 Runtime，Close 顺序执行（先 eg.Wait 再 cleanups 再 SDK 侧资源）；WithPostgres 场景连接池归零。
- **W7 Kernel.PollInterval / Server.Host 接线**：kernel_loop/serve_routine 读 cfg 注入；或从 config 删除并从 yaml 示例移除（二选一）。

### Phase 3：删除项清理（第 3-4 周，每删一个包跑全量构建+测试）

- 删 ares_runtime 插件生态（PluginBus、11 插件、4 Router、Collector、StateSnapshot、events.go 死事件常量）+ workflow/graph 的 4 个无人调用的 setter；或整体挪 `internal/experimental/`。**注意与 workflow-engine-wiring-plan-zh.md 的选项 A 协调：该计划若决定复用 graph，则只删 PluginBus 相关。**
- 删 ares_shutdown 四个未接线组件、ares_ratelimit 三个未接线实现 + 死常量 + 死配置字段、agents Handoff、core/models 死行为方法、ares_protocol/ahp（或迁移）、storage/postgres/query、logger.ModuleWith、sdk 的 MustNew/quickstart 链/NewContextCleaner/KnowledgeStore/Stream（或实现真流式）。
- 删 dead config 字段（Phase 0 登记为"删除"的），同步更新 configs/*.yaml 示例与文档。
- ares_bootstrap：删 SetupMCP/NewCallbackRegistry/NewLLMClientWithCallbacks/WireTaskExecutorCallbacks/ComponentStatus/IsSystemReady 或接线。

### Phase 4：bug 修复批（与 Phase 2/3 并行）

- provide_llm.go:36：按 provider 分发构造器（ollama/anthropic/openrouter 各自适配或显式只支持 openai-compatible 并文档化）。
- cmd/ares：handleAction 错误映射（500/400/404 分流，不回传原始 err.Error）；POST body 加 http.MaxBytesReader；audit 恒 ok=true 修复；EOF 用 errors.Is；mcp_null 挂死修复；db_migrate 接 ensureDatabase；main.go 使用说明订正；doctor 不打印 key 前缀。
- sub/tools.go GetTool 持锁拷贝；sub/executor Set* 加锁或文档化构造期约定。
- agentipc：Request 校验 timeout<=0（取默认值）；Subscribe 去重。
- kernelscheduler 主 ticker 用 preemptInterval 同款防护；Snapshot 加锁。
- ares_flight：graph root 语义修复（首个无 ParentID 节点固定为 root）；collector Start/Stop 加锁；诊断 payload 键对齐 emitter。
- ares_archive：extractToolArgs 支持 args 为 JSON 字符串（对齐 agentloop emitter），或 emitter 侧统一改传 map（建议后者，一处改）；Flush 实装或删。
- sdk：Task.Timeout 文档对齐；Submit 完成后 fabric.Delete；WithLLMConfig 合并而非替换；bootstrap_runtime 保真传参；graph MaxIterations 锁统一；空 agentName 在 AddNode 拒绝。
- introspect：POST /api/evolution/feedback 移出只读面或改挂 actionHandler（带鉴权）。
- aresrecovery：反馈环 panic 记日志；RestartAgent spawn 失败不扣预算。
- system_runtime：Shutdown 超时后放弃等待的 goroutine 加泄漏说明或可取消机制。
- ares_events：Append 派生 goroutine 合并（低优先）；共享 *Event 传订阅者前文档化只读约定或深拷贝。

### Phase 5：防回归闭环（第 4 周起，CI 化）

1. **可达性门禁**：CI 增加 `go list -deps ./cmd/ares/... ./sdk/...` 与 `go list ./internal/...` 差集检查——新增"从生产入口不可达"的包必须出现在白名单（白名单=Phase 0 决策为"仅 examples/实验"的包）。工具可用自写脚本（本次审计的 comm 命令即可），或 deadcode 工具修复后替代。
2. **config 契约测试**：为 ares_config 每个字段写消费测试——反射遍历 cfg 字段，grep 不到消费点的字段在 CI 报错（或维护一份 generated 消费矩阵）。
3. **事件契约测试**：ares_events 常量表 vs 全库 emitter/subscriber 字面量的对齐测试（订阅的事件必须有 emitter，防止再出现 EventSubTaskResult 式静默失效）。
4. **`go vet -race` + 长跑**：nightly 12h serve 内存曲线与 goroutine 数基线告警。
5. 文档同步：api/ARCHITECTURE.md 已声明"每个导出类型标注实际消费者，可 grep 验证"——把本文档的结论回写进去，并要求新包 PR 附消费者清单。

### 里程碑与验收

| 里程碑 | 内容 | 验收标准 |
|---|---|---|
| M1（第 1 周末） | Phase 1 五个 PR 合入 | -race 全绿；12h 内存平稳；kill -TERM 停机日志完整；chaos 注入可观测生效 |
| M2（第 3 周末） | Phase 2 接线项 W1-W7 完成 | /metrics 有 ARES_*；/api/insights 非空；面板任务板/决策页有数据；GA 参数改 yaml 生效；SDK Close 无连接泄漏 |
| M3（第 4 周末） | Phase 3 删除 + Phase 4 bug 批完成 | `go build ./... && make check` 全绿；白名单外零不可达包；dead config 字段数 = 0（或全部登记为"仅展示"） |
| M4（第 5 周） | Phase 5 CI 门禁上线 + 补审 5 个未审计模块组 | 本文档 5 个"未审计"组全部补齐并纳入同一决策矩阵 |

### 与既有计划的关系

- `workflow-engine-wiring-plan-zh.md`（选项 A：workflow/engine 编译为 taskfabric.Task）是 Phase 3 中"ares_runtime 插件生态删除决策"的前置输入：若 graph 被复用为拓扑载体，保留 graph 包本体，只删 PluginBus/setter 桥。
- 本文档 Phase 0 的决策矩阵完成后，应回填该计划的 Step 1 范围。
