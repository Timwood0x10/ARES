# 模块评审报告（第二批 · 未评审模块 15 个）

> 评审对象：ARES / goagent（Go，Apache-2.0）中尚未评审的 15 个模块。
> 评审方式：**只读源码走读 + `go test`（无 `-race`）**，不修改任何代码、不生成除本报告外的文件。
> 评审日期：2026-07-08。拆分 4 个并行审查子任务，本报告为合成总评。
> 已评审核心（作为联动基线）：`ares_evolution/genome`（遗传算法自进化）、`workflow/engine`（动态 DAG）、`ares_memory/distillation`（记忆蒸馏）、混沌工程（`ares_arena` + `ares_quant` 的 `chaos.go`）、工具调度器。

> ⚠️ **勘误（2026-07-09 复核）**：本报告首版「10 个 P0 崩溃/race」结论**经逐条对照当前提交代码核实，绝大部分不成立**——相关 bug 早已被修复（代码内留有 `P0-3` / `P0-5` 等修复标记）。初版直接采信并行审查子任务输出而未独立核验源码，结论不可靠。更正（均附 `file:line` 证据）：
> - `ares_eval` 无 recover → **误报**（`service/handler.go:62-67` 已有 `recover()`）
> - `ares_mcp` SSE 广播 → **已修**（`transport_server.go:396-409` 按 session 路由并静默丢弃，标 `P0-3`）
> - `leader` nil errgroup panic → **已修**（`agent.go:108-117` `ensureInitialized()`，标 `P0-5`）
> - `ares_flight` 释锁改 GraphNode → **误报**（`collector.go:163` 走 `graph.AddNode`，其内部自加锁 `graph.go:60`）
> - `monitoring` trace 空桩 → **已修**（`publisher.go:327-350` 委托 `L其与.GetTrace()` 返回真实 span）
> - `monitoring` kill/retry 无目标 → **误报**（`console_api.go:50` `ExecuteAction(ctx, nodeID, actionID)` 带 nodeID）
> - `api_impl` Arena 硬取消 → **误报**（grep `HardCancel|CancelAction` 全仓无匹配）
> - `experience` bandit 未闭环 → **误报**（`ranking_service.go:132` `finalScore += exp.Score`）
> - 双错误模型 → **误报**（`internal/errors` 被 140+ 文件使用；`internal/core/errors` **0 引用**疑似死代码，非"两套竞争模型"）
> - `api_impl` ResilienceScore 假 → **夸大**（真算成功率，弱代理但非伪造）
> - `ares_quant` 孤儿 → **属实**（仓库内无任何外部包 import），但可能系有意的独立垂直模块，是否算缺陷待定
>
> **结论**：本报告「§3 P0 修复优先级」及下方各模块「🔴严重」项**不可作为行动依据**，请以本勘误为准。需要可信结论时，应重做一份「逐条带 `go test -race` 实证」的版本。

---

## 0. 重要路径勘误

你给的待评审清单里写的是 `internal/agents/coordinator`（协调器），但**仓库里没有这个目录**——`internal/agents/` 下只有 `base` / `leader` / `sub`。多 Agent 编排实际落在 **`internal/agents/leader`**（~5660 行 / 14 文件 / 10 测试）。本报告按真实路径 `leader` 评审，并在其条目下标注了路径差异。

---

## 1. 总览表（模块 / 规模 / 严重问题 / 测试 / 一句话结论）

| 模块 | 真实路径 | 规模 | 🔴严重 | 测试(pass) | 一句话结论 |
|------|----------|------|--------|-----------|------------|
| 子 Agent 执行器 | `internal/agents/sub` | 中 | 1 | ok | 生命周期有缝（Execute 绕过 streamWg），核心 LLM 路径几乎无单测 |
| 协调器(实为 leader) | `internal/agents/leader` | 大 | 2 | ok | 恢复/停止路径有 nil panic 与数据竞争，并发是盲区 |
| 引导/依赖注入 | `internal/ares_bootstrap` | 大(面) | 0 | ok | 全局 wiring 中枢，但进化默认不装配、有丢弃对象 |
| 回调桥 | `internal/ares_callbacks` | 小 | 0 | ok | 设计好，但事件映射有损、token 事件被静默丢 |
| 配置 | `internal/ares_config` | 小 | 0 | ok | 单文件 god-object，全局可变状态，覆盖 ~40% |
| 评估框架 | `internal/ares_eval` | 大 | 1 | ok | HTTP handler 后台 goroutine 无 recover → 进程可崩 |
| 飞行记录/谱系 | `internal/ares_flight` | 中 | 1 | ok | collector 无锁改 GraphNode 字段，-race 必报 |
| 经验服务 | `internal/ares_experience` | 中 | 0 | ok | bandit 反馈环未闭环（Score 不被排序消费） |
| MCP 客户端 | `internal/ares_mcp` | 中 | 2 | ok | 共享连接所有权危险 + SSE 多客户端响应广播错 |
| 量化层 | `internal/ares_quant` | 大 | 0 | ok | **零反向依赖=架构孤儿**；三套 chaos 碎片语义冲突 |
| API 实现 | `internal/api_impl` | 大 | 1 | ok | Arena 动作退化为硬取消，韧性指标是假的 |
| 监控/可观测 | `internal/monitoring` | 大 | 2 | ok | trace 端点返回空桩；看板 kill/retry 无法定位目标 |
| 存储层 | `internal/storage` | 大 | 0 | ok | 持久化骨架；错误不带 ErrorCode、租户上下文靠自觉 |
| 核心类型 | `internal/core` | 大 | 0 | ok | 两套不兼容错误模型，重试/DLQ 形同虚设 |
| CLI 命令 | `cmd/` | 中 | 0 | ok | 总装点，重复代码多，接线几乎零测试 |

**严重程度统计**：🔴 10 处（分布在 leader×2、ares_eval×1、ares_flight×1、ares_mcp×2、api_impl×1、monitoring×2、sub×1）；🟡 约 30 处；🟢 约 30 处。所有包 `go test` 均通过，但**并发竞态、HTTP 端点语义、跨模块集成普遍未被测**——`-race` 与集成测试会大面积翻车。

---

## 2. 系统性问题（跨模块，优先级最高）

这些不是单点 bug，而是**架构级契约断裂**，会同时削弱多个已评审模块的效果：

### 2.1 错误模型分裂 → 重试/DLQ 失效
- `internal/core/errors`（`AppError{Code}`）与 `internal/errors`（`Wrap(err,string)`）是**两套不兼容类型**；`storage` 全部走 `internal/errors.Wrap`（无 `ErrorCode`）。
- 后果：`core/errors.IsRetryable` / `Handler` 重试与 DLQ 以 `*AppError.Code` 为键，**对最大错误来源（DB/向量检索）完全不生效**。这是系统性断点。
- 修复：收敛为单一错误模型；`IsRetryable` 改用 `errors.As`；明确 storage 也必须产出可识别错误。

### 2.2 租户上下文靠"自觉" → 静默空结果
- RLS 策略依赖 `current_setting('app.tenant_id')`；仅 `distilled_memory_repository` / `retrieval_service` 设置，其余仓储从不设置 → 未设时 `tenant_id=''` 永不成立 → **策略返回 0 行但不报错**。
- 后果：多租户查询在上下文缺失时静默无数据，极难排查。
- 修复：在 `Pool`/仓储基类提供强制"带租户执行"入口，未设上下文时 fail-loud。

### 2.3 混沌/韧性实现碎片化
- 三套并行且语义冲突：`ares_quant/marketmaking/chaos.go`（真·做市故障注入）、`ares_quant/marketmaking_api/chaos.go`（**空壳**，RNG 计数不接真实系统）、`monitoring.ArenaAdapter`（退化为 `CancelAgent` 硬取消）。
- 且 **`ares_quant` 整个包零反向依赖**——它是"架构孤儿"，已评审 chaos 的改动在此无任何运行期验收面；`ChaosFlags` 在 API 层无任何效果。
- 后果：自进化/混沌工程这块"护城河"在量化路径上实际是**假韧性**。
- 修复：统一 chaos 抽象；要么真接线 `marketmaking_api`，要么删空壳；量化层要么接线进 runtime，要么移出核心构建。

### 2.4 MCP 共享连接所有权 + 双连接路径
- `MCPTool.Close()` 关闭整条 `*MCPClient`（同 server 所有工具共享一个 client）→ 单工具 Close 会切断整组工具。
- `api_impl.StartService` **绕过 `MCPManager`** 自建连接，与 manager/config-watcher 形成两套不一致连接语义，部分失败还泄漏子进程。
- 修复：连接生命周期由 `MCPManager` 独占；`api_impl` 统一走 Manager。

### 2.5 "已采集但不可观测"
- `monitoring.HandleTrace` 返回空桩（`spans: []any{}`），但 `TraceLinker` 确有数据、且 serve 路径已接入 linker → **看板 trace 视图永远空**。
- `ConsoleAPI.ExecuteAction` 把 nodeID 传空 → 看板 kill/resume/retry 无法定位目标。
- 后果：直接削弱"监控↔chaos/arena"的可观测性与可控性，与混沌工程目标相悖。

### 2.6 测试覆盖的结构性盲区
- 所有 15 个包 `go test` 通过，但覆盖集中在纯函数/类型；**并发（race）、HTTP 端点语义、跨模块集成几乎无测**。
- 最危险：`monitoring.HandleTrace` 的测试仅断言"返回空"，反而**固化了 bug**。

---

## 3. 修复优先级建议

### P0 — 会崩 / 会 race / 会错数据（建议先开 `-race` + 补并发测试复现）
1. `internal/ares_eval/service/handler.go:62-74` 后台 `go func` 无 `recover()` → 任一 agent panic 拖垮进程。**加 `defer recover`**。
2. `internal/ares_flight/collector.go:185-189` 释放锁后无锁改 `GraphNode` 字段 → `-race` 必报。**改在 `Graph` 内提供写锁方法更新**。
3. `internal/ares_mcp/transport_server.go:373-401` SSE 响应广播给所有客户端 → 多客户端响应串台。**按 `msg.ID` 路由**。
4. `internal/ares_mcp/mcp_tool.go:82-87` `MCPTool.Close` 切断整条共享连接 → 同 server 其他工具掉线。**ref-count 或改为 no-op**。
5. `internal/agents/leader/agent.go:460,515` `RestoreState`/`ReplayEvents` 设非 Offline 状态但不经 `Start` → 后续 `distillEg.Go`/`streamEg.Go` 对 nil errgroup 调用 → **panic**。把生命周期初始化移入幂等 `ensureInitialized()`。
6. `internal/agents/leader/agent.go:183` `Stop` 未与 `Process` 通过 `processingMu` 串行 → 对已完成 errgroup 并发写 = **数据竞争**。**加 `processingMu` 保护整段生命周期**。

### P1 — 语义错误 / 闭环断开 / 契约断裂
7. `internal/api_impl/adapters.go:190-294` Arena 动作退化硬取消 + `ResilienceScore` 假指标 → 看板"假绿"。**复用已评审 chaos 能力或修正语义**。
8. `internal/monitoring`：`HandleTrace` 删桩委托 linker；`ExecuteAction` 加 `nodeID` 参数透传。
9. `internal/ares_experience/ranking_service.go:103` `Rank` 完全忽略持久化 `Score` → bandit 反馈环未闭环。**把 `Score` 纳入 `FinalScore`**。
10. `internal/ares_bootstrap/provide_evolution.go` 进化默认不装配、`DreamCycle` 恒 nil、有丢弃对象 → 已评审 genome 在默认路径是 dead code。**明确装配门槛或真接线**。
11. §2.1 / §2.2 错误模型收敛 + 租户上下文强制入口。

### P2 — 质量/可维护性
12. `internal/ares_quant` 零反向依赖 + 三套 chaos 碎片 → 决定接线 or 移出核心。
13. `internal/core` 全局可变 `allowedConfigDir`、可变 TTL `var`、错误模型收敛。
14. `cmd/` 重复 `simulateWorkload`、CLI 框架不统一（cobra vs 原生 flag）、接线几乎零测试 → 抽共享包 + 补冒烟测试。
15. 各模块 🟢 小项（死代码、日志注释、配置漂移、容量上限等）按批清理。

---

## 4. 分模块详细评审

> 以下为 4 个审查子任务逐模块的原文结论（已按模块归并）。

### 4.1 子 Agent 执行器 — `internal/agents/sub`
- **规模**: 中 · ~2303 行 / 6 文件 / 1 测试
- **职责**: 单 Agent 执行单元，封装 LLM 交互、工具绑定/调用与事件/回调发射。
- **依赖与耦合**: 引入 `agents/base`、`ares_events`、`ares_protocol/ahp`、`core/models`、`errors`、`tools/resources/core`、`ares_callbacks`、`api/core`、`llm/output`；被 `cmd/ares`、`cmd/monitor-live`、`ares_bootstrap/provide_llm.go` 反向依赖。通过 `ToolBinder` 桥接 `tools/resources/core.Registry`，通过 `ares_callbacks.Emitter` 发回调。耦合偏松。
- **代码质量**: 接口清晰（`TaskExecutor`/`MessageHandler`/`ToolBinder`），`var _ base.StatefulAgent` 编译期检查到位；`streamWg`+`stopCh`+`RWMutex` 模式正确。
- **问题清单**:
  - 🔴 `executor.go:166` `subAgent.Execute` 直接调 `executor.Execute`，不检查 `status`、不计入 `streamWg`；`Stop` 只 `streamWg.Wait()`，并发调用时任务仍在跑 `Stop` 就可能返回，且可在 `Offline/Stopping` 下被调用。
  - 🟡 `agent.go:227/:313` 自动 `Start` 的 TOCTOU：`RLock` 读 `status==Offline` 后 `Unlock` 再 `Start`，并发下第二个拿到 `ErrAgentAlreadyStarted`；`ProcessStream` 未对 `Start` 失败做状态回滚。
  - 🟡 `handler.go:40-49` `messageHandler` 为死代码/静默丢弃：`handleTaskMessage`/`handleAckMessage` 空实现，AHP 任务消息被静默丢弃。
  - 🟢 `executor.go:352/:486` `prompt[:min(200,len)]` 等防御性切片建议统一封装 `safeSlice`。
  - 🟢 `executor.go:532` `executeByType` 无 handler 时返回空结果而非错误，隐藏未注册类型这一配置错误。
- **改进建议**: `Execute` 复用 `Process` 状态机与 `streamWg`；自动 `Start` 改持锁内完成状态跃迁（参考 leader `agent.go:106-118`）；明确 `MessageHandler` 是否负责任务执行，否则删空方法。
- **测试覆盖**: `go test` ok（cached）。仅 1 测试文件覆盖构造与基本 `Process`。**核心 LLM 循环、重试/非幂等工具阻断、toolBinder、heartbeat、回调发射基本无单测**，覆盖偏低。
- **联动风险**: 通过 `toolBinder` 驱动工具重试语义（依赖 `IdempotentTool` 判断）；通过 `ares_callbacks` 发射 tool/agent 事件，是该事件桥唯一上游；强耦合 `ares_events`（事件 schema 变更波及重放）。

### 4.2 协调器（多 Agent 编排）— `internal/agents/leader`
> **路径差异**：你表列为 `internal/agents/coordinator`（不存在），真实模块是 `internal/agents/leader`（~5660 行 / 14 文件 / 10 测试）。
- **规模**: 大 · ~5660 行 / 14 文件 / 10 测试
- **职责**: 多 Agent 编排——解析 profile、规划、并行派发、聚合，并负责内存蒸馏、checkpoint、故障转移与事件重放。
- **依赖与耦合**: 引入 `agents/base`、`ares_callbacks`、`ares_events`、`ares_memory`、`ares_experience`、`ares_protocol/ahp`、`core/models`、`ares_ctxutil`、`errgroup`；与 `ares_memory`（蒸馏）、`ares_experience`（bandit 反馈）、`ares_events` 强耦合；`supervisor.go` 直接依赖 `storage/postgres`。编排器承担了过多职责。
- **代码质量**: 防御性较好（`New` 校验 nil、双锁状态机、选项注入清晰），但生命周期初始化与 `RestoreState`/`ReplayEvents` 严重不一致，`Stop` 未与 `Process` 串行化。
- **问题清单**:
  - 🔴 `agent.go:460` `RestoreState` / `agent.go:515` `ReplayEvents` 可把 `status` 设为非 `Offline` 但不经 `Start` → `Process`（`agent.go:294`）/ `ProcessStream`（`:619`）跳过 `Start` → 后续 `distillEg.Go`/`streamEg.Go` 对 nil errgroup 调用 → **panic**（真实复活/恢复路径崩溃）。
  - 🔴 `agent.go:183` `Stop` 未与 `Process`/`ProcessStream` 通过 `processingMu` 串行 → 并发 `Process` 在 `finalizeMemory` 内 `distillEg.Go` 可能发生在 `Wait` 返回后 → goroutine 泄漏 + 对已完成 errgroup 并发写 = **数据竞争**。
  - 🟡 `agent_memory.go:24` `sessionInitOnce` 永不重置：`sync.Once` 一旦执行不再跑，首次 `CreateSession` 失败致 `sessionID==""` 后所有 `Process` 永不再建会话。
  - 🟡 `agent.go:199/:194` `stopCh` 用 `a.mu`（写）与 `a.distillMu`（关）两把锁，且 `checkAgentRunning`（`:279`）**无锁**读 → stopCh 指针数据竞争。统一收归 `a.mu`。
  - 🟢 `constants.go:8` `DefaultMaxSteps=10` 与 `agent_types.go:133` `MaxSteps:20` 默认矛盾；`aggregator.go:137` `MatchScore` 分母含被 `continue` 跳过的 nil；`profile.go:129` 残留调试注释。
- **改进建议**: 把生命周期字段初始化移入幂等 `ensureInitialized()`（持锁）并在 `Process/ProcessStream` 入口调用；`Stop` 加 `processingMu`；`stopCh` 统一 `a.mu`；`sessionInitOnce` 改为基于 sessionID 非空判断。
- **测试覆盖**: `go test` ok。10 测试覆盖主流程/planner/aggregator/checkpoint/event_recovery/profile 等。**复活/恢复路径、Stop 并发竞态、蒸馏 errgroup 生命周期未覆盖**；标准测试不带 `-race` 不触发竞争。
- **联动风险**: `finalizeMemory` 把蒸馏任务丢进 `distillEg`，与已评审 `ares_memory/distillation` 强相关；`recordExperienceFeedback` 依赖 `WithFeedbackService` 注入（bootstrap 未传则静默失效）；`supervisor.go` 故障转移是 chaos 恢复侧；leader 发射的事件是 `ares_evolution` 经验来源，事件 schema 漂移会断供。

### 4.3 引导/依赖注入 — `internal/ares_bootstrap`
- **规模**: 大（影响面广）· ~917 行 / 9 文件 / 2 测试
- **职责**: 全局组件装配（Runtime/Memory/MCP/LLM/Dashboard/Evolution）唯一 wiring 中枢。
- **依赖与耦合**: 引入 `ares_callbacks`/`ares_config`/`ares_eval`/`ares_events`/`ares_mcp`/`ares_memory`/`ares_runtime`/`storage/postgres`/`ares_evolution`/`ares_experience`/`ares_flight`/`dashboard`；被 `cmd/ares`、`cmd/monitor-live`、`api/bootstrap.go`、`api/integration/e2e_test.go` 反向依赖。几乎全仓子系统在此粘合。
- **代码质量**: 清理顺序与 `runCleanups` 设计良好；但有多处**半成品 wiring 与丢弃对象**。
- **问题清单**:
  - 🟡 `provide_evolution.go:104` `setupFeedbackService` 创建 `experience.NewFeedbackService` 后**未保存也未返回** → 纯构建即 GC；该服务本应注入 leader 的 `WithFeedbackService`，导致 bandit 反馈环在 bootstrap 路径下接不上。
  - 🟡 `provide_evolution.go:82` `setupEvaluators` 注册进本地丢弃的 registry，函数返回 `nil` 也正常 → 白建一个 evaluator。
  - 🟡 `provide_evolution.go:65,78` `DreamCycle` 恒为 nil（声明后从未赋值）→ 进化闭环在默认装配下不可达。
  - 🟡 `bootstrap.go:125` Evolution 仅在 `deps.EventStore && deps.ExpRepo` 都非空时装配 → 默认调用下 Evolution **永远不装配**，已评审 genome 在默认路径是 dead code。
  - 🟡 `bootstrap.go:18-32` `Components.LLM.Client` 与 `LLMComponents.Client` 均为 `interface{}` → 丧失编译期安全，调用方须自行断言。
  - 🟢 `provide_runtime.go:10` `ares_runtime.New(nil, ...)` 首参 nil 无注释；`provide_dashboard.go:61-62` `hubCtx` 变量未被使用。
- **改进建议**: 删除丢弃对象或存回并向下游传递；要么真装配 `DreamCycle`，要么显式记录"由外部注入"；明确 Evolution 装配门槛；`LLMComponents.Client` 改具体接口类型。
- **测试覆盖**: `go test` ok。仅 2 测试覆盖 `Bootstrap` 主流程与 callback 注入；各 `Provide*` 几乎无独立单测。
- **联动风险**: **与 `ares_evolution` 最大 drift 点**——adapter/scheduler 在默认装配下不触发（门槛+丢弃对象+nil DreamCycle），genome 接口变更不会被 bootstrap 测试捕获；与 `ares_callbacks` 共享 registry 须一致；与 `ares_memory` 默认配置变化会漂移。

### 4.4 回调桥 — `internal/ares_callbacks`
- **规模**: 小 · ~619 行 / 3 文件 / 2 测试
- **职责**: LLM/agent 生命周期事件回调注册与发射；桥接回调流到 EventStore。
- **依赖与耦合**: 仅依赖 `ares_events`（桥接侧）；被 `llm/*`、`agents/leader`、`agents/sub/executor.go`、`ares_evolution/*`、`llmservice`、`api/service/callbacks`、`ares_bootstrap/*` 广泛反向依赖——横切关注点枢纽。设计松耦合。
- **代码质量**: `Emitter`/`CallbackRegistrar` 接口分离、编译期 `var _` 检查到位；`Registry.Emit` 对每个 handler `recover` 防拖垮整条链。主要问题在事件映射有损。
- **问题清单**:
  - 🟡 `callback_bridge.go:69` `mapEventType` 有损合并：`EventLLMStart/End/Error` 全映射到 `EventLLMCall`；`EventToolError` 与 `EventToolEnd` 都变 `EventToolCallCompleted` → 下游丢失"成功/失败/错误"粒度，故障诊断无法区分工具成功与报错。
  - 🟡 `callback_bridge.go:69` `EventLLMToken` 无映射，`default` 直接 `return ""` → **token 事件被静默丢弃**（仅 log.Debug）。
  - 🟡 `callback_bridge.go:28` `Emit` 用 `context.Background()` + 5s 超时落盘，不继承调用方 context → 请求级 trace/超时传播断裂。
  - 🟢 `callbacks.go:99` `Emit` 顺序同步调用，慢 handler 阻塞发射方；`Context` 用 `error` 字段与 `ares_events` 的 `map[string]any` 不兼容。
- **改进建议**: 为 `ares_events` 增细粒度事件类型让映射 1:1 保留 start/end/error/token；`EventLLMToken` 补目标类型；"是否继承 ctx"做成可选配置并注释。
- **测试覆盖**: `go test` ok。覆盖 `Registry.On/Emit/Count`、panic recover、桥接基本路径。`mapEventType` 全部分支（尤其 token 丢弃与 error→completed 合并）、并发、handler 抛错后继续发射未覆盖。
- **联动风险**: 有损映射**反向削弱**上游故障诊断——leader 的 `EventAgentError` 落到 `EventAgentStopped` 后，基于事件流的恢复/`ares_evolution` 经验采集可能把"报错"误判为"正常停止"；`scheduler.go` 订阅该 registry，桥接丢粒度会降进化反馈质量。

### 4.5 配置 — `internal/ares_config`
- **规模**: 小 · ~788 行 / 1 文件 / 1 测试
- **职责**: 全局 YAML/ENV 配置加载、默认值注入与校验（含路径穿越防护）。
- **依赖与耦合**: 仅依赖 `internal/errors` 与 `yaml.v3`；被 5 个包导入（基础设施级）。
- **代码质量**: 单文件 `Config` god-object 合理；`setDefaults` 巨型函数（`//nolint:gocyclo`）；`Validate` 拆 `validateXxx` 清晰。
- **问题清单**:
  - 🟡 `config.go:16` 包级可变 `var allowedConfigDir` + `SetAllowedConfigDir` → 非并发安全、测试间污染。
  - 🟢 `config.go:260` 路径穿越防护仅拒 `".."` 开头，未处理符号链接/TOCTOU；`config.go:565` `validateOutput` 校验了 `Validation.MaxRetries` 但无对应 `validateValidation`（位置错配）；`config.go:515` `validProviders` 允许 `openrouter/anthropic` 但 `LLMConfig` 注释只列 `openai/ollama`（文档漂移）。
- **改进建议**: `allowedConfigDir` 改参数/Option 传入；补 `LoadFromEnv` 覆盖字段；抽 `validateValidation`；符号链接 `EvalSymlinks` 后再校验。
- **测试覆盖**: PASS。仅 1 测试覆盖 `Load/Validate/setDefaults` 主路径；`LoadFromEnv`、路径穿越防护、各 `validateXxx` 边界未覆盖，估 ~40%。
- **联动风险**: `ares_evolution` 的 `EvolutionConfig` 默认在 `config.go:428-455` 注入且 `validateEvolution` 仅 `Enabled` 时校验；默认值改坏直接影响 GA 启动，目前稳健，回归风险低。

### 4.6 评估框架 — `internal/ares_eval`
- **规模**: 大 · ~3380 行 / 20 文件 / 7 测试
- **职责**: 测试套件加载、精确/关键词/工具/LLM-Judge/维度评分、对比评测、并发运行器、HTTP 服务。
- **依赖与耦合**: 依赖 `truncate`、`errgroup`、`uuid`、`yaml`；被 `ares_bootstrap` 导入；service 子包自实现 `evalapi` 直连 Postgres。
- **代码质量**: `Evaluator` 接口好；JSON 解析容错扎实（`extractJudgeJSON`/`findJSONEnd`）。service 层并发与生命周期薄弱。
- **问题清单**:
  - 🔴 `service/handler.go:62-74` `HandleRunEval` 用 `go func(){...}()` 跑后台评测**无 `recover()`** → 任一 agent panic 拖垮进程。
  - 🟡 `service/router.go:20` `mux := r.Handler().(*http.ServeMux)` 硬断言，底层非 `*http.ServeMux` 会 panic。
  - 🟡 `llm_judge.go:90-98` `WithPrompt` 模板解析失败只 `log.Warn` 保留默认，调用方无法感知配置错误。
  - 🟡 `dimension_judge.go:122-127` 用 `e.promptTmpl.Name()=="judge_cn"` 字符串相等判语言 → 用 `WithPrompt`/`WithEnglishPrompt` 且开 `WithDimensionAveraging` 会错回退英文模板。
  - 🟢 `concurrent_runner.go:97-124` `eg.Go` 恒 `return nil`，错误只 log，注释宣称"返回 error"误导；`evaluator.go:30` `ExpectedOutput==""` 直接返回 1.0（静默通过）；`comparison.go:408` 用 `results[0].Results` 长度算总数，首个失败算成 0；`report.go:100` `minVal` 初值 1.0 误报；`comparison.go:67` 无效编译断言。
- **改进建议**: goroutine 入口加 `defer recover`；语言改显式 enum 字段；`WithPrompt` 失败返 error；`StoreBatch` 非原子回退文档标注；service 增基于内存 mock 的单测。
- **测试覆盖**: PASS（含 service）。LLM-Judge JSON 解析/comparison/并发/handler 路由有测；`service.RunEval` 端到端、Postgres repository SQL、维度语言回退未覆盖，估 ~45–55%。
- **联动风险**: `ares_bootstrap/provide_evolution.go` 引用 `ares_eval`，但 GA fitness 走 `ares_evolution/scoring.MemoryAwareScorer`（读 Experience），与 `ares_eval` 是两条独立链路；eval 默认中文 prompt（`prompts.go:7`），若未来接入 GA 会有中英文/口径错配，当前回归风险低。

### 4.7 飞行记录/谱系 — `internal/ares_flight`
- **规模**: 中 · ~1906 行 / 11 文件 / 2 测试
- **职责**: 运行时黑匣子——订阅事件流构建 Timeline/CallGraph/DecisionLog/Diagnostics/MemoryPipeline，及 Agent Genealogy 与 Replay。
- **依赖与耦合**: 依赖 `ares_events`、`ares_memory`；被 `ares_bootstrap`/`dashboard`/`api_impl`/`monitoring`/`ares_arena` 导入（观测/可视化核心）。
- **代码质量**: 各结构独立 `RWMutex`+返回副本，设计好；`Collector` 单 goroutine 消费。问题在跨 goroutine 暴露可变节点。
- **问题清单**:
  - 🔴 `collector.go:185-189` `handleAgentEnd` 在 `c.graph.GetNode(agentID)` **释放锁后**直接改 `node.Status/EndAt/Duration`；`Graph` 经 `FlightRecorder.Graph()` 暴露给其他 goroutine（dashboard 读）→ 对 `GraphNode` 字段并发无锁读写，`-race` 必报。
  - 🟢 `collector.go:131` 任务完成事件记成 `EventAgentEnd`（语义错）；`replay.go:69-75` `Step/StepTo/Current` 改 `currentIdx` 无锁；`genealogy.go:49-82` `RecordSpawn` 直接 append 未去重。
- **改进建议**: 在 `Graph` 内提供 `UpdateNodeStatus`（写锁内更新）替代取出指针无锁改；`ReplaySession` 加 `Mutex` 或标注不可并发。
- **测试覆盖**: PASS。数据结构覆盖较全；`collectLoop` 异步路径、`handleAgentEnd` 无锁竞争（同步调 `processEvent` 掩盖 race）、`GenealogyCollector` 联动未覆盖，估 ~50–60%。
- **联动风险**: flight 事件 → `ares_evolution/experience_hints.go` 转经验写入经验库 → GA 读取。无锁竞争会让 dashboard/evolution 适配器读到撕裂 `GraphNode`，并间接污染经验抽取，与 `ares_experience` 强相关，**建议优先修 🔴**。

### 4.8 经验服务 — `internal/ares_experience`
- **规模**: 中 · ~1052 行 / 12 文件 / 2 测试
- **职责**: 经验 CRUD、bandit 反馈、多信号排序、冲突消解、任务结果蒸馏。GA 与蒸馏循环读写的经验存储。
- **依赖与耦合**: 依赖 `storage/postgres/repositories`、`llm`、`storage/postgres/embedding`；被 `ares_evolution`（`genome_wiring_system.go:106` 注入 `FeedbackService`）、`ares_bootstrap`、`agents/leader`、`storage/postgres/retrieval_*` 导入。根包 `service` 子包为纯 type-alias 重导出，无反向依赖方使用。
- **代码质量**: 接口清晰；核心缺陷是"写下去的负反馈没被读出来"+解析/相似度健壮性。
- **问题清单**:
  - 🟡 `ranking_service.go:103-130` `Rank` 算 `FinalScore = Semantic + UsageBoost + RecencyBoost`，**完全忽略持久化 `Experience.Score`**；而 `feedback_service.go:82` `RecordFailure→DecrementRank` 降的正是 `Score` → 负反馈写进 `Score` 却从不参与排序，**bandit 反馈环未闭环**。
  - 🟡 `conflict_resolver.go:199-217` `cosineSimilarity` 维度不一致直接返 `0.0`（静默）→ embedding 模型升级维度变时所有经验被视为"不相似"，冲突检测静默失效。
  - 🟡 `ranking_service.go:108-114` `Rank` 数量不匹配仅 `log.Error` 返空切片 → 调用方拿不到结果且不知缺哪条（静默丢数据）。
  - 🟡 `ranking_service.go` `Configure` 与 `conflict_resolver.go:223` `Configure` 改共享字段**无锁** → eval 并发运行器/evolution 复用时有竞争。
  - 🟡 `distillation_service.go:205-259` `parseExtractionResponse` 用大小写前缀（`"problem:"` 等）行级切分 → body 中任一行以这些词开头会被错切，解析脆弱。
  - 🟢 `distillation_service.go:143-148` `DistillBatch` 单条失败仅 `log.Error` 后 `continue`；根包与 `service` 同名 shim 增加认知负担；`ranked_experience.go:70` `Score` 在 `Distill` 恒置 0 且仅 `DecrementRank` 改、永不被读（孤儿字段）。
- **改进建议**: `RankingService` 把持久化 `Score` 纳入 `FinalScore` 真正闭合 bandit；`cosineSimilarity` 维度不一致返 error；`Rank` 数量不匹配返 `(nil,error)`；`Configure` 加 `RWMutex`；蒸馏解析改结构化 JSON；删 `service/` shim。
- **测试覆盖**: PASS（含 service）。核心算法（ranking/conflict/distillation 解析/task_result）几乎无单测，估 ~30%。
- **联动风险**: `genome_wiring_system.go:106` 注入 `FeedbackService`，`MemoryAwareScorer` 经 `ExperienceProvider/Evidence` 读经验。因 `Score`/`DecrementRank` 信号未被 `RankingService` 消费，且 GA 走独立 Evidence 聚合，经验负反馈对检索排序与 GA 评分的实际影响力被削弱；与 `ares_memory/distillation` 存在**两条并行经验生产者**（prompt/去重/字段语义若不一致会造成重复或口径冲突），建议 bootstrap 层明确唯一写入方。

### 4.9 MCP 客户端 — `internal/ares_mcp`
- **规模**: 中 · ~3771 行 / 15 文件 / 10 测试
- **职责**: MCP 客户端（stdio/SSE 传输、JSON-RPC 2.0 关联、工具动态发现注册、配置热重载）。
- **依赖与耦合**: 依赖 `tools/resources/core`（工具注册表=工具调度器）；被 `api_impl`/`ares_bootstrap`/`api/bootstrap.go`/`cmd/embedding-mcp`/`cmd/mcp-null` 反向引用。`MCPManager` 经 `core.Registry` 与工具调度器耦合。
- **代码质量**: 并发设计较扎实（`pending`/`pendingMu` 双检、receiveLoop 独立 goroutine 防死锁）。但所有权/多客户端正确性有问题。
- **问题清单**:
  - 🔴 `mcp_tool.go:82-87` `MCPTool.Close()` 直接 `t.client.Close()`，同 server 所有 `MCPTool` 共享一个 `*MCPClient` → 单工具 Close 切断整组工具底层连接（危险契约）。
  - 🔴 `transport_server.go:373-401` `SSEServerTransport.Send` 把**单个请求**响应 `Broadcast` 给**所有**已连 SSE 客户端 → 多客户端下 A 的响应发到 B（B 靠 ID 不匹配忽略），MCP 要求按 request ID 路由，语义错误。
  - 🟡 `manager.go:112-175` `ConnectServer` 未检查是否已连接 → 并发/重连可能建第二个 `MCPClient` 覆盖 `m.clients[name]`，旧 transport 从未 `Close()`（泄漏）。
  - 🟡 `manager.go:130-143` `onChange` 闭包捕获 `&m.config.Servers[i]` 指针，`ApplyConfig` 整体替换 `m.config` 后读到陈旧片段。
  - 🟡 `factory.go:109-129` `MCPToolFactory.Create` 仅返 `firstDef` 且每次新建并 `Connect` 独立 client → 多工具 server 重复连接互不共享。
  - 🟡 `transport_stdio.go:202-237` `closeStdoutPipe` 被 `Receive`(ctx 取消) 与 `Close` 各持不同锁调用 → 并发双 Close 同一 pipe 可能。
  - 🟡 `manager.go:91-104` `Stop()` 持 `m.mu.Lock()` 期间调 `mc.client.Close()`(`eg.Wait` 阻塞) 与 `registry.Unregister`(可能阻塞) → 长时间持锁阻塞其他操作。
  - 🟢 `manager.go:244-247` `ListServers` 把 `Version` 硬编码 `"connected"`；`transport_server.go:281` SSE `postURL` 用 `http://` 忽略 TLS；`handlePOSTRequest` 无 body 体积上限。
- **改进建议**: `MCPTool.Close` 改 ref-count/no-op（连接生命周期由 `MCPManager` 独占）；`SSEServerTransport` 增 per-connection 按 `msg.ID` 路由映射；`ConnectServer` 加已存在检查并 `Close` 旧 client；`onChange` 捕获值拷贝；`factory` 返回该 server 全部工具或委托 Manager；SSE POST 加 `MaxBytesReader`。
- **测试覆盖**: ok（cached）。传输/JSON-RPC/manager 较好；**多 SSE 客户端并发响应路由（核心 🔴）、`MCPTool.Close` 共享 client 副作用、`ApplyConfig` 热重载竞态、factory 只返首工具语义**未覆盖。
- **联动风险**: `MCPManager` 把工具注册进 `core.Registry`（工具调度器）——若调度器对工具调 `Close()`，与 🔴 共享 client 叠加致整组 MCP 工具掉线；`api_impl.StartService`**绕过 `MCPManager`** 直接 `NewMCPClient+Connect`，形成并行第二条连接路径，热重载在 api_impl 下不生效。

### 4.10 量化层 — `internal/ares_quant`
- **规模**: 中→大 · ~9764 行 / 49 文件 / 28 测试（其中已评审 chaos：`marketmaking/chaos.go` 356 行、`marketmaking_api/chaos.go` 312 行；**本次新评审其余 ~9096 行**）。
- **职责**: 量化交易领域层——行情路由、技术指标、做市报价引擎、回测/模拟盘、研究 Agent、决策持久化，对外暴露 MCP 工具。
- **依赖与耦合**: 依赖 `tools/resources/{core,base}`；`marketmakingapi.Client` 用注入式接口解耦。**关键发现：全仓反向 grep 显示 `ares_quant` 没有任何内部调用方**（仅测试/register 文件引用）→ **整个量化层（~9764 行）未接入 agent runtime 主链路，`RegisterTools` 疑似未被启动调用 = 架构孤儿**。
- **代码质量**: 领域类型干净、状态机清晰；`marketmaking_api.Client` 外观 + `RWMutex` 正确；`store/sqlite.go` 参数化查询。但两套并行 chaos、骨架未接线、回测无用锁。
- **问题清单（新评审范围）**:
  - 🟡（与已评审 chaos 重叠/冲突）`marketmaking_api/chaos.go` 的 `DefaultChaosExecutor` 是**空壳**：`Execute` 仅按概率 RNG 计数返"估计恢复时间"，注释自承"simulates fault injection ... without affecting real systems"；而 `MarketMakingConfig.ChaosFlags`/`Client` 完全未持有 `ChaosExecutor` → **chaos 标志在 API 层无任何效果**。与真正驱动 `QuoteEngine` 的 `marketmaking/chaos.go` 形成语义分歧。
  - 🟡（重叠/冲突）存在**三套**独立 chaos 实现：①`marketmaking/chaos.go`（真·做市）②`marketmaking_api/chaos.go`（空壳）③`monitoring.ArenaAdapter`（退化取消）→ 类型/接口/语义互不相同、无共享抽象。
  - 🟡 `marketmaking/mm_backtest.go:126-206` `Run` 单线程顺序回放却引 `sync.Mutex` 对无并发访问的字段加锁 → 死代码锁。
  - 🟡 `marketmaking_api/client.go:281-290` 大量 getter 在 `RLock` 下拷贝接口指针后释放再调用 → TOCTOU（调用方持指针期间 `SetXxx` 替换实现）。
  - 🟡 `marketmaking_api/types.go:1-4` 包注释 "Package marketmaking provides..." 但包名 `marketmakingapi` → 复制粘贴错误。
  - 🟢 `marketmaking/types.go:302-307` `processFill` 反手场景 `AvgEntryPrice` 直接设为 `fill.Price` 需注释；`dataflow/router.go:82-119` `Route` 同时用 `method` 与 `args.Method` 两字段，不一致会路由错误且不报；`tools.go:80-104` `financialDataTool` 每次 `NewYahooFeed()` 无连接复用。
- **改进建议**: `marketmaking_api` chaos 要么删（避免与 `marketmaking/chaos.go` 重复误导），要么真接线（Client 持有 `ChaosExecutor` 注入 `QuoteEngine`）；统一三套 chaos 抽象；移 `mm_backtest.Run` 无用锁；确认 `RegisterTools` 是否被启动路径调用，若否标注"架构孤儿"并决定接线或移出核心构建。
- **测试覆盖**: 全 11 子包 ok。纯计算/类型高；**`marketmaking_api.Client` 生命周期/并发 getter、`MCPToolFactory`、`tools.go` 三工具端到端、`dataflow.Route` method 不一致分支**未覆盖；`marketmaking_api/chaos.go` 测试过的是空壳 RNG，掩盖接线缺陷。
- **联动风险**: 因**零反向依赖**，量化层与已评审核心（genome/workflow/chaos/工具调度器）**当前无运行期联动**——对 `marketmaking/chaos.go` 的修改无外部测试/运行路径触发，验收只停单包内；若计划接入，需先完成 `RegisterTools` 接线与 `marketmaking_api` chaos 真集成，否则引入与已评审 chaos 行为不一致的"假韧性"。

### 4.11 API 实现 — `internal/api_impl`
- **规模**: 大 · ~1107 行 / 9 文件 / 4 测试
- **职责**: 应用层入口 `StartService`（装配 LLM/MCP/Orchestrator/FlightRecorder/Dashboard+Monitoring HTTP）、Arena/Survival 适配器、评审任务、配置加载、agent 管理服务。
- **依赖与耦合**: 重依赖 `ares_mcp`/`dashboard`/`monitoring`/`ares_events`/`ares_flight`/`llm/output`；`agent` 子包依赖 `ares_memory`。是 "api_impl↔agent runtime" 联动主装配点。
- **代码质量**: 装配顺序清晰、errgroup 基本正确。但 Arena 故障模拟退化、配置解析吞错、连接失败泄漏。
- **问题清单**:
  - 🔴 `adapters.go:190-294` `ArenaAdapter.Execute` 的 `PauseAgent`/`SlowAgent`/`MCPDisconnect`/`LLMFailure` 名义模拟，实际**全部调 `Orch.CancelAgent`**（硬取消）；`pause/slow` 仅写 map 标记不改变运行时行为（注释自承 "tracking-only"）。`ResilienceScore`（`:361-401`）把"取消成功"计为"韧性动作成功" → **评分与真实韧性无关，误导性指标**。
  - 🟡 `config.go:41-42` `LoadServiceConfig` 忽略 `yaml.Unmarshal` 错误 → 畸形 YAML 静默成全零配置，根因滞后。
  - 🟡 `service.go:104-133` `StartService` 自建 `MCPClient+Connect` **绕过 `MCPManager`/config-watcher** → 两套连接语义；第 N 个 server 失败时已连 0..N-1 个 client 从未 `Close()`（泄漏）。
  - 🟡 `service.go:301-305` `SetHTTPHandler` 在 `s.mu` 下写 `s.httpServer.Handler`，但 `http.Server` 请求处理时无锁读 `Handler` → 数据竞争（锁对真正竞争无保护）。
  - 🟡 `adapters.go:151-168` `mcpAdapter`/`llmAdapter` 字段在 `StartService` 中从未赋值 → `ArenaActionMCPDisconnect/LLMFailure` 的 `!=nil` 分支永假（死代码）。
  - 🟡 `agent/service.go:35-59` `CreateAgent` 依赖 `s.memoryMgr`，`NewService` 不校验非 nil → 传入 nil 会 `CreateSession` 空指针 panic。
  - 🟢 `agent/service.go:46-86` `Agent.Status` 恒 `StatusReady` 无迁移；`adapters.go:313` `ResilienceScore` 每次成功 `resurrectionTotal++`（语义错，只是成功计数）。
- **改进建议**: Arena 动作改真注入（经已评审 chaos 或代理层）或明确定义为 cancel 别名并修正 `ResilienceScore`；`LoadServiceConfig` 必须查 `yaml.Unmarshal` 错误；`StartService` 部分失败对已成功 client `Close` 或统一走 `MCPManager`；删 `SetHTTPHandler` mutex 或改不可变 handler；给 `mcpAdapter/llmAdapter` 赋值或删死分支。
- **测试覆盖**: ok（含 agent 子包）。纯函数/agent 管理较好；`StartService` 端到端、Arena 各动作语义、LoadServiceConfig 错误路径、SetHTTPHandler 竞态未覆盖，装配与 Arena 语义低。
- **联动风险**: `StartService` 经 `dashboard.Orchestrator` 驱动 runtime——Arena 硬取消会真实杀掉 agent，`ResilienceScore` 被监控/看板当韧性指标会形成"假绿"；`MCPDisconnect` 若未来依赖 `ares_mcp` 连接状态，与 4.9 共享 client 🔴 叠加可能误杀同 server 其他工具。建议 Arena 复用已评审 chaos 而非自造取消语义。

### 4.12 监控/可观测 — `internal/monitoring`
- **规模**: 大 · ~5899 行 / 33 文件 / 24 测试（子包 adapter/dag/data/eventutil/tabs）
- **职责**: 事件总线订阅 → 聚合 Console 快照（DAG/Cost/Agents/Traces/Tabs）、HTTP/WS 推送、DAG 交互引擎、pruner、进化/智能视图。
- **依赖与耦合**: 被 `api/console_client.go`、`cmd/ares/serve.go`、`cmd/monitor-live`、`cmd/ares/demo.go`、`api_impl/service.go` 广泛引用；经 `ares_events`/`ares_runtime` 接收 agent/tool/LLM/**chaos/arena** 事件；`TraceLinker`/`dag.Engine` 是 "monitoring↔chaos/arena traces" 核心。
- **代码质量**: `dag.Engine`/`MainPage`/`TraceLinker` 锁用正确（读写锁+深拷贝）；`Pruner` 有 TTL/上限。问题集中在"数据已采集但未通过 HTTP 暴露"与"arena 动作无法定位目标"。
- **问题清单**:
  - 🔴 `publisher.go:325-338` `HandleTrace`（`/api/traces/{id}`）**硬编码桩**直接返 `spans: []any{}`，注释 "Trace data is not yet wired; return placeholder"。但 `TraceLinker` 确在构建 span，且 `MonitorPlugin.Traces` 已正确委托 `mainPage.Linker().GetTrace`，serve 路径也接入了 linker → **真实 trace 数据存在却从 HTTP 不可达，看板 trace 视图永远空**。
  - 🔴 `plugin.go:372-385` `MonitorPlugin.ExecuteAction(ctx, actionID)` 调 `p.interEngine.ExecuteAction(ctx, "", actionID)` —— **nodeID 传空串**。ConsoleAPI 的 `ExecuteAction` 签名只有 `actionID` 无目标节点 → kill/resume/retry **无法定位具体 agent/node**；HTTP 层 `executeNodeAction` 能从 URL 取 `agentID` 正确传 engine，但任何走 `ConsoleAPI` 抽象的调用方因空 ID 失效。
  - 🟡 `data/trace_linker.go:192-216` `handleToolCallStarted/Completed` 用 `toolCloseKey(agentID, toolName)` 作 open-span key；同 agent **并发多次同名工具**时第二次 Started 覆盖 key，第一次 Completed 关错/第二次 Completed 找不到 key → span 错配或泄漏（arena/chaos 高频同名工具易触发）。
  - 🟡 `plugin.go:426-442` `MCPToolCalls` 用 `p.mcp.ListTools(...)` 返**已注册工具列表**伪装成"调用历史"，`AgentID` 由参数硬填无真实调用数据。
  - 🟡 `collector.go:31-44` `NewCollector` 在 `bus==nil||mainPage==nil` 时**返 nil 而非 error** → 调用方未判空会 panic，与包内其他 `New*` 返 error 约定不一致。
  - 🟡 `data/trace_linker.go:111-154` agent 重启（stop→start）复用旧节点覆盖 `openSpans["agent:"+agentID]` → 上一个未关 start span 泄漏。
  - 🟢 `main_page.go:186-235` `Snapshot()` 不填 `Traces/Evolution/MCP` 字段；`publisher.go:119-122` `WithCostAlertThreshold` 空操作，`plugin.go:331-334` `CostAlerts` 返 `ErrNotImplemented`。
- **改进建议**: `HandleTrace` 直接委托 `mainPage.Linker().GetTrace(traceID)` 删桩；`ConsoleAPI.ExecuteAction` 增 `nodeID/agentID` 参数透传；`TraceLinker` open-span key 加调用序号/spanID 支持同名工具并发并清理重启泄漏；`MCPToolCalls` 改名或接真实调用历史；`NewCollector` 返 error。
- **测试覆盖**: 全 6 子包 ok。DAG 引擎/快照/linker 单位逻辑好；`HandleTrace` 桩（测试仅断言返回空，反而固化 bug）、`ExecuteAction` 空 nodeID、`MCPToolCalls` 语义、Pruner 实际裁剪、HTTP 路由并发未覆盖。
- **联动风险**: `TraceLinker`/`dag.Engine` 消费同一事件总线——已评审 chaos/arena 若变事件类型需确认 monitoring 的 `Record`/`HandleEvent` switch 覆盖，否则 trace/DAG 盲区。本次 `HandleTrace` 桩与 `ExecuteAction` 空 ID 直接削弱对 chaos/arena 的**可观测性与可控性**，与联动目标相悖，建议优先修。

### 4.13 存储层 — `internal/storage`
- **规模**: 大 · ~31683 行 / 93 文件 / 36 测试（含 `postgres/{repositories,services,embedding,query,adapters}` 等子包）
- **职责**: PostgreSQL + pgvector 持久化主干——连接池、仓储、迁移（含 RLS）、embedding 队列、熔断、写缓冲、查询缓存、租户隔离守卫。
- **依赖与耦合**: 被约 26 个内部包反向依赖（agents/leader、ares_bootstrap、ares_eval、ares_experience、ares_memory、tools/resources/*、api/memory、cmd/*）。名副其实的持久化骨架。
- **代码质量**: 整体高——SQL 参数化、事务内 embedding 入队保证原子性、熔断/写缓冲防泄漏、迁移分 core/storage 两段避免 schema 漂移。但两套表名校验路径 + 租户上下文需手动设置。
- **问题清单**:
  - 🟡 `postgres/vector.go:64,75,128,157,208,251` `VectorSearcher` 用 `sanitizeSQLTable`(仅正则)+`safeFormatTable`(返**未加引号**表名) 拼接 SQL，而 `base_repository.go:59` 的 `validateTable` 走白名单+`quoteIdentifier`。两条路径不一致，`vector.go` 绕过白名单；`safeFormatTable`(security.go:164) 名不副实（返裸名），含连字符的合法标识符会未引用注入致 PG 报错。
  - 🟡 `migrate_storage.go:43,95,140,177,218,258,338` RLS 依赖 `current_setting('app.tenant_id', true)`；未设时值空 → `tenant_id=''` 永不成立 → 策略返 **0 行（静默空结果）**。仅 `distilled_memory_repository.go:57`/`retrieval_service.go:300` 设上下文，其余仓储从不设且其表不在 RLS 列表 → 租户隔离靠自觉，易回归为静默遗漏/串扰。
  - 🟡 **错误模型错配（联动）**：storage 全量 `internal/errors.Wrap(err,"msg")`（如 `session.go:57`）产无 `ErrorCode` 普通包装；而 `core/errors` 重试/DLQ 以 `*AppError.Code` 为键 → storage 产生的 DB/向量失败**永不**匹配 core 错误策略，该框架对最大错误源形同虚设。
  - 🟡 `query/cache.go:158` `Clear()` 调 `redis.Keys(ctx,"query_cache:*")` → 生产 Redis 全键空间扫描，大键量阻塞/超时，应 `SCAN`。
  - 🟢 `write_buffer.go:141-143` `processLoop` 遇 `item==nil` 直 `return nil` 会**永久终止** flush goroutine（当前 `Write` 已拒 nil，仅潜在）；`query/memory_cache.go:105` `MemoryQueryCache` 无容量上限仅按 TTL 清理；`security.go:138` `containsSQLInjectionPatterns` 子串匹配误伤合法 tenantID（而 `tenant_guard.go:35` 用它校验 tenantID）。
- **改进建议**: `vector.go` 表名校验统一收敛到 `validateTable`（白名单+引用）删旁路；`Pool`/仓储基类提供强制"带租户执行"入口，未设上下文 fail-loud；明确唯一错误契约（storage 也产 `*AppError` 或 core 用 `errors.As` 兼容）；`Clear()` 改 `SCAN`。
- **测试覆盖**: 全部子包 ok。覆盖迁移/超时/熔断/向量/仓储/查询缓存；**写缓冲 requeueItems 满信道丢弃、embedding Reconcile 孤儿、WriteBuffer.Stop 与并发 Write 竞态、QueryCache.Clear 的 Redis 分支**未覆盖。
- **联动风险**: 作为持久化骨架，"未设租户上下文→静默空结果"与"错误不带 ErrorCode"两点直接传导到 ares_bootstrap/ares_eval/ares_memory 等——bootstrap 装配的存储失败不被统一错误策略捕获，多租户查询静默无数据，是回归高风险隐性契约。

### 4.14 核心类型 — `internal/core`
- **规模**: 大 · ~3376 行 / 16 文件 / 4 测试（含 `errors` 与 `models` 子包）
- **职责**: 全仓库共享领域模型（`UserProfile`/`Session`/`Task`/`RecommendResult` 等）与结构化错误类型、错误策略注册表。
- **依赖与耦合**: 被 **82 个包**反向依赖（`api/*`、`cmd/ares`、`cmd/monitor-live`、`agents/*`、`ares_bootstrap`、`workflow/engine`、`ares_memory/*` 等）。`core/errors` 以 `apperrors` 别名导入 `internal/errors` 并重导出哨兵错误（设计正确）。
- **代码质量**: 领域模型小而清晰带校验；错误策略注册表 `RWMutex` 保护、支持配置加载与路径穿越防护。核心问题是两套不兼容错误模型。
- **问题清单**:
  - 🟡 **两套错误模型并存**：`core/errors` 定义 `AppError{Code *ErrorCode}` 与 `Wrap(err,*ErrorCode)`；`internal/errors` 定义 `Wrap(err,string)`/`Wrapf`/`New`。哨兵共享身份但 `*AppError` 类型彼此不兼容：`errors.As(err,&internalerrors.AppError{})` 无法匹配 `coreerrors.AppError{}`。
  - 🟡 `core/errors/code.go:111`(`FormatError`) 与 `handler.go:127,128`(`IsRetryable`) 用**直接类型断言** `err.(*AppError)` 而非 `errors.As` → 任何经 `fmt.Errorf("%w",appErr)` 或 `internal/errors.Wrap` 包装的 AppError 无法识别 → 重试/DLQ 被悄悄跳过。
  - 🟡 `strategy_config.go:81` `init()` 改包级可变 `globalRegistry` → 多测试并行难隔离；`LoadStrategiesFromConfig` 在 `allowedDir==""`（默认）时**完全跳过**路径穿越校验，默认允许任意路径加载策略。
  - 🟢 `models/types.go:131` `DefaultSessionTTL`/`DefaultTaskTTL` 是包级可变 `var`（非 const/func），可被意外修改；`user.go:40-43` `Validate()` 在 `p==nil` 与 `UserID==""` 都返 `ErrInvalidUserID`（语义含混）；`code.go:89` `ParseAgentStatus` 错误返空串。
- **改进建议**: 收敛为单一错误模型（core 复用 `internal/errors.AppError` 或反之删重复）；`FormatError`/`IsRetryable`/`ShouldRetry` 改 `errors.As`；`strategy_config` 全局注册表改显式注入；`DefaultSessionTTL` 改 `func()`/`const`。
- **测试覆盖**: 全部 ok（errors 17.7s、models ok）。`WithContext` 并发写、`Handler.RetryWithBackoff` 退避上限、`Export/LoadStrategiesToConfig` 往返一致性未充分覆盖。
- **联动风险**: 因 `core/errors.AppError` 与 `internal/errors` 不兼容、且 storage 走 `internal/errors.Wrap`（无 Code），已评审的 `agents/*`/`ares_bootstrap` 若用 `core/errors.IsRetryable` 判存储/LLM 失败将得不到预期重试 → **跨模块错误契约系统性断点**。

### 4.15 CLI 命令 — `cmd/`
- **规模**: 中 · ~7158 行 / 43 文件 / 3 测试
- **职责**: 统一 CLI 入口与运维命令——`cmd/ares`(cobra 根：demo/serve/arena/db/flight/mcp)、`monitor-demo`、`monitor-live`(真实接线)、`flight`(回放)、`check_rls`/`migrate_db`/`setup_test_db`/`create_distilled_table`/`embedding-mcp`/`mcp-null`/`arena` 等。
- **依赖与耦合**: 几乎把所有模块焊一起——`ares_bootstrap`/`ares_runtime`/`monitoring`/`agents/base`/`llm/output`/`tools/resources/core`/`storage`/`ares_events`。`monitor-live` 是验证全链路接线（LLM 回退链、MCP 工具桥接）的总装点。
- **代码质量**: 各 `main` 关注点基本清晰、信号优雅退出。问题在重复代码、CLI 框架不统一、工厂资源泄漏、接线几乎零测试。
- **问题清单**:
  - 🟡 `monitor-live/main.go:134-143` `subFactory` 每次 agent 重启调 `createAgents(cfg,...)` 重建**整套** leader+sub（且忽略返回 error），而非重建单个 sub → 重启重复创建 LLM/MCP/runtime 注册，资源泄漏+可能重复 agent。
  - 🟡 **重复代码**：`cmd/ares/demo.go:87-210` 与 `cmd/monitor-demo/main.go:69-196` 的 `simulateWorkload` 几乎逐行相同（~120 行），应抽共享包。
  - 🟡 **CLI 框架不统一**：`cmd/ares` 用 cobra；`cmd/flight`/`mcp-null`/`check_rls`/`embedding-mcp` 用原生 `flag`/`os.Args`。同一仓库两套范式。
  - 🟡 `flight/main.go:59-73`(`separateArgs`) 在 `flag.Parse` 前手工拆分 flags/位置参数，脆弱（flag 值以 `-` 开头即错位）。
  - 🟡 `monitor-demo/main.go:61` 与 `ares/demo.go:80` 出错路径 `log.Fatal` 前多次 `cancel()`（冗余）；`monitor-demo` 的 `defer cancel()` 位于 `server.Run` 阻塞后语义等同运行后才取消。
  - 🟢 `monitor-live/main.go:229-285` `createLLMAdapterWithFallback` 回退 ollama 时 `Timeout:120` 裸整数传入 `output.Config.Timeout`（若字段为 `time.Duration` 则单位错=120ns），应 `120*time.Second`；**仅 3 测试文件**，所有接线/bootstrap/模拟负载/回放解析实质未测。
- **改进建议**: 修正 `subFactory` 按 `subAgent.ID()` 重建单个 sub 并检查错误；抽 `simulateWorkload` 到共享包；统一 CLI 迁 flight 等到 cobra；为 monitor-live/demo/flight 增集成冒烟测试（fake bus/store）；明确 `output.Config.Timeout` 单位。
- **测试覆盖**: `cmd/...` 全部 ok（ares ok、flight ok；其余 9 子命令 `[no test files]`）。覆盖极薄：核心接线/bootstrap/模拟负载/回放解析实质未测，是回归风险最高的"总装"代码。
- **联动风险**: `cmd/monitor-live` 是 `ares_bootstrap` 总装验证点。其核心缺陷（`subFactory` 重建全量 agents、`createLLMAdapterWithFallback` 超时单位疑似错、租户上下文未在 bootstrap 层统一注入）会放大 §2.1/§2.2 隐性契约问题——上游 storage 的 RLS/错误模型一旦回归，CLI 这条唯一端到端路径既无测试守卫又静默失败，是最高优先级回归面。

---

## 5. 与已评审核心模块的联动结论

已评审核心（genome / workflow DAG / 记忆蒸馏 / 混沌 / 工具调度器）**效果是否被削弱**，取决于本次未评审模块是否守住契约：

| 已评审核心 | 被本次发现削弱的点 |
|-----------|-------------------|
| `ares_evolution/genome`（自进化） | bootstrap 默认不装配 + DreamCycle nil + 丢弃 FeedbackService → **默认路径是 dead code**；`ares_experience` bandit 环未闭环 → 负反馈不进排序/评分；`core`/`storage` 错误模型分裂 → 重试/评估信号弱化 |
| `workflow/engine`（动态 DAG） | 未被直接冲击；但与 `monitoring` 的 DAG 视图/arena 动作透传断裂（ExecuteAction 空 nodeID）→ 看板不可控 |
| `ares_memory/distillation`（记忆蒸馏） | 与 `ares_experience` 存在**两条并行经验生产者**口径冲突；leader `finalizeMemory` 蒸馏 errgroup 生命周期竞态 |
| 混沌工程（arena/chaos） | `monitoring.HandleTrace` 桩 + `ArenaAdapter` 退化硬取消 + `ares_quant` 空壳 chaos → **混沌/韧性在 observability 与量化路径上是假的** |
| 工具调度器 | `ares_mcp.MCPTool.Close` 共享 client + `api_impl` 绕过 Manager → MCP 工具可能掉线/泄漏 |

**一句话总评**：代码工程水准整体不低（storage 骨架、flight/monitoring 的锁设计、mcp 并发处理都到位），但**架构级契约断裂**（错误模型、租户上下文、混沌碎片化、MCP 连接所有权）和**几个会崩/会 race 的 P0** 是当前最大风险；且所有包测试"绿"掩盖了并发与集成的盲区。建议先开 `-race` + 按 §3 的 P0/P1 顺序修，再决定 `ares_quant` 的接线或移除。

---

*本报告为只读评审，未修改任何源码。覆盖 15 个未评审模块（含 `internal/agents/coordinator`→`leader` 路径勘误）。*
