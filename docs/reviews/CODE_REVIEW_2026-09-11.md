# ARES 全库深度 Code Review 报告

> 生成日期：2026-09-11
> 审查范围：188 包 / 1473 个 Go 文件（`cmd/` · `sdk/` · `api/` · `compat/` · `internal/**` · `services/`），逐模块覆盖。
> 基线：HEAD `2aeaf942` + 20 个未提交改动。`go build ./...` **通过（exit 0）**。
> 方法：9 路并行静态深审（语义导航 + 全仓引用计数），对高危项逐一回到源码取证。
> 证据标注：`[已核实]` = 复核过源码；`[评审]` = 带 file:line 证据、未二次复核。
> 本次为**只读审查，未改动任何源码**。

---

## 一、总体结论

工程质量整体很高：编译干净、架构不变量有 `architecture_test.go` 测试锁定、反模式扫描（`panic`/`fmt.Println`/`ioutil`）近乎为零。**但存在两类系统性问题：**

1. **【未闭合模块】批量“单侧接线”**：大量功能只有生产者无消费者（或反之）、默认关闭但文档当作已启用、声明未赋值的状态/字段。最典型的是**进化信任根旁路**、**agent RUNNING/IDLE 状态机空转**、**token 预算形同虚设**。
2. **【真实 bug】集中在并发与数据一致性**：shutdown 丢数据、`-race` 级数据竞争、SSE 相对端点、nil 解引用 panic、经验检索返回空 Content。

按模块统计（去重后）：**critical/high 约 28 项、medium 约 45 项、low ~30 项**，其中**未闭合类约占 40%**。

---

## 二、P0 / P1 关键缺陷（建议首批处理）

### 2.1 并发 & 数据一致性

| # | 严重度 | 位置 | 问题 |
|---|---|---|---|
| 1 | 🔴 high `[已核实]` | `internal/storage/postgres/write_buffer.go:134-149` | **优雅关闭丢数据**：`ctx.Done()` 分支只 flush 已累积的 `batch`，仍排在 `b.buffer` 通道里的条目被直接遗弃（`ProductionMemoryManager.Stop` 先 `cancel()` 再 stop，必然命中该分支）。 |
| 2 | 🔴 high `[已核实]` | `internal/storage/postgres/write_buffer.go:188-219` | **最终 flush 失败被当成功**：`flushBatchFiltered` 在**所有路径都 `return nil`**（198/209/217/219 行），因此新加的 `if len(leftover) > 0` 是**死代码**。本次未提交改动本想强化“incomplete”检测，反而把原先 `err != nil` 的有效检查改没了——**这是当前工作区引入的回归**。 |
| 3 | 🔴 high `[已核实]` | `internal/storage/postgres/write_buffer.go:247-267` | **send-on-closed-channel panic**：`requeueItems` 先读 `b.stopped.Load()` 再向 `b.buffer` 发送，但**不持有 `b.mu`**，与 `Stop()` 关闭通道形成 TOCTOU，flush 失败时会崩进程。 |
| 4 | 🟠 high `[已核实]` | `internal/runtime/memory/manager_impl.go:493` vs `:82-85` | **数据竞争**：`SetSkillsRegistry` 持 `m.mu.Lock()` 写 `skillsRegistry`，而 `BuildContext` 裸读该字段（同文件其它字段都做了 RLock 快照）。`-race` 可复现。 |
| 5 | 🟠 high `[评审]` | `internal/runtime/manager.go:487` | **复活路径数据竞争**：`stopOldRestoredAgent` 在 Unlock 后读 `oldMA.cancel/.agent`；`RestartAgent` 已在锁内快照并注释说明原因，此处漏改。 |
| 6 | 🟠 high `[评审]` | `internal/runtime/ares_evolution/genome/population.go:682` | **持写锁调用外部 scorer**：`ScoreAgentsMulti` 在 `p.mu.Lock()` 内调 `scorer(agent)`（可能是 LLM/网络调用），而 `ScoreAgents:604` 明确“拷出后锁外评分”。阻塞全部读者，且 scorer 回调 `Stats()` 会自我死锁。 |
| 7 | 🟠 high `[评审]` | `internal/fabric/planprojection/coordinator.go:510,576` | **增量编译器读到活指针**：`GraphChange.Step` 发布的是 DAG 内部 `*Step`，而 `AddEdge`/`SetNodeMetadata` 会原地改它 → 投影协程锁外读取即 `-race` + 依赖列表撕裂。 |
| 8 | 🟠 medium `[评审]` | `internal/ares_shutdown/signal.go:96` / `manager.go:200` | `h.ctx` 无锁读写（竞争）；`Shutdown` 每个 phase 新建 `m.wg.Wait()` goroutine，超时后 `WaitGroup` 不复位 → 回调卡死则整个关闭流程永久阻塞。 |

### 2.2 安全面

| # | 严重度 | 位置 | 问题 |
|---|---|---|---|
| 9 | 🔴 high `[已核实]` | `cmd/ares/agent.go:205-207` + `internal/introspect/api.go:20-29` | **只读面板 fail-open**：未配置任何凭据时 `checkAuthRead` **无条件 return true**（无 loopback 判断）；`/api/v1/introspect/eventstream` 返回原始事件全量 payload（任务输入、checkpoint）。`serve.go` 对 wildcard host 仅 `log.Info` 告警不阻断。`--host 0.0.0.0` 无凭据即全量暴露。 |
| 10 | 🟠 high `[已核实]` | `internal/fabric/agent/l2graph.go:404` | **serve 路径工具调用不盖身份章**：`toolCognition.ExecuteStep` 直接 `binder.CallTool(ctx, …)`，全仓 `kctx.WithCallerID` 仅 `agentloop/engine.go:453`（SDK 路径）调用 → serve 下 `spawn_agent` 回退到 **LLM 传入的 ParentID**（可伪造）、`create_task` 的 `Origin=""`。安全属性只对 SDK 成立。 |
| 11 | 🟠 medium `[评审]` | `internal/storage/postgres/migrate_storage.go:45-50 等` | **RLS 策略休眠**：迁移为每张租户表建了 `ROW LEVEL SECURITY` + `current_setting('app.tenant_id')`，但唯一写入 GUC 的 `QueryWithTenant` **无生产调用者** → 策略要么被属主绕过、要么恒返回空，多租户隔离实际全靠 WHERE。 |
| 12 | 🟠 medium `[评审]` | `internal/runtime/protocol/mcp/transport_server.go:248,324` | MCP HTTP/SSE 服务端**无鉴权**，且 SSE 通告 URL 硬编码 `http://`（裸 `:8080` 时无 host）。 |

### 2.3 真实功能 bug

| # | 严重度 | 位置 | 问题 |
|---|---|---|---|
| 13 | 🔴 high `[已核实]` | `internal/apitools/builtin.go:720` | **nil 解引用 panic**：`info, _ := e.Info()` 后直接 `info.Size()`；文件在 `ReadDir` 与 `Info` 间被删/无权限即 `nil.Size()` 崩溃（`Registry.Execute` 无 recover）。核心 `file_tools.go:528` 已正确 `continue`。 |
| 14 | 🔴 high `[评审]` | `internal/apitools/builtin.go:347,353` | 遗留 `regexTool` 用 `FindAllString(text, -1)` **无上限**，空匹配模式在 1MiB 输入上产出百万级结果——正是 `text/regex_tool.go` #62 已修的问题，但生产注册的是这个旧实现。 |
| 15 | 🔴 high `[已核实]` | `sdk/discovery.go:52,59` | **开启工具发现即丢 syscall 工具**：`spawn_agent/create_task/ask_agent/create_plan` 只在 `legacy()` 分支（:36）被 append；discovery 成功/失败两条路径都不补，与 :24-27 文档承诺矛盾 → agent 无法自主分解/派生。 |
| 16 | 🔴 high `[已核实]` | `internal/mcpclient/sse.go:71,102,178` | **SSE 相对端点未解析**：`data: /messages` 原样存入 `messageURL`，`http.NewRequest` 报 “unsupported protocol scheme”，连接建立但每次请求失败（包内测试正是相对端点）。 |
| 17 | 🟠 high `[评审]` | `internal/storage/postgres/repositories/experience_repository.go:258-341` | **默认开启的经验排序返回空 Content**：`SearchByVector` 只填 `Input/Output`，`applyExperienceRanking` 却读 `Problem/Solution` → `Content: exp.Solution` 恒空。`DefaultRetrievalPlan().ExperienceRankingEnabled=true`，整条排序分支产出不可用结果。 |
| 18 | 🟠 high `[评审]` | `internal/runtime/protocol/mcp/manager.go:233` | **重连后工具指向已关闭 client**：换新 client 前未 `unregisterTools(stale)`，同名工具注册被跳过 → 注册表残留旧 client 的 tool，`Close` 后永久失效且无法卸载。 |
| 19 | 🟠 high `[评审]` | `internal/runtime/arena/regression.go:605,560` | **平局计为胜**：`newScores[i] >= oldScores[i]` 使全等策略 `WinRate=1.0 ≥ MinWinRate` → `NewBetter=true`；配合 `:638` 零方差时 `Confident=true`，"完全相同的策略"被判为显著回退/更优。 |
| 20 | 🟠 medium `[评审]` | `internal/ares_events/compactor.go:324,335,523` | **按字节截断 UTF-8**：`input[:200]`/`content[:200]` 切断多字节 rune → 非法 UTF-8 写入 PG `text` 列报错，compaction 永久失败、事件继续膨胀。仓库已有 rune-safe 的 `internal/truncate`。 |
| 21 | 🟠 medium `[评审]` | `internal/storage/postgres/repositories/strategy_repository.go:159-177` | `SetActive` 的 INSERT **无 ON CONFLICT**，重新激活/回滚已知策略必然唯一键冲突并使整个事务回滚 → 回滚功能失效。 |
| 22 | 🟠 medium `[评审]` | `cmd/ares/evolution.go:520,547` | `ask_agent` 在 `Bus.Send` 的**同步 handler 内**跑最多 10 分钟的会话等待，违反 `Send` 非阻塞契约；并发下耗尽调度并发 → 嵌套会话无执行者 → 互锁至超时；且 `Send` 路径丢弃最终答案只付延迟。 |

---

## 三、未闭合 / 半接线模块台账（本项目最系统性的债务）

| 模块 | 类型 | 证据 |
|---|---|---|
| **Agent 状态机 RUNNING/IDLE 空转** | producer-less | `fabric/agent/lifecycle.go:352,370` `SetRunning/SetIdle` **零生产调用**；`IsIdle` 实际恒真 → `drainLimit/fabricCandidateCount` 高估空闲 agent。真正准入靠 `LoadTracker` 兜底。 |
| **token 预算 / ResetResource** | 半接线 | `kernel/scheduler.go:228` `ConsumeResource(winner, 0, 1)` token 恒 0 → token 上限永不生效；`fabric/agentfabric/governance.go:131 ResetResource` 无生产调用；`Deadline` 一旦耗尽不可恢复。 |
| **Strategy DEGRADED 状态** | 声明未用 | `ares_evolution/lifecycle.go:40-53` 枚举存在、`String()` 映射，但全仓非测试代码**从不赋值**（回滚直接置 `ACTIVE`，`:1046`）。 |
| **DreamCycle 旁路信任根** | 第二 promote 路径 | `ares_evolution/dream_cycle.go:625` 直接 `stateManager.Deploy` 绕过 G1/G2/G3/回归门；`ShouldDeployLoose` + “样本不足也部署” fail-open。默认 `EnableDreamCycle=false`（`bootstrap_steps.go:225`），但一旦开启即生效。 |
| **`promotion` 晋升状态机** | 仅日志 | `ares_evolution/service/service.go:294` 只 `log` promoter 结果，`DefaultPromoter.Promote/Demote`、champion 列表、冷却逻辑**零部署权**（约 650 行）。 |
| **进化 lineage/checkpoint** | 写侧缺失 | 默认生产路径 `GenomePopulationAdapter.Run`（`genome_wiring_run.go:46`）从不调 `RecordPopulationLineage`；唯一写入者 `RunIdleEvolution` 无生产调用。 |
| **memory 事件驱动蒸馏** | 无接线 | `distillation/distiller_admin.go:55 SubscribeAndDistill` 仅测试调用；`OnTaskCompleted/OnMessageAdded` 生产无赋值 → 消息/任务事件永不自动蒸馏。 |
| **arena flight recorder** | 无接线 | `arena/http.go:43 SetFlightRecorder`/`SetFlightBridge` 无生产调用 → `/arena/flight/*` 恒 503，`integration.go` 的 FlightBridge 只在测试生效。 |
| **evidence 收集（memory）** | 无接线 | `memory/production_manager.go:290 SetEvidenceCollector` 无调用者 → `StoreDistilledTask` 永远不进统一 Evidence Store。 |
| **会话租约 / 多租户** | 无接线 | `manager_impl.go:101,803` `SetLeaseManager/AcquireSessionLease`、`SetDefaultTenantID` 仅测试引用 → `tenantID` 恒 `"default"`，租户隔离名存实亡；且写入用 `payload["tenant_id"]`（:652）而检索用 `defaultTenantID`（:753）**不一致，自写自检索不到**。 |
| **`RouterPlugin` 子系统** | 整包死码 | `runtime/router.go` + `router_fallback.go` 仅自测引用；`RouteState.Collector` 从不赋值；`architecture_test.go` 只查 `New*Plugin` 漏掉 `New*Router`。 |
| **capability 服务发现** | 死码 | `runtime/bus.go:307 PluginsByCap` 仅测试调用；`CapRouter/CapTool/CapRecovery/CapInterrupt` 无消费者。 |
| **allowlist 死插件** | 死码 | `runtime/architecture_test.go:48` 自述 `NewInterruptPlugin/NewObserverPlugin/NewBasicRecoveryPlugin/NewToolPlugin` 零生产引用；`startPluginBus` 丢弃 `store` 参数。 |
| **`RecoverSnapshotOrEvents`** | 死导出 | `runtime/recovery.go:75` 全仓零调用，且 `manager.go:518` 内联了同逻辑。 |
| **embedding 死信清理 / 迁移** | 无接线 | `embedding_queue.go:606,646 PurgeDeadLetters/RequeueDeadLetter` 无调用 → dead_letter 表无界增长、Reconcile 扫描劣化；`migrate_eval.go:54`/`migrate_evolution.go:63` 从不被任何 bootstrap/CLI 调用，`eval_results`/`evolution_lineages` 是 schema 幽灵。 |
| **`VectorIndex` 可插拔 ANN** | 文档虚构 | `knowledge/vector_index.go:26` 全仓无 store 引用，README 却称“Stores delegate recall to a VectorIndex internally”。 |
| **`ServiceAdapter.Query` 桩** | 恒空 | `knowledge/service/adapter.go:89` 无条件 `return nil, nil`，忽略所有过滤条件，却实现公开 `knowledgeapi.KnowledgeService`。 |
| **AgentProfile/role 系统** | 死模块 | `agents/profile.go` 自述“zero production callers”，`ProfileRegistry/DefaultProfiles` 仅自测。 |
| **introspect feedback sink** | wired-but-dead | `introspect/control.go:75` 字段注入但无 handler 读取，POST 硬返 405。 |
| **`discoveryapi` / `evoapi`** | 无消费者 | 两者仅 `examples/_fixtures` 与 `test/apifwd` 引用（`discoveryapi` 甚至是对 `internal/discovery` 的纯转发）。 |
| **compat/** | 整树 | 自证零生产引用；`compat/tool/builtin` 是 `Noop` 占位。 |
| **shadow 执行钩子 / strategy_shadow** | 无消费者 | `kernel/shadow.go:29 WithShadowExecutionHook` 仅测试；`shadow_executor.go:41` 写入的 `strategy_shadow` 证据无读者。 |
| **`SetSchedulerType`** | 不可达 | `mutable_dag.go:48` 仅测试调用；生产者走的是 `graph.PatchChangeScheduler`（不同类型）→ 随机排序分支不可达。 |
| **`api/` 转发层 / `agents/sub` 死面** | 待下葬 | 见 M5 收敛计划；`buildPeerRegistry`（`serve.go:1308`）因 `SendMessage` 已删除而**永远注册不到人**。 |

---

## 四、本次未提交改动（20 文件）的 Review

方向整体正确（身份/TOCTOU/资源泄漏修补），但发现 **1 处新引入的回归 + 3 处残留**：

1. 🔴 **`write_buffer.go` 回归** `[已核实]`：新引入的 `flushBatchFiltered` **永远返回 nil**，使 `if len(leftover) > 0` 的“incomplete”检测成为死码——比改动前的 `err != nil` 更弱。建议让它真实返回失败条目，或在失败时返回显式 error。
2. 🟠 `write_buffer.go:134` `[已核实]`：`ctx.Done()` 仍未 drain `b.buffer` 通道（见 P0-1）。
3. 🟠 `write_buffer.go:248` `[已核实]`：`requeueItems` 仍非持锁发送（见 P0-3）。
4. 🟠 `mcpclient/sse.go` `[已核实]`：本次只加了 scanner 上限与 drain 日志，**相对端点解析问题未修**（见 P0-16）；`internal/runtime/protocol/mcp/transport_sse.go:760` 新增 `time.After` 握手超时（未 Stop，触发前泄漏一个 timer，影响很小）。
5. 🟡 `ares_events/compactable_store.go` `[评审]`：把 drain 失败从 best-effort 改为硬 defer 方向正确，但作者自述 drain-vs-trim TOCTOU **只是缩小窗口未消除**（claim 是标志位而非跨临界区的锁）。

✅ 已验证无误的改动：`fabric.go` `flushedSeq` 单调推进、`orchestrator.go` Stopped 不覆盖、`scheduler.go` `Forget` 前置 `Load==0`、`sdk/scheduler.go` `AfterFunc` stop 泄漏、`reloader.go` 瞬时解析失败保留 last-good、`l2graph.go` O(1) 查询（`ReadDeps/HasNode/AgentTypeOf/CountByAgentType` 均已存在，编译通过）。

---

## 五、分模块补充发现（medium / low）

### 5.1 kernel + fabric
- `[评审]` `fabric/agent/session_registry.go:157`：`ReleaseSession` 持写锁等待订阅 goroutine 退出（最长 ~30s）→ 并发 `GetSession` 队头阻塞。
- `[评审]` `fabric/agent/l2graph.go:491`：answer 失败路径不 `ReleaseSession`，仅靠外部 `task.failed` 订阅 + 30min sweep 兜底。
- `[评审]` `kernel/scheduler.go:977`：未来 schema checkpoint 解码失败仅 Warn 后按当前版本重编码，**有损丢弃 UserProfile/Payload**。
- `[评审]` `mutable_dag.go:172` vs `:301,489`：`AddNode` 存调用方指针，`AddEdge/SetNodeMetadata` 原地改，与“深拷贝隔离”契约矛盾。
- `[评审]` `kernel/scheduler.go:105`：`quantumHook` 注释“Guarded by execMu”但所有 `With*` setter 不加锁；`fabric/task/fabric.go:920` cond-wait 的 `AfterFunc` 广播存在丢唤醒窗口。

### 5.2 runtime core / protocol
- `[评审]` `manager.go:416` `RestoreAgent` 缺 `isStopped` 守卫；`manager.go:240` `StartAgent` 提前 return 泄漏 `agentCancel`；`manager.go:56` agent 启动顺序 map 非确定。
- `[评审]` `bus.go:100` `PluginBus.Start` 无重复启动守卫（重复订阅泄漏 goroutine）；`protocol/mcp/factory.go:127` 显式 TODO：子进程无回收；`config_watcher.go:99` 死 `done` 通道 + debounce timer 未 Stop。
- `[评审]` `protocol/ahp/queue.go:79` `Peek`+`Dequeue` 并发时可能永久阻塞。

### 5.3 evolution
- `[评审]` `scoring/memory_aware_scorer.go:389`：最终分可为负，被 `IsScoreEvaluated`（≥0）当作“未评估”→ 坏策略被排除出 best-ever。`multi_objective.go:214` 明确规避了这点。
- `[评审]` `genome/population.go:360`：填充循环可能二次 clone 已入选 elite → 重复 ID，破坏 ID 匹配回写/lineage/黑名单。
- `[评审]` `gate_eval.go:213` / `evolution/candidate_pipeline.go:182`：EvalGate 非严格默认、release 回归门可选 → 类型零值 fail-open（bootstrap 已覆盖，但类型本身不安全）。
- `[评审]` `guardrails.go:483`：基线回退日志打印 `g.BaselineScore` 而非实际生效的 `bestKnown`。

### 5.4 runtime services
- `[评审]` `arena/scenario.go:267`：注释声称“不用全局 Stats”，实际 `CalculateScoreV1(service.Stats())` 仍取跨运行累积值 → 重复跑场景分数被历史污染。
- `[评审]` `arena/regression.go`、`observability/metrics_tracer.go:62`：每次调用伪造 traceID，成本看板按 session 聚合失效；`flight/genealogy.go:119` 旧根被驱逐后新节点成孤儿；`memory/production_manager.go:567` `MaxHistory=0` 清空全部历史；`memory/memory_patcher.go:129` JSON `float64` 断言 `int` 静默 no-op；`arena/service.go:77` nil Injector panic。

### 5.5 storage / evidence / embedding
- `[评审]` `write_buffer.go:315` `Write` 持锁最长 100ms；`embedding_queue.go:206` 卡死 `processing` 行永不回收；`knowledge_repository.go:216` `CreateBatch` 未查 `rows.Err()` 且批量内同 key 触发 “ON CONFLICT cannot affect row a second time”；`evidence/postgres_store.go:79-116` 同纳秒 ID 碰撞静默丢记录；`conversation_repository.go:126` `GetByID` 无租户过滤。

### 5.6 knowledge / tools
- `[评审]` `adapter/distill.go:274`：先 `SaveRepresentation` 再 `FindDuplicate` → 批内重复不去重 + 自匹配把自己置为 Superseded；`pipeline.go:186` entity resolution 从不使用 `MatchedObjectID`（merge 是 no-op）；`sqlite/store.go:425` HybridSearch 先全量载入再截断；`apitools/builtin.go:569` webSearch 响应无大小上限；两套同名 `file_tools` 参数键不一致。

### 5.7 LLM / mcpclient / compat
- `[评审]` `llm/client.go:475`（**已核实** bodyclose 非泄漏）：流式路径恒上报 0 token；`llmservice/service.go:80` 未装 sanitizer（bootstrap 路径有）；`llm/output/openai.go:81` `MaxTokens=0` 未 clamp → 400；`compat/protocol/openai_api/openai_api.go:108` `detectEndpoint` 使 moderation/models 分支不可达、image 请求路由错。

### 5.8 entry / sdk
- `[评审]` `sdk/scheduler.go:148` taskRunCtxs 在 `Create` 后注册（Submit 超时可能打不到执行）；`sdk/sdk.go:437` scheduler goroutine 不在 `Close` join；`sdk/distill_events.go:74` 与 bootstrap 重复订阅同一 EventStore → 经验被蒸馏两次；`cmd/ares/agent_routes_agents.go:28` kill/resume 打到 legacy `Manager` 而非 Agent Fabric（对执行无效）。

### 5.9 infra
- `[评审]` `bootstrap_steps.go:120`：多数后台 worker 用裸 `bgGroup.Go`（errgroup 不 recover panic），唯二带 recover 的是 `embedding_worker.go`；注释却称“every other worker runs under the same boundary”。
- `[评审]` `provide_discovery.go:76`：discovery goroutine 绑调用方 ctx、无 Stop、无 cleanup 注册 → bootstrap 中途失败时泄漏。
- `[评审]` `config_validate.go:127`：`events_retention_days` 无范围校验，负值经 `days<=0` 静默变成“永久保留”。
- `[评审]` `ares_ratelimit/limiter.go:22`：`RefillRate`/`Timeout` 死配置；`ares_events/memory_store.go:295` 订阅缓冲满静默丢事件（仅计数，distillation/skill-outcome 依赖该通道）。

---

## 六、建议修复顺序

1. **立即（安全 + 数据）**：introspect/read 面 fail-closed（#9）、l2graph 身份盖章（#10）、write_buffer 三项（#1/#2/#3）——尤其当前工作区已把 #2 变成死检测，勿先提交。
2. **高置信低风险**：nil 解引用（#13）、SSE 相对端点（#16）、sdk discovery syscall 工具（#15）、经验排序空 Content（#17）、MCP 重连孤儿工具（#18）、compactor rune-safe 截断（#20）、`SetActive` ON CONFLICT（#21）。
3. **按台账清理未闭合模块**：优先决定“接线 or 删除”——Agent 状态机、token 预算、DEGRADED、DreamCycle 旁路、`promotion`、`VectorIndex`、`ServiceAdapter.Query`、embedding 死信清理/迁移、distilled 事件链。删除批次沿用仓库纪律：`grep` 全仓清零 + 独立 commit + `build/vet/lint/test` 全绿。
4. **并发加固**：`skillsRegistry` 读快照、`ScoreAgentsMulti` 锁外评分、planprojection 发布快照、shutdown ctx/WaitGroup 修复。

---

## 七、诚实声明

- 本次为**只读审查**，未改动任何源码；`go build ./...` 在包含未提交改动的当前工作区**通过（exit 0）**。
- 覆盖为逐模块“精读级”，但并发类结论多为静态分析；标注 `[评审]` 的项动手前建议对目标模块补一次 `go test -race` 复现。
- 与既有 `DEEP_CODE_REVIEW.md`（2026-09-08）/ `ARCHITECTURE_DIAGRAM_REVIEW.md`（2026-09-11）是独立批次：本报告对旧台账多项做了复验（如 `fabric/task/restore.go` 类型断言、`llm/client.go` bodyclose 均**已证伪为真 bug**），并新增当前未提交改动的回归发现。
