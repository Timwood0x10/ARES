# Examples

ARES SDK 示例集合。按复杂度从低到高排列。

## 快速开始

```bash
# 需要 Ollama (默认) 或设置 OPENAI_API_KEY
make quickstart
```

或直接运行：

```bash
go run examples/quickstart/main.go
```

---

## 示例列表

### 🟢 入门级（P0）

| 示例 | 文件 | 核心看点 | 行数 |
|---|---|---|---|
| **[quickstart](quickstart/main.go)** | 20 行 | 最简 Agent：创建 Runtime → Agent → Run | ≤20 |
| **[tool-calling](tool-calling/main.go)** | 60 行 | 多工具注册、ReAct 循环执行 | ≤60 |

### 🟡 进阶级（P1）

| 示例 | 文件 | 核心看点 | 行数 |
|---|---|---|---|
| **[dag-workflow](dag-workflow/main.go)** | 130 行 | `NewGraph` + `FuncNode` + `Edge` 条件分支 | ≤130 |
| **[multi-agent](multi-agent/main.go)** | 60 行 | `NewTeam` 领导/成员编排 | ≤60 |
| **[evolution-demo](evolution-demo/main.go)** | 96 行 | `Evolve()` 指令进化前后对比 | ≤96 |
| **[chaos-resilience](chaos-resilience/main.go)** | 114 行 | 真实文件系统容错 + 自愈 | ≤114 |
| **[human-in-loop](human-in-loop/main.go)** | 150 行 | `WithHumanInput` 人工审批工具调用 | ≤150 |
| **[mcp-integration](mcp-integration/main.go)** | 73 行 | `WithMCP` 连接 MCP 服务器 | ≤73 |

### 🔵 综合应用（P2）

| 示例 | 文件 | 核心看点 | 行数 |
|---|---|---|---|
| **[full-app](full-app/main.go)** | 240 行 | Web UI + Agent + Tools + Memory + Stats | ≤240 |

### 📦 其他示例（原有）

| 示例 | 说明 |
|---|---|
| [autonomous-evolution](autonomous-evolution/) | 自进化 Dream Cycle 完整演示 |
| [quant-trading](quant-trading/) | 量化交易多 Agent 系统 |
| [graph_demo](graph_demo/) | MutableDAG 图编排各种场景 |
| [knowledge-base](knowledge-base/) | 知识库 + 蒸馏 |
| [mcp-server](mcp-server/) | MCP 服务器端实现 |
| [mcp-dashboard](mcp-dashboard/) | MCP + Dashboard 监控 |
| [travel](travel/) | 旅行规划 DAG 工作流 |
| [end-to-end](end-to-end/) | 端到端工作流 |
| [tool-intelligence](tool-intelligence/) | 工具智能编排 |

---

## 一键运行所有示例

```bash
make examples
```

或单独运行：

```bash
go run examples/quickstart/main.go
go run examples/tool-calling/main.go
go run examples/dag-workflow/main.go
```
