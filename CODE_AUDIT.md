# GoAgent (ARES) 代码审计报告

> 审计日期：2026-07-31
> 范围：项目全部 Go 源码（`api/`、`internal/`、`compat/`、`sdk/`、`cmd/`、`examples/`）
> 方法：人工读码核查 + `go vet` + `golangci-lint` + 代码知识图谱 + codescope 证据分析
> 状态：**全部审计项已于 2026-07-31 晚复核完毕，R1-R17 与首轮高危/中危均已修复**。本文件为最终版，只保留仍有意义的遗留项与结论。

## 验证环境（最终复核）

- `go build ./...`：通过
- `go vet ./...`：通过（无输出）
- `go test ./...`：全部通过
- codescope 索引：干净重建（1382 文件 / 11528 节点 / 2243 调用边），自动化信号对 Go 可靠性有限，详见[附录](#附录codescope-自动化信号可靠性说明)

---

## 一、审计结论总览

### 首轮高危（已全部修复）

| 原问题 | 修复 |
|---|---|
| `internal/agentloop/engine.go` 工具事件版本 bug，第一个工具调用后所有 Started/Completed 事件静默丢失 | `emitToolEvent` 改用 `expectedVersion=0`，错误改为记录日志，注释明确记载旧 bug |

### 复验 R1-R17（已全部修复）

| # | 位置 | 原问题 | 修复 |
|---|---|---|---|
| R1 | `recovery_patcher.go` `applyChangeBackoff` | no-op，改配置不生效 | 按步快照 + 回滚恢复（`recoveryBackoffSnapshot`），无 policy 步骤统一建 policy，持锁应用 |
| R2 | `graph/node.go` ToolNode 状态 key | 用工具名 `"node."+toolName`，两包复用同一工具时冲突 | 改用 `nodeID`（与 AgentNode/SubGraphNode 一致） |
| R3 | `marketmaking_api/paper.go` | mark-to-market 占位，PnL 恒 0 | 真实 mark-to-market：`paperPosition` 记录成本基准 + LastPrice，平仓兑现 PnL，开仓按最新价盯市 |
| R4 | `ares_runtime/tool.go` | 注册校验是死路径（`IsRegistered` 零调用） | `AfterStep` 真正校验 allowlist（空注册表 = 未配置校验），未注册工具返回 `ErrToolNotRegistered` |
| R5 | `api/handler/evolution.go` `HandleRegisterComponent` | HTTP 端点占位，返回 200 不注册 | 明确拒绝 REST 注册，提示用 SDK `RuntimeEvolution.RegisterComponent` |
| R6 | `graph/patcher.go` `defaultNodeExecute` | 演化节点静默 no-op | 改为 `placeholderNodeExecute`：写入显式 `PlaceholderResult{Placeholder,NodeID,Reason}`，缺省可观测，不再伪装成功 |
| R7 | `ares_runtime/loop.go:160` | `_ = state` 占位，空 RouteState | `RouteState` 填充真实字段（ExecutionID + `Variables{round}`），并如实注释边界限制 |
| R8 | `ares_runtime/interrupt.go` | `Capabilities()` 返回 nil | 返回 `[]Capability{CapInterrupt}` |
| R9 | `knowledge/planner/default.go` `detectProviderType` | 恒返回 provider 名 | 检测 `provider.TypedProvider` 接口并返回真实类型 |
| R10 | `api/client/health.go` `checkLLMHealth` | 生产无调用方（死代码） | 已接入 `api/client/client.go` 健康检查路径 |
| R11 | `workflow/graph/scheduler.go` | counter 只增不减、无界 | 改为有界 DRR（deficit-round-robin）：按 weight 累积积分、每轮扣减，加权公平选择 |
| R12 | `storage/postgres/query/memory_cache.go` | 无 maxSize 淘汰，仅 TTL | 增加 maxSize + LRU 淘汰 + TTL 清理 goroutine，缓存有界 |
| R13 | `storage/postgres/query/cache.go` | `sortFilters` 对 map 排序无效；`normalizeText` 仅 ASCII | `sortFilters` 已删除；`normalizeText` 改用 `strings.ToLower`（Unicode 大小写折叠）+ `strings.TrimSpace` |
| R14 | `knowledge/pipeline/llm_summarizer.go` | 提示词硬编码"用中文" | 语言可配置：`LanguageChinese`/`DefaultLLMSummaryLanguage`/`WithLanguage` |
| R15 | `agents/leader/` | 硬编码表名 `task_results_1024`；`getAgentID()` 恒返回 `"leader"` | 表名/ID 常量提取（`DefaultDispatcherAgentID`），sender identity 可配置 |
| R16 | `knowledge/adapter/memory.go` | 固定截断 200 字符 | 长度可配置（`DefaultMaxMemoryContentLen=200` 保留默认），按 `a.maxContentLen` 截断 |
| R17 | `ares_runtime/router.go` ExpressionRouter | 无匹配返回 error，与接口文档"nil 表示无需路由"矛盾 | 返回 `nil, nil` 并加 `//nolint:nilnil` + 契约注释 |

### 首轮中危（复验已修复，不再列细节）

retriever `Query.Types` 过滤（`filterByTypes`）、compactable_store 用调用方 ctx、patcher 持锁、recovery_patcher 快照+持锁、manager_chaos `chaosSlowDelay`/`chaosToolTimeout`/`chaosWrappedAgent`、evolution_plugin nil 契约（`//nolint:nilnil`）、`GetLatestSessionForLeader` 返回 `ErrLeaderCheckpointNotSupported`、client.go 拷贝 symbols 不改写调用方 req、bus/memory_store 订阅 ctx 生命周期清理、monitoring 哨兵清理、Close() 委托幂等 Stop()。

### 误报（幻觉，已推翻）

- **storage↔postgres / llm↔output 模块环依赖**：`go list` 证实 `storage/postgres` 单向导入 `internal/storage`（正常父子依赖），`llm/output` 不导入 `internal/llm`，全仓无导入环。来源：codescope 架构漂移按名匹配。
- codescope 死代码/能力漂移大量误报（详见附录）。

---

## 二、遗留项（低危，可选处理）

> 全部审计主项已关闭，以下为低危/有意保留，不影响功能正确性。

### 错误处理

- `internal/ares_arena/service.go` 失败证据 `s.evStore.Append(...)` 用 `_ =` 吞错。
- `cmd/ares/actions.go` 14 处 `_ = json.NewEncoder(w).Encode(...)`（HTTP 响应编码错误被忽略）。
- `internal/ares_runtime/bus.go` `invokeWithTimeout`：fn 返回时即使父 ctx 已取消也报 `ErrPluginTimeout` 的语义问题（goroutine 本身因缓冲 channel 非永久泄漏，属设计取舍）。

### 有意保留的 Stub（自述文档确认）

- `compat/loader/html/html.go` 正则剥标签骨架 loader。
- `compat/tool/builtin` `Noop` 占位工具。
- `compat/protocol/openai_api` images/audio/moderation stub（返回规范错误响应）。
- `internal/knowledge/runtime/runtime.go` `LazyLoading` 仅近似实现（clamp 预算），注释自述 "Known limitation"。
- `sdk/options.go` no-op 兼容选项。

### 微小项

- goconst：`"max iterations reached"` 出现 3 次，可提为常量。

---

## 三、codescope 使用建议（本次审计沉淀）

1. **发现/导航层可用**：索引、全文搜索、符号定位、入口点、路由、图谱导航均正常。
2. **裁决层对 Go 不可靠**：`find_callers`/`find_references` 对一切符号返回 0、`verify_*` 全 `Unknown`（`verify_claim` 报 "no verifier registered"）、`semantic_fact` 0 行、指标/语义未生成。任何死代码/证据/漂移结论必须以全仓 grep + `go list` + 人工读码复核。

---

## 附录：codescope 自动化信号可靠性说明

> 注：2026-07-31 删除 `.codescope/` 下全部 DB 后干净重建（1382 文件 / 11528 节点 / 2734 图边 / 2243 调用边），硬测试结论在干净索引下成立。

- **死代码检查为系统性误报**：新旧索引均报一批"零调用"函数（`getAllowedConfigDir`/`findConfigPath`/`loadFromEnv`/`setDefaults`/`validate`/`sendAgentEvent`/`authMiddleware`/`handleBuild`/`resolveBudget`/`nodeIDs`/`sendSSE`/`convertEvent`/`registerAgents`/`parseCrossoverType`/`singlePointCrossover`/`twoPointCrossover`/`parseMutationType`/`convertNodes` 等），经人工核实**全部在用**。自由函数 / 接口 / 闭包调用解析不到。其"孤立"结论不可信。
- **调用图查询失效**：`find_callers`/`find_references`/`trace_flow`(callees)/`explain_symbol`(callers/callees) 对已知真实调用点仍返回空；`ready_features.call_graph/metrics/semantic_search` 恒 false，embedding 0。即使索引阶段构建了 2243 条调用边，查询层仍无法利用。
- **验证管道为空**：`verify_claim` 直接报 "no verifier registered for this claim type" —— verifier 注册表为空，所有 `verify_*` 必然 `Unknown`/0 置信度；`build_evidence`/`build_project_state` 无产出。
- **架构漂移误报**：100 条 Controller→Controller 同层调用均为按名匹配误报；其"模块环依赖"结论经 `go list` 证实为假（无导入环）。
- **能力漂移误报**：把 README 标题、表格列头解析成能力并判为"缺失"。
- **结论**：codescope 适合做代码发现/导航与可疑位置定位，**不可作为死代码/证据/架构的自动裁决器**。
