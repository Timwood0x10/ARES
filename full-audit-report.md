# ARES 全量源码摸底大排查报告

> 审计日期：2026-08-28
> 方法：逐模块源码精读 + 代码图（in_degree / trace_path）交叉验证 + 生产路径调用链追踪
> 覆盖：internal/ 全部 8 组模块（对照 pseudo-wiring-audit-plan-zh.md 的 5 组未完成模块全部补齐）
> 判定标准：**符号在生产代码（非 _test.go）有调用者 = 已接线 ✅；仅测试调用 = 死代码 ❌；有实现但无生产触发点 = 空转 ⚠️**

---

## 0. 总体结论

| 类别 | 数量 |
|------|------|
| 完全未接入内核的模块（cmd/ares 不可达） | 13 个 |
| 已确认死代码符号（生产零调用） | **80+ 个** |
| 已确认空转子系统（有实现无触发） | 25+ 个 |
| 已确认 P1/P2 级 bug | 30+ 项 |
| 接线良好的核心模块 | ~20 个 |

**最重要的 5 个新确认发现**（本报告源码级验证）：

1. **`ares_observability` 三套监控全部空转**：`NewOTelTracer`/`NewPrometheusMetrics`/`NewCostDashboard` 生产零调用，生产唯一 Tracer 是 `NoopTracer`（workflow/graph 默认）→ `/metrics` 只有 go_* 指标，成本仪表盘端点从未挂载。
2. **混沌注入对生产 agent 静默无效**（manager_chaos.go）：`chaosWrappedAgent` 只在 `StartAgent`/`RestartAgent` 包裹，生产 `RegisterAgent`（serve_agents.go:62）存**裸 agent**，`Manager.Start()` 直接从 `m.agents` launch → `chaosFault`/`chaosSlowDelay` 无读取者，注入器（SlowAgent/ToolTimeout/PartitionNetwork/CorruptMemory/DisconnectMCP/InjectLLMFailure）全部写空。
3. **`ares_runtime` 插件生态全无装配**：`NewPluginBus` + 9 个插件构造函数（Observer/Loop/Interrupt/Tool/Checkpoint/Evolution/Recovery/Arena）+ 4 个 Router（Expression/Evolution/Fallback/Memory）+ ExecutionCollector + StateSnapshot 全部生产零调用。
4. **`workflow/engine` 无执行器**：`NewAgentExecutor` in_degree=0，HITL/中断存储/输出存储/重新加载/AgentRegistry 全部生产零调用 → 工作流引擎是"有 DAG 无执行器"。
5. **`ares_memory.NewProductionMemoryManager` 生产零调用**：bootstrap 用 `NewMinimalMemoryManager`，PG 集成的生产记忆管理器从未被使用。

---

## 1. 完全未接入内核的模块（cmd/ares 生产路径不可达）

| 模块 | 说明 |
|------|------|
| `internal/agentloop` | LLM 对话引擎，仅 SDK 使用，内核用 agentfabric.ChatCognition 取代 |
| `internal/detector` | 环境检测，仅 SDK quickstart 部分使用 LLM 检测 |
| `internal/llmservice` | 第三套 LLM 客户端，字段丢失，建议删除 |
| `internal/knowledge/linker` | 4 个连接器从未构建 |
| `internal/knowledge/provider/postgres` | PG 知识提供者从未注册 |
| `internal/knowledge/retriever` | 检索器从未构建 |
| `internal/knowledge/service` | ServiceAdapter 恒返回 nil |
| `internal/knowledge/store/sqlite` | SQLite 知识存储从未构建 |
| `internal/knowledge/workflow` | 知识工作流从未构建 |
| `internal/storage/memory` | 内存向量存储从未构建 |
| `internal/storage/postgres/query` | 查询缓存完全孤儿（含测试 0 引用） |
| `internal/tools/toolsource` | 工具选择器从未构建 |

---

## 2. 组1：内核与运行时

### ares_runtime ✅ 已确认

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| Manager.New | 100+ | ProvideRuntime | ✅ |
| Manager.RegisterAgent | 20+ | serve_agents.go | ✅ |
| Manager.Start/Stop | 10+ | runServe | ✅ |
| **NewPluginBus** | **0** | **无** | ❌ 死代码 |
| **NewObserverPlugin/LoopPlugin/InterruptPlugin/ToolPlugin/CheckpointPlugin/EvolutionPlugin/RecoveryPlugin/ArenaPlugin** | **0** | **无** | ❌ 死代码 |
| **NewExpressionRouter/EvolutionRouter/FallbackRouter/MemoryRouter** | **0** | **无** | ❌ 死代码 |
| **NewExecutionCollector** | **0** | **无** | ❌ 死代码 |
| **SaveStateSnapshot/LoadStateSnapshot** | **0** | **无** | ❌ 死代码 |
| MemoryRouter.BeforeStep | — | 仅测试 | ❌ 死代码（且预取 goroutine ctx 被 cancel，逻辑有 bug） |

**确认的 bug**：
- **P1 混沌注入对生产 agent 无效**（manager_chaos.go:221-306 + manager.go:231 + manager_lifecycle.go:54-61）：`RegisterAgent` 存裸 agent，只有 `StartAgent` 存 `chaosWrappedAgent`，`Start()` launch 的是裸 agent。
- manager_lifecycle.go:37 `m.g/m.gctx` 持锁重赋值，与 pre-start goroutine 无锁读竞态。
- bus.go:257-268 订阅清理只在调用方 ctx 取消时触发，PluginBus.Stop 不清理。

### kernelscheduler ✅ 已确认

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewKernelScheduler | 5+ | peer_mode.go:130 | ✅ |
| WithAgentFabric | 5+ | peer_mode.go:213 | ✅ |
| PreemptLowerPriority | — | 仅测试路径 | ❌ 生产空转（ExecutorCount==0 守卫） |
| executeUnbound 静态 executor | — | — | ⚠️ 生产模式被 fabric 分支跳过 |

### taskfabric ✅ 已确认

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewFabric | 108 | peer_mode, kernel_loop | ✅ |
| Create | 62 | 内核调度 | ✅ |
| Acquire | 39 | 内核调度 | ✅ |
| **NewLease** | **0** | **无** | ❌ 死代码（且用墙钟，与 f.now() 不一致） |
| WithConfidenceSource | 0 | 无 | ❌ 死代码 |
| **Fabric.Create 存调用方指针** | — | — | **P2 bug：别名数据竞争** |

### agentfabric ✅ 已确认

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewFabric | 101 | peer_mode.go:163 | ✅ |
| Spawn | 75 | peer_mode.go:188 | ✅ |
| NewChatCognition | 6 | peer_mode.go:184 | ✅ |
| **NewSubAgentCognition** | **0** | **无** | ❌ 死代码 |
| SetIdle | 0 | 无 | ❌ 死代码 |

### agentipc ✅ 已确认（全部接线良好）

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewBus | 30 | evolution_ipc, peer_registry | ✅ |
| Register | 23 | 多处 | ✅ |
| NewDualTrackDispatcher | 6 | peer_mode.go:118 | ✅ |
| Unregister | 0 | 无 | ❌ 死代码（小） |

**确认的 bug**（primitives.go）：Request 不校验 timeout<=0；超时后 handler goroutine 继续跑；Subscribe 不去重。

### agentloop ❌ 完全空转

| 符号 | 生产调用者 | 判定 |
|------|-----------|------|
| Engine.Run | 仅 SDK（sdk/agent.go） | ⚠️ 内核不可达 |
| FriendlyErr | SDK 使用 | ⚠️ 内核不可达 |
| **Run 无 nil 校验** | — | **P1 bug：req/e.LLM/e.Tools 为 nil 时 panic** |
| **parseArgs 静默吞 JSON 错误** | — | P2 bug |
| **req.Timeout 不约束单次 Generate** | — | P2 bug |

---

## 3. 组2：可观测性与事件

### ares_observability ⚠️ 三套监控全部空转（本报告关键发现）

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| **NewOTelTracer** | **20** | **无（全测试）** | ❌ 死代码 |
| **NewPrometheusMetrics** | **3** | **无（全测试）** | ❌ 死代码 |
| **NewCostDashboard** | **18** | **无（全测试）** | ❌ 死代码 |
| **NewCostTracker** | **12** | **无（全测试）** | ❌ 死代码 |
| NewNoopTracer | 11 | workflow/graph.NewGraph 默认 | ✅ 唯一实际使用的 Tracer |
| RecordEvolutionShadow | 0 | 无 | ❌ 死代码 |
| RecordEvolutionDeploy | 0 | 无 | ❌ 死代码 |
| SetEvolutionScore | 0 | 无 | ❌ 死代码 |

**结论：OTel/Prometheus/成本仪表盘三套全部未接线，生产 `/metrics` 无 ARES_* 指标。**

### ares_events ✅ 已确认

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewMemoryEventStore | 179 | 广泛 | ✅ |
| NewCompactableEventStore | 7 | ares_archive | ✅ |
| NewCompactor | 32 | 内部 | ✅ |
| **NewPostgresEventStore** | **2** | **仅测试** | ❌ 死代码（约 750 行 PG 代码生产未用） |
| **NewPgSummaryRepository** | **18** | **仅测试** | ❌ 死代码 |
| **NewPgTrimStore** | **4** | **仅测试** | ❌ 死代码 |
| **CompactAll/ForceCompact/CleanupSummaries** | **1** | **仅测试** | ❌ SummaryTTL 生产从不执行 |

**确认的 bug**：
- **P1 事件存储无界**：ares_archive/store.go 构造 `NewCompactableEventStore` 时 trimStore=nil、EnableTrimming=false → 压实只生成摘要从不删除原始事件。
- **EventSubTaskResult 有订阅者无发布者**：ares_skills/outcome_recorder.go:83 订阅，全库无 emitter → 技能结果记录静默失效。

### ares_archive ✅ 已确认

| 符号 | 判定 |
|------|------|
| NewCompactableStoreWithArchive | ✅（serve.go:136 使用） |
| summarizeFileChange output 参数未用 | P3 bug |
| **提取器与生产事件形状不匹配**（extractFileChanges 期待 map args，实际传 JSON 字符串） | P2 bug：RoundRecord.Files 恒空 |

### ares_flight ⚠️ 部分空转

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewFlightRecorder | 6 | Bootstrap | ✅ |
| NewCollector | 8 | flight.NewFlightRecorder 内部 | ✅ |
| **NewGenealogyCollector** | **6** | **无（全测试）** | ❌ 死代码 → genealogy 恒空 |
| **AutoDiagnose** | — | **无** | ❌ 死代码 |
| **handleMemoryDistilled 类型断言 float64 恒失败** | — | — | **P2 bug：计数恒为 0** |
| **Timeline 时长配对失效**（pairStartOf 期待 tool.result 但映射为 EventToolCall） | — | — | **P2 bug：Tool/LLM 时长恒 0** |
| **全聚合结构无 ring cap** | — | — | **P1 内存无界** |

### introspect ⚠️ 部分空转

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewEngine | 11 | setupServeControlPlane | ✅ |
| NewControlServer | 9 | setupServeControlPlane | ✅ |
| NewCollector/NewSink/NewHandler | 5+ | serve_agents.go | ✅ |
| **NewDashboard** | **1** | **仅 examples/30** | ❌ 运行时面板仅示例使用 |
| **Engine.insights 从未写入** | — | — | **P2：/api/insights 恒空** |
| **Collab/Tasks/Decisions 三 Source 恒 nil**（serve_agents.go:154-157 未接） | — | — | **P2：面板协作图/任务板/决策页恒空** |
| **FeedIntel 4 个事件字面量在 ares_events 中不存在** | — | — | **P2：异常检测只覆盖 2 个事件** |
| spawnAgent bus.Register 错误被丢弃 | — | — | P3 bug |

---

## 4. 组3：Bootstrap/Config/SDK/API

### ares_bootstrap ✅ 接线良好（子系统空转见各模块）

| 符号 | 判定 |
|------|------|
| Bootstrap / ProvideRuntime / ProvideMemory / ProvideMCP / ProvideLLM / ProvideEvolution / ProvideNewEvolution / ProvideDiscovery / ProvideObservability | ✅ 全部接线 |
| Components.ComponentStatus / IsSystemReady | ⚠️ 仅测试使用 |
| setupEvaluators → EvaluatorRegistry | ⚠️ 创建但无人消费 |
| setupFeedbackService → FeedbackService | ⚠️ 创建但无人设置 |
| NewCallbackRegistry / NewLLMClientWithCallbacks / WireTaskExecutorCallbacks | ⚠️ 仅测试 |

### ares_config ⚠️ 40+ 死配置字段（见 pseudo-wiring-audit-plan-zh.md §3.11 完整表）

本报告重点新确认：
- `Memory.EnableDistillation` / `DistillationThreshold`：ares.yaml 设置了，生产从不读取（蒸馏实际由 Storage+Embedding 门控）
- `Evolution` 14 个 GA 参数全死（wireGAEvolution 用 DefaultSystemConfig + 硬编码）
- `Kernel.PollInterval` 从未注入调度器
- `Kernel.Policy` 运行时硬编码 PolicyTaskFabric
- `Tools.Defaults/Agents`、`Memory.SessionMemory/UserProfile/TaskDistillation` 全死
- `SetAllowedConfigDir` 路径防护全库仅测试调用 → **路径穿越防护空转**

---

## 5. 组4：安全/恢复/命令层

### ares_security ⚠️ 中间件形态空转

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewSanitizer | 12 | bootstrap/ProvideLLM | ✅ |
| Sanitize | 18 | llm/client | ✅ |
| NewAuditLogger | 4 | actions.go | ✅ |
| NewAuthMiddleware | 9 | serve_routine.go:186 | ✅（但只用 Wrap，PermAdmin 未接） |
| **Wrap/WrapGin/PrincipalFromGin/FromContext/HasPermission** | — | 仅测试 | ❌ 死代码 |
| **PermAdmin 从未被路由 require** | — | — | **P2 安全：chaos kill-all 对 operator 放行** |

**确认的 bug**：maskString 非 ASCII 按字节切片（P3）；sanitizeValue json.Number 死分支（P3）。

### aresrecovery ✅ 核心接线良好，边缘空转

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| Recovery.New | 159 | peer_mode.go:207 | ✅ |
| NewGlobalTracer | 13 | Bootstrap | ✅ |
| NewEvolutionTracer | 7 | Bootstrap | ✅ |
| NewFeedbackStore | 7 | Bootstrap | ✅ |
| NewEvolutionAwareIPC | 6 | wireEvolutionIPC | ✅ |
| RequeueExpiredLeases | 7 | kernel_loop | ✅ |
| RestartAgent | 5 | kernel_loop | ✅ |
| **TraceTask** | 8 | **仅测试** | ❌ 生产 task/agent span 恒空 |
| **TraceAgent** | 2 | **仅测试** | ❌ 同上 |
| **WithCognitionFactory** | — | **无** | ❌ 认知工厂从未安装 |
| **Sandbox.Simulate** | 2 | **仅测试** | ❌ |
| **Recovery.RecoverTaskCheckpoint / RecoverFromAgentDeath** | 5/3 | **仅测试/混沌** | ❌ |

### ares_shutdown ⚠️ 部分空转

| 符号 | 生产调用者 | 判定 |
|------|-----------|------|
| NewManager / RegisterPhase / AddCallback / StartShutdown | serve.go:77-80 | ✅ |
| **NewSignalHandler** | **无** | ❌ 死代码（serve 内联信号处理） |
| **NewCallbackRegistry** | **无** | ❌ 死代码 |
| **NewPhaseExecutor** | **无** | ❌ 死代码 |
| **CallbackChain** | **无** | ❌ 死代码 |

**确认的 bug**：
- **P1 StartShutdown 防重入失效**：`m.currentPhase != 0` 而 PhasePreShutdown==0（iota 首值）→ 第二阶段期间二次调用可重跑回调。
- **P2 优雅停机可被跳过**：serve.go:104 shutdownCtx 30s 与 phase 回调共享预算，耗尽后 shutdownSystemRuntime 拿过期 ctx 被跳过。
- signal.go:134 SetContext 无锁写。

### cmd/ares ✅ 装配完整

- **peerRegistry 双写零读**（kernel.go:47 + serve_agents.go:237 + serve.go:285）：构建两次但无消费方。
- **kernelHandle.flipped/flag 只写不读**。
- handleAction 一切错误映射 404 并泄漏 err.Error()。
- /introspect、/metrics 无鉴权（已文档化边界）。
- 所有 POST 无 body 上限。
- db_migrate 端口文档 5432 代码 5433。

---

## 6. 组5：记忆/知识/存储

### ares_memory ⚠️ 核心良好，边缘空转（本报告关键发现）

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewMemoryManager | 33 | ProvideMemory | ✅ |
| NewDistiller | 26 | buildAKGDistiller | ✅ |
| NewMemoryRetriever | 9 | wireRetrievers | ✅ |
| NewPipeline | 6 | 内部 | ✅ |
| NewMemoryPatchExecutor | 7 | ProvideNewEvolution | ✅ |
| NewContextCleaner | 28 | bootstrap | ✅ |
| **NewProductionMemoryManager** | **2** | **无（仅测试）** | ❌ 死代码 —— bootstrap 用 NewMinimalMemoryManager |
| **NewPushService** | **0** | **无** | ❌ 死代码 |
| **NewReportGenerator** | **0** | **无** | ❌ 死代码 |
| **experienceadapters.SearchByVector / GetByMemoryType** | **0** | **无** | ❌ 死代码 |
| NewDistillationRepo / NewKnowledgeRetrieverAdapter | 3/2 | 仅测试 | ❌ |

### knowledge ⚠️ 核心保留，边缘删除

| 子包 | 判定 |
|------|------|
| pipeline/planner/provider{memory,code,vector,store}/runtime/mcp | ✅ 被 BuildKnowledgeRuntime 使用 |
| linker / provider/postgres / retriever / service / store/sqlite / workflow | ❌ cmd/ares 不可达，建议删除 |

### storage ⚠️

| 子包 | 判定 |
|------|------|
| postgres | ✅ bootstrap 使用 |
| postgres/query | ❌ 完全孤儿，建议删除 |
| memory | ❌ 生产孤儿，建议删除 |

### ares_experience ⚠️ FeedbackService 空转（已确认）

---

## 7. 组6：演化/评估

### ares_evolution（旧版）✅ 核心接线良好

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewWiredEvolutionSystem | 22 | wireGAEvolution (bootstrap) | ✅ |
| NewMutator / NewPopulation / NewCrossover / NewEvolutionScheduler / NewDreamCycle | 20-62 | 大量 | ✅ |
| NewLLMScorer | 1 | wireLLMScorer → wireGAEvolution | ✅ |
| NewGenomePopulationAdapter | 3 | NewWiredEvolutionSystem | ✅ |
| **NewEvidenceAggregatorProvider** | **0** | **无** | ❌ 死代码 |
| **NewMetaController / NewHypothesisGenerator / NewLLMReflector / NewPGStrategyStore / NewKnowledgeDistiller / NewNondominatedSortingSelection / NewTruncationSelection** | 1-6 | **仅测试** | ❌ 高级组件未接 |
| HintsForTask / RecordStrategyOutcome | 0 | 无 | ❌ |

### evolution（新）⚠️ 候选管线空转（已确认）

CandidateVerifier / CandidatePipeline / Diagnoser / GAGenerator / ProfileStore / ProfileExecutor 全部生产零构建。

### ares_eval ⚠️ EvaluatorRegistry 空转（已确认）

### ares_arena ⚠️ FlightBridge 空转（本报告关键发现）

| 符号 | 生产调用者 | 判定 |
|------|-----------|------|
| NewService | arena.go serve | ✅ |
| **NewFlightBridge** | **无** | ❌ 死代码 → timeline/diagnostics 端点恒 503 |
| Handler.SetFlightRecorder | 无 | ❌ |
| ScenarioConfig.ParallelActions/MaxConcurrent/DependsOn | 解析不执行 | ⚠️ |

---

## 8. 组7：工具/MCP/技能

### ares_mcp ✅ 全部接线良好（本报告确认）

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewMCPManager | 11 | ProvideMCP | ✅ |
| NewMCPServer / NewMCPClient / NewMCPTool / NewStdioTransport / NewSSETransport / NewTransportFromConfig / NewMCPConfigWatcher / NewMCPToolFactory | 2-22 | 全部被使用 | ✅ |

**ares_mcp 无伪接线。**

### ares_skills ⚠️ 反馈/工具/置信度全空转（本报告关键发现）

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewCatalog | — | wireSkills (bootstrap) | ✅ |
| **NewSkillOutcomeRecorder** | **0** | **无** | ❌ 死代码 |
| **CatalogTools（5 个技能工具）** | **0** | **无** | ❌ 死代码 |
| **NewExperienceConfidenceSource** | **0** | **无** | ❌ 死代码 |
| **FetchHTTPManifest 远程技能 Path 未设置** | — | — | **P2 bug：Load 从错误路径读** |

### tools ⚠️（已确认）

| 子包 | 判定 |
|------|------|
| planner | executionPlanner 硬编码 cost=3/latency=100ms；capabilityPlanner ExpressionEvaluation 死配置 |
| toolsource | CapabilitySelector/TagSelector 生产零调用 |
| resources/core | GlobalRegistry 死代码 |
| resources/builtin | text_processor 错误标记 domain:"math" |
| discovery | CommandTool.Execute 内存检查在 Output() 之后 |

---

## 9. 组8：Workflow/LLM/基础设施

### workflow/engine ⚠️ 无执行器（本报告关键发现）

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewMutableDAG | 64 | ProvideNewEvolution / BuildLiveAgentDAG | ✅ |
| NewRecoveryPatchExecutor | 14 | ProvideNewEvolution | ✅ |
| **NewAgentExecutor** | **0** | **无** | ❌ 死代码 —— 引擎无执行器 |
| **NewHITLFeedbackPlugin / NewMemoryInterruptStore / NewOutputStore / NewWorkflowReloader / NewAgentRegistry** | **0** | **无** | ❌ 死代码 |

### workflow/graph ⚠️ 基础接线，插件 setter 全空转（本报告关键发现）

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewGraph / Node / Edge / NewFuncNode / Start | 1-9 | ProvideNewEvolution | ✅ |
| **SetPluginBus / SetRouter / SetTracer / SetExecutionCollector / SetLimiter / SetCheckpointStore / SetScheduler / NewGraphWithTracer / NewAgentNode / NewToolNode / Clear / RemoveEdge / RemoveNode** | **0** | **无** | ❌ 全部死代码 |

### internal/llm ✅ 接线良好（本报告确认）

| 符号 | 生产调用者 | 判定 |
|------|-----------|------|
| NewClient / NewFailoverClient / NewCircuitBreaker / WithCallbacks / WithSanitizer | bootstrap/gate3/llmservice/sdk/compat | ✅ |
| **NewClientFromEnv / NewFailoverScorer / WithRateLimiter / WithRetryPolicy / IsOpen / IsHalfOpen** | **无** | ❌ 死代码 |
| Config.Extra 字段 | 无人读取 | ❌ 死配置 |

### internal/llm/output ✅ 工厂接线良好

| 符号 | 判定 |
|------|------|
| NewFactory / CreateAdapter / NewParser / NewValidator / NewTemplate | ✅ serve_routine/dashboard/sub/agentfabric |
| NewOpenAIAdapter/Ollama/OpenRouter | ✅ 工厂模式注册 |
| **timeout.go 全部导出函数（WithLLMTimeout 等 5 个）** | ❌ 死代码 |
| **NewTemplateEngine / NewTemplateRegistry / ParseOutput / NewSchema / NewSchemaGenerator / NewTimeout / RenderTemplate** | ❌ 死代码 |

### ares_protocol/ahp ⚠️ 核心用，子系统死

| 符号 | 判定 |
|------|------|
| AHPMessage / SendMessage / queue | ✅ agents/cmd 使用 |
| **NewDLQ / NewDLQProcessor / NewHeartbeatMonitor / NewMessageQueue / NewMessageRouter / NewProtocol / NewDynamicRouter / NewRateLimiter / NewCodecRegistry / NewJSONCodec** | ❌ 全部死代码 |

### detector / llmservice ❌ 不可达（已确认）

### errors / ares_ctxutil / kernelctx / agentsyscall / logger ✅ 接线良好

---

## 10. 关键 bug 汇总（源码级确认）

| 严重度 | 位置 | 问题 |
|--------|------|------|
| **P1** | ares_runtime/manager_chaos.go | 混沌注入对 RegisterAgent 路径无效 |
| **P1** | ares_observability | OTel/Prometheus/CostDashboard 三套未接线 |
| **P1** | ares_flight + evidence | 聚合结构 + MemoryStore 无界增长 |
| **P1** | ares_events/archive | EnableTrimming=false + SummaryTTL 不执行 → 事件存储无界 |
| **P1** | ares_shutdown/manager.go:118 | StartShutdown 防重入 phase==0 失效 |
| **P1** | serve.go:104-110 | shutdown 预算耗尽跳过 SystemRuntime 真实 Stop |
| **P1** | agentloop/engine.go | Run 无 nil 校验直接 panic |
| **P2** | ares_flight/collector.go | handleMemoryDistilled float64 断言恒失败 |
| **P2** | ares_flight/timeline.go | 事件映射错位，Tool/LLM 时长恒 0 |
| **P2** | ares_skills/http_source.go | 远程技能 Path 未设置 |
| **P2** | ares_archive/extract.go | 提取器与生产事件形状不匹配，Files 恒空 |
| **P2** | introspect | insights 恒空 + 三 Source 未接 + FeedIntel 死事件字面量 |
| **P2** | taskfabric/fabric.go | Create 存调用方指针别名竞态 |
| **P2** | ares_security | PermAdmin 从未 require，chaos 破坏性操作放行 |
| **P2** | ares_config | SetAllowedConfigDir 路径防护空转 |
| **P2** | tools/discovery | Output() 先读全内存再检查上限 |
| **P2** | kernelscheduler | PreemptLowerPriority 生产恒空转 |
| **P2** | cmd/ares | peerRegistry 双写零读；Kernel.Policy 运行时无效；handleAction 404 化 |

---

## 11. 修复优先级建议（对接 development-plan.md）

1. **P0 决策登记**：80+ 死代码符号、25+ 空转子系统、13 个不可达模块，逐一"接线 or 删除"（已有决策表，见 supplementary-audit.md 和 development-plan.md）。
2. **P1 修复**：混沌注入闭环、可观测性接线（OTel/Prometheus 是最低成本高收益项）、内存无界、shutdown 防重入、事件存储 trim。
3. **P2 修复**：按本报告 §10 清单逐项修复。
4. **闭环保留项**：workflow/engine 无执行器需专门设计（见 workflow-engine-wiring-plan-zh.md）；ares_memory 生产管理器切换为 NewProductionMemoryManager。

---

## 12. 附录：本报告与已有审计的关系

| 文档 | 关系 |
|------|------|
| `pseudo-wiring-audit-plan-zh.md` | 基础审计（9 组中 4 组完成、5 组未完成），本报告补齐了 5 组 |
| `code-audit-report.md` | 第一轮审计（广度），本报告补充了 in_degree/trace_path 精确证据 |
| `supplementary-audit.md` | 第一轮补充（决策表雏形），本报告是其源码级验证版 |
| `development-plan.md` | 修复计划，本报告是其证据来源更新 |
