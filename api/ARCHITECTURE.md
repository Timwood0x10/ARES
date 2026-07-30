# API Architecture Design

## 架构概览

ARES API 采用三层架构设计，每一层都有明确的职责和边界。

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

## Dependency Graph

This section documents how the SDK Runtime wires its modules at construction
time (`sdk.New`) and which modules the agent execution flow passes through at
run time (`Agent.Run`). Every claim below is traceable to a source location so
new developers can answer "which modules does the agent execution flow pass
through?" without reading the whole codebase.

### Bootstrap stage (sdk.New)

`sdk.New` (`sdk/sdk.go:587`) constructs the Runtime by composing a handful of
`wire*` helpers. The call order below is the actual order in `New`, not a
logical reordering. Each leaf names the value stored on the `Runtime` struct
(`sdk/sdk.go:127`) and the source location that backs it.

```
sdk.New  (sdk/sdk.go:587)
├─ llm.NewService                     → llmSvc            (sdk.go:601)
├─ tools.NewRegistry                  → toolReg           (sdk.go:606)
├─ wireMemory                         → memMgr, embClient, expRepo, distillSvc,
│   (sdk/memory_wiring.go:413)          akgDistiller, distillCleanup
│   ├─ distillation disabled:
│   │    memory.NewMemoryManager           (compression only)   (memory_wiring.go:417)
│   ├─ distillation enabled + deps OK:
│   │    memory.NewMemoryManagerWithDistiller (compression + RAG + distiller)
│   │                                                              (memory_wiring.go:440)
│   ├─ buildDistillationService       → distillSvc        (optional, non-fatal) (memory_wiring.go:451)
│   └─ buildAKGDistiller              → akgDistiller      (optional, non-fatal) (memory_wiring.go:465)
│   Fallback: distillation deps missing or construction failed → compression-only
│   NewMemoryManager                                          (memory_wiring.go:424-438)
├─ wireMCPClients                     → mcpClients        (sdk.go:629, def L552)
├─ wireKnowledge                      → knowledgeRT, knowledgeStore, evolutionStore
│   (sdk.go:636, def L366)
│   ├─ registerKnowledgeProviders     (memory / evolution / extra)  (sdk.go:378, def L423)
│   ├─ buildKnowledgeStore            (SQLite > Postgres > in-memory) (sdk.go:382, def L449)
│   ├─ StoreProvider registered       (closes AKG write→read loop)   (sdk.go:391)
│   └─ khruntime.New                  (planner + linkers + reducers) (sdk.go:402)
├─ registerAKFTools                   (optional, when knowledge enabled) (sdk.go:642)
├─ wireEvolutionHotUpdate             → evoComponents      (optional) (sdk.go:650, def L525)
├─ wireSDKRetrievers                  (best-effort) injects MemoryRetriever +
│   (sdk.go:654, def L723)              KnowledgeRetriever into memMgr via SetRetrievers
├─ buildAKGBridge                     → akgBridge          (optional) (sdk.go:659, def L499)
└─ newEventBackend                    → ctx, cancel, eg, eventStore + subscriber
    (sdk.go:661, def sdk/distill_events.go:47)
    └─ wireDistillationSubscriber     (TaskCompleted/TaskFailed → distillSvc + akgBridge)
        (sdk/distill_events.go:85)
```

Notes on the bootstrap tree:

- `wireMemory` is the only helper with a fallback path. When distillation is
  enabled but its dependencies (embedding service URL or Postgres host) are
  missing, it logs a warning and returns a compression-only
  `MemoryManager` instead of failing the whole Runtime
  (`sdk/memory_wiring.go:424-438`, `ErrDistillDepsMissing` at L57).
- `distillSvc` and `akgDistiller` are both constructed non-fatally: a
  construction failure disables event-driven distillation (or the AKG bridge)
  but leaves the `MemoryManager` working (`sdk/memory_wiring.go:451-472`).
- `newEventBackend` creates the in-memory `MemoryEventStore`
  (`sdk/distill_events.go:55`) and starts the background distillation
  subscriber only when at least one of `distillSvc` / `akgBridge` is non-nil
  (`sdk/distill_events.go:56`). When both are nil, the store is still
  returned but no subscriber runs.

### Execution stage (Agent.Run)

`Agent.Run` (`sdk/sdk.go:954`) runs a ReAct loop. The tree below lists every
module the execution flow passes through. "optional" means the call is gated
on a runtime flag (memory enabled / eventStore non-nil) and may be skipped.

```
Agent.Run  (sdk/sdk.go:954)
├─ buildMessages                                 (sdk.go:965, def L1128)
│  ├─ memMgr.BuildContext        (optional, RAG context)        (sdk.go:1140)
│  ├─ knowledgeRT.Execute        (optional, AKG context)        (sdk.go:1156)
│  └─ memMgr.AddMessage          (user message, optional)       (sdk.go:1181)
├─ for iter < maxIter:
│  ├─ llmSvc.Generate            → LLM response                 (sdk.go:983)
│  ├─ memMgr.AddMessage          (assistant message, optional)  (sdk.go:1002)
│  ├─ if no tool calls (final answer):
│  │  ├─ ares_events.Emit EventTaskCompleted
│  │  │   (optional, gated on eventStore + distillSvc)          (sdk.go:1017)
│  │  └─ return Result
│  └─ for each tool call:
│     ├─ eventStore.Append EventToolCallStarted  (optional)     (sdk.go:1066)
│     ├─ toolReg.Execute                         → tool result  (sdk.go:1078)
│     └─ eventStore.Append EventToolCallCompleted (optional)    (sdk.go:1093)
```

So the modules the execution flow always passes through are `llmSvc` and
`toolReg`. The `memMgr`, `knowledgeRT`, and `eventStore` modules are
optional and depend on the Runtime configuration (`memEnabled`,
`knowledgeEnabled`, and whether `distillSvc` was constructed).

### Events flow

The diagram below shows the flow of events emitted by `Agent.Run`. Solid
edges run inside every `sdk.New` Runtime that has distillation enabled. The
dashed edge (Dashboard EventBridge) is wired only in the full service
deployment (`internal/api_impl/service.go`), where a shared `EventStore` is
injected into `ares_bootstrap.Bootstrap` and also fed to the dashboard — it
is not wired by `sdk.New` itself.

```mermaid
graph LR
    Run["Agent.Run<br/>(sdk.go:940)"]
    ES[("EventStore<br/>in-memory<br/>ares_events.MemoryEventStore")]
    Sub["Distillation Subscriber<br/>(sdk/distill_events.go:85)"]
    DistSvc["DistillationService<br/>(ares_experience)"]
    ExpRepo[("Experience Repository<br/>Postgres")]
    AKG["AKG DistillBridge<br/>(knowledge/adapter)"]
    KS[("KnowledgeStore")]
    Dash["Dashboard EventBridge<br/>(internal/dashboard)"]
    WS["WebSocket Hub"]
    Intel["Intelligence Engine"]

    Run -->|EventTaskCompleted<br/>EventToolCallStarted/Completed| ES
    ES -->|Subscribe<br/>TaskCompleted / TaskFailed| Sub
    Sub --> DistSvc
    DistSvc -->|experienceRepo.Create<br/>(distillation_service.go:109)| ExpRepo
    Sub --> AKG
    AKG -->|DistillConversation<br/>→ quality gate| KS
    ES -.->|Subscribe<br/>all events<br/>wired in api_impl/service.go:223| Dash
    Dash --> WS
    Dash --> Intel
```

Verified branches:

- `Agent.Run` → `EventStore`: `ares_events.Emit` writes `EventTaskCompleted`
  (`sdk/sdk.go:1003-1012`); `eventStore.Append` writes `EventToolCallStarted`
  and `EventToolCallCompleted` (`sdk/sdk.go:1052`, `sdk/sdk.go:1079`).
- `EventStore` → Distillation Subscriber → `DistillationService` → Experience
  Repository: the subscriber is started in `wireDistillationSubscriber`
  (`sdk/distill_events.go:85`), which calls
  `ares_bootstrap.HandleTaskCompletedForDistillation`
  (`sdk/distill_events.go:119`) → `DistillationService.Distill`
  (`internal/ares_bootstrap/provide_distillation.go:180`) →
  `experienceRepo.Create` (`internal/ares_experience/distillation_service.go:109`).
- `EventStore` → AKG DistillBridge → KnowledgeStore: the subscriber calls
  `triggerAKGBridge` (`sdk/distill_events.go:122`) →
  `bridge.DistillConversation` (`sdk/distill_events.go:165`), which persists
  AKG KnowledgeObjects through the quality gate into the `KnowledgeStore`
  wired by `buildAKGBridge` (`sdk/sdk.go:646`).
- `EventStore` → Dashboard EventBridge → WebSocket / Intelligence Engine:
  `dashboard.NewEventBridge` subscribes to the EventStore and forwards every
  event to the WebSocket hub and the intelligence engine
  (`internal/dashboard/event_bridge.go:32`, `:84`, `:95`). It is wired in
  `internal/api_impl/service.go:223` against the shared EventStore built at
  `internal/api_impl/service.go:176` and passed to
  `ares_bootstrap.Bootstrap` at `internal/api_impl/service.go:184`. This
  branch does NOT run in a pure `sdk.New` Runtime, whose `eventStore` is
  private and has no public accessor on `Runtime` (`sdk/sdk.go:129`).

### Module maturity

The AKG / knowledge packages — `internal/knowledge` and its subpackages
(`adapter`, `compiler`, `linker`, `planner`, `provider`, `runtime`, `store/*`)
— plus the evolution packages under `internal/ares_evolution` are designated
Beta. They carry package-level Beta markers added by Task 2 of the ARES
optimization plan; treat their public API as potentially unstable and subject
to breaking changes before GA. The LLM, tool registry, memory manager, event
store, and distillation service referenced in the trees above are stable.