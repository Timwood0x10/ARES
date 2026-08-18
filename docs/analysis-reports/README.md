# 项目源码分析报告索引

> 对 `goagent`（ares agent 框架）逐模块进行源码审查，查找潜在 bug、逻辑问题与死代码。
> 本索引保留与生产路径仍相关的报告；已修复模块的逐模块报告已删除（修复记录见下方进度表）。

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

### ares_runtime 内置插件/router 子系统——结论：能力储备（保留）

**深度核查修正了初版报告的两处误判**：
1. `manager_chaos.go`（`chaosWrappedAgent`）**不是死代码**，是 Manager 生产路径的组成部分（`manager.go:211` 使用它做故障注入边界）。
2. `LoopPlugin`/`CheckpointPlugin`/`CapCheckpoint`/`CapEvolution`/`Flusher`/`EvolutionPlugin` 接口被 workflow **生产消费**（`runner_plugins.go:70,83` 类型断言），不是"生产不用的死插件"。

**最终分类**（详见 [`ares-runtime-capability-reserve.md`](ares-runtime-capability-reserve.md)）：
- **生产活跃**：PluginBus、Manager(+chaos/lifecycle)、Loop/Checkpoint 插件、`ExpressionRouter`、`RouterPlugin`/`MemoryPlugin`/`EvolutionPlugin` 接口、`ExecutionOutcome`、全部 `Cap*` 常量。
- **能力储备**（完整实现+测试，生产暂未注册，**保留**）：`ArenaPlugin`/`ObserverPlugin`/`ToolPlugin`/`MemoryRouter`/`EvolutionRouter`/`FallbackRouter`/`NewEvolutionPlugin`/`NewBasicRecoveryPlugin`/`NewInterruptPlugin`。
- **无"滥竽充数"假实现**（经核查，与已删的 `ares_eval/placeholderRunner` 性质不同）。

**处置**：这些能力储备有明确未来能力点（混沌/可观测/记忆路由/进化路由/降级），属"锦上添花"，按用户判断标准**保留**。已在 `serve.go` 修正注释为"能力储备"定位，并建立启用路径文档。**未删除任何文件，已还原全部破坏性改动。**

> **分析确认未改动项**：ares_arena `computeWinRate` 平局计入（文档化设计）、queue.Enqueue 忽略 ctx（文档化非阻塞设计）、workflow runner_checkpoint Acknowledge 失败（resume 经 Restore 重建队列，无重复应用）。

---

## 保留报告清单

> 逐模块审查报告（含全局问题汇总）已随修复完成归档删除；以下为仍被生产代码/文档引用的设计类报告，以及 2026-08-17 新产生的路线图/审查摘要。

| 报告 | 引用方 / 用途 |
|------|--------|
| [`ares-capability-fabric-design.md`](ares-capability-fabric-design.md) | `docs/agent-birth-capabilities.md` / `.en.md` |
| [`ares-runtime-capability-reserve.md`](ares-runtime-capability-reserve.md) | `cmd/ares/serve.go`（能力储备接线留痕） |
| [`ares-vs-prime-agent.md`](ares-vs-prime-agent.md) | `README.md`（Agent-OS Primitives 依据） |
| [`v0.3.0-feature-suggestions-corrected.md`](v0.3.0-feature-suggestions-corrected.md) | v0.3.0 路线图（M1 多 Agent 协作 / M2 Evolution-Runtime 集成 / M3 可解释性与人工反馈 / M4 全局观测，含优先级矩阵与落地计划） |
| [`CODE-REVIEW-SUMMARY.md`](CODE-REVIEW-SUMMARY.md) | 2026-08-17 深度 Code Review 完成报告（审查项与主要发现汇总） |
