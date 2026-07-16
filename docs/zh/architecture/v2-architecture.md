# ARES v2 架构设计

**更新日期**: 2026-06-10

## 概述

v2 在 v1 基础上新增两个核心能力：

1. **Leader Failover** - Leader 故障自动检测与恢复
2. **Runtime Dynamic Graph** - 运行时动态修改工作流 DAG

这两个能力增强了系统的可靠性和灵活性。

## v1 架构回顾

```mermaid
graph TB
    User["用户输入"] --> Leader["Leader Agent"]
    Leader -->|"分发任务"| SubAgents["Sub Agents"]
    SubAgents --> Protocol["AHP Protocol"]
    Protocol --> Storage["PostgreSQL + pgvector"]
    Protocol --> LLM["LLM System"]
    Leader --> Memory["Memory System"]
```

v1 的局限：
- Leader 崩溃 = 会话丢失
- DAG 构建后不可变
- 无自动故障恢复

## v2 架构扩展

```mermaid
graph TB
    App["应用"] --> RT

    subgraph RuntimeLayer["Runtime 层（新增）"]
        RT["Runtime (Manager)"]
        RT -->|"监控"| Agents["受管 Agent"]
        RT -->|"回放"| ES["EventStore"]
        RT -->|"恢复"| MM["MemoryStore"]
    end

    subgraph v2Additions["v2 新增组件"]
        HB["HeartbeatMonitor<br/>心跳检测"]
        LS["LeaderSupervisor<br/>故障转移编排"]
        CR["ColdRestartStrategy<br/>冷重启策略"]
        CP["CheckpointRepository<br/>状态快照"]
        TR["TaskRecovery<br/>孤立任务清理"]
        MDAG["MutableDAG<br/>可变 DAG"]
        DE["DynamicExecutor<br/>动态执行器"]
        GEH["GraphEventHub<br/>变更事件通知"]
    end

    subgraph v1Core["v1 核心组件"]
        Leader["Leader Agent"]
        SubAgents["Sub Agents"]
        AHP["AHP Protocol"]
        DAG["DAG (不可变)"]
        Executor["Executor"]
        PG["PostgreSQL"]
    end

    RT -->|"生命周期"| Leader
    RT -->|"生命周期"| SubAgents

    HB -->|"超时回调"| LS
    LS --> CR
    LS --> CP
    LS --> TR
    CP --> PG
    TR --> PG

    MDAG -->|"Version 变更"| DE
    GEH --> MDAG
    DE --> Executor

    Leader -.->|"failover"| LS
    DAG -.->|"扩展"| MDAG
    Executor -.->|"扩展"| DE
```

## Leader Failover 在架构中的位置

Leader Failover 在 AHP Protocol 层和 Leader Agent 之间插入监控和恢复机制：

```mermaid
graph LR
    subgraph AHP["AHP Protocol"]
        HB["HeartbeatMonitor"]
        Sender["HeartbeatSender"]
    end

    subgraph Supervisor["Supervisor 层"]
        LS["LeaderSupervisor"]
        FS["FailoverStrategy"]
    end

    subgraph Persistence["持久化层"]
        CP["CheckpointRepository"]
        TR["TaskRecovery"]
    end

    Sender -->|"定期发送"| HB
    HB -->|"超时触发"| LS
    LS -->|"创建新实例"| FS
    LS -->|"恢复状态"| CP
    LS -->|"清理任务"| TR
```

数据流：
1. `HeartbeatSender` 定期通过 AHP 消息队列发送心跳
2. `HeartbeatMonitor` 检测超时，触发回调
3. `LeaderSupervisor` 编排故障转移：停止旧实例 → 恢复 checkpoint → 创建新实例 → 清理孤立任务
4. `CheckpointRepository` 和 `TaskRecovery` 通过 PostgreSQL 持久化状态

## Dynamic Graph 在架构中的位置

Dynamic Graph 扩展了 Workflow Engine 层：

```mermaid
graph TB
    subgraph WorkflowEngine["Workflow Engine"]
        MDAG["MutableDAG"]
        DE["DynamicExecutor"]
        GEH["GraphEventHub"]
        BaseDAG["DAG (基础)"]
        BaseExec["Executor (基础)"]
    end

    subgraph Operations["图变更操作"]
        AddN["AddNode"]
        RemN["RemoveNode"]
        AddE["AddEdge"]
        RemE["RemoveEdge"]
    end

    subgraph Execution["执行模式"]
        CP["ApplyAtCheckpoint"]
        IM["ApplyImmediate"]
    end

    BaseDAG -->|"扩展"| MDAG
    BaseExec -->|"扩展"| DE
    Operations --> MDAG
    MDAG -->|"Version"| DE
    GEH -->|"事件通知"| MDAG
    Execution --> DE
```

数据流：
1. 外部通过 `MutableDAG` 的方法修改图结构
2. 每次变更递增 `version` 并通过 `GraphEventHub` 发布事件
3. `DynamicExecutor` 在执行期间检测版本变更
4. 根据 `ApplyMode` 在检查点或立即重算执行顺序
5. 新增步骤自动追加到执行队列

## Runtime 层

Runtime 层管理 Agent 生命周期。Agent 是可丢弃的执行器；Runtime 拥有它们的诞生、死亡和复活。

```mermaid
graph LR
    subgraph Runtime["Runtime (Manager)"]
        Reg["RegisterAgent"]
        Start["StartAgent"]
        Health["HealthCheck"]
        Notify["NotifyAgentDead"]
        Restore["RestoreAgent"]
    end

    subgraph Recovery["恢复"]
        ES["EventStore<br/>运维恢复"]
        MM["MemoryStore<br/>认知恢复"]
    end

    Reg --> Start
    Start --> Health
    Health -->|"Agent 死亡"| Notify
    Notify --> Restore
    Restore --> ES
    Restore --> MM
    Restore -->|"新实例"| Start
```

核心行为：
- **注册**：Agent 通过 Factory 函数注册，用于复活
- **健康监控**：后台循环通过心跳或状态检查 Agent 存活性
- **自动恢复**：崩溃时，Runtime 创建新实例、回放事件、恢复记忆并重启
- **两个恢复维度**：EventStore 用于运维状态（"执行到哪一步？"），MemoryStore 用于认知状态（"我是谁？"）
- **优雅关闭**：Stop 取消所有 Agent 并等待 goroutine 完成

详见 [Runtime 层详解](./runtime.md)。

## 组件交互

```mermaid
sequenceDiagram
    participant User as 用户
    participant Leader as Leader Agent
    participant HB as HeartbeatMonitor
    participant LS as LeaderSupervisor
    participant DAG as MutableDAG
    participant DE as DynamicExecutor
    participant Sub as Sub Agents

    User->>Leader: 提交任务
    Leader->>DAG: 构建工作流 DAG
    Leader->>DE: ExecuteDynamic(workflow, dag)
    DE->>Sub: 分发步骤执行

    Note over HB: 后台运行
    HB->>HB: 检测心跳
    HB->>LS: Leader 超时回调
    LS->>LS: 创建新 Leader
    LS->>Leader: 恢复 checkpoint

    Note over DAG: 运行时变更
    DAG->>DAG: AddNode() / AddEdge()
    DAG->>DE: Version 变更通知
    DE->>DE: recomputeOrder()
    DE->>Sub: 执行新增步骤
```

## v1 → v2 迁移

v2 完全向后兼容 v1。新增组件均为可选：

| v1 组件 | v2 扩展 | 是否必须 |
|---------|---------|---------|
| Leader Agent | + LeaderSupervisor | 可选 |
| DAG | MutableDAG | 可选 |
| Executor | DynamicExecutor | 可选 |
| AHP Protocol | + HeartbeatMonitor | 可选 |
| （无） | Runtime (Manager) | 可选 |

最小迁移步骤：
1. 引入 `HeartbeatMonitor` 和 `LeaderSupervisor` 获得故障转移能力
2. 将 `DAG` 替换为 `MutableDAG` 获得动态图能力
3. 将 `Executor` 替换为 `DynamicExecutor` 获得运行时重排能力
4. 使用 `Runtime` 包装 Agent，获得生命周期管理和自动恢复能力

## 相关文档

- [Runtime 层详解](./runtime.md)
- [Leader Failover 详解](../features/leader-failover.md)
- [Runtime Dynamic Graph 详解](../features/dynamic-graph.md)
- [v1 架构设计](./arch.md)
