# ARES 能力—模块映射图

> 从"我想用某个能力"出发，一步定位到代码模块，无需先理解目录分层。

约定：路径相对仓库根。`★` = 该能力有独立 CLI 子命令。

---

## 地图

| 能力 | 入口模块 | 做什么 | CLI / SDK |
|---|---|---|---|
| **Agent 执行** | `internal/agents/base` | Run / Stream 单 Agent | `sdk.NewAgent` |
| **多 Agent 分发** | `internal/agents/sub`, `internal/taskfabric`, `internal/agentipc` | 基于能力的 Agent 注册、任务分发、Peer IPC、检查点恢复 | `rt.RegisterAgent` + `rt.Submit` |
| **运行时装配** | `internal/ares_bootstrap` | 依赖注入、配置加载、服务直连 | `ares serve` |
| ★ **策略进化 GA** | `internal/ares_evolution` | 种群进化、交叉变异、评分晋升、梦周期 | `ares evolution run/status` |
| ★ **运行时补丁引擎** | `internal/evolution` | 部署期对 DAG/调度器/恢复策略打热补丁 | `ares evolution deploy` |
| ★ **DAG 工作流** | `internal/workflow` | 有向无环图编排、条件分支、自动恢复 | `ares workflow run` |
| **记忆 & 蒸馏** | `internal/ares_memory` | 会话上下文、任务蒸馏、向量嵌入 | `sdk.WithDefaultMemory` |
| **事件存储** | `internal/ares_events` | 事件持久化、压缩、剪裁 | — |
| ★ **知识图谱** | `internal/knowledge` | 知识规划、编译、链接、检索、存储 | `ares knowledge build` |
| **LLM 客户端** | `internal/llm` | OpenAI / Ollama / Anthropic 适配 | `sdk.WithOpenAI` 等 |
| **工具系统** | `internal/tools` | 内置工具、格式化、规划器、资源管理 | `sdk.WithTools` |
| ★ **MCP 集成** | `internal/ares_mcp` | SSE / stdio 协议，连接任意 MCP 服务器 | `sdk.WithMCP` |
| ★ **Chaos Arena** | `internal/ares_arena` | 故障注入、压测、场景编排、生存测试 | `ares arena run/validate/…` |
| ★ **Flight Recorder** | `internal/ares_flight` | 任务录制与回放 | `ares flight inspect/replay` |
| **安全 & 鉴权** | `internal/ares_security` | 安全策略、AHP 协议 | — |
| **限流** | `internal/ares_ratelimit` | 请求速率限制 | — |
| **优雅关停** | `internal/ares_shutdown` | 多组件协调关停 | — |
| ★ **评估框架** | `internal/ares_eval` | 评测运行器、LLM 裁判、维度评分、对比、报告 | `ares bench` |
| **可观测性** | `internal/ares_observability`, `internal/monitoring` | Trace / Metric / Log | — |
| **回调注入** | `internal/ares_callbacks` | 回调桥接 | — |
| ★ **HTTP API 服务** | `cmd/ares/actions.go` | 对外 REST：`GET /api/tools`、`POST /api/tools/call`、`POST /api/tasks`、`POST /api/graphs` | `ares serve` |
| **SDK 入口** | `sdk/` | `sdk.MustNew` 一站式初始化 | `sdk.MustNew` |
| **量化交易** | `internal/ares_quant` | 做市、指标、组合管理、研究 | — |
| ★ **CLI 总入口** | `cmd/ares/` | 所有子命令的起点 | `ares …` |
| **插件系统** | `internal/plugins` | 复活等插件 | — |
| **发现注册** | `internal/discovery` | 提供者发现与注册 | — |

---

## 快速导航

### 最常见的两个困惑

**Q：`internal/ares_evolution` 和 `internal/evolution` 有什么区别？**

| 目录 | 职责 | 包名 |
|---|---|---|
| `internal/ares_evolution` | **策略进化 GA**——种群、交叉、变异、评分、晋升 | `evolution` |
| `internal/evolution` | **运行时补丁引擎**——部署期对 DAG/调度器/恢复策略打热补丁 | `coordinator`/`diff`/`patch`/`genome` |

**Q：`internal/ares_memory` 与 `api/core.Message` 有什么区别？**

| 路径 | 职责 |
|---|---|
| `internal/ares_memory` | 记忆主模块：会话上下文、蒸馏、向量 push |
| `api/core.Message` | 对外消息类型定义（LLM 层消息） |

### 定位三步法

1. 在上表找到你要的能力，记住 **入口模块** 路径
2. 若入口在 `internal/` → 具体实现在那里；若入口在 `sdk/`/`api/` → 对外接口定义在那里
3. 对外 HTTP 服务统一由 `ares serve` 暴露（`/api/tools`、`/api/tasks`、`/api/graphs`）

---

**代码快照**：`dev` 分支，对外接口统一为 `sdk` + `ares serve` 四端点（`/api/tools`、`/api/tasks`、`/api/graphs`）。