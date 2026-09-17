# ARES 能力—模块映射图

> 从"我想用某个能力"出发，一步定位到代码模块，无需先理解目录分层。
> 按代码核实更新：2026-09-13（dev 分支，v0.3.1）。

约定：路径相对仓库根。`★` = 该能力有独立 CLI 子命令。

---

## 地图

| 能力 | 入口模块 | 做什么 | CLI / SDK |
|---|---|---|---|
| **Agent 执行** | `internal/agents/base` | Run / Stream 单 Agent | `sdk.NewAgent` |
| **共享 L2 执行内核** | `internal/agentruntime` | 会话注册表 + 计划投影编译 + 路由认知 + 提交器（serve 与 SDK 共用，v0.3.1 起唯一执行引擎） | — |
| **多 Agent 分发** | `internal/agents/sub`, `internal/fabric/task`, `internal/agentipc` | 基于能力的任务织物（状态机/租约/epoch/checkpoint）、Peer IPC | `rt.Submit` |
| **运行时装配** | `internal/ares_bootstrap` | 组件图装配、依赖注入、演化/记忆/知识接线 | `ares serve` |
| **内核调度器** | `internal/kernel` | drain 循环、能力评分、租约心跳、优先级抢占、恢复提名 | — |
| **Agent 生命周期** | `internal/fabric/agent` | Spawn/Kill/Suspend、会话注册表、L2 图 | — |
| **崩溃恢复** | `internal/aresrecovery` | 租约过期→重新入队→替代 agent→checkpoint 续跑；跨重启任务回折（`RestoreFromStore`，v0.3.1） | — |
| ★ **策略进化 GA** | `internal/runtime/ares_evolution` | 种群进化、门控晋升（shadow A/B、回归门、人工审批）、生命周期管理 | `ares evolution run/status` |
| ★ **运行时补丁引擎** | `internal/runtime/evolution` | Genome/Diff/Patch：对 DAG/恢复策略/知识参数打热补丁 | `ares evolution run`（底层） |
| **DAG 图引擎** | `internal/fabric/task/workflow/engine` | MutableDAG、DAG/恢复补丁执行器、HITL 插件（HITL 未接生产） | — |
| **计划轮次循环** | `internal/fabric/task` | PlanLoop：计划 DAG 重执行 N 轮 + UntilCondition + Replan 钩子（v0.3.1） | `create_plan loop` 参数 |
| **记忆 & 蒸馏** | `internal/runtime/memory` | 会话上下文、蒸馏、向量嵌入、经验/反馈 | `sdk.WithDefaultMemory` |
| **Serve 记忆增强** | `cmd/ares/memory_enricher.go` | serve 提交路径的跨轮会话记忆（PromptEnricher，v0.3.1） | — |
| **事件存储** | `internal/ares_events` | 事件持久化（内存/PG）、压缩、剪裁、保留清理 | — |
| ★ **知识系统** | `internal/knowledge` | 知识规划、编译、链接、检索、存储（AKF） | `ares knowledge build` |
| **LLM 客户端** | `internal/llm` | OpenAI / OpenRouter / Ollama / Anthropic 适配 + failover | `sdk.WithOpenAI` 等 |
| **工具系统** | `internal/tools`, `internal/apitools` | 内置工具（web_search/calculator/regex/json/file 等）、注册表 | `sdk.WithTools` |
| ★ **MCP 集成** | `internal/mcpclient` | MCP 服务器发现与连接 | `sdk.WithMCP` |
| ★ **Chaos Arena** | `internal/runtime/arena` | 故障注入（Kill/Partition/Pause/Slow/LLMFailure 等）、生存/场景模式 | `ares arena run/validate/…` |
| ★ **Flight Recorder** | `internal/runtime/observability/flight` | 任务录制与回放 | `ares flight inspect/replay` |
| **安全 & 鉴权** | `internal/ares_security` | JWT/API key/introspect token、审计 | — |
| **限流** | `internal/ares_ratelimit` | 请求速率限制 | — |
| **优雅关停** | `internal/ares_shutdown` | 多组件协调关停 | — |
| ★ **评估框架** | `internal/runtime/eval` | 评测运行器、维度评分、对比报告 | `ares bench` |
| **可观测性** | `internal/runtime/observability`, `internal/introspect` | OTel trace / Prometheus / introspect 只读面板 | — |
| **回调注入** | `internal/ares_callbacks` | 回调桥接 | — |
| **发现注册** | `internal/discovery` | 提供者发现与注册 | — |
| ★ **HTTP API 服务** | `cmd/ares/agent.go` + `agent_routes_*.go` | REST：`GET /api/tools`、`POST /api/tools/call`、`POST /api/tasks`、`POST /api/graphs`、`/api/v1/introspect/*` | `ares serve` |
| ★ **运行状态** | `cmd/ares/status.go` | 一览运行时健康/配置/能力资产 | `ares status` |
| **SDK 入口** | `sdk/` | `sdk.New` / `sdk.MustNew` 一站式初始化 | `sdk.MustNew` |
| ★ **CLI 总入口** | `cmd/ares/` | 所有子命令的起点 | `ares …` |

> 已删除的能力（不要在代码里找了）：Leader-Sub 架构（v0.3.x 移除）、量化交易模块（`ares_quant` 已不存在）、独立插件系统包（`internal/plugins` 已不存在，插件面收敛到 `internal/runtime` 的 PluginBus）、`api/*` 公共用户面包（v0.3.0 起转发层，唯一公共 API 是 `sdk`）。

---

## 快速导航

### 最常见的两个困惑

**Q：`internal/runtime/ares_evolution` 和 `internal/runtime/evolution` 有什么区别？**

| 目录 | 职责 |
|---|---|
| `internal/runtime/ares_evolution` | **GA 种群进化**——种群、交叉变异、评分、门控晋升、生命周期、梦周期 |
| `internal/runtime/evolution` | **运行时补丁引擎**——Genome/Diff/Patch，部署期对 DAG/恢复/知识参数打热补丁 |

**Q：serve 和 SDK 是两套执行引擎吗？**

不是。v0.3.1 起（"convergence"）两者共用 `internal/agentruntime.NewExecution` 构建的同一个 L2 执行内核（会话注册表 + 计划投影 + 路由认知 + 提交器），SDK 不再有独立的内置 ReAct 引擎。装配点由 `sdk/arch_test.go` 锁定（仅 `sdk/l2.go`、`cmd/ares/agent_kernel.go`、`cmd/ares/peer_assembly.go` 三处可构造）。

### 定位三步法

1. 在上表找到你要的能力，记住 **入口模块** 路径
2. 若入口在 `internal/` → 具体实现在那里；若入口在 `sdk/` → 对外接口定义在那里
3. 对外 HTTP 服务统一由 `ares serve` 暴露（`/api/tools`、`/api/tasks`、`/api/graphs`、`/api/v1/introspect/*`）
