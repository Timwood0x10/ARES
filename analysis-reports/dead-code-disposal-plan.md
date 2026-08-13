# 生产未用模块处置方案

> 基于全仓库扫描（2026-08-12），对"已实现但生产（非测试）代码从不调用"的子系统逐项给出处置决策：**接线**、**砍**、或**保留**。
> 决策原则：有真实生产消费价值且接线成本低的 → 接线；半成品（空实现/桩/恒 501/恒错误）或纯死代码 → 直接砍并写清理由；近期修复过的导出 API → 保留并说明。

---

## 一、接线（2 项）

### 1. monitoring publisher 交互引擎（kill/resume/retry 恒 501）

- **位置**：`internal/monitoring/publisher.go`（`WithInteractionEngine` 仅定义处）、`internal/monitoring/plugin.go:195-198`
- **现状**：`NewConsole` 组装 `Publisher` 时不传 `WithInteractionEngine`，`publisher.interEngine` 恒 nil；`executeNodeAction`/`HandleKillAgent`/`HandleResumeAgent`/`HandleRetryAgent` 恒返回 501。但 `plugin.go` 在配置了 runtime/orchestrator 控制器时已创建 `dag.NewInteractionEngine(engine, runtimeCtrl, orchCtrl)`，其 `ExecuteAction(ctx, nodeID, action)` 签名与 publisher 的 `InteractionExecutor` 接口完全匹配。
- **处置**：**接线**。在 `NewConsole` 内部，若 `MonitorPlugin.interEngine != nil`，则把 `p.interEngine` 传给 `publisher` 的 `WithInteractionEngine`。
- **理由**：这是真实功能缺口（监控面板 kill/resume/retry 动作在生产完全不可用），不是死代码；底层 `dag.InteractionEngine` 已存在且已接线 runtime/orchestrator 控制器，接线成本仅几行，且不改变任何现有行为（仅在原本 501 的路径上补全）。
- **影响面**：`internal/monitoring/plugin.go`（NewConsole 组装逻辑）、`publisher_test.go` 可加一条"未接线仍 501、接线后分发成功"的断言。

### 2. monitoring DetailPanel（当前为半成品）

- **位置**：`internal/monitoring/detail_panel.go`、`main_page.go`（`DetailPanelReader` 接口 + `WithDetailPanel` + `detailPanel` 字段）
- **现状**：`NewDetailPanel` 仅定义处引用；`WithDetailPanel` 生产无调用方；`main_page.go:156` 读取 `mp.detailPanel` 生产恒 nil（后面有 nil 判断所以不崩，但面板功能从不启用）。`DetailPanel.HandleEvent` 是**空操作**（注释描述的本应实现的 UI 刷新从未实现）。
- **处置**：**砍**（半成品）。删除 `detail_panel.go`、`main_page.go` 中的 `DetailPanelReader` 接口、`WithDetailPanel` option 与 `detailPanel` 字段。
- **理由**：`HandleEvent` 空实现 = 半成品；生产从不实例化；接口无消费者。与其留一个永远不启用的空壳，不如砍掉，将来需要面板详情时按真实需求重建。若面板详情是明确产品需求，也可改为"接线"，但当前空实现说明它从未被完成，砍更诚实。
- **影响面**：`detail_panel.go` 整文件、`main_page.go` 三处（接口/option/字段+读取点）、相关测试。

---

## 二、砍（纯死代码 / 与原生机制重复）

### 3. ares_runtime 内置插件（7 个构造器）

- **位置**：`checkpoint.go`、`arena.go`、`observer.go`、`interrupt.go`、`tool.go`、`loop.go`、`recovery.go`
- **现状**：`NewCheckpointPlugin`/`NewArenaPlugin`/`NewObserverPlugin`/`NewInterruptPlugin`/`NewToolPlugin`/`NewLoopPlugin`/`NewBasicRecoveryPlugin` 全部仅定义处引用；生产只注册 `MonitorPlugin`，从不注册这些内置插件。
- **处置**：**保留**（Q1 用户确认，详见 `ares-runtime-capability-reserve.md`）。
- **理由**：插件框架本身（`PluginBus` + 接口 + `chaosWrappedAgent`）是**生产活跃**的（`cmd/ares/serve.go:290` 构造；`internal/workflow/runner_plugins.go:70,83` 消费 `CapCheckpoint`/`CapEvolution`/`Flusher`/`EvolutionPlugin` 接口；`manager.go:211` 用 chaosWrappedAgent 做故障注入边界）。这些插件是**完整实现 + 有测试覆盖的能力储备**（ArenaPlugin 故障注入、ObserverPlugin 事件订阅、MemoryRouter/EvolutionRouter 记忆/进化驱动路由等），未来能力点明确，非滥竽充数。是否注册属产品方向决策（铁律 #4），当前不注册因 workflow 有原生 loop/checkpoint/router 等价物——但**砍的前提（模块完全无利用价值）不成立**。
- **影响面**：无改动（保留现状）。

### 4. ares_runtime 路由器（4 个构造器）

- **位置**：`router.go`、`router_memory.go`、`router_evolution.go`、`router_fallback.go`
- **现状**：`NewExpressionRouter`/`NewMemoryRouter`/`NewEvolutionRouter`/`NewFallbackRouter` 全部仅定义处引用，生产不注册任何 RouterPlugin。
- **处置**：**保留**（Q1 用户确认，与 #3 同批）。
- **理由**：`ExpressionRouter`/`RouteRule` 被 graph executor 测试夹具使用（`internal/workflow/graph/executor_test.go:738`）；`MemoryRouter`/`EvolutionRouter`/`FallbackRouter` 是记忆/进化驱动路由与降级链的完整实现，属能力储备。与 #3 同理，框架活跃、插件完整有测试，保留。
- **影响面**：无改动（保留现状）。`loop_test.go` 的 mock 重复问题已在审查中修复（移除重复定义），与保留/删除无关。

### 5. ares_observability CostDashboard

- **位置**：`internal/ares_observability/cost.go`
- **现状**：`NewCostDashboard`/`GetSessionCost`/`GetAllSessions`/`GenerateDashboardHTML`/`RegisterCostRoutes` 仅 `cost.go` 内部相互引用；`NewCostTracker` 仅被 CostDashboard 内部使用。
- **处置**：**砍**（整个 cost.go 的成本展示面；若 `CostTracker` 无其它生产消费者则一并砍）。
- **理由**：成本展示生产侧已有独立实现 `internal/monitoring/cost_bar.go`（按 agent 聚合、追踪货币），CostDashboard 的 HTML 生成器与 HTTP 路由无任何挂载点，纯死代码。
- **影响面**：`cost.go` 相关类型/方法 + 测试；执行前复核 `CostTracker` 是否被 dashboard 或 monitoring 引用。

### 6. ares_protocol DLQ 自动重试（StartAutoRetry / retryInterval）

- **位置**：`internal/ares_protocol/ahp/dlq.go`
- **现状**：`NewDLQ` 本体生产活跃（`protocol.go:51` 创建，`p.dlq.Add`（87 行）、`p.dlq.Size()`（174 行）被调用）；`DLQProcessor`/`StartAutoRetry`/`retryInterval` 生产无调用方。
- **处置**：**保留**（Q2 用户确认）。
- **理由**：DLQ 是**功能保障模块**——失败消息入队后必须有消费/重试侧（DLQProcessor）才构成完整闭环，砍掉 processor 会让 DLQ 变成"只写不读"的无效队列。且 `b1816c50` 刚修复过该模块（MaxRetries 生效 + Retries 加锁，有测试守护），不是半成品。保留 DLQ 本体 + DLQProcessor + StartAutoRetry 作为保障能力的挂载点。
- **待办（非砍）**：生产目前无 processor 接线，DLQ 消息只进不出——建议在 `serve.go` 或文档中标注 tech-debt，提示未来接线 `StartAutoRetry` 的挂载点。

### 7. ares_eval ComparisonRunner / ConcurrentRunner

- **位置**：`internal/ares_eval/comparison.go`、`concurrent_runner.go`
- **现状**：`NewComparisonRunner`/`NewConcurrentRunner` 仅定义 + 编译期接口检查（`var _ = (*ComparisonRunner)(nil)`）+ 测试引用。
- **处置**：**砍**。
- **理由**：生产评估走 `ares_eval/service` 的 agent runner 路径（`internal/api_impl/service.go` 接线）；这两个 runner 是早期导出库面，无任何生产消费者，纯死代码。
- **影响面**：2 个文件 + 各自测试；执行前复核是否有 examples/ 引用。

### 8. ares_flight SetGenealogy / Snapshot / AgentSnapshot

- **位置**：`internal/ares_flight/recorder.go`
- **现状**：`SetGenealogy` 仅定义处；`Snapshot`/`AgentSnapshot` 仅测试引用。
- **处置**：**砍**（方法级，保留 FlightRecorder 主体）。
- **理由**：genealogy 数据由 dashboard/orchestrator 与 api 层管理，recorder 不需要 setter；`Snapshot` 是测试专用。ares_flight 其余部分（timeline、Summary、collector）生产活跃（cmd/ares/flight.go、cmd/flight、dashboard），只砍这 3 个方法。
- **影响面**：`recorder.go` 3 个方法 + `flight_test.go` 相关用例。

### 9. ares_archive AllowedActions

- **位置**：`internal/ares_archive/record.go`
- **现状**：`AllowedActions()` 仅定义处，全仓库无调用。
- **处置**：**砍**。
- **理由**：原计划可能用于 CLI/错误帮助文本但从未引用；其依赖的 `actionPlan` 等常量仍活跃，只砍函数。
- **影响面**：`record.go` 删函数。

### 10. tools CapabilityEngine

- **位置**：`internal/tools/resources/core/capability.go`
- **现状**：`NewCapabilityEngine`/`CapabilityEngine`/`capabilityKeywords` 仅定义处 + 测试引用；生产走 `tools.Registry` 直取工具，capability 检测/过滤引擎无消费者。
- **处置**：**砍**。
- **理由**：纯死代码；能力过滤逻辑若有需要应在 registry 层实现，而不是保留一个从未接线的并行引擎。
- **影响面**：`capability.go` 整文件 + 测试。

### 11. ares_arena scorer.go

- **位置**：`internal/ares_arena/scorer.go`
- **现状**：`EnsembleScorer`/`ExactMatchScorer`/`MapScorer` 及构造器全仓库无生产引用（`Scorer` 接口定义在 `regression.go:87`，活跃；scorer.go 只是 3 个未接线的实现）。
- **处置**：**砍**。
- **理由**：`regression.go` 已定义并消费 `Scorer` 接口，scorer.go 的 3 个实现没有任何调用方，纯死代码，且与 regression 的 scorer 概念重复。
- **影响面**：`scorer.go` 整文件 + `scorer_test.go`。

### 12. monitoring 成本告警（WithCostAlertThreshold / cost_aggregator 告警）

- **位置**：`internal/monitoring/plugin.go:118-122`、`internal/monitoring/data/cost_aggregator.go`（`SetAlert`/`CheckAlerts`）
- **现状**：`WithCostAlertThreshold` 函数体为空返回空闭包，仅测试调用；`SetAlert`/`CheckAlerts` 仅测试引用。
- **处置**：**砍**。
- **理由**：半成品/桩配置；成本告警生产无人消费（成本展示走 `cost_bar.go`）。空闭包 option 会让使用者误以为配置生效。
- **影响面**：`plugin.go` 删 option、`cost_aggregator.go` 删告警方法 + 相关测试。

### 13. monitoring MonitorPlugin 桩方法（Events/CostAlerts/Interactions/MCPToolCalls）

- **位置**：`internal/monitoring/plugin.go:306-313、334-337、392-394、430-434`
- **现状**：4 个方法恒返回 `ErrNotConfigured`，从未接真实存储；生产无调用方（grep 无命中）。
- **处置**：**砍**。
- **理由**：半成品桩——暴露"ConsoleAPI 功能存在"但调用必失败；无消费者。砍掉后 API 面诚实（没有就是没有），避免误导。
- **影响面**：`plugin.go` 删 4 个方法 + `plugin_test.go` 相关用例。

### 14. ares_memory context 子包死类型（RAG / LRUCache / UserMemory）

- **位置**：`internal/ares_memory/context/rag.go`、`cache.go`、`user.go`
- **现状**：`NewRAG`/`NewLRUCache`/`UserMemory` 仅定义处 + 测试引用；`cleaner.go` 不依赖 Cache（已核实）；context 包活跃部分（`ContextRetriever`、`Cleaner`、`RunRetrieval`、`SnippetsToSystemMessages` 等）被 `manager_rag.go`/`production_manager_rag.go` 使用。
- **处置**：**砍**这 3 个死类型（文件级：rag.go/cache.go/user.go 若文件内仅含死类型则整文件删）。
- **理由**：生产记忆检索走 production_manager + retrievalservice 与 context 包的活跃检索面；RAG/Cache/UserMemory 是早期内存实现，无消费者。
- **影响面**：3 个文件 + 测试；执行前确认文件内无活跃导出。

### 15. ares_memory ToBuildContextFormat

- **位置**：`internal/ares_memory/manager.go`
- **现状**：仅 `manager_test.go` 引用。
- **处置**：**砍**。
- **理由**：纯测试专用死代码。
- **影响面**：`manager.go` 删方法 + `manager_test.go` 用例。

### 16. ares_evolution ScoreTrendAnalysis + 不可达分支

- **位置**：`internal/ares_evolution/rollback_policy.go`
- **现状**：`ScoreTrendAnalysis` 仅测试引用；`checkStart := len(p.scoreHistory)/2; if checkStart < 0` 不可达（len 永不为负）。
- **处置**：**砍**函数 + 死分支。
- **理由**：纯死代码；不可达 guard 是防御性噪声。
- **影响面**：`rollback_policy.go` + `rollback_policy_test.go` 相关用例。

### 17. ares_evolution 死字段（promoter.previousScores / experience.totalScore / mutation 常量）

- **位置**：`promotion/promoter.go:53-54,69,89`、`experience/types.go:249,335`、`mutation/constants.go`
- **现状**：`previousScores` 初始化+写入但从不读取；`totalScore` 累加但不放入返回值；`TypeDefault`/`ParamTemperature` 等常量未被引用（代码用字符串字面量）。
- **处置**：**砍**。
- **理由**：写而不读的死状态/死工作/死常量，属于误导性代码。
- **影响面**：3 个文件 + 相关测试。

---

## 三、保留（已接线或活跃）

| 子系统 | 理由 |
|--------|------|
| ares_experience（RankingService/ConflictResolver/Distillation/Feedback） | 第三轮修复已接线（production_manager.go:193-197、provide_distillation.go、provide_evolution.go、sdk/memory_wiring.go），生产活跃 |
| ares_protocol DLQ 本体（NewDLQ/Add/Remove/GetAll/Process） | `protocol.go:51` 生产创建；Process 近期修复有测试守护 |
| ares_archive 主体（sink/writer/reader/extract） | `cmd/ares/recall.go`、`serve.go`、`archive_hook.go`、`api_impl/store.go` 活跃 |
| ares_observability otel_tracer / prometheus | `llm/failover.go`、`client.go`、`dashboard`、`ares_evolution`、`workflow/graph` 活跃（仅 CostDashboard 砍） |
| ares_flight 主体（timeline/Summary/collector/recorder 核心） | `cmd/ares/flight.go`、`cmd/flight`、dashboard、api 活跃（仅砍 SetGenealogy/Snapshot） |
| monitoring tabs / EventTab / MainPage / cost_bar | `cmd/ares/serve.go`、`cmd/monitor-live` 活跃 |
| ares_eval/service（agent runner 路径） | `internal/api_impl/service.go` 接线活跃 |
| ares_arena service/scenario/survival/http/injector | `cmd/arena`、`serve.go`、`ares_runtime/manager.go`、`api/service/arena` 活跃 |
| ares_memory manager_impl / production_manager / context 活跃面 | 生产核心路径 |

---

## 四、执行顺序与验证

1. **先接线**（第 1 项 publisher 交互引擎）——功能补全，独立验证。
2. **再砍无依赖死代码**：#7（eval runner）、#9（AllowedActions）、#10（CapabilityEngine）、#11（scorer.go）、#15（ToBuildContextFormat）、#16（ScoreTrendAnalysis）——单个文件级，风险最低。
3. **再砍带联动**：#2（DetailPanel）、#12/#13（monitoring 桩）、#14（context 子包）、#17（进化死字段）、#8（flight 方法）。
4. **最后处理大面**：#5（CostDashboard）。
5. 每步执行 `go build ./...` + `go vet ./...` + `go test ./...`（或受影响包）。
6. 删除前用 `find_references` 再次复核"仅测试引用"，避免误删。

## 五、决策点确认记录（2026-08-12 用户确认）

| 决策点 | 结论 | 依据 |
|--------|------|------|
| #3/#4 ares_runtime 内置插件/路由器 | ✅ **保留**（能力储备，不动） | `ares-runtime-capability-reserve.md`：插件框架生产活跃（PluginBus/接口/chaosWrappedAgent），插件为完整实现+测试覆盖，未来能力点明确，砍的前提不成立 |
| #6 DLQProcessor/自动重试 | ✅ **保留**（含 StartAutoRetry） | 功能保障模块：DLQ 本体生产活跃，processor 是死信消费侧构成完整闭环；b1816c50 刚修复有测试守护。待办：标注 tech-debt 提示未来接线 |
| #2 DetailPanel | ✅ **砍** | HandleEvent 空实现=半成品，生产从不实例化，接口无消费者 |
| #13 MonitorPlugin 4 桩方法 | ✅ **砍** | 恒 ErrNotConfigured，生产无调用方 |

> 其余项目（#1 publisher 接线、#5 CostDashboard、#7-#17 砍）按方案正文执行。

---

## 六、执行记录（2026-08-13，实际落地）

按用户标准重新校准后执行——**完整实现 + 有测试 + 有能力点 = 能力储备（保留）**，与 ares_runtime 插件同标准；仅删除"调用者以为生效、实际空操作"的欺骗性空壳。

### ✅ 已接线（增强系统稳健性）

- **#1 monitoring publisher 交互引擎**（`internal/monitoring/plugin.go`）：根因是**构造顺序缺陷**——`NewConsole` 先创建 publisher（190 行）、后创建 `interEngine`（196-198 行），导致 publisher 永远拿不到交互引擎，kill/resume/retry 恒 501，即便调用方已传 `WithRuntimeManager`（serve.go 确实传了）。修复：将 `interEngine` 创建提前，并在 `interEngine != nil` 时通过 `WithInteractionEngine` 传给 publisher。补回归测试 `TestNewConsole/runtime_manager_wires_publisher_interaction_engine`。

### ✅ 已砍（唯一真正空壳）

- **`WithCostAlertThreshold`**（`plugin.go`）：空闭包 option，注释自认 placeholder，调用者传阈值却什么都不做——欺骗性 API，删除本体及对应测试。

### 🔄 由"砍"修正为"保留"（核查后为完整能力储备，非空壳）

| 项 | 原计划 | 核查结论 |
|----|--------|----------|
| #2 DetailPanel | 砍 | `GetDetail` 是完整实现（tracker/cost/linker 组装详情视图），`HandleEvent` 是有意 no-op（tracker 单独处理事件，有注释说明），完整+有能力点，保留 |
| #5 CostDashboard | 砍 | cost.go 547 行完整实现（HTML/HTTP/会话成本聚合）+ 有 cost_dashboard_test.go，自包含可挂载能力，保留 |
| #13 monitoring 4 桩（Events/CostAlerts/Interactions/MCPToolCalls） | 砍 | 是 `ConsoleAPI` 接口契约方法，**诚实返回 `ErrNotConfigured`**，不像已删的 ares_eval `placeholderRunner` 那样伪造 pass 数据；诚实占位，保留 |
| arena scorer.go | 砍 | 3 个 scorer（Ensemble/ExactMatch/Map）完整实现 regression.go 的活跃 `Scorer` 接口 + 有 scorer_test.go，保留 |

### 验证

- `go build ./...` ✅
- `go vet ./internal/monitoring/...` ✅
- `go test ./internal/monitoring/ -run 'TestNewConsole|TestPublisher_HandleKillAgent'` ✅

### 未执行项（ares_runtime 能力储备，按用户指示保留不动）

#3/#4（插件/路由器）、#6（DLQ）经 `ares-runtime-capability-reserve.md` 核实为能力储备，用户明确要求保留，本次不动。其余 #7-#17 死代码项（eval runner、AllowedActions、CapabilityEngine、ToBuildContextFormat、ScoreTrendAnalysis、进化死字段、flight SetGenealogy、context 死类型等）属纯清理、不影响系统完善度，可单独一轮处理。
