# 模块级深度 Review 汇总（2026-08-03）

> 范围：**全仓库逐模块审查**（api/ 34 包、cmd/ 11 命令、internal/ 80+ 包、sdk/、examples/），由 3 批共 12 个 explore 子代理并行完成，覆盖全部模块，无遗漏。审查重点是：空实现 / stub / 死代码、功能闭环断裂（写了没接、接了没通）、忽略 error、裸 goroutine、缺 ctx 取消、nil 解引用、goroutine 泄漏、并发竞态。严重度：🔴 真 bug / 闭环断裂，🟡 健壮性，⚪ nit。

---

## 0. 本轮已修复项（配合前序 review 结论）

| # | 修复 | 文件 | 验证 |
|---|------|------|------|
| 4.1 | `FlightRecorder.Stop` 接入 serve shutdown 管理器 + `api_impl.Stop` | `cmd/ares/serve.go`、`internal/api_impl/service.go` | build/vet/test 全绿 |
| 4.2 | `Components.WaitBackground()` 等待后台 goroutine 退出 | `internal/ares_bootstrap/bootstrap.go`、`serve.go`、`api_impl/service.go` | 新增 `TestWaitBackground_*` 通过 |
| 4.3 | AKG 蒸馏 errgroup `Wait()` 加入关闭路径（不丢在途蒸馏） | `internal/ares_bootstrap/bootstrap_steps.go` | build 通过 |
| 4.4 | LLM 建议 prompt 注入真实进化状态（各基因组 fitness 均值 + 当前部署策略） | `internal/ares_bootstrap/bootstrap_steps.go` | 新增 `TestBuildEvolutionSuggestionPrompt_*` / `TestRecentFitnessSummary_*` 通过 |
| 3.4 | 删除 legacy `EvolutionComponents.DreamCycle` 死字段 | `internal/ares_bootstrap/provide_evolution.go` | build 通过 |

> 修复过程中按 `plan/rules/code_rules.md` 核查：gofmt 全绿、文件行数 <1000、新增函数 <100 行、无全局变量传参、error 不静默忽略（`akgEg.Wait` 显式处理）、注释英文、测试覆盖正常+边界+异常路径。

---

## 1. api/ 层

### 1.1 api/agent — OK（纯别名/再导出包，无逻辑）

### 1.2 api/bootstrap — 3 issues
- 🟡 `bootstrap.go:179-185`（根因 :108）`ARES.Stop()` 在 `cfg.Memory == nil` 时对空 `&memsvc.Service{}`（nil inner）调用 `Stop` → 关闭路径 panic。
- 🟡 `bootstrap.go:159-164` `ARES.Start()` 只启动 Runtime；配置的 Flight recorder 创建后从未 `Start()` → collector 不订阅事件，flight/evidence 零记录。
- 🟡 `bootstrap.go:340` `getEvolutionStatus` 忽略 `EvidenceStore.Query` 错误（`evs, _ :=`），失败时静默报 `EvidenceEntries: 0`。

### 1.3 api/client — 3 issues
- 🟡 `config.go:480 + simple.go:77-97` `ToClientConfig()` / `NewClientFromConfigPath()` / `NewSimpleClient` 产出全部 service 为 nil 的 client，`SimpleClient.Execute/Chat` 必然 `ErrLLMNotConfigured` — 文档宣称的"最简单入口"闭环断裂。
- 🟡 `client.go:144` `Runtime(config, _ interface{})` 静默丢弃文档化的 `eventStore` 参数，恒传 nil。
- 🟡 `health.go:65` `checkLLMHealth` 只调 `IsEnabled()`（无网络请求），LLM 不可达也报 healthy。

### 1.4 api/core — OK（接口/类型/配置默认值）

### 1.5 api/discovery — 2 issues
- 🟡 `discovery.go:116-118` `Engine.Start()` 转调 `StartAutoDiscovery`，内部裸 `go func` 循环（无 errgroup），周期错误只内部打日志不上抛。
- ⚪ `discovery.go:14` 包注释示例引用不存在的 `NewSQLiteStore`。

### 1.6 api/embedding — OK（纯接口）

### 1.7 api/evolution — 3 issues
- 🔴 `evolution.go:100-109` `NewDreamCycle` 对 `any` 参数裸类型断言（类型错即 panic）且静默丢弃 `opts ...any`，恒接线 `tester=nil, genealogy=nil` — GA/tester 路径永远配不起来。
- 🟡 `evolution.go:253-259` `NewPopulation` 无 nil base 检查，直接解引用。
- ⚪ `evolution.go:167` `Agents()` 丢弃 `Snapshot()` 错误。

### 1.8 api/evolution/genome — 2 issues
- 🟡 `genome.go:45-63` `NewCrosser` 从未应用 `CrosserConfig.CrossoverType`（配置字段是死的，恒 uniform）。
- ⚪ `genome.go:91-97` `CrossWithType(..., CrossoverScattered)` 实际执行 plain uniform — 标签误导的 no-op。

### 1.9 api/evolution/mutation — 1 issue
- 🟡 `mutator.go:103-104,159-172` `Mutator.paramProb/promptProb` 设置了但从不读取；`MutatorConfig.ParamMutationProb/PromptMutationProb` 从未转发给内部 mutator — 用户概率配置静默失效。

### 1.10 api/experience — OK（纯 DTO + 接口）

### 1.11 api/flight — 2 issues
- 🟡 `flight.go:49-52,90-102` `Graph()` / `Diagnostics()` 返回零导出方法的黑盒 stub — 内部读取 API（Root/Nodes/Depth/Export*）从未暴露，读侧缺失。
- 🟡 `flight.go:60-68` `Events()` 转换时丢失 `TimelineEvent.Metadata`。

### 1.12 api/graph — OK（纯再导出）

### 1.13 api/handler — 2 issues
- 🔴 `agent.go:13` 11 个 handler 类型中 9 个（agent/llm/workflow/arena/evolution/runtime/flight/eval/stream）从未在生产接线 — 死端点，只有 memory/retrieval 挂载。
- 🟡 `stream.go:140` `StreamRequest.SessionID` 与 `Options` 解码后从不使用。

### 1.14 api/integration — OK（纯测试包）

### 1.15 api/knowledge — OK（类型别名 + 服务接口契约）

### 1.16 api/mcp — 3 issues
- 🔴 `mcp.go:162-174 + stdio.go:65-105` `sendNotification` 走 `roundTrip`，stdio 下阻塞等 30s 等一个永远不来的响应，然后关 stdin — 每次 `ConnectStdio` 连接都慢 30s 且打断连接。
- ⚪ `mcp.go:245-271` `scanClaudeConfig` 只解析 command/args，从不解析 `url` — SSE 配置的 MCP server 被静默跳过。
- ⚪ `mcp.go:35-36,113-114` `Client.connected` / `Client.tools` 字段设置后从不读取 — 死状态。

### 1.17 api/memory — 1 issue
- ⚪ `memory.go:53-61` `Config.Storage` 是死配置 — 存储后端仅由调用哪个构造器决定。

### 1.18 api/memory/distillation — 1 issue
- 🟡 `distillation.go:119-125` `DistillConversation` 转换时丢弃 `Message.ToolCalls`（及 EventKind/ParentID/ArtifactRefs）— 工具调用数据静默丢失。

### 1.19 api/router — 3 issues
- 🔴 `router.go:51,70` `NewRouter()` 恒 `apiKey=""`（默认全拒）且无关闭鉴权途径；`api_impl` 挂载 memory/retrieval/eval 路由不带 key → 这些端点恒 401。
- 🔴 `router.go:91,108` `RegisterEvolutionEndpoints` 等 5 组注册函数生产从不调用 — 死路由。
- 🟡 `router.go:91` `RegisterEvolutionEndpoints(nil)` 注册绑定 nil receiver 的路由 → 首次请求 panic（无 nil 守卫）。

### 1.20 api/service/* — 汇总
- api/service/agent — 🟡 `service.go:218-235` `ExecuteTask` 缓存 key 用 `task.ID` 但 `TaskResult.TaskID` 是内部生成的 ID → `GetTaskResult` 永远 miss。
- api/service/arena — OK（薄包装）
- api/service/callbacks — OK
- api/service/dashboard — OK
- api/service/eval — OK（薄包装）
- api/service/events — OK（Subscribe goroutine 尊重 ctx）
- api/service/evolution — OK
- api/service/flight — OK
- api/service/knowledge — 3 issues：🟡 `service.go:217-243` `handleDistill` 是 stub（伪造 KnowledgeObject 直接返回，无蒸馏无持久化）；🟡 `service.go:121,154,198` 无 nil 守卫解引用 `s.rt/s.ret/s.comp`（`New(nil,nil,nil)` 合法 → 请求即 panic）；⚪ 包本身是死代码（无外部引用）。
- api/service/llm — OK
- api/service/memory — 🟡 `service.go:109` `CreateSession(ctx, userID)` 签名与 `core.MemoryService.CreateSession(ctx, *SessionConfig)` 不符，且缺 `GetSession/UpdateSession` → 无法满足 handler 要求接口，bootstrap 的 memory service 永远接不上 API（闭环断裂）。
- api/service/runtime — ⚪ `service.go:154-161` `RegisterAgent` 静默丢弃 nil agent/factory（无错误路径）。
- api/service/workflow — OK（errgroup + ctx + 超时齐全）

### 1.21 api/tools — 2 issues
- 🟡 `builtin.go:166,316,373,487,608` calculator/regex/json/web_search/file 工具的 `Parameters()` 返回 nil，但 `Execute` 读取具名参数 — 工具对 planner/LLM 无参数 schema。
- ⚪ `tools.go:101` `NewRegistry()` 忽略 `RegisterBuiltinTools` 错误。

### 1.22 api/workflow — OK（纯类型/常量再导出）

---

## 2. cmd/ 层

### 2.1 cmd/arena — 1 issue
- 🟡 `main.go:428` `arena serve` 无信号处理/优雅关闭 — `ListenAndServe()` 阻塞到出错，SIGINT/SIGTERM 直接杀进程（不像 `pollSurvival` 装了 handler）。

### 2.2 cmd/ares — 4 issues
- 🔴 `db_create_table.go:102` `CREATE POLICY IF NOT EXISTS ...` 是非法 PostgreSQL 语法 — RLS 策略从未创建，却打印 "Row Level Security enabled"。
- 🟡 `serve.go:112` 信号 goroutine 调 `comp.WaitBackground()`；若 Bootstrap 之前出错返回（bootstrap/LLM/MCP 失败路径），`comp` 为 nil → goroutine 内 nil 解引用 panic。
- 🟡 `arena.go:475-488` `ares arena survival` 无信号处理，且 `pollSurvival` 在服务完成后永不退出 — 循环到被 Ctrl+C 杀死。
- 🟡 `knowledge_cli.go:24-30` `ares knowledge build` 是 stub — 恒返回错误让用户改用 curl，命令从不执行动作。

### 2.3 cmd/check_rls — OK

### 2.4 cmd/create_distilled_table — 1 issue
- 🔴 `main.go:93` 同样的 `CREATE POLICY IF NOT EXISTS` 非法 SQL — RLS 策略创建恒失败（静默警告），却打印 "Row Level Security enabled"。（与 cmd/ares/db_create_table.go:102 同 bug。）

### 2.5 cmd/embedding-mcp — OK（errgroup + 信号处理齐全）

### 2.6 cmd/flight — OK

### 2.7 cmd/mcp-null — OK（errgroup + 信号处理）

### 2.8 cmd/migrate_db — OK

### 2.9 cmd/monitor-demo — 1 issue
- 🟡 `main.go:51,157` 裸 `go simulateWorkload` / 每任务 `go func` 无跟踪无 join；无信号处理，Ctrl+C 直接杀进程，`cancel()` 从不执行。

### 2.10 cmd/monitor-live — 3 issues
- 🔴 `main.go:72-76,229` SIGINT/SIGTERM handler 只调 `cancel()`，但 `main` 阻塞在 `httpSrv.ListenAndServe()` 永不退出 — Ctrl+C 无法优雅关闭（需 kill -9）。
- 🟡 `actions.go:25-23` `actionHandler` 无 API-key 鉴权（不像 cmd/ares/actions.go 的 deny-by-default）— kill/resume/chaos/tool-call 端点裸奔。
- ⚪ `main.go:199,208` 裸 `go bridgeEvents` / `go submitTasks`；`setupMCP` 泄漏 bootstrap 的 MCP manager（建第二个 manager）。

### 2.11 cmd/setup_test_db — OK

> 跨命令注意项：`cmd/ares/db_setup_test.go:51,54` 双关 adminDB；`cmd/ares/start.go:53` 裸 `go func` 信号 handler 忽略 `svc.Stop` 错误；多个 db 命令忽略自建 DSN 的 `url.Parse` 错误。

---

## 3. internal/ 层

### 3.1 internal/agentloop — 1 issue
- 🟡 `engine.go:239` `_ = e.Memory.AddMessage(...)` 静默吞掉 assistant 消息持久化失败。

### 3.2 internal/agents — 2 issues
- 🟡 `service_impl.go:406-424` `executeTaskLogic` 是 stub — "retrieve"/"generate"/"simple" 全伪造输出字符串，无实际检索/生成。
- ⚪ `service_impl.go:430` `taskResults` 内存 map 无界增长，无淘汰/TTL。

### 3.3 internal/agents/base — OK（接口 + 配置）

### 3.4 internal/agents/leader — 4 issues
- 🟡 `agent.go:172-180` `Start` 只记录一次心跳，从不启动周期性发送（注释自认 "In a production environment, you would start a background goroutine"）— 基于心跳的存活检测实际从不持续。
- 🟡 `agent_types.go:115` / `agent.go:248` `distillWg` 从未 `Add`（蒸馏用 `distillEg.Go`），`Stop` 的 `distillWg.Wait()` 是死 no-op。
- 🟡 `supervisor.go:168-190` `handleFailover` 在释放 `s.mu` 之后才调 `g.Go(...)`；`Stop` 可能在该窗口完成 `g.Wait()` → errgroup panic（"Add called after Wait"）。锁注释声称封住 TOCTOU，但没覆盖 `g.Go` 本身。
- 🟡 `checkpoint.go:44` `CheckpointRepository.Save` 生产从不调用，`WithCheckpoint` 从未接线（cmd/ares、cmd/monitor-live）— leader 只读不写 checkpoint，会话恢复闭环未通。

### 3.5 internal/agents/sub — 4 issues
- 🟡 `agent.go:193-207` `Stop` 直接 `close(stopCh)` 无幂等守卫 — 并发/重复 Stop（Stopping/Busy 期间）double-close panic。
- 🟡 `agent.go:340` `ProcessStream` 的 `defer a.setStatus(Ready)` 在函数返回时执行（早于 spawn 的 goroutine 完成）— 状态提前翻 Ready，并发 `Process/ProcessStream` 可并行跑 executor。
- 🟡 `handler.go:40-48` `handleTaskMessage`/`handleAckMessage` 是 no-op stub，`messageHandler.agentID` 是死字段 — task 消息被当 nil 确认且不执行任何动作。
- 🟡 `heartbeat.go:21-74` `heartbeatSender` 生产从不启动（仅测试）— 子/leader agent 从不发周期心跳。

### 3.6 internal/api_impl — 2 issues
- ⚪ `adapters.go:254` `resurrectionTotal` 计数增加后从不读取 — 死状态。
- ⚪ `service.go:324,337,370` `wireWiredServices` 无检查类型断言 `router.Handler().(*http.ServeMux)` — 若 Handler 返回其它类型即 panic。

### 3.7 internal/api_impl/agent — 3 issues
- 🟡 `service.go:46-72` `CreateAgent` 重复检查是 TOCTOU（RLock 查存在、之后独立 Lock 插入）— 两个并发同 ID create 都通过，泄漏一个 session。
- 🟡 `service.go:54` `s.memoryMgr.CreateSession` 无条件调用；`agentapi.NewService(nil)` 后 `CreateAgent/ExecuteTask` nil 接口 panic（公共包装有守卫，但构造器接受 nil）。
- ⚪ `service.go:298-301` `StatusRunning/StatusStopped/StatusError` 常量从不使用（agent 只设 `StatusReady`）。

### 3.8 internal/ares_archive — OK（10 文件全查，清理/轮转错误均为记录过的 best-effort）

### 3.9 internal/ares_arena — 2 issues
- 🟡 `service.go:76-128` `Execute` 无 nil 守卫解引用 `s.injector`，但 `NewService` 接受 nil injector（仅警告）→ panic；`randomChaosAction`（survival.go:248,253,258,268）同样未守卫。
- ⚪ `http.go:450` 裸 `go func` 在 `context.Background()` 上跑长任务 `RunSurvival`（可达 2×duration）fire-and-forget，关闭时无跟踪/join。

### 3.10 internal/ares_bootstrap — OK（本轮已深度审查 + 修复，见第 0 节）

### 3.11 internal/ares_callbacks — OK（Registry/bridge/dispatch 干净，Emit 有 panic recovery）

### 3.12 internal/ares_config — 1 issue
- ⚪ `config.go:392,418` 畸形 `SERVER_PORT`/`DB_PORT` env 值被静默丢弃（Sscanf 错误被 `err == nil` 守卫吞掉）。

### 3.13 internal/ares_ctxutil — OK

### 3.14 internal/ares_events — 1 issue
- ⚪ `memory_store.go:215-218` 每个订阅者的清理 goroutine 若调用者从不 cancel ctx 则永远阻塞在 `ctx.Done()`；`Close()` 只 cancel store 自己的 ctx，不 cancel 订阅者 ctx → 被弃订阅的 goroutine 泄漏。

### 3.15 internal/ares_eval — OK（agent runner / 并发 runner(errgroup+SetLimit) / LLM judge / evaluator / loader / report 全干净）

### 3.16 internal/ares_eval/service — 2 issues
- ⚪ `handler.go:62` 裸 `go func` 在 `context.Background()` 上启动异步 eval（30min 超时、panic recovery，但不可跟踪/不可 join，脱离请求生命周期）。
- ⚪ `service.go:260-296` `placeholderRunner` 兜底是死代码（`NewAgentTestRunner` 只对 nil executor 报错，而该 nil 已在 253 行预检）；若真走到，stub 伪造零指标的 "pass" 结果。

### 3.17 internal/ares_experience — 3 issues
- 🟡 `distillation_service.go:80` `Distill` 调用 `s.embeddingClient.Embed` 无 nil 检查 — 未配 embedding 时 panic。
- ⚪ `distillation_service.go:109` `s.experienceRepo.Create` 无 nil 守卫（构造器接受 nil repo）。
- ⚪ `feedback_service.go:51,82` `IncrementUsageCount`/`DecrementRank` 无 nil 检查解引用（构造器接受 nil repo）。

### 3.18 internal/ares_experience/service — OK（纯再导出 shim）

### 3.19 internal/ares_evolution（根包）— OK（Scheduler/DreamCycle/adapter/feedback/guardrails/shadow/strategy store 均用 errgroup，无阻断问题）

### 3.20 internal/ares_evolution/experience — OK（store/normalizer/aggregator/collector 完整，锁与 ctx 正确）

### 3.21 internal/ares_evolution/genome — 1 issue
- ⚪ `spatial_index.go:39` `newSpatialIndex` 解引用 `scored[0].Params[k]` 无 `len(scored)` 守卫（当前唯一调用点先查 `m<2`，潜在复用即 panic）。

### 3.22 internal/ares_evolution/mutation — OK（mutator/guided mutator(空池有兜底)/adaptive distribution/LLM hint provider 完整）

### 3.23 internal/ares_evolution/promotion — OK（DefaultPromoter 完整状态机，线程安全，cooldown/历史上限）

### 3.24 internal/ares_evolution/scoring — OK（Budget/LRU cache/tiered scorer(LLM 有 panic recovery)/memory-aware 完整）

### 3.25 internal/ares_evolution/service — 2 issues
- ⚪ `service.go:775-784` `scoreAgents` 逐 agent 路径丢弃 errgroup 派生 ctx（`g, _ :=`）且吞 `g.Wait()` 错误；ctx 取消时在途 scorer goroutine（ScorerFunc 无 ctx）照跑，未启动槽位静默记 0。
- ⚪ `llm_scorer.go:381` `batchScoreChunk` 调 `s.client.Generate(context.Background(), ...)` 无超时/取消 — 每批无界阻塞 LLM 调用。

### 3.26 internal/ares_flight — OK（collector 用 WaitGroup 跟踪，图遍历有环守卫）

### 3.27 internal/ares_integration — OK（纯测试包）

### 3.28 internal/ares_mcp — 4 issues
- 🟡 `transport_server.go:320` `handleSSEConnect` defer 排空 `for range msgCh`，但 session 已从 `sessions` 删除 → `Close()` 永不关此 channel — 每个断连的 SSE 客户端卡死一个 goroutine。
- 🟡 `client.go:316` `receiveLoop` 每个 notification 起一个 `eg.Go`；`Close()` 的 `eg.Wait()` 可被 30s 调用超时拖住。
- 🟡 `factory.go:96-130` `MCPToolFactory.Create` 成功路径从不 `Close` MCPClient，`MCPTool.Close()` 是 no-op — 每个工厂创建的 tool 泄漏子进程/SSE 连接 + receive goroutine。
- 🟡 `manager.go:171-173` `ConnectServer` 无重复守卫 — 同名二次连接覆盖 `m.clients[name]` 而不关首个连接（泄漏连接/tool）。

### 3.29 internal/ares_memory/context — 1 issue
- ⚪ `task.go:146-147` `TaskMemory.Get` 返回内部 `*TaskData` 指针（无防御拷贝），调用方可改共享状态；`UserMemory.Get`（user.go:75）同模式。

### 3.30 internal/ares_memory/distillation — 1 issue
- 🟡 `distiller.go:585-593` KeepBoth 冲突路径构建的 `oldMemory` 只有 `Metadata["solution"]`（缺 problem 及其余键）— 下游 `embedOneMemory`/`convertMemoryToExperience` 丢失问题文本并重嵌错误向量（" → solution"）。

### 3.31 internal/ares_memory/embedding — OK

### 3.32 internal/ares_memory/experienceadapters — 1 issue
- ⚪ `adapters.go:332-333` `ToStorageExperience` 恒把 `CreatedAt/UpdatedAt` 打成 `time.Now()` — `Update` 会重置原始创建时间。

### 3.33 internal/ares_memory/push — OK

### 3.34 internal/ares_memory/report — OK

### 3.35 internal/ares_observability — OK（NoopTracer 是有意 no-op；Prometheus/OTel/metrics/cost 完整）

### 3.36 internal/ares_protocol — OK（无非测试 Go 文件，内容在 ahp/）

### 3.37 internal/ares_protocol/ahp — 3 issues
- 🔴 `dlq.go:141` `RemoveBySession` 无 nil 检查解引用 `entry.Message.SessionID`（`GetBySession` 在 :81 有检查）— 对 nil message 条目 panic。
- 🟡 `dlq.go:209` `StartAutoRetry` 内部调 `g.Wait()` 阻塞调用者 — 违反其 "background loop" 契约。
- 🟡 `dlq.go:178` `Process` 从不检查 `ctx` — 全量 DLQ 迭代在关闭期间不可被取消打断。

### 3.38 internal/ares_ratelimit — 2 issues
- 🔴 `semaphore.go:198` `WeightedSemaphoreLimiter.Allow` 减 `available` 但从不记 `weighted[key]` → 后续 `Release(key, weight)` 是 no-op — 已获得容量永久丢失。
- ⚪ `sliding_window.go:49` `Rate==0`（maxRequests=0）时 `Wait` 每 100ms 轮询自旋而不是快速失败。

### 3.39 internal/ares_runtime — 4 issues
- 🟡 `bus.go:317` `invokeWithTimeout` 超时后泄漏 spawn 的 goroutine（插件 fn 忽略 ctx 会跑过超时）。
- 🟡 `manager_lifecycle.go:171` `Stop()` 的 `m.g.Wait()` 无界 — 若某 agent 的 `Start` 忽略 ctx 取消则永久挂起（无总 deadline）。
- 🟡 `manager_lifecycle.go:126-140` 最终快照 `store.Save(...)` 在持有 `m.mu` 写锁时执行 — 全局锁下做阻塞 I/O，阻塞健康检查/注册。
- 🟡 `router_memory.go:78` `BeforeStep` 启动裸 `go func` 预取，无 errgroup/WaitGroup 跟踪 — 慢 `AdviseRoute` 调用逐步骤堆积。

### 3.40 internal/ares_security — 1 issue
- ⚪ `sanitizer.go:327,347` `maskPhone`/`maskCreditCard` 在 `maskLen==0`（长度等于保留前缀+后缀）时返回未脱敏完整号码 — 脱敏绕过（当前检测正则不可达，潜在）。

### 3.41 internal/ares_shutdown — 3 issues
- 🟡 `manager.go:132` `StartShutdown` 在首个阶段错误/超时即返回 — `PhaseForce`/`PhaseDone` 在 Graceful 超时后永不执行，强制清理路径是死的。
- 🟡 `callbacks.go:258` `ExecuteParallel` 在 ctx 取消后仍 `<-done` 等待 — 忽略 ctx 的回调永久阻塞（goroutine 泄漏）。
- ⚪ `signal.go:110` `handleSignal` 用 `context.Background()` 而非 handler 的 ctx，并在信号 goroutine 内同步跑完整 shutdown。

### 3.42 internal/cmdutil — 不存在（仅 docs 提及，计划中）

### 3.43 internal/core（根）— 无 Go 文件（仅 errors/ 与 models/ 子包）

### 3.44 internal/core/errors — 3 issues
- 🟡 `handler.go:63` `HandleError` 读 `appErr.Context` 不持 `AppError.mu` — 与并发 `WithContext` 写竞争（类型文档自认实例跨 goroutine 共享）。
- 🟡 `strategy_config.go:149-154` `LoadStrategiesFromConfig` 逐个写入全局 registry 后中途返回校验错误 — registry 半更新（无回滚/原子性）。
- 🟡 `strategy_config.go:116` 路径穿越守卫仅当调过 `SetAllowedDir` 才生效；默认 `allowedDir==""` 时静默允许从任意路径加载策略配置。

### 3.45 internal/core/models — OK（nil map 均有写入方先初始化，nil 守卫齐全）

### 3.46 internal/dashboard — 1 issue
- ⚪ `watcher.go:54-70` `AgentWatcher.Start` 启动 `go w.pushLoop(ctx)` 无跟踪（无 WaitGroup/handle），关闭时不可 join；无 stop 守卫（双 Start = 双循环）。

### 3.47 internal/detector — OK（nil ctx 守卫、逐调用探测超时、best-effort close、测试充分）

### 3.48 internal/discovery — 4 issues
- 🟡 `engine.go:210` `StartAutoDiscovery` 启动裸 `go func`（无 errgroup/WaitGroup）— 调用方无法等待关闭或观察完成。
- 🟡 `store.go:23-31` `MemoryStore.Save` 对 nil/空 ID service 静默返回 nil — 丢弃保存与成功无法区分。
- 🟡 `identity.go:180-187` `normalizeEndpoint` 把含空格的非常规名称压成同一 key（"foo bar"/"foo baz" → `foo`）— 不同服务被合并。
- ⚪ `engine.go:159-202` 健康检查在生产唯一接线中是死的：`ares_bootstrap/provide_discovery.go:51` 传 `nil` HealthChecker → 自动发现循环里 `CheckHealth` 静默 no-op，`MCPHealthChecker` 仅测试使用。

### 3.49 internal/discovery/providers — 2 issues
- ⚪ `binary.go:83-86` 每个 goroutine 共享一个 3s `probeCtx` 跑最多 4 次串行子进程探测（--help/help/--version/version）— 慢 `--help` 饿死后续探测。
- ⚪ `binary.go:166-172` 已判定非 MCP 的 binary 仍探测 --version/version — 每周期最多 ~36 次子进程 spawn（PATH 9 个 binary）。

### 3.50 internal/errors — 3 issues
- ⚪ `wrap.go:210` `Wrapf` 用 `append(args, err)` — 当 `args...` 从有余量的切片展开时改写调用方底层数组。
- ⚪ `wrap.go:220-227` `FormatError` 含多个 `%w` 时误格式化（ReplaceAll 全替换，但只追加一个 `err.Error()`）。
- ⚪ `wrap.go:215` `FormatError` 是死代码 — 全仓库无调用点。

### 3.51 internal/evidence — OK（详见前序 GA/AKG 深度 review）

### 3.52 internal/evolution/coordinator — OK（Evaluate/decide/deployer + 回退路径完整，重试预算耗尽 → 可观测 `DecisionDrop`）

### 3.53 internal/evolution/deployment — OK（Canary staging→live 管线，带 rollback，默认禁用）

### 3.54 internal/evolution/diff — OK（五个 differ 全部实现，无 stub）

### 3.55 internal/evolution/genome — OK（所有基因组实现 Mutate/Crossover/Fitness/Snapshot；注：`RecoveryGenome.clone()` 拷贝 `*g.policy` — policy 为 nil 时 panic，但所有构造器传非 nil）

### 3.56 internal/evolution/patch — 1 issue
- 🟡 `patch.go:179,253,317` `Registry.executors` / `Registry.applied` map 无锁读写 — coordinator/deployment 并发 `Apply`/`ApplySet` 与 `Register`/`Replace` 并存 → 数据竞争 / applied 去重撕裂。

### 3.57 internal/knowledge（根）— OK（dedup/hybrid/quality/relation/pipeline 完整；ProcessStream 用 errgroup + ctx 可取消发送）

### 3.58 internal/knowledge/adapter — OK（context_retriever / distill bridge / evolution/memory 转换器完整）

### 3.59 internal/knowledge/compiler — OK

### 3.60 internal/knowledge/linker — OK

### 3.61 internal/knowledge/mcp — 2 issues
- ⚪ `mcp.go:315` `handleQueryKnowledge` 解析 `Types`/`Tags` 但从不使用 — query_knowledge 实际不过滤。
- ⚪ `mcp.go:161` `s.Runtime.Execute` 无 nil 守卫，而 `NewAKFServiceWithStore` 文档声明 Runtime "may be nil" — 此类 service 上调用 build_graph/query_knowledge 即 nil 解引用。

### 3.62 internal/knowledge/pipeline — OK

### 3.63 internal/knowledge/planner — OK

### 3.64 internal/knowledge/provider — OK

### 3.65 internal/knowledge/provider/code — OK

### 3.66 internal/knowledge/provider/evolution — OK

### 3.67 internal/knowledge/provider/memory — OK

### 3.68 internal/knowledge/provider/mysql — 2 issues
- 🟡 `provider.go:148,122` 流可能死锁在第二个错误：scan 错误与 deferred `rows.Close` 错误都发到 cap-1 `errCh`，而 runtime `loadAndProcess` 只在 `objCh` 关闭后才排空 — 第二次错误发送阻塞生产者 → `close(objCh)` 永不执行 → 消费者永久等待。
- ⚪ `provider.go:103` `Stream` 忽略 `intent`（硬编码 `LIMIT 10000` 全扫，`Scope.MaxObjects` 未用）。

### 3.69 internal/knowledge/provider/postgres — 1 issue
- 🟡 `provider.go:146-149` 同款第二错误死锁模式：循环内 `errCh <-` 发到 cap-1 通道，runtime 在 `objCh` 关闭前从不读 — 多行 scan 失败挂起流。

### 3.70 internal/knowledge/provider/store — OK

### 3.71 internal/knowledge/provider/vector — OK

### 3.72 internal/knowledge/retriever — OK

### 3.73 internal/knowledge/service — 2 issues
- 🟡 `adapter.go:82-87` `Query` 是 no-op stub — 恒返回 `(nil, nil)`，静默丢弃 Query.Types/Limit/Tags。
- ⚪ `adapter.go:94-108` `Distill` 是 stub 无管线；`distilled-%d`(len(raw)) ID 对等长输入碰撞。

### 3.74 internal/knowledge/store（接口）— OK

### 3.75 internal/knowledge/store/memory — OK（详见前序 AKG 深度 review）

### 3.76 internal/knowledge/store/mysql — OK

### 3.77 internal/knowledge/store/postgres — OK

### 3.78 internal/knowledge/store/sqlite — OK

### 3.79 internal/knowledge/workflow — 1 issue
- ⚪ `workflow.go:76-81` `Process` 忽略 `json.Unmarshal` 错误 — 畸形 step 输入静默回退默认配置。

### 3.80 internal/llmservice — OK（failover/chat routing/embedding fallback 完整，token 估算诚实）

### 3.81 internal/monitoring — 1 issue
- ⚪ `pruner.go:102-115` `Pruner.Start()` 无幂等守卫（双 Start 双循环，只存最后 cancel → goroutine 泄漏）；`Stop()` 不等循环退出。

### 3.82 internal/monitoring/adapter — 1 issue
- ⚪ `intel_adapter.go:19-35` `AnomalyCount/InsightCount/SystemLevel/AgentLevel` 无 nil 守卫解引用 `a.engine`（`NewIntelAdapter(nil)` 即挂）。

### 3.83 internal/monitoring/dag — OK（Engine + interaction engine 完整，DAG 操作持锁且环/依赖错误回滚）

### 3.84 internal/monitoring/data — OK（AgentTracker/TraceLinker/CostAggregator 完整且锁正确）

### 3.85 internal/monitoring/eventutil — 1 issue
- ⚪ `eventutil.go:159-163` `ExtractAgentID(nil)` 会 panic：`ExtractString` 返回 "" 后仍解引用 `evt.StreamID`（当前调用方都 nil 守卫，但 helper 是潜伏陷阱）。

### 3.86 internal/monitoring/tabs — OK（arena/event/evolution/llm/mcp/memory/workflow tab 全真实实现，有 cap 和 Trim）

### 3.87 internal/scoreutil — OK

### 3.88 internal/workflow/engine — 1 issue
- 🟡 `registry.go:200-204` `OutputStore.Close()` 置 `outputs=nil`；之后 `Set()` 对 nil map 写 → panic，与文档注释 "Get/Set calls will return zero values safely" 矛盾。

### 3.89 internal/workflow/graph — OK（GraphPatchExecutor 与 placeholder 节点语义诚实，可观测 `PlaceholderResult` 而非静默 no-op）

---

## 4. sdk/ 层

### 4.1 sdk — 4 issues
- 🔴 `options.go:18-73, sdk.go:344` `WithConfig`/`WithConfigFromEnv` 返回 `ConfigOption`（与 `Option` 不同的具名类型），不可赋给 `New(...Option)`/`NewRuntime(...Option)` — 文档宣传的 `sdk.NewRuntime(sdk.WithConfigFromEnv())` 不编译；死 API（从未被调用）。
- 🟡 `evolve.go:228-242` `applyEvolvedParams` 只应用 `tool_selector`；其余 4 个演化维度（search_depth/scheduler_strategy/memory_threshold/recovery_strategy）日志 "TODO ... not wired to Agent field yet" — 宣传的演化维度从未接线（闭环缺口）。
- 🟡 `sdk.go:604-649, 569-596` `New()` 错误路径泄漏资源：`wireMemory`/`wireKnowledge`/`wireMCPClients` 失败时已成功的 `llmSvc` 从不 Close；已连接的 MCP client 在后续连接失败时也不关。
- 🟡 `sdk.go:225-232` `memStrategyStore.GetHistory` 只守卫上界（`n > len(history)`）；负数 `n` 在 `s.history[:n]` panic。

---

## 5. examples/ 层

- examples/01-quickstart — OK
- examples/02-tool-calling — OK
- examples/03-dag-workflow — OK
- examples/04-multi-agent — OK
- examples/05-evolution-demo — OK
- examples/06-chaos-resilience — OK
- examples/07-human-in-loop — OK
- examples/08-mcp-integration — OK
- examples/09-full-app — OK
- examples/10-ga-full-evolution — 2 issues：🟡 `main.go:282-292` `confidenceForStrategy` 把 `tool_selector` 参数值（"auto"/"manual"/"priority"）与 hint `tool` 名（"search"/"read"/"calculate"/"exec"）比较 — 永不相等，记忆引导特性恒返回 0（死逻辑）；⚪ `main.go:328-331` `init()` 丢弃创建的 `rand.New(...)`，显式播种是 no-op（Go≥1.20 自动播种）。
- examples/11-knowledge-import — 2 issues：🟡 `main.go:666-667` `impTool` 全块维度检查失败（无错跳过嵌入）时 `lastErr` 保持 nil → 令人困惑的 `no chunks embedded: %!v(<nil>)`；⚪ `main.go:153-158` `dispatch` 裸 `go func` 起 ArenaHTTPServer（无 errgroup），`registerTools`（main.go:711）忽略所有 Register 错误。
- examples/12-yaml-driven-flags — OK
- examples/13-archive-akg-chain — 1 issue：⚪ `main.go:45` 忽略 `os.ReadFile` 错误 — 不可读记录被当空内容处理。
- examples/14-tool-discovery — OK
- examples/21-ai-assistant-integration — OK（stub runtime `New(nil,...)` 与失败 BuildGraph 是文档化/有意为之）
- examples/22-evolution-blocks — 1 issue：🟡 `main.go:135` `best`（`BestStrategy()`）可能为 nil — 前面打印有守卫，但 `promoter.Evaluate(ctx, best.ID, ...)` 无条件解引用 → 潜在 nil panic。
- examples/custom-store — OK
- examples/discovery — OK
- examples/eval — OK
- examples/external-tools — OK
- examples/graph_demo — OK
- examples/knowledge-fabric — 1 issue：⚪ `main.go:208-219` `memoryProvider.Stream` 裸 `go func` 发送对象，无消费者取消（读者停止则泄漏），且从不发 `errCh`。
- examples/mcp-registry — OK
- examples/arena — 仅 YAML 无 Go。
- plugins/ — 不存在。

---

## 6. 🔴 级问题汇总（优先处理）

| # | 位置 | 问题 |
|---|------|------|
| 1 | `api/evolution/evolution.go:100-109` | `NewDreamCycle` 裸类型断言 + 丢弃 opts → 类型错即 panic，GA/tester 永远配不起来 |
| 2 | `api/handler/agent.go:13` | 9/11 handler 从未接线 — 死端点 |
| 3 | `api/router/router.go:51,70,91` | 恒 401 且无法关鉴权 + 5 组注册函数生产不调用 + nil receiver 路由 |
| 4 | `api/mcp/mcp.go:162-174` | stdio notification 走 roundTrip 阻塞 30s 再断连 |
| 5 | `cmd/ares/db_create_table.go:102` | 非法 PG 语法 `CREATE POLICY IF NOT EXISTS` — RLS 实际未建却报成功 |
| 6 | `cmd/create_distilled_table/main.go:93` | 同上 RLS bug |
| 7 | `cmd/monitor-live/main.go:72-76,229` | Ctrl+C 永不退出（需 kill -9） |
| 8 | `internal/ares_protocol/ahp/dlq.go:141` | `RemoveBySession` nil 解引用 panic |
| 9 | `internal/ares_ratelimit/semaphore.go:198` | `Allow`/`Release` 容量永久丢失 |
| 10 | `sdk/options.go:18-73` | `WithConfigFromEnv` 与 `Option` 类型不兼容 — 宣传入口不编译 |

## 7. 结论

- **全部模块已逐一审查**（api 34 包 / cmd 11 / internal 80+ / sdk / examples），无遗漏。
- **无 🔴 的模块**：绝大多数 internal 子系统（ares_bootstrap、ares_flight、ares_events、ares_eval、evolution/*、knowledge/* 核心、workflow/*、monitoring/* 等）实现完整、errgroup 纪律好、占位/兜底均有文档或可观测语义。
- **最值得优先修**：上表 10 个 🔴（多集中在"宣传入口不可用/未接线"与"资源/容量泄漏"两类）；其次是 🟡 中的几个高影响项：`evolution/patch.Registry` 无锁 map（并发竞态）、MySQL/PG provider 的 errCh 第二错误死锁、`knowledge/service.Query` no-op stub、`ares_mcp` 工厂泄漏、leader/sub 心跳与 checkpoint 未接线。
- **后续建议**：按 🔴 → 高影响 🟡 的顺序分批修复；每个修复遵循 `plan/rules/code_rules.md`（errgroup、不忽略 error、注释英文、补测试）。

