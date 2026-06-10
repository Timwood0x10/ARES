# 集成指南

**更新日期**: 2026-03-23

## 简介

本文档介绍如何将 GoAgent 框架集成到现有项目中，支持两种集成模式：

1. **库模式**: 直接使用 GoAgent 作为依赖库
2. **服务模式**: 将 GoAgent 作为独立服务运行

## 集成方式

### 库模式（推荐用于 Go 项目）

适合场景：
- 现有项目是 Go 语言
- 需要深度集成和定制
- 需要高性能和低延迟

**代码位置**: `api/service/`

#### 步骤 1: 添加依赖

在你的项目中添加 GoAgent 作为依赖：

```bash
go get github.com/yourusername/goagent@latest
```

或使用 go.mod：

```go
require github.com/yourusername/goagent v1.0.0
```

#### 步骤 2: 初始化 Agent

创建一个 Leader Agent：

```go
package main

import (
    "context"
    "github.com/yourusername/goagent/api/service"
    "github.com/yourusername/goagent/internal/config"
)

func main() {
    // 加载配置
    cfg, err := config.Load("config/agent.yaml")
    if err != nil {
        panic(err)
    }

    // 创建服务
    agentService := service.NewAgentService(cfg)
    
    // 启动 Agent
    ctx := context.Background()
    if err := agentService.Start(ctx); err != nil {
        panic(err)
    }
    
    // 处理请求
    response, err := agentService.Process(ctx, "你的问题")
    if err != nil {
        panic(err)
    }
    
    fmt.Println(response)
}
```

**代码位置**: `api/service/agent_service.go:30-50`

#### 步骤 3: 配置文件

创建 `config/agent.yaml`：

```yaml
llm:
  provider: "ollama"
  base_url: "http://localhost:11434"
  model: "llama3.2"
  timeout: 60

agents:
  leader:
    id: "my-agent"
    max_steps: 5
    max_parallel_tasks: 3

  sub:
    - id: "agent-tool"
      type: "tool"
      triggers: ["tool", "工具"]

storage:
  enabled: false  # 如果不需要持久化，可以禁用
```

**代码位置**: `internal/config/config.go:20-50`

### 服务模式（推荐用于多语言项目）

适合场景：
- 现有项目不是 Go 语言
- 需要 REST API 集成
- 需要分布式部署

#### 步骤 1: 启动 GoAgent 服务

```bash
# 克隆项目
git clone https://github.com/yourusername/goagent.git
cd goagent

# 配置服务
cp config/server.example.yaml config/server.yaml

# 启动服务
go run cmd/server/main.go
```

服务将在 `http://localhost:8080` 启动。

**代码位置**: `cmd/server/main.go:50-100`

#### 步骤 2: 通过 API 调用

使用 REST API 与 GoAgent 交互：

```bash
# 创建会话
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "user123",
    "agent_config": {
      "type": "travel"
    }
  }'

# 发送消息
curl -X POST http://localhost:8080/api/v1/sessions/{session_id}/messages \
  -H "Content-Type: application/json" \
  -d '{
    "content": "帮我规划一次旅行"
  }'
```

**代码位置**: `api/service/handlers.go:80-120`

## 最小集成示例

### 示例 1: 简单问答 Agent

创建一个简单的问答 Agent：

```go
package main

import (
    "context"
    "fmt"
    "github.com/yourusername/goagent/api/service"
)

func main() {
    // 使用默认配置
    cfg := &service.Config{
        LLM: service.LLMConfig{
            Provider: "ollama",
            Model:    "llama3.2",
        },
    }

    // 创建 Agent 服务
    agent := service.NewAgentService(cfg)
    
    // 启动
    ctx := context.Background()
    agent.Start(ctx)
    
    // 处理查询
    result, err := agent.Process(ctx, "什么是 RAG？")
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    
    fmt.Printf("Answer: %s\n", result)
}
```

**代码位置**: `examples/simple/main.go:1-50`

### 示例 2: 带 Tools 的 Agent

添加工具支持：

```go
cfg := &service.Config{
    LLM: service.LLMConfig{
        Provider: "ollama",
        Model:    "llama3.2",
    },
    Tools: service.ToolsConfig{
        Enabled: true,
        Tools: []service.ToolConfig{
            {
                Name: "calculator",
                Type: "builtin",
            },
            {
                Name: "datetime",
                Type: "builtin",
            },
        },
    },
}

agent := service.NewAgentService(cfg)
```

**代码位置**: `internal/tools/resources/core/agent_tools.go:100-150`

### 示例 3: 带 Memory 的 Agent

启用记忆系统：

```go
cfg := &service.Config{
    LLM: service.LLMConfig{
        Provider: "ollama",
        Model:    "llama3.2",
    },
    Memory: service.MemoryConfig{
        Enabled: true,
        MaxHistory: 10,
    },
    Storage: service.StorageConfig{
        Enabled: true,
        Type:     "postgres",
        Host:     "localhost",
        Port:     5433,
        User:     "postgres",
        Password: "postgres",
        Database: "goagent",
    },
}

agent := service.NewAgentService(cfg)
```

**代码位置**: `internal/memory/production_manager.go:50-100`

## 配置说明

### 基础配置

```yaml
# config/agent.yaml
llm:
  provider: "ollama"           # LLM 提供商
  base_url: "http://localhost:11434"
  model: "llama3.2"
  timeout: 60
  max_tokens: 2048

agents:
  leader:
    id: "agent-id"
    max_steps: 10
    max_parallel_tasks: 4
```

**代码位置**: `internal/config/config.go:20-50`

### 工具配置

```yaml
tools:
  enabled: true
  
  # 内置工具
  builtin:
    - calculator
    - datetime
    - file_read
    - http_request
  
  # 自定义工具
  custom:
    - name: "my_tool"
      path: "/path/to/tool"
      type: "executable"
```

**代码位置**: `internal/tools/resources/core/agent_tools.go:80-120`

### 存储配置

```yaml
storage:
  enabled: true
  type: "postgres"
  
  # PostgreSQL 配置
  postgres:
    host: "localhost"
    port: 5433
    user: "postgres"
    password: "postgres"
    database: "goagent"
  
  # pgvector 配置
  pgvector:
    enabled: true
    dimension: 1024
```

**代码位置**: `internal/storage/postgres/pool.go:35-80`

## 常见集成场景

### 场景 1: Web 应用集成

将 GoAgent 集成到 Web 应用中：

```go
// Web 服务器端点
func chatHandler(w http.ResponseWriter, r *http.Request) {
    var request struct {
        Message string `json:"message"`
    }
    
    if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // 使用 Agent 处理
    result, err := agentService.Process(r.Context(), request.Message)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(map[string]string{
        "response": result,
    })
}
```

**代码位置**: `examples/devagent/main.go:150-200`

### 场景 2: CLI 工具集成

创建一个命令行工具：

```go
package main

import (
    "bufio"
    "context"
    "fmt"
    "os"
    "github.com/yourusername/goagent/api/service"
)

func main() {
    cfg := loadConfig()
    agent := service.NewAgentService(cfg)
    
    ctx := context.Background()
    agent.Start(ctx)
    
    scanner := bufio.NewScanner(os.Stdin)
    fmt.Println("输入 'exit' 退出")
    
    for scanner.Scan() {
        input := scanner.Text()
        if input == "exit" {
            break
        }
        
        result, err := agent.Process(ctx, input)
        if err != nil {
            fmt.Printf("错误: %v\n", err)
            continue
        }
        
        fmt.Printf("Agent: %s\n", result)
    }
}
```

**代码位置**: `examples/multi-agent-dialog/main.go:50-100`

### 场景 3: 微服务集成

在微服务架构中集成：

```go
// 服务间通信
func (s *OrderService) ProcessWithAgent(ctx context.Context, order *Order) (*OrderResponse, error) {
    // 准备 Agent 查询
    query := fmt.Sprintf("处理订单: %s, 数量: %d", order.ID, order.Quantity)
    
    // 调用 Agent 服务（可以是本地或远程）
    result, err := s.agentService.Process(ctx, query)
    if err != nil {
        return nil, err
    }
    
    // 解析 Agent 响应
    return s.parseAgentResponse(result, order)
}
```

**代码位置**: `api/client/client.go:50-100`

## 错误处理

### 常见错误及解决方案

#### 错误 1: 配置文件未找到

```
Error: config file not found
```

**解决方法**:
```go
// 检查配置文件路径
if _, err := os.Stat(configPath); os.IsNotExist(err) {
    // 使用默认配置
    cfg = service.DefaultConfig()
} else {
    // 加载配置文件
    cfg, err = config.Load(configPath)
}
```

**代码位置**: `internal/config/config.go:80-100`

#### 错误 2: LLM 连接失败

```
Error: failed to connect to LLM service
```

**解决方法**:
```go
// 添加重试逻辑
var result string
var err error

for i := 0; i < 3; i++ {
    result, err = agentService.Process(ctx, query)
    if err == nil {
        break
    }
    time.Sleep(time.Second * time.Duration(i+1))
}

if err != nil {
    return "", fmt.Errorf("LLM service unavailable after retries: %w", err)
}
```

**代码位置**: `internal/llm/client.go:150-180`

#### 错误 3: 存储连接失败

```
Error: failed to connect to database
```

**解决方法**:
```go
// 检查数据库连接
if err := pool.Ping(ctx); err != nil {
    // 禁用存储功能，使用内存模式
    cfg.Storage.Enabled = false
    log.Warn("Storage disabled, using in-memory mode")
}
```

**代码位置**: `internal/storage/postgres/pool.go:50-80`

## 性能优化

### 连接池配置

```go
poolConfig := &pool.Config{
    MaxOpenConns:    25,
    MaxIdleConns:    10,
    ConnMaxLifetime: 5 * time.Minute,
}
```

**代码位置**: `internal/storage/postgres/pool.go:70-100`

### 并发控制

```go
cfg := &service.Config{
    Agents: service.AgentsConfig{
        Leader: service.LeaderConfig{
            MaxParallelTasks: 4,  // 控制并发数
        },
    },
}
```

**代码位置**: `internal/agents/leader/agent.go:120-150`

### 缓存配置

```go
cfg := &service.Config{
    Cache: service.CacheConfig{
        Enabled: true,
        TTL:     30 * time.Minute,
    },
}
```

**代码位置**: `internal/llm/client.go:200-250`

## 测试集成

### 单元测试

```go
func TestAgentIntegration(t *testing.T) {
    ctx := context.Background()
    
    // 创建测试配置
    cfg := service.DefaultConfig()
    
    // 创建 Agent
    agent := service.NewAgentService(cfg)
    
    // 启动 Agent
    if err := agent.Start(ctx); err != nil {
        t.Fatalf("Failed to start agent: %v", err)
    }
    
    // 测试查询
    result, err := agent.Process(ctx, "测试查询")
    if err != nil {
        t.Errorf("Failed to process query: %v", err)
    }
    
    if result == "" {
        t.Error("Expected non-empty result")
    }
}
```

**代码位置**: `internal/agents/leader/agent_test.go:50-100`

### 集成测试

```go
func TestEndToEndIntegration(t *testing.T) {
    // 启动测试数据库
    db := startTestDB(t)
    defer db.Close()
    
    // 创建完整配置
    cfg := &service.Config{
        LLM: service.LLMConfig{
            Provider: "test",
        },
        Storage: service.StorageConfig{
            Enabled: true,
            Type:     "postgres",
            Host:     db.Host,
            Port:     db.Port,
        },
    }
    
    // 测试完整流程
    agent := service.NewAgentService(cfg)
    ctx := context.Background()
    
    agent.Start(ctx)
    result, err := agent.Process(ctx, "测试消息")
    
    assert.NoError(t, err)
    assert.NotEmpty(t, result)
}
```

**代码位置**: `api/service/integration_test.go:100-150`

## 参考文档

- [快速开始](quick_start.md)
- [架构文档](arch.md)
- [配置参考](../examples/travel/config/server.yaml)
- [API 文档](storage/api.md)

## 支持

如有问题或需要帮助：
1. 查看本文档的其他章节
2. 参考示例代码（`examples/`）
3. 提交 Issue 到 GitHub

---

**版本**: 1.0  
**最后更新**: 2026-03-23  
**维护者**: GoAgent 团队