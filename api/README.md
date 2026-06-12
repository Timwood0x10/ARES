# GoAgent API Architecture

## 概述

GoAgent API 采用分层架构设计，提供统一、清晰、可扩展的API接口。本文档详细说明了API层的结构和使用方法。

## 架构设计

### 分层结构

```
api/
├── core/              # 核心抽象层（接口定义）
│   ├── types.go      # 公共类型定义
│   ├── agent.go      # Agent核心接口
│   ├── memory.go     # Memory核心接口
│   ├── retrieval.go  # Retrieval核心接口
│   └── llm.go        # LLM核心接口
│
├── service/          # 服务层（业务逻辑实现）
│   ├── agent/        # Agent服务实现
│   │   ├── service.go
│   │   └── errors.go
│   ├── memory/       # Memory服务实现
│   │   ├── service.go
│   │   └── errors.go
│   ├── retrieval/    # Retrieval服务实现
│   │   ├── service.go
│   │   └── errors.go
│   └── llm/          # LLM服务实现
│       ├── service.go
│       └── errors.go
│
├── client/           # 客户端层（对外暴露）
│   ├── unified.go    # 统一客户端入口
│   └── errors.go
│
└── errors/           # 统一错误定义
    └── common.go     # 通用错误
```

### 各层职责

#### 1. Core Layer（核心抽象层）

**职责**：
- 定义所有模块的核心接口（Repository和Service接口）
- 定义公共数据结构
- 提供类型安全和抽象

**特点**：
- 纯接口定义，不包含具体实现
- 所有类型都在core包中定义
- 服务层和客户端层都依赖core层

**主要接口**：
- `AgentRepository` / `AgentService`
- `MemoryRepository` / `MemoryService`
- `RetrievalRepository` / `RetrievalService`
- `LLMRepository` / `LLMService`

#### 2. Service Layer（服务层）

**职责**：
- 实现core层定义的Service接口
- 编排业务逻辑
- 处理数据验证和转换
- 管理与internal层的交互

**特点**：
- 依赖core层的接口
- 依赖internal层的具体实现
- 不对外暴露，只通过client层访问

**主要功能**：
- Agent服务：创建、查询、更新、删除Agent
- Memory服务：会话管理、消息管理、任务蒸馏
- Retrieval服务：知识库检索、知识项管理
- LLM服务：文本生成、嵌入生成

#### 3. Client Layer（客户端层）

**职责**：
- 提供统一的客户端接口
- 管理所有服务的生命周期
- 提供便捷的访问方法

**特点**：
- 对外暴露的最终接口
- 聚合所有服务
- 提供统一配置和初始化

**使用方式**：
```go
client := client.NewClient(config)
agentSvc := client.Agent()
memorySvc := client.Memory()
```

#### 4. Errors Layer（错误层）

**职责**：
- 统一错误定义
- 提供错误包装和上下文
- 标准化错误处理

**特点**：
- 所有错误都继承自统一的错误类型
- 支持错误链和上下文
- 提供详细的错误信息

## 使用指南

### 1. 初始化客户端

```go
package main

import (
    "context"
    "log"
    
    "goagentx/api/client"
    "goagentx/api/core"
    "goagentx/api/service/agent"
    "goagentx/api/service/memory"
    "goagentx/api/service/retrieval"
    "goagentx/api/service/llm"
)

func main() {
    // 创建配置
    config := &client.Config{
        BaseConfig: &core.BaseConfig{
            RequestTimeout: 30 * time.Second,
            MaxRetries:     3,
            RetryDelay:     1 * time.Second,
        },
        Agent: &agent.Config{
            // Agent服务配置
        },
        Memory: &memory.Config{
            // Memory服务配置
        },
        Retrieval: &retrieval.Config{
            // Retrieval服务配置
        },
        LLM: &llm.Config{
            LLMConfig: &core.LLMConfig{
                Provider: core.LLMProviderOllama,
                BaseURL:  "http://localhost:11434",
                Model:    "llama3",
                Timeout:  60,
            },
        },
    }
    
    // 创建客户端
    client, err := client.NewClient(config)
    if err != nil {
        slog.Error(err)
    }
    defer client.Close(context.Background())
}
```

### 2. 使用Agent服务

```go
// 获取Agent服务
agentSvc, err := client.Agent()
if err != nil {
    slog.Error(err)
}

// 创建Agent
agent, err := agentSvc.CreateAgent(ctx, &core.AgentConfig{
    ID:   "agent-001",
    Name: "My Agent",
    Type: "sub",
})
if err != nil {
    slog.Error(err)
}

// 查询Agent
agent, err = agentSvc.GetAgent(ctx, "agent-001")
if err != nil {
    slog.Error(err)
}

// 列出所有Agents
agents, pagination, err := agentSvc.ListAgents(ctx, &core.AgentFilter{
    Type: "sub",
})
if err != nil {
    slog.Error(err)
}
```

### 3. 使用Memory服务

```go
// 获取Memory服务
memorySvc, err := client.Memory()
if err != nil {
    slog.Error(err)
}

// 创建会话
sessionID, err := memorySvc.CreateSession(ctx, &core.SessionConfig{
    UserID:   "user-001",
    TenantID: "tenant-001",
    ExpiresIn: 24 * time.Hour,
})
if err != nil {
    slog.Error(err)
}

// 添加消息
err = memorySvc.AddMessage(ctx, sessionID, core.MessageRoleUser, "Hello")
if err != nil {
    slog.Error(err)
}

// 获取消息
messages, err := memorySvc.GetMessages(ctx, sessionID, &core.PaginationRequest{
    Page:     1,
    PageSize: 10,
})
if err != nil {
    slog.Error(err)
}
```

### 4. 使用Retrieval服务

```go
// 获取Retrieval服务
retrievalSvc, err := client.Retrieval()
if err != nil {
    slog.Error(err)
}

// 搜索知识
results, err := retrievalSvc.Search(ctx, "tenant-001", "如何使用GoAgent")
if err != nil {
    slog.Error(err)
}

// 添加知识
item, err := retrievalSvc.AddKnowledge(ctx, &core.KnowledgeItem{
    TenantID: "tenant-001",
    Content:  "GoAgent是一个强大的AI Agent框架",
    Source:   "docs",
    Category: "getting-started",
})
if err != nil {
    slog.Error(err)
}
```

### 5. 使用LLM服务

```go
// 获取LLM服务
llmSvc, err := client.LLM()
if err != nil {
    slog.Error(err)
}

// 生成文本
response, err := llmSvc.GenerateSimple(ctx, "写一首关于春天的诗")
if err != nil {
    slog.Error(err)
}
println(response)

// 生成嵌入
embeddingResp, err := llmSvc.GenerateEmbedding(ctx, &core.EmbeddingRequest{
    Input: "这是一段测试文本",
})
if err != nil {
    slog.Error(err)
}
println(embeddingResp.Embedding)
```

## 错误处理

所有API调用都返回错误，应该正确处理：

```go
agent, err := agentSvc.CreateAgent(ctx, config)
if err != nil {
    if errors.Is(err, errors.ErrInvalidConfig) {
        // 处理配置错误
    } else if errors.Is(err, errors.ErrAgentAlreadyExists) {
        // 处理已存在错误
    } else {
        // 处理其他错误
        slog.Error(err)
    }
}
```

## 最佳实践

### 1. 依赖注入

所有服务都应该通过构造函数注入依赖：

```go
func NewService(config *Config) (*Service, error) {
    if config == nil {
        return nil, errors.ErrInvalidConfig
    }
    // ...
}
```

### 2. 接口依赖

业务逻辑应该依赖接口而不是具体实现：

```go
type Service struct {
    repo core.AgentRepository // 依赖接口
    // ...
}
```

### 3. 上下文传播

所有异步操作都应该传递context：

```go
func (s *Service) CreateAgent(ctx context.Context, config *core.AgentConfig) (*core.Agent, error) {
    // 使用ctx进行超时控制、取消等
}
```

### 4. 错误包装

使用`fmt.Errorf`和`%w`包装错误以保留错误链：

```go
return nil, fmt.Errorf("create agent: %w", err)
```

### 5. 并发安全

使用适当的同步机制保护共享状态：

```go
mu sync.Mutex

func (s *Service) UpdateAgent(ctx context.Context, agentID string, updates map[string]interface{}) (*core.Agent, error) {
    mu.Lock()
    defer mu.Unlock()
    // ...
}
```

## 扩展指南

### 添加新的服务模块

1. 在`core/`中定义接口
2. 在`service/`中实现服务
3. 在`client/unified.go`中添加客户端访问方法

### 添加新的Repository实现

1. 实现`core`包中定义的Repository接口
2. 在Service配置中传入实现

## 迁移指南

从旧API迁移到新API：

1. 更新导入路径
2. 使用新的Client初始化方式
3. 更新错误处理代码
4. 测试所有功能

## 注意事项

1. **向后兼容**：旧API仍然可用，但建议逐步迁移到新API
2. **性能考虑**：新API增加了抽象层，但性能影响很小
3. **测试覆盖**：确保所有服务都有单元测试
4. **文档更新**：及时更新API文档

## 贡献指南

在贡献代码时，请遵循以下规范：

1. 遵循`code_rules.md`中的编码规范
2. 为新功能添加单元测试
3. 更新相关文档
4. 确保通过所有lint检查

## 参考资料

- [编码规范](../plan/code_rules.md)
- [架构文档](../docs/arch.md)
- [API示例](../examples/)