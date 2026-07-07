
```shell
           _____  ______  _____ 
     /\   |  __ \|  ____|/ ____|
    /  \  | |__) | |__  | (___  
   / /\ \ |  _  /|  __|  \___ \ 
  / ____ \| | \ \| |____ ____) |
 /_/    \_\_|  \_\______|_____/ 

```

**ARES** — 智能体运行时与进化系统（Agent Runtime & Evolution System）。

用 Go 构建高韧性、自进化的 AI Agent。统一 SDK、DAG 工作流、混沌工程、MCP 支持。

## 快速开始

```go
package main

import "github.com/Timwood0x10/ares/sdk"

func main() {
    rt := sdk.MustNew(sdk.WithOllama("llama3.2"))
    defer rt.Close()

    agent := rt.NewAgent("assistant")
    result, _ := agent.Run(ctx, "Say hello")
    println(result.Output)
}
```

安装 CLI：

```bash
go install github.com/Timwood0x10/ares/cmd/ares@latest
ares doctor
ares run -c ares.yaml "什么是 Go？"
```

或直接运行示例：

```bash
git clone https://github.com/Timwood0x10/ares
cd ares
make quickstart        # 运行快速开始示例
make examples          # 构建全部 24 个示例
```

## 核心特性

| 特性 | 说明 |
|---|---|
| **统一 SDK** | 单一 `sdk.MustNew()` API，统一管理 LLM、工具、记忆、进化 |
| **自我进化** | 遗传算法自动优化提示词和策略 |
| **DAG 工作流** | 动态图编排，支持条件分支和自动恢复 |
| **混沌韧性** | 故障注入、自动切换、生存测试、自愈恢复 |
| **记忆系统** | 会话上下文、任务蒸馏、向量相似度检索 |
| **MCP 就绪** | 连接任意 MCP 服务器扩展工具和数据 |
| **多 Agent** | 领导/成员编排，支持自动故障切换 |
| **可观测性** | OpenTelemetry 追踪、结构化日志、Prometheus 指标 |

## CLI 命令

```bash
ares init        # 创建新项目脚手架（main.go + ares.yaml）
ares run         # 从配置文件运行 agent
ares bench       # 快速性能基准测试
ares doctor      # 诊断环境（LLM key、Ollama、Git）
ares version     # 显示版本
ares arena       # 混沌工程场景
ares flight      # 检查与回放任务记录
```

## SDK 用法

```go
rt := sdk.MustNew(
    sdk.WithOpenAI("gpt-4o-mini"),          // 或 WithOllama、WithAnthropic
    sdk.WithDefaultMemory(),                 // 开启会话记忆
    sdk.WithEvolution(),                     // 开启策略进化
    sdk.WithMCP(sdk.MCPConn{                 // 连接 MCP 服务器
        Name: "my-server", Command: "/path/to/server", Args: []string{"serve"},
    }),
)
defer rt.Close()

// 带工具和人工审批的 Agent
agent := rt.NewAgent("assistant",
    sdk.WithInstruction("你是一个助手。"),
    sdk.WithTools(calculatorTool, weatherTool),
    sdk.WithHumanInput(approveFn),
)
result, _ := agent.Run(ctx, "计算 15*23")

// 流式响应
ch, _ := agent.Stream(ctx, "讲个故事")
for chunk := range ch { fmt.Print(chunk.Content) }

// 多 Agent 团队
team := rt.NewTeam("project", leaderAgent, []*Agent{memberAgent})
teamResult, _ := team.Run(ctx, "调研并撰写报告")
```

完整示例见 [examples/README.md](examples/README.md)。

## 架构

```mermaid
graph TB
    User["用户 / CLI"] --> SDK

    subgraph sdk ["SDK 层 (sdk/)"]
        RT["Runtime<br/>MustNew / New"]
        A["Agent<br/>Run / Stream"]
        T["Team<br/>多 Agent"]
        CFG["配置<br/>YAML + Options"]
    end

    SDK --> LLM
    SDK --> Tools
    SDK --> Memory
    SDK --> Evo

    subgraph LLM ["LLM 提供商"]
        OAI["OpenAI"]
        OLL["Ollama"]
        ANTH["Anthropic"]
        OR["OpenRouter"]
    end

    subgraph Tools ["工具系统"]
        BT["内置工具<br/>计算器、搜索..."]
        MCP["MCP 服务器<br/>Stdio / SSE"]
        CT["自定义工具<br/>ToolFunc"]
    end

    subgraph Memory ["记忆"]
        SES["会话上下文"]
        DIST["任务蒸馏"]
        VEC["向量检索"]
    end

    subgraph Evo ["进化"]
        GA["遗传算法"]
        MUT["变异"]
        SCORE["评分"]
    end

    subgraph CLI ["CLI 命令"]
        INIT["ares init"]
        RUN["ares run"]
        BENCH["ares bench"]
        DOCTOR["ares doctor"]
    end

    subgraph EX ["示例 (24 个)"]
        QS["01 快速开始"]
        TC["02 工具调用"]
        DAG["03 DAG 工作流"]
        MA["04 多 Agent"]
        EVO["05 进化演示"]
        CHAOS["06 混沌测试"]
        HIL["07 人工审批"]
    end

    style sdk fill:#1e3a5f,stroke:#3b82f6,color:#fff
    style LLM fill:#1a2332,stroke:#64748b
    style Tools fill:#1a2332,stroke:#64748b
    style Memory fill:#1a2332,stroke:#64748b
    style Evo fill:#1a2332,stroke:#64748b
    style CLI fill:#2d1b69,stroke:#8b5cf6,color:#fff
    style EX fill:#1a3a2a,stroke:#22c55e
```

## 评估框架

5 个场景直接检验 ARES 核心能力：

```bash
go run examples/eval/main.go
```

| 场景 | 评估内容 |
|---|---|
| `basic-chat` | 基础对话正确性 |
| `tool-calling` | 工具调用准确性 |
| `multi-agent` | 团队协作能力 |
| `resilience` | 错误恢复能力 |
| `evolution` | 进化前后效果对比 |

## 混沌示例

9 种故障模式全覆盖：

```bash
go run examples/06-chaos-resilience/main.go
```

文件系统故障 / 工具超时 / 不可靠服务 / 优雅降级 / 网络故障 / MCP 断连 / LLM 故障 / 内存损坏

## 文档

| 语言 | 文档 |
|---|---|
| English | [Architecture](docs/articles/en/architecture-overview-deep-dive.md), [Evolution](docs/articles/en/autonomous-evolution-deep-dive.md), [MCP](docs/articles/en/mcp-integration-deep-dive.md) |
| 中文 | [架构](docs/articles/zh/architecture-overview-deep-dive.md), [进化](docs/articles/zh/autonomous-evolution-deep-dive.md), [MCP](docs/articles/zh/mcp-integration-deep-dive.md) |

## 项目结构

```
├── sdk/           # 统一 SDK（package sdk）
├── cmd/ares/      # CLI 入口
├── evaluation/    # 评估框架
├── examples/      # 24 个可运行示例
├── docs/          # 文档和文章
├── api/           # 公开 API 接口
└── internal/      # 内部实现
```

## 许可证

Apache 2.0
