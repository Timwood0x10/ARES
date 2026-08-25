# ares 框架深度 Review - 进度与发现

> 目标：逐模块深度 review，找出潜在 bug 与"没有闭环 / open loop"的位置。
> 模块：`github.com/Timwood0x10/ares`，目录 `/Users/scc/go/src/goagent`，分支 `dev`。
> 规模：约 1419 Go 文件，50+ 内部包，24772 图节点。

## 收口结论（2026-08-25，2026-08-25 二次核实已更新状态）

> **二次核实（本轮）**：逐条 grep 复核了 #7-#14 的生产接线现状，发现 **#8、#9 代码里其实已经修复**（先前记录过期），**#7 为部分修复**。
> **第三轮（2026-08-25）**：补齐 **#11 的第二半（envcap 工具能力搜索）** → #11 现为**完全修复**。下表已同步为最新真实状态。

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
| 11 | SKILLS 渐进披露 + 工具能力搜索 | 中 | ✅ **已修（第三轮补齐两半）**：**上半**（渐进披露）`skills_wiring.go` 的 `wireSkills`（`bootstrap.go:265`）已构造 catalog + `SeedRegistry` + `SetSkillsRegistry`，并把 seed 出的 registry 存入 `comp.SkillsRegistry`。**下半**（工具能力搜索）`registerCapabilitySearch`（`tools.go:107`）用 `envcap.NewSearcher(NewRegistryLister(internalReg), comp.SkillsRegistry, nil)` 构造并注册 `search_capabilities` 工具（`serve.go:236`，binder 之前）。两源：registry 工具（含 registerNativeTools 预注册的原生命令，KindTool）+ 技能。envcap 零生产调用问题已消除。 |

**部分修复：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 7 | 存储过期/衰减清理未接线 | 中 | 🟡 **部分已修**：`maintenance_worker.go` 的 `startExpiryCleanupWorker`（`bootstrap.go:297`）已接线，每小时 purge。**但只注册了 `experiences_1024`**（`bootstrap_steps.go:53`）；`ConversationRepository`/`KnowledgeRepository`/`SecretRepository`/`SessionRepository` 的 `CleanupExpired` + distilled_memories 仍**零注册**（各表 `expires_at`/`decay_at` 仍不清理）。 |

**仍开放（未修，装配/注册层）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 12 | "演化内核"适配器 serve 装配 | 中 | 🟡 **部分已修（二次核实纠正）**：quota/spawn-gate/population **三阶段均已接线**（`serve_agents.go:97/114/131`，注释 "REVIEW #12 stage-N closure"）。**仅 `Chaos`（+`Sandbox`）未接线**——而 `Chaos.InjectFailure` 会真实 `Kill`/`Suspend` 活跃 agent，属危险项。规划走 Shadow Sandbox 默认（生产零影响）+ Live Chaos 可选，见 `plan/0.3.1plan/chaos_isolation.md`。 |
| 13 | 异步 embedding 队列子系统全链路无消费者（`EmbeddingQueue.FetchPendingTasks`/`MarkCompleted`/`MarkFailed`/`Reconcile` 零生产调用，无 backfill worker；且唯一生产者 `ProductionMemoryManager`+`WriteBuffer` 仅测试构造，serve 走 `memoryManager` 同步 `expRepo.Create`） | 中 | ⚠️ 已记录，未修 |
| 14 | 监控 DAG 节点图在 serve 下无界增长（`Pruner` 仅在 `WithPruneConfig` 时启动，serve/demo 均不传；`dag.Engine.nodes` map 每 agent/task 一节点、`AddNode` 无 cap、仅 Pruner 会 RemoveNode） | 中 | ⚠️ 已记录，未修（**规划并入 0.3.1 runtime 检测面板重设计，见 `plan/0.3.1plan/monitoring.md`**） |
> **第四轮（2026-08-25）**：全量复核 #7-#14 生产接线 + 补读剩余高复杂度模块，确认如下。

**第四轮新增缺陷（#15-#18）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 15 | Planner 证据反馈回路断裂 + 内存证据无界增长：直接路径保存证据缺 `CapabilityName`（`bridge.go:84-89`），查询按能力名（`planner.go:118`）+ 评分键 `Tool:Capability`（`evidence.go:166`）→ 最常见路径证据永不反馈评分器；`memoryEvidenceStore.Save`（`executor.go:137-145`）仅追加无 cap → 长期运行内存泄漏 | 中 | ⚠️ 已记录，未修（P0） |
| 16 | `stringutils.parseInt`（`stringutils.go:177-186`）无整数溢出保护 | 低 | ⚠️ 已记录，未修（P2） |
| 17 | `KnowledgeUpdate`/`KnowledgeCreate` 标签数组含非 string 元素时静默置 `""`（`knowledge_base.go:244-251`、`347-354`） | 低 | ⚠️ 已记录，未修（P1） |
| 18 | `KnowledgeUpdate` 未 nil 检查 `GetKnowledge` 返回（`knowledge_base.go:231`）——生产 `StoreAdapter` 返回 `errObjectNotFound` 无 panic，属接口契约防御建议 | 低 | ⚠️ 已记录，防御性建议（P2） |


**第四轮核实更新：**
- **#7**：确认 worker 接线行号为 `bootstrap.go:325`（原记录 297），`experiences_1024` 仍为唯一注册 Cleaner；其余 4 个 `CleanupExpired`（Conversation/Knowledge/Secret/Session）仍未注册。
- **#12**：确认 quota/spawn-gate/population 三阶段接线存在（`serve_agents.go:97/114/131`），仅 Chaos/Sandbox 未接。
- **#13**：**确认仍未修**——`FetchPendingTasks`/`MarkCompleted`/`MarkFailed`/`Reconcile` 零生产调用，`ProductionMemoryManager` 仅测试构造。
- **#14**：**确认仍未修**——`NewConsole`（`serve_routine.go:78-84`）未传 `WithPruneConfig`，`Pruner`（`plugin.go:202-205`）从未创建。

**第四轮贯穿性结论**：累计 **18 项 = 10 已修 + 2 部分修（#7、#12）+ 6 待处理（#13、#14、#15、#16、#17、#18）**。新增 **#15 是唯一"纯数据流"型缺陷**（非装配层）——单测全部通过（测试直接构造证据时总是设置 CapabilityName），生产链路却丢失该字段；此类缺陷比装配层问题更难被单测发现。完整报告见 `REVIEW_SUMMARY.md`。

---

## 第五轮（2026-08-25）：并行子 agent 深读 workflow/core/evolution/tools/llm/ares_runtime/ares_mcp

用并行子 agent 对 7 个尚未深读或需复核的模块逐文件 bug-hunt。**本轮修复 5 个真 bug + 新记录若干开放回路/缺陷。**

**本轮已修（5 个，均已 `go build`+`go test`+改动包 `-race` 绿）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 19 | `evolution/llm_adapter.go` `parseAddEdge`(L177)/`parseRemoveEdge`(L203)：`len(parts) < 4` 守卫后却读 `parts[4]` → 输入 `"add edge A ->"`（4 字段）**索引越界 panic**。该解析器跑在 bootstrap LLM 建议 goroutine（15min ticker），**无 recover** → LLM 输出截断的 edge 指令会崩进程 | 高 | ✅ **已修**：守卫改 `len(parts) < 5`（两处）。 |
| 20 | `llm/failover.go` `Generate`(L244)/`GenerateStream`(L337)：所有 provider 冷却时 `lastErr==nil`，却 `fmt.Errorf("...%w", nil)` → 产出 `%!w(<nil>)` 且非 nil 包空错误。`Chat`(L388) 已有 nil 守卫，另两个漏了 | 高 | ✅ **已修**：补 `if lastErr == nil` 返回"all N cooled down"明确错误。 |
| 21 | `ares_runtime/manager.go` `RestartAgent`(L332-337)：释放 `m.mu` 后读 `ma.cancel`/`ma.agent`，与 `ResumeAgent`/`PauseAgent` 锁内写同字段 **data race**（`StopAgent` 已正确锁内捕获，此处漏） | 高 | ✅ **已修**：锁内捕获 `prevCancel`/`prevAgent` 再于锁外使用。 |
| 22 | `ares_runtime/manager.go` `NotifyAgentDead`(L536)→`scheduleResurrection`(L579)：`isStopped` 守卫检查与 `m.g.Go` 不在同一临界区，Stop 若在二者间插入 → `WaitGroup reused before Wait returned` panic | 中 | ✅ **已修**：`scheduleResurrectionLocked` 移进持锁闭包，errgroup Add 与 isStopped 检查原子化。 |
| 23 | `evolution/candidate.go` `containsDangerousPattern`(L320)：对原文 `strings.Contains` 匹配小写模式串 → `"IGNORE ALL SAFETY"` 大写变体**绕过危险指令闸门** | 中 | ✅ **已修**：先 `strings.ToLower` 再匹配。 |

**本轮新记录（未修，多为开放回路/低危）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 24 | `evolution/coordinator/coordinator.go` `Evaluate`(L400/425)：`patchHistory`/`decisions` append-only 无 cap，**在 live path 上**（bootstrap LLM ticker 每 15min `Evaluate`）→ 进程生命周期内单调增长 | 中 | ⚠️ 已记录，未修（唯一"live path 无界增长"，比装配层 #13/#14 更实） |
| 25 | `evolution/candidate_pipeline.go` `Release`(L197)：`registry.Register("profile:"+role,...)` 对已注册 target 报错 → 同一 role 第二次 release 必失败（应用 `Replace`） | 中 | ⚠️ 已记录，未修（pipeline 属开放回路，仅 examples/tests 可达） |
| 26 | `ares_mcp/factory.go` `Create`(L105-108)：`connectCtx` `defer cancel()` 会级联取消 `client.ctx`（`client.go:78` 从传入 ctx 派生）→ 工厂返回的 tool **连接已死**，注释"lifecycle extends beyond"是错的 | 高（但开放回路） | ⚠️ 已记录，未修（`NewMCPToolFactory` 仅测试构造，生产未接） |
| 27 | `ares_mcp/client.go` `receiveLoop`(L310)：每条 `tools/listChanged` 通知 `eg.Go` + `ListTools`，无限流 → 恶意/故障 server 洪泛通知致 goroutine 无界增长 | 中 | ⚠️ 已记录，未修 |
| 28 | `ares_mcp/transport_stdio.go` `Send`(L121)：忽略 ctx，持 `t.mu` 调 `stdin.Write` 无 deadline → 子进程停读则永久阻塞并卡死 `Close` | 中 | ⚠️ 已记录，未修 |
| 29 | `ares_mcp/config_watcher.go`(L104)：debounce 内层阻塞 select 使快速保存不合并、且 200ms 内停止 drain fsnotify 事件；ctx.Done 路径 timer 未 Stop | 中（开放回路） | ⚠️ 已记录，未修（`NewMCPConfigWatcher` 仅测试构造，热重载生产未接） |
| 30 | `tools/resources/builtin/pdf/pdf.go`：`WithAllowedDir` 从未被生产调用（`builtin.go:184`/`api/tools/builtin.go:72` 都 `NewPDFTool()` 无选项）→ `allowedDir==""`，沙箱检查跳过，`pdf_tool` 可读任意路径（`/etc/passwd`），与已沙箱的 FileTools 不一致 | 中（安全） | ⚠️ 已记录，未修（建议 0.3.1 补 sandbox） |
| 31 | `workflow/engine/mutable_dag.go` `ReplaceNode` same-ID 分支(L732)：只加新依赖边不删旧边 → `recalculateDegrees` InDegree 错、拓扑序错 | 中（开放回路） | ⚠️ 已记录，未修（`graph.Graph` 仅作演化 patch target，无生产 Execute） |
| 32 | `core/errors/*` 整包零 importer（含测试）——完整 DLQ/retry/alert 子系统 + ~1500 行测试但无任何生产/测试消费方 | 低 | ⚠️ 已记录（纯 dead package） |

**本轮开放回路补充（不单列编号，归入既有 register-but-never-wired 结论）**：
- `ares_runtime`：约 20 个 plugin 构造器（`NewObserverPlugin`/`NewArenaPlugin`/`NewCheckpointPlugin`/各 router/collector 等）零生产调用——但 `serve_routine.go:49-61` 已注释说明为**刻意的"capability reserve"**，非误删。`CheckpointPlugin.Cleanup` 未接是 checkpoint map 无界增长的潜在诱因（插件本身未接线，暂无害）。
- `tools`：整个 plugin-factory 子系统（`NewPluginRegistry`）、`CapabilitySelector`/`NewToolScorer`/`TagSelector` 选择层、`code_runner.SetTimeout`（dead-write 空设）均实现+测试但零生产接线。
- `llm`：整个 streaming 路径（`Client.GenerateStream`/`FailoverClient.GenerateStream`）+ output adapter 的 tool-calling API（`GenerateWithTools`）+ `parser.go` 多数 Parse* + `timeout.go` 多数 With* 助手——生产仅用 `Chat`/`GenerateWithParams`/`ParseRecommendResult`，其余仅测试可达。故 #26/#28 类 streaming leak 目前**不可达生产**。
- `workflow`：scheduler 族（`NewPriorityScheduler` 等）/`NewToolNode`/`NewAgentNode`/HITL plugin/`NewWorkflowReloader`/`NewOutputStore` 全部零生产调用——`graph.Graph` 无 `Execute` 方法，仅作演化 patch 的目标结构。
- `evolution`（旧包）：除 `LLMAdapter`（bootstrap 15min ticker 用）外，整个 Candidate→Verify→Promote pipeline（`NewCandidatePipeline`/`NewGAGenerator`/`NewDiagnoser` 等）仅 examples/tests 可达，已被 `internal/ares_evolution` 取代。

**第五轮贯穿性结论**：累计 **32 项 = 15 已修（+#19-#23）+ 2 部分修（#7、#12）+ 15 待处理**。本轮验证了此前判断：真 bug 集中在**少数被实际接线的热路径**（evolution LLM adapter、llm failover、ares_runtime manager），而绝大多数模块的问题是**开放回路（实现+测试但未接生产）**。`ares_mcp` 的 #26/#28（连接被 defer cancel 杀死、Send 无 ctx）代码危险但因工厂/热重载未接线暂不可达；一旦 0.3.1 接线需先修。安全项 #30（pdf 沙箱未启用）建议随 0.3.1 修。

---

## 第六轮（2026-08-25）：并行深读 ares_security/ares_callbacks/storage-repositories/knowledge-剩余/evidence/eval/小包

**本轮修复 3 个真 bug（含 1 个 CRITICAL 生产数据丢失）+ 揭出 1 个 CRITICAL 多租户隔离问题（需设计决策）。**

**本轮已修（3 个，`go build`+`go test` 绿）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 33 | `evidence/postgres_store.go:50` DDL `id uuid` 与生产 ID 格式冲突：`Collector.Emit`(collector.go:93) 强制 `WithID("")` → 生成 `ev_%x`（非 UUID），`Append` fallback 生成 `%d-%s`（非 UUID）→ **每条 evidence INSERT 都被 Postgres 以 `22P02 invalid input syntax for type uuid` 拒绝**。serve 开 Storage 时 evidence 持久化 100% 失效，GA fitness 读取链随之空转 | **CRITICAL** | ✅ **已修**：DDL 改 `id text PRIMARY KEY`（ID 本就是应用侧生成的字符串）。 |
| 34 | `ares_flight/collector.go:151/181/190/204`：4 处 evidence `Emit`/`EmitWithMeta` 结果 `_ =` 吞掉 → 配合 #33，Postgres evidence 写入全失败却无任何日志/指标（这也是 #33 长期没被发现的原因） | 高 | ✅ **已修**：4 处改为检查 err 并 `log.Warn`。 |
| 35 | `evidence/evidence.go:110` `MemoryStore.Query` 不排序且 `Limit` 截断取**最旧 N** 条：`Store` 契约要求"按时间倒序"，PostgresStore 遵守（`ORDER BY ts DESC`）而 MemoryStore 违反。GA fitness（`genome/fitness.go` 传 Limit:50/100 期望最近证据）在默认 MemoryStore 下拿到最旧证据 → dev/prod 行为分叉 | 中 | ✅ **已修**：`sort.Slice` 按 Timestamp 倒序后再 `Limit` 截断。 |

**本轮揭出的 CRITICAL（需设计决策，未擅自改）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 36 | **多租户隔离实际未生效**（storage）：三重问题叠加——(a) 所有表 `ENABLE ROW LEVEL SECURITY`+policy 但**无 `FORCE ROW LEVEL SECURITY`**，而 `config.go:103` 以表 owner `postgres` 连接 → PG 对 owner 跳过 RLS，policy 全成死规则；(b) `SetTenantContext`（`tenant_guard.go:42`）用 `set_config(...,is_local=true)` 且经 `Pool.Exec` 单条 autocommit 执行 → SET LOCAL 出事务即失效，且落在与 repo 查询**不同的池化连接**上；(c) 多个 id-only mutator（`KnowledgeRepository.GetByID/Update/UpdateEmbedding`、`ExperienceRepository.GetByID/Update/UpdateScore`、`ToolRepository`、`TaskResultRepository` 等）**无 `tenant_id` 谓词**，完全依赖上述失效的 RLS → **任何知道 id 的租户可跨租户读写**。tenant-safe 的 `Pool.ExecWithTenant`/`QueryWithTenant`（pool.go:177/228）已实现但**零生产调用**（开放回路），生产走的是失效路径。`DistilledMemoryRepository`（`withTenantTx`）是唯一做对的 repo | **CRITICAL** | ⚠️ 已记录，未修（需设计决策：全量走 ExecWithTenant/QueryWithTenant，或给每个 id-scoped 查询加 `AND tenant_id=$n`，并加 `FORCE ROW LEVEL SECURITY` + 非 owner 运行角色做纵深防御） |

**本轮新记录（未修，多为开放回路/低危）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 37 | `ares_security/sanitizer.go:66` `SanitizeOptions` 被完全忽略：`Sanitize`/`maskXxx` 从不读 `s.options`，`MaskChar` 硬编码 `'*'`、`KeepLength`/`PreserveLengthFor` 无效 → `NewSanitizerWithOptions` 的自定义配置静默失效 | 中 | ⚠️ 已记录，未修 |
| 38 | `ares_security/sanitizer.go:174` `sanitizeValue` 仅处理 string，JSON 数字型机密（`{"card":4111...}`）原样透出不脱敏 | 低-中 | ⚠️ 已记录，未修 |
| 39 | `ares_callbacks/callback_bridge.go` `NewBridge`/`BridgeEventStore` 整体零调用（含测试）；`mapEventType` 把 error 事件坍缩成 success 事件（LLMError→LLMCall 等）、`EventLLMToken` 丢弃 → 若启用则审计失真 | 中（开放回路） | ⚠️ 已记录，未修 |
| 40 | `knowledge/runtime/lazy_graph.go` 整个 LazyGraph/LazyNode 子系统零生产调用（`NewLazyGraph`/`ExpandNode` 等仅测试）；runtime.go 注释自承 "Future direction"，`cfg.LazyLoading` 只是 clamp budget 而非真惰性加载 | 中（开放回路） | ⚠️ 已记录，未修 |
| 41 | `knowledge/provider/mysql/provider.go` `NewMySQLProvider` 零生产调用（对比 postgres/vector 已接线）——死 provider | 中（开放回路） | ⚠️ 已记录，未修 |
| 42 | `knowledge` 各 provider `Stream` 用 `errgroup.WithContext` 却从不 `Wait()` → 派生 ctx 的 cancel 从不调用（轻微 ctx 资源滞留，goroutine 仍经 defer 退出，非泄漏） | 低 | ⚠️ 已记录，未修 |
| 43 | `knowledge/linker/architecture.go:26` `ObjectDecision`/`ObjectDocument` 被双重分类进 codeObjs+archObjs → 同标签两对象产生双向/重复 `depends_on` 边，下游无 dedup | 低 | ⚠️ 已记录，未修 |
| 44 | `knowledge/pipeline/normalizer.go:112`（及 memory/code provider）按字节截断可切裂 UTF-8 rune，CJK 摘要（默认中文）易产出非法 UTF-8 | 低 | ⚠️ 已记录，未修 |
| 45 | `eval`（区别于 ares_eval）整包生产死路：唯一生产构造 `setupEvaluators`(provide_evolution.go:103) 不传 `WithDimensionAveraging`/`WithEvidenceStore` → dimension/evidence 路径永不触发。另 `evidence.go:139` dimension pass 用整数截断 `>=max*2/3` 而 item status 用浮点 `<max*2/3.0` → `max=2,score=1` 时 Pass=true 但 item="failed" 自相矛盾 | 低（死路） | ⚠️ 已记录，未修 |
| 46 | `errors/wrap.go:215` `FormatError` 含 `%w` 时把 err append 到末尾但 `%w` 常非末位 → 参数错位产出 `%!s/%!d` 垃圾。但 `internal/errors.FormatError` 零调用（死码） | 低（死码） | ⚠️ 已记录，未修 |
| 47 | `ares_ctxutil/ctxutil.go:48` `trackBackground` 三处生产递增但 `DoneBackground` 零生产调用 → `BackgroundStats` 计数器单调增长永不回落（`runtime:notify-agent-dead` 每次 agent 死亡 +1），label 永不移除 | 低 | ⚠️ 已记录，未修 |
| 48 | `storage/secret_repository.go:583` `Import` 对非 `ErrNoRows` 的查错吞掉后继续 insert；`:632` 全为已存在 key 时 `importedCount==0` → 整个 tx 回滚失败（应为幂等 no-op） | 低 | ⚠️ 已记录，未修 |

**本轮确认干净（生产用到、无 bug）**：`ares_security` JWT（HS256 constant-time `hmac.Equal`、无 alg confusion、exp/iat 校验）、RBAC（default-deny）、middleware/audit（token 不入日志、API-key `subtle.ConstantTimeCompare`）；`scoreutil`（`ClampUnit` NaN/边界正确，4 处生产用）；`truncate`（rune 数学各边界正确，18 处生产用）；`logger`（57 处用，无状态无竞争）；`errors`（100+ 处用，`Wrap`/`Wrapf`/`WrapError` 的 `Unwrap []error` 正确，仅死码 `FormatError` 有 bug）；`ares_callbacks` 的 `Emit`（RLock 快照 handler 后调用、panic 隔离，正确——仅 bridge 是死码）；storage `circuit_breaker`（CAS+atomics 无竞争）、`pool` `ManagedRow/Rows`（连接释放+ctx 取消正确）、`base_repository`（表白名单+`quoteIdentifier`+全参数化）、`vector`/`CreateBatch`/`RotateKey`（参数化+FOR UPDATE+committed 回滚旗标）。

**第六轮贯穿性结论**：累计 **48 项 = 18 已修（+#33/#34/#35）+ 2 部分修（#7、#12）+ 28 待处理**。本轮首次揭出**两个 CRITICAL**：#33（evidence Postgres 写入 100% 失败，已修）与 #36（多租户隔离实际未生效，需设计决策）。#36 是迄今最严重的生产问题——它不是"未接线"而是"接了但整条隔离链失效"，且 tenant-safe 原语已存在却没被用。建议 #36 优先级最高。#36 专项方案见 `plan/0.3.1plan/tenant_isolation.md`。

---

## 第七轮（2026-08-25）：深读 agents 内部 / ares_archive / ares_observability / ares_evolution（top+service+genome+mutation）

用并行子 agent 覆盖最后 4 个生产模块（含全仓库最大的 ares_evolution ~22k 行）。**本轮修 1 个真 bug（NSGA-II 选择核心）+ 揭出 1 个正在丢数据的 HIGH（ares_archive）+ 一批 service 层桩函数缺陷。**

**本轮已修（1 个，`go build`+`go test` 绿）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 49 | `ares_evolution/genome/selection.go:867` NSGA-II crowding-distance 排序错乱：`sort.SliceStable(front,...)` 原地置换 `front`，但 less-func 索引未置换的并行数组 `frontCD` → 首次交换后配对全乱（经典"并行数组 sort.Slice" bug），多目标选择的 partial-front 退化成任意子集，静默破坏多样性保持。**已接线**（`nsga2`/`nondominated` 选择策略，`population.go:552`） | 中（正确性，接线） | ✅ **已修**：改为排序索引置换 `order`，再按序 materialize，`frontCD` 配对不再错乱。 |

**第八轮补修（2026-08-25，两个 P0，`go build`+`go test`+`-race`+`go vet`+gofmt 全绿）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 50 | ares_archive 跨流 round 文件互相覆盖 — 生产正在丢数据 | **高（CRITICAL）** | ✅ **已修**：`RoundRecord` 加 `StreamID` 字段（record.go）；sink 打流归属（sink.go）；writer 新增 `streamDir()`+`sanitizeStreamID()`（allowlist `[A-Za-z0-9._-]` 防路径穿越），每流写独立子目录、rotate 也 scoped 到流子目录（一个流的轮转不再驱逐别流）；reader `Read`/`List`/`Search` 扫 flat root + 各流子目录并跨流去重；顺带修"单损坏 round 文件使 Search 全失败"（改 skip+log）。空 StreamID 走 flat 布局向后兼容。store_test 断言更新到 `dir/streamID/round_1.json`。 |
| 53 | GA `scoreImprovement` 恒为 0（`dream_cycle_ga.go:93`）：`best.Score - BestEverScore()` 两值同源恒 0 → GA shadow 评估永远记平局/负，核心反馈失效 | 高（GA 失效） | ✅ **已修**：在 scoring+evolve **之前**捕获 `prevBestEverScore`，改进量算 `best.Score - prevBestEverScore`（本轮新 best-ever − 上轮），`parent.Score` 亦用捕获值。 |

**本轮揭出（未修，含正在丢数据的 HIGH）：**


| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 50 | **ares_archive 跨流 round 文件互相覆盖 — 生产正在丢数据**：归档文件名全局 `round_%d.json`（`writer.go:104`）但 `roundCounter` per-stream（`compactable_store.go:488/510`），`RoundRecord` 无 stream 字段。serve 里每个 session（`uuid.NewString()`）与每个 sub-agent（`a.id` 流）的终态事件各自从 round 1 起 → 所有流的 `round_1.json`/`round_2.json` 原子 rename **互相覆盖**。archive 默认开（`ArchiveConfig.IsEnabled()` 无 Enabled 时返 true），已由 `cmd/ares/serve.go:129` `NewCompactableStoreWithArchive` 接线 | **高**（生产数据丢失） | ✅ **已修（第八轮，见上）**：StreamID + 每流子目录 + reader 跨流扫描。 |
| 51 | `ares_evolution/service/service_bridge.go:112` `toAPILineage` 是**空桩**（`return StrategyLineage{}`）：`Service.Lineages()`（service.go:589）/`collectLineages()`（:720）把每条真实 lineage 转成零值 → 服务层谱系 API 返回 N 条空记录，genealogy 上报链静默失效 | 中（正确性） | ⚠️ 已记录，未修 |
| 52 | `ares_evolution/service/service_bridge.go:30` `apiGuidanceBridge.RecordStrategyOutcome` 是 **no-op**（`return nil`）：API 层 GuidanceProvider 接入时每个 strategy outcome 被丢弃 → 经验学习反馈回路（outcome→未来变异偏置）在此路径死掉，而 `HintsForTask` 已实现 → hints 被读但从不被强化 | 中（正确性） | ⚠️ 已记录，未修 |
| 53 | `ares_evolution/dream_cycle_ga.go:93` GA `scoreImprovement` **恒为 0**：`best:=population.BestStrategy()`（返回 bestEver 克隆）`- population.BestEverScore()`（bestEver.Score）→ 同值相减恒 0，传导到 lineage ChildScore + shadow eval `RecordResult(parent,winner+parent)` → GA 路径 shadow 评估永远记为平局/负 | 高（GA 失效） | ✅ **已修（第八轮，见上）**：cycle 前捕获 `prevBestEverScore` 作基线。 |
| 54 | `ares_evolution/service/service.go:466/491` lineage 二次方重复增长：`collectLineages()` 每代返回**全量**累积谱系，循环里每代 `append` → G 代后 `result.Lineages` 为 O(G×total) 重复膨胀 | 中（无界增长） | ⚠️ 已记录，未修 |
| 55 | `ares_evolution/genome_wiring.go:342`（+dream_cycle.go:384、dream_cycle_ga.go:49）data race on `Population.Agents`：`PopulationSize()` 无锁读 `len(a.pop.Agents)`，而 `Evolve`/`EvolveAfterScoring` 持锁重赋值 `p.Agents`。`PopulationSize()` 经 `scheduler.checkGuardrails`（`OnAgentEnd`）可与 `adapter.Run` 并发 | 中-高（data race，`-race` 可验） | ⚠️ 已记录，未修（改用 `pop.Snapshot()`/`Stats().Size`） |
| 56 | `ares_evolution/genome/promotion/promoter.go:44/559` `DefaultPromoter` 无界增长：`history` append-only 无 prune、`strategies` 从不移除退休策略（仅 `ScoreHistory` cap 20）→ 长跑 GA 每代新 strategyID 时两 map 无界增长 | 中（无界增长） | ⚠️ 已记录，未修 |
| 57 | `ares_evolution/genome/adaptive.go:608` `rng.Intn(max(iVal,1)+iVal)` 在 `iVal<=-1` 时参数 `<=0` → **panic**；负 int 参数经 `injectFreshMutantsLocked`（population_guard.go:105）可达，stagnation reset 克隆时崩（在持 `p.mu` 的 `doEvolve` 内无 recover） | 中（panic 风险） | ⚠️ 已记录，未修 |
| 58 | `ares_evolution/genome/population.go:588` `ScoreAgents` 并发下静默丢分：snapshot 后写回按 `agent.ID==agents[i].ID` 校验，并发 `Evolve` 改了 `p.Agents` 则校验失败 → 分数被静默丢弃无错无警 | 低-中 | ⚠️ 已记录，未修 |

**本轮 agents 内部结论（大量开放回路 + 2 个潜伏正确性缺陷）：**

| # | 缺陷 | 严重 | 状态 |
|---|------|------|------|
| 59 | `agents/lease/lease.go:41` `leases` map 永不 prune 过期项（`Get` 视过期为不存在但不删）；`Count`(:132) 计入过期项虚高 → 大量短命 sessionID 场景无界增长 | 中（无界增长） | ⚠️ 已记录，未修 |
| 60 | `agents/actionlog/actionlog.go:35/53` `entries` append-only 无 cap + 每次 `Append` 全量线性扫描去重 → O(N²) + 内存泄漏（当前因 actionlog 未接线暂无害，见下） | 中（潜伏） | ⚠️ 已记录，未修 |
| 61 | `agents/sub/agent.go:441` `recordAction` 写 actionlog 时 `SessionID` 恒空（`models.Task` 无 session 字段）→ `List`/`Replay` 按 session 过滤永远匹配不到；即使 actionlog 接线，审计/回放对任何非空 session 返回空 | 中（潜伏正确性） | ⚠️ 已记录，未修 |
| 62 | `agents/lease/lease.go:79` `Renew` 不查 `ExpiresAt` → 可复活已过期租约（另一 worker 已有权 Acquire 时）；`Release`(:99) 同样不查过期 | 低（Renew 无生产调用） | ⚠️ 已记录，未修 |
| 63 | `agents/profile.go:53` `Register(nil)` 会 nil-deref panic（无 nil 守卫，对比 peer/actionlog 都校验输入） | 低 | ⚠️ 已记录，未修 |

**agents 层开放回路（不单列编号，归入 register-but-never-wired）**：
- `cmd/ares/serve.go:264` 构建 peer.Registry 后**丢弃返回值** → `peer.Registry.Send`/`Lookup`/`Unregister` 全零生产调用，"peer-to-peer 消息"表面不可达（registry 仅用于打印计数）。
- `agents/sub/agent.go:105` `WithActionLog` 零调用（含测试）→ actionlog 整包生产死码（`Store.List`/`Replay` 也零非测试调用）。
- `agents/handoff.go` 整文件死码（`NewHandoff`/`WithContext`/`WithArtifact` 等零非测试调用；IPC 层 `Bus.Handoff` 是无关类型）。
- `agents/profile.go:153` `ApplyToContext` 零生产调用 → `GetFromContext` 恒返 nil，`activeRoleInstructions` 恒空，role 切换是 no-op；`NewProfileRegistry`/`DefaultProfiles` 亦零生产调用。
- `agents/lease/lease.go:79` `Manager.Renew` 零生产调用（kernelscheduler/taskfabric 的 `Renew` 是不同类型）。
- **确认真正接线且工作正常**：`outputguard/guard.go`（`sub/agent.go:407` 每次 finalize 调用，无绕过）；`base/agent.go`（`StatefulAgent`/`SnapshotStore`/`Config` 有真实消费者）。

**ares_evolution 其他开放回路（归入既有结论）**：
- `service`：`NewMutationAdapter`/`WithAdapterAdaptiveDistribution`/`WithAdapterFeedbackRecorder`/`WithActiveStrategyManager`/`WithAdapterMetrics` 零生产调用（对应字段走直接赋值）；`ActiveStrategyManager.Rollback`/`RollbackPolicy.Evaluate`（趋势降级检测）零生产调用——deploy-time 自动 rollback（guardrails）已接，但趋势 rollback 是开放回路。
- `genome/mutation`：`NewKnowledgeDistiller`/`KnowledgeAdapter.SuggestMutation`/`refine.NewRefiner` 整包/`experience.NewDefaultEvidenceAggregator` 零生产调用；`Reflector`→`HypothesisGen` 已接但 `ApplyHypothesis` 零生产调用 → 反思→假设→变异是半接回路（假设被产出计数却从不应用）。
- 低危：`genome/adaptive.go:608` 外的 `context.Background()` 多为 logging；`mutation/guided_mutator.go:395` child 复制 parent `CreatedAt`（时间戳陈旧）；`meta_evolution.go:227` `DecisionHistory` append-only；`experience/store.go:166` `RetentionDays` 定义但零读取（保留策略 no-op）。

**ares_observability 复核（确认前轮结论 + 补 4 个潜伏 bug）**：`PrometheusMetrics`/`CostDashboard`/`OTelTracer`/`LogTracer` 仍全零生产构造（仅 `NoopTracer` 接线；`RegisterMetricsRouter` 接了但 `/metrics` 只暴露 Go runtime 默认，无 `ARES_*` 指标）。潜伏 bug（接线前需修）：`prometheus.go:158` `cachedMetrics` 无锁 data race；`prometheus.go:97` `CostUSDTotal` 按 session 打 label → 无界 cardinality 爆炸；`cost.go:264` `CostDashboard.sessions/order` 无界增长；`cost.go:219` `Reset` 保留底层数组。

**ares_archive 复核**：除 #50（HIGH，正在丢数据）外——`reader.go:137` 单个损坏 round 文件使整个 `Search`/`Recall` 失败（应跳过）；`writer.go:97` `writeAtomic` rename 不 fsync，硬崩溃下"归档先于压缩丢弃"的持久性保证不成立。其余（`w.mu` 保护、临时文件清理、identifier regex 不可变、`ProtectIdentifiers` 拷贝）干净。

**第七轮贯穿性结论**：累计 **63 项 = 19 已修（+#49）+ 2 部分修（#7、#12）+ 42 待处理**。本轮把全部主要生产模块深读完毕（含最大的 ares_evolution）。新揭 **#50 是继 #33/#36 后第三个 CRITICAL 级实际影响**——archive 默认开启且正在**静默丢失归档数据**（跨流 round 覆盖），建议紧随 #36 修复。ares_evolution 的 service 层暴露一组**桩函数/恒零缺陷**（#51/#52/#53），说明 API service 层与内部 wired 系统之间的 bridge 未真正实现——这是"接了但桩空"的新型缺陷，比开放回路更隐蔽。至此规律完整：**真 bug 分三类——(a) 热路径逻辑错（NSGA-II/failover/llm_adapter/RestartAgent，已修）、(b) 桩空/恒零 bridge（evidence UUID、service lineage/outcome、GA improvement）、(c) 跨流/跨租户的共享资源冲突（archive round 覆盖、tenant 隔离失效）**；其余绝大多数是开放回路。

**贯穿性结论**：全部 14 项 = **10 已修（#1-#6、#8、#9、#10、#11）+ 2 部分修（#7、#12）+ 2 未修（#13、#14）**。仍开放/部分开放的项清一色是"装配/注册层"问题——组件被实现、被测、甚至被引用，但缺少一个逻辑上的生产消费/构造方。代码库在生命周期/循环/goroutine/事件消费层**极其规整**（errgroup/WaitGroup/ctx.Done/ticker.Stop/panic-recover 全覆盖），在纯数据流与纯类型层也干净（compiler/pipeline/provider/knowledge/eval/protocol 等）。真正残留的缺陷集中在: register-but-never-consume / start-but-never-read / adapter-never-constructed。

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

### ✅ #11 SKILLS 渐进披露 + 工具能力搜索 — 已修（第三轮补齐两半，2026-08-25）
- **上半（渐进披露，已生效）**：`skills_wiring.go` 的 `wireSkills`（`bootstrap.go:265`，serve 路径）构造 `ares_skills.NewCatalog(...)` + `catalog.Build()` + `skills.NewRegistry()` + `catalog.SeedRegistry(reg)` + `setter.SetSkillsRegistry(reg)`（type-assert `skillsRegistrySetter`）。memory manager 的 "Available skills" 渐进披露块在 serve 已被填充。Catalog Close 已挂 cleanup。`wireSkills` 现返回 `(*Catalog, *skills.Registry)`，seed 出的 registry 存入 `comp.SkillsRegistry` 供下半复用。
- **下半（工具能力搜索，本轮补齐）**：
  - `internal/tools/envcap` 新增 `RegistryLister`（把 `*core.Registry` 的 `List()`+`Get()` 适配成 `ToolLister`，每次取快照，Get 竞态返回 false 时跳过）与 `SearchTool`（`search_tool.go`，包装 `Searcher` 为 `core.Tool`，名 `search_capabilities`，参数 `query`+可选 `limit`，默认上限 20；错误走 `Result.Success=false` 而非 transport error）。
  - `registerCapabilitySearch`（`tools.go:107`）用 `envcap.NewSearcher(NewRegistryLister(internalReg), comp.SkillsRegistry, nil)` 构造并 `internalReg.Register(tool)`；在 `serve.go:236` 于 `newToolBinder` **之前**调用，使工具自然流入 agent 工具集。
  - **两源而非三源（关键修正）**：native command 源传 `nil`。因为 `registerNativeTools`（`serve.go:227`）已先把每个白名单命令作为 `CommandTool` 注册进同一 registry，经 `RegistryLister` 以 `KindTool` 呈现；若再给 envcap 传一个 `discovery.NewDiscoverer` 会导致**双重列举** + **每次搜索重探主机**（`command -v`/`--help`）。envcap 包注释已同步说明该设计。
  - `comp.SkillsRegistry` 为 nil（技能禁用）时 Searcher 跳过该源，无 panic。
- **状态**：完全修复。渐进披露块 + `search_capabilities` 工具两半齐活；envcap 零生产调用问题消除。测试：`search_tool_test.go`（含 RegistryLister/limit/nil-skill/require-query）+ `skills_wiring_test.go`（registry 回传断言）。

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

### 🟡 #12 "演化内核"适配器 serve 装配 — 部分已修（2026-08-25 二次核实纠正）
- **纠正**：先前记录"四个适配器全零构造"已过期。核实 `cmd/ares/serve_agents.go`，GA 执行侧**已分三阶段接线**：
  - **stage-1 quota**（`serve_agents.go:97-99`）：`NewEvolutionAwareQuotaManager` + `runKernelQuotaLoop`，GA budget → fabric P5 admission。无害（`UpdateResourceBudget` 原地替换）。
  - **stage-2 spawn gate**（`serve_agents.go:112-116`）：`NewEvolutionAwareSpawner` + `recovery.WithSpawner`。**刻意只作用于 recovery 替换 spawn**（`SpawnForRecovery` 跳过 MaxConcurrent，避免自愈被配额卡死），不杀 agent。
  - **stage-3 population**（`serve_agents.go:129-133`）：`NewPopulationAdapter` + `RunKernelEvolutionLoop`，GA 拓扑 → fabric spawn/retire。retire 是优雅退休非强杀。
- **仍缺（危险项）**：`Chaos`（`chaos.go`）+ `Sandbox`（`sandbox.go`）零生产构造。`Chaos.InjectFailure`（`chaos.go:60`）会 `agents.Kill`（真实从 registry 删 agent）/`Suspend` **活跃 agent** → 干扰生产。
- **方案**：走 Shadow Sandbox 默认（`NewSandbox` 独立 scratch fabric，生产零影响）+ Live Chaos 可选（默认关 + 多重护栏 + GA 静默窗口 + 急停）。详见 `plan/0.3.1plan/chaos_isolation.md`。
- **状态**：部分修复（quota/spawn/population 已接且无害；chaos/sandbox 待 0.3.1 按 shadow-first 方案接线）。

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
| are_skills | ✅ 已接入 serve | outcome_recorder.go 订阅→consume→Record panic 恢复（✓）；`ares_skills`/`skills.Registry` 现经 `wireSkills`(#11 上半) 接入 serve 运行时，并经 `envcap.Searcher`(#11 下半) 供 `search_capabilities` 工具检索 |
| aresrecovery | ⚠️ chaos/sandbox 待接 | 两个 ticker loop（pop:140 / exec_feedback:332）ticker.Stop+ctx.Done+panic-recover（✓）；quota/spawn/population 三阶段已接（#12），仅 chaos/sandbox 待 0.3.1；观测面(bootstrap)与 W4(peer_mode) 已接 |
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
| monitoring/dashboard | ✅ 逻辑干净（tabs 自封顶、publisher/collector ctx 受管），但 DAG 节点无界增长 #14 + 两套子系统臃肿 → 规划 0.3.1 重设计（`plan/0.3.1plan/monitoring.md`）。 |
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

---

## 第八轮收口（2026-08-25）：累计状态 + 0.3.0 合并就绪

**累计 65 项 = 21 已修 + 2 部分修（#7、#12）+ 42 待处理。**

**已修（21）**：#1-#6（首轮）、#8-#11（二次核实确认已接线）、#19-#23（第五轮热路径）、#33-#35（第六轮 evidence）、#49（NSGA-II）、#50 + #53（第八轮两个 P0）。

**部分修（2）**：#7（experiences 已接，余 4 表待接，与 #13 绑定）、#12（quota/spawn/population 已接，chaos 走 Shadow 方案）。

**待处理（42）**：全部落入 `plan/0.3.1plan/outstanding_tasks.md`，按 P0/P1/P2/P3 排期。0.3.1 的 P0 剩 **#36 多租户隔离**（方案 `tenant_isolation.md` 已议定 B+C，Phase 3 改 DB 角色需签字）。

**0.3.0 合并说明**：本轮所有已修项均 `go build ./...` 0 / 改动包 `go test` + `-race` + `go vet` 0 / gofmt 干净。已修的 #50（archive 丢数据）与 #53（GA 反馈失效）是当前 serve 路径的实际缺陷，已闭合。剩余待处理项以**开放回路**（实现+测试但未接生产，不影响当前运行）与**需设计决策的接线/迁移**（#36 等）为主，不阻塞 0.3.0 合并——它们是 0.3.1 的工作面，已完整记录在 `plan/0.3.1plan/`。

**0.3.1 plan 文档清单**（`plan/0.3.1plan/`）：
- `outstanding_tasks.md` — 全部待办总表（P0-P3 + 开放回路清单 + 缺陷类型总结）
- `tenant_isolation.md` — #36 多租户隔离 B+C 方案（Phase 3 需签字）
- `monitoring.md` — 运行时检测面板重设计（含 #14）
- `chaos_isolation.md` — #12 chaos Shadow-first 隔离方案
- `async_embedding_pipeline.md` — #13 异步 embedding 队列决策
