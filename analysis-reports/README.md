# 项目源码分析报告索引

> 对 `goagent`（ares agent 框架）逐模块进行源码审查，查找潜在 bug、逻辑问题与死代码。
> 每个模块对应一份 markdown 报告。本文件为索引与全局高优先级问题汇总。

**审查方法**：对每个模块读取全部非测试 Go 源文件，结合包间调用关系（LSP/文本搜索）验证死代码与逻辑问题，只报告可验证的真实问题。

---

## 修复进度（按模块）

> 以下问题已完成修复并补充了测试（遵循 plan/rules/code_rules_v2.md）。全部改动已通过 `go build ./...`、相关模块 `go test`、`go vet` 与 `gofmt`。

| 模块 | 已修复问题 |
|------|-----------|
| internal/agentloop | 达迭代上限时补发 TaskCompleted（新增测试） |
| internal/ares_arena | runStrategy 不完整 scores 检测 + `ErrEmptyScores` 死代码清理 + `Samples` 改用实际得分长度 + `MinWinRate` 通过 `NewBetter` 生效（新增测试） |
| internal/storage | pool.QueryRow 真实连接错误经 Scan 上浮 + vector 查询/插入补 `::vector` 转换 + 移除哑 import |
| internal/knowledge | postgres HybridSearch 参数修复 + memory store 分页顺序修复（更新 bug 测试）+ mergeConfidence 钳制 [0,1]（新增测试）+ workflow TokenBudget ForGraph 保留/负 Reserved 钳制 |
| internal/tools | extractor 求和公式修正（含下界）+ web_search 改 allowlist 导向（修复 SSRF 自阻默认端点） |
| internal/workflow | graph.Edge 保留不同条件边（新增测试）；runner_checkpoint 经分析为正确设计，未改动 |
| internal/ares_memory | KeepBoth 分支补全 problem/confidence/extraction_method 元数据 + 接线 ExperienceRepository 修复 SearchSimilarTasks 永远空 |
| internal/ares_protocol | dlq 新增 AddWithMaxRetries 使重试预算生效 + Process 加锁修复 Retries 数据竞争（新增测试） |
| api/service | ExecuteStream 应用超时（goroutine 拥有派生 ctx）+ errgroup 错误经合成 Failed 事件上浮 + CreateAgent 回填 Config |
| cmd/ares | start.go 用 atomic.Pointer 修复 svc 数据竞争 + actions.ListTools 补 nil guard |
| internal/ares_eval | report 空值集不再除零/误报 min + 软失败原因持久化 + 移除不可达 ErrNoRows 分支 + **移除假实现 placeholderRunner（铁律#2）及不可达 fallback** |
| sdk | New() 错误路径关闭 llmSvc/MCP clients/排空 bootstrap + wireMCPClients 失败关闭已连客户端 + Team.Run 补 nil guard |

## 第二轮修复（接线 + 死代码清理，已完成）

> 严格遵循 `plan/rules/code_rules_v2.md`，已通过 `go build ./...`、`go test ./...`、`go vet`、`gofmt`。

| 模块 | 修复内容 | 类型 |
|------|---------|------|
| internal/ares_memory | 在 `NewProductionMemoryManager` 构造 RetrievalService 后调用 `SetExperienceServices` 注入 ranking/conflictResolver，使 experience 检索的 ranking + 冲突消解在生产真正生效（此前 `rankingService`/`conflictResolver` 恒为 nil，`applyExperienceRanking` 静默回退） | 接线 + 新增 2 个 contract 测试 |
| internal/api_impl | 删除从未读取的死字段 `experienceRanking`/`experienceConflicts` 及构造代码（真实接线已在 production_manager 完成），清理 `experience` 死 import | 死代码清理 |
| internal/ares_eval | 移除违反铁律#2 的假实现 `placeholderRunner`（伪造 pass 结果）+ 移除不可达的 fallback 分支；runner 创建错误已由 nil 守卫排除，直接使用真实 runner | 死代码/假实现清理 |

> **接线确认**：`NewRetrievalService` 生产仅由 `NewProductionMemoryManager` 调用，本次接线覆盖唯一生产路径。

## 第三轮修复（live-DAG 进化替换 + 插件子系统审查）

> 已完成 `go build ./...`、`go test ./...`、`go vet`、`gofmt` 验证。

| 模块 | 修复内容 | 类型 |
|------|---------|------|
| internal/workflow/graph | 新增 `GraphPatchExecutor.SetGraph(*Graph)` 方法，支持就地替换底层图（镜像 `RecoveryPatchExecutor.SetDAG` 模式） | 修复 + 新增方法 |
| internal/ares_bootstrap | 新增 `graphExec` 字段并在 bootstrap 保存；`UpdateLiveDAG` 改为 `graphExec.SetGraph(g)` 就地更新，删除必失败的 `RegisterComponent`/`Register("graph.scheduler")`。**修复"每次调用必然失败、graph executor 的 DAG 从不上链到 live DAG"缺陷**（`serve.go:530` 辅助更新现在真正生效）。与 recovery/knowledge 的 `SetDAG`/`SetRuntime` 模式统一 | 修复 + 新增 2 个回归测试 |
| cmd/ares | `serve.go` PluginBus 接线点补充 tech-debt 注释（铁律 #3 留痕） | 文档留痕 |

### ares_runtime 内置插件子系统——Code Review 结论（方向决策，不改动）

深度审查后确认：
- `PluginBus` **活跃**（serve.go 注册 `MonitorPlugin`），workflow Runner 通过 `WithPluginBus` 驱动其 `BeforeStep`/`AfterStep`。
- 但内置插件（Arena/Checkpoint/Loop/Observer/Tool）与 router（Expression/Memory/Evolution/Fallback）**生产从不注册**，`MonitorPlugin` 不实现 `WorkflowHook`，因此 hook 机制在生产无触发。
- 关键判断：**workflow 引擎已有原生 loop/checkpoint/routing**（`LoopSpec.MaxIterations`、`WithCheckpointStore`、`NodeRouter`、`graph.SetPluginBus`）。内置插件是**并行/替代机制**，仅被 `graph/executor_test.go` 等测试路径使用。
- **结论**：强制接线会改变运行时行为（loop 限制、持久化突然生效），属**产品方向决策**（铁律 #4），非 bug 修复。已按铁律 #3 在 `serve.go` 标注 tech-debt，**未改动行为**。

> **分析确认未改动项**：ares_arena `computeWinRate` 平局计入（文档化设计）、queue.Enqueue 忽略 ctx（文档化非阻塞设计）、workflow runner_checkpoint Acknowledge 失败（resume 经 Restore 重建队列，无重复应用）。

---

## 报告清单

| # | 模块 | 报告 |
|---|------|------|
| 1 | 核心 Agent 循环 | [`internal-agentloop.md`](internal-agentloop.md) |
| 2 | 竞技场/回归测试 | [`internal-ares-arena.md`](internal-ares-arena.md) |
| 3 | Agent 与多智能体协作 | [`internal-agents.md`](internal-agents.md) |
| 4 | 进化/遗传算法 | [`internal-ares-evolution.md`](internal-ares-evolution.md) |
| 5 | 知识库 | [`internal-knowledge.md`](internal-knowledge.md) |
| 6 | 存储层 | [`internal-storage.md`](internal-storage.md) |
| 7 | 工具系统 | [`internal-tools.md`](internal-tools.md) |
| 8 | 工作流引擎 | [`internal-workflow.md`](internal-workflow.md) |
| 9 | 记忆系统 | [`internal-ares-memory.md`](internal-ares-memory.md) |
| 10 | MCP 集成 | [`internal-ares-mcp.md`](internal-ares-mcp.md) |
| 11 | 事件系统 | [`internal-ares-events.md`](internal-ares-events.md) |
| 12 | 飞行记录/追踪 | [`internal-ares-flight.md`](internal-ares-flight.md) |
| 13 | 评估系统 | [`internal-ares-eval.md`](internal-ares-eval.md) |
| 14 | 归档 | [`internal-ares-archive.md`](internal-ares-archive.md) |
| 15 | 运行时/混沌 | [`internal-ares-runtime.md`](internal-ares-runtime.md) |
| 16 | Bootstrap 及辅助模块 | [`internal-ares-bootstrap.md`](internal-ares-bootstrap.md) |
| 17 | 可观测性/协议/经验 | [`internal-ares-observability-protocol-experience.md`](internal-ares-observability-protocol-experience.md) |
| 18 | API core/service | [`api-core-and-service.md`](api-core-and-service.md) |
| 19 | cmd/api_impl/client/handler | [`cmd-and-api-impl.md`](cmd-and-api-impl.md) |
| 20 | 监控 | [`internal-monitoring.md`](internal-monitoring.md) |
| 21 | 仪表盘与发现 | [`internal-dashboard-and-discovery.md`](internal-dashboard-and-discovery.md) |
| 22 | 进化协调/GA 管线 | [`internal-evolution.md`](internal-evolution.md) |
| 23 | LLM 与工具包 | [`internal-llm-and-utils.md`](internal-llm-and-utils.md) |
| 24 | SDK | [`sdk.md`](sdk.md) |

---

## 全局高优先级问题汇总（按严重度）

### 🟥 BUG（可能造成崩溃 / 数据损坏 / 功能失效）

| 模块 | 位置 | 问题 |
|------|------|------|
| storage | `postgres/pool.go` 313-321 | `QueryRow` 吞掉真实连接错误，伪装成 NoRows |
| knowledge | `store/postgres/store.go` 391-400 | `HybridSearch` 参数与占位符不匹配，Postgres 向量召回必失败 |
| dashboard | `ws_hub.go` 203/318 | WebSocket ping/unregister 竞争 → "send on closed channel" panic |
| ares_memory | `distiller.go` 584-604 | KeepBoth 冲突分支丢 problem/confidence，持久化数据丢失 |
| workflow | `runner_checkpoint.go` 312-314 | Acknowledge 失败不回滚，mutation 可能重复应用 |
| ares_evolution | `regression_tester.go` 88-89 | 共享 `rng` 并发竞争 |
| ares_protocol | `dlq.go` 43、187 | `MaxRetries` 从不设置（重试上限失效）+ `Retries++` 数据竞争 |
| sdk | `sdk.go` 699-788 | `New()` 错误路径资源泄漏（llmSvc/MCP clients/bootstrap） |
| sdk | `team.go` 75-127 | leader/runtime 无 nil guard |
| ares_evolution | `shadow_evaluator.go` 162-203 | 影子评估总是阻止部署（samples 不足） |
| workflow | `graph/graph.go` 179 | 静默丢弃不同条件边 |
| ares_bootstrap | `provide_new_evolution.go` 343 | UpdateLiveDAG 的 Register 总失败，live-DAG executor 从不替换 |

### 🟨 LOGIC（逻辑错误 / 结果不准确）

| 模块 | 位置 | 问题 |
|------|------|------|
| agentloop | `Run()` 循环末尾 | 达到迭代上限时不发射 TaskCompleted |
| ares_arena | `runStrategy` 381-445 | 取消/出错时返回 0 填充的不完整 scores |
| ares_arena | `MinWinRate` | 被设置但从不参与判定（死配置） |
| knowledge | `store/memory/store.go` 103 | 先 Limit 后 Offset，破坏分页 |
| knowledge | `pipeline.go` 252 | mergeConfidence 可返回 >1.0 |
| tools | `web_search.go` 104 | SSRF 自阻默认 SearXNG 端点 |
| tools | `extractor.go` 76 | 求和忽略下界 a |
| tools | `bridge.go` 226 | 多步计划从不保存证据 |
| ares_memory | `production_manager.go` 139 | SearchSimilarTasks 永远空（expRepo 未接线） |
| api/service | `workflow/service.go` 285 | ExecuteStream 忽略超时 |
| api/service | `workflow/service.go` 188 | errgroup 未 await，执行错误丢失 |
| api/service | `agent/service.go` 47 | CreateAgent 丢弃 config.Config |
| monitoring | `dag/engine.go` 379 | task created 直接 Running |
| discovery | `binary.go` 44 | symlink 绕过 allowlist（安全） |
| cmd/ares | `start.go` 53-62 | `svc` 数据竞争 |

### ⚪ DEAD_CODE（大量，详见各报告）

- 多个 `constants.go` 未使用常量（tools/ares_evolution/llm）
- 整个未接线的子系统：ares_arena/scorer.go、ares_runtime 内置插件/router、ares_experience 蒸馏管线、monitoring publisher 交互引擎、api/service/events 错误类型等
- 不可达分支：`rollback_policy.go` `checkStart < 0`、`service/repository.go` `ErrNoRows`、`pipeline.go` nil 检查等

---

## 特别注意

- **`internal/ares_arena/regression.go`** 是本仓库唯一有未提交改动的文件（+80/-10），其中的自适应模式、beta 函数重写引入了 `MinWinRate` 未使用、`Samples` 报告配置值、`p>0.5` 过早停止等问题，建议提交前复查。
- 各报告的"结论"表格含精确定位（文件/行号/优先级），可直接作为修复清单。
