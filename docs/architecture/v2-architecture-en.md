# GoAgent v2 Architecture

**Updated**: 2026-06-10

## Overview

v2 adds two core capabilities on top of v1:

1. **Leader Failover** - Automatic Leader failure detection and recovery
2. **Runtime Dynamic Graph** - Runtime modification of workflow DAGs

These capabilities enhance system reliability and flexibility.

## v1 Architecture Review

```mermaid
graph TB
    User["User Input"] --> Leader["Leader Agent"]
    Leader -->|"dispatch tasks"| SubAgents["Sub Agents"]
    SubAgents --> Protocol["AHP Protocol"]
    Protocol --> Storage["PostgreSQL + pgvector"]
    Protocol --> LLM["LLM System"]
    Leader --> Memory["Memory System"]
```

v1 limitations:
- Leader crash = session lost
- DAG immutable after construction
- No automatic failure recovery

## v2 Architecture Extensions

```mermaid
graph TB
    subgraph v2Additions["v2 New Components"]
        HB["HeartbeatMonitor<br/>Heartbeat detection"]
        LS["LeaderSupervisor<br/>Failover orchestration"]
        CR["ColdRestartStrategy<br/>Cold restart strategy"]
        CP["CheckpointRepository<br/>State snapshots"]
        TR["TaskRecovery<br/>Orphaned task cleanup"]
        MDAG["MutableDAG<br/>Mutable DAG"]
        DE["DynamicExecutor<br/>Dynamic executor"]
        GEH["GraphEventHub<br/>Mutation event notifications"]
    end

    subgraph v1Core["v1 Core Components"]
        Leader["Leader Agent"]
        SubAgents["Sub Agents"]
        AHP["AHP Protocol"]
        DAG["DAG (immutable)"]
        Executor["Executor"]
        PG["PostgreSQL"]
    end

    HB -->|"timeout callback"| LS
    LS --> CR
    LS --> CP
    LS --> TR
    CP --> PG
    TR --> PG

    MDAG -->|"Version change"| DE
    GEH --> MDAG
    DE --> Executor

    Leader -.->|"failover"| LS
    DAG -.->|"extends"| MDAG
    Executor -.->|"extends"| DE
```

## Leader Failover in the Architecture

Leader Failover inserts monitoring and recovery between the AHP Protocol layer and Leader Agents:

```mermaid
graph LR
    subgraph AHP["AHP Protocol"]
        HB["HeartbeatMonitor"]
        Sender["HeartbeatSender"]
    end

    subgraph Supervisor["Supervisor Layer"]
        LS["LeaderSupervisor"]
        FS["FailoverStrategy"]
    end

    subgraph Persistence["Persistence Layer"]
        CP["CheckpointRepository"]
        TR["TaskRecovery"]
    end

    Sender -->|"periodic send"| HB
    HB -->|"timeout trigger"| LS
    LS -->|"create new instance"| FS
    LS -->|"restore state"| CP
    LS -->|"cleanup tasks"| TR
```

Data flow:
1. `HeartbeatSender` periodically sends heartbeats via AHP message queue
2. `HeartbeatMonitor` detects timeouts and triggers callbacks
3. `LeaderSupervisor` orchestrates failover: stop old instance -> restore checkpoint -> create new instance -> clean orphaned tasks
4. `CheckpointRepository` and `TaskRecovery` persist state via PostgreSQL

## Dynamic Graph in the Architecture

Dynamic Graph extends the Workflow Engine layer:

```mermaid
graph TB
    subgraph WorkflowEngine["Workflow Engine"]
        MDAG["MutableDAG"]
        DE["DynamicExecutor"]
        GEH["GraphEventHub"]
        BaseDAG["DAG (base)"]
        BaseExec["Executor (base)"]
    end

    subgraph Operations["Graph Mutations"]
        AddN["AddNode"]
        RemN["RemoveNode"]
        AddE["AddEdge"]
        RemE["RemoveEdge"]
    end

    subgraph Execution["Execution Modes"]
        CP["ApplyAtCheckpoint"]
        IM["ApplyImmediate"]
    end

    BaseDAG -->|"extends"| MDAG
    BaseExec -->|"extends"| DE
    Operations --> MDAG
    MDAG -->|"Version"| DE
    GEH -->|"event notifications"| MDAG
    Execution --> DE
```

Data flow:
1. External callers modify graph structure via `MutableDAG` methods
2. Each mutation increments `version` and publishes events via `GraphEventHub`
3. `DynamicExecutor` detects version changes during execution
4. Based on `ApplyMode`, recomputes execution order at checkpoints or immediately
5. Newly added steps are automatically appended to the execution queue

## Component Interaction

```mermaid
sequenceDiagram
    participant User as User
    participant Leader as Leader Agent
    participant HB as HeartbeatMonitor
    participant LS as LeaderSupervisor
    participant DAG as MutableDAG
    participant DE as DynamicExecutor
    participant Sub as Sub Agents

    User->>Leader: Submit task
    Leader->>DAG: Build workflow DAG
    Leader->>DE: ExecuteDynamic(workflow, dag)
    DE->>Sub: Dispatch step execution

    Note over HB: Background
    HB->>HB: Check heartbeats
    HB->>LS: Leader timeout callback
    LS->>LS: Create new Leader
    LS->>Leader: Restore checkpoint

    Note over DAG: Runtime mutation
    DAG->>DAG: AddNode() / AddEdge()
    DAG->>DE: Version change notification
    DE->>DE: recomputeOrder()
    DE->>Sub: Execute new steps
```

## v1 to v2 Migration

v2 is fully backward compatible with v1. All new components are optional:

| v1 Component | v2 Extension | Required? |
|-------------|-------------|-----------|
| Leader Agent | + LeaderSupervisor | Optional |
| DAG | MutableDAG | Optional |
| Executor | DynamicExecutor | Optional |
| AHP Protocol | + HeartbeatMonitor | Optional |

Minimal migration steps:
1. Add `HeartbeatMonitor` and `LeaderSupervisor` for failover capability
2. Replace `DAG` with `MutableDAG` for dynamic graph capability
3. Replace `Executor` with `DynamicExecutor` for runtime reordering capability

## Related Documents

- [Leader Failover Details](../features/leader-failover-en.md)
- [Runtime Dynamic Graph Details](../features/dynamic-graph-en.md)
- [v1 Architecture Design](./arch_en.md)
