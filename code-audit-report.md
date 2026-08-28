# ARES 代码审计报告

> 审计日期: 2026-08-28
> 审计范围: `internal/` 全部模块，`cmd/ares/` 内核接线
> 审计目标: 伪接线模块(空转)、空转实现方法、潜在 bug

---

## 目录

1. [伪接线模块 (Pseudo-Wiring) 总览](#1-伪接线模块-pseudo-wiring-总览)
2. [未导入内核的模块 (Unreachable from Kernel)](#2-未导入内核的模块-unreachable-from-kernel)
3. [各模块详细审计](#3-各模块详细审计)
4. [未使用的配置字段](#4-未使用的配置字段)
5. [已知 Bug 汇总](#5-已知-bug-汇总)

---

## 1. 伪接线模块 (Pseudo-Wiring) 总览

### 1.1 完全未接入内核的模块 (从 cmd/ares 不可达)

以下模块的源文件在 `internal/` 下存在，但 `cmd/ares` (生产内核二进制) 的生产代码路径(非测试)从未导入它们。它们处于完全空转状态。

| 模块 | 路径 | 说明 |
|------|------|------|
| **agentloop** | `internal/agentloop/` | 完整的 LLM 对话引擎，但内核从未使用 |
| **detector** | `internal/detector/` | 环境检测器，仅 SDK quickstart 部分使用其 LLM 检测结果 |
| **knowledge/linker** | `internal/knowledge/linker/` | 知识图谱连接器，从未被构建 |
| **knowledge/provider/postgres** | `internal/knowledge/provider/postgres/` | PG 知识提供者，从未被注册 |
| **knowledge/retriever** | `internal/knowledge/retriever/` | 知识检索器，从未被构建 |
| **knowledge/service** | `internal/knowledge/service/` | 知识服务 API 适配器，所有方法空转 |
| **knowledge/store/sqlite** | `internal/knowledge/store/sqlite/` | SQLite 知识存储，从未被构建 |
| **knowledge/workflow** | `internal/knowledge/workflow/` | 知识工作流，从未被构建 |
| **llmservice** | `internal/llmservice/` | LLM 服务包装器，部分字段空转 |
| **storage/memory** | `internal/storage/memory/` | 内存向量存储，从未被构建 |
| **storage/postgres/query** | `internal/storage/postgres/query/` | 查询缓存，从未被生产代码调用 |
| **tools/toolsource** | `internal/tools/toolsource/` | 工具选择器，从未被生产代码构建 |

### 1.2 已接线但子系统空转的模块

以下模块已通过 `ares_bootstrap` 或代码路径接线，但其某个子系统/功能处于空转状态。

| 模块 | 空转子系统 | 说明 |
|------|-----------|------|
| **ares_evolution** | CandidateVerifier, CandidatePipeline, Diagnoser, GAGenerator, ProfileStore, ProfileExecutor, GA Generator | 候选验证→发布管线全空转 |
| **ares_evolution/coordinator** | SelfHealing (NotifySelfHealingAttempt/Outcome) | 自愈状态机从未被触发 |
| **ares_evolution/genome** | CrossoverGenome 接口 | 已移除的接口，无人实现 |
| **ares_arena** | FlightBridge, FlightRecorder (timeline/diagnostics) | 记录器从未在生产路径挂载 |
| **ares_arena** | ScenarioConfig.ParallelActions / MaxConcurrent / DependsOn | 解析但不执行 |
| **ares_arena** | RegressionTester.arena 字段 | 存了但从未被读取 |
| **ares_flight** | GenealogyCollector | 家族谱系收集器从未被创建 |
| **ares_flight** | AutoDiagnose, SuggestFix (DiagConcurrencyError) | 死代码 |
| **ares_callbacks** | Registry (全部) | 回调注册表创建但无 handler 注册 |
| **ares_eval** | EvaluatorRegistry, RunEvaluation | 评估器注册表创建但无人读取 |
| **ares_experience** | FeedbackService | 反馈服务创建但无人调用 |
| **ares_skills** | SkillOutcomeRecorder, CatalogTools, ExperienceConfidenceSource | 技能反馈/工具/经验和置信度全空转 |
| **ares_skills** | Experience.BestMatch | 学习源从未被消费 |
| **ares_shutdown** | SignalHandler, CallbackRegistry, PhaseExecutor, CallbackChain | 全部仅测试使用 |
| **ares_observability** | OTELTracer, PrometheusMetrics, CostDashboard | 从未被生产代码构造 |
| **ares_runtime** | MemoryRouter, EvolutionRouter, FallbackRouter | 路由器实现但从未被构造 |
| **ares_runtime** | OutcomeRecorder, MemoryPlugin, EvolutionPlugin | 插件系统从未被接线 |
| **ares_recovery** | Recovery.WithCognitionFactory | 认知工厂从未被设置 |
| **ares_recovery** | Recovery.RecoverTaskCheckpoint, RecoverFromAgentDeath | 仅测试/混沌使用 |
| **ares_recovery** | GlobalTracer.TraceTask / TraceAgent | 追踪方法从未被调用 |
| **knowledge/mcp** | AKFService | 通过 serve.go 注册了工具，但 serve 代码路径中 knowledgeRuntime 可能为 nil |
| **knowledge/service** | ServiceAdapter (全部方法) | 知识服务适配器完整但从未被构建 |
| **kernelscheduler** | PreemptLowerPriority, executeUnbound (executor 部分) | 抢占功能和静态 executor 注册表在生产模式下空转 |
| **tools/planner** | executionPlanner.Plan, capabilityPlanner.Plan | 工具规划器占位实现 |
| **tools/toolsource** | CapabilitySelector, TagSelector | 从未被生产代码使用 |
| **tools/resources/builtin** | builtin 工具注册中的 text_processor 域标签错误 | 标签与实际能力矛盾 |
| **tools/resources/core** | GlobalRegistry 包装函数 | 已废弃的全局注册表 |

---

## 2. 未导入内核的模块 (Unreachable from Kernel)

通过分析 `cmd/ares` 生产代码的传递导入关系，以下目录的生产代码不可达：

```
internal/agentloop
internal/detector
internal/knowledge/linker
internal/knowledge/provider/postgres
internal/knowledge/retriever
internal/knowledge/service
internal/knowledge/store/sqlite
internal/knowledge/workflow
internal/llmservice
internal/storage/memory
internal/storage/postgres/query
internal/tools/toolsource
```

---

## 3. 各模块详细审计

### 3.1 internal/agentloop

状态: **完全空转，未接入内核**

| 函数/方法 | 问题类型 | 描述 | 代码 |
|-----------|---------|------|------|
| Engine.Run | bug | 对 req、e.LLM、e.Tools 无 nil 校验，传入 nil 指针时直接 panic | engine.go |
| Engine.Run | bug | req.Timeout 只检查两次 LLM 调用之间，不约束单次 Generate | engine.go |
| parseArgs | bug | JSON 解析失败被静默吞掉，返回 nil map 而非错误 | engine.go |
| FriendlyErr | bug | fmt.Errorf("%s", msg) 应使用 errors.New(msg) | engine.go |

### 3.2 internal/agentfabric

状态: **已接入内核** (通过 ares_bootstrap)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.3 internal/agentipc

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.4 internal/agentsyscall

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.5 internal/agents

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.6 internal/ares_archive

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| summarizeFileChange | bug | 参数 `output` 声明但从未使用 | extract.go |
| fileArchiveWriter.Flush | stub | 文档自认的 no-op: 只检查 ctx.Err() 就返回 nil | writer.go |

### 3.7 internal/ares_arena

状态: **部分空转** (通过 `cmd/ares/arena.go` 独立入口，但 FlightBridge 未接线)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| FlightBridge 全部方法 | 伪接线 | FlightBridge 完整实现但 never 在生产路径挂载 | integration.go |
| Handler.SetFlightRecorder | 伪接线 | 从未在生产路径调用，导致 timeline/diagnostics 端点恒返回 503 | http.go |
| Handler.RegisterRoutes (flight 端点) | 伪接线 | 注册了但恒 503 | http.go |
| ScenarioConfig.ParallelActions | 伪接线 | 解析并警告"not yet supported"，但永远不执行 | scenario.go |
| ScenarioConfig.MaxConcurrent | 伪接线 | 同上 | scenario.go |
| ScenarioConfig.DependsOn | 伪接线 | 同上 | scenario.go |
| MetricsCollector.RecordRecovery | stub | 已废弃，生产路径不调用 | metrics.go |
| MetricsCollector.RecordFailover | stub | 已废弃，生产路径不调用 | metrics.go |
| MetricsCollector.RecordConsistency | stub | 已废弃，生产路径不调用 | metrics.go |
| RunScenarioReport (CalculateScoreV1) | bug | 注释声称用 per-scenario 分数，实际使用的仍是 global Stats | scenario.go |
| RegressionTester.arena 字段 | 空转 | 存了但从未被读取 | regression.go |

### 3.8 internal/ares_bootstrap

状态: **接线总枢纽，自身无空转**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 子系统空转见各模块 | - |

### 3.9 internal/ares_callbacks

状态: **已接线但空转** (在 ProvideLLM 中创建并注入，但无人注册 handler)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| Registry.On | 空转 | 所有 handler 注册仅出现在测试中 | callbacks.go |
| Registry 全部方法 | 空转 | 整个回调系统空转 | callbacks.go |

### 3.10 internal/ares_config

状态: **已接入内核**，但部分配置字段无人消费

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| ToolsConfig.Defaults | 死配置 | 从未被生产代码读取 | config.go |
| ToolsConfig.Agents (AgentToolConfig) | 死配置 | 从未被生产代码读取 | config.go |
| Memory.SessionMemory | 死配置 | SessionConfig 定义但从未被 bootstrap 读取 | config.go |
| Memory.UserProfile | 死配置 | ProfileConfig 定义但从未被 bootstrap 读取 | config.go |
| Memory.TaskDistillation | 死配置 | DistillConfig 定义但从未被 bootstrap 读取 | config.go |
| Memory.EnableDistillation | 死配置 | `ares.yaml` 中 `memory.enable_distillation` 已设置但生产路径从不读取；实际蒸馏由 `cfg.Storage.Enabled + Type==postgres + Embedding.Enabled` 门控（bootstrap_steps.go:37） | config.go / ares.yaml |
| Memory.DistillationThreshold | 死配置 | 同上，从未被 bootstrap/生产路径消费 | config.go / ares.yaml |

### 3.11 internal/ares_ctxutil

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.12 internal/ares_eval

状态: **已接线但空转**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| setupEvaluators | 空转 | EvaluatorRegistry 在 ProvideEvolution 中创建但无人消费 | provide_evolution.go |
| RunEvaluation | 空转 | 完整评估流水线仅测试调用 | report.go |
| emitDimensionEvidence | 空转 | 维度桥仅在启用维度平均时调用，但 setupEvaluators 未启用 | dimension_judge.go |

### 3.13 internal/ares_events

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.14 internal/ares_evolution

状态: **已接线但子系统空转** (evolution.Enabled 为 false 时整个跳过，true 时部分子系统仍空转)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| CandidateVerifier 全部 | 伪接线 | 候选验证管线从未在生产路径构建 | candidate.go |
| CandidatePipeline 全部 | 伪接线 | 候选发布管线从未在生产路径构建 | candidate_pipeline.go |
| Diagnoser 全部 | 伪接线 | 失败证据诊断器从未在生产路径构建 | diagnoser.go |
| GAGenerator 全部 | 伪接线 | GA 候选生成器仅通过 Diagnoser 可达，而 Diagnoser 未接线 | ga_generator.go |
| ProfileStore 全部 | 伪接线 | 候选/稳定配置文件存储仅通过 CandidatePipeline 可达 | profile_store.go |
| ProfileExecutor 全部 | 伪接线 | 配置文件指令执行器仅通过 CandidatePipeline 构造 | profile_executor.go |
| Coordinator.NotifySelfHealingAttempt | 伪接线 | 自愈状态机从未被触发 | coordinator/coordinator.go |
| Coordinator.NotifySelfHealingOutcome | 伪接线 | 同上 | coordinator/coordinator.go |
| PolicyGenome.SelfHealingEnabled/MaxRetries | 死配置 | 门控无人读取 | coordinator/coordinator.go |
| CrossoverGenome 接口 | 死代码 | 文档自述已移除，接口仍保留 | genome/genome.go |

### 3.15 internal/ares_experience

状态: **已接线但空转**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| FeedbackService (全部) | 空转 | 在 ProvideEvolution 中创建但 wireGAEvolution 从未设置 gaCfg.FeedbackService | provide_evolution.go |

### 3.16 internal/ares_flight

状态: **已接入内核但部分空转**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| handleMemoryDistilled | bug | 类型断言 `.(float64)` 失败，发射器写入的是 Go int 类型，inputCount/outputCount 恒为 0 | collector.go |
| GenealogyCollector 全部 | 死代码 | 家族谱系收集器从未被创建 | genealogy_collector.go |
| AutoDiagnose | 死代码 | 自动诊断函数无人调用，handleTaskEnd 内联实现 | diagnostics.go |
| SuggestFix (DiagConcurrencyError) | 死代码 | ClassifyError 永不返回 DiagConcurrencyError | diagnostics.go |
| Graph.AddNode | bug | 父节点顺序异常时子节点永久成为孤儿 | graph.go |
| Timeline.Add (handleAgentEnd) | bug | TimelineEvent 未设置 ParentID，并发配对有风险 | timeline.go |

### 3.17 internal/ares_integration

状态: **仅测试文件**，无生产代码

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | 仅测试 | 目录下只有 12 个 _test.go 文件，无生产代码 | - |

### 3.18 internal/ares_mcp

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.19 internal/ares_memory

状态: **已接入内核** (通过 wireMemory + ProvideMemory)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.20 internal/ares_observability

状态: **已接入内核** (包被导入) 但核心功能空转

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| NewOTELTracer | 伪接线 | 仅在包内定义，无人构造 | otel_tracer.go |
| NewPrometheusMetrics | 伪接线 | 仅在包内定义，无人构造 | prometheus.go |
| NewCostDashboard | 伪接线 | 仅在包内定义，无人构造 | cost.go |
| NewCostTracker | 伪接线 | 同上 | cost.go |
| CostDashboard.RegisterCostRoutes | 伪接线 | HTTP 路由注册了但从未被挂载 | cost.go |
| NoopTracer 全部方法 | stub | 明确标记为 no-op，但这是有意设计 | noop.go |

### 3.21 internal/ares_protocol/ahp

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.22 internal/ares_ratelimit

状态: **已接入内核** (通过 llm/failover.go, serve_chaos.go, workflow/graph)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.23 internal/ares_runtime

状态: **已接入内核** (Manager 被使用) 但路由器/插件系统空转

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| NewMemoryRouter | 伪接线 | 内存路由器实现但从未被生产代码构造 | router_memory.go |
| NewEvolutionRouter | 伪接线 | 进化路由器实现但从未被生产代码构造 | router_evolution.go |
| NewFallbackRouter | 伪接线 | 回退路由器实现但从未被生产代码构造 | router_fallback.go |
| OutcomeRecorder 全部 | 伪接线 | 结果记录器从未被生产代码构造 | outcome_recorder.go |
| MemoryPlugin 接口 | 伪接线 | 内存插件接口定义但无实现被注册 | plugin.go |
| EvolutionPlugin 接口 | 伪接线 | 进化插件接口定义但无实现被注册 | plugin.go |

### 3.24 internal/ares_security

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| maskString | bug | 对非 ASCII UTF-8 字符串按字节切片，可能截断 rune 产生无效 UTF-8 | sanitizer.go |
| sanitizeValue (json.Number case) | bug | 死代码分支，json.Unmarshal 不会产生 json.Number | sanitizer.go |

### 3.25 internal/ares_shutdown

状态: **已接入内核** (Manager 被使用) 但部分子系统空转

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| Manager.StartShutdown | bug | PhasePreShutdown == 0 与零值启动哨兵混淆，并发第二次调用可进入死循环 | manager.go |
| SignalHandler 全部 | 空转 | 生产代码 serve.go 内联处理信号，不使用 SignalHandler | signal.go |
| CallbackRegistry 全部 | 空转 | 仅测试使用 | callbacks.go |
| PhaseExecutor 全部 | 空转 | 仅测试使用 | phase.go |
| CallbackChain 全部 | 空转 | 仅测试使用 | callbacks.go |
| SignalHandler.SetContext | bug | 写 h.ctx 未加锁，与 handleSignals 读 h.ctx 存在数据竞争 | signal.go |

### 3.26 internal/ares_skills

状态: **已接入内核但核心功能空转**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| SkillOutcomeRecorder.Start | 伪接线 | 技能结果记录器从未在生产路径启动 | outcome_recorder.go |
| CatalogTools 全部工具 | 伪接线 | 5 个技能工具从未被注册到生产工具注册表 | tools.go |
| ExperienceConfidenceSource | 伪接线 | 经验置信度源从未被接入调度器 | experience_confidence.go |
| Experience.BestMatch | 空转 | 学习源经验从未被消费 | experience.go |
| FetchHTTPManifest | bug | 远程技能 Path 未设置，Load() 时从错误路径读取 | http_source.go |

### 3.27 internal/aresrecovery

状态: **已接入内核** (Recovery, EvolutionTracer, FeedbackStore, GlobalTracer 被构建)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| Recovery.WithCognitionFactory | 伪接线 | 认知工厂从未被设置，始终为 nil | recovery.go |
| Recovery.RecoverTaskCheckpoint | 仅测试/混沌 | 文档自述生产恢复循环不使用此方法 | recovery.go |
| Recovery.RecoverFromAgentDeath | 仅测试/混沌 | 同上 | recovery.go |
| GlobalTracer.TraceTask | 空转 | 从未被非测试代码调用 | global_tracer.go |
| GlobalTracer.TraceAgent | 空转 | 同上 | global_tracer.go |

### 3.28 internal/core

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.29 internal/detector

状态: **完全空转，未接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| Environment.EmbeddingModel | 死字段 | 从未被任何 Detect 路径赋值 | environment.go |
| Detect 中的 PostgreSQLURL/MCPEndpoints | 伪接线 | SDK 注释明确"intentionally ignored" | environment.go |
| detectPostgreSQL | stub | 只读环境变量不验证连通性，与 doc 声称的"probes"不符 | environment.go |
| detectMCP | stub | 只解析环境变量不做 URL 校验 | environment.go |

### 3.30 internal/errors

状态: **已接入内核** (被广泛使用)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.31 internal/eval

状态: **已接入内核** (被 ares_bootstrap 使用)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.32 internal/evidence

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.33 internal/evolution

状态: **已接入内核** (通过 ares_bootstrap/provide_new_evolution.go) 但部分子系统空转

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| CandidateVerifier 全部 | 伪接线 | 同上 - 候选验证管线空转 | candidate.go |
| CandidatePipeline 全部 | 伪接线 | 候选发布管线空转 | candidate_pipeline.go |
| Diagnoser 全部 | 伪接线 | 失败证据诊断器空转 | diagnoser.go |
| NewGAGenerator 全部 | 伪接线 | GA 生成器空转 | ga_generator.go |
| ProfileStore 全部 | 伪接线 | 配置文件存储空转 | profile_store.go |
| ProfileExecutor 全部 | 伪接线 | 配置文件指令执行器空转 | profile_executor.go |

### 3.34 internal/introspect

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| spawnAgent | bug | bus.Register 返回错误被 _ = 丢弃，重复 agent ID 静默失败 | dashboard.go |

### 3.35 internal/kernelctx

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.36 internal/kernelscheduler

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| PreemptLowerPriority | bug | 仅检查静态 executor 数量，生产模式下恒为 0，抢占功能空转 | scheduler.go |
| executeUnbound | 伪接线 | 静态 executor 在生产模式下被全部跳过 | scheduler.go |
| HasCapableExecutor | 死代码 | 循环中冗余条件恒为 true | executor_registry.go |

### 3.37 internal/knowledge

状态: **部分接入内核** (runtime, provider, pipeline 通过 ares_bootstrap 接入)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| ServiceAdapter.Query | stub | 始终返回 (nil, nil)，文档自述"stateless" | service/adapter.go |
| ServiceAdapter.Distill | stub | 不运行 AKF 管线，直接包装原始字节 | service/adapter.go |
| NewServiceAdapter | 伪接线 | 从未被生产代码构造 | service/adapter.go |
| 全部 linker/ (similarity, timeline, decision, architecture) | 伪接线 | 知识连接器从未被创建 | linker/ |
| 全部 provider/postgres | 伪接线 | PG 知识提供者从未被注册 | provider/postgres/ |
| 全部 retriever | 伪接线 | 知识检索器从未被构建 | retriever/ |
| 全部 store/sqlite | 伪接线 | SQLite 知识存储从未被构建 | store/sqlite/ |
| 全部 workflow | 伪接线 | 知识工作流从未被构建 | workflow/ |

### 3.38 internal/llm

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.39 internal/llmservice

状态: **完全空转，未接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| Service.config 字段 | 死代码 | 存储后仅测试读取 | service.go |
| NewService 中的 BaseConfig 字段 | bug | RequestTimeout/MaxRetries/RetryDelay 被静默丢弃 | service.go |
| NewService 中的 MaxTokens | bug | LLMConfig.MaxTokens 未复制到 internalConfig | service.go |
| GenerateEmbedding | 空转 | embeddingClient 从未被生产路径设置 | service.go |
| GenerateEmbedding 类型断言 | bug | 脆弱运行时类型断言 | service.go |
| Generate (request==nil 错误语义) | bug | 返回 ErrInvalidConfig 而非 ErrInvalidInput | service.go |
| GenerateEmbedding 错误 | bug | 使用临时 fmt.Errorf 而非哨兵错误 | service.go |
| buildPrompt | bug | += 拼接 O(n²) 且无分隔符转义，存在 prompt 注入风险 | service.go |

### 3.40 internal/logger

状态: **已接入内核** (被广泛使用)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.41 internal/scoreutil

状态: **已接入内核** (被 ares_bootstrap 导入)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.42 internal/storage

状态: **部分接入内核** (postgres 通过 ares_bootstrap 接入)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| storage/memory/vector.go | 伪接线 | 内存向量存储从未被构建 | memory/vector.go |
| storage/postgres/query/QueryCache 全部 | 伪接线 | 查询缓存从未被生产代码调用 | postgres/query/cache.go |
| storage/postgres/query/MemoryQueryCache 全部 | 伪接线 | 同上，还启动无 cleanup 的 goroutine | postgres/query/memory_cache.go |

### 3.43 internal/system_runtime

状态: **已接入内核** (通过 ares_bootstrap)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.44 internal/taskfabric

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| Fabric.Create | bug | 直接存储调用方传入的指针，存在别名数据竞争 | fabric.go |
| NewLease | 死代码 | 全仓库无调用，且使用墙钟而非 f.now() 有不一致问题 | lease.go |

### 3.45 internal/tools

状态: **部分接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| executionPlanner.Plan | stub | 硬编码 cost=3, latency=100ms，占位实现 | tools/planner/executor.go |
| extractor.go var _ = math.Round | 死代码 | 为消除未使用 import 的占位引用 | tools/planner/extractor.go |
| capabilityPlanner.Plan subsumes | 死配置 | ExpressionEvaluation 能力从未被定义 | tools/planner/capability.go |
| CapabilitySelector 全部 | 伪接线 | 从未被生产代码构建 (SDK 默认用 AllSelector) | tools/toolsource/capability_selector.go |
| TagSelector 全部 | 伪接线 | 从未被生产代码引用 | tools/toolsource/selector.go |
| GlobalRegistry 包装函数 | 死代码 | 已废弃的全局注册表 | tools/resources/core/registry.go |
| builtin RegisterGeneralTools | bug | text_processor 被标记为 domain:"math"，标签与实际能力矛盾 | tools/resources/builtin/builtin.go |
| CommandTool.Execute | bug | Output() 先读入全部内存再检查 maxCommandOutputBytes | tools/discovery/discover.go |

### 3.46 internal/truncate

状态: **已接入内核**

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

### 3.47 internal/workflow

状态: **已接入内核** (engine 通过 ares_bootstrap 接入，graph 通过 provide_new_evolution 接入)

| 函数/方法 | 问题类型 | 描述 | 文件 |
|-----------|---------|------|------|
| - | - | 未发现明显空转或 bug | - |

---

## 4. 未使用的配置字段

| 配置路径 | 类型 | 说明 |
|---------|------|------|
| `cfg.Tools.Defaults` | `[]string` | 从未被生产代码读取 |
| `cfg.Tools.Agents` | `map[string]AgentToolConfig` | 从未被生产代码读取 |
| `cfg.Memory.SessionMemory` | `SessionConfig` | 定义但从未被 bootstrap 读取 |
| `cfg.Memory.UserProfile` | `ProfileConfig` | 定义但从未被 bootstrap 读取 |
| `cfg.Memory.TaskDistillation` | `DistillConfig` | 定义但从未被 bootstrap 读取 |
| `cfg.Memory.EnableDistillation` | `bool` | `ares.yaml` 已设置 `enable_distillation: false`，但生产路径从不读取（蒸馏实际由 Storage+Embedding 门控） |
| `cfg.Memory.DistillationThreshold` | `int` | `ares.yaml` 已设置 `distillation_threshold: 0`，但生产路径从不读取 |
| `ares.yaml` 中的 `reflection.enabled` | - | 不是合法配置字段，无效 |

---

## 5. 已知 Bug 汇总

### 5.1 空指针/类型断言风险

| 位置 | 风险 | 严重程度 |
|------|------|----------|
| agentloop/engine.go Run | req/e.LLM/e.Tools 为 nil 时直接 panic | **高** |
| ares_evolution/genome_wiring_system.go | 多处 nil 指针解引用风险 | **高** |
| ares_flight/collector.go handleMemoryDistilled | float64 类型断言恒失败，埋点恒为 0 | **中** |
| llmservice/service.go GenerateEmbedding | 脆弱运行时类型断言 | **中** |
| ares_arena/scenario.go RunScenarioReport | 注释声称 per-scenario 实际用 global stats | **中** |

### 5.2 并发安全/数据竞争

| 位置 | 风险 | 严重程度 |
|------|------|----------|
| ares_shutdown/signal.go SetContext | 写 h.ctx 未加锁 | **高** |
| ares_shutdown/manager.go StartShutdown | PhasePreShutdown==0 与零值混淆 | **高** |
| taskfabric/fabric.go Create | 直接存储调用方指针，存在别名竞态 | **中** |

### 5.3 逻辑错误

| 位置 | 描述 | 严重程度 |
|------|------|----------|
| ares_arena/scenario.go runScenarioReport | 分数计算实际使用 global stats 而非 per-scenario | **中** |
| ares_skills/http_source.go FetchHTTPManifest | 远程技能 Path 为空，加载时从错误路径读取 | **中** |
| llmservice/service.go NewService | MaxTokens 未复制，用户配置被静默丢弃 | **中** |
| llmservice/service.go buildPrompt | += 拼接 O(n²) + 无转义，prompt 注入风险 | **低** |
| tools/discovery/discover.go CommandTool.Execute | 内存检查在 Output() 之后，无法限制内存 | **中** |
| tools/resources/builtin/builtin.go | text_processor 被错误标记为 domain:"math" | **低** |
| tools/planner/extractor.go | var _ = math.Round 死代码引用 | **低** |
| ares_flight/graph.go AddNode | 父节点顺序异常时子节点永久孤儿 | **低** |
| ares_flight/timeline.go handleAgentEnd | ParentID 未设置，并发配对风险 | **低** |
| ares_arena/metrics.go 三个 Record* 方法 | 已废弃但未移除 | **低** |
| ares_security/sanitizer.go maskString | 非 ASCII 字符串按字节切片 | **低** |
| ares_security/sanitizer.go json.Number | 死代码分支 | **低** |
| introspect/dashboard.go spawnAgent | bus.Register 错误被丢弃 | **低** |
| agentloop/engine.go parseArgs | JSON 解析失败被静默吞掉 | **中** |
| agentloop/engine.go FriendlyErr | 错误链断裂 | **低** |
| llmservice/service.go Generate | 错误语义错配 | **低** |
| llmservice/service.go GenerateEmbedding | 错误不一致 | **低** |
| kernelscheduler/scheduler.go PreemptLowerPriority | 抢占功能在生产模式下恒空转 | **中** |
| kernelscheduler/executor_registry.go HasCapableExecutor | 冗余条件死逻辑 | **低** |
| kernelscheduler/scheduler.go executeUnbound | 静态 executor 在生产模式下被跳过 | **中** |
| agentloop/engine.go Run | req.Timeout 不约束单次 Generate | **中** |
| llmservice/service.go NewService | BaseConfig 字段被忽略 | **中** |
| ares_shutdown/callbacks.go | 整个 Callback* 子系统仅测试 | **低** |
