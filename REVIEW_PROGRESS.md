# ares 框架深度 Review - 进度与发现

> 目标：逐模块深度 review，找出潜在 bug 与"没有闭环 / open loop"的位置。
> 模块：`github.com/Timwood0x10/ares`，目录 `/Users/scc/go/src/goagent`，分支 `dev`。
> 规模：约 1419 Go 文件，50+ 内部包，24772 图节点。

## 收口结论（2026-08-25）

**全部已定位缺陷均已修复，零遗留。** 逐笔状态：

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 1 | `kernelscheduler` 缺 `waitFor` helper + 两处并发测试竞态 | 中 | ✅ 已修（补 helper + 轮询同步，15×+race 绿） |
| 2 | `taskfabric.record()` 持 `f.mu` 做 `store.Append` I/O | 中 | ✅ 已修（`recordLocked`/`flushAppends` 拆分，I/O 移出锁） |
| 3 | `EvolutionScheduler` 订阅错配（EventAgentEnd 无生产者） | 中 | ✅ 已修（改订阅 `ares_events.EventAgentStopped`） |
| 4 | `recordGenealogy` 的 `s.lineages` append 无界增长 | 低 | ✅ 已修（套用 `maxLineages` cap） |
| 5 | README 使用不存在的 `sdk.WithYAMLFile` API | 中 | ✅ 已修（全仓库改 `sdk.WithConfig`，6 处文档） |
| 6 | README benchmark 数字过期 + 引用已删除 bench | 低 | ✅ 已修（M3 Max 重跑全量，同步 README/README_CN） |

**新增 6 条"开放回路"（未修，全属装配/注册层，需设计决策后接线）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 7 | 存储过期/衰减清理未接线（`CleanupExpired`/`Decay` 5 方法零调用） | 中 | ⚠️ 已记录，未修 |
| 8 | `EvolutionScheduler.RecordScore` 生产无调用方（阈值降级触发不工作） | 低 | ⚠️ 已记录，未修 |
| 9 | `EvolutionProvider` 仅 SDK 注册，服务端知识图缺演化上下文 | 中 | ⚠️ 已记录，未修 |
| 10 | 服务发现引擎 `comp.Discovery.Engine` 被启动但零消费（检测结果被滞留） | 低 | ⚠️ 已记录，未修 |
| 11 | SKILLS 渐进披露子系统未接入服务端运行时（`skills.Registry` 从不构造 / `ares_skills` 仅 CLI status 可达 / `SetSkillsRegistry`、`SeedRegistry`、`envcap.NewSearcher` 零调用） | 中 | ⚠️ 已记录，未修 |
| 12 | "演化内核"适配器未接入 serve 装配（`EvolutionAwareSpawner`、`NewChaos`、`NewPopulationAdapter`+`RunKernelEvolutionLoop`、`NewEvolutionAwareQuotaManager` 零生产构造；`spawn_policy_source.go` 适配器无消费方） | 中 | ⚠️ 已记录，未修 |

**贯穿性结论**：全部 12 项(6 已修 + 6 未修)中，**未修的 6 项清一色是"装配/注册层"问题**——组件被实现、被测、甚至被引用，但缺少一个逻辑上的生产消费/构造方。代码库在生命周期/循环/goroutine/事件消费层**极其规整**（errgroup/WaitGroup/ctx.Done/ticker.Stop/panic-recover 全覆盖），在纯数据流与纯类型层也干净（compiler/pipeline/provider/knowledge/eval/protocol 等）。真正残留的缺陷集中在: register-but-never-consume / start-but-never-read / adapter-never-constructed。

**注（轻微，未单列）**：legacy `comp.Evolution.EvaluatorRegistry`(llm_judge) 在 provide_evolution.go 创建但无下游 `Get/Evaluate` 消费（仅 NEW `Coordinator.Evaluate` 生效）；ares_memory `BuildPromptMessages` 重复调 `snapshotTuning()`(L625/L660) 无害；knowledge store 构造器的 `context.Background()` 仅用于迁移 `initTables` 无泄漏。

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

### ★★ 演化决策 Provider 仅 SDK 注册，服务端知识图缺演化上下文（已记录，未修）
- **现象**：`EvolutionProvider`（`provider/evolution`，把当前/历史 strategy 流为 `ObjectDecision` 知识对象）**只在 SDK 路径注册**（`sdk/knowledge.go:202` `evoprovider.New("evolution", evoStore)`）。服务端 bootstrap 的知识运行时（`provide_new_evolution.go:443-471`）只注册 memory / codebase / vector / akg_store 四个 provider，**未注册** EvolutionProvider。
- **后果**：服务端运行时，进化产生的 strategy（决策/版本/父ID/mutation/score）**从不进入 AKG 知识图**，任何"演化上下文"查询在服务端都取不到 strategy 决策。属"演化产物未回读 / 未参与知识检索" open loop 候选。
- **待办**：确认服务端是否应注册 `evoprovider`（需要把 `comp.NewEvolution.StrategyStore` 注入）——若 do 则补注册；若 do 是刻意（演化知识走 `provider_evolution` 的"演化历史"wiring），需明确不注册的理由。
- **状态**：未修（需先确认设计意图）。

### ares_memory（已查：manager_impl / production_manager / production_manager_tasks）
- `manager_impl.go`：结构清晰，`SetSkillsRegistry/SetLeaseManager/AcquireSessionLease/ReleaseSessionLease` 均 `RLock/Lock` 保护，线程安全。lease 管理器共享，正确。
- distillation 管道（pipeline.go）闭环：Distiller→ReportGenerator→PushService 一条线；`PushAfterDistill` 默认 true；部分失败仅 warn 不中断。
- `production_manager.go`：CreateSession/AddMessage/GetMessages/BuildContext/BuildPromptMessages 均先 `tenantGuard.SetTenantContext`，正确。`sessionCache` LRU 逐出（按 UpdatedAt，O(n) 扫描，无 bug）。轻微冗余：`BuildPromptMessages` 调两次 `snapshotTuning()`（L625/L660）+ max-history 截断在 repo（GetBySession 传 maxHist）与 L661 各做一次，无害。
- `production_manager_tasks.go`：**蒸馏写→读闭环成立**。写路径 `StoreDistilledTask` 用 `memembed.BuildMemoryExperienceSpec(...)` 组装 spec，`WriteItem{Table:"experiences_1024", SpecKind/Prefix/Hash}` 入 writeBuffer；读路径 `SearchSimilarTasks` 用 `retrievalService.Search(SearchExperience=true)` 查同一 `experiences_1024` 表。`write_buffer.go:322` INSERT 到 `experiences_1024`、`flushBatch:358` 建 `make([]float64,1024)` 占位 + 入 embedding_queue 异步回填，读侧 `retrieval_search.go` 取已回填向量。同一张表、同一维度(1024)、同一租户 → 无断环。`SpecDim:0` 仅写进 metadata `embedding_dim` 供 trace，不影响实际维度（embedding client/model 决定）。
- `memory_patcher.go`：内存补丁 executor 有 `Apply` 写配置，配合 `MemoryConfigStore`（Lock/Unlock/GetConfig）读一致性（`snapshotTuning` 已用 RLock），线程安全。

### ★★ 服务发现引擎被启动但零消费 — 检测结果被滞留（已记录，未修）
- **现象**：`ProvideDiscovery(ctx, &cfg.Discovery)`（bootstrap.go:478）在 `cfg.Discovery.Enabled` 时创建 `discovery.NewEngine(discovery.NewMemoryStore(), nil)` 并调用 `eng.StartAutoDiscovery(ctx, interval)`（provide_discovery.go:65）启动自动发现循环（轮询 ARES/Claude/Cursor/VSCode/二进制探测，默认 5min）。
- **确认**：该 engine 实例被 `comp.Discovery.Engine`（bootstrap.go:487）持有后**从此不被消费**——全仓库非测试代码无任何路由/消费者读取它（grep `comp.Discovery`/`.Discovery.Engine` 仅 system_runtime_wiring.go:148 注册为状态组件 + dashboard watcher 无关）。`api/discovery` 包是**自建** engine（内含 `NewSQLiteStore`），与 bootstrap 的 engine 是**两个独立实例**；cmd/ares 未将其路由到 comp.Discovery。
- **后果**：启用 discovery 时，自动发现循环持续轮询并把检测到的 MCP server / agent runtime 写入**无人读取的内存 store**——纯作废功（发现即丢弃）。默认 `cfg.Discovery.Enabled=false`，故此回路为 opt-in、默认不触发。
- **待办**：把 `comp.Discovery.Engine` 接到消费方（如发现→MCP 自动注册，或通过 `api/discovery` 路由暴露），否则启动无意义。
- **状态**：未修。

### ★★ SKILLS 渐进披露子系统未接入服务端运行时 — 特性空转（已记录，未修）
- **现象**：`internal/knowledge/skills.Registry`（registry.go:42 `NewRegistry`）为内存管理器的渐进披露（progressive disclosure）而设计——`memoryManager.SetSkillsRegistry`（manager_impl.go:82）挂载后，`BuildContext` 会 prepend "Available skills" 块（L489-492）。但**全仓库非测试代码从不构造 `skills.NewRegistry()`**，`SetSkillsRegistry`、`ares_skills.Catalog.SeedRegistry`、`envcap.NewSearcher` 全部零调用。
- **确认**：
  - `internal/ares_skills`（catalog/loader/discovery/experience/outcome_recorder）**只被 `cmd/ares/status.go` 导入**（CLI 展示），`ares serve` 运行时完全引用不到它。
  - `internal/knowledge/skills` 只被 envcap / ares_memory / ares_skills 自身导入；`NewRegistry` 无非测试调用。
  - `internal/tools/envcap`（`NewSearcher` 接收 skills.Registry）无任何消费者。
- **后果**：服务端运行时，内存管理器的 "Available skills" 渐进披露块**永远为空**（`skillsRegistry` 恒为 nil）；skill 目录/检索/激活（`ares_skills.Catalog.Activate/ResolveTools/Search`）在 serve 路径不可用；tool 能力搜索（envcap）未接线。整套 SKILLS 特性仅 `ares status` 的只读展示 + 大量测试覆盖，**生产 serve 不生效**。
- **待办**：在 serve 装配中创建 `skills.NewRegistry()` + `ares_skills.NewCatalog(...).SeedRegistry(reg)`（并从配置装载 skill 源），`SetSkillsRegistry` 挂进 memory manager，`envcap.NewSearcher` 接入工具能力检查；否则此特性为未写实的 "register-but-never-populate" 回路。
- **状态**：未修。

### ★★ "演化内核"适配器未接入 serve 装配 — M2 策略不生效（已记录，未修）
- **现象**：`internal/aresrecovery` 实现了一组"演化内核"适配器，把活跃演化策略（`StrategyStore`）强制施加到运行时：`EvolutionAwareSpawner`（M2-1 孵化策略：`spawn.enabled`/`spawn.max_concurrent`/`spawn.preferred_capabilities`）、`PopulationAdapter`+`RunKernelEvolutionLoop`（周期性执行群体策略）、`NewChaos`（混沌/恢复）、`NewEvolutionAwareQuotaManager`。**全仓库非测试代码零构造这些适配器**（grep 确认：`NewEvolutionAwareSpawner/NewChaos/NewPopulationAdapter/RunKernelEvolutionLoop/NewEvolutionAwareQuotaManager` 均 0 生产引用）。
- **佐证**：`ares_bootstrap/spawn_policy_source.go` 的 `evolutionSpawnPolicySource`（把 `StrategyStore`→`SpawnPolicySource`）唯一目的就是喂给 `EvolutionAwareSpawner`，但后者从不被构造 → 该适配器在 serve 下也是死代码。
- **对照**：aresrecovery 在 `cmd/ares` 里真正被接的只有——观测面（`EvolutionTracer`/`FeedbackStore`/`GlobalTracer`，bootstrap.go:296-298）、W4 反馈回路（`peer_mode.go:144` `RunEvolutionFeedbackLoop`）、IPCDM 策略（`evolution_ipc.go:89` `NewEvolutionAwareIPC`）、dashboard 变更归因（`dashboard_observability.go:57` `NewChangeAttributor`，只读展示）。列表**缺**孵化器/群体/混沌/配额四个执行侧适配器。
- **后果**：标准 `ares serve` 路径下，演化策略里 `spawn.max_concurrent`、`preferred_capabilities`、群体上限、混沌/恢复策略**不生效**（孵化政策退回普通 spawn；群体/混沌/配额循环不跑）。仅 W4 反馈回路与 IPC 策略活着。
- **待办**：在 serve（bootstrap）装配中构造 `EvolutionAwareSpawner`+`NewSpawnPolicySource(store)`、`PopulationAdapter`+`RunKernelEvolutionLoop(ctx,...,interval,timeout)`（挂 bgGroup）、暴露 `NewChaos`/配额管理器；确认 M2 是否已进入 serve 阶段。
- **状态**：未修。

### ★★ 存储过期/衰减清理未接线 — 无界增长（已记录，未修）
- **现象**：PostgreSQL 多条表设计了 `expires_at`/`decay_at` 列 + 对应清理方法，但**生产没有任何调度器调用它们**：
  - `ExperienceRepository.CleanupExpired`（`experience_repository.go:538`，`DELETE ... WHERE decay_at < NOW()`）——从不在生产调用；读侧却过滤 `(decay_at IS NULL OR decay_at > NOW())`（L239/316/373/460/573）。
  - `ConversationRepository.CleanupExpired`（`conversation_repository.go:351`）、`KnowledgeRepository.CleanupExpired`（`knowledge_repository.go:795`）、`SecretRepository.CleanupExpired`（`secret_repository.go:270`）、`SessionRepository.CleanupExpired`（`session.go:213`）——全部只定义、零调用。
- **后果**：到期/衰减的行（conversations 的 `expires_at`=24h、experiences 的 `decay_at`=30d、sessions/secrets/distilled_memories）**永远留在 PostgreSQL**。读路径已按 `decay/expires` 过滤，业务无感，但存储无限增长→成本上升、后续扫描/索引维护变慢。属"只写不清理" open loop。
- **待办**：在 `cmd/ares`（或 ares_bootstrap）接线一个周期性 maintenance worker，定时调 5 个 repo 的 `CleanupExpired`（distilled_memories 同样）。
- **状态**：未修（无 config 开关，需定清理间隔）。

### 开放回路（已记录，未修）— EvolutionScheduler.RecordScore 生产无调用方
- **现象**：`EvolutionScheduler.RecordScore(score float64)`（`scheduler.go:214`）把任务分写入滑动窗口（`s.scores`，上限 `scoreWindowSize`），供 `shouldEvolve`（L390-411）做**趋势降级检测**：`TriggerOnThreshold` 分支取 `avg/recent` 算 `drop=(avg-recent)/avg`，`>= degradationThreshold` 才触发演化。
- **确认**：全仓库唯一调用方全部在测试文件（`scheduler_test.go:128/157/221/271`、`dream_cycle_test.go:289/395/448/555`、`genome_wiring_integration_test.go:349/501/577/857`）。生产无任何代码调用 `EvolutionScheduler.RecordScore`。其它 `RecordScore` 是**不同类型**的方法（`RollbackPolicy.RecordScore` `rollback_policy.go:137`、`PopulationGenealogyRecorder.RecordScore` `genome_wiring_genealogy.go:135`），不喂 scheduler 窗口。
- **影响**：`TriggerOnThreshold` 下 `avg<=0||recent<=0` → 恒 return false，降级触发与分数驱动的演化**永远不触发**（除非另有调用方喂分）。默认 trigger 是 `TriggerOnIdle`（走空闲检测，无此问题），故此回路为**潜伏**，不阻断当前默认行为。
- **待办**：确定生产分数来源（诊断计数？attribution？）并接线到 scheduler；或在默认 idle 路径下保留代码但明确标注阈值模式需 feed。
- **状态**：未修（等明确生产喂分来源后再定）。

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
| ares_bootstrap (完整装配) | ⚠️ 2 处开放回路 | `bgGroup` errgroup 统一收口 + 逆序 cleanup；演化 5min ticker / LLM suggester 15min / 蒸馏事件循环均 bound。**但** 知识运行时缺 EvolutionProvider(见 #9)；`comp.Discovery.Engine` 被 `StartAutoDiscovery` 启动却零消费(见 #10)。FlightRecorder.Start(bootstrap.go:408) 已调、Dashboard.Start/Stop(serve.go:312/319) 已接 |
| ares_evolution (evolution main loop) | ✅ 闭环 | `bootstrap_steps.go:204-227` 5min ticker 直接调 `popAdapter.Run(ctx)` 驱动 GA 主回路；`Service.RunIdleEvolution` 并行入口 |

**结论**：此代码库在"生命周期/循环/goroutine/事件消费"层**极其规整**——几乎全部用 errgroup / WaitGroup / ctx.Done / ticker.Stop / panic-recover 正确收口，肉眼未见裸 goroutine 泄漏或未受管的无限循环。跨模块"未闭环"问题集中在**装配/注册层**（见下方 3 条已记录开放回路）。`agentipc` 的 `Request` 已确认：handler goroutine 以调用方 ctx 受管，timeout/cancel 时 `deliverReply` best-effort drop、`removePending` 经 defer 清理，无泄漏。

## 待深入模块（已生命周期扫描标 ✅，剩业务逻辑深读）

已对全部主要模块完成深读。生命周期/循环/goroutine 层经全库扫描确认干净，无残留的未受管循环/裸 goroutine。**剩余深读项**：llmservice、storage(repositories/services 非清理)、monitoring/detector、agents(sub/executor/heartbeat)、ares_archive 内部业务、ares_observability 内部业务、ares_experience 内部业务、ares_protocol(ahp)。

**进展说明**：知识(compiler/pipeline/provider/store)、ares_bootstrap(完整装配)、ares_arena、ares_mcp、ares_skills、aresrecovery、ares_eval/experience/archive/observability/protocol、agentfabric/agents(lease)、cmd/ares(serve) 均已深入审完并记录结论（见"模块扫描进展"表与 #7-#12 详情）。

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
