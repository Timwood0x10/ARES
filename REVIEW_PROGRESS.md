# ares 框架深度 Review - 进度与发现

> 目标：逐模块深度 review，找出潜在 bug 与"没有闭环 / open loop"的位置。
> 模块：`github.com/Timwood0x10/ares`，目录 `/Users/scc/go/src/goagent`，分支 `dev`。
> 规模：约 1419 Go 文件，50+ 内部包，24772 图节点。

## 收口结论（2026-08-25，2026-08-25 二次核实已更新状态）

> **二次核实（本轮）**：逐条 grep 复核了 #7-#14 的生产接线现状，发现 **#8、#9 代码里其实已经修复**（先前记录过期），**#7 为部分修复**。下表已同步为最新真实状态。

**已修复缺陷（含二次核实新确认的接线）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 1 | `kernelscheduler` 缺 `waitFor` helper + 两处并发测试竞态 | 中 | ✅ 已修（补 helper + 轮询同步，15×+race 绿） |
| 2 | `taskfabric.record()` 持 `f.mu` 做 `store.Append` I/O | 中 | ✅ 已修（`recordLocked`/`flushAppends` 拆分，I/O 移出锁） |
| 3 | `EvolutionScheduler` 订阅错配（EventAgentEnd 无生产者） | 中 | ✅ 已修（改订阅 `ares_events.EventAgentStopped`） |
| 4 | `recordGenealogy` 的 `s.lineages` append 无界增长 | 低 | ✅ 已修（套用 `maxLineages` cap） |
| 5 | README 使用不存在的 `sdk.WithYAMLFile` API | 中 | ✅ 已修（全仓库改 `sdk.WithConfig`，6 处文档） |
| 6 | README benchmark 数字过期 + 引用已删除 bench | 低 | ✅ 已修（M3 Max 重跑全量，同步 README/README_CN） |
| 8 | `EvolutionScheduler.RecordScore` 生产无调用方 | 低 | ✅ **已修（二次核实确认）**：`scheduler.go:366-369` 订阅 `EventTaskCompleted/Failed` → `RecordScore(taskScoreSuccess/Failure)`；`scheduler.Register()` 在 `provide_evolution.go:79`（生产）与 `genome_wiring_system.go:654` 调用。生产已有分数来源。 |
| 9 | `EvolutionProvider` 仅 SDK 注册，服务端知识图缺演化上下文 | 中 | ✅ **已修（二次核实确认）**：`attachEvolutionKnowledgeProvider`（`bootstrap_steps.go:164`）在 `wireGAEvolution`（`bootstrap.go:479`，serve 路径）里把 `evoprovider.New("evolution", store)` 注册进 `comp.KnowledgeRuntime`。 |
| 10 | 服务发现引擎被启动但零消费 | 低 | ✅ **已修（二次核实确认，commit a4e4c147）**：`provide_discovery.go:66-70` `forwardDiscoveryEvent` 把 discovery 事件（added/removed/updated/health）转发进共享 `EventStore`（注释 "REVIEW #10: previously the engine ran with zero consumers"），不再是写入无人读取的内存 store。**注**：`EventDiscovery*` 目前只进 EventStore 供 timeline/审计消费，尚无业务侧订阅者做"发现→MCP 自动注册"（若需该闭环仍待接）。 |

**部分修复：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 7 | 存储过期/衰减清理未接线 | 中 | 🟡 **部分已修**：`maintenance_worker.go` 的 `startExpiryCleanupWorker`（`bootstrap.go:297`）已接线，每小时 purge。**但只注册了 `experiences_1024`**（`bootstrap_steps.go:53`）；`ConversationRepository`/`KnowledgeRepository`/`SecretRepository`/`SessionRepository` 的 `CleanupExpired` + distilled_memories 仍**零注册**（各表 `expires_at`/`decay_at` 仍不清理）。 |
| 11 | SKILLS 渐进披露 + 工具能力搜索 | 中 | 🟡 **部分已修（commit a4e4c147）**：`skills_wiring.go` 的 `wireSkills`（`bootstrap.go:265`，serve 路径）已构造 `ares_skills.NewCatalog` + `SeedRegistry` + `SetSkillsRegistry`（注释 "REVIEW #11 closure"），memory manager 的 "Available skills" 渐进披露块在 serve 已生效。**但 `envcap.NewSearcher`（工具能力搜索，`internal/tools/envcap`）仍零生产调用**——tool capability search 那半仍未接。 |

**仍开放（未修，装配/注册层）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 12 | "演化内核"适配器未接入 serve 装配（`EvolutionAwareSpawner`、`NewChaos`、`NewPopulationAdapter`+`RunKernelEvolutionLoop`、`NewEvolutionAwareQuotaManager` 零生产构造；`NewSpawnPolicySource`/`evolutionSpawnPolicySource` 已定义但无消费方——因唯一消费者 `EvolutionAwareSpawner` 从不构造） | 中 | ⚠️ 已记录，未修 |
| 13 | 异步 embedding 队列子系统全链路无消费者（`EmbeddingQueue.FetchPendingTasks`/`MarkCompleted`/`MarkFailed`/`Reconcile` 零生产调用，无 backfill worker；且唯一生产者 `ProductionMemoryManager`+`WriteBuffer` 仅测试构造，serve 走 `memoryManager` 同步 `expRepo.Create`） | 中 | ⚠️ 已记录，未修 |
| 14 | 监控 DAG 节点图在 serve 下无界增长（`Pruner` 仅在 `WithPruneConfig` 时启动，serve/demo 均不传；`dag.Engine.nodes` map 每 agent/task 一节点、`AddNode` 无 cap、仅 Pruner 会 RemoveNode） | 中 | ⚠️ 已记录，未修（**规划并入 0.3.1 runtime 检测面板重设计，见 `plan/0.3.1_monitoring.md`**） |

**贯穿性结论**：全部 14 项 = **9 已修（#1-#6、#8、#9、#10）+ 2 部分修（#7、#11）+ 3 未修（#12、#13、#14）**。仍开放/部分开放的项清一色是"装配/注册层"问题——组件被实现、被测、甚至被引用，但缺少一个逻辑上的生产消费/构造方。代码库在生命周期/循环/goroutine/事件消费层**极其规整**（errgroup/WaitGroup/ctx.Done/ticker.Stop/panic-recover 全覆盖），在纯数据流与纯类型层也干净（compiler/pipeline/provider/knowledge/eval/protocol 等）。真正残留的缺陷集中在: register-but-never-consume / start-but-never-read / adapter-never-constructed。

**注（轻微，未单列）**：legacy `comp.Evolution.EvaluatorRegistry`(llm_judge) 在 provide_evolution.go 创建但无下游 `Get/Evaluate` 消费（仅 NEW `Coordinator.Evaluate` 生效）；ares_memory `BuildPromptMessages` 重复调 `snapshotTuning()`(L625/L660) 无害；knowledge store 构造器的 `context.Background()` 仅用于迁移 `initTables` 无泄漏。**收尾轮补充的轻微项**：`retrieval_embedding.go` `getEmbeddingCached`(L66-70) 命中不刷新 access list → 实际 FIFO 非真 LRU（bounded 1000，非泄漏）；`ahp/queue.go` `IsFull`(L190) 不计 backupBuffer 而 `Available`(L199) 计（无害，`SendMessage` 刻意不用 `IsFull` 避免 TOCTOU）；`retrieval_search.go:790` `var _ = strings.ToLower` 抑制未用 import 的 dummy（风格）；heartbeat（`ahp.NewProtocol`/`NewHeartbeatMonitor`/`NewHeartbeatSender` + `sub.heartbeatSender`）零生产构造，属 peer-mode 刻意未接线（`peer_agents.go:52`/`peer_mode.go:299` 显式传 nil），归 register-but-never-wired 同族，非缺陷。

门禁：`go build ./...` 0 / `go vet ./...` 0 / `gofmt` 干净 / `go test ./...` 143 包全绿 / 改动包 `-race` 绿。

---


## 基础验证（全部通过）

- [x] `go build ./...` 通过
- [x] `go vet ./...` 通过（早期首跑 `scheduler_failure_attribution_test.go:85 undefined: waitFor` 并非瞬态，是真缺陷：`waitFor` helper 缺失 + 同包两处并发竞态。**已修复**：补 `waitFor` 轮询 helper，failure-attribution 与 smoke 的 `Scheduled` 断言改为轮询 scheduler 侧副作用，连跑 15 次 + race 全绿）
- [x] `golangci-lint run ./...` → 0 issues
- [x] `staticcheck ./...` → clean
- [x] `go test -count=1 -timeout 50s ./...` → 143 包全部 ok
- [x] `-race` 通过：ares_runtime, kernelscheduler, taskfabric, ares_events, ares_bootstrap, discovery, ares_mcp

## 已完成模块

### kernelscheduler（扎实）
- `scheduler.go`：动态 executor 注册、预emption、panic 恢复、lease 续租、fencing token 均正确。
- `load_tracker.go`：mutex 保护，线程安全。轻微：未对 confidence 值做 clamp（低风险）。

### taskfabric（已闭环）
- lease / scheduler / quantum 均正确。
- ~~`fabric.go` 的 `record()`：持有 `f.mu` 时同步调用 `f.store.Append(...)` —— 持全局锁做 I/O~~ **[已修复 2026-08-25]**：拆分为 `recordLocked`（锁内只更内存事件日志 + 构造待写 `pendingAppend`，无 I/O）+ `flushAppends`（解锁后执行 `store.Append`）。所有 11 个变更方法用 `defer f.flushAppends(&pending)` 先于 `defer f.mu.Unlock()` 注册，LIFO 保证先解锁再刷盘——store I/O 不再阻塞 fabric 的 CAS/状态机锁，W3 durability 日志契约保持不变。race + 全量测试绿。

### ares_events（safe）
- `memory_store.go`：Close/unsubscribe 无 double-close 竞态。

### ares_ratelimit（可接受）
- `sliding_window.go`、`token_bucket.go`：无明显 bug。

### ares_config（正确）
- `store.go` debounce 热更新循环正确。

## 已确认的"未闭环"（关键发现）

### W4 演化反馈回路 - 已闭合
- `cmd/ares/peer_mode.go:143-144`：`NewEvolutionFeedbackAdapter(attribution, tracker)` + `go RunEvolutionFeedbackLoop(...)`。
- `cmd/ares/scheduler_compat.go`：type alias 保证与 scheduler 共用同一 `kernelscheduler.LoadTracker`，无断环。

### ★★ 演化调度器事件通道错配 — 已修复（2026-08-25）
- **原现象**：`EvolutionScheduler` 订阅 `ares_callbacks.EventAgentEnd` 触发进化，但该回调从不 emit → 事件驱动进化死路。
- **现状（代码已修）**：`internal/ares_evolution/scheduler.go:325-327` 现订阅 `ares_events.EventFilter{Types: []ares_events.EventType{ares_events.EventAgentStopped}}` —— 即 agent 生命周期真正发信号的频道。`Register()` 在受管 goroutine 里消费该订阅通道并调 `OnAgentEnd`，Shutdown 取消 context 时 EventStore 关闭通道退出。`provide_evolution.go:78` 传入的是 `eventStore`（EventStore），不再是 callbacks Registry。`scheduler_test.go:191` / `genome_wiring_integration_test.go:338` 钉住"订阅类型必须是 EventAgentStopped"。
- **结论**：事件驱动进化已闭环，监听端与生产端（`agent.go:253`、`manager.go:279` 等 `emitEvent(EventAgentStopped)`）现已对齐。REVIEW_PROGRESS 早期记录（订阅 EventAgentEnd）已过期。
- `callback_bridge.go:89-92` 仍保留 callbacks→EventStore 的 `EventAgentEnd→EventAgentStopped` 映射，作为 callbacks 侧未来 emit agent.* 时的桥接，非死代码（有 integration/bridge 测试覆盖）。

### 演化实际触发路径（已确认）
- `internal/ares_evolution/service/service.go:414-420` `RunIdleEvolution`：每次 `CreateWiredSystem` + run。
- `service.go:456`：wiredSystem 存在时每代调 `evolution.RunIdleEvolution(ctx, s.wiredSystem, 1)`。
- 分支 2（`service.go:468`，`s.population` 非空）：走 `EvolveOnIdle` + `initScores` + `recordGenealogy` + `recordLineages`，产物记录到 `s.lineages`。
- **已确认**：生产 GA 主回路实际由 `bootstrap_steps.go:204-227` 的 5min ticker 直接调 `popAdapter.Run(ctx)` 驱动，`Service.RunIdleEvolution` 是并行入口，两条都不断环。
- **已修复 2026-08-25**：`s.lineages` 无界增长——`recordLineages`（L922）本已有 `maxLineages`(1000) cap；`recordGenealogy`（L824）那条 append 之前无 cap，现已补同一 cap（每代最多加一条，超限 trim 最旧）。`recordLineages`（每子代一条）与 `recordGenealogy`（每代 best 变化时一条）记录维度不同，非重复。

### knowledge（runtime / retriever / provider 已查）
- `runtime.go` Execute：Plan→Load→Link→Reduce→Graph 流程完整；并发控制 errgroup+SetLimit；context 取消正确；goroutine 泄漏修复（`case <-ctx.Done()` 时 drain `for range objCh`，L264）。
- `store.go` 明确为可选持久层（Provider→Pipeline→Runtime 绕过 Store），无强制依赖。
- `retriever/retriever.go`：干净。Query 校验、预算计算（graph 60%）、`Types` 过滤已在 reduce 后正确应用（注释标明早期曾"Types 被静默忽略"，现已修）。`filterByTypes` 正确保留端点都存活的两端边。
- `provider/evolution/provider.go` + `adapter/evolution.go`：结构优秀——errgroup 保证流可 ctx 取消、active 与其历史去重（跳过与 active 同 version）、`scoreToConfidence` sigmoid 映射并 clamp 到 [0.1,0.99]、`IntentMatch` 对 decision/evolution/why/optimize 打 0.9。无 bug。

### ✅ #9 演化决策 Provider 服务端知识图接线 — 已修（2026-08-25 二次核实）
- **原现象**：`EvolutionProvider`（`provider/evolution`）曾只在 SDK 路径注册。
- **现状（代码已修）**：`attachEvolutionKnowledgeProvider`（`bootstrap_steps.go:350`）`evoprovider.New("evolution", store)` 注册进 `comp.KnowledgeRuntime`，由 `wireGAEvolution`（`bootstrap_steps.go:164`）调用，而 `wireGAEvolution` 在 serve 路径 `Bootstrap`（`bootstrap.go:479`）中执行。注释明确标注 "Close the evolution context in the knowledge graph loop (#9)"。
- **结论**：服务端知识运行时已注册 evolution provider，演化 strategy 可被"演化上下文"查询检索到。先前"仅 SDK 注册"记录已过期。

### ares_memory（已查：manager_impl / production_manager / production_manager_tasks）
- `manager_impl.go`：结构清晰，`SetSkillsRegistry/SetLeaseManager/AcquireSessionLease/ReleaseSessionLease` 均 `RLock/Lock` 保护，线程安全。lease 管理器共享，正确。
- distillation 管道（pipeline.go）闭环：Distiller→ReportGenerator→PushService 一条线；`PushAfterDistill` 默认 true；部分失败仅 warn 不中断。
- `production_manager.go`：CreateSession/AddMessage/GetMessages/BuildContext/BuildPromptMessages 均先 `tenantGuard.SetTenantContext`，正确。`sessionCache` LRU 逐出（按 UpdatedAt，O(n) 扫描，无 bug）。轻微冗余：`BuildPromptMessages` 调两次 `snapshotTuning()`（L625/L660）+ max-history 截断在 repo（GetBySession 传 maxHist）与 L661 各做一次，无害。
- `production_manager_tasks.go`：**蒸馏写→读闭环成立**。写路径 `StoreDistilledTask` 用 `memembed.BuildMemoryExperienceSpec(...)` 组装 spec，`WriteItem{Table:"experiences_1024", SpecKind/Prefix/Hash}` 入 writeBuffer；读路径 `SearchSimilarTasks` 用 `retrievalService.Search(SearchExperience=true)` 查同一 `experiences_1024` 表。`write_buffer.go:322` INSERT 到 `experiences_1024`、`flushBatch:358` 建 `make([]float64,1024)` 占位 + 入 embedding_queue 异步回填，读侧 `retrieval_search.go` 取已回填向量。同一张表、同一维度(1024)、同一租户 → 无断环。`SpecDim:0` 仅写进 metadata `embedding_dim` 供 trace，不影响实际维度（embedding client/model 决定）。
- `memory_patcher.go`：内存补丁 executor 有 `Apply` 写配置，配合 `MemoryConfigStore`（Lock/Unlock/GetConfig）读一致性（`snapshotTuning` 已用 RLock），线程安全。

### ✅ #10 服务发现引擎事件转发 — 已修（2026-08-25 二次核实，commit a4e4c147）
- **原现象**：`ProvideDiscovery` 创建的 engine 把检测结果写入无人读取的内存 store。
- **现状（代码已修）**：`provide_discovery.go:66-70` 在 `eventStore != nil` 时 `eng.AddHandler`，`forwardDiscoveryEvent`（:83）把每个 discovery 事件（added/removed/updated/health/cycle）经 `ares_events.Emit` 转发到共享 `EventStore` 的 "discovery" stream。`ProvideDiscovery(ctx, &cfg.Discovery, comp.EventStore)`（`bootstrap.go:488`，serve 路径）传入共享 EventStore。注释明确 "REVIEW #10: previously the engine ran with zero consumers"。
- **残留（轻微，非阻断）**：`EventDiscovery*` 目前只进 EventStore 供 timeline/审计通道消费，**尚无业务侧订阅者**做"发现→MCP 自动注册"的闭环动作。若产品需要自动注册发现的 MCP server，仍需接一个订阅消费者。默认 `cfg.Discovery.Enabled=false`。
- **结论**：核心 open loop（发现即丢弃）已闭合；"自动注册"是可选增强，非缺陷。

### 🟡 #11 SKILLS 渐进披露 + 工具能力搜索 — 部分已修（2026-08-25 二次核实，commit a4e4c147）
- **已修部分**：`skills_wiring.go` 的 `wireSkills`（`bootstrap.go:265`，serve 路径）构造 `ares_skills.NewCatalog(...)` + `catalog.Build()` + `skills.NewRegistry()` + `catalog.SeedRegistry(reg)` + `setter.SetSkillsRegistry(reg)`（type-assert `skillsRegistrySetter`）。memory manager 的 "Available skills" 渐进披露块在 serve 已被填充。git blame commit a4e4c147，注释 "REVIEW #11 closure"。Catalog Close 已挂 cleanup。
- **仍缺**：`internal/tools/envcap`（`NewSearcher(tools, skills.Registry, cmds *discovery.Discoverer)`，工具能力检索）**全仓库零生产调用**——SKILLS 的"tool capability search"那半仍未接线。
- **待办**：若需要 agent 侧工具能力搜索，在 serve 装配构造 `envcap.NewSearcher(...)` 并接入工具选择路径。
- **状态**：部分修复（progressive disclosure 已生效；envcap 工具能力搜索仍空转）。

### ★★ #12 "演化内核"适配器未接入 serve 装配 — M2 策略不生效（已记录，未修）
- **现象**：`internal/aresrecovery` 实现了一组"演化内核"适配器，把活跃演化策略（`StrategyStore`）强制施加到运行时：`EvolutionAwareSpawner`（M2-1 孵化策略：`spawn.enabled`/`spawn.max_concurrent`/`spawn.preferred_capabilities`）、`PopulationAdapter`+`RunKernelEvolutionLoop`（周期性执行群体策略）、`NewChaos`（混沌/恢复）、`NewEvolutionAwareQuotaManager`。**全仓库非测试代码零构造这些适配器**（二次核实 grep 确认：`NewEvolutionAwareSpawner/NewChaos/NewPopulationAdapter/RunKernelEvolutionLoop/NewEvolutionAwareQuotaManager` 均 0 生产引用）。
- **佐证**：`ares_bootstrap/spawn_policy_source.go` 的 `NewSpawnPolicySource`/`evolutionSpawnPolicySource`（把 `StrategyStore`→`SpawnPolicySource`）已定义，但唯一消费者 `EvolutionAwareSpawner.source`（evolution_spawner.go:55/68/113）从不被构造 → 该适配器在 serve 下是死代码。二次核实：`NewSpawnPolicySource` 也无任何非测试调用方。
- **对照**：aresrecovery 在 `cmd/ares` 里真正被接的只有——观测面（`EvolutionTracer`/`FeedbackStore`/`GlobalTracer`，bootstrap.go:296-298）、W4 反馈回路（`peer_mode.go:144` `RunEvolutionFeedbackLoop`）、IPCDM 策略（`evolution_ipc.go:89` `NewEvolutionAwareIPC`）、dashboard 变更归因（`dashboard_observability.go:57` `NewChangeAttributor`，只读展示）。列表**缺**孵化器/群体/混沌/配额四个执行侧适配器。
- **后果**：标准 `ares serve` 路径下，演化策略里 `spawn.max_concurrent`、`preferred_capabilities`、群体上限、混沌/恢复策略**不生效**（孵化政策退回普通 spawn；群体/混沌/配额循环不跑）。仅 W4 反馈回路与 IPC 策略活着。
- **待办**：在 serve（bootstrap）装配中构造 `EvolutionAwareSpawner`+`NewSpawnPolicySource(store)`、`PopulationAdapter`+`RunKernelEvolutionLoop(ctx,...,interval,timeout)`（挂 bgGroup）、暴露 `NewChaos`/配额管理器；确认 M2 是否已进入 serve 阶段。
- **状态**：未修。

### 🟡 #7 存储过期/衰减清理 — 部分已修（2026-08-25 二次核实）
- **已修部分**：`maintenance_worker.go` 的 `startExpiryCleanupWorker`（`bootstrap.go:297`，serve 路径）每小时 purge 已注册的 cleaner；`experiences_1024` 已通过 `bootstrap_steps.go:53` 注册（`ExperienceRepository` 实现 `ExpiryCleaner`）。best-effort、panic-recover、ctx 受管，闭环正确。
- **仍缺**：只注册了 experiences 一张表。以下 `CleanupExpired` 实现**仍零注册**：
  - `ConversationRepository.CleanupExpired`（`conversation_repository.go:355`，`expires_at`=24h）
  - `KnowledgeRepository.CleanupExpired`（`knowledge_repository.go:800`）
  - `SecretRepository.CleanupExpired`（`secret_repository.go:274`）
  - `SessionRepository.CleanupExpired`（`session.go:213`）
  - distilled_memories（无 CleanupExpired 实现）
- **待办**：让上述 repo 也实现/注册 `ExpiryCleaner`，append 进 `comp.ExpiryCleaners`（`bootstrap_steps.go` 参照 experiences 的写法）。
- **状态**：部分修复（框架已就位，只差把其余 4 张表接进去）。

### ✅ #8 EvolutionScheduler.RecordScore 生产接线 — 已修（2026-08-25 二次核实）
- **原现象**：曾认为 `RecordScore` 生产无调用方 → 阈值降级触发不工作。
- **现状（代码已修）**：`scheduler.go:363-370` 的订阅循环处理 `EventTaskCompleted`→`RecordScore(taskScoreSuccess=100.0)`、`EventTaskFailed`→`RecordScore(taskScoreFailure=0.0)`；`scheduler.Register()` 在生产 `provide_evolution.go:79` 与 `genome_wiring_system.go:654` 调用。生产分数来源已存在（task 完成/失败事件）。
- **结论**：`TriggerOnThreshold` 分支现有真实喂分，趋势降级检测可工作。先前"生产无调用方"记录已过期。

## 业务逻辑深读进展（2026-08-25 收尾：剩余内部业务逻辑逐文件深读）

单线程人工深读（子 agent 派发受限：Too Many Requests / token 超限）。本轮结论：

| 模块 | 结论 | 关键佐证 |
|------|------|----------|
| agents/sub | ✅ 干净 | `agent.go`：subAgent 对 nil monitor/queue 全防御；`ExecuteStep`/`finalizeErr` 的输出守卫、事件、action log 均正确。`executor.go`：量子 checkpoint 带 version+TID guard、非幂等工具重试阻断、空 prompt fail-fast、prose 包装、max-round 优雅降级。 |
| ares_protocol/ahp | ✅ 全包干净 | `protocol.go`/`queue.go`/`codec.go`/`message.go`/`dlq.go`/`heartbeat.go` 全读。`CheckTimeouts` 离线标记+回调在锁外执行；HeartbeatSender restart-safe；DLQ 有 processMu 串行化+retry budget（`AddWithMaxRetries` 修复了 `Add` 从不设 budget 的 dead branch）。轻微：`queue.go` `IsFull`(L190) 不计 backupBuffer 而 `Available`(L199) 计——不一致但无害（`SendMessage` 刻意不用 `IsFull` 避免 TOCTOU）。 |
| storage/postgres services | ✅ 干净（含轻微项） | `retrieval_search.go`（L790 `var _ = strings.ToLower` 为抑制未用 import 的 dummy，轻微风格）；`retrieval_service.go`（`SetAllowedSynonymDir` 安全、embedding/query cache 均加锁）；`simple_retrieval_service.go`；`retrieval_helpers.go`（`normalizeEnglishQuery` 的 `replaceAllIgnoreCase` 作用于已小写串，命名略误导但无 bug）。轻微：`retrieval_embedding.go` `getEmbeddingCached`(L66-70) 命中不刷新 access list → 实际 FIFO 淘汰非真 LRU（bounded 1000，非泄漏）。 |

### heartbeat 接线核实 — 归入 #7-#12 同族（刻意未接线，非新缺陷）
- **现象**：`sub.New` 两个生产调用点（`cmd/ares/peer_agents.go:52`、`cmd/ares/peer_mode.go:299`）均显式传 `nil` 给 message queue 与 heartbeat monitor，注释明确 "fabric owns scheduling; no AHP queue loop" / "no Process/Launch lifecycle in peer mode"。
- **确认**：`ahp.NewProtocol`、`ahp.NewHeartbeatMonitor`、`ahp.NewHeartbeatSender`、`sub.heartbeatSender` 均零生产构造/调用（grep 空）。
- **结论**：属**刻意未接线的 peer-mode 设计**，与 #7-#12（register/define but never-wired）同族，**不单列为新缺陷**。

### ★★ #13 异步 embedding 队列子系统全链路无消费者（已记录，未修）
- **现象**：`internal/storage/postgres` 设计了完整的异步 embedding 回填链路——`WriteBuffer`（批量写 `knowledge_chunks_1024`/`experiences_1024`，向量列先占位 `make([]float64,1024)`，`embedding_status='pending'`）→ `EmbeddingQueue.EnqueueTx`（同事务入队）。但**队列的消费侧全链路零生产调用**：
  - `EmbeddingQueue.FetchPendingTasks`（`embedding_queue.go:159`）、`MarkCompleted`（:254）、`MarkFailed`（:281）、`Reconcile`（:354）——**全仓库唯一调用方都在 `internal/ares_integration/storage_test.go`**，非测试代码零调用。
  - `KnowledgeRepository.UpdateEmbedding`/`UpdateEmbeddingStatus`、`ExperienceRepository.UpdateEmbedding` 等回填方法（把 `embedding_status` 置 `completed` + 写真实向量）——**生产零调用**。
  - 全仓库无任何 embedding worker / drain goroutine / cron 消费 `embedding_queue` 表。
- **佐证（生产者侧也未接线）**：唯一往 `WriteBuffer` 写入的是 `ProductionMemoryManager.StoreDistilledTask`（`production_manager_tasks.go:240`），而 `ProductionMemoryManager` **仅在 `internal/ares_integration/*_test.go` 构造**（`NewProductionMemoryManager` 无非测试调用）。serve/bootstrap 实际用的是 `ares_memory.NewMemoryManager`/`NewMemoryManagerWithDistiller` 返回的 `memoryManager`，其 `StoreDistilledTask`（`manager_impl.go:694`）走 `expRepo.Create` **同步**写入（`experience_repository.go` INSERT 时若 `exp.Embedding` 非空则直接写真实向量），**完全不经过 WriteBuffer / EmbeddingQueue**。
- **后果**：整套"写缓冲 + 异步 embedding 队列 + 死信 + Reconcile 幂等回填"子系统（`write_buffer.go`/`embedding_queue.go`，含大量 idempotency/lost-update/DLQ 正确性设计）在生产 serve 路径下**从不被触达**——是一套完成度很高但未接线的并行实现。若未来切到 `ProductionMemoryManager`，则因缺 backfill worker，`knowledge_chunks_1024` 的占位零向量永远 `pending`，而读侧 `KnowledgeRepository.SearchByVector`（`knowledge_repository.go:442`）过滤 `embedding_status='completed'` → 知识向量检索恒空（experiences 侧读路径不过滤 status，会命中零向量→相似度失真）。
- **待办**：明确设计意图——(a) 若异步链路是目标态，需接线 embedding worker（`FetchPendingTasks`→`Embed`→`UpdateEmbedding`+`MarkCompleted`/`MarkFailed`，挂 bgGroup）并把 serve 切到 `ProductionMemoryManager`；(b) 若同步 `expRepo.Create` 是既定态，则 `WriteBuffer`/`EmbeddingQueue` 应标注为实验/未接线，避免误用。
- **状态**：未修（需先确认哪条写路径是目标态）。

### ★★ #14 监控 DAG 节点图在 serve 下无界增长（已记录，未修）
- **现象**：`monitoring.Pruner`（`pruner.go`）是唯一清理 `dag.Engine` 节点的组件（`RemoveNode` 仅被 `pruner.go:177` 调用），但它**只在 `plugin.go:203` `o.pruneCfg != nil` 时构造**，而 `pruneCfg` 只能经 `WithPruneConfig` 设置——**全仓库唯一调用方是 `plugin_test.go:362`**。serve（`serve_routine.go:78`）与 demo（`demo.go:51`）的 `NewConsole(...)` 选项里**都没有 `WithPruneConfig`**。
- **确认**：`dag.Engine.HandleEvent` 对每个 `agent.started`/`task.started` 事件 `e.nodes[nodeID]=node`（engine.go:319/388），`AddNode` **无数量上限**；节点仅在 `StatusDead/StatusCompleted` 且早于 cutoff 时由 Pruner 删除。Pruner 不启动 → 完成的 agent/task 节点及其 edges **永不回收**。
- **对照（事件 Tab 层是安全的）**：各 `tabs/*.go`（event/llm/mcp/workflow/evolution/arena/memory）在 `HandleEvent`/`Add*` 内**都有自封顶**（`len>=maxXxx` 时丢最旧），与 Pruner 无关；DAG 节点的 `Timeline` 也有 `TrimTimeline`（但同样仅 Pruner 调用）。故无界增长**仅限 DAG 的 `nodes`/`edges` map**（每个曾出现过的 agent/task 一个常驻节点）。
- **后果**：长时间运行的 serve 进程中，`dag.Engine.nodes` 随累计 agent/task 数单调增长 → 内存缓慢泄漏 + `Snapshot`/`Nodes` 全量拷贝随之变慢。短会话/demo 无感；长期 7x24 服务会累积。
- **待办**：在 serve 装配 `NewConsole(...)` 增加 `monitoring.WithPruneConfig(monitoring.PruneConfig{})`（用默认 24h/5min），或让 MonitorPlugin 默认构造 Pruner（除非显式关闭）。
- **状态**：未修（需定默认清理策略是否随 serve 自动开启）。

## 模块扫描进展（2026-08-25 下半场：生命周期/循环/goroutine 层）

对**生命周期、循环、goroutine、事件消费层**做了跨模块系统性扫描（`go func` / `time.NewTicker` / `for range ch` / `for { select` / `context.Background()`）：

| 模块 | 结论 | 关键佐证 |
|------|------|----------|
| ares_evolution | ✅ 已修 scheduler；service.go 干净 | service 各代 post-score、recordGenealogy/recordLineages 均带 `maxLineages`(1000) cap；errgroup 受管；`context.Background` 多为 el.Info/Warn 日志或 ScorerFunc 接口限制（`llm_scorer.go:335` 已注明） |
| knowledge | ✅ 干净 | retriever filterByTypes 正确；linker(decision/architecture/similarity/timeline) 无状态无 goroutine；runtime errgroup+SetLimit |
| ares_memory | ✅ 干净 | production_manager_* 先 SetTenantContext；pipeline.go Run 循环 bound 于 ctx.Err/io.EOF；session/cache 都有 ticker+ctx.Done |
| ares_flight | ✅ 干净 | genealogy_collector.go 订阅→consume→树，errgroup 受管；collector.go 消费 agent 事件 |
| ares_runtime | ✅ 干净 | manager_chaos.go:286 relay goroutine 有 stop 信号（channel 关闭/ctx 取消即退，defer cancel）；observer/bus/lifecycle 均 WaitGroup/ctx 受管 |
| agentfabric / workflow | ✅ 干净 | fabric 索引用 map+range；workflow graph 纯迭代无 bare goroutine |
| discovery | ✅ 干净 | engine.go StartAutoDiscovery goroutine bound 于 ctx.Done()+ticker.Stop |
| ares_arena | ✅ 干净 | injector 仅转发到 ares_runtime（无阻塞 sleep）；survival 用 ticker.Stop+ctx.Done+深拷贝读；regression.go:481 semaphore+WaitGroup；http.go:450 长任务带 timeout |
| ares_shutdown | ✅ 干净 | callbacks/manager ExecuteParallel 用 WaitGroup+done channel+ctx.Done drain，无泄漏 |
| ares_mcp | ✅ 干净 | transport_stdio 读写泵 ctx 受管；config_watcher debounce 定时器；transport_server/SSE 泵 ctx.Done+msgCh close |
| are_skills | ⚠️ 特性层空转 | outcome_recorder.go 订阅→consume→Record panic 恢复（✓）；但 `ares_skills`/`skills.Registry` 未接入 serve 运行时（见 #11） |
| aresrecovery | ⚠️ 执行侧未接 | 两个 ticker loop（pop:140 / exec_feedback:332）ticker.Stop+ctx.Done+panic-recover（✓）；但 spawner/chaos/population/quota 零生产构造（见 #12）；观测面(bootstrap)与 W4(peer_mode) 已接 |
| ares_eval / ares_experience / ares_archive / ares_observability / ares_protocol | ✅ 干净 | 五包非测试零 goroutine/ticker；ares_eval 经 provide_evolution(wiring) 注册，`AgentTestRunner` 消费；ares_experience 经 provide_distillation 闭环(写入 experiences_1024→读取) 供 Track A guidance；观测面 M3-1/M3-2/M4-1 读写两侧均be接；ares_protocol 为纯 AHP 数据类型。轻微：legacy `comp.Evolution.EvaluatorRegistry`(llm_judge) 被创建但无 `Get/Evaluate` 消费（仅 NEW `Coordinator.Evaluate` 生效，见注） |
| agentfabric / agents (lease) | ✅ 干净 | agentfabric 非测试零 goroutine/ticker；agents/lease 过期由 kernel_loop.go W1 循环 `RequeueExpiredLeases`(recovery.go:145→fabric.CheckExpiredLeases) 驱动，sem-guarded+panic-recover+timeout 已接，无泄漏 |
| cmd/ares serve wiring | ✅ 干净 | serve.go:118 `WaitBackground()` 收口 bgGroup；:267 `mgr.Start`；:312/319 Dashboard Start/Stop；serve_routine.go:246 `SystemRuntime.Shutdown`；:86 plugin.Start；AKF service 复用共享 `comp.KnowledgeRuntime`(serve.go:208-209) 闭合 AKG 读回路 |
| storage / ares_config / ares_ratelimit | ✅ 干净 | write_buffer.js errgroup + requeue；embedding cache 有 cleanup ticker；sliding_window/token_bucket 已复核 |
| api/core / api/tools / ares_security | ✅ 干净 | 计goroutine/ctx.Background 信号为 0 |
| agentipc / agentloop / agentsyscall / kernelctx / system_runtime | ✅ 干净 | primitives.go:93 handler goroutine 以 ctx 受管，deliverReply 为 best-effort drop + removePending(defer)；orchestrator.go:197/244/284 均 wrapped `Wait()` + timeout select |
| ares_bootstrap (AKG bridge) | ✅ 闭环 | knowledge_akg.go `buildBootstrapAKGBridge`(写 DistillBridge) + `StoreProvider`(读)；`triggerAKGBridge` 于 eg 下 bounded goroutine + 30s timeout，best-effort |
| ares_bootstrap (完整装配) | ⚠️ 1 处开放回路 | `bgGroup` errgroup 统一收口 + 逆序 cleanup；演化 5min ticker / LLM suggester 15min / 蒸馏事件循环均 bound；expiry cleanup worker(bootstrap.go:297) 已接。EvolutionProvider 已接(#9 已修)。**仍开放**：`comp.Discovery.Engine` 被 `StartAutoDiscovery` 启动却零消费(见 #10)。FlightRecorder.Start(bootstrap.go:408) 已调、Dashboard.Start/Stop(serve.go:312/319) 已接 |
| ares_evolution (evolution main loop) | ✅ 闭环 | `bootstrap_steps.go:204-227` 5min ticker 直接调 `popAdapter.Run(ctx)` 驱动 GA 主回路；`Service.RunIdleEvolution` 并行入口 |

**结论**：此代码库在"生命周期/循环/goroutine/事件消费"层**极其规整**——几乎全部用 errgroup / WaitGroup / ctx.Done / ticker.Stop / panic-recover 正确收口，肉眼未见裸 goroutine 泄漏或未受管的无限循环。跨模块"未闭环"问题集中在**装配/注册层**（见 #10-#14 开放回路）。`agentipc` 的 `Request` 已确认：handler goroutine 以调用方 ctx 受管，timeout/cancel 时 `deliverReply` best-effort drop、`removePending` 经 defer 清理，无泄漏。

## 全模块深读完成（2026-08-25 二次核实）

**所有主要模块的业务逻辑深读已完成，无遗留深读项。** 收尾轮补读并确认：

| 模块 | 结论 |
|------|------|
| llmservice | ✅ 干净。`Service.Generate`（tool 消息路由 Chat API vs 普通 Generate）、`GenerateEmbedding`（类型断言 embedder 接口）、`GenerateSimple` 均正确；repo 日志 best-effort warn。**非死包**：经 `api/service/llm` 公开包被 `sdk/sdk.go:245`（`llm.NewService`）与 `agentloop/engine.go` 消费。先前"死包"猜测已否定。 |
| storage/postgres (embedding_queue/write_buffer) | ✅ 代码本身干净（idempotency/lost-update/DLQ/Reconcile 正确），但整套子系统生产未接线 → 见 #13。 |
| storage/postgres repositories | ✅ CleanupExpired 5 处实现均正确（DELETE + RowsAffected）；仅 experiences 接进 maintenance worker → 见 #7。 |
| monitoring/dashboard | ✅ 逻辑干净（tabs 自封顶、publisher/collector ctx 受管），但 DAG 节点无界增长 #14 + 两套子系统臃肿 → 规划 0.3.1 重设计（`plan/0.3.1_monitoring.md`）。 |
| detector | ✅ 干净。`environment.go` `Detect` 只读探测（Ollama/API keys/PG/MCP），ctx + per-call timeout，never-panic 契约；经 `sdk/quickstart.go:15` 消费。 |

### 重点排查类型
- 只写不读 / 只注册不消费（**本代码库已确认集中在此层**——装配/注册缺一个逻辑上的读取/消费方）
- 手动 close/fflush 缺失、资源句柄泄漏
- 锁内 I/O / 持锁长操作（token_bucket、stream channel、pbp 锁）
- debounce/cron/generation 空转
- 内存无限增长（append-only slice / map、无清理、无 cap）
- context 丢失（context.Background() 硬编码、无取消传递）
- 演化产物未回读、未参与调度/路由

## 关联文件（file:line）

- `internal/ares_evolution/scheduler.go:303` — 订阅 EventAgentEnd，无生产者 → 断联。
- `internal/ares_callbacks/callbacks.go:19` — `EventAgentEnd Event = "agent.end"`。
- `internal/ares_bootstrap/bootstrap.go:598` / `provide_evolution.go` — 演化装配入口。
- `cmd/ares/peer_mode.go:143-144` — W4 反馈回路闭合点。
- `cmd/ares/scheduler_compat.go` — 共享 LoadTracker/Scheduler 的 type alias。
- `internal/kernelscheduler/scheduler.go` — WithAttribution L167-171、attribution.Record L810。
- `internal/taskfabric/fabric.go` — record() 持锁调 Append（潜在持锁 I/O）。
- `internal/ares_evolution/service/service.go` — RunIdleEvolution L414、Evolve L434、EvolveOnIdle 分支 L468、recordGenealogy L484、recordLineages L487。
- `internal/ares_evolution/genome_wiring_run.go:678`、`genome_wiring_system.go:697`、`service.go:460` — 独立演化触发需核对。
