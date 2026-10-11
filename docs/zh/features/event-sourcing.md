# Event Sourcing

**更新日期**: 2026-06-11

## 概述

Event Sourcing 将每次状态变更记录为不可变事件。系统不覆写当前状态，而是将事件追加到有序日志。当前状态通过重放事件流（从头或从快照）派生。

对 Agent 系统而言，Event Sourcing 的价值在于：

- **可审计性**：每个决策、任务分发、故障转移都带有时间戳和版本号。
- **可重放**：通过重放事件流重建任意 Agent 的状态。对 agent 复活（`failover.*`）与任务跨重启恢复至关重要。
- **解耦**：生产者（peer agent、Workflow、Memory）独立写入事件。消费者（Projector、Auditor）异步订阅和响应；死信由 `internal/agentipc` 的 `DeadLetterStore` 记录，**只诊断、不自动重试**（AHP 的 DLQ 机器已于 v0.3.3 退役）。
- **乐观并发控制**：基于版本的冲突检测，防止多 Agent 写入同一 Stream 时静默数据损坏。

## 架构

```mermaid
graph LR
    subgraph Producers ["生产者"]
        Agent[Peer Agents]
        Workflow[Workflow Engine]
        Memory[Memory System]
    end

    subgraph EventStore ["EventStore 接口"]
        Append["Append(ctx, streamID, events, expectedVersion)"]
        Read["Read(ctx, streamID, opts)"]
        ReadAll["ReadAll(ctx, opts)"]
        Subscribe["Subscribe(ctx, filter)"]
        StreamVersion["StreamVersion(ctx, streamID)"]
    end

    subgraph Implementations ["实现"]
        MemStore[MemoryEventStore]
        PgStore[PostgresEventStore]
    end

    subgraph Consumers ["消费者"]
        Projector[Projector]
        Replayer[Replayer]
        Auditor[Auditor]
    end

    Agent --> Append
    Workflow --> Append
    Memory --> Append

    Append --> MemStore
    Append --> PgStore

    MemStore --> Read
    PgStore --> Read
    MemStore --> Subscribe
    PgStore --> Subscribe

    Subscribe --> Projector
    Subscribe --> Replayer
    Subscribe --> Auditor
```

## Event Types

共 35 种事件类型（`internal/ares_events/types.go`），按领域分组：

### Agent 生命周期

| 事件 | Type | 说明 |
|------|------|------|
| Agent Started | `agent.started` | Agent 实例启动 |
| Agent Stopped | `agent.stopped` | Agent 正常停止 |

### Task 生命周期（基础）

| 事件 | Type | 说明 |
|------|------|------|
| Task Created | `task.created` | 提交入口创建新任务（`submitPeerTask` / POST /api/graphs） |
| Task Dispatched | `task.dispatched` | 任务被分派 |
| Task Completed | `task.completed` | 任务执行成功 |
| Task Failed | `task.failed` | 任务执行失败（payload 带 `retryable` 判定） |

### Task Fabric 状态机（`internal/fabric/task` 在每次状态迁移时发布）

| 事件 | Type | 说明 |
|------|------|------|
| Task Ready | `task.ready` | 进入可调度集合（allow-partial 的降级缺口也挂在此事件上） |
| Task Acquired | `task.acquired` | 被某 agent 取得租约 |
| Task Started | `task.started` | 开始执行 |
| Task Yielded | `task.yielded` | 主动让出（等待恢复/重调度） |
| Task Checkpointed | `task.checkpointed` | checkpoint 落盘 |
| Task Preempted | `task.preempted` | 被更高优先级抢占 |
| Task Released | `task.released` | 释放租约 |
| Task Expired | `task.expired` | 租约过期 |
| Task Deleted | `task.deleted` | tombstone（任务被删除） |

### Session 与 Memory

| 事件 | Type | 说明 |
|------|------|------|
| Session Created | `session.created` | 新会话创建 |
| Message Added | `message.added` | 消息添加到会话 |
| Memory Distilled | `memory.distilled` | Memory 蒸馏管线完成 |

### Tool 与 LLM

| 事件 | Type | 说明 |
|------|------|------|
| Tool Call Started | `tool.call.started` | 工具调用开始 |
| Tool Call Completed | `tool.call.completed` | 工具调用结束 |
| LLM Call | `llm.call` | LLM 被调用时触发 |

### Workflow Step 恢复

| 事件 | Type | 说明 |
|------|------|------|
| Step Failed | `step.failed` | Workflow Step 失败 |
| Step Recovery Started | `step.recovery.started` | Step 恢复启动 |
| Step Recovery Completed | `step.recovery.completed` | Step 恢复完成 |
| Step Recovery Failed | `step.recovery.failed` | Step 恢复失败 |

### 协作（沿用 sub 命名）

| 事件 | Type | 说明 |
|------|------|------|
| Sub Task Result | `sub_task.result` | sub 返回结果（成功或失败） |
| Sub Agent Failed | `sub_agent.failed` | sub 崩溃（捕获 panic） |

### Failover（agent 恢复，非 Leader）

| 事件 | Type | 说明 |
|------|------|------|
| Failover Triggered | `failover.triggered` | agent 恢复启动（旧实例停用） |
| Failover Completed | `failover.completed` | 状态恢复完成，新实例接管 |

> 该类型名沿用自已删除的 Leader-Sub 时代：现在由 `internal/runtime/manager.go` 在 **agent 复活**（停旧实例 → 恢复状态 → 启动新实例）时发出，stream 为 `agentID`，与"Leader 故障转移"无关。

### 服务发现

| 事件 | Type | 说明 |
|------|------|------|
| Discovery Service Added | `discovery.service.added` | 发现新服务（MCP server / agent runtime） |
| Discovery Service Removed | `discovery.service.removed` | 服务下线 |
| Discovery Service Updated | `discovery.service.updated` | 服务信息更新 |
| Discovery Health Changed | `discovery.health.changed` | 健康状态变化 |
| Discovery Cycle Completed | `discovery.cycle.complete` | 一轮发现完成 |

### 系统组件

| 事件 | Type | 说明 |
|------|------|------|
| Component Failed | `component.failed` | 受管后台组件 panic/退出（stream 为 `system_runtime/<component>`） |

## 核心类型

### Event

```go
// Event represents something that happened in the system.
type Event struct {
    ID        string         `json:"id"`
    StreamID  string         `json:"stream_id"`
    Type      EventType      `json:"type"`
    Payload   map[string]any `json:"payload"`
    Metadata  map[string]any `json:"metadata,omitempty"`
    Version   int64          `json:"version"`
    Timestamp time.Time      `json:"timestamp"`
}
```

每个事件属于一个 **Stream**（由 `StreamID` 标识）。`Version` 字段在每个 Stream 内单调递增，用于乐观并发控制。

### EventStore 接口

```go
// EventStore defines the interface for appending, reading, and subscribing to events.
type EventStore interface {
    Append(ctx context.Context, streamID string, events []*Event, expectedVersion int64) error
    Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error)
    ReadAll(ctx context.Context, opts ReadOptions) ([]*Event, error)
    Subscribe(ctx context.Context, filter EventFilter) (<-chan *Event, error)
    StreamVersion(ctx context.Context, streamID string) (int64, error)
}
```

### 哨兵错误

| 错误 | 含义 |
|------|------|
| `ErrVersionConflict` | Append 时乐观并发冲突 |
| `ErrStreamNotFound` | 请求的 Stream 不存在 |
| `ErrEventStoreClosed` | Store 已关闭，拒绝操作 |

## 用法

### 创建和追加事件

```go
store := events.NewMemoryEventStore()

// 创建事件
evt := &events.Event{
    Type:    events.EventTaskCreated,
    Payload: map[string]any{"task_id": "task-1", "description": "analyze data"},
}

// 追加到 Stream（expectedVersion=0 表示新 Stream）
err := store.Append(ctx, "task-1", []*events.Event{evt}, 0)
if err != nil {
    log.Fatal(err)
}
```

### 读取事件

```go
// 读取 Stream 的所有事件
evts, err := store.Read(ctx, "task-1", events.ReadOptions{})

// 分页和过滤
evts, err := store.Read(ctx, "task-1", events.ReadOptions{
    FromVersion: 5,
    Limit:       10,
    Direction:   events.ReadAscending,
})

// 读取指定时间之后的事件
evts, err := store.Read(ctx, "task-1", events.ReadOptions{
    Since: time.Now().Add(-1 * time.Hour),
})

// 跨 Stream 读取
allEvts, err := store.ReadAll(ctx, events.ReadOptions{
    Direction: events.ReadDescending,
    Limit:     100,
})
```

### 订阅事件

```go
filter := events.EventFilter{
    Types: []events.EventType{
        events.EventTaskCompleted,
        events.EventTaskFailed,
    },
}

ch, err := store.Subscribe(ctx, filter)

go func() {
    for evt := range ch {
        fmt.Printf("Task event: %s at %v\n", evt.Type, evt.Timestamp)
    }
}()
```

### 乐观并发控制

```go
// 读取当前版本
version, _ := store.StreamVersion(ctx, "task-1")

// 带预期版本追加 - 如果其他写入者已变更 Stream 则失败
err := store.Append(ctx, "task-1", newEvents, version)
if errors.Is(err, events.ErrVersionConflict) {
    // 重新读取并重试
}
```

## 实现

### MemoryEventStore

用于开发、测试和原型验证的内存存储。不适用于生产环境。

```go
store := events.NewMemoryEventStore()
defer store.Close()

// 所有操作线程安全（内部使用 sync.RWMutex）
_ = store.Append(ctx, "stream-1", events, 0)
evts, _ := store.Read(ctx, "stream-1", events.ReadOptions{})
```

特性：
- 通过 `sync.RWMutex` 保证线程安全
- 非阻塞订阅通知（缓冲区满时丢弃事件）
- `Close()` 时自动清理所有订阅 channel

### PostgresEventStore

基于 PostgreSQL 的生产级存储，支持事务写入。

```go
pool, _ := postgres.NewPool(ctx, cfg)
store := events.NewPostgresEventStore(pool)

// 相同接口，持久化存储
_ = store.Append(ctx, "task-1", events, 0)
```

特性：
- 事务性追加，行级锁定
- `UNIQUE(stream_id, version)` 约束在数据库层面捕获并发冲突
- 基于轮询的订阅（1 秒间隔），使用游标分页
- 错误时自动回滚

需要 `events` 表：

```sql
CREATE TABLE events (
    id          TEXT PRIMARY KEY,
    stream_id   TEXT NOT NULL,
    type        TEXT NOT NULL,
    payload     JSONB NOT NULL DEFAULT '{}',
    metadata    JSONB,
    version     BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (stream_id, version)
);
CREATE INDEX idx_events_stream_version ON events (stream_id, version);
CREATE INDEX idx_events_created_at ON events (created_at);
```

## 死信记录（原 AHP DLQ 已退役）

> **v0.3.3 订正**：本节原描述的 AHP Dead Letter Queue（`internal/runtime/protocol/ahp/dlq.go` 的 `DLQ` / `DLQProcessor` / `RegisterHandler` / `StartAutoRetry`）**已随 AHP 协议机器一并删除**，该包如今只剩消息词汇（`message.go`）。这里原有的 `ahp.NewDLQ(...)` 代码示例在任何版本中都不再可运行。

现在唯一活着的死信设施是 **`internal/agentipc` 的 `DeadLetterStore`**：有界 FIFO（默认容量 1024），记录无法投递或超时的请求，**只诊断、不自动重试**——没有 redelivery 策略，这是已知缺口（`cmd/ares/kernel.go` 里有 `TODO(tech-debt)` 记录了补齐它的意图）。

## 可靠性

### 事件丢失防护

- **MemoryEventStore**：仅内存存储，进程崩溃时丢失。用于测试。
- **PostgresEventStore**：持久化到 PostgreSQL，带 WAL。崩溃后可恢复。用于生产。
- 订阅 channel 缓冲区大小为 1。缓冲区满时丢弃事件（非阻塞）。消费者必须及时消费。

### 版本冲突处理

当两个写入者并发追加同一 Stream 时：

1. 写入者 A 读取 version=5，准备事件
2. 写入者 B 读取 version=5，先追加（version 变为 6）
3. 写入者 A 以 expectedVersion=5 追加，收到 `ErrVersionConflict`
4. 写入者 A 重新读取（version=6），以 expectedVersion=6 重试

这是乐观并发控制。读-改-写周期内不持有锁。PostgreSQL 实现通过 `UNIQUE(stream_id, version)` 约束作为安全网。

## 性能数据

平台：darwin/arm64, Apple M3 Max, Go 1.26.4

| 操作 | ns/op | allocs/op | 说明 |
|------|-------|-----------|------|
| Append（单事件） | 489 | 7 | 单 Stream，100 个轮询 |
| Append（批量 100） | 4,349 | 1 | 每次追加 100 个事件 |
| Read（1000 事件） | 5,757 | 11 | 完整 Stream 扫描 |
| ReadAll（10,000 事件） | 36,391 | 3 | 10 Stream x 1000 事件 |
| Subscribe（100 订阅） | 118,892 | 799 | 100 个过滤订阅 |
| Concurrent Append | 707 | 6 | 并行写入，50 Stream |

以上均为 MemoryEventStore 的 benchmark。PostgreSQL 性能取决于连接池、磁盘 I/O 和索引配置。

## 配置

`MemoryEventStore` 无需外部配置。

`PostgresEventStore` 需要传入已有的 `postgres.Pool`：

```go
pool, err := postgres.NewPool(ctx, &postgres.Config{
    Host:     "localhost",
    Port:     5433,
    Database: "ARES",
    User:     "postgres",
    Password: "postgres",
})
if err != nil {
    log.Fatal(err)
}

store := events.NewPostgresEventStore(pool)
```

## 源码文件

| 文件 | 说明 |
|------|------|
| `internal/ares_events/types.go` | Event 结构体、EventType 常量、ReadOptions、EventFilter |
| `internal/ares_events/store.go` | EventStore 接口、NewEventID |
| `internal/ares_events/memory_store.go` | 内存实现 |
| `internal/ares_events/pg_store.go` | PostgreSQL 实现 |
