# GoAgent 框架架构设计

**更新日期**: 2026-03-25

## 系统架构总览

```mermaid
graph TB
    %% User Input
    UserInput[User Input<br/>帮我规划一次旅行...] --> LeaderAgent

    %% Leader Agent
    subgraph LeaderAgent["Leader Agent"]
        ParseInput[Parse Input<br/>LLM]
        TaskPlan[Task Plan<br/>LLM]
        Aggregate[Aggregate<br/>Results]
        ParseInput --> TaskPlan
        TaskPlan --> Aggregate
    end

    LeaderAgent -->|dispatch tasks 并行| SubAgents

    %% Sub Agents
    subgraph SubAgents["Sub Agents"]
        AgentDest[Agent-destination<br/>Sub Agent]
        AgentFood[Agent-food<br/>Sub Agent]
        AgentHotel[Agent-hotel<br/>Sub Agent]
    end

    SubAgents --> ProtocolLayer

    %% Protocol Layer
    subgraph ProtocolLayer["Protocol Layer"]
        AHPProtocol[AHP Protocol<br/>Agent Handshake Protocol]
        TASK[TASK]
        RESULT[RESULT]
        PROGRESS[PROGRESS]
        AHPProtocol --> TASK
        AHPProtocol --> RESULT
        AHPProtocol --> PROGRESS
    end

    ProtocolLayer --> CoreServices

    %% Core Services
    subgraph CoreServices["Core Services"]
        %% Tools
        subgraph Tools["Tools System"]
            Calculator[calculator]
            DateTime[datetime]
            HTTPRequest[http_request]
        end

        %% LLM
        subgraph LLM["LLM System"]
            Ollama[Ollama]
            OpenRouter[OpenRouter]
        end

        %% Storage
        subgraph Storage["Storage"]
            PostgreSQL[PostgreSQL + pgvector]
            ConnPool[Connections Pool 25/10]
            VectorIndex[Vector Index ivfflat]
            CosineSearch[Cosine Search]
            PostgreSQL --> ConnPool
            PostgreSQL --> VectorIndex
            PostgreSQL --> CosineSearch
        end
    end

    CoreServices --> MemorySystem

    %% Memory System
    subgraph MemorySystem["Memory System"]
        Session[Session<br/>短期内存]
        UserMemory[User Memory<br/>长期-PG]
        TaskMemory[Task Memory<br/>蒸馏-PG]
        ExperienceSystem[Experience System]
        ProdMgr[ProductionMemoryManager<br/>PG + pgvector 持久化]

        subgraph ExpFlow["Experience Distillation Flow"]
            TaskExec[Task Execution]
            TaskResult[TaskResult]
            Distillation[Distillation]
            Experience[Experience]
            DB[DB]
            Query[Query]
            Retrieval[Retrieval<br/>Vector Search]
            Ranked[Ranked]
            Conflict[Conflict Resolution]

            TaskExec --> TaskResult
            TaskResult --> Distillation
            Distillation --> Experience
            Experience --> DB
            Query --> Retrieval
            Retrieval --> Ranked
            Ranked --> Conflict
        end
    end

    %% Styling
    style LeaderAgent fill:#e1f5ff
    style SubAgents fill:#fff4e1
    style ProtocolLayer fill:#f3e5ff
    style MemorySystem fill:#e8f5e9
    style ExperienceSystem fill:#ffebee
    style ExpFlow fill:#fce4ec
```

**代码位置**:
- Leader Agent: `internal/agents/leader/agent.go`
- Sub Agent: `internal/agents/sub/agent.go`
- Protocol: `internal/protocol/ahp/`
- LLM Client: `internal/llm/client.go`
- Storage Pool: `internal/storage/postgres/pool.go`
- Memory Manager: `internal/memory/production_manager.go`
- Experience Distillation: `api/experience/`
- Experience Repository: `internal/storage/postgres/repositories/`

---

## 消息流转机制

```mermaid
sequenceDiagram
    participant Leader as Leader Agent
    participant MQ as Message Queue
    participant Sub as Sub Agent

    Note over Leader,Sub: 发送 (Send)
    Leader->>MQ: TASK
    MQ->>Sub: TASK

    Note over Sub,Leader: 接收/处理 (Receive/Process)
    Sub->>Leader: RESULT
    Leader->>Sub: ACK
```

```mermaid
graph LR
    subgraph MessageTypes["消息类型 (Message Types)"]
        TASK[TASK<br/>派发任务<br/>Leader → Sub Agent]
        RESULT[RESULT<br/>返回结果<br/>Sub Agent → Leader]
        PROGRESS[PROGRESS<br/>进度汇报<br/>Sub Agent → Leader]
        ACK[ACK<br/>确认收到<br/>Sub Agent → Leader]
        HEARTBEAT[HEARTBEAT<br/>心跳保活<br/>All Agents]
    end

    style TASK fill:#e1f5ff
    style RESULT fill:#fff4e1
    style PROGRESS fill:#e8f5e9
    style ACK fill:#f3e5ff
    style HEARTBEAT fill:#ffebee
```

---

## 任务生产消费流程

```mermaid
graph TB
    subgraph Producer["Leader Agent (Producer)"]
        Step1["1. 解析用户输入"]
        ParseProfile[Parse Profile<br/>UserProfile]
        Step2["2. LLM 决策需要哪些 agent"]
        DetermineTasks[Determine Tasks<br/>top, bottom]
    end

    subgraph TaskQueues["Task Queues"]
        QueueTop[Task Queue<br/>agent_top]
        QueueBottom[Task Queue<br/>agent_btm]
        QueueOther[Task Queue<br/>...]
    end

    subgraph Phase1["Phase 1: 并行派发<br/>ThreadPoolExecutor"]
        SubAgent1[Sub Agent<br/>Consumer]
        SubAgent2[Sub Agent<br/>Consumer]
    end

    subgraph TaskExecution["3. 处理任务"]
        ExecuteTask[Execute Task]
        Tools[Tools<br/>RAG]
        LLM[LLM]
    end

    subgraph Result["4. 返回结果"]
        SendResult[Send RESULT<br/>to Leader]
    end

    subgraph Phase2["Phase 2: 依赖感知的任务<br/>top结果给shoes"]
        Coordination[Coordination<br/>Context]
    end

    subgraph Aggregator["Leader (Aggregator)"]
        Step5["5. 聚合所有结果"]
        Aggregate[Aggregate<br/>top+bottom+head+shoes]
        FinalOutput[Final Output<br/>Save to DB]
    end

    Step1 --> ParseProfile
    ParseProfile --> Step2
    Step2 --> DetermineTasks
    DetermineTasks --> QueueTop
    DetermineTasks --> QueueBottom
    DetermineTasks --> QueueOther

    QueueTop --> Phase1
    QueueBottom --> Phase1
    QueueOther --> Phase1

    Phase1 --> SubAgent1
    Phase1 --> SubAgent2

    SubAgent1 --> TaskExecution
    SubAgent2 --> TaskExecution

    TaskExecution --> ExecuteTask
    ExecuteTask --> Tools
    ExecuteTask --> LLM

    TaskExecution --> SendResult
    SendResult --> Phase2
    Phase2 --> Coordination
    Coordination --> Step5
    Step5 --> Aggregate
    Aggregate --> FinalOutput

    style Producer fill:#e1f5ff
    style Phase1 fill:#fff4e1
    style TaskExecution fill:#e8f5e9
    style Result fill:#f3e5ff
    style Phase2 fill:#fce4ec
    style Aggregator fill:#ffebee
```

---

## Actor 模型对应关系

| Actor 模型概念 | go-agent 实现 |
|----------------|-----------|
| Actor | `LeaderAgent`, `OutfitSubAgent` |
| Mailbox | `MessageQueue` (In-Memory) |
| Message | `AHPMessage` (TASK/RESULT/PROGRESS/ACK) |
| Behavior | Agent 内部的 `_handle_task()`, `_recommend()` |
| Supervisor | `LeaderAgent` 协调多个 Sub Agent |
| Failure Handling | DLQ (Dead Letter Queue) |

---

## 关键设计点

| 特性 | 实现方式 |
|------|----------|
| **并发模型** | Worker Pool 派发任务到多个 Sub Agent |
| **通信协议** | In-Memory Message Queue + AHP 自定义协议 |
| **状态管理** | SessionMemory 短期会话 + TaskMemory 蒸馏 |
| **容错机制** | DLQ 存储失败消息，支持重试 |
| **任务协调** | Phase 1 (并行) → Phase 2 (依赖感知) |
| **扩展性** | 可动态注册新的 Agent 类型 |
| **Agent 定义** | Markdown 文件配置，支持热加载 |
| **工作流编排** | YAML/JSON DSL，用户自定义流程 |
| **LLM 输出** | 四层保障机制确保输出一致性 |

---

## Agent 定义 (Markdown 配置)

Agent 采用 Markdown 文件定义，允许非开发人员通过编辑配置文件调整 Agent 行为。

```mermaid
graph TB
    subgraph AgentDef["Agent Definition (Markdown)"]
        Metadata["## Metadata<br/>name: agent_top<br/>version: 1.0.0"]
        Role["## Role<br/>你是一位专业的时尚穿搭顾问"]
        Profile["## Profile<br/>expertise: 上衣搭配<br/>category: tops<br/>style_tags: [casual, formal, street]"]
        Tools["## Tools<br/>- fashion_search<br/>- weather_check<br/>- style_recomm"]
        Instructions["## Instructions<br/>1. 根据用户风格偏好推荐合适的款式<br/>2. 考虑当地天气因素<br/>3. 匹配用户预算范围"]
        Output["## Output Format<br/>```json<br/>{ 'items': [...], 'reason': '...' }<br/>```"]
    end

    Metadata --> Role
    Role --> Profile
    Profile --> Tools
    Tools --> Instructions
    Instructions --> Output

    style AgentDef fill:#e1f5ff
```

### 内置变量

| 变量 | 说明 |
|------|------|
| {{.UserProfile}} | 用户画像 |
| {{.SessionID}} | 会话 ID |
| {{.Context}} | 上下文信息 |
| {{.Input}} | 用户输入 |
| {{.Results}} | 上游结果 |

---

## Workflow Engine (工作流编排)

用户可以通过 YAML/JSON 文件自定义工作流，实现灵活的 Agent 编排。

```mermaid
graph TB
    subgraph Workflow["Workflow Engine"]
        WorkflowName["name: 穿搭推荐流程"]

        subgraph Agents["Agents"]
            AgentLeader["id: leader<br/>type: leader<br/>prompt_file: ./agents/agent_leader.md"]
            AgentTop["id: agent_top<br/>type: sub<br/>prompt_file: ./agents/agent_top.md<br/>depends_on: [leader]"]
            AgentShoes["id: agent_shoes<br/>type: sub<br/>prompt_file: ./agents/agent_shoes.md<br/>depends_on: [leader, agent_top]"]
        end

        subgraph Execution["Execution"]
            Phase1["phase1: [agent_top, agent_bottom]"]
            Phase2["phase2: [agent_shoes]"]
        end
    end

    WorkflowName --> Agents
    AgentLeader --> AgentTop
    AgentTop --> AgentShoes
    Agents --> Execution
    Phase1 --> Phase2

    style Workflow fill:#fff4e1
    style Agents fill:#e8f5e9
    style Execution fill:#f3e5ff
```

### 目录结构

```mermaid
graph TB
    subgraph Workflows["workflows/"]
        Default[default.yaml<br/>默认工作流]
        Summer[summer.yaml<br/>夏季推荐]
        Winter[winter.yaml<br/>冬季推荐]

        subgraph AgentsDir["agents/"]
            AgentLeader[agent_leader.md]
            AgentTop[agent_top.md]
            AgentBottom[agent_bottom.md]
            AgentShoes[agent_shoes.md]
            AgentAccessory[agent_accessory.md]
        end

        subgraph Templates["templates/"]
            TemplatesDir[模板]
        end
    end

    style Workflows fill:#e1f5ff
    style AgentsDir fill:#fff4e1
    style Templates fill:#e8f5e9
```

---

## LLM Output 标准化

多 LLM 输出通过四层保障机制确保一致性。

```mermaid
graph TB
    subgraph Layer1["Layer 1: Prompt Template"]
        PromptTemplate["{{.Instructions}}<br/>Output Format:<br/>```json<br/>{ 'items': [...], 'reason': '...' }<br/>```"]
    end

    subgraph Layer2["Layer 2: JSON Schema / Tool Calling"]
        JSONSchema["{<br/>'type': 'object',<br/>'properties': {<br/>  'items': { 'type': 'array' },<br/>  'reason': { 'type': 'string' }<br/>}<br/>}"]
    end

    subgraph Layer3["Layer 3: Output Parser & Validator"]
        Parser["Parser (解析)<br/>- 提取 JSON<br/>- 修复破损 JSON"]
        Validator["Validator (校验)<br/>- Schema 验证<br/>- 自动重试"]
    end

    subgraph Layer4["Layer 4: LLM Adapter Layer"]
        AdapterOllama["Ollama<br/>Adapter"]
        AdapterOpenAI["OpenAI<br/>Adapter"]
        AdapterAnthropic["Anthropic<br/>Adapter"]
        Note["(统一抽象，上层不依赖具体模型)"]
    end

    PromptTemplate --> JSONSchema
    JSONSchema --> Parser
    Parser --> Validator
    Validator --> AdapterOllama
    Validator --> AdapterOpenAI
    Validator --> AdapterAnthropic
    AdapterOllama --> Note
    AdapterOpenAI --> Note
    AdapterAnthropic --> Note

    style Layer1 fill:#e1f5ff
    style Layer2 fill:#fff4e1
    style Layer3 fill:#e8f5e9
    style Layer4 fill:#f3e5ff
```

### 完整调用流程

```mermaid
graph TD
    Start["LLM 输出"]
    Parser["Parser 解析<br/>提取 JSON"]
    Fail1["失败"]
    FixJSON["Fix JSON<br/>修复破损"]
    Fail2["失败"]
    Validator["Validator 校验<br/>Schema 验证"]
    Fail3["失败"]
    Retry["Retry (3次)<br/>自动重试"]
    Result["返回结构化结果"]

    Start --> Parser
    Parser -->|失败| Fail1
    Fail1 --> FixJSON
    Parser -->|成功| Validator
    FixJSON -->|失败| Fail2
    FixJSON -->|成功| Validator
    Validator -->|失败| Fail3
    Fail3 --> Retry
    Retry --> Validator
    Validator -->|成功| Result

    style Start fill:#e1f5ff
    style Parser fill:#fff4e1
    style FixJSON fill:#fce4ec
    style Validator fill:#e8f5e9
    style Retry fill:#ffebee
    style Result fill:#c8e6c9
```

---

## 目录结构

```
goagent/
├── internal/                # 核心实现
│   ├── agents/              # Agent 系统
│   │   ├── base/            # Agent 基础接口
│   │   ├── leader/          # Leader Agent
│   │   └── sub/             # Sub Agent
│   ├── protocol/            # AHP 协议
│   │   └── ahp/             # 协议实现
│   ├── storage/             # 存储层
│   │   └── postgres/        # PostgreSQL + pgvector
│   │       ├── pool.go      # 连接池
│   │       ├── repositories/ # 数据仓库
│   │       └── migrations/   # 数据库迁移
│   ├── memory/              # 记忆系统
│   │   └── production_manager.go
│   ├── llm/                 # LLM 客户端
│   │   └── client.go
│   ├── tools/               # 工具系统
│   │   └── resources/
│   ├── core/                # 核心类型
│   │   ├── errors/          # 错误定义
│   │   └── types.go
│   ├── config/              # 配置管理
│   ├── workflow/            # 工作流引擎
│   ├── ratelimit/           # 限流
│   ├── shutdown/            # 优雅退出
│   └── observability/       # 可观测性
├── api/                     # API 层
│   ├── service/             # 服务接口
│   │   ├── agent/           # Agent 服务
│   │   ├── llm/             # LLM 服务
│   │   ├── memory/          # 记忆服务
│   │   └── retrieval/       # 检索服务
│   └── client/              # 客户端
├── examples/                # 示例应用
│   ├── travel/              # 旅行规划
│   ├── knowledge-base/      # 知识库问答
│   ├── simple/              # 简单示例
│   └── capability-demo/     # 功能演示
├── services/                # 独立服务
│   └── embedding/           # Embedding 服务
│       ├── app.py           # FastAPI 服务
│       └── config.py
├── cmd/                     # 命令行工具
│   └── server/              # 服务器启动
├── docs/                    # 文档
└── configs/                 # 配置文件
```

**代码位置**: 项目根目录

---

## 技术栈

| 层级 | 技术选型 | 代码位置 |
|------|----------|----------|
| 语言 | Go 1.21+ | - |
| LLM | Ollama / OpenRouter | `internal/llm/client.go` |
| 协议 | AHP (Agent Handshake Protocol) | `internal/protocol/ahp/` |
| 存储 | PostgreSQL 15+ with pgvector | `internal/storage/postgres/` |
| 并发 | errgroup, sync | - |
| 工具 | 内置工具 | `internal/tools/` |
| Embedding | FastAPI + Ollama/SentenceTransformers | `services/embedding/` |

---

## 消息格式 (AHP Protocol)

**代码位置**: `internal/protocol/ahp/message.go`

```go
type Message struct {
    MessageID  string    `json:"message_id"`
    Method     Method    `json:"method"`     // TASK, RESULT, PROGRESS, ACK
    AgentID    string    `json:"agent_id"`
    TargetID   string    `json:"target_id"`
    TaskID     string    `json:"task_id"`
    SessionID  string    `json:"session_id"`
    Payload    []byte    `json:"payload"`
    Timestamp  time.Time `json:"timestamp"`
}

type Method string

const (
    MethodTask     Method = "TASK"
    MethodResult   Method = "RESULT"
    MethodProgress Method = "PROGRESS"
    MethodAck      Method = "ACK"
)
```

---

## 错误码体系

### 错误码规范

```
格式: XX-YYY-ZZZ
  - XX:   模块代码 (01-Agent, 02-Protocol, 03-Storage, 04-LLM, 05-Tools)
  - YYY:  错误类型 (001-099 系统级, 100-199 业务级)
  - ZZZ:  具体错误序号
```

### 错误码表

| 错误码 | 名称 | 说明 | 可重试 | 最大重试次数 |
|--------|------|------|--------|--------------|
| **01-Agent** |
| 01-001 | AgentNotFound | Agent 未注册 | 否 | 0 |
| 01-002 | AgentTimeout | Agent 执行超时 | 是 | 3 |
| 01-003 | AgentPanic | Agent 内部 panic | 是 | 2 |
| 01-004 | TaskQueueFull | 任务队列满 | 是 | 5 |
| 01-005 | DependencyCycle | 任务依赖循环 | 否 | 0 |
| **02-Protocol** |
| 02-001 | InvalidMessage | 消息格式错误 | 否 | 0 |
| 02-002 | MessageTimeout | 消息发送超时 | 是 | 3 |
| 02-003 | HeartbeatMissed | 心跳丢失 | 是 | 5 |
| **03-Storage** |
| 03-001 | DBConnectionFailed | 数据库连接失败 | 是 | 3 |
| 03-002 | QueryFailed | 查询失败 | 是 | 2 |
| 03-003 | VectorSearchFailed | 向量搜索失败 | 是 | 2 |
| **04-LLM** |
| 04-001 | LLMRequestFailed | LLM 请求失败 | 是 | 3 |
| 04-002 | LLMTimeout | LLM 响应超时 | 是 | 2 |
| 04-003 | LLMQuotaExceeded | 配额超限 | 否 | 0 |

### 统一错误处理

```go
type ErrorCode struct {
    Code       string                 `json:"code"`
    Message    string                 `json:"message"`
    Module     string                 `json:"module"`
    Retry      bool                   `json:"retry"`
    RetryMax   int                    `json:"retry_max"`
    Backoff    time.Duration          `json:"backoff"`
}

type AppError struct {
    Code    ErrorCode
    Err     error
    Stack   string
    Context map[string]interface{}
}
```

---

## 优雅 Shutdown 流程

```mermaid
graph TB
    Signal["SIGTERM / SIGINT"]
    Phase1["Phase 1: Stop Accept<br/>停止接收新请求/任务"]
    Wait1["等待 30s<br/>grace period"]
    Phase2["Phase 2: Cancel<br/>取消所有 Agent 上下文"]
    Wait2["等待 60s<br/>shutdown period"]
    Phase3["Phase 3: Drain Queues<br/>处理完积压消息"]
    Phase4["Phase 4: Save State<br/>落盘内存状态"]
    Phase5["Phase 5: Close<br/>关闭 DB/连接池"]
    Exit["EXIT 0"]

    Signal --> Phase1
    Phase1 --> Wait1
    Wait1 --> Phase2
    Phase2 --> Wait2
    Wait2 --> Phase3
    Phase3 --> Phase4
    Phase4 --> Phase5
    Phase5 --> Exit

    style Signal fill:#e1f5ff
    style Phase1 fill:#fff4e1
    style Phase2 fill:#fce4ec
    style Phase3 fill:#e8f5e9
    style Phase4 fill:#f3e5ff
    style Phase5 fill:#ffebee
    style Exit fill:#c8e6c9
```

### Shutdown 阶段状态

| 阶段 | 状态码 | 说明 |
|------|--------|------|
| Running | 0 | 正常运行 |
| Stopping | 1 | 停止接收新请求 |
| Draining | 2 | 处理积压任务 |
| Exiting | 3 | 清理退出 |

### 实现要点

- **Grace Period**: 30秒，允许新请求完成当前处理
- **Shutdown Period**: 60秒，等待队列清空
- **Force Timeout**: 超过总时间后强制退出
- **Signal 捕获**: 同时支持 SIGTERM（推荐）和 SIGINT

---

## 限流与背压机制

### 限流策略

| 场景 | 限流方式 | 阈值 |
|------|----------|------|
| Agent 并发数 | 信号量 (Semaphore) | 每 Agent 最大 10 并发 |
| 任务队列 | 队列长度限制 | 单队列最大 1000 条 |
| LLM 请求 | 令牌桶 (Token Bucket) | 每秒 10 请求 |
| 全局 QPS | 滑动窗口 | 系统最大 100 QPS |

---

## 数据库连接池设计

采用"谁用谁连接，用完释放"的原则，避免长时间占用数据库连接资源。

### 传统模式 vs 连接池模式

```mermaid
graph TB
    subgraph Traditional["传统模式 (长连接)"]
        Agent1["Agent 1<br/>占用"]
        DB1["DB<br/>长连接"]
        Agent2["Agent 2<br/>占用"]
        Agent3["Agent 3<br/>等待..."]
        Waste["连接一直保持<br/>资源浪费"]
    end

    subgraph PoolMode["连接池模式 (用完释放)"]
        Agent1P["Agent 1<br/>获取连接<br/>使用归还"]
        Pool["Connection<br/>Pool"]
        Agent2P["Agent 2<br/>获取连接<br/>使用归还"]
        Agent3P["Agent 3<br/>立即获取"]
        Efficient["按需获取<br/>资源高效利用"]
    end

    Agent1 --> DB1
    Agent2 --> DB1
    DB1 --> Waste
    Agent3 -.等待.-> DB1

    Agent1P --> Pool
    Pool --> Agent2P
    Pool --> Agent3P
    Pool --> Efficient

    style Traditional fill:#ffebee
    style PoolMode fill:#e8f5e9
```

### 连接池设计

```go
// 连接池管理器
type ConnectionPool struct {
    maxOpen    int           // 最大打开连接数
    maxIdle    int           // 最大空闲连接数
    maxLifetime time.Duration // 连接最大生命周期
    
    mu         sync.Mutex
    openCount  int           // 当前打开的连接数
    idleCount  int           // 当前空闲的连接数
    connections chan *DBConn  // 连接池队列
}

// 获取连接
func (p *ConnectionPool) Get(ctx context.Context) (*DBConn, error) {
    select {
    case conn := <-p.connections:
        // 从池中获取空闲连接
        if conn.IsValid() {
            return conn, nil
        }
        // 连接已过期，重新创建
        return p.createConn()
        
    case <-ctx.Done():
        return nil, ctx.Err()
        
    default:
        // 池中没有空闲连接
        if p.openCount >= p.maxOpen {
            // 达到最大连接数，等待
            return p.waitForConnection(ctx)
        }
        // 创建新连接
        return p.createConn()
    }
}

// 归还连接
func (p *ConnectionPool) Put(conn *DBConn) error {
    if !conn.IsValid() {
        // 连接已失效，关闭
        conn.Close()
        p.mu.Lock()
        p.openCount--
        p.mu.Unlock()
        return nil
    }
    
    // 放回池中
    select {
    case p.connections <- conn:
        return nil
    default:
        // 池已满，关闭连接
        conn.Close()
        p.mu.Lock()
        p.openCount--
        p.mu.Unlock()
        return nil
    }
}
```

### Agent 使用模式

```go
// Agent 中使用连接池
func (a *SubAgent) ExecuteTask(ctx context.Context, task *Task) (*TaskResult, error) {
    // 从池中获取连接
    conn, err := pool.Get(ctx)
    if err != nil {
        return nil, err
    }
    defer pool.Put(conn)  // 用完归还
    
    // 使用连接执行操作
    result, err := a.doQuery(ctx, conn, task)
    if err != nil {
        return nil, err
    }
    
    return result, nil
}
```

### 连接池配置

| 参数 | 默认值 | 说明 |
|------|--------|------|
| max_open | 25 | 最大打开连接数 |
| max_idle | 10 | 最大空闲连接数 |
| conn_max_lifetime | 5m | 连接最大生命周期 |
| conn_max_idle_time | 1m | 空闲连接最大存活时间 |
| max_wait_time | 30s | 获取连接最大等待时间 |

### 监控指标

| 指标 | 说明 | 告警阈值 |
|------|------|----------|
| db_open_connections | 当前打开的连接数 | > 20 |
| db_idle_connections | 当前空闲的连接数 | < 2 |
| db_wait_count | 等待连接次数 | > 100 |
| db_wait_duration | 等待连接总时长 | > 1s |

### 背压机制

```mermaid
graph LR
    subgraph Entry["请求入口"]
        Request["Request"]
    end

    subgraph Queue["队列层"]
        QueueSize["Queue<br/>500/1000"]
        Backpressure["触发背压<br/>拒绝/排队<br/>429 Too Many<br/>Retry-After"]
    end

    subgraph Agent["Agent 层"]
        AgentPool["Agent Pool<br/>处理中"]
    end

    Request -->|超过阈值| QueueSize
    QueueSize --> Backpressure
    Backpressure --> Request
    QueueSize --> AgentPool

    style Entry fill:#e1f5ff
    style Queue fill:#fff4e1
    style Agent fill:#e8f5e9
    style Backpressure fill:#ffebee
```

**背压策略:**
1. 队列 80% → 告警通知
2. 队列 90% → 拒绝新任务 (429)
3. 队列 100% → 触发 DLQ

### 实现方案

```go
// 令牌桶限流
type TokenBucket struct {
    rate       float64       // 每秒令牌数
    capacity   int           // 桶容量
    tokens     float64
    lastUpdate time.Time
    mu         sync.Mutex
}

// 背压控制
type Backpressure struct {
    queueLimit    int           // 队列上限
    currentLoad   atomic.Int32   // 当前负载
    rejectionRate float64       // 拒绝率阈值
    
    // 响应头
    RetryAfter   time.Duration  // 建议重试时间
    RetryCount   int            // 已重试次数
}

// 限流策略选择
var LimiterStrategy = map[string]Limiter{
    "llm":    NewTokenBucket(10, 50),   // LLM 请求
    "agent":  NewSemaphore(10),          // Agent 并发
    "global": NewSlidingWindow(100),    // 全局 QPS
}
```

### 监控指标

| 指标 | 说明 | 告警阈值 |
|------|------|----------|
| queue_depth | 队列深度 | > 800 |
| rejection_rate | 拒绝率 | > 5% |
| latency_p99 | 延迟 P99 | > 5s |
| active_agents | 活跃 Agent 数 | < 50% 利用率 |

---

## 生产环境补充建议

### 可观测性

- **日志**: 结构化 JSON 日志，分级输出 (DEBUG/INFO/WARN/ERROR)
- **指标**: Prometheus + Grafana 面板
- **链路**: OpenTelemetry 分布式追踪

### 扩展性预留

- 消息队列支持 Redis Stream 替换
- 存储层支持多数据源切换
- Agent 支持动态注册与发现

---

