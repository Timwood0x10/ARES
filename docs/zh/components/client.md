# Client 设计文档

## 1. 概述

`api/client` 包是一个库式嵌入接口，允许 Go 应用程序将 GoAgent 作为库嵌入使用，而无需将其作为独立服务运行。它将 5 个核心服务（Agent、Memory、Retrieval、LLM、Workflow）封装在统一的 `Client` 结构体之后。

### 核心设计原则

- **统一入口**: 通过一个 Client 结构体访问所有核心服务
- **按需配置**: 每个服务均为可选，未配置的服务返回明确的错误信息
- **线程安全**: 所有公共方法均支持并发访问
- **优雅关闭**: 提供幂等的 Close 方法，支持优雅关闭

## 2. 客户端（Client）

### 2.1 Client 结构体

```go
type Client struct {
    config      *Config
    agentSvc    core.AgentService
    memorySvc   core.MemoryService
    retrievalSvc core.RetrievalService
    llmSvc      core.LLMService
    workflowSvc core.WorkflowService
    mu          sync.RWMutex
    closed      bool
    pluginBus   *plugins.PluginBus
}
```

### 2.2 关键方法

| 方法 | 描述 |
|------|------|
| `NewClient(config)` | 创建客户端，使用 nil-safe 默认值（超时 30s，重试 3 次） |
| `Agent()` | 获取 Agent 服务，未配置时返回 `ErrAgentNotConfigured` |
| `Memory()` | 获取 Memory 服务，未配置时返回 `ErrMemoryNotConfigured` |
| `Retrieval()` | 获取 Retrieval 服务，未配置时返回 `ErrRetrievalNotConfigured` |
| `LLM()` | 获取 LLM 服务，未配置时返回 `ErrLLMNotConfigured` |
| `Workflow()` | 获取 Workflow 服务，未配置时返回 `ErrWorkflowNotConfigured` |
| `Runtime(config, eventStore)` | 创建 Runtime 实例 |
| `Health(ctx)` | 检查所有服务的健康状态 |
| `Config()` | 线程安全的配置深拷贝 |
| `Close(ctx)` | 优雅关闭（幂等） |
| `Ping(ctx)` | 轻量级连通性检查 |

### 2.3 服务访问器说明

每个服务访问器（`Agent()`、`Memory()` 等）在服务未配置时会返回对应的哨兵错误。这种设计使得客户端可以在单服务模式下轻量运行，同时保持统一的访问接口。

## 3. 配置（Config）

### 3.1 Config 结构体

```go
type Config struct {
    BaseConfig
    Agent     *core.AgentConfig
    Memory    *core.MemoryConfig
    Retrieval *core.RetrievalConfig
    LLM       *core.LLMConfig
    Workflow  *core.WorkflowConfig
}
```

`Config` 由 `BaseConfig` 和 5 个服务配置指针组成。所有服务配置均为可选，`nil` 表示该服务未配置。

### 3.2 BaseConfig

| 字段 | 类型 | 描述 | 默认值 |
|------|------|------|--------|
| `Timeout` | `time.Duration` | 请求超时时间 | 30s |
| `RetryCount` | `int` | 失败重试次数 | 3 |
| `RetryDelay` | `time.Duration` | 重试间隔 | 1s |
| `Plugins` | `[]PluginConfig` | 插件配置列表 | 空 |

## 4. 文件配置（File Configuration）

### 4.1 YAML 配置结构

配置支持通过 YAML 文件加载，文件结构如下：

```yaml
timeout: 30s
retry_count: 3
retry_delay: 1s
plugins:
  - name: example-plugin
    config:
      key: value

agent:
  enabled: true
  # ... Agent 特有配置

memory:
  enabled: true
  # ... Memory 特有配置

retrieval:
  enabled: false

llm:
  provider: openai
  api_key: ${GOAGENT_LLM_API_KEY}
  model: gpt-4

workflow:
  enabled: false
```

### 4.2 配置加载器

`ConfigLoader` 提供灵活的配置加载方式：

| 函数 | 描述 |
|------|------|
| `NewConfigLoader()` | 创建加载器，搜索默认路径 |
| `LoadConfigFile(path)` | 从指定路径加载配置文件 |
| `NewClientFromConfigPath(path)` | 从配置文件路径创建客户端 |
| `NewClientFromDefaultPath()` | 从默认路径加载并创建客户端 |
| `ToClientConfig()` | 将文件配置转换为 Client Config |

### 4.3 环境变量覆盖

支持通过环境变量覆盖配置值，例如 `GOAGENT_LLM_API_KEY` 可以覆盖 LLM 配置中的 API Key。

### 4.4 配置验证

配置加载时会执行以下验证：

| 验证项 | 规则 |
|--------|------|
| 超时时间 | 1s ~ 300s |
| 重试次数 | 0 ~ 10 |
| 提供商 | 必须在 [ollama, openrouter, openai, anthropic] 范围内 |

## 5. 简单客户端（SimpleClient）

### 5.1 概述

`SimpleClient` 是快速入门入口点，提供极简的使用方式——只需一行代码即可从配置文件路径创建客户端。

### 5.2 接口定义

```go
type SimpleClient struct {
    client *Client
}

func NewSimpleClient(configPath string) (*SimpleClient, error)
func (c *SimpleClient) Execute(ctx, query string) (string, error)
func (c *SimpleClient) ExecuteWithAgent(ctx, agentID, query string) (string, error)
func (c *SimpleClient) Chat(ctx, messages string) (string, error)
func (c *SimpleClient) Close(ctx) error
```

### 5.3 方法说明

| 方法 | 描述 |
|------|------|
| `NewSimpleClient(configPath)` | 从配置文件路径创建简单客户端 |
| `Execute(ctx, query)` | 使用默认 Agent 执行查询 |
| `ExecuteWithAgent(ctx, agentID, query)` | 使用指定 Agent 执行查询 |
| `Chat(ctx, messages)` | 发起对话 |
| `Close(ctx)` | 关闭客户端 |

## 6. 工作流客户端（WorkflowClient）

### 6.1 概述

`WorkflowClient` 提供基于 YAML 的工作流编排能力，支持从文件或字符串创建工作流并执行。

### 6.2 接口定义

```go
type WorkflowClient struct { ... }

func NewWorkflowClient(client *Client) *WorkflowClient
func (wc *WorkflowClient) Execute(ctx, workflow, input string) (map[string]interface{}, error)
func (wc *WorkflowClient) ExecuteFromFile(ctx, path, input string) (map[string]interface{}, error)
```

### 6.3 方法说明

| 方法 | 描述 |
|------|------|
| `NewWorkflowClient(client)` | 基于已有 Client 创建工作流客户端 |
| `Execute(ctx, workflow, input)` | 执行 YAML 字符串定义的工作流 |
| `ExecuteFromFile(ctx, path, input)` | 执行文件中定义的工作流 |

## 7. 健康检查（Health Check）

### 7.1 HealthReport

健康检查返回 `HealthReport`，包含每个服务的详细状态以及整体状态。

```go
type HealthReport struct {
    Agent     ServiceStatus
    Memory    ServiceStatus
    Retrieval ServiceStatus
    LLM       ServiceStatus
    Workflow  ServiceStatus
    OverallStatus ServiceStatus
}

type ServiceStatus struct {
    Healthy bool
    Latency time.Duration
    Error   string
}
```

### 7.2 健康检查策略

- `Health(ctx)`: 检查所有已配置服务的健康状态，汇总为整体报告
- `Ping(ctx)`: 轻量级连通性检查，仅验证客户端是否可用

## 8. 错误处理（Error Handling）

### 8.1 哨兵错误

客户端定义了 6 个哨兵错误（sentinel errors），用于处理配置无效和服务未配置的情况：

| 错误 | 描述 |
|------|------|
| `ErrInvalidConfig` | 配置无效 |
| `ErrAgentNotConfigured` | Agent 服务未配置 |
| `ErrMemoryNotConfigured` | Memory 服务未配置 |
| `ErrRetrievalNotConfigured` | Retrieval 服务未配置 |
| `ErrLLMNotConfigured` | LLM 服务未配置 |
| `ErrWorkflowNotConfigured` | Workflow 服务未配置 |

### 8.2 使用示例

```go
client, err := client.NewClient(cfg)
if err != nil {
    return fmt.Errorf("创建客户端失败: %w", err)
}

agentSvc, err := client.Agent()
if err != nil {
    // 处理 Agent 未配置的情况
    return fmt.Errorf("Agent 服务不可用: %w", err)
}
```

## 9. 使用示例

### 9.1 基础用法

```go
import "github.com/user/goagent/api/client"

// 从配置文件加载
cfg := &client.Config{
    LLM: &core.LLMConfig{
        Provider: "openai",
        APIKey:   os.Getenv("GOAGENT_LLM_API_KEY"),
        Model:    "gpt-4",
    },
}

c, err := client.NewClient(cfg)
if err != nil {
    log.Fatal(err)
}
defer c.Close(context.Background())

llmSvc, err := c.LLM()
if err != nil {
    log.Fatal(err)
}

resp, err := llmSvc.Chat(ctx, req)
```

### 9.2 简单客户端用法

```go
// 一行代码创建并使用
sc, err := client.NewSimpleClient("config.yaml")
if err != nil {
    log.Fatal(err)
}
defer sc.Close(ctx)

result, err := sc.Execute(ctx, "帮我总结今天的会议记录")
```

### 9.3 工作流客户端用法

```go
wc := client.NewWorkflowClient(c)

// 从 YAML 字符串执行
result, err := wc.Execute(ctx, `
name: example
steps:
  - name: step1
    agent: default
    prompt: "执行第一步"
`, input)
```