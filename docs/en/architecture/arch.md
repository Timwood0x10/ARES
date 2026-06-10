# GoAgent Architecture Design

**Last Updated**: 2026-03-24

## System Architecture Overview

```mermaid
graph TB
    %% User Input
    UserInput[User Input<br/>Help me plan a trip...] --> LeaderAgent

    %% Leader Agent
    subgraph LeaderAgent["Leader Agent"]
        ParseInput[Parse Input<br/>LLM]
        TaskPlan[Task Plan<br/>LLM]
        Aggregate[Aggregate<br/>Results]
        ParseInput --> TaskPlan
        TaskPlan --> Aggregate
    end

    LeaderAgent -->|dispatch tasks parallel| SubAgents

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
        Session[Session<br/>Short-term]
        UserMemory[User Memory<br/>Long-term]
        TaskMemory[Task Memory<br/>Distilled]
        ExperienceSystem[Experience System]
        ProdMgr[ProductionMemoryManager<br/>PG + pgvector persistence]

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

**Code Locations**:
- Leader Agent: `internal/agents/leader/agent.go`
- Sub Agent: `internal/agents/sub/agent.go`
- Protocol: `internal/protocol/ahp/`
- LLM Client: `internal/llm/client.go`
- Storage Pool: `internal/storage/postgres/pool.go`
- Memory Manager: `internal/memory/production_manager.go`
- Experience Distillation: `api/experience/`
- Experience Repository: `internal/storage/postgres/repositories/`

---

## Core Component Implementation

### Leader Agent

Leader Agent is responsible for task decomposition, distribution, and result aggregation.

**Code Location**: `internal/agents/leader/agent.go`

```go
type LeaderAgent struct {
    id               string
    maxSteps         int
    maxParallelTasks int
    subAgents        map[string]*SubAgent
    llmClient        *llm.Client
}

func (l *LeaderAgent) Process(ctx context.Context, input string) (string, error) {
    // 1. Parse user input
    parsed, err := l.parseInput(ctx, input)
    if err != nil {
        return "", err
    }

    // 2. Generate task plan
    tasks, err := l.planTasks(ctx, parsed)
    if err != nil {
        return "", err
    }

    // 3. Execute tasks in parallel
    results, err := l.executeTasks(ctx, tasks)
    if err != nil {
        return "", err
    }

    // 4. Aggregate results
    return l.aggregateResults(ctx, results)
}
```

### Sub Agent

Sub Agent is responsible for executing specific tasks.

**Code Location**: `internal/agents/sub/agent.go`

```go
type SubAgent struct {
    id        string
    agentType string
    triggers  []string
    llmClient *llm.Client
    tools     []Tool
}

func (s *SubAgent) Execute(ctx context.Context, task *Task) (*TaskResult, error) {
    // 1. Check trigger conditions
    if !s.shouldExecute(task) {
        return nil, nil
    }

    // 2. Execute tools
    toolResults, err := s.executeTools(ctx, task)
    if err != nil {
        return nil, err
    }

    // 3. LLM generates response
    response, err := s.llmClient.Generate(ctx, s.buildPrompt(task, toolResults))
    if err != nil {
        return nil, err
    }

    return &TaskResult{
        AgentID: s.id,
        Result:  response,
    }, nil
}
```

### LLM Client

Unified client supporting multiple LLM providers.

**Code Location**: `internal/llm/client.go`

```go
type Client struct {
    config     *Config
    httpClient *http.Client
}

func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
    switch ProviderType(c.config.Provider) {
    case ProviderOpenRouter:
        return c.generateOpenRouter(ctx, prompt)
    case ProviderOllama:
        return c.generateOllama(ctx, prompt)
    default:
        return "", fmt.Errorf("unsupported provider: %s", c.config.Provider)
    }
}
```

### Storage Pool

PostgreSQL connection pool implementing "Get-Use-Release" pattern.

**Code Location**: `internal/storage/postgres/pool.go`

```go
type Pool struct {
    cfg          *Config
    db           *sql.DB
    mu           sync.RWMutex
    openCount    int
    idleCount    int
    waitCount    int
    waitDuration time.Duration
}

func (p *Pool) WithConnection(ctx context.Context, fn func(*sql.Conn) error) error {
    conn, err := p.Get(ctx)
    if err != nil {
        return err
    }
    defer p.Release(conn)

    return fn(conn)
}
```

---

## Tech Stack

| Layer | Tech Stack | Code Location |
|-------|-----------|---------------|
| Language | Go 1.21+ | - |
| LLM | Ollama / OpenRouter | `internal/llm/client.go` |
| Protocol | AHP (Agent Handshake Protocol) | `internal/protocol/ahp/` |
| Storage | PostgreSQL 15+ with pgvector | `internal/storage/postgres/` |
| Concurrency | errgroup, sync | - |
| Tools | Built-in tools | `internal/tools/` |
| Embedding | FastAPI + Ollama/SentenceTransformers | `services/embedding/` |

---

## Message Format (AHP Protocol)

**Code Location**: `internal/protocol/ahp/message.go`

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

## Directory Structure

```
goagent/
├── internal/                # Core implementation
│   ├── agents/              # Agent system
│   │   ├── base/            # Agent base interfaces
│   │   ├── leader/          # Leader Agent
│   │   └── sub/             # Sub Agent
│   ├── protocol/            # AHP protocol
│   │   └── ahp/             # Protocol implementation
│   ├── storage/             # Storage layer
│   │   └── postgres/        # PostgreSQL + pgvector
│   │       ├── pool.go      # Connection pool
│   │       ├── repositories/ # Data repositories
│   │       └── migrations/   # Database migrations
│   ├── memory/              # Memory system
│   │   └── production_manager.go
│   ├── llm/                 # LLM client
│   │   └── client.go
│   ├── tools/               # Tool system
│   │   └── resources/
│   ├── core/                # Core types
│   │   ├── errors/          # Error definitions
│   │   └── types.go
│   ├── config/              # Configuration management
│   ├── workflow/            # Workflow engine
│   ├── ratelimit/           # Rate limiting
│   ├── shutdown/            # Graceful shutdown
│   └── observability/       # Observability
├── api/                     # API layer
│   ├── service/             # Service interfaces
│   │   ├── agent/           # Agent service
│   │   ├── llm/             # LLM service
│   │   ├── memory/          # Memory service
│   │   └── retrieval/       # Retrieval service
│   └── client/              # Client
├── examples/                # Example applications
│   ├── travel/              # Travel planning
│   ├── knowledge-base/      # Knowledge base Q&A
│   ├── simple/              # Simple example
│   └── capability-demo/     # Feature demonstration
├── services/                # Standalone services
│   └── embedding/           # Embedding service
│       ├── app.py           # FastAPI service
│       └── config.py
├── cmd/                     # Command line tools
│   └── server/              # Server startup
├── docs/                    # Documentation
└── configs/                 # Configuration files
```

**Code Location**: Project root directory

---

## Key Design Points

| Feature | Implementation |
|---------|----------------|
| **Concurrency Model** | Worker pool dispatches tasks to multiple Sub Agents |
| **Communication Protocol** | In-Memory Message Queue + AHP custom protocol |
| **State Management** | SessionMemory short-term + TaskMemory distillation |
| **Fault Tolerance** | DLQ stores failed messages, supports retry |
| **Task Coordination** | Phase 1 (parallel) → Phase 2 (dependency-aware) |
| **Scalability** | Dynamic registration of new Agent types |
| **Agent Definition** | Markdown file configuration, supports hot reload |
| **Workflow Orchestration** | YAML/JSON DSL, user-defined workflows |
| **LLM Output** | Four-layer guarantee mechanism ensures output consistency |

---

## Agent Definition (Markdown Configuration)

Agents are defined using Markdown files, allowing non-developers to adjust Agent behavior by editing configuration files.

```mermaid
graph TB
    subgraph AgentDef["Agent Definition (Markdown)"]
        Metadata["## Metadata<br/>name: agent_top<br/>version: 1.0.0"]
        Role["## Role<br/>You are a professional fashion consultant"]
        Profile["## Profile<br/>expertise: top wear<br/>category: tops<br/>style_tags: [casual, formal, street]"]
        Tools["## Tools<br/>- fashion_search<br/>- weather_check<br/>- style_recomm"]
        Instructions["## Instructions<br/>1. Recommend suitable styles based on user preferences<br/>2. Consider local weather conditions<br/>3. Match user budget range"]
        Output["## Output Format<br/>```json<br/>{ 'items': [...], 'reason': '...' }<br/>```"]
    end

    Metadata --> Role
    Role --> Profile
    Profile --> Tools
    Tools --> Instructions
    Instructions --> Output

    style AgentDef fill:#e1f5ff
```

### Built-in Variables

| Variable | Description |
|----------|-------------|
| {{.UserProfile}} | User profile |
| {{.SessionID}} | Session ID |
| {{.Context}} | Context information |
| {{.Input}} | User input |
| {{.Results}} | Upstream results |

---

## Workflow Engine (Workflow Orchestration)

Users can customize workflows through YAML/JSON files to achieve flexible Agent orchestration.

```mermaid
graph TB
    subgraph Workflow["Workflow Engine"]
        WorkflowName["name: Outfit Recommendation Flow"]

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

```mermaid
graph TB
    subgraph Workflow["Workflow Engine"]
        WorkflowName["name: Travel Planning Workflow"]

        subgraph Agents["Agents"]
            AgentLeader["id: leader<br/>type: leader<br/>prompt_file: ./agents/agent_leader.md"]
            AgentDest["id: agent_destination<br/>type: sub<br/>prompt_file: ./agents/agent_destination.md<br/>depends_on: [leader]"]
            AgentHotel["id: agent_hotel<br/>type: sub<br/>prompt_file: ./agents/agent_hotel.md<br/>depends_on: [leader, agent_destination]"]
        end

        subgraph Execution["Execution"]
            Phase1["phase1: [agent_destination, agent_food]"]
            Phase2["phase2: [agent_hotel]"]
        end
    end

    WorkflowName --> Agents
    AgentLeader --> AgentDest
    AgentDest --> AgentHotel
    Agents --> Execution
    Phase1 --> Phase2

    style Workflow fill:#fff4e1
    style Agents fill:#e8f5e9
    style Execution fill:#f3e5ff
```

### Directory Structure

```mermaid
graph TB
    subgraph Workflows["workflows/"]
        Default[default.yaml<br/>Default workflow]
        Summer[summer.yaml<br/>Summer recommendation]
        Winter[winter.yaml<br/>Winter recommendation]

        subgraph AgentsDir["agents/"]
            AgentLeader[agent_leader.md]
            AgentDest[agent_destination.md]
            AgentFood[agent_food.md]
            AgentHotel[agent_hotel.md]
            AgentAccessory[agent_accessory.md]
        end

        subgraph Templates["templates/"]
            TemplatesDir[Templates]
        end
    end

    style Workflows fill:#e1f5ff
    style AgentsDir fill:#fff4e1
    style Templates fill:#e8f5e9
```

---

## LLM Output Standardization

Multi-LLM output is ensured through a four-layer guarantee mechanism.

```mermaid
graph TB
    subgraph Layer1["Layer 1: Prompt Template"]
        PromptTemplate["{{.Instructions}}<br/>Output Format:<br/>```json<br/>{ 'items': [...], 'reason': '...' }<br/>```"]
    end

    subgraph Layer2["Layer 2: JSON Schema / Tool Calling"]
        JSONSchema["{<br/>'type': 'object',<br/>'properties': {<br/>  'items': { 'type': 'array' },<br/>  'reason': { 'type': 'string' }<br/>}<br/>}"]
    end

    subgraph Layer3["Layer 3: Output Parser & Validator"]
        Parser["Parser (Parse)<br/>- Extract JSON<br/>- Fix broken JSON"]
        Validator["Validator (Validate)<br/>- Schema validation<br/>- Auto retry"]
    end

    subgraph Layer4["Layer 4: LLM Adapter Layer"]
        AdapterOllama["Ollama<br/>Adapter"]
        AdapterOpenAI["OpenAI<br/>Adapter"]
        AdapterAnthropic["Anthropic<br/>Adapter"]
        Note["(Unified abstraction, upper layer doesn't depend on specific model)"]
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

### Complete Call Flow

```mermaid
graph TD
    Start["LLM Output"]
    Parser["Parser Parse<br/>Extract JSON"]
    Fail1["Failed"]
    FixJSON["Fix JSON<br/>Fix broken JSON"]
    Fail2["Failed"]
    Validator["Validator Validate<br/>Schema validation"]
    Fail3["Failed"]
    Retry["Retry (3 times)<br/>Auto retry"]
    Result["Return structured result"]

    Start --> Parser
    Parser -->|Failed| Fail1
    Fail1 --> FixJSON
    Parser -->|Success| Validator
    FixJSON -->|Failed| Fail2
    FixJSON -->|Success| Validator
    Validator -->|Failed| Fail3
    Fail3 --> Retry
    Retry --> Validator
    Validator -->|Success| Result

    style Start fill:#e1f5ff
    style Parser fill:#fff4e1
    style FixJSON fill:#fce4ec
    style Validator fill:#e8f5e9
    style Retry fill:#ffebee
    style Result fill:#c8e6c9
```

---

## Message Flow Mechanism

```mermaid
sequenceDiagram
    participant Leader as Leader Agent
    participant MQ as Message Queue
    participant Sub as Sub Agent

    Note over Leader,Sub: Send
    Leader->>MQ: TASK
    MQ->>Sub: TASK

    Note over Sub,Leader: Receive/Process
    Sub->>Leader: RESULT
    Leader->>Sub: ACK
```

```mermaid
graph LR
    subgraph MessageTypes["Message Types"]
        TASK[TASK<br/>Dispatch Task<br/>Leader → Sub Agent]
        RESULT[RESULT<br/>Return Result<br/>Sub Agent → Leader]
        PROGRESS[PROGRESS<br/>Progress Report<br/>Sub Agent → Leader]
        ACK[ACK<br/>Acknowledge<br/>Sub Agent → Leader]
        HEARTBEAT[HEARTBEAT<br/>Heartbeat<br/>All Agents]
    end

    style TASK fill:#e1f5ff
    style RESULT fill:#fff4e1
    style PROGRESS fill:#e8f5e9
    style ACK fill:#f3e5ff
    style HEARTBEAT fill:#ffebee
```

---

## Task Production-Consumption Flow

```mermaid
graph TB
    subgraph Producer["Leader Agent (Producer)"]
        Step1["1. Parse User Input"]
        ParseProfile[Parse Profile<br/>UserProfile]
        Step2["2. LLM Decides Which Agents Needed"]
        DetermineTasks[Determine Tasks<br/>destination, hotel]
    end

    subgraph TaskQueues["Task Queues"]
        QueueDest[Task Queue<br/>agent_destination]
        QueueFood[Task Queue<br/>agent_food]
        QueueOther[Task Queue<br/>...]
    end

    subgraph Phase1["Phase 1: Parallel Dispatch<br/>ThreadPoolExecutor"]
        SubAgent1[Sub Agent<br/>Consumer]
        SubAgent2[Sub Agent<br/>Consumer]
    end

    subgraph TaskExecution["3. Execute Task"]
        ExecuteTask[Execute Task]
        Tools[Tools<br/>RAG]
        LLM[LLM]
    end

    subgraph Result["4. Return Result"]
        SendResult[Send RESULT<br/>to Leader]
    end

    subgraph Phase2["Phase 2: Dependency-Aware Tasks<br/>destination result to hotel"]
        Coordination[Coordination<br/>Context]
    end

    subgraph Aggregator["Leader (Aggregator)"]
        Step5["5. Aggregate All Results"]
        Aggregate[Aggregate<br/>destination+food+hotel]
        FinalOutput[Final Output<br/>Save to DB]
    end

    Step1 --> ParseProfile
    ParseProfile --> Step2
    Step2 --> DetermineTasks
    DetermineTasks --> QueueDest
    DetermineTasks --> QueueFood
    DetermineTasks --> QueueOther

    QueueDest --> Phase1
    QueueFood --> Phase1
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

## Actor Model Mapping

| Actor Model Concept |  Implementation |
|---------------------|----------------------|
| Actor | `LeaderAgent`, `OutfitSubAgent` |
| Mailbox | `MessageQueue` (In-Memory) |
| Message | `AHPMessage` (TASK/RESULT/PROGRESS/ACK) |
| Behavior | Agent internal `_handle_task()`, `_recommend()` |
| Supervisor | `LeaderAgent` coordinates multiple Sub Agents |
| Failure Handling | DLQ (Dead Letter Queue) |

---

## Error Code System

### Error Code Specification

```
Format: XX-YYY-ZZZ
  - XX:   Module code (01-Agent, 02-Protocol, 03-Storage, 04-LLM, 05-Tools)
  - YYY:  Error type (001-099 system level, 100-199 business level)
  - ZZZ:  Specific error sequence number
```

### Error Code Table

| Error Code | Name | Description | Retriable | Max Retries |
|------------|------|-------------|-----------|-------------|
| **01-Agent** |
| 01-001 | AgentNotFound | Agent not registered | No | 0 |
| 01-002 | AgentTimeout | Agent execution timeout | Yes | 3 |
| 01-003 | AgentPanic | Agent internal panic | Yes | 2 |
| 01-004 | TaskQueueFull | Task queue full | Yes | 5 |
| 01-005 | DependencyCycle | Task dependency cycle | No | 0 |
| **02-Protocol** |
| 02-001 | InvalidMessage | Invalid message format | No | 0 |
| 02-002 | MessageTimeout | Message send timeout | Yes | 3 |
| 02-003 | HeartbeatMissed | Heartbeat missed | Yes | 5 |
| **03-Storage** |
| 03-001 | DBConnectionFailed | Database connection failed | Yes | 3 |
| 03-002 | QueryFailed | Query failed | Yes | 2 |
| 03-003 | VectorSearchFailed | Vector search failed | Yes | 2 |
| **04-LLM** |
| 04-001 | LLMRequestFailed | LLM request failed | Yes | 3 |
| 04-002 | LLMTimeout | LLM response timeout | Yes | 2 |
| 04-003 | LLMQuotaExceeded | Quota exceeded | No | 0 |

### Unified Error Handling

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

## Graceful Shutdown Flow

```mermaid
graph TB
    Signal["SIGTERM / SIGINT"]
    Phase1["Phase 1: Stop Accept<br/>Stop accepting new requests/tasks"]
    Wait1["Wait 30s<br/>grace period"]
    Phase2["Phase 2: Cancel<br/>Cancel all Agent contexts"]
    Wait2["Wait 60s<br/>shutdown period"]
    Phase3["Phase 3: Drain Queues<br/>Process pending messages"]
    Phase4["Phase 4: Save State<br/>Persist in-memory state"]
    Phase5["Phase 5: Close<br/>Close DB/connection pools"]
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

---

## Rate Limiting & Backpressure Mechanism

### Rate Limiting Strategy

| Scenario | Rate Limiting Method | Threshold |
|----------|---------------------|-----------|
| Agent Concurrency | Semaphore | Max 10 concurrent per Agent |
| Task Queue | Queue length limit | Max 1000 items per queue |
| LLM Requests | Token Bucket | 10 requests per second |
| Global QPS | Sliding Window | System max 100 QPS |

---

## Database Connection Pool Design

Adopting "get-use-release" principle to avoid long-term occupation of database connection resources.

### Traditional Mode vs Connection Pool Mode

```mermaid
graph TB
    subgraph Traditional["Traditional Mode (Long Connection)"]
        Agent1["Agent 1<br/>Occupied"]
        DB1["DB<br/>Long Connection"]
        Agent2["Agent 2<br/>Occupied"]
        Agent3["Agent 3<br/>Waiting..."]
        Waste["Connection always maintained<br/>Resource waste"]
    end

    subgraph PoolMode["Connection Pool Mode (Use & Release)"]
        Agent1P["Agent 1<br/>Get connection<br/>Use & return"]
        Pool["Connection<br/>Pool"]
        Agent2P["Agent 2<br/>Get connection<br/>Use & return"]
        Agent3P["Agent 3<br/>Immediate acquire"]
        Efficient["Get on demand<br/>Efficient resource utilization"]
    end

    Agent1 --> DB1
    Agent2 --> DB1
    DB1 --> Waste
    Agent3 -.waiting.-> DB1

    Agent1P --> Pool
    Pool --> Agent2P
    Pool --> Agent3P
    Pool --> Efficient

    style Traditional fill:#ffebee
    style PoolMode fill:#e8f5e9
```

### Connection Pool Configuration

| Parameter | Default Value | Description |
|-----------|---------------|-------------|
| max_open | 25 | Maximum open connections |
| max_idle | 10 | Maximum idle connections |
| conn_max_lifetime | 5m | Connection maximum lifetime |
| conn_max_idle_time | 1m | Idle connection maximum survival time |
| max_wait_time | 30s | Maximum wait time to get connection |

### Monitoring Metrics

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| db_open_connections | Current open connections | > 20 |
| db_idle_connections | Current idle connections | < 2 |
| db_wait_count | Wait connection count | > 100 |
| db_wait_duration | Total wait duration | > 1s |

### Backpressure Mechanism

```mermaid
graph LR
    subgraph Entry["Request Entry"]
        Request["Request"]
    end

    subgraph Queue["Queue Layer"]
        QueueSize["Queue<br/>500/1000"]
        Backpressure["Trigger backpressure<br/>Reject/Queue<br/>429 Too Many<br/>Retry-After"]
    end

    subgraph Agent["Agent Layer"]
        AgentPool["Agent Pool<br/>Processing"]
    end

    Request -->|Exceed threshold| QueueSize
    QueueSize --> Backpressure
    Backpressure --> Request
    QueueSize --> AgentPool

    style Entry fill:#e1f5ff
    style Queue fill:#fff4e1
    style Agent fill:#e8f5e9
    style Backpressure fill:#ffebee
```

**Backpressure Strategy:**
1. Queue 80% → Alert notification
2. Queue 90% → Reject new tasks (429)
3. Queue 100% → Trigger DLQ

---

## Production Environment Recommendations

### Observability

- **Logging**: Structured JSON logging, hierarchical output (DEBUG/INFO/WARN/ERROR)
- **Metrics**: Prometheus + Grafana dashboards
- **Tracing**: Distributed tracing for request flow
- **Alerting**: Multi-level alerting strategy

### Scalability Reservations

- Horizontal scaling support for Agent instances
- Database read-write separation
- Cache layer (Redis) for hot data
- CDN acceleration for static resources

---

**Version**: 1.0  
**Last Updated**: 2026-03-25  
**Maintainer**: GoAgent Team