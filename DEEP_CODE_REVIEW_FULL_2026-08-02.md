# ARES POLIS 全量深度 Code Review

**Review Date**: 2026-08-02
**Scope**: goagent 全仓库 — 1430 个 Go 文件,9226 函数 + 4492 方法
**方法**: codebase-memory 知识图谱 (死代码/结构分析) + 逐文件人工阅读 + `rg` 交叉验证调用点
**说明**: 仅分析,未修改任何代码。

---

## 一、死代码 (Dead Code)

### 1.1 整棵 `internal/ares_quant/` 子树完全孤儿 🔴

40+ 个导出入口 **零生产导入方** (只有包内自引用和测试引用), `docs/module_review_unreviewed.md` 自己标注"零反向依赖"。约 5000+ 行代码未被任何二进制引用。

| 子包 | 死导出入口示例 |
|---|---|
| `marketmaking_api/` | `RegisterTools` (tools.go:57), `NewClient`, `NewDefaultBacktestRunner`, `NewDefaultPaperTrader`, `NewDefaultChaosExecutor`, `DefaultConfig` |
| `research/` | `NewReflector`, `NewMemoryLog`, `NewMemoryStore`, `NewGraphBuilder`, `NewEvaluator`, `RenderMarkdown` 系列, `Build*AnalystPrompt` |
| `research/agents/` | `NewBaseAgent`, `NewMarkdownParser`, `BuildMarketAnalystPrompt` 等 |
| `portfolio/` | `NewInvestmentSimulator`, `NewMultiAssetSimulator`, `LoadSignalsFromCSV`, `ValidateTradeSignal` |
| `marketmaking/` | `NewQuoteEngine`, `NewMMBacktestRunner`, `NewChaosExecutor`, `ComputeMMMetrics` |
| `market/` | `NewYahooFeed`, `NewPolymarketFeed`, `NewCoinGeckoFeed` |
| `dataflow/` | `NewVendorRouter`, `NewSnapshotBuilder`, `NewValidator`, `NewNormalizer` |
| `store/` | `NewMemoryStore`, `NewSQLiteStore` |

其中 `dataflow/router.go:14` 的 `Vendor` 接口连实现都不存在。

### 1.2 `internal/ares_memory/` 多个子系统从未接线 🔴

- **`context/rag.go` 整个 RAG 实现** (`RAG`, `NewRAG`, `VectorIndex`, `VectorSearcher`, `SearchByText`, `IsPersistent`, `InitStorage`, `Get`, `Delete`, `Size`) — 120+ 行向量搜索逻辑零生产引用。生产路径用 `MemoryRetriever` + `RunRetrieval`。
  - 附带 bug: `Add` 持久化到 pgvector 但 `evictOldestLocked` (rag.go:88) 只清内存 map → 持久化模式 DB 表无限增长,重启后内存 `Search` 查不到 DB 数据。
- **`context/user.go` 整文件** (`UserMemory`, `NewUserMemory`) — 零引用。
- **`context/cache.go` 整文件** (`Cache`, `NewCache`, `LRUCache`, `NewLRUCache`) — 零引用。
- **`pipeline.go` + `push/` + `report/` 整个蒸馏子系统** (`NewPipeline`/`Pipeline.Run`、`push.DefaultPushService`、`report.DefaultReportGenerator`) — 零生产 imports,从未 wire 进任何二进制。整个 ExperienceStore→Distiller→Report→Push 编排只作为代码 + 测试存在。
- `context/task.go:208-293` — `GetSteps`/`AddStep`/`AddResult`/`GetResults`/`SetContext`/`GetContext` 死方法。
- 其他: `manager.go:297` `ToBuildContextFormat`, `distillation/scorer.go` `TopNFilter`/`SortByImportance`/`GetMinImportance`, `distillation/resolver.go:130` `DetectConflictByExperience`, `distillation/classifier.go` `GetMemoryTypeFromString`, `distillation/distiller_admin.go` `GetMetrics`/`ResetMetrics`, `distillation/test_runner.go`+`test_set.go` 整套测试 harness 编译进生产代码。

### 1.3 纯 Redis 后端从未被生产使用 🔴

- `internal/storage/postgres/query/cache.go:55` `NewQueryCache` + `RedisClient` 接口 (`Del/Keys/Scan`) — **零非测试调用者**。生产 (`api_impl/service.go:364`) 用 `NewMemoryQueryCache`。整个 Redis 缓存类型是死的。
- `internal/storage/postgres/embedding/cache.go` — `RedisClient`、`NewEmbeddingCache` 仅测试引用。
- `internal/storage/postgres/embedding/fallback.go` — `FallbackClient`/`NewFallbackClient` 仅文档引用。

### 1.4 `PgSummaryRepository` 从未被生产使用 🔴

`internal/ares_events/summary_repository.go:21` — 整个 300 行 PG 实现 (`Save`/`FindByStreamID`/`FindByAgentAndTask`/`FindLatestByStreamID`/`Delete`/`DeleteOlderThan`) 零非测试调用者。生产 (`ares_archive/store.go:44`, `api_impl/store.go:32`) 用 `NewMemorySummaryRepository`。

### 1.5 `internal/api_impl/service.go` bootstrap dashboard 泄漏 🔴

`comp.Dashboard` (WSHub goroutine + http.Server) 启动后 **从不 Start/Stop** —— 每次 `StartService` 泄漏一个 hub goroutine(只靠 `hub.Stop()` 退出)并分配一个从不使用的 HTTP server。

### 1.6 无实现的空接口契约 🟠

| 位置 | 问题 |
|---|---|
| `internal/workflow/engine/types.go:125` | `StepRecoveryHandler.RecoverStep` — 全仓库无实现、无调用 (v3 遗留) |
| `internal/ares_runtime/executable.go:38` | `Executable.Execute` — 无实现 |
| `internal/monitoring/plugin.go:72` | `IntelProvider.AgentLevel` — `intel_adapter.go:34` 实现了但零调用 |
| `internal/tools/resources/core/factory.go:160,163` | `ToolLifecycle.Init/Stop` — `BaseTool` 实现了但零调用 |
| `internal/ares_ratelimit/limiter.go:16,18` | `Limiter.Reset/Rate` — 实现了但只有 `Allow/Wait` 被生产使用 |
| `internal/ares_observability/tracer.go:26` | `Tracer.WithTrace` — noop/log/otel 三实现,零生产调用 |
| `api/embedding/service.go:76` | `EmbeddingService.GetTimeout` — `client.go:55` 实现了,零调用 |
| `api/core/cleaning.go:67` | `ContextCleaner.ResetStats` — `cleaner.go:670` 实现了,零调用 |
| `internal/monitoring/detail_panel.go:78` | `DetailPanel.SetViewedAgent` — 零调用 |
| `internal/monitoring/data/trace_linker.go:101` | `TraceLinker.ListTraces` — 零调用 |
| `internal/agents/sub/tools.go:107` | `toolBinder.ListIdempotentTools` — 零调用 |

### 1.7 整包仅测试引用 / 死导出函数 🟠

- **`internal/core/errors/` 整个重试/DLQ/告警框架** (`AppError`, `ErrorCode`, `Handler`, `HandleError`, `RetryWithBackoff`, `ShouldDLQ`, `ShouldAlert`, `GetAlertMessage`, `LoadStrategiesFromConfig`, ...) — 仅测试引用;生产唯一用处是 `api/client/workflow.go` 里两个哨兵变量。
- `api/client.NewClient` — 无生产调用者,整个包基本测试/文档专用。
- `sdk.MustNew` (quickstart.go:37-48) — 仓库内零调用。
- `api/core/factory.go` `DefaultArenaConfig`/`DefaultEvolutionConfig`/`DefaultRuntimeConfig` — 仅测试引用 (生产 `DefaultDreamCycleConfig` 在 `internal/ares_evolution/dream_cycle.go`)。
- `internal/storage/postgres/models/tool.go:31` `Tool.UpdateUsage` + `ToolRepository.UpdateUsage` — 仅测试调用。
- `internal/storage/postgres/pool.go:177,209` `Pool.ExecWithTenant`/`QueryWithTenant` — 零调用。
- `internal/storage/postgres/services/retrieval_search.go:413` `bm25Search` — 被统一搜索路径取代,零调用。
- `internal/storage/postgres/services/retrieval_helpers.go:22` `GenerateDebugInfo` — 仅文档。
- `internal/api_impl/agent/service.go` `ErrAgentAlreadyExists` (errors.go:19) — 定义但从未使用。
- `internal/api_impl/adapters.go:159,251` `resurrectionTotal` 计数器 — 只加不读。
- `internal/api_impl/service.go:66,364` `experienceRanking`/`experienceConflicts`/`queryCache` — 构造后从未读取,queryCache 还空跑一个 5 分钟清理 goroutine。
- `api/evolution/evolution.go` `dreamCycleAdapter.SetEnabled/IsEnabled/TaskCount`、`populationAdapter.Agents` — 仅被 examples 引用。

### 1.8 `api/handler/` 11 个文件中 9 个整体死代码 🟠

`agent/arena/runtime/eval/flight/llm/workflow/evolution/stream` 的 `Register*Endpoints` 全部零生产调用 (仅 `router_test.go` 引用)。唯一接入生产的是 `MemoryHandler` 和 `RetrievalHandler` (`api_impl/service.go:303,316`)。

- `api/handler/agent.go:120` `HandleUpdate` — 无 PUT 路由,不可达。
- `api/handler/runtime.go:29` `StartAgentRequest` 死类型;runtime 的 StartAgent/StopAgent 无任何端点暴露。
- `api/handler/stream.go` 整个 `StreamHandler` 死代码。
- `api/core` 的 `Arena`/`FlightRecorder`/`EvaluatorRegistry`/`Dashboard`/`EventStore` 接口无生产实现 —— 仅 `_test.go` mock 维持。

### 1.9 `compat/` 层注册是"只写不读" 🟠

`compat.RegisterLLM` (`provide_llm.go:37`) 写入 registry 后, **没有任何代码从 registry 读回** (无 `Default.LLM().Get()`)。`protocol`/`loader`/`vector`/`tool` 子系统全部无生产使用方;`compat/loader/html` 自己承认是 placeholder 骨架。

---

## 二、敷衍实现 (Perfunctory / Stub)

| 位置 | 严重度 | 问题 |
|---|---|---|
| `sdk/evolve.go:186-203` | 🔴 | `applyEvolvedParams` **纯 no-op**:声称"把演化参数应用到 agent",实际每个参数只 `log.Printf`,**什么都不改**。`Evolve` 是化妆表演 |
| `sdk/evolve.go:104-107` | 🔴 | docstring 承诺返回 "best-evolved instruction",实际返回 `fmt.Sprintf("evolved: tool=...")` 参数摘要。`examples/eval/main.go:170` 把摘要当指令喂给 `WithInstruction` |
| `sdk/evolve.go:124-159` | 🟠 | `executeAndScore` 只有 `tool_selector` 真正影响结果,`search_depth`/`scheduler_strategy`/`memory_threshold`/`recovery_strategy` 零效果 —— GA 在优化一堆 no-op 维度 |
| `api/client/simple.go:73-93` | 🔴 | `SimpleClient` 基本是坏的:底层 `NewClientFromConfigPath` 的 `ToClientConfig()` 全 service 字段为 nil → `LLM()` 永远 `ErrLLMNotConfigured`,Execute/Chat 永不成功。无测试 |
| `api/router/router.go:25,50` | 🔴 | **硬编码默认 API key `"change-me-in-production"`**,且生产路径 `api_impl/service.go:302,315,344` 从不调 `WithAPIKey` → 内存/检索/eval 端点被公开源码里的已知凭据保护 |
| `api_impl/adapters.go:334-352` | 🟠 | `StartSurvival`/`StopSurvival` 纯 log 返回 nil;`GetSurvivalStatus` 硬编码 `{"running": false, "mode": "chaos_demo"}`。调了"开始"状态永远是"没在跑" (接入 dashboard 端点 `api_handlers.go:351,370`) |
| `sdk/sdk.go:274-307` | 🟠 | `Agent.Stream` **假流式**:先跑完整 `Run` 到完成,再把 `result.Output` 切成 10-rune 块 ("Simulate streaming")。docstring 承诺流式但延迟行为撒谎 |
| `sdk/team.go:227-250` | 🔴 | auto-split 时 `idx := i % len(available)`,available 为空 (零成员,或单成员兼 verifier) → **除零 panic** |
| `sdk/team.go:279-306` | 🟠 | member 执行是单次 LLM 调用,工具结果不回流 (无 ReAct 循环),`humanInput`/`maxIter` 被忽略 —— "并发执行"名不副实 |
| `internal/ares_eval/service/service.go:358-393` | 🟠 | `placeholderRunner` 全部测试返回 "默认 pass + 零指标" |
| `cmd/ares/db_migrate.go:47-50` | 🟠 | 打开 admin 连接后**立即 Close,从不调 `ensureDatabase`**,与 `Long` 描述 "creates the database if it doesn't exist" 矛盾 → DB 不存在时 `ares db migrate` 必失败 (定义在 114 行但未被本命令调用) |
| `cmd/check_rls/main.go` + `cmd/ares/db_check_rls.go` | 🟠 | 硬编码 `127.0.0.1:5433 postgres/postgres/ARES`,忽略 env 变量;文档宣称支持 `DB_HOST` 等 |
| `internal/monitoring/http_api.go:155` | 🟡 | `/subscribe` SSE 端点实际实现了,注释仍写 "SSE placeholder" |
| `internal/monitoring/plugin.go:120` | 🟡 | `WithCostAlertThreshold` 是空 Option ("placeholder for future cost alert integration") |
| `cmd/ares/knowledge_cli.go:20-31` | 🟡 | `knowledge build` 是返回 curl 说明的错误 stub,却在 usage 里当能力宣传 |
| `cmd/ares/evolution.go:50-66` | 🟡 | CLI 演化跑在**空的 MutableDAG** 上 (serve.go:582 注释承认) |
| `api/handler/evolution.go:214-232` | 🟡 | `HandleRegisterComponent` 是 501 stub (文档明确) |
| `cmd/ares/db_setup_test.go:16-29` | 🟡 | `db setup-test` 命令只在 `_test.go` 注册,发布二进制里不存在却在 usage 里列出 |
| `api/evolution/evolution.go:100-109` | 🟡 | `NewDreamCycle` 用裸类型断言 `scheduler.(*evolve.EvolutionScheduler)`,传错类型即 panic |
| `internal/api_impl/service.go:206` | 🟡 | `s.handler = http.NotFoundHandler()` 随后被 276 行覆盖,死赋值 |
| `internal/api_impl/service.go:114` | 🟡 | 启动时 `llm.Generate(ctx, "Reply OK")` 硬编码探活:每次启动烧一次真实 4096 max-token LLM 调用且阻塞启动 |

---

## 三、空实现 / 空占位

| 位置 | 严重度 | 问题 |
|---|---|---|
| `internal/ares_memory/context/task.go:319-350` | 🟠 | `TaskMemory.Distill` 是**透传占位**:docstring 承诺"提取关键信息",实际只是把 input/output/context 复制进 `models.Task`,Priority 固定 0。`DistillTask` 直接返回它 |
| `internal/tools/resources/core/factory.go:157-163` | 🟠 | `ToolLifecycle.Init/Stop` 空契约,无调用者 |
| `compat/tool/builtin/builtin.go` | 🟡 | `Noop` 工具,"for 骨架 wiring only",还留了 `var _ = fmt.Sprintf` 保证 import 存活 |
| `internal/storage/postgres/migrate.go:183-193` | 🟡 | `Migrate.RollbackLast` 无条件返回 `ErrQueryFailed`;`Seed` 空 no-op 返回 nil |
| `internal/knowledge/runtime/patcher.go:111-113` | 🟠 | `KnowledgePatchExecutor.Snapshot` 永远返回空 `PlanConfig{}` (真正 getter `PlanConfig()` 在 42 行)。兄弟 `GraphPatchExecutor`/`RecoveryPatchExecutor` 都返回真实快照 —— 唯独这个不一致,导致任何 diff 都对比零值 |
| `internal/workflow/engine/recovery_patcher.go` | 🟠 | `StepRecoveryHandler` 接口无任何实现 |
| `sdk/options.go:18,30,52` | 🔴 | `WithConfig`/`WithConfigFromEnv` 返回命名类型 `ConfigOption`,与 `Option` 不兼容 → **无法编译**,却写在 `sdk.go:335` docstring 里当用法示例,仓库内零引用 |
| `sdk/sdk.go:225-226` | 🟡 | memStrategyStore 的 `save` 方法注释引用了一个**不存在的方法** — 无写入路径 |
| `sdk/memory_wiring.go:363` | 🟡 | `buildPostgresPool(ctx)` 收到 ctx,`_ = ctx` 丢弃,docstring 与实现不符 |
| `internal/ares_mcp/server.go:522,529` | 🟡 | `url.PathUnescape` 双返回 `_` 吞掉错误 |
| `internal/ares_arena/injector.go` | 🟢 | RuntimeProvider/DAGProvider 接口(1 行方法)是合法接口,非空实现 — 排除误判 |

---

## 四、逻辑不通 (Logic Bugs)

### 4.1 列表过滤 / 复制字段错误 🔴

**`internal/api_impl/agent/service.go:141`** — `ListAgents` 的 type 过滤器拿 `a.Status` 跟 `filter.Type` 比 (复制粘贴错误,应比 `a.Type`)。Status 过滤器在 144 行正确。结果 `/api/v1/agents?type=xxx` 永远匹配不到。

**`internal/api_impl/agent/service.go:149-154`** — `ListAgents` 返回的防御性副本**丢掉 `Name` 和 `Type`** (只有 ID/SessionID/Status/CreatedAt)。公共层 `api/service/agent/service.go:133-140` 对每个 agent 都输出空 Name/Type。而 `GetAgent` (86-93) 正确复制了这两个字段 — 行为不一致。

### 4.2 Schema 漂移:repository ↔ 生产迁移 🚨🔴

`internal/storage/postgres/migrate_storage.go` 与 repository 引用的列/约束不一致,导致运行时崩溃,且被 `//go:build integration` + 与生产 schema 分叉的测试 schema (repository_test_helper.go) **掩盖**:

| # | 问题 | 位置 |
|---|---|---|
| 1 | `conversations` 表**无 `metadata` 列**,但 `ConversationRepository` 的 INSERT/SELECT 全部引用 (Create:59,70,84,95;GetByID:128;GetBySession:164) → 每个 Create/Get 报 `column "metadata" does not exist` | migrate_storage.go:162-172 |
| 2 | `tools` 表**无 UNIQUE(tenant_id,name)**,`ToolRepository.Create` 用 `ON CONFLICT (tenant_id, name)` (60,82,107,129) → `there is no unique or exclusion constraint matching the ON CONFLICT specification` | migrate_storage.go:120-145 (只有非唯一 `idx_tools_tenant_name`) |
| 3 | `secrets` 表**无 UNIQUE(tenant_id,key)** 且**缺 `updated_at` 列**,`SecretRepository` 用 `ON CONFLICT (tenant_id, key)` (72,217) + `updated_at = NOW()` (75,221) | migrate_storage.go:245-265 |
| 4 | `experiences_1024` **缺 `updated_at` 和 `usage_count`**,`IncrementUsageCount` (612-617) 与 `DecrementRank` (649-654) 引用不存在的列 | migrate_storage.go:75-90 |
| 5 | `experiences_1024.type` CHECK 约束 (`'query','solution','failure','pattern','distilled'`) **拒绝模型主常量 `"success"`** (models/experience.go:88) | migrate_storage.go:78 |
| 6 | `Experience.Problem/Solution` (文档字段) 从不持久化,repo 只写 `Input/Output` | experience_repository.go:63,76,93,104 |
| 7 | 测试 schema 与生产 schema 已经分叉 (缺 `conversations.metadata`、`experiences_1024.updated_at/usage_count`) → 集成测试也测不出 1/4/5 | repository_test_helper.go |
| 8 | `allowedTables` 白名单含 `embeddings`/`tasks`/`task_results`/`strategies` 陈旧表名 (实际是 `task_results_1024`/`evolution_strategies`) | base_repository.go:27-45 |
| 9 | 孤儿 `embeddings` 表 `VECTOR(1536)` 与全局 1024 维不符,无 repo 读写 | migrate.go:67-73 |

### 4.3 类型映射断裂:方案上限与跨会话去重静默失效 🔴

**`internal/ares_memory/experienceadapters/adapters.go:276-294`** — `GetByMemoryType`/`CountByMemoryType` 用 `memoryType.String()` (`"fact"/"preference"/"solution"/"rule"`) 查库,但:
- `ToStorageExperience` (**adapters.go:276-294**,struct literal 281-293) **插入时从不设置 Type**(留空 `""`);
- 存储模型的合法 Type 值是 `"success"/"failure"` (models/experience.go:17-18)。

结果 `WHERE type='fact'` 永远查不到。连锁后果:
- `enforceSolutionCap` (`internal/ares_memory/distillation/distiller.go:753`) 永远看到 0 条已有 solution → **方案记忆上限从不生效**;
- 跨会话去重查找永远空 → **"同一问题"检测路径永远触发不了**,冲突消解设计整体失效。

### 4.4 双 MCP manager 🔴

**`cmd/ares/serve.go:125+151`** — `Bootstrap`→`ProvideMCP` (bootstrap.go:124) 创建/启动第一个 manager,`serve.go:344` 注册其 Stop。随后 `setupMCP`→`SetupMCP` (provide_mcp.go:75-88) 对**同一批服务器**创建第二个独立 manager,`mcp.go:35-37` 只打印丢弃、**从不 Stop** → 重复建连 + 无关闭钩子。

### 4.5 `sdk/team.go:245` 除零 panic 🔴

auto-split 模式,`files` 非空但 `available` 为空 (零成员,或唯一成员也是 verifier) → `idx := i % len(available)` panic。应降级而非崩溃。

### 4.6 蒸馏内部逻辑 🟠

| 位置 | 问题 |
|---|---|
| `internal/ares_memory/distillation/distiller.go:578-596` | KeepBoth 分支:旧记忆 `Content` 存 problem,但 `Metadata` 只放 `"solution"` 键没放 `"problem"` 键 → `embedOneMemory` (242) 从 `Metadata["problem"]` (空) 构建 embedding → 旧记忆 embedding 与存储 Content 不匹配,problem 文本从知识库流失 |
| `internal/ares_memory/distillation/distiller.go:332,623` | 无锁读 `d.config.MinImportance`/`MaxMemoriesPerDistillation`,与 `UpdateConfig` (distiller_admin.go) 数据竞争 |
| `manager_rag.go:42-57` / `production_manager_rag.go:43-56` | 无锁读 `m.config.EnableRAG/RAGTopK/RAGMinScore`,与 `MemoryPatchExecutor.Apply` 并发变更竞态 |
| `push/service.go:278-337` | context 取消 (非 Stop) 退出时 `isRunning` 不复位 → 后续 `Start` 永远 `ErrAlreadyRunning` |
| `production_manager.go:107-190` | `NewProductionMemoryManager` 不调 `config.validate()` (对比 `NewMemoryManager`);session 只在内存 cache,重启后 `AddMessage` 回退 `userID="anonymous"`;FIFO 驱逐被注释成 "simple LRU" |
| `cleaner.go:594-650` | strategy 3 `groupByStructuralLinkage` 死分支 (`groupByUserBoundary` 保证非空输入 ≥1 turn),但 switch 仍接受 strategy 3 |
| `manager_impl.go:519` | `StoreDistilledTask` 的清理 sweep (含 `ExpireSessions`) 与刚蒸馏的会话同一 workspace → 长会话超 `MaxSessions` 时,刚蒸馏未落库的记忆可能被提前驱逐 |
| `production_manager.go:776` | `err == sql.ErrNoRows || strings.Contains(err.Error(), ...)` 直接相等 + 字符串嗅探,应 `errors.Is` |
| `internal/ares_memory/distillation/detector.go:36-44` | `"请问"` (中文"请问")被当负面关键词 → 以"请问"开头的真实问题永不蒸馏 |
| `retrieve_helper.go` | `RunRetrieval` (166-183) 与 `RetrieveAll` (42-102) 的 min-score 默认语义不一致 |
| `manager_impl.go:313` | `AddStructuredMessage` 无条件 `msg.Time = time.Now()` 覆盖调用方时间戳 |
| `manager_impl.go:381` / `production_manager.go:688` | `BuildContext` 失败时返回 `input, nil` 隐藏错误 |

### 4.7 API / handler 层逻辑 🟠

| 位置 | 问题 |
|---|---|
| `api/handler/agent.go:138-147` | `HandleUpdate` 解码 `req.Config` 但从不拷进 updates map → agent 配置更新静默丢弃;且无 PUT 路由 (router.go:136-139) → 死代码 |
| `api/handler/llm.go:53-56` | `ChatRequest.Stream` 解码后从不传给 `core.GenerateRequest{Stream:...}` → 请求 `stream:true` 静默拿到非流式响应 |
| `api/handler/stream.go:148-165` | SSE 循环只开头顶检查 `ctx.Done()`,processor 停止且不关 eventCh 时 `range` 永久阻塞 → goroutine 泄漏/挂死;全程无 heartbeat/ping (长 LLM 思考间隙代理可能断连);`StreamRequest.SessionID`/`Options` 解析后忽略;CORS 无 OPTIONS preflight (405) |
| `api/handler/stream.go:223` | `if flusher != nil` 死防御判断 (两处调用点都已断言非 nil) |
| `api/handler/retrieval.go:76-98` | `AddKnowledgeRequest.Metadata` (string 类型) 解码后从不使用,且与 `core.Metadata` (map) 类型不兼容 |
| `api/router/router.go:88-91,104-106` | 测试里 `RegisterEvolutionEndpoints(nil)` 注册的 handler 首次请求即 nil panic |
| `api/handler/memory.go:157` | `HandleGetMessages` 传 nil pagination → 无界查询 |

### 4.8 SDK / client 层逻辑 🟠

| 位置 | 问题 |
|---|---|
| `sdk/config.go:51-57,141-213` | `Tools.Builtin/MCP`、`Reflection.Enabled` 解析后从不应用;`LLM.Temperature/MaxTokens` 只校验不转发 → yaml 里配了无效 |
| `sdk/config.go:264-275` | `q.MinFinalScore < 0` 分支不可达 (264 行已对 `<=0` return) |
| `sdk/sdk.go:136,681,405-441` | `evolutionStore` 字段赋值后从不读取;`wireKnowledge` 建两个不同 `memStrategyStore`,都无写入路径 → 演化知识 provider 永远为空 |
| `sdk/sdk.go:948` / `akf_tools.go:31` | MCP/AKF 工具 `Parameters()` 返回 nil → LLM 拿到 "无参数" schema,所有 MCP 工具被当无参工具 |
| `sdk/options.go:268-273` | `WithLLMConfig` 无 nil 检查 |
| `api/client/config.go:363-382` | `setDefaults` 无 Anthropic 分支,而 `validate` 要求 provider/model 非空 → anthropic 配置必被拒 |
| `api/client/client.go:144-150` | `Runtime(config, eventStore)` 丢弃 eventStore 参数,`UseMemoryStore:false` 时把 nil EventStore 塞给 runtime service |
| `api/client/client.go:155-182` | `Close` 只停 memory/llm,docstring 承诺的 agent/retrieval/workflow 关闭没实现 |
| `api/client/client.go:235-237` | `Ping` 无锁读 `c.closed` → 与 `Close` 数据竞争 |
| `api/client/health.go` | `checkLLMHealth` 忽略 ctx,只测同步 `IsEnabled()` 布尔;`checkServiceHealth` 的 health = 非 nil 指针 |
| `api/client/workflow.go:342-351` | `Process` 对所有 agent 一律返回 `*models.RecommendResult` — 硬编码结果形状复制粘贴 |
| `api/client/simple.go:40-51` | config 被加载两次 (loader.Load + NewClientFromConfigPath 内部) |

### 4.9 工作流 / 知识层 🟠

| 位置 | 问题 |
|---|---|
| `internal/workflow/engine/graph_events.go` | `GraphEventHub` (`Subscribe`/`Publish`) 零生产订阅/发布,executor 从不发事件 |
| `internal/knowledge/store/postgres/store.go:574-633` | `pqStringArray`/`pqFloat32Array` 只实现 `Scanner` 无 `driver.Valuer`,却作 bind 参数用 → 写路径数组编码依赖 driver 行为;`store_test.go` 只测 Scan,无集成测试覆盖写路径 |
| `cmd/ares/bridge.go:17-19` | `Subscribe` 错误被裸 `return` 吞掉,订阅失败静默停桥 |

### 4.10 其他 🟡

| 位置 | 问题 |
|---|---|
| `internal/core/models/session.go:44-46` | `IsCompleted()` 把 `SessionStatusFailed` 当 completed |
| `internal/core/models/session.go:67-78` | `Progress()` 用结果数除任务数,无 result↔task 关联 → 可超 1.0 |
| `internal/core/models/task.go` | `TaskType` 与 `AgentType` 并存,`NewTask` 只设 AgentType,TaskType 永空 |
| `internal/core/models/user.go:39` | nil profile 返回 `ErrInvalidUserID` 而非 `ErrNilPointer` |
| `internal/core/errors/strategy_config.go:9-16` | 文档示例 `"backoff": "5s"` (字符串) 与 `time.Duration` (int64) 不兼容;导出按纳秒数字 |
| `internal/core/errors/handler.go:93` | `strategy.Backoff * time.Duration(1<<(attempt-1))` 在 attempt≥64 时先溢出后被 maxBackoff 截断 |
| `internal/api_impl/adapters.go:323-327` | `ResilienceScore` 里 `score` 与 `success_rate` 值完全相同 (重复 key) |
| `internal/api_impl/adapters.go:27,80` | `MCPContentBlock` 转换只拷 Type/Text,丢弃其他 content kind |
| `internal/api_impl/agent/service.go:216-245` | `ExecuteTask` 的 taskID 参数从不使用,memory manager 自生成 ID → 公共层按调用方 task.ID 缓存,而 result.TaskID 是生成 ID,不匹配 |
| `internal/api_impl/agent/service.go:39-65` | `CreateAgent` 静默覆盖同名 agent,旧 session 变孤儿 (ErrAgentAlreadyExists 定义但不用) |
| `internal/api_impl/service.go:516-522` | `Wait()` 用 `Shutdown(context.Background())` 无超时 + 丢弃 errgroup error |
| `sdk/team.go:371-373` | PASS/FAIL 是首现关键词子串匹配 |
| `sdk/team.go:409-429` | `extractFilePaths` 硬编码 `.md` 行 |
| `internal/ares_memory/classifier.go:60-62` | `solutionKeywords` 里 `"resolve"` 出现两次 |
| `internal/ares_memory/extractor.go:264-279` | `extractCoreSolution` 按字节索引截断,可切断多字节 UTF-8 (CJK) |
| `internal/ares_memory/session.go:147-164` | `Get` 返回内部 `*SessionData` 指针 (可被无锁修改) |
| `api/core/factory.go:21` | `DefaultArenaConfig` 硬编码 `"latency_spike"`,不是定义的 `core.FaultType` 常量 |
| `cmd/ares/db_create_table.go:44-50` | adminDB 双重 Close (defer + 显式) |
| `internal/storage/postgres/memory/vector.go:41` | `VectorSearcher.Search` 忽略 ctx |
| `internal/monitoring/plugin.go:120-122` | `WithCostAlertThreshold` 是空 option (注释自认 placeholder,未来成本告警预留) |
| `internal/monitoring/http_api.go:155` | 注释 "SSE placeholder" 过时——`handleSubscribe` (391-449) 是完整 SSE 实现,仅注释误导 |

---

## 五、已核查为"干净/正确"的区域 (排除误判)

以下先前被怀疑但复核确认为正常:

- `internal/ares_arena/injector.go` 的 1 行方法 — 是接口声明 (RuntimeProvider/DAGProvider),非空实现。
- `internal/ares_mcp/server.go` `matchURITemplate`/`splitTemplate`/`isPlaceholder` — greedy 占位符匹配逻辑正确。
- `internal/knowledge/hybrid.go` `ScoreHybrid` = 0.7*Vector + 0.3*Lexical,正确。
- 三个 SQL store (pg/mysql/sqlite) 的 `HybridSearch` 管线一致且正确。
- `internal/ares_evolution/genome/multi_objective.go:282` `DefaultDimensionBounds` — 有 `NormalizeDimensions` 潜在使用但 grep 无直接调用者,标注为"低优先级死代码"而非误判。
- `internal/ares_evolution/genome/crossover.go:483` `Crossover` 1 行 — 是接口声明。
- eval PostgreSQL SQL (`internal/ares_eval/service/repository.go`) — 列名全部正确。
- 事务块均使用 commit-flag + deferred-Rollback 正确模式。
- `internal/ares_quant/market/*` — `Markets()`/`Quote()` 返回 `"not supported"` 是各数据源 (yahoo/coingecko/polymarket) **能力边界的有意声明**,非缺陷(该项已降级为备注)。

---

## 六、总结与优先修复建议

**项目最大问题不是单个 bug,而是"代码资产化"严重**: 大量子系统 (`ares_quant` 全树、`ares_memory` 的 RAG/pipeline/push/report、`api/handler` 的 9 个死 handler、`compat` 只写不读层、Redis QueryCache、PgSummaryRepository) 是写完没接线或只被测试引用的悬空代码,合计上万行,与真实路径交织,是维护成本暗雷。(`internal/core/errors` 框架主体仅测试引用,但 `api/client/workflow.go:272,286` 使用了其中 `ErrAgentAlreadyStarted`/`ErrAgentNotRunning` 两个哨兵,不能整包删除。)

### 优先修复 TOP 5 (真实运行缺陷)

1. **`api_impl/agent/service.go:141,149`** — ListAgents type 过滤字段错 + Name/Type 丢失 → API 正确性。
2. **storage schema 漂移** (4.2) — 生产运行即崩且测试掩盖。
3. **`api/router` 硬编码 API key** (2) — 安全问题。
4. **`sdk/evolve.go` `applyEvolvedParams` 空实现** (2) — Evolve 功能名存实亡。
5. **`experienceadapters` Type 映射断裂** (4.3) — 方案上限与跨会话去重静默失效。

### 建议决策项

- `ares_quant` / `ares_memory` RAG/pipeline/push/report / `api/handler` 死 handler / `compat` 层 / `core/errors` — 在路线图内则接线,否则删除 (~上万行)。
- `migrate_storage.go` 与 `repository_test_helper.go` 统一 schema,并让测试 schema 从生产迁移派生。

---

**统计**: 🔴 HIGH 死代码/空实现 12 处 + 逻辑 bug 6 处;🟠 MED ~40 处;🟡 LOW ~30 处。
