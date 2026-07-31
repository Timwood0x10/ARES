# GoAgent (ARES) 代码审计报告

> 审计日期：2026-07-31
> 范围：项目全部 Go 源码（`api/`、`internal/`、`compat/`、`sdk/`、`cmd/`、`examples/`）
> 方法：人工读码核查 + `go vet` + `golangci-lint` + 代码知识图谱 + codescope 证据分析
> **本文件已按同日复验更新**：每条原始结论标注当前状态 —— ✅ 仍成立 / 🔧 已修复（旧结论作废）/ ⚪ 已重构消失 / ⚠️ 未重验。

## 验证环境

- `go vet ./...`：通过
- `golangci-lint run ./...`：2 个问题（goconst ×1，gofmt ×1）
- codescope 索引已刷新（2026-07-31 重建），其自动化信号（死代码/漂移/完整性）经人工核实对 Go 项目存在系统性误报，详见[附录](#附录codescope-自动化信号可靠性说明)

---

## 〇、复验结论速览（2026-07-31 下午）

> 复验后最重要的事实：**首轮审计后代码已被大幅修改，原先列出的"高危"与多数"中危"已全部修复**。
> 以下仅列出复验后**仍然成立**的问题；🔧 标记的旧条目已被修复，从优先清单中移除。

### 复验后仍成立（按重要性排序）

| # | 位置 | 状态 | 问题 |
|---|---|---|---|
| R1 | `internal/workflow/engine/recovery_patcher.go:151-156` | ✅ 仍成立 | `applyChangeBackoff` 接收 `PatchChangeBackoff` 但**不应用任何改动**，返回的 rollback patch 也是 no-op。 |
| R2 | `internal/workflow/graph/node.go:215-217` | ✅ 仍成立 | `ToolNode.Execute` 状态 key 用**工具名**（`"node."+toolName`），而 `AgentNode`(L66)/`SubGraphNode`(L365) 用 node ID —— 不一致；同一工具在两个包复用时状态 key 冲突。 |
| R3 | `internal/ares_quant/marketmaking_api/paper.go` | ✅ 仍成立 | mark-to-market 仍是占位（`_ = positions`，L120），**PnL 恒为 0**；`DefaultPaperTrader` 仍为骨架。 |
| R4 | `internal/ares_runtime/tool.go` | ✅ 仍成立 | 包文档声称 ToolPlugin"校验步骤使用的工具已注册"，但 `RegisterTool`/`IsRegistered`（L35/L42）在**非测试代码中零调用**，注册校验是死路径。 |
| R5 | `api/handler/evolution.go:223-236` | ✅ 仍成立 | `HandleRegisterComponent` HTTP 端点占位：校验 name 后返回 200 但**不做任何注册**，仅提示"请用 SDK"。 |
| R6 | `internal/workflow/graph/patcher.go:262-265` | ✅ 仍成立 | `defaultNodeExecute` 明确 no-op：演化插入/替换的节点运行时**什么都不做**（注释自述 "structural placeholders"）。 |
| R7 | `internal/ares_runtime/loop.go:160` | ✅ 仍成立 | `_ = state` 占位，内存插件以空 `RouteState{ExecutionID}` 驱动，真实轮次数据未接入。 |
| R8 | `internal/ares_runtime/interrupt.go:38` | ✅ 仍成立 | `InterruptPlugin.Capabilities()` 返回 `nil`，插件对 `PluginsByCap` 查询不可见。 |
| R9 | `internal/knowledge/planner/default.go:209-213` | ✅ 仍成立 | `detectProviderType` 未实现，直接返回 provider 名（注释自述 "Real implementations would register their type explicitly"）。 |
| R10 | `api/client/health.go:47` | ✅ 仍成立 | `checkLLMHealth` 仅被测试引用，**生产代码无调用方**（已用全仓 grep 确认）。 |
| R11 | `internal/workflow/graph/scheduler.go:155-188` | ✅ 仍成立 | `WeightedFairScheduler` 的 `counter` 只增不减，计数无界增长，非文档所述的加权公平队列。 |
| R12 | `internal/storage/postgres/query/memory_cache.go:74-80` | ✅ 仍成立 | 缓存无 maxSize 淘汰，仅靠 TTL 兜底 → 长 TTL 下无界增长。 |
| R13 | `internal/storage/postgres/query/cache.go:229` | ✅ 仍成立 | `sortFilters` 对 Go map "排序"是无效操作；`normalizeText` 仅 ASCII 大小写折叠，非 ASCII 文本产生额外缓存 miss。 |
| R14 | `internal/knowledge/pipeline/llm_summarizer.go:81,93` | ✅ 仍成立 | 提示词硬编码"用中文"输出。 |
| R15 | `internal/agents/leader/recovery.go:13` + `dispatcher.go:204` | ✅ 仍成立 | 硬编码表名 `task_results_1024`；`getAgentID()` 恒返回 `"leader"`。 |
| R16 | `internal/knowledge/adapter/memory.go:17-19` | ✅ 仍成立 | `FromMemory` 记忆内容固定截断 200 字符，与配置的摘要长度无关，信息丢失。 |
| R17 | `internal/ares_runtime/router.go:92-95` | ✅ 仍成立 | `ExpressionRouter.Route` 无匹配规则时返回 error，与接口文档（router.go:16）"Returning nil means no routing is needed"矛盾，调用方需把 error 当非匹配处理。 |

### 已修复（旧报告条目作废）

| 旧位置 | 原结论 | 现实现 |
|---|---|---|
| `internal/agentloop/engine.go` 工具事件版本 bug（**原高危**） | 事件全部静默丢失 | 🔧 已修复：`emitToolEvent` 改用 `expectedVersion=0`，错误改为记录日志，注释明确记载了旧 bug 与修复 |
| `internal/knowledge/retriever/retriever.go` `Query.Types` 被忽略 | 类型过滤参数失效 | 🔧 已修复：`filterByTypes` 已实现（retriever.go:105） |
| `internal/ares_events/compactable_store.go` Append 用 `ctx.Background()` | 忽略调用方取消 | 🔧 已修复：改用调用方 ctx |
| `internal/workflow/graph/patcher.go` `applyChangeScheduler` 无锁读 | 数据竞争 | 🔧 已修复：已持锁 |
| `internal/workflow/engine/recovery_patcher.go` 回滚损坏异构配置 / 锁外改共享指针 | 中危 ×2 | 🔧 已修复：按步快照 + 持锁 |
| `internal/ares_runtime/manager_chaos.go` `SlowAgent` 死代码 / `ToolTimeout` 误杀 | 故障注入与文档不符 | 🔧 已修复：`chaosSlowDelay`/`chaosToolTimeout`/`chaosWrappedAgent` 已实现 |
| `internal/ares_runtime/evolution_plugin.go` nil provider 当 error | 合法空推荐判失败 | 🔧 已修复：`Recommend` 对无 provider 与空推荐均返回 `nil,nil`（带 `//nolint:nilnil` 与契约注释） |
| `internal/ares_memory/manager_impl.go` `GetLatestSessionForLeader` 空实现 | failover 静默丢失 session | 🔧 已修复：改为返回 `ErrLeaderCheckpointNotSupported` 并附详细说明 |
| `internal/ares_memory/manager_impl.go:593-595` 死分支 | 死代码 | 🔧 已修复：已删除，并加注释说明为何不存在 all-failed 检查 |
| `internal/ares_quant/marketmaking_api/client.go` Backtest/PaperTrade 改写调用方 req | 请求被就地修改 | 🔧 已修复：改为拷贝 symbols，注释明确记载 |
| `internal/ares_runtime/bus.go` 订阅泄漏 | 常驻 goroutine 泄漏 | 🔧 已改为 ctx 生命周期设计：`Subscribe` 在 ctx.Done 时自动退订+关闭 channel，`Emit` 持 RLock 防 close/send 竞争（已有详细注释）。ctx 永不取消才会残留 —— 属惯例设计，非 bug |
| `internal/ares_events/memory_store.go` 订阅泄漏 | 同上 | 🔧 同上，ctx.Done → unsubscribe |
| `internal/ares_runtime/bus.go` `invokeWithTimeout` 不 join 子 goroutine | 每次超时泄漏一个 goroutine | ⚪ 降级：`done` 为容量 1 的缓冲 channel，goroutine 在 fn 返回后即退出，**仅在 fn 真正无视 ctx 且永不返回时才滞留** —— 属标准取舍，非永久泄漏；fn 返回时即使父 ctx 已取消也报 `ErrPluginTimeout` 的语义问题仍存在 |
| `internal/monitoring/plugin.go` `ErrNotImplemented` 文本 | 改名后文本仍 "not implemented" | 🔧 已修复：当前哨兵仅 `ErrNotConfigured` 等，全仓生产代码已无 `ErrNotImplemented` 与 "not implemented" 文本 |
| `internal/ares_memory/experienceadapters/adapters.go` gofmt | 未格式化 | 🔧 已修复：`gofmt -l` 通过 |
| `sdk/memory_wiring.go` TODO 重复实现 | 待统一到共享包 | 🔧 已修复：全文件无 TODO/FIXME 残留 |
| `internal/ares_quant/marketmaking_api/client.go` Close() 非原子判定 | 双清理竞态 | 🔧 已修复：改为直接委托 `Stop()`（幂等+持锁），注释明确记载旧竞态 |
| storage↔postgres、llm↔output 模块环依赖 | 真实漂移、环形依赖 | ❌ **误报（幻觉）**：`go list` 证实 `storage/postgres` 单向导入 `internal/storage`（正常父子依赖），`llm/output` 根本不导入 `internal/llm`，`ares_quant`↔`research`、`ares_evolution`↔`genome` 同理 —— **全仓无任何导入环**（有环无法编译）。此结论源自 codescope 架构漂移按名匹配误报 |
| `internal/workflow/graph/mutable_dag.go` | 无法退订 / 伪随机 PRNG | ⚪ 文件已不存在（重构移除） |
| `internal/workflow/reloader.go` 删除不清理 | 陈旧工作流残留 | ⚪ 文件已不存在（重构移除） |
| `internal/workflow/runner.go:51` `MaxParallel=1` 硬编码 | 图始终串行 | ⚪ 已重构：`MaxParallel` 已被 `Schedule.n` 取代 |

### ⚠️ 未重验（复验范围外，原条目保留、未确认）

`internal/workflow/graph/mutable_dag.go` 已消失（重构）——原"无法退订/伪随机 PRNG"条目作废。其余条目经复验结论见上表与下文：环依赖为误报，invokeWithTimeout 降级为设计取舍，其余已修复。仅剩 `compat/protocol/openai_api` stub、`compat/tool/builtin` Noop、`sdk/options.go` no-op、`chaos.go` skeleton 为**有意保留**的实现（自述文档确认），无需处理。

---

## 一、潜在 Bug（复验后现状）

### 高危

> **已无高危害项。** 原高危（agentloop 工具事件版本 bug 导致事件全部丢失）已修复。

### 中危（复验后仅剩）

| 位置 | 问题 |
|---|---|
| `internal/ares_quant/marketmaking_api/paper.go` | mark-to-market 占位（`_ = positions`），PnL 恒为 0；`DefaultPaperTrader` 骨架（见 R3）。 |

> 其余原中危条目（retriever、compactable_store、patcher、recovery_patcher、manager_chaos、evolution_plugin、GetLatestSessionForLeader、client.go req 改写）复验时**已全部修复**，不再列示。

### 低危

- `api/client/health.go:47` — `checkLLMHealth` 生产无调用方（已确认，见 R10）。

> 原"`client.go Close()` 非原子 wasStarted 判定"已修复（现直接委托幂等 `Stop()`，见速览表）。

---

## 二、空实现 / 骨架代码（Stub，复验后现状）

| 位置 | 状态 | 说明 |
|---|---|---|
| `internal/workflow/engine/recovery_patcher.go:151-156` | ✅ | `applyChangeBackoff` 不应用任何改动，rollback 也是 no-op。 |
| `internal/workflow/graph/patcher.go:262-265` | ✅ | `defaultNodeExecute` 明确 no-op（"structural placeholders"）。 |
| `internal/ares_runtime/tool.go` | ✅ | 注册校验未实现：`RegisterTool`/`IsRegistered` 非测试代码零调用。 |
| `internal/ares_quant/marketmaking_api/paper.go` | ✅ | `DefaultPaperTrader` 骨架，`Start` 占位响应，PnL 占位。 |
| `api/handler/evolution.go:223-236` | ✅ | `HandleRegisterComponent` 端点占位，不做注册。 |
| `internal/ares_runtime/loop.go:160` | ✅ | `_ = state` 占位，真实轮次数据未接入。 |
| `internal/ares_runtime/interrupt.go:38` | ✅ | `Capabilities()` 返回 `nil`，对 `PluginsByCap` 不可见。 |
| `internal/knowledge/planner/default.go:209-213` | ✅ | `detectProviderType` 恒返回 provider 名。 |
| `compat/loader/html/html.go` | ✅ | 正则剥标签骨架 loader（注释自述 placeholder，未用 tokenizer）。 |
| `compat/tool/builtin` | ✅ | `Noop` 占位工具（"for skeleton wiring only"，有意保留）。 |
| `compat/protocol/openai_api` | ✅ | images/audio/moderation stub（返回规范错误响应，可接受的有意 stub）。 |
| `internal/knowledge/runtime/runtime.go:142-160` | ✅ | `LazyLoading` 仅近似实现（clamp 预算），仍加载全部对象，返回 `*WorkingGraph`；注释已自述 "Known limitation: not a full LazyGraph" 与未来方向。 |
| `internal/ares_quant/marketmaking_api/chaos.go` | ✅ | `DefaultChaosExecutor` 注入逻辑实际可用，且已用 `math/rand`（合理）。 |
| `sdk/options.go:290` | ✅ | no-op 兼容选项（有注释说明，有意保留）。 |

---

## 三、技术债务（复验后现状）

### 并发 / 资源泄漏

- **订阅清理**：`bus.go` / `memory_store.go` 均已改为 ctx 生命周期自动退订 + 关 channel（🔧 已修复/设计合理，见速览表）。
- **超时 goroutine**：`internal/ares_runtime/bus.go` `invokeWithTimeout` 不 join 子 goroutine，但缓冲 channel 使 goroutine 在 fn 返回后即退出，非永久泄漏（⚪ 降级为设计取舍）。
- **无界计数**：`internal/workflow/graph/scheduler.go:155-188` `WeightedFairScheduler` 的 `counter` 只增不减（✅，R11）。
- **无界缓存**：`internal/storage/postgres/query/memory_cache.go` 无 maxSize 淘汰，仅 TTL 兜底（✅，R12）。
- `internal/workflow/graph/node.go:215-217` ToolNode 状态 key 用工具名而非 nodeID（✅，R2）。
- `internal/ares_runtime/router.go:92-95` ExpressionRouter 无匹配时返回 error，与接口文档"Returning nil means no routing is needed"矛盾（✅，R17）。

### 错误处理

- `internal/ares_arena/service.go:159` 失败证据 `s.evStore.Append(...)` 用 `_ =` 吞错（✅ 已确认仍存在）。
- `cmd/ares/actions.go` 14 处 `_ = json.NewEncoder(w).Encode(...)`（HTTP 响应编码错误被忽略，✅ 已确认仍存在）。

### 重复 / 硬编码

- `internal/agents/leader/` 硬编码表名 `task_results_1024`（`recovery.go:13` 等）+ `dispatcher.go:204` `getAgentID()` 恒返回 `"leader"`（✅，R15）。
- `internal/knowledge/pipeline/llm_summarizer.go:81,93` 硬编码中文输出（✅，R14）。
- `internal/knowledge/adapter/memory.go:17-19` 记忆固定截断 200 字符（✅，R16）。
- goconst：`"max iterations reached"` 出现 3 次，应提为常量（仍存在）。

### 架构 / 依赖

> **环依赖结论已推翻（误报）**：`go list` 证实 `storage/postgres` 单向导入 `internal/storage`，`llm/output` 不导入 `internal/llm`，全仓无导入环。原"storage↔postgres、llm↔output 上下层倒置"为 codescope 按名匹配的幻觉。

- `internal/storage/postgres/query/cache.go:229` `sortFilters` 对 map 排序无效（✅，R13）。
- 一致性：`internal/monitoring/plugin.go` `ErrNotImplemented` 已清理（🔧），仅剩 `ErrNotConfigured` 等语义清晰的哨兵。

---

## 四、优先处理建议（复验后更新）

> 原 P0/P1（agentloop 事件 bug、retriever Types、recovery_patcher 回滚、manager_chaos、GetLatestSessionForLeader）**均已修复**，不再列入。

1. **P1** `internal/workflow/engine/recovery_patcher.go` `applyChangeBackoff` 仍是 no-op（R1）——改配置不生效。
2. **P1** `internal/ares_runtime/tool.go` 注册校验是死路径（R4）——要么接入 `AfterStep`/前置校验，要么删掉文档声明。
3. **P2** `internal/workflow/graph/node.go` ToolNode 状态 key 与节点 ID 不一致（R2）。
4. **P2** `internal/ares_quant/marketmaking_api/paper.go` mark-to-market 占位，PnL 恒 0（R3）。
5. **P2** `internal/ares_runtime/router.go` Route 返回 error 与接口文档矛盾（R17）。
6. **P2** `api/client/health.go` `checkLLMHealth` 死代码（R10）——删除或接入生产路径。
7. **P2** 剩余骨架/占位（R5-R9）与债务项（R11-R16）。

---

## 附录：codescope 自动化信号可靠性说明

> 注：2026-07-31 曾删除 `.codescope/` 下全部 DB（`codescope.db` / `-shm` / `-wal` / `lbug`）后从零重建索引（1382 文件 / 11523 实体），并重跑全部检查。以下结论在**干净索引**下复测依然成立。

- **死代码检查为系统性误报**：新旧索引均报一批"零调用"函数（`getAllowedConfigDir`/`findConfigPath`/`loadFromEnv`/`setDefaults`/`validate`/`sendAgentEvent`/`authMiddleware`/`handleBuild`/`resolveBudget`/`nodeIDs`/`sendSSE`/`convertEvent`/`registerAgents`/`parseCrossoverType`/`singlePointCrossover`/`twoPointCrossover`/`parseMutationType`/`convertNodes` 等），经人工核实**全部在用**。方法调用可解析，自由函数 / 接口 / 闭包调用解析不到。其"孤立"结论不可信。
- **唯一经核实的死代码**：`api/client/health.go:47` `checkLLMHealth` 仅被测试引用，生产代码无调用方（未使用辅助函数，低危）。**提示**：codescope 的 `find_callers`/`find_references` 对一切符号返回 0（即使真实有调用点），`verify_statement` 对全部论断返回 `Unknown`/0 置信度，`semantic_fact` 表 0 行 —— 这些信号不可用，死代码结论必须以全仓 grep + 人工读码为准。
- **架构漂移**：100 条 Controller→Controller 同层调用均为按名匹配的误报（构造函数/工具函数/转换函数被归为 Controller）。真实问题是附录列出的模块级环依赖（storage↔postgres、llm↔output），需人工读码确认。
- **能力漂移**：把 README 标题、表格列头（`BuildResilientSelfEvolvingAIAgentsInGoUn`、`FeatureStatusEXPERIMENTALAPIMayChangeNot`）解析成能力并判为"缺失"，纯误报。
- **证据管道**：`build_evidence`/`build_project_state` 在干净索引下 sync/memory/error/ffi 事实数仍为 0（`semantic_fact` 表 0 行），v0.3 规则引擎对 Go 仓库无产出；`build_evidence` 恒返回 `[]`。
- **调用图覆盖有限**：干净索引入库 1382 文件、2730 条名称匹配边；`project_overview` 的 `call_graph`/`semantic_search` 恒为 false。**任何基于该调用图的自动化结论均须人工复核。**
