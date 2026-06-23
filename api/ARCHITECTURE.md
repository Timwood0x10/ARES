# API Architecture Design

## 架构概览

GoAgent API 采用三层架构设计，每一层都有明确的职责和边界。

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client Layer                              │
│                   (ares/api/client)                           │
│                                                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐         │
│  │   unified.go │  │   agent.go   │  │  memory.go   │         │
│  │              │  │              │  │              │         │
│  │  统一客户端   │  │  Agent客户端  │  │ Memory客户端 │         │
│  │  入口         │  │              │  │              │         │
│  └──────────────┘  └──────────────┘  └──────────────┘         │
│         │                  │                  │                 │
│         └──────────────────┼──────────────────┘                 │
│                            │                                     │
└────────────────────────────┼─────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Service Layer                              │
│                (ares/api/service/*)                           │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                        agent/                             │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐               │   │
│  │  │ service  │  │ errors   │  │  TODO    │               │   │
│  │  │    .go   │  │   .go    │  │ (future) │               │   │
│  │  └──────────┘  └──────────┘  └──────────┘               │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                        memory/                            │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐               │   │
│  │  │ service  │  │ errors   │  │  TODO    │               │   │
│  │  │    .go   │  │   .go    │  │ (future) │               │   │
│  │  └──────────┘  └──────────┘  └──────────┘               │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                       retrieval/                          │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐               │   │
│  │  │ service  │  │ errors   │  │  TODO    │               │   │
│  │  │    .go   │  │   .go    │  │ (future) │               │   │
│  │  └──────────┘  └──────────┘  └──────────┘               │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                          llm/                              │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐               │   │
│  │  │ service  │  │ errors   │  │  TODO    │               │   │
│  │  │    .go   │  │   .go    │  │ (future) │               │   │
│  │  └──────────┘  └──────────┘  └──────────┘               │   │
│  └──────────────────────────────────────────────────────────┘   │
│         │                  │                  │                 │
│         └──────────────────┼──────────────────┘                 │
│                            │                                     │
└────────────────────────────┼─────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                        Core Layer                                 │
│                   (ares/api/core)                              │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  types.go      - 公共类型定义                            │   │
│  │  agent.go      - Agent核心接口                          │   │
│  │  memory.go     - Memory核心接口                         │   │
│  │  retrieval.go  - Retrieval核心接口                      │   │
│  │  llm.go        - LLM核心接口                            │   │
│  └──────────────────────────────────────────────────────────┘   │
│         │                  │                  │                 │
│         └──────────────────┼──────────────────┘                 │
│                            │                                     │
└────────────────────────────┼─────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Internal Layer                              │
│                    (ares/internal/*)                           │
│                                                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐      │
│  │  agents  │  │  memory  │  │ storage  │  │   llm    │      │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘      │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 层次说明

### 1. Client Layer（客户端层）

**位置**: `ares/api/client`

**职责**:
- 提供统一的客户端接口
- 管理所有服务的生命周期
- 提供便捷的服务访问方法

**特点**:
- 对外暴露的最终接口
- 聚合所有服务
- 统一配置和初始化
- 错误处理和日志记录

**主要文件**:
- `unified.go`: 统一客户端入口
- `errors.go`: 客户端错误定义

### 2. Service Layer（服务层）

**位置**: `ares/api/service/*`

**职责**:
- 实现core层定义的Service接口
- 编排业务逻辑
- 处理数据验证和转换
- 管理与internal层的交互

**特点**:
- 依赖core层的接口
- 依赖internal层的具体实现
- 不对外暴露，只通过client层访问
- 每个服务独立维护

**主要模块**:
- `agent/`: Agent服务实现
- `memory/`: Memory服务实现
- `retrieval/`: Retrieval服务实现
- `llm/`: LLM服务实现

### 3. Core Layer（核心抽象层）

**位置**: `ares/api/core`

**职责**:
- 定义所有模块的核心接口
- 定义公共数据结构
- 提供类型安全和抽象

**特点**:
- 纯接口定义，不包含具体实现
- 所有类型都在core包中定义
- 服务层和客户端层都依赖core层
- 确保接口一致性

**主要文件**:
- `types.go`: 公共类型定义
- `agent.go`: Agent核心接口
- `memory.go`: Memory核心接口
- `retrieval.go`: Retrieval核心接口
- `llm.go`: LLM核心接口

### 4. Internal Layer（内部实现层）

**位置**: `ares/internal/*`

**职责**:
- 提供具体的业务实现
- 管理数据存储和访问
- 处理底层技术细节

**特点**:
- 不对外暴露
- 包含具体的业务逻辑
- 可以随时替换实现
- 遵循internal包可见性规则

## 数据流向

```
用户请求
    │
    ▼
Client Layer (unified.go)
    │ - 验证配置
    │ - 初始化服务
    ▼
Service Layer (service/*.go)
    │ - 业务逻辑
    │ - 数据验证
    │ - 类型转换
    ▼
Internal Layer (internal/*)
    │ - 具体实现
    │ - 数据访问
    ▼
返回结果
```

## 依赖关系

```
Client Layer
    │
    ├── depends on ───▶ Service Layer
    │
    └── depends on ───▶ Core Layer

Service Layer
    │
    ├── depends on ───▶ Core Layer
    │
    └── depends on ───▶ Internal Layer

Core Layer
    │
    └── independent (no dependencies)

Internal Layer
    │
    └── independent (no dependencies on API layers)
```

## 接口设计原则

### 1. Repository接口

**目的**: 定义数据访问操作

**特点**:
- CRUD操作
- 查询操作
- 无业务逻辑
- 可替换实现

**示例**:
```go
type AgentRepository interface {
    Create(ctx context.Context, agent *Agent) error
    Get(ctx context.Context, agentID string) (*Agent, error)
    Update(ctx context.Context, agent *Agent) error
    Delete(ctx context.Context, agentID string) error
    List(ctx context.Context, filter *AgentFilter) ([]*Agent, error)
}
```

### 2. Service接口

**目的**: 定义业务逻辑操作

**特点**:
- 业务规则
- 数据验证
- 事务管理
- 错误处理

**示例**:
```go
type AgentService interface {
    CreateAgent(ctx context.Context, config *AgentConfig) (*Agent, error)
    GetAgent(ctx context.Context, agentID string) (*Agent, error)
    UpdateAgent(ctx context.Context, agentID string, updates map[string]interface{}) (*Agent, error)
    DeleteAgent(ctx context.Context, agentID string) error
    ListAgents(ctx context.Context, filter *AgentFilter) ([]*Agent, *PaginationResponse, error)
}
```

## 错误处理策略

### 错误分类

1. **输入验证错误**: 用户提供的数据无效
2. **业务逻辑错误**: 业务规则违反
3. **系统错误**: 系统内部错误
4. **外部依赖错误**: 外部服务不可用

### 错误传播

```
Internal Layer
    │ error
    ▼
Service Layer
    │ wrap error with context
    ▼
Client Layer
    │ return to user
    ▼
User
```

### 错误示例

```go
// Internal layer
if agent == nil {
    return ErrAgentNotFound
}

// Service layer
agent, err := s.repo.Get(ctx, agentID)
if err != nil {
    return nil, fmt.Errorf("get agent: %w", err)
}

// Client layer
agent, err := s.agentSvc.GetAgent(ctx, agentID)
if err != nil {
    if errors.Is(err, ErrAgentNotFound) {
        // Handle not found
    }
    return err
}
```

## 扩展性设计

### 添加新服务

1. 在core层定义接口
2. 在service层实现服务
3. 在client层添加访问方法

### 添加新功能

1. 在core层定义类型和接口
2. 在service层实现业务逻辑
3. 更新文档和示例

### 替换实现

1. 实现core层的接口
2. 在service配置中传入新实现
3. 测试验证

## 性能考虑

1. **连接池**: 数据库和外部服务使用连接池
2. **缓存**: 频繁访问的数据使用缓存
3. **并发**: 使用goroutine池管理并发
4. **限流**: 对外部服务调用进行限流
5. **监控**: 添加性能监控和日志

## 安全考虑

1. **输入验证**: 所有输入都进行验证
2. **权限控制**: 多租户隔离和权限检查
3. **敏感数据**: 不记录敏感信息
4. **错误信息**: 不泄露系统细节
5. **依赖注入**: 使用依赖注入提高安全性

## 测试策略

1. **单元测试**: 测试每个service
2. **集成测试**: 测试服务之间的交互
3. **端到端测试**: 测试完整的用户流程
4. **Mock测试**: 使用mock隔离外部依赖

## 总结

新的分层架构提供了：

- **清晰的职责分离**: 每层都有明确的职责
- **更好的可测试性**: 基于接口的设计
- **更强的扩展性**: 易于添加新功能
- **统一的错误处理**: 标准化的错误定义
- **更好的文档**: 完整的类型定义

这种架构设计遵循了SOLID原则，特别是依赖倒置原则（DIP），确保了代码的可维护性和可扩展性。