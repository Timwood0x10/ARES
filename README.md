# ares

```shell
           _____  ______  _____ 
     /\   |  __ \|  ____|/ ____|
    /  \  | |__) | |__  | (___  
   / /\ \ |  _  /|  __|  \___ \ 
  / ____ \| | \ \| |____ ____) |
 /_/    \_\_|  \_\______|_____/ 

```

ARES(Adaptive Resilient Evolution System)  A Self-Healing Evolutionary Runtime for Autonomous Agents

Go-based multi-agent framework with DAG workflow orchestration, memory distillation, and AHP inter-agent protocol.

## Architecture 

```mermaid
graph TB
    User["User Request"] --> RT

    subgraph runtime ["Runtime Layer"]
        RT["Runtime Manager"]
        RT -->|"manages lifecycle"| Leader
        RT -->|"replays"| ES["EventStore"]
        RT -->|"restores"| MM["MemoryStore"]
    end

    subgraph agents ["Agent System"]
        direction TB
        Leader["Leader Agent"]
        SubA["Sub Agent A"]
        SubB["Sub Agent B"]
        SubC["Sub Agent C"]
        Leader -->|"AHP Protocol"| SubA
        Leader -->|"AHP Protocol"| SubB
        Leader -->|"AHP Protocol"| SubC
        Supervisor["Supervisor"]
        Leader -.->|"Checkpoint Recovery"| Supervisor
    end

    subgraph workflow ["Workflow Engine"]
        MutableDAG["MutableDAG"]
        DynamicExec["DynamicExecutor"]
        MutableDAG --> DynamicExec
        DynamicExec --> TopoSort["Topological Sort"]
        DynamicExec --> CycleDetect["Cycle Detection"]
    end

    subgraph llm ["LLM Layer"]
        Adapters["Output Adapters"]
        Templates["Prompt Templates"]
        Parser["Function Call Parser"]
    end

    subgraph mem ["Memory Manager"]
        Session["Session Memory"]
        Task["Task Memory"]
        Distilled["Distilled Memory"]
        Session --> Pipeline["Distillation Pipeline"]
        Task --> Pipeline
        Pipeline --> Distilled
    end

    subgraph evo ["Evolution Engine"]
        Pop["GA Population"]
        Score["Scoring Pipeline"]
        Cross["Crossover"]
        Mut["Mutation"]
        Pop --> Score
        Score --> Cross
        Cross --> Mut
        Mut --> Pop
    end

    subgraph hitl ["Human-in-the-Loop"]
        IH["Interrupt Handler"]
        IS["Interrupt Store"]
        AW["Approval Workflow"]
        IH --> IS
        IH --> AW
    end

    subgraph stor ["Storage Layer"]
        VS["VectorStore Interface"]
        PG[("PostgreSQL + pgvector")]
        MEM[("In-Memory")]
        QD[("Qdrant")]
        SQL[("SQLite + sqlite-vec")]
        CUSTOM[("Your Backend")]
        Cache["Cache"]
        CB["Circuit Breaker"]
        VS --> PG
        VS --> MEM
        VS --> QD
        VS --> SQL
        VS --> CUSTOM
        PG --> Cache
        Cache --> CB
    end

    subgraph tools ["Tool System"]
        Registry["Tool Registry"]
        MCP["MCP Manager"]
        MCP_Ext["MCP Servers"]
        Registry --> MCP
        MCP -->|"stdio/SSE"| MCP_Ext
    end

    subgraph obs ["Observability"]
        Dashboard["Web Dashboard"]
        Flight["Flight Recorder"]
        Genealogy["Agent Genealogy"]
    end

    subgraph cb ["Callbacks"]
        Handler["Handler Registry"]
        Events["Lifecycle Events"]
    end

    subgraph arena ["Chaos Engineering"]
        FI["Fault Injector"]
        RS["Resilience Scoring"]
        SM["Survival Mode"]
        FI --> RS
        RS --> SM
    end

    EmbedSvc["Embedding Service"]

    evo -->|"stores results"| Distilled
    evo -->|"uses"| Score

    Leader --> MutableDAG
    Leader --> Session
    Leader --> Registry
    Leader --> Adapters
    Leader --> cb
    Leader --> IH
    Session --> VS
    Registry --> VS
    VS -.->|"embeds via"| EmbedSvc

    obs -.->|"monitor"| RT
    obs -.->|"monitor"| Leader
    obs -.->|"monitor"| MutableDAG
    arena -.->|"stress test"| RT
    arena -.->|"stress test"| Leader
```

### Plugin System Architecture

```mermaid
graph TB
    subgraph executor ["Workflow Engine"]
        DEX["DynamicExecutor"]
        DAG["MutableDAG"]
    end

    subgraph plugins ["PluginBus & Plugins"]
        PB["PluginBus"]
        EB["EventBus"]
        WH["WorkflowHook"]
        REG["Plugin Registry"]
        CAP["Capability Index"]

        subgraph builtins ["Built-in Plugins"]
            OBS["ObserverPlugin"]
            CP["CheckpointPlugin"]
            TP["ToolPlugin"]
            RP["RouterPlugin<br/>(ExpressionRouter / MemoryRouter / EvolutionRouter)"]
            LP["LoopPlugin"]
            REC["RecoveryPlugin"]
            IP["InterruptPlugin"]
        end
    end

    subgraph storage ["Plugin Storage"]
        ES[("EventStore")]
        CKPT[("CheckpointStore")]
        COL["ExecutionCollector"]
    end

    subgraph discovery ["Discovery & Routing"]
        ADV["MemoryPlugin.AdviseRoute"]
        EVO["EvolutionPlugin.Recommend"]
    end

    DEX -->|"BeforeStep / AfterStep"| WH
    DEX -->|"flush checkpoints"| CP
    DEX -->|"PluginsByCap"| CAP
    REG --> builtins
    PB --> REG
    PB --> EB
    PB --> WH
    EB -->|"subscribe"| OBS
    OBS -->|"persist"| ES
    CP -->|"save to"| CKPT
    CP -->|"collect data"| COL
    RP -->|"Route(ctx, state)"| DEX
    LP -->|"ShouldContinue"| DEX
    REC -->|"ShouldRecover"| DEX
    RP -.->|"optional"| ADV
    RP -.->|"optional"| EVO

    style DEX fill:#4a6fa5,color:#fff
    style PB fill:#2e7d32,color:#fff
    style EB fill:#2e7d32,color:#fff
    style WH fill:#2e7d32,color:#fff
    style ES fill:#6a1b9a,color:#fff
    style CKPT fill:#6a1b9a,color:#fff
```

The PluginBus sits between the DynamicExecutor and all plugins. The executor calls **BeforeStep/AfterStep** hooks on every step boundary; hooks dispatch with configurable timeout and automatic panic recovery. **EventBus** enables pub/sub decoupling — plugins emit events without knowing who consumes them. **Capability Index** allows loose‑coupling lookup (`PluginsByCap`) so the executor never depends on concrete plugin types.

### Data Flow

#### Request Lifecycle

```mermaid
sequenceDiagram
    participant U as User
    participant RT as Runtime
    participant L as Leader Agent
    participant SA as Sub Agents
    participant T as Tools / MCP
    participant LLM as LLM
    participant M as Memory Store

    U->>RT: 1. Request
    RT->>L: 2. Dispatch
    L->>L: 3. Plan (DAG)
    L->>SA: 4. AHP Tasks
    SA->>LLM: 5. LLM Call
    SA->>T: 6. Tool/MCP Call
    T-->>SA: 7. Tool Result
    LLM-->>SA: 8. LLM Response
    SA-->>L: 9. Aggregate
    L-->>RT: 10. Response
    RT-->>U: 11. Final Response
    L->>M: 12. Store Experiences
```

Request routed through Runtime → Leader plan with DAG → Sub Agents execute (LLM + Tools) → results aggregated and returned.

#### Memory Distillation Pipeline

```mermaid
flowchart LR
    A["Extract"] --> B["Classify"]
    B --> C["Score"]
    C --> D["Denoise"]
    D --> E["Conflict Resolution"]
    E --> F["Capacity Cap"]
    F --> Distilled[("Distilled Memory")]
```

6-step pipeline: extract experiences from raw interactions, classify by type, score relevance, filter noise, resolve conflicts with existing memories, and enforce capacity limits.

### AHP Protocol

Custom Agent Hosting Protocol handling inter-agent communication with heartbeat monitoring, dead-letter queue (DLQ), and progress tracking. All protocol operations benchmark under 1 us.

### Leader Failover

Checkpoint-based recovery. Supervisor detects leader failure, recovers stale tasks from last checkpoint, and reassigns work to available sub-agents.

## Key Features

**DAG Workflow Engine**
- MutableDAG: runtime graph mutation (add/remove nodes and edges) all under 1 us
- DynamicExecutor: executes DAG with topological sort
- Incremental cycle detection on edge insertion
- Hot reload and runtime mutation without stopping execution

**Memory System**
- Session memory: short-term conversation context
- Task memory: per-task working memory
- Distilled memory: long-term compressed knowledge via 6-step pipeline (errgroup-based concurrent embedding)
- Multi-language experience extraction with Chinese keyword detection and importance scoring
- Content-hash deduplication for idempotent memory storage
- pgvector-backed semantic search

**Storage Layer**
- Pluggable vector store interface — swap PostgreSQL for Qdrant, Milvus, SQLite, or your own backend
- Built-in implementations: PostgreSQL + pgvector (production), in-memory (dev/test)
- Repository pattern abstraction
- Built-in cache layer + circuit breaker for fault tolerance
- Idempotent DDL migrations, safe for repeated execution
- See [Custom Vector Store Guide](docs/en/development/custom-vector-store.md)

**Agent System**
- Leader/Sub agent architecture
- AHP protocol for structured communication (heartbeat, DLQ, progress)
- Leader failover with checkpoint recovery
- Parallel task execution with configurable concurrency
- Agent resurrection plugin with pluggable health checking

**Runtime Layer**
- Agent lifecycle management: register, start, stop, restart, restore
- Automatic crash detection and resurrection via AgentFactory
- Two recovery dimensions: EventStore (operational) + MemoryStore (cognitive)
- Health monitoring with heartbeat and status-based checks
- Structured concurrency via errgroup with graceful shutdown

**Event Sourcing**
- EventStore interface with optimistic concurrency control
- MemoryEventStore for dev/test, PostgresEventStore for production
- 17 event types covering agent lifecycle, tasks, sessions, workflows, failover
- Pub/sub via Subscribe with filtered event channels
- DLQ auto-retry with configurable retry budgets

**Human-in-the-Loop**
- Pause workflow steps for human approval before execution
- InterruptConfig on any step, InterruptHandler for blocking approval
- InterruptStore for crash recovery of pending approvals
- Approval workflows and review gates

**Tool System**
- Dynamic tool registration and discovery
- Capability matching between agents and tools
- Parameter validation with schema support
- Pre/post execution lifecycle hooks

**MCP Integration**
- Model Context Protocol client with JSON-RPC 2.0 messaging
- Stdio and SSE transport support
- Tool schema management and connection lifecycle

**Observability**
- Web Dashboard: real-time monitoring with WebSocket streaming
- Flight Recorder: timeline tracking, decision logging, diagnostics engine
- Agent Genealogy: lineage tracking with graph export (DOT/JSON)
- Event Bridge: system state streaming to dashboard

**Chaos Engineering**
- Arena framework for fault injection testing
- Fault types: process_kill, network_partition, latency_spike, kill_orchestrator
- Resilience scoring with configurable metrics
- Survival mode for continuous chaos testing

**LLM Tool Calling**
- Multi-provider output adapters (OpenAI, Ollama, OpenRouter)
- Prompt template engine with Go template syntax
- Function calling extraction and validation
- Schema-based parameter validation
- Streaming output parser

**Extensibility**
- Event-driven callbacks system with typed contexts
- Event auto-compaction with retention policies
- Pluggable health checking for agent resurrection

**Autonomous Evolution (Genetic Algorithm)**
- Multi-generation population-based evolution with selection, crossover, and mutation
- Strategy mutation engine with deterministic reproducibility (seed-controlled)
- Arena regression testing with Welch's t-test statistical significance
- Dream cycle orchestration: trigger → mutate → evaluate → adopt → record lineage
- Bandit feedback loop for continuous experience quality optimization
- Event-driven callback system for LLM/Tool/Agent lifecycle hooks
- Wired high-level API: `NewWiredEvolutionSystem` for one-call component wiring
- Elite preservation and adaptive survival rate across generations

**Plugin System**
- PluginBus: centralized plugin registry and lifecycle manager. Thread-safe Start/Stop with reverse-order shutdown, duplicate detection, and started-state guards.
- EventBus: typed event pub/sub interface. `Emit` is non-blocking — drops events on full subscriber buffers. `Subscribe` supports filtering by stream ID, event type, and time range.
- WorkflowHook: synchronous interceptor interface (`BeforeStep` / `AfterStep`) invoked by DynamicExecutor at every step boundary. Each dispatch has configurable timeout and automatic panic recovery with structured `PluginError` wrapping.
- Capability-based discovery: `PluginsByCap(CapCheckpoint)` returns a copy of all plugins advertising that capability. Enables loose coupling between the workflow engine and plugins — the executor never depends on concrete plugin types.

**Built-in Plugins**

| Plugin | Capability | Role |
|--------|------------|------|
| ObserverPlugin | observer | Subscribes to workflow lifecycle events (workflow.started/completed/failed, step.started/completed/failed, checkpoint.saved) and persists them to EventStore |
| CheckpointPlugin | checkpoint | Saves deep-copy execution snapshots at step boundaries. Configurable flush interval. 22-field schema covering step states, variables, route/tool/memory/interrupt/error/loop history, and scoring signals |
| ToolPlugin | tool | Validates and records tool invocations via ExecutionCollector |
| ExpressionRouter | router | Rule-based router: FromStepID → ToStepID with predicate condition. First-match semantics |
| MemoryRouter | router | Queries `MemoryPlugin.AdviseRoute` first, falls back to expression rules |
| EvolutionRouter | router | Queries `EvolutionPlugin.Recommend` first, falls back to expression rules |
| LoopPlugin | loop | Controlled execution loops with MaxIterations, UntilCondition, and SubStepIDs |
| BasicRecoveryPlugin | recovery | Allowlist-based step failure recovery decisions |
| InterruptPlugin | — | Records HITL interrupt lifecycle events via collector |
| ArenaPlugin | — | Fault injection for robustness testing (plugin_panic, plugin_timeout, plugin_error, bus_stop) |

**ExecutionCollector**
- Thread-safe data aggregator collecting route decisions, tool calls, memory hits, interrupts, and errors during workflow execution
- `Export()` produces serializable maps; `MergeInto()` copies into `ExperienceCheckpoint`
- Consumed by CheckpointPlugin, memory distillation pipeline, and evolution engine scoring

**ExperienceCheckpoint** — full execution snapshot:
```json
{
  "schema_version": 1,
  "execution_id": "...",
  "workflow_id": "...",
  "workflow_version": "...",
  "state_version": 1,
  "status": "running",
  "step_states": [...],
  "variables": {...},
  "output_store": {...},
  "route_history": [...],
  "tool_history": [...],
  "memory_hits": [...],
  "interrupt_history": [...],
  "loop_history": [...],
  "error_history": [...],
  "scoring_signals": [...],
  "created_at": "..."
}
```
Enables complete execution state restore for leader failover and step-level recovery.

## Benchmark Highlights

32 benchmarks total. 2573 tests pass with `-race` across 49 packages.

Platform: darwin/arm64, Apple M3 Max, Go 1.26.4

| Category | Count | Hot (< 1 us) | Normal (1-100 us) | Cold (> 100 us) |
|----------|-------|---------------|--------------------|--------------------|
| Eval | 5 | 2 | 2 | 1 |
| Distillation | 9 | 0 | 8 | 1 |
| Tools/Core | 8 | 3 | 5 | 0 |
| Errors | 4 | 2 | 2 | 0 |
| Event Sourcing | 6 | 0 | 5 | 1 |
| **Total** | **32** | **7** | **22** | **3** |

Selected hot-path results:

| Operation | ns/op | allocs/op |
|-----------|-------|-----------|
| ExactMatchEvaluator | 180.3 | 0 |
| ToolUsageEvaluator | 361.3 | 0 |
| ToolExecution | 347.3 | 0 |
| ConvertEvent | 97.00 | 0 |
| ResultCreation (Success) | 125.0 | 0 |
| ResultCreation (Error) | 55.33 | 0 |
| ParameterValidation | 208.3 | 0 |
| Wrap (error) | 69.67 | 0 |
| WrapMultipleWraps | 69.33 | 0 |
| ConflictDetection | 2125 | 0 |

7 of 32 benchmarks run under 1 us. Zero-allocation paths for evaluation, tool execution, result creation, event conversion, error wrapping, and conflict detection.

Full benchmark report: `benchmarks/benchmark_report.md`

## Quick Start

### Prerequisites

- Go 1.26+
- PostgreSQL 15+ with pgvector (optional, for persistence)
- Docker (optional, for database)

### 1. Set API Key

```bash
export OPENROUTER_API_KEY="your-api-key"
```

### 2. Start Database (Optional)

```bash
# One-click restart with migration (optionally import a doc)
./scripts/docker/restart.sh
./scripts/docker/restart.sh --save examples/knowledge-base/README.md

# Or manually:
docker run -d \
  --name ares-db \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=goagent \
  -p 5433:5432 \
  pgvector/pgvector:pg15
```

### 3. Run Examples

```bash
# Travel planning (multi-agent collaboration)
cd examples/travel && go run main.go

# Knowledge base Q&A (requires database + embedding service)
cd examples/knowledge-base
go run main.go --save README.md              # Import document
go run main.go --save docs/goagent-overview.md  # Import framework overview
go run main.go --chat                         # Start Q&A (supports knowledge correction)

# Advanced examples (v2 features)
go run ./examples/advanced/leader_failover/
go run ./examples/advanced/agent_resurrection/
go run ./examples/advanced/runtime_resurrection/
go run ./examples/advanced/dynamic_executor/
go run ./examples/advanced/mutable_dag/

# Dashboard with MCP integration
cd examples/mcp-dashboard && go run main.go

# Quantitative analysis demo
cd examples/quant-trading && go run . --ticker AAPL

# Development agent demo
cd examples/devagent && go run main.go

# Tool capability demo
cd examples/capability-demo && go run main.go

# Autonomous evolution (genetic algorithm) demo
cd examples/autonomous-evolution && go run main.go
```

See [Advanced Examples](docs/en/development/examples.md) for detailed documentation.

### 4. Run Tests

```bash
go test ./...                      # All tests
go test -race ./...                # With race detector
go test -bench=. ./...             # Benchmarks
```

## Project Structure

```
ares/
├── internal/
│   ├── agents/          # Leader/Sub agent system
│   ├── runtime/         # Runtime lifecycle + PluginBus (+ 10 built-in plugins)
│   │   ├── plugin.go    # RuntimePlugin, WorkflowHook, EventBus interfaces
│   │   ├── bus.go       # PluginBus — registry, lifecycle, dispatch, capabilities
│   │   ├── events.go    # Workflow lifecycle event constants + payload keys
│   │   ├── types.go     # Step, StepResult, StepStatus types
│   │   ├── collector.go # ExecutionCollector — thread-safe runtime data aggregation
│   │   ├── observer.go  # ObserverPlugin — event persistence to EventStore
│   │   ├── checkpoint.go# CheckpointPlugin — step-boundary snapshots
│   │   ├── tool.go      # ToolPlugin — tool invocation recording
│   │   ├── router.go    # ExpressionRouter — rule-based routing
│   │   ├── router_memory.go   # MemoryRouter — memory-aware routing
│   │   ├── router_evolution.go# EvolutionRouter — evolution-aware routing
│   │   ├── loop.go      # LoopPlugin — controlled execution loops
│   │   ├── recovery.go  # BasicRecoveryPlugin — step failure recovery
│   │   ├── interrupt.go # InterruptPlugin — HITL interrupt recording
│   │   ├── arena.go     # ArenaPlugin — fault injection testing
│   │   ├── errors.go    # PluginError type + sentinel errors
│   │   └── options.go   # PluginBusOption (WithPluginTimeout, WithLogger)
│   ├── protocol/ahp/    # AHP inter-agent protocol
│   ├── memory/          # Memory system + distillation
│   ├── events/          # EventStore interface, MemoryEventStore, event types
│   ├── workflow/engine/  # DAG workflow engine (DynamicExecutor + PluginBus integration)
│   ├── storage/          # VectorStore interface + implementations
│   │   ├── postgres/     # PostgreSQL + pgvector (production)
│   │   └── memory/       # In-memory (dev/test)
│   ├── mcp/             # MCP client (stdio/SSE transport)
│   ├── dashboard/        # Web dashboard (WebSocket + REST API)
│   ├── flight/           # Flight recorder (timeline/genealogy/diagnostics)
│   ├── arena/            # Chaos engineering arena
│   ├── callbacks/        # Event-driven callback system
│   ├── llm/output/       # LLM output parsers + prompt templates
│   └── tools/           # Tool registry and invocation
├── services/embedding/  # Embedding gateway (FastAPI + Ollama)
├── examples/            # Travel, knowledge-base, dashboard, quant, devagent, ...
├── api/                 # Service interfaces and client
├── cmd/                 # CLI tools (arena, flight, migration, ...)
└── benchmarks/          # Benchmark reports and logs
```

## Configuration

Configuration is YAML-based. Key sections:

```yaml
llm:
  provider: openrouter
  api_key: "${OPENROUTER_API_KEY}"
  model: meta-llama/llama-3.1-8b-instruct
  timeout: 60

agents:
  leader:
    id: leader-main
    max_steps: 10
    max_parallel_tasks: 4
  sub:
    - id: agent-a
      type: research
      max_retries: 3
      timeout: 30

storage:
  type: postgres
  host: localhost
  port: 5433
  database: goagent
  pgvector:
    enabled: true
    dimension: 1024

memory:
  enabled: true
  enable_distillation: true
  distillation_threshold: 3
```

See `examples/travel/config.yaml` for a complete example.

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.26+ |
| Database | PostgreSQL 15+ with pgvector (pluggable: Qdrant, Milvus, SQLite, or custom) |
| Protocol | Custom AHP (Agent Hosting Protocol) |
| Embedding | FastAPI + Ollama/SentenceTransformers |
| Cache | Redis |
| Concurrency | errgroup, sync |

## Documentation

- [Changelog](CHANGELOG.md)
- [Architecture](docs/en/architecture/arch.md)
- [Runtime Layer](docs/en/architecture/runtime.md)
- [Quick Start](docs/en/guides/quick-start.md)
- [FAQ](docs/en/guides/faq.md)
- [Integration Guide](docs/en/development/integration-guide.md)
- [Custom Vector Store](docs/en/development/custom-vector-store.md)
- [Leader Failover](docs/en/features/leader-failover.md)
- [Dynamic Graph](docs/en/features/dynamic-graph.md)
- [Human-in-the-Loop](docs/en/features/hitl.md)
- [Agent Resurrection](docs/en/features/resurrection.md)
- [MCP & Dashboard](docs/en/features/mcp-and-dashboard.md)
- [Event Sourcing](docs/en/features/event-sourcing.md)
- [Integration Testing](docs/en/development/integration-testing.md)
- [CI/CD Pipeline](docs/en/development/ci-cd.md)
- [Framework Comparison](docs/en/framework-comparison.md)
- [Benchmark Report](benchmarks/benchmark_report.md)
- [Autonomous Evolution Guide](docs/en/features/autonomous-evolution.md)

## LICENSE
Apache 2.0