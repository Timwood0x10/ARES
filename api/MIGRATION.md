# API Migration Guide

## ⚠️ IMPORTANT: Old Client Removed

**The legacy client (`goagentx/api` package's `Client`, `Config`, `NewClient`) has been REMOVED.**

The following types/functions no longer exist and must be migrated to `goagentx/api/client`:

- ~~`api.Client`~~ → **REMOVED** — use `client.Client`
- ~~`api.Config`~~ → **REMOVED** — use `client.Config`
- ~~`api.NewClient()`~~ → **REMOVED** — use `client.NewClient()`
- ~~`api.DatabaseConfig`~~ → **REMOVED**
- ~~`api.LLMConfig`~~ → **REMOVED**
- ~~`api.MemoryConfig`~~ → **REMOVED**
- ~~`api.EmbeddingConfig`~~ → **REMOVED**
- ~~`api.RetrievalConfig`~~ → **REMOVED**

**Note:** The `goagentx/api` package still exists and provides the **Service layer** (`StartService`, `LoadServiceConfig`, `Service`, etc.) used by examples. Only the legacy *library-style client* was removed.

## 概述

本文档说明如何从旧的API（`goagentx/api`包中的老客户端）迁移到新的分层API（`goagentx/api/client`包）。

**老客户端已删除。** 如果你的代码仍在使用上述类型，请立即迁移到新客户端。

## 为什么迁移？

新的分层API架构具有以下优势：

1. **清晰的职责分离**：Core、Service、Client三层架构，职责明确
2. **更好的可测试性**：基于接口的设计，易于mock和测试
3. **更强的扩展性**：易于添加新的功能和模块
4. **统一的错误处理**：标准化的错误定义和处理
5. **更好的文档**：完整的类型定义和使用说明

## 迁移步骤

### 步骤1：更新导入路径

**旧代码**：
```go
import (
    "goagentx/api"
    "goagentx/api/agent"
    "goagentx/api/memory"
)
```

**新代码**：
```go
import (
    "goagentx/api/client"
    "goagentx/api/core"
    "goagentx/api/service/agent"
    "goagentx/api/service/memory"
)
```

### 步骤2：更新客户端初始化

**旧代码**：
```go
config := &api.Config{
    Database: &api.DatabaseConfig{
        Host:     "localhost",
        Port:     5432,
        User:     "user",
        Password: "pass",
        Database: "goagent",
    },
    LLM: &api.LLMConfig{
        Provider: "ollama",
        BaseURL:  "http://localhost:11434",
        Model:    "llama3",
        Timeout:  60,
    },
    Memory: &api.MemoryConfig{
        Enabled:    true,
        MaxHistory: 100,
    },
}

client, err := api.NewClient(config)
if err != nil {
    slog.Error(err)
}
defer client.Close(context.Background())
```

**新代码**：
```go
config := &client.Config{
    BaseConfig: &core.BaseConfig{
        RequestTimeout: 30 * time.Second,
        MaxRetries:     3,
        RetryDelay:     1 * time.Second,
    },
    Agent: &agentservice.Config{
        // Agent服务配置
    },
    Memory: &memoryservice.Config{
        // Memory服务配置
    },
    Retrieval: &retrievalservice.Config{
        // Retrieval服务配置
    },
    LLM: &llmservice.Config{
        LLMConfig: &core.LLMConfig{
            Provider: core.LLMProviderOllama,
            BaseURL:  "http://localhost:11434",
            Model:    "llama3",
            Timeout:  60,
        },
    },
}

client, err := client.NewClient(config)
if err != nil {
    slog.Error(err)
}
defer client.Close(context.Background())
```

### 步骤3：更新服务访问方式

**旧代码**：
```go
agentSvc := client.Agent()
memorySvc := client.Memory()
retrievalSvc := client.Retrieval()
```

**新代码**：
```go
agentSvc, err := client.Agent()
if err != nil {
    slog.Error(err)
}

memorySvc, err := client.Memory()
if err != nil {
    slog.Error(err)
}

retrievalSvc, err := client.Retrieval()
if err != nil {
    slog.Error(err)
}
```

### 步骤4：更新类型引用

**旧代码**：
```go
agent, err := agentSvc.CreateAgent(ctx, "agent-001")
```

**新代码**：
```go
agent, err := agentSvc.CreateAgent(ctx, &core.AgentConfig{
    ID:   "agent-001",
    Name: "My Agent",
    Type: "sub",
})
```

### 步骤5：更新错误处理

**旧代码**：
```go
agent, err := agentSvc.CreateAgent(ctx, "agent-001")
if err != nil {
    if err == agent.ErrInvalidAgentID {
        // 处理错误
    }
    slog.Error(err)
}
```

**新代码**：
```go
agent, err := agentSvc.CreateAgent(ctx, &core.AgentConfig{
    ID: "agent-001",
})
if err != nil {
    if errors.Is(err, agent.ErrInvalidAgentID) {
        // 处理错误
    }
    slog.Error(err)
}
```

## API对比表

### Agent API

| 功能 | 旧API | 新API |
|------|-------|-------|
| 创建Agent | `CreateAgent(ctx, agentID)` | `CreateAgent(ctx, *AgentConfig)` |
| 获取Agent | `GetAgent(ctx, agentID)` | `GetAgent(ctx, agentID)` |
| 更新Agent | 不支持 | `UpdateAgent(ctx, agentID, updates)` |
| 删除Agent | `DeleteAgent(ctx, agentID)` | `DeleteAgent(ctx, agentID)` |
| 列出Agent | 不支持 | `ListAgents(ctx, *AgentFilter)` |
| 执行任务 | 不支持 | `ExecuteTask(ctx, *Task)` |

### Memory API

| 功能 | 旧API | 新API |
|------|-------|-------|
| 创建会话 | `CreateSession(ctx, userID)` | `CreateSession(ctx, *SessionConfig)` |
| 添加消息 | `AddMessage(ctx, sessionID, role, content)` | `AddMessage(ctx, sessionID, MessageRole, content)` |
| 获取消息 | `GetMessages(ctx, sessionID)` | `GetMessages(ctx, sessionID, *PaginationRequest)` |
| 删除会话 | `DeleteSession(ctx, sessionID)` | `DeleteSession(ctx, sessionID)` |
| 蒸馏任务 | `DistillTask(ctx, taskID)` | `DistillTask(ctx, taskID)` |
| 搜索任务 | `SearchSimilarTasks(ctx, query, limit)` | `SearchSimilarTasks(ctx, *SearchQuery)` |

### Retrieval API

| 功能 | 旧API | 新API |
|------|-------|-------|
| 搜索 | `Search(ctx, tenantID, query)` | `Search(ctx, tenantID, query)` |
| 自定义搜索 | `SearchWithConfig(ctx, tenantID, query, *Config)` | `SearchWithConfig(ctx, *RetrievalRequest)` |
| 添加知识 | 不支持 | `AddKnowledge(ctx, *KnowledgeItem)` |
| 获取知识 | 不支持 | `GetKnowledge(ctx, tenantID, itemID)` |
| 更新知识 | 不支持 | `UpdateKnowledge(ctx, tenantID, *KnowledgeItem)` |
| 删除知识 | 不支持 | `DeleteKnowledge(ctx, tenantID, itemID)` |
| 列出知识 | 不支持 | `ListKnowledge(ctx, tenantID, *KnowledgeFilter)` |

### LLM API

| 功能 | 旧API | 新API |
|------|-------|-------|
| 生成文本 | 不支持 | `Generate(ctx, *GenerateRequest)` |
| 简单生成 | 不支持 | `GenerateSimple(ctx, prompt)` |
| 生成嵌入 | 不支持 | `GenerateEmbedding(ctx, *EmbeddingRequest)` |
| 获取配置 | 不支持 | `GetConfig()` |
| 检查可用性 | 不支持 | `IsEnabled()` |

## 完整迁移示例

### 旧代码示例

```go
package main

import (
    "context"
    "log"
    
    "goagentx/api"
)

func main() {
    config := &api.Config{
        Database: &api.DatabaseConfig{
            Host:     "localhost",
            Port:     5432,
            User:     "user",
            Password: "pass",
            Database: "goagent",
        },
        Memory: &api.MemoryConfig{
            Enabled:    true,
            MaxHistory: 100,
        },
    }
    
    client, err := api.NewClient(config)
    if err != nil {
        slog.Error(err)
    }
    defer client.Close(context.Background())
    
    // Create agent
    agent, err := client.Agent().CreateAgent(context.Background(), "agent-001")
    if err != nil {
        slog.Error(err)
    }
    
    // Create session
    sessionID, err := client.Memory().CreateSession(context.Background(), "user-001")
    if err != nil {
        slog.Error(err)
    }
    
    // Add message
    err = client.Memory().AddMessage(context.Background(), sessionID, "user", "Hello")
    if err != nil {
        slog.Error(err)
    }
}
```

### 新代码示例

```go
package main

import (
    "context"
    "log"
    "time"
    
    "goagentx/api/client"
    "goagentx/api/core"
    "goagentx/api/service/agent"
    "goagentx/api/service/memory"
)

func main() {
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
    }
    
    client, err := client.NewClient(config)
    if err != nil {
        slog.Error(err)
    }
    defer client.Close(context.Background())
    
    // Get agent service
    agentSvc, err := client.Agent()
    if err != nil {
        slog.Error(err)
    }
    
    // Create agent
    agent, err := agentSvc.CreateAgent(context.Background(), &core.AgentConfig{
        ID:   "agent-001",
        Name: "My Agent",
        Type: "sub",
    })
    if err != nil {
        slog.Error(err)
    }
    
    // Get memory service
    memorySvc, err := client.Memory()
    if err != nil {
        slog.Error(err)
    }
    
    // Create session
    sessionID, err := memorySvc.CreateSession(context.Background(), &core.SessionConfig{
        UserID:   "user-001",
        TenantID: "tenant-001",
    })
    if err != nil {
        slog.Error(err)
    }
    
    // Add message
    err = memorySvc.AddMessage(context.Background(), sessionID, core.MessageRoleUser, "Hello")
    if err != nil {
        slog.Error(err)
    }
}
```

## 向后兼容性

**老客户端已删除，不再可用。** 以下变更已生效：

1. ~~`api.Client`~~、~~`api.Config`~~、~~`api.NewClient()`~~ 等类型和函数已从代码库中移除
2. 新项目必须使用 `goagentx/api/client` 包
3. 仍在使用老类型的代码将无法编译

### 迁移检查清单

- [ ] 将 `import "goagentx/api"` （用于客户端）改为 `import "goagentx/api/client"`
- [ ] 将 `api.Config{...}` 改为 `client.Config{...}`（注意结构不同）
- [ ] 将 `api.NewClient(cfg)` 改为 `client.NewClient(cfg)`
- [ ] 将 `client.Agent()`（直接返回）改为 `client.Agent()` （返回 `(T, error)`）
- [ ] 将 `client.Memory()` / `client.Retrieval()` 同上处理错误返回值

## 常见问题

### Q: 必须立即迁移吗？

A: **是的，必须立即迁移。** 老客户端已从代码库中删除，使用老类型的代码将无法编译。

### Q: 迁移需要多少时间？

A: 迁移时间取决于代码规模。对于小型项目，可能只需要几个小时。对于大型项目，可能需要几天到几周。

### Q: 新API支持所有旧API的功能吗？

A: 新API支持所有旧API的功能，并提供了更多新功能。如果发现缺少某些功能，请提交issue。

### Q: 可以在同一个项目中同时使用新旧API吗？

A: **不可以。** 老客户端已删除，无法再同时使用。请完全迁移到新API `goagentx/api/client`。

## 获取帮助

如果在迁移过程中遇到问题，请：

1. 查看API文档：[API README](./README.md)
2. 查看示例代码：[examples/](../examples/)
3. 提交issue：[GitHub Issues](https://github.com/your-repo/issues)

## 总结

新的分层API架构提供了更好的代码组织和可维护性。通过遵循本指南，你可以平滑地迁移到新API，并享受更好的开发体验。