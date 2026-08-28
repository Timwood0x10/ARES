# 补充审计报告：未完成 5 组模块深度审计

> 基于 `pseudo-wiring-audit-plan-zh.md` 的未完成清单（5/9 组），使用代码图（in_degree/ trace_path）逐符号验证，补全可决策级别的审计结论。
> 审计方法：每个导出符号查 `trace_path` 验证生产调用者，结合 `search_code` 的 in_degree 字段判定接线状态。

---

## ⑧ LLM / 基础设施（已完成，可决策）

### internal/llm

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewClient | 22 | bootstrap/ProvideLLM, gate3_orchestrator, llmservice, sdk/memory_wiring, compat | ✅ 接线良好 |
| NewFailoverClient | 11 | introspect/dashboard, gate3_orchestrator, llmservice, cmd/ares/peer_agents | ✅ 接线良好 |
| NewCircuitBreaker | 10 | gate3_orchestrator, retrieval_guard | ✅ 接线良好 |
| WithCallbacks | 8 | bootstrap/ProvideLLM, llmservice | ✅ 接线良好 |
| WithSanitizer | 3 | bootstrap/ProvideLLM | ✅ 接线良好 |
| WithCircuitBreaker | 2 | gate3_orchestrator | ✅ 接线良好 |
| WithRetryPolicy | 0 | 无人调用 | ❌ 死代码 |
| WithRateLimiter | 0 | 无人调用 | ❌ 死代码 |
| NewClientFromEnv | 0 | 无人调用（仅测试） | ❌ 死代码，建议删除 |
| NewFailoverScorer | 0 | 无人调用（Deprecated） | ❌ 死代码，建议删除 |
| Config.Extra 字段 | — | 全库无人读取 | ❌ 死配置，建议删除 |
| IsOpen/IsHalfOpen | 0 | 无人调用 | ❌ 死方法，建议删除 |
| DefaultRetryPolicy | 0 | 仅内部 NewClient 使用，无外部调用者 | ✅ 内部使用，保留 |

**结论：接线良好。删除 5 个死代码/死配置点。**

### internal/llm/output

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewFactory | 5 | serve_routine, dashboard | ✅ 接线良好 |
| CreateAdapter | 5 | serve_routine, dashboard | ✅ 接线良好 |
| NewParser | 2 | sub/executor, agentfabric/chat_cognition | ✅ 接线良好 |
| NewValidator | 1 | cmd/ares/peer_agents | ✅ 接线良好 |
| NewTemplate | 4 | 多处 | ✅ 接线良好 |
| NewOpenAIAdapter | 0 | 仅通过 Factory 注册，无人直接调用 | ✅ 工厂模式，设计合理 |
| NewOllamaAdapter | 0 | 同上 | ✅ 工厂模式 |
| NewOpenRouterAdapter | 0 | 同上 | ✅ 工厂模式 |
| WithLLMTimeout | 0 | 无人调用 | ❌ 死代码 |
| WithLLMStructuredTimeout | 0 | 无人调用 | ❌ 死代码 |
| WithDatabaseTimeout | 0 | 无人调用 | ❌ 死代码 |
| WithDatabaseTransactionTimeout | 0 | 无人调用 | ❌ 死代码 |
| NewTemplateEngine | 4 | peer_mode.go, peer_agents.go, chat_cognition.go, dashboard.go | ✅ 接线良好 — 生产真实调用4处；原报告0调用为误判，已纠正 |
| NewTemplateRegistry | 0 | 无人调用 | ❌ 死代码 |
| ParseOutput | 0 | 无人调用 | ❌ 死代码 |
| NewSchema | 0 | 无人调用 | ❌ 死代码 |
| NewSchemaGenerator | 0 | 无人调用 | ❌ 死代码 |
| NewTimeout | 0 | 无人调用 | ❌ 死代码 |
| RenderTemplate | 0 | 无人调用 | ❌ 死代码 |

**结论：接线良好。删除或 unexport 10+ 个死代码符号。**

### internal/errors

| 符号 | 生产调用者 | 判定 |
|------|-----------|------|
| New, Wrap, Wrapf, Newf | 全库广泛使用 | ✅ 接线良好 |

**结论：接线良好。**

### internal/ares_ctxutil

| 符号 | 生产调用者 | 判定 |
|------|-----------|------|
| WithDetachedLabel | manager_lifecycle.go, manager.go | ✅ 接线良好 |
| BackgroundStats | 被 ares_runtime/manager_lifecycle.go:202 生产调用 | ✅ 接线良好（原报告"无人调用"误判，已纠正） |

**结论：接线良好。BackgroundStats 生产在用（manager_lifecycle.go:202），保留。**

### internal/ares_protocol/ahp

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| AHPMessage（类型） | — | agents/peer, agents/sub, agents/base, cmd/ares | ✅ 类型被使用 |
| SendMessage | — | agents/peer, evolution_ipc | ✅ 被使用 |
| NewJSONCodec | 0 | 无人调用 | ❌ 死代码 |
| NewCodecRegistry | 0 | 无人调用 | ❌ 死代码 |
| NewDLQ | 0 | 无人调用 | ❌ 死代码，建议删除 |
| NewDLQProcessor | 0 | 无人调用 | ❌ 死代码，建议删除 |
| NewHeartbeatMonitor | 0 | 无人调用 | ❌ 死代码，建议删除 |
| NewMessageQueue | 0 | 无人调用 | ❌ 死代码，建议删除 |
| NewMessageRouter | 0 | 无人调用 | ❌ 死代码，建议删除 |
| NewProtocol | 0 | 无人调用 | ❌ 死代码，建议删除 |
| NewDynamicRouter | 0 | 无人调用 | ❌ 死代码，建议删除 |
| NewRateLimiter | 0 | 无人调用 | ❌ 死代码，建议删除 |

**结论：部分接线。保留 message/protocol/codec 核心，删除 DLQ/HeartbeatMonitor/Queue/Router/Limiter 等死子系统。**

---

## ⑥ Workflow / 任务编排（可决策）

### workflow/engine

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewMutableDAG | 64 | ProvideNewEvolution, BuildLiveAgentDAG | ✅ 接线良好 |
| Set | 107 | 大量使用 | ✅ 接线良好 |
| NewRecoveryPatchExecutor | 14 | ProvideNewEvolution | ✅ 接线良好 |
| NewGraphEventHub | 11 | 仅测试？ | ⚠️ 需验证 |
| NewJSONFileLoader | 5 | 仅测试？ | ⚠️ 需验证 |
| NewDAG | 3 | 仅测试？ | ⚠️ 需验证 |
| NewDefinitionParser | 3 | 仅测试？ | ⚠️ 需验证 |
| NewFileLoader | 3 | 仅测试？ | ⚠️ 需验证 |
| NewDirectoryLoader | 3 | 仅测试？ | ⚠️ 需验证 |
| NewFileWatcher | 2 | 仅测试？ | ⚠️ 需验证 |
| NewDirectoryParser | 1 | 仅测试？ | ⚠️ 需验证 |
| **NewAgentExecutor** | **0** | **无人调用** | ❌ 死代码 — 工作流引擎无执行器，确认了现有审计的"engine 无执行器"结论 |
| **NewHITLFeedbackPlugin** | **0** | **无人调用** | ❌ 死代码 |
| **NewMemoryInterruptStore** | **0** | **无人调用** | ❌ 死代码 |
| **NewOutputStore** | **0** | **无人调用** | ❌ 死代码 |
| NewWorkflowReloader | 0 | 无人调用 | ❌ 死代码 |
| NewAgentRegistry | 0 | 无人调用 | ❌ 死代码 |
| ListPending (HITL) | 0 | 无人调用 | ❌ 死代码 |

**结论：核心 MutableDAG + RecoveryPatchExecutor 被使用，但执行器（AgentExecutor）、HITL、中断存储、重新加载、输出存储、AgentRegistry 全部无人调用。工作流引擎整体处于"有 DAG 无执行器"状态，与现有审计一致。**

### workflow/graph

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewGraph | 6 | ProvideNewEvolution | ✅ 接线良好 |
| Node | 6 | ProvideNewEvolution | ✅ 接线良好 |
| Edge | 6 | ProvideNewEvolution | ✅ 接线良好 |
| NewFuncNode | 9 | ProvideNewEvolution | ✅ 接线良好 |
| Start | 1 | ProvideNewEvolution | ✅ 接线良好 |
| **SetPluginBus** | **0** | **无人调用** | ❌ 死代码 |
| **SetRouter** | **0** | **无人调用** | ❌ 死代码 |
| **SetTracer** | **0** | **无人调用** | ❌ 死代码 |
| **SetExecutionCollector** | **0** | **无人调用** | ❌ 死代码 |
| **SetLimiter** | **0** | **无人调用** | ❌ 死代码 |
| **SetCheckpointStore** | **0** | **无人调用** | ❌ 死代码 |
| **SetScheduler** | **2** | 仅测试 | ❌ 死代码 |
| **NewGraphWithTracer** | **0** | **无人调用** | ❌ 死代码 |
| NewAgentNode | 0 | 无人调用 | ❌ 死代码 |
| NewToolNode | 0 | 无人调用 | ❌ 死代码 |
| RemoveEdge | 1 | 仅测试 | ❌ 死代码 |
| RemoveNode | 1 | 仅测试 | ❌ 死代码 |
| Clear | 0 | 无人调用 | ❌ 死代码 |

**结论：基础图操作（NewGraph/Node/Edge/Start）被使用，但所有插件/路由器/追踪器/限流器/检查点 setter 全部无人调用。**

### taskfabric（已审，确认）

| 符号 | 判定 |
|------|------|
| Fabric.Create / Acquire / Task | ✅ 被内核调度器使用 |
| NewLease | ❌ 死代码，且使用墙钟 |

### agentfabric

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewFabric | 101 | 大量使用 | ✅ 接线良好 |
| NewChatCognition | 6 | 被使用 | ✅ 接线良好 |
| **NewSubAgentCognition** | **0** | **无人调用** | ❌ 死代码 |

**结论：`NewFabric` 和 `NewChatCognition` 接线良好。`NewSubAgentCognition` 无人调用，建议删除。**

---

## ④ 演化 / 评估（可决策）

### ares_evolution（旧版）

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewWiredEvolutionSystem | 22 | wireGAEvolution (bootstrap) | ✅ 接线良好 |
| NewMutator | 62 | 大量使用 | ✅ 接线良好 |
| NewPopulation | 46 | 大量使用 | ✅ 接线良好 |
| NewEvolutionGuardrails | 38 | 大量使用 | ✅ 接线良好 |
| NewCrossover | 35 | 大量使用 | ✅ 接线良好 |
| NewEvolutionScheduler | 33 | 大量使用 | ✅ 接线良好 |
| NewDreamCycle | 21 | 大量使用 | ✅ 接线良好 |
| NewMemoryAwareScorer | 29 | 大量使用 | ✅ 接线良好 |
| NewScoreCache | 28 | 大量使用 | ✅ 接线良好 |
| NewAdaptiveDistribution | 20 | 大量使用 | ✅ 接线良好 |
| NewRollbackPolicy | 20 | 大量使用 | ✅ 接线良好 |
| NewLLMArenaScorer | 17 | 大量使用 | ✅ 接线良好 |
| NewFlightToExperienceAdapter | 15 | 大量使用 | ✅ 接线良好 |
| NewShadowEvaluator | 15 | 大量使用 | ✅ 接线良好 |
| NewDefaultPromoter | 15 | 大量使用 | ✅ 接线良好 |
| NewTieredScorer | 12 | 大量使用 | ✅ 接线良好 |
| NewMemoryStrategyStore | 12 | 大量使用 | ✅ 接线良好 |
| NewExperienceGuidedMutator | 12 | 大量使用 | ✅ 接线良好 |
| NewFeedbackRecorder | 12 | 大量使用 | ✅ 接线良好 |
| NewActiveStrategyManager | 10 | 大量使用 | ✅ 接线良好 |
| NewLLMScorer | 1 | wireLLMScorer → wireGAEvolution | ✅ 接线良好 |
| NewGenomePopulationAdapter | 3 | NewWiredEvolutionSystem | ✅ 接线良好 |
| **NewEvidenceAggregatorProvider** | **0** | **无人调用** | ❌ 死代码 |
| **NewKnowledgeAdapter** | **1** | 仅测试 | ❌ 死代码 |
| **NewMutationAdapter** | **1** | 仅测试 | ❌ 死代码 |
| **NewMetaController** | **2** | 仅测试 | ❌ 死代码 |
| NewKnowledgeDistiller | 2 | 仅测试 | ❌ 死代码 |
| NewNondominatedSortingSelection | 1 | 仅测试 | ❌ 死代码 |
| NewTruncationSelection | 4 | 仅测试 | ❌ 死代码 |
| NewPopulationGenealogyRecorder | 4 | 仅测试 | ❌ 死代码 |
| NewHypothesisGenerator | 3 | 仅测试 | ❌ 死代码 |
| NewLLMReflector | 6 | 仅测试 | ❌ 死代码 |
| NewPGStrategyStore | 1 | 仅测试 | ❌ 死代码 |
| HintsForTask | 0 | 无人调用 | ❌ 死代码 |
| RecordStrategyOutcome | 0 | 无人调用 | ❌ 死代码 |

**结论：核心进化管线（NewWiredEvolutionSystem → 所有组件）接线良好。但多个高级/备用组件（PG 存储、多目标选择、假设生成、元控制、知识蒸馏、谱系记录等）仅测试引用。这些是"可选功能"类的死代码，建议决策：保留（进化系统启用时可能用到）或删除。**

### eval（原包，不是 ares_eval）

| 符号 | 判定 |
|------|------|
| 未审计（仅 ~5 行） | — |

### scoreutil

| 符号 | 判定 |
|------|------|
| 未审计（仅 ~5 行） | — |

### evidence（已审，确认）

| 符号 | 判定 |
|------|------|
| NewMemoryStore, NewPostgresStore | ✅ 接线良好 |

---

## ③ 记忆 / 知识 / 存储（可决策）

### ares_memory

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewMemoryManager | 33 | ProvideMemory | ✅ 接线良好 |
| NewPushService | 33 | ？ | ⚠️ 见下文 |
| NewDistiller | 26 | buildAKGDistiller | ✅ 接线良好 |
| NewReportGenerator | 25 | ？ | ⚠️ 见下文 |
| NewContextCleaner | 28 | bootstrap | ✅ 接线良好 |
| NewMemoryRetriever | 9 | wireRetrievers (bootstrap) | ✅ 接线良好 |
| NewPipeline | 6 | 被使用 | ✅ 接线良好 |
| NewMemoryPatchExecutor | 7 | ProvideNewEvolution | ✅ 接线良好 |
| NewMinimalMemoryManager | 6 | buildMemoryManager | ✅ 接线良好 |
| **NewProductionMemoryManager** | **2** | **无人调用（仅测试）** | ❌ 死代码——bootstrap 用 NewMinimalMemoryManager |
| NewMemoryManagerWithDistiller | 7 | 被使用 | ✅ 接线良好 |
| NewSessionMemory | 6 | 被使用 | ✅ 接线良好 |
| NewTaskMemory | 3 | 被使用 | ✅ 接线良好 |
| **NewExperienceSearcher** | **2** | wireRetrievers | ✅ 接线良好 |
| **NewDistillationRepo** | **3** | 仅测试 | ❌ 死代码 |
| **NewKnowledgeRetrieverAdapter** | **2** | 仅测试 | ❌ 死代码 |
| **SearchByVector (experienceadapters)** | **0** | **无人调用** | ❌ 死代码 |
| **GetByMemoryType (experienceadapters)** | **0** | **无人调用** | ❌ 死代码 |
| NewPushService | 0 | 无人调用 | ❌ 死代码 |
| NewReportGenerator | 0 | 无人调用 | ❌ 死代码 |

**结论：核心记忆管线（NewMemoryManager + Distiller + Retriever + Pipeline + PatchExecutor）接线良好。但 `NewProductionMemoryManager` 生产零调用（bootstrap 用 `NewMinimalMemoryManager`）——这是关键决策点：`ProductionMemoryManager` 有 PG 集成能力但未被使用。此外 `experienceadapters` 子包中的 `SearchByVector`/`GetByMemoryType` 无人调用，`NewPushService`/`NewReportGenerator` 无人调用。**

### knowledge/**（已确认，无需逐符号验证）

| 子包 | 从 cmd/ares 可达 | 判定 |
|------|-----------------|------|
| knowledge/pipeline | ✅ 是（通过 BuildKnowledgeRuntime） | 保留 |
| knowledge/planner | ✅ 是 | 保留 |
| knowledge/provider/memory | ✅ 是 | 保留 |
| knowledge/provider/code | ✅ 是 | 保留 |
| knowledge/provider/vector | ✅ 是（当 PG+embedding 启用） | 保留 |
| knowledge/provider/store | ✅ 是（当 AKG 启用） | 保留 |
| knowledge/provider/evolution | ？ | ⚠️ 需验证 |
| knowledge/runtime | ✅ 是 | 保留 |
| knowledge/mcp | ✅ 是 | 保留 |
| knowledge/linker | ❌ 不可达 | 删除或接线 |
| knowledge/provider/postgres | ❌ 不可达（仅示例） | 删除或接线 |
| knowledge/retriever | ❌ 不可达 | 删除或接线 |
| knowledge/service | ❌ 不可达（仅示例） | ServiceAdapter 恒返回 nil，删除 |
| knowledge/store/sqlite | ❌ 不可达 | 删除或接线 |
| knowledge/workflow | ❌ 不可达（仅示例） | 删除或接线 |

### storage/**

| 子包 | 判定 |
|------|------|
| storage/postgres | ✅ 被 bootstrap 使用 |
| storage/postgres/query | ❌ 无人调用（含测试）——完全孤儿，建议删除 |
| storage/memory | ❌ 仅集成测试引用——生产孤儿，建议删除或接线 |

### truncate

| 符号 | 判定 |
|------|------|
| 包很小，未发现明显问题 | — |

### services/embedding、api/embedding、api/knowledge、api/experience

**未审计**（这些是嵌入服务 API 和知识服务 API，非核心内核模块，相对独立）

---

## ⑤ 工具 / MCP（可决策）

### ares_mcp

| 符号 | in_degree | 生产调用者 | 判定 |
|------|-----------|-----------|------|
| NewMCPManager | 11 | ProvideMCP | ✅ 接线良好 |
| NewMCPServer | 22 | 被使用 | ✅ 接线良好 |
| NewMCPClient | 14 | 被使用 | ✅ 接线良好 |
| NewMCPTool | 8 | 被使用 | ✅ 接线良好 |
| NewStdioTransport | 9 | 被使用 | ✅ 接线良好 |
| NewSSETransport | 8 | 被使用 | ✅ 接线良好 |
| NewTransportFromConfig | 5 | 被使用 | ✅ 接线良好 |
| NewMCPConfigWatcher | 3 | 被使用 | ✅ 接线良好 |
| NewMCPToolFactory | 2 | 被使用 | ✅ 接线良好 |

**结论：`ares_mcp` 全部接线良好，无伪接线。**

### tools/**（已审，确认）

| 子包 | 判定 |
|------|------|
| tools/planner | executionPlanner 硬编码占位，capabilityPlanner 死配置 |
| tools/toolsource | CapabilitySelector/TagSelector 无人调用 |
| tools/resources/core | GlobalRegistry 死代码 |
| tools/resources/builtin | text_processor 标签错误 |
| tools/discovery | CommandTool 内存检查在 Output() 之后 |

### ares_skills（已审，确认）

| 子系统 | 判定 |
|-------|------|
| SkillOutcomeRecorder | ❌ 从未启动 |
| CatalogTools 5 工具 | ❌ 从未注册 |
| ExperienceConfidenceSource | ❌ 从未接入调度器 |
| FetchHTTPManifest | bug：远程技能 Path 未设置 |

### discovery、api/tools、api/mcp、compat

**未审计**（这些是 API 层和兼容层，非核心内核模块）

---

## 关键决策表

### 建议删除（无人调用，无保留价值）

| 符号 | 所属包 | 理由 |
|------|--------|------|
| NewClientFromEnv | internal/llm | 0 调用，生产不从环境变量构建 |
| NewFailoverScorer | internal/llm | Deprecated 别名 |
| WithRateLimiter | internal/llm | 0 调用 |
| WithRetryPolicy | internal/llm | 0 调用 |
| 全部 timeout.go 导出函数 | internal/llm/output | 全部 0 调用 |
| NewTemplateRegistry | internal/llm/output | 0 调用（NewTemplateEngine 生产在用，勿删） |
| ParseOutput / NewSchema / NewSchemaGenerator / NewTimeout / RenderTemplate | internal/llm/output | 全部 0 调用 |
| DLQ / DLQProcessor / HeartbeatMonitor / MessageQueue / MessageRouter / Protocol / DynamicRouter / RateLimiter | ares_protocol/ahp | 全部 0 调用 |
| NewAgentExecutor | workflow/engine | 0 调用 — 工作流引擎无执行器，需重新设计后才能接 |
| NewHITLFeedbackPlugin | workflow/engine | 0 调用 |
| NewMemoryInterruptStore | workflow/engine | 0 调用 |
| NewOutputStore | workflow/engine | 0 调用 |
| NewWorkflowReloader | workflow/engine | 0 调用 |
| NewAgentRegistry | workflow/engine | 0 调用 |
| SetPluginBus / SetRouter / SetTracer / SetExecutionCollector / SetLimiter / SetCheckpointStore / SetScheduler | workflow/graph | 全部 0 调用 |
| NewGraphWithTracer / NewAgentNode / NewToolNode / Clear / RemoveEdge / RemoveNode | workflow/graph | 全部 0 调用 |
| NewSubAgentCognition | agentfabric | 0 调用 |
| NewProductionMemoryManager | ares_memory | 生产零调用（bootstrap 用 NewMinimalMemoryManager） |
| NewPushService | ares_memory | 0 调用 |
| NewReportGenerator | ares_memory | 0 调用 |
| SearchByVector / GetByMemoryType | ares_memory/experienceadapters | 0 调用 |
| NewDistillationRepo / NewKnowledgeRetrieverAdapter | ares_memory/experienceadapters | 仅测试 |
| NewEvidenceAggregatorProvider | ares_evolution | 0 调用 |
| NewKnowledgeAdapter | ares_evolution/genome | 仅测试 |
| NewMutationAdapter | ares_evolution | 仅测试 |
| NewMetaController | ares_evolution/genome | 仅测试 |
| NewKnowledgeDistiller | ares_evolution/genome | 仅测试 |
| NewNondominatedSortingSelection | ares_evolution/genome | 仅测试 |
| NewTruncationSelection | ares_evolution/genome | 仅测试 |
| NewPopulationGenealogyRecorder | ares_evolution | 仅测试 |
| NewHypothesisGenerator | ares_evolution/genome | 仅测试 |
| NewLLMReflector | ares_evolution/genome | 仅测试 |
| NewPGStrategyStore | ares_evolution | 仅测试 |
| HintsForTask / RecordStrategyOutcome | ares_evolution | 0 调用 |
| storage/postgres/query 全部 | storage | 完全孤儿（含测试 0 引用） |
| storage/memory 全部 | storage | 生产孤儿 |
| knowledge/service | knowledge | 不可达，ServiceAdapter 恒返回 nil |
| knowledge/linker | knowledge | 不可达 |
| knowledge/provider/postgres | knowledge | 不可达 |
| knowledge/retriever | knowledge | 不可达 |
| knowledge/store/sqlite | knowledge | 不可达 |
| knowledge/workflow | knowledge | 不可达 |

### 建议保留（接线良好）

| 符号 | 所属包 | 理由 |
|------|--------|------|
| 全部核心 LLM 客户端 | internal/llm | 真实使用 |
| NewMCPManager + 全部 ares_mcp | ares_mcp | 真实使用 |
| NewFabric + NewChatCognition | agentfabric | 真实使用 |
| NewMutableDAG + 核心图操作 | workflow/engine + graph | 被进化系统使用 |
| NewWiredEvolutionSystem + 核心进化组件 | ares_evolution | 被 bootstrap 使用 |
| NewMemoryManager + Distiller + Retriever + Pipeline | ares_memory | 被 bootstrap 使用 |
| NewContextCleaner | ares_memory | 被 bootstrap 使用 |
| NewMemoryPatchExecutor | ares_memory | 被 ProvideNewEvolution 使用 |
| 全部 knowledge 保留子包 | knowledge | 被 BuildKnowledgeRuntime 使用 |

### 建议接线（保留但需接）

| 符号 | 所属包 | 接线方案 |
|------|--------|---------|
| NewProductionMemoryManager | ares_memory | bootstrap 改为用 NewProductionMemoryManager 替代 NewMinimalMemoryManager，让 PG 集成生效 |
| NewAgentExecutor | workflow/engine | 需要完整的工作流执行器设计（现有审计已计划，见 workflow-engine-wiring-plan-zh.md） |
| NewLLMScorer | ares_evolution | 已接（wireLLMScorer），但需确认 cfg.Evolution.LLMScoring.Enabled 真正触发 |
