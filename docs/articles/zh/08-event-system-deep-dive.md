# ares 架构深度解析（八）：事件系统 — 追加式事件日志、压缩归档与任务生命周期（0.3.x）

> 0.3.x 更新：`internal/fabric/task` 引入完整 Task 生命周期事件（created/ready/acquired/started/yielded/checkpointed/preempted/released/completed/failed/expired/stolen）。Task Fabric 把每次状态迁移追加到内存日志，并在挂了 `EventStore` 时写进持久层，方便跨重启重建 Scheduler / Task / Lease 状态。注意：所谓"事件直接驱动调度"是过度宣传——调度器是能力+优先级的 work-stealing 打分，不是"task.completed → 依赖变 Ready"的事件驱动 dispatch。

> Agent 启动是个事件、任务状态迁移是个事件、工具调用是个事件、LLM 返回是个事件、Agent 挂了也会留下一个事件。
> 我当时的想法：如果我把每一种状态变更都记成一条只追加的记录，是不是就能在进程挂了之后，把发生过的事情重放回来？
> 答案是：能重建一部分。但要诚实地说——它**不能**重建全部（见后文"它做不到什么"）。

---

## 一、为什么记事件

我最早做 Agent 时状态就是用内存 struct 存。干到哪一步、处理了什么、报了什么错，全在进程里。进程一挂，什么证据都没有：没有记录、没法回放、没法审计这一步到底是不是越权调的某个工具。

于是我想到了 Event Sourcing：**不存当前状态，存每一次变更操作。想要当前状态？把事件流重放一遍自己算。**

```mermaid
graph LR
    subgraph "传统方式（状态存储）"
        S1[当前状态] -->|UPDATE| S2[新状态]
    end

    subgraph "追加式事件日志"
        E1[Event 1] --> E2[Event 2]
        E2 --> E3[Event 3]
        E3 -->|REPLAY| STATE[推导状态]
    end
```

但这个设计真正能给你的，需要先讲清楚**它做不到什么**——避免读者对着标题产生不切实际的期待：

- ✅ **审计轨迹**：每次状态变更都有时间戳、类型、来源模块（`module_name`）和载荷。
- ✅ **回放**：按流把事件升序读回来，`ares_flight` 的 ReplaySession 可以逐步回放、跳转。
- ✅ **跨重启重建**：`taskfabric` 能从持久事件里重建任务集（`RestoreFromStore`）。
- ⚠️ **它不做**：事件日志本身不承担"精确重放每一步工具调用副作用"的责任——它记录发生了什么，不保证外部世界可回放。
- ⚠️ **它不做**：`taskfabric` 里只有少数生命周期事件会落持久层（见第六章）；大多数状态迁移是"仅观测"的，重启后靠重新编译 DAG 而非重放这些事件来重建拓扑。

核心文件（本次讲解只涉及灰显之外、标记 ✅ 的文件，其余见对应文章）：

| 文件 | 用途 |
|------|------|
| `internal/ares_events/types.go` | Event 模型、EventType 枚举、ReadOptions / EventFilter、哨兵错误 |
| `internal/ares_events/store.go` | EventStore / EventAppender 接口 + `Emit` helper |
| `internal/ares_events/memory_store.go` | 内存实现 `MemoryEventStore`（含订阅、裁剪） |
| `internal/ares_events/pg_store.go` | PostgreSQL 实现 `PostgresEventStore` |
| `internal/ares_events/compactor.go` | 压缩器：把旧事件聚合为摘要 |
| `internal/ares_events/summary.go` | EventSummary 模型 + SummaryRepository 接口 + CompactionConfig |
| `internal/ares_events/compactable_store.go` | 自动压缩的 EventStore 包装器 |
| `internal/ares_events/trim_store.go` | `TrimAwareStore`：压缩后裁剪旧事件 |
| `internal/ares_events/archive_hook.go` | `ArchiveSink`：round 归档钩子 |
| `internal/ares_events/tool_events.go` | 工具完成事件统一载荷键 |
| `internal/fabric/task/events.go` | Task 生命周期 EventType + TaskEvent |
| `internal/fabric/task/fabric.go` | 事件记录 / 持久化 / 恢复逻辑 |
| `internal/runtime/observability/flight/replay.go` | ReplaySession 逐步重放（见系列第 16 篇） |

---

## 二、事件模型

### 2.1 事件结构

`internal/ares_events/types.go` 定义：

```go
type Event struct {
    ID         string         `json:"id"`
    StreamID   string         `json:"stream_id"`
    Type       EventType      `json:"type"`
    ModuleName string         `json:"module_name,omitempty"`
    Payload    map[string]any `json:"payload"`
    Metadata   map[string]any `json:"metadata,omitempty"`
    Version    int64          `json:"version"`
    Timestamp  time.Time      `json:"timestamp"`
}
```

事件属于一个**流**（`StreamID`）——某个实体的只追加序列，通常是一个任务或 agent。`Version` 在 append 时由存储分配并递增（乐观并发控制）。`ModuleName` 记录是哪个子系统发出的（例如 `taskfabric`），回放时你能直接知道来源，而不是从 payload 反推。`Type` 用于路由和分类。

同文件的辅助能力：`VerifyStreamIntegrity` 校验一段事件流版本连续无空洞；`StreamHash` 给整条流算一个确定性哈希用于检测静默损坏；哨兵错误包括 `ErrVersionConflict`、`ErrStreamNotFound`、`ErrEventStoreClosed`、`ErrEventIntegrity`、`ErrSummaryNotFound`。

### 2.2 事件类型（ares_events 侧）

`ares_events.EventType` 是一个字符串别名，枚举覆盖系统各处事件：

```go
// 节选
EventAgentStarted      = "agent.started"
EventTaskCreated       = "task.created"
EventTaskCompleted     = "task.completed"
EventTaskFailed        = "task.failed"
EventSessionCreated    = "session.created"
EventMessageAdded      = "message.added"
EventMemoryDistilled   = "memory.distilled"
EventLLMCall           = "llm.call"
EventToolCallStarted   = "tool.call.started"
EventToolCallCompleted = "tool.call.completed"
// Task 生命周期（taskfabric 发布）
EventTaskReady       = "task.ready"
EventTaskAcquired    = "task.acquired"
EventTaskStarted     = "task.started"
EventTaskYielded     = "task.yielded"
EventTaskCheckpointed= "task.checkpointed"
EventTaskPreempted   = "task.preempted"
EventTaskReleased    = "task.released"
EventTaskExpired     = "task.expired"
EventTaskStolen      = "task.stolen"
// Leader-sub 协作
EventSubTaskResult    = "sub_task.result"
EventSubAgentFailed   = "sub_agent.failed"
// 其他：step.* / discovery.* / component.failed
```

### 2.3 EventStore 接口

`internal/ares_events/store.go`：

```go
type EventStore interface {
    Append(ctx context.Context, streamID string, events []*Event, expectedVersion int64) error
    Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error)
    ReadAll(ctx context.Context, opts ReadOptions) ([]*Event, error)
    Subscribe(ctx context.Context, filter EventFilter) (<-chan *Event, error)
    StreamVersion(ctx context.Context, streamID string) (int64, error)
}
```

关键语义（两个实现一致）：

- `Append(expectedVersion)`：`>0` 必须等于流当前版本，否则返回 `ErrVersionConflict`；`<=0` 表示"自动接在当前版本后追加，不做冲突检查"。两个实现都用**事务/锁**实现，PGC/内存双实现。
- `ReadOptions`：`FromVersion`（含）、`ToVersion`（含，0 = 不限）、`Limit`（0 表示不限）、`Direction`（Ascending/Descending）、`Since`。`ToVersion` 是压缩路径的有界读取（见 4.3）与读路径摘要合并（见 4.5）共用的版本上界。
- 辅助接口 `EventAppender` 只是 `Append` 一个方法的窄接口，供 `Emit` 使用；`Emit(ctx, store, streamID, type, moduleName, payload)` 是唯一的规范发布入口，失败只记一条 warning 并返回 false。

---

## 三、两类存储实现

两个实现都满足同一套接口，语义尽量对齐，但实现路径不同：

```mermaid
classDiagram
    class EventStore {
        <<interface>>
        +Append(ctx, streamID, events, expectedVersion) error
        +Read(ctx, streamID, opts) []*Event
        +ReadAll(ctx, opts) []*Event
        +Subscribe(ctx, filter) <-chan *Event
        +StreamVersion(ctx, streamID) int64
    }

    class MemoryEventStore {
        -mu sync.RWMutex
        -events []*Event
        -streams map[string][]*Event
        -versions map[string]int64
        -subscribers []subscription
        -dropped atomic.Int64
    }

    class PostgresEventStore {
        -pool *Pool
    }

    EventStore <|-- MemoryEventStore
    EventStore <|-- PostgresEventStore
```

### 3.1 MemoryEventStore

`memory_store.go`。加锁 → 版本校验 → 为每个非 nil 事件分配递增 `Version`（nil 事件跳过，不占版本号）→ 写入 `events`（扁平）和 `streams`（按流）→ 通知订阅者。发布给订阅者的是**克隆副本**（B19），因为你下发的 `*Event` 指针和内部存储共享，订阅者若改动会与并发 Read/Append 竞争。

`Subscribe` 的 channel 容量是 **64**；`notifySubscribers` 是**非阻塞发送**——缓冲区满了就静默丢弃并累计到 `dropped` 计数器（`Stats()` 暴露 `dropped_events`，仅用于监控数据丢失，不会阻塞写路径）。订阅者 context 被取消或 store `Close()` 时，订阅 channel 会被关闭并清理。

`MemoryEventStore` 还实现了 `TrimBefore`，让压缩后的裁剪在内存半边也能工作——否则长驻进程里内存无界增长。

### 3.2 PostgresEventStore

`pg_store.go`。`Append` 用一个事务：`SELECT MAX(version) WHERE stream_id = $1` 读当前版本 → 乐观并发检查 → 逐条 `INSERT INTO events (id, stream_id, type, payload, metadata, version, created_at)`。并发的唯一键冲突（PG 错误码 `23505`）会被翻译成 `ErrVersionConflict`。

`Subscribe` 没有用 LISTEN/NOTIFY，而是**每 1 秒 poll 一次**（`defaultEventReadLimit = 100` 的滑动窗口），用 `delivered` id 集合去重（上限 `maxDeliveredIDs = 8192`，溢出后重置集合——最多可能重投旧事件，绝不丢新事件）。这是把"订阅"简化成了"定时查询"，代价是实时性只有秒级。

两个实现的 `Emit` 路径、`ReadOptions` 语义、`expectedVersion` 语义保持一致。

---

## 四、压缩与归档管线

不压缩，事件会无限增长。`Compactor` 把旧事件聚合成紧凑的 `EventSummary`，存进 `SummaryRepository`（关系库），再把原始事件从热存储里裁剪掉。

### 4.1 CompactionConfig 默认值

```go
func DefaultCompactionConfig() CompactionConfig {
    return CompactionConfig{
        Threshold:             500,          // 流超过该事件数触发压缩
        KeepRecent:            100,          // 压缩后保留的最新原始事件数
        MaxSummariesPerStream: 50,           // 每条流最多摘要数
        SummaryTTL:            30 * 24 * time.Hour, // 30 天
        EnableTrimming:        true,         // 压缩后删除已合并的原始事件
    }
}
```

`CompactableEventStore` 是带自动压缩的**包装器**（`compactable_store.go`）：它内嵌 `EventStore`，重写 `Append`，在写入后异步检查是否需要压缩。

> **`SummaryTTL` 语义修复**：`CleanupOldSummaries` 对 `SummaryTTL <= 0` 的处理有变化——旧实现零值直接流入 `threshold := time.Now().Add(-SummaryTTL)`，第一次 cleanup 会删掉所有摘要（零 TTL = "禁用清理" 和 "立刻全部过期" 不可区分）。新逻辑显式判断 `SummaryTTL <= 0` 时直接返回 `(0, nil)`，语义为"禁用清理"。

### 4.2 自动触发 + 防抖

`Append` 只做同步写入，真正的压缩工作**异步**跑（每个事件批次触发一个 goroutine，用 store 自身的生命周期 context `lctx` 而不是调用方 context，避免短请求导致的取消——每次运行仍有 `compactionTimeout = 30s` 上限）：

- `maybeCompact` 先读 `StreamVersion`，**防抖**：只有 `version - lastChecked >= threshold/4`（`compactionCheckDivisor = 4`）才真正检查，避免热流上每次 append 都做 I/O。
- 超阈值就调 `compactor.CheckAndCompact`。
- `Key` 路径全程靠 `lastChecked` map 记录上次检查的版本。

### 4.3 压缩流程

```mermaid
graph TD
    A[Append 写入热存储] --> B{命中 terminator<br/>task.completed/failed?}
    B -->|是| C[DrainPendingRounds<br/>归档本轮 round]
    B -->|否| D[直接进入压缩检查]
    C --> D
    D --> E{version-lastCheck >= threshold/4 ?}
    E -->|否| Z[防抖跳过]
    E -->|是| F[CheckAndCompact]
    F --> G{version <= Threshold ?}
    G -->|是| Z
    G -->|否| H[Read 有界候选<br/>ToVersion = version-KeepRecent]
    H --> I[buildSummary<br/>规则聚合]
    I --> K[repo.Save 摘要]
    K --> K2{MaxSummariesPerStream > 0 ?}
    K2 -->|是| K3[pruneSummaries<br/>删最旧超额摘要]
    K2 -->|否| L
    K3 --> L{EnableTrimming 且挂 trimStore ?}
    L -->|是| M[TrimBefore 裁剪原始事件]
    L -->|否| Z
```

核心点：

- **压缩先落摘要、后裁剪**。裁剪的边界是摘要的 `EndVersion`——保证被剪掉的每个事件都已经进了摘要，不会丢数据。
- **候选读取是有界的**：`compactStream` 用 `ReadOptions{Direction: Ascending, ToVersion: version - KeepRecent}` 只读候选窗口，不再拉取整条流。旧实现无 `ToVersion` 上界，流很长时整个 O(n) 读进内存且候选外的 keep-recent 尾部也白读一遍；有界读把读放大消除，裁剪过的流上也能精确定位（前缀删除只移最早版本，上界自然跳过缺失段）。
- **`MaxSummariesPerStream` 现在有实际强制**：`pruneSummaries` 在摘要落库后排序现有摘要（按 `StartVersion` / `EndVersion` / `CreatedAt` 三级 tie-break，不信任 repo 的顺序契约），把超出上界的**最旧**摘要逐个删除（best-effort，删除失败只记日志、循环继续）。旧版此字段只在配置里写了"超限合并或裁剪"却从未实现，摘要会无限堆积。
- 配置防御：`NewCompactor` 在入口对 `Threshold <= 0` 或 `KeepRecent < 0` 做消毒，直接替换成 `DefaultCompactionConfig()`，避免负数 `KeepRecent` 导致 `events[:total-KeepRecent]` 越界 panic 杀死整个进程（不只是单次请求）。
- `Compactor` 还暴露 `ForceCompact`（无视阈值强压）和 `CleanupOldSummaries`（按 `SummaryTTL` 删过期摘要）。

### 4.4 DefaultSummarizer：规则式摘要

`EventSummarizer` 是个函数类型（`func([]*Event) string`），默认 `DefaultSummarizer` 是纯规则的，不调 LLM。它统计事件数、去重收集 `task.created` 的任务 ID 与 `llm.call` / `tool.call.*` 里的工具名、截取用户请求、按 `task.failed` / `task.completed` 判定 outcome（`failed` / `partial` / `completed` / `active`），输出形如：

```
Agent stream-1, ran 1 task(s) [task-42], called 2 tool(s) [search, calculator], emitted 6 events, duration 1s, bound to user request: "Plan a trip to Tokyo", result: completed
```

（工具列表截断到 5 个、任务列表到 3 个、请求片段到 120 字符、错误最多 3 条——都有硬上限，防止摘要失控。）

两处值得单独提：

- **rune-safe 截断**：`RequestSummary` 与 `DefaultSummarizer` 的请求片段都用 `internal/truncate.WithEllipsis`（按 `[]rune` 截断，再追加 `...` 保证总长不超 `maxLen`）。字节截断会把 CJK 多字节字符拦腰劈开产生 invalid UTF-8，PG `text` 列直接拒写，一个多字节字符落在前 200 字节就永久让压缩失败、流持续膨胀——这是实际踩过的坑。
- **多 Agent 窗口**：`buildSummary` 遍历事件提取 `AgentID`，先记录第一个非空值；若后续事件携带不同的 agent ID 则标记为 `"multi"`，不再静默把整条归因给第一个 agent。`"unknown"` 是没有任何事件携带 agent_id 时的默认。

### 4.5 读取时的摘要回退（v2）

`CompactableEventStore.Read` 的回退比旧版复杂得多，旧版只在底层返回**空**时查摘要；新版把"摘要 + 存活尾部"当一条完整时间线来合并：

1. **触发条件更宽**：底层 `Read` 返回事件后，若调用方的 `FromVersion` 落在最旧存活版本之下（即请求窗口延伸进了裁剪区），也要查 `SummaryRepository`——一条只裁剪了头部的流不能因为"有返回值"就跳过摘要。未被压缩的流（存活事件从 v1 开始）不触发额外 repo 查询，无热路径开销。
2. **合成摘要事件遵守调用方 `ReadOptions`**：`applyReadOptions` 把合成的 `event.summary` 事件按 `FromVersion` / `ToVersion`（含）/ `Since` / `Direction` / `Limit` 过滤排序——旧版回退完全无视这些参数，带 `Limit` 的分页调用拿到的是无界摘要列表，`DESC` 调用拿到升序。
3. **重叠摘要丢弃**：`dropOverlappingSummaries` 会丢掉范围延伸进存活事件版本区间的摘要——并发压缩轮次可能留下摘要覆盖 1–15 而裁剪只删到 11，存活事件 12–14 恰好落在摘要范围内，硬拼会破坏版本序合同；丢弃是语义无损的，因为那些存活事件本身就是摘要所代表的原始事件。摘要区间端点从每个合成事件自己的 payload 里读（`int64` / `int` / `float64` 都兼容 PG JSON 往返），解码失败按 `MaxInt64` 保守处理（丢而不是错拼）。
4. **方向感知的拼接**：`mergeSyntheticWithLive` 升序读摘要在前、降序读摘要在后（摘要的版本天然小于所有存活版本），`Limit` 作用于合并后的切片。

请注意：这是**降级**——你拿到的是摘要（`event.summary`），不是原始事件。

### 4.6 ArchiveSink：round 归档

`compactable_store.go` 通过 `WithArchiveSink(sink)` 挂一个 `ArchiveSink`（`archive_hook.go`，函数类型）。它的职责在**压缩之前**把"一轮任务"（从上一个终端事件到 `task.completed`/`task.failed`）的记录归档，确保这些原始事件在被压缩裁剪**之前**已经留下一份 durable 副本，专门给上下文压缩策略用。

归档是在 `task.completed`/`task.failed` 终态命中（同步检测，异步 goroutine 里跑）或压缩前由 `drainPendingRounds` 分页扫描（每页 `archiveReadLimit = 500`，最多 `maxArchiveDrainRounds = 1000` 轮）完成。新版的三层防御：

- **独占声明（`archiveInflight`）**：旧版边界检查（`lastArchivedVersion` 比较）并非真正的独占——两个 goroutine 可以在 sink 调用前同时观察到相同的 `lastArchivedVersion`，双双通过边界检查、对同一 round 各调一次 sink（round 被归档两次）。新逻辑在 `archiveMu` 下先 CAS `archiveInflight[streamID] = true`，同一 stream 只能有一个 in-flight claimant；声明在函数结束（成功/失败/无事可做）时由 `defer releaseClaim` 统一释放，避免显式释放在竞态下剥掉并发 drainer 的新声明。
- **单轮事件上限（`maxArchiveRoundEvents = 50000`）**：旧版只限单页（500）和单 drain 轮数（1000），但**没有任何上限限制单个 round 积累的事件总量**——一个长期没有终态的长流会在首次压缩时把整条流物化进内存。新逻辑在 roundEvents 达到 50000 时立即返回错误（不是静默 defer），`maybeCompact` 收到此错误后**推迟压缩到下一个窗口**，而不是 trim 掉一个未归档窗口里的原始事件。
- **drain 耗尽是硬错误**：`drainPendingRounds` 跑满 1000 轮上限时返回错误而非 `nil`。旧版返回 `nil` 告诉 `maybeCompact`"归档完成"，导致裁剪掉 RoundRecord 从未落盘的原始事件——永久性丢失。新逻辑下压缩被推迟到后续窗口。

`archiveSink` 的写入失败是 best-effort：边界不前进，下次 drain 重新扫描同一窗口重试 sink（旧版边界在 durable 写入**前**前进，一次 I/O 错误就永久丢掉该轮记录）。

### 4.7 流 bookkeeping TTL sweep（新）

`lastChecked` / `lastTouched` / `roundCounter` / `lastArchivedVersion` 四个 map 各按 stream 永久留存——serve 每个对话 mint 一条流，长驻进程下每条流都留下若干永久条目，随进程生命周期无界增长。

新逻辑在 `maybeCompact` 入口触发 TTL sweep（`streamBookkeepingTTL = 24h` 空闲后清除，`streamBookkeepingSweepInterval = 1min` 控制 sweep 频率，不额外开 goroutine）：清除 `lastChecked` 和 `lastTouched` 里的过期条目，同时在 `archiveMu` 下清除 `roundCounter` 和 `lastArchivedVersion`。重置空闲 stream 的 bookkeeping 在现有设计下是安全的：`lastChecked` 是纯防抖缓存（重置只会多做一次 I/O 检查），`roundCounter` 本来就在每次进程重启时从 1 重来（in-memory only）。

锁序固定为 `mu → archiveMu`（`maybeSweepBookkeepingLocked` 是唯一的持 `mu` 去取 `archiveMu` 的路径），归档路径从不持 `mu` 去取 `archiveMu`，倒置不可能发生。

### 4.8 `Close()` 安全停机（新）

`CompactableEventStore` 的 `lctx`（`context.Background()` 派生，`Close` 调用 `lcancel`）是 background compaction workers 的生命周期 context，与任何 `Append` 调用方的 ctx 解耦——短请求的 ctx 返回后立即取消，但 lctx 让压缩工作可以跨请求存活；每次运行仍受 `compactionTimeout = 30s` 上限约束。

`Close()` 的三步停机序列：

1. 置 `closed = true`（`mu` 下），取消 `lctx`；
2. 等待 in-flight workers（`wg.Wait()`，有 `compactionTimeout + 5s` 兜底超时，不无限阻塞）；
3. 若底层 `EventStore` 实现 `io.Closer`，调用其 `Close()`。

`Append` 在 `mu` 下检查 `closed` 标志：`Close` 已开始则跳过 launch（避免 WaitGroup Add-after-Wait），`Append` 赢了 race 则 `Close` 的 `wg.Wait` 覆盖该 worker。

---

## 五、Task Fabric：任务生命周期事件

`internal/fabric/task/events.go` 定义了一个**独立于** `ares_events` 的 `EventType` 枚举：

```go
const (
    EventTaskCreated      EventType = "task.created"
    EventTaskReady        EventType = "task.ready"
    EventTaskAcquired     EventType = "task.acquired"
    EventTaskStarted      EventType = "task.started"
    EventTaskYielded      EventType = "task.yielded"
    EventTaskCheckpointed EventType = "task.checkpointed"
    EventTaskPreempted    EventType = "task.preempted"
    EventTaskReleased     EventType = "task.released"
    EventTaskCompleted    EventType = "task.completed"
    EventTaskFailed       EventType = "task.failed"
    EventTaskExpired      EventType = "task.expired"
    EventTaskStolen       EventType = "task.stolen"
    EventTaskUpdated      EventType = "task.updated" // 仅观测，不落盘
)
```

`TaskEvent` 是那条**内存日志**里的不可变记录：`{Type, TaskID, AgentID, Origin, State, Checkpoint, At}`。fabric 每个状态迁移都会 `append` 进内存日志（`f.events`，有上限 `maxInMemoryEvents`）。

### 5.1 状态迁移 → 事件 映射

```mermaid
stateDiagram-v2
    [*] --> Ready: Create -> task.created (持久)
    Ready --> Leased: Acquire -> task.acquired (仅观测) + task.ready (仅观测)
    Leased --> Running: Start -> task.started (仅观测)
    Running --> Suspended: Yield -> task.yielded (仅观测) / Checkpoint -> task.checkpointed (持久)
    Suspended --> Leased: Release/Resume -> task.released (仅观测)
    Running --> Done: Complete -> task.completed (持久)
    Running --> Done: Fail -> task.failed (持久)
    Leased --> Done: Expire -> task.expired (持久)
    Leased --> Done: Steal -> task.stolen (仅观测) / Preempt -> task.preempted (仅观测)
    Done --> [*]
```

> 这张图是"状态迁移 → 事件"的**意图示意**，标注了每个事件是否落持久层。具体每个迁移跑在哪个函数里、准确的状态集合，请以 `internal/fabric/task` 的 state machine 为准，本文只陈述事件这一侧的事实（含持久与否），不展开全部状态机细节。

### 5.2 哪些落盘，哪些不落盘

fabric 把事件记进内存日志，**只有当挂了 `EventStore` 时**（`WithEventStore`）才会往持久层写，并且不是每个事件都写。`isMustPersistEvent` 决定：

```go
// 必须持久（恢复/重放正确性依赖它们）
TaskCreated, TaskCheckpointed, TaskCompleted, TaskFailed, TaskExpired

// 仅观测（丰富轨迹，但重建状态不依赖）
TaskReady, TaskAcquired, TaskStarted, TaskYielded,
TaskPreempted, TaskReleased, TaskStolen
```

机制：`recordLocked` 在持锁时构建内存事件 + 一个 `pendingAppend`（不碰 I/O）；解锁后 `flushAppends` 再把 durable 写入**锁外**执行，并通过 `flushCond` + 单调 `seq` 保证并发 fabric 调用落盘顺序与记录顺序一致（N7），避免按流版本倒挂。每一个持久事件都带上 `module_name: "taskfabric"`，payload 里包括 `task_id / agent_id / origin / epoch / strategy_id / session_id`；**must-persist** 事件额外携带完整恢复字段（capability / priority / dependencies / deadline / retry / created_at / checkpoint JSON）——`RestoreFromStore` 正是靠这些字段 `foldRestoreEvent` 把任务折叠回来。

持久写失败：must-persist 事件会记 `Error` 日志（可检测到内存与事件日志的 divergence），观测事件则静默 best-effort。进程内的状态机始终是权威，append 失败不会回滚迁移。

### 5.3 EventTaskUpdated：增量重写，只观测不落盘

`taskfabric` 的增量编译器在改一个任务的调度形态（`Dependencies`）或 payload 时，会记一条 `EventTaskUpdated`。但这条事件**特意不落盘**（`events.go` 注释明说 "BY DESIGN"）：

- 它不在 `isMustPersistEvent` 里；
- 它在 `taskEventType` 映射里**没有对应项**（映射返回 `""`，fabric 永不 publish）。

原因：这些是**原位重写**（`SetDependencies` 一句话搬动一个任务，而不是重新编译整批）。重启后拓扑是**重新编译 live DAG** 重建的，而不是回放这些重写事件。把它纳入跨重启协议属于协议变更，不是编译器变更。这一点值得你留意——如果期望"事件日志是全部真相"，`EventTaskUpdated` 会打破这个预期。

---

## 六、它做不到什么（诚实清单）

1. **不是每个生命周期事件都落盘**。Ready/Started/Yielded/Stolen 等是仅观测的；`EventTaskUpdated` 压根不进持久层。跨重启重建依赖 must-persist 那五类 + 重新编译 DAG。
2. **Postgres 订阅是 1 秒轮询**，不是实时推送。Realtime 想推给 Dashboard，得自己叠加 SSE（事件层本身不提供）。
3. **只记"发生什么"，不保证"可重放副作用"**。工具调用副作用（发邮件、写库）发生后，事件只是事后记录，不会替你撤销或重放外部效果。
4. **压缩后的读回退是降级**：拿到 `event.summary` 合成事件，不是原始事件。要原始事件得靠归档/裁剪前的副本。注意：新版回退正确遵守调用方的 `ReadOptions`（版本窗口 / 方向 / Limit），且会丢弃与存活事件版本区间重叠的摘要（`dropOverlappingSummaries`），不再是无界升序列表。
5. **写路径不阻塞**：压缩、归档都是异步 best-effort，失败只记日志；`dropped_events` 计数器只暴露给监控，不保证不丢。但归档的 **drain cap 耗尽是硬错误**（`drainPendingRounds` 返回 error 而非 nil），`maybeCompact` 收到后会推迟压缩，不再 trim 掉 RoundRecord 尚未落盘的原始事件。

---

## 七、重放与恢复

### 7.1 ReplaySession

`internal/runtime/observability/flight/replay.go` 的 `NewReplaySession(ctx, eventStore, taskID)` 把某个任务的流升序读进来做逐步回放。详见系列第 16 篇（Flight Recorder），这里只提它的存在与依赖。

### 7.2 Task Fabric 跨重启重建

```mermaid
sequenceDiagram
    participant F as Fabric
    participant ES as EventStore (Postgres)
    participant R as Scheduler

    Note over F: 启动
    F->>F: WithEventStore(store)
    F->>ES: ReadAll / 按流读事件
    ES-->>F: task.* 事件流
    loop 每条事件
        F->>F: foldRestoreEvent(payload)
        F->>F: 重建 Task / Lease / 调度状态
    end
    F-->>R: RESTORE 后的 ReadyTasks 可被调度
```

### 7.3 与"Agent 复活"的关系

Agent 复活（两阶段恢复、快照优先、事件流回退降级）是**第 7 篇**（Runtime / Resurrection）的主题：`internal/runtime/recovery.go` 的 `RecoverSnapshotOrEvents()` 先快照后事件流，`event_recovery.go` 从事件流重建 RecoveryState。事件系统在这里扮演的是"回退数据源"角色——**这属于 ares_runtime 对事件的消费**，不是事件系统自身的能力，别归错账。

---

## 八、设计模式总结

| 模式 | 位置 | 用途 |
|------|------|------|
| 追加式事件日志 | `types.go` | 不可变只追加记录（含流内版本） |
| 乐观并发控制 | `Append(expectedVersion)` | `>0` 必须匹配当前版本，冲突返回 `ErrVersionConflict` |
| CQRS（衰减版） | EventStore（写/热存储）+ SummaryRepository（摘要） | 压缩后读取回退摘要，方向/分页感知（`applyReadOptions`） |
| 观察者 / 订阅 | `Subscribe(EventFilter)` | 内存：非阻塞广播；PG：1 秒轮询推送 |
| 策略 | `EventSummarizer` 函数类型 | 可插拔摘要（默认规则式） |
| 装饰器 | `CompactableEventStore` 包装 `EventStore` | 透明自动压缩，Append 接口不变 |
| 防抖 + TTL sweep | `lastChecked` + `threshold/4`；`streamBookkeepingTTL` | 减少热流上的重复压缩检查；长驻进程 bookkeeping map 无界增长防御 |
| CAS 独占声明 | `archiveInflight`（`archiveMu` 下） | 同一 stream 的 round 归档并发去重，防双写 |
| 双写解耦 | `recordLocked`（持锁）+ `flushAppends`（锁外） | 事件持久写不阻塞 fabric 状态机 |
| 有界读取 | `ReadOptions.ToVersion`（`compactStream` 候选窗口） | 压缩只读候选段，不拉全流，长流防 OOM |
| 安全停机 | `CompactableEventStore.Close()` | `lctx` 取消 → `wg.Wait`（有上限）→ 底层 `Close` |

### 关键数据流

```mermaid
graph TB
    subgraph "写路径"
        F[taskfabric\nrecordLocked] -->|pendingAppend| FE[flushAppends 锁外序化]
        FE -->|Append| CES[CompactableEventStore]
        CES -->|写热存储| ES[EventStore]
        CES -->|异步 lctx| CP[Compactor]
        CP -->|有界读 ToVersion| ES
        CP -->|buildSummary| SR[(SummaryRepository)]
        CP -->|pruneSummaries 超额最旧| SR
        CP -->|可选裁剪| TS[TrimBefore]
    end

    subgraph "读路径"
        FL[ReplaySession / describe] -->|Read| CES
        CES -->|有原始事件?| ES
        CES -->|窗口延伸进裁剪区? 查摘要| SR
        CES -->|dropOverlappingSummaries +\nmergeSyntheticWithLive| FL
    end
```

---

## 九、结尾

Event Sourcing 的核心价值不是"跑得飞快"，而是**出事之后能拿到一份按顺序排列的、谁在什么时候干了什么的记录**，并且这份记录能支撑重放和跨重启重建。它也有一堆边界——不是每个事件都落盘、订阅是秒级轮询、外部副作用不可重放。这些边界我在这篇里照实写了，因为它们才是真实系统里你会踩到的坑。

**事件系统不让你跑得更快，它让你在出事之后，知道该查哪里、能查到什么。**

---

*下一篇预告：Arena / 故障注入——你可能从 Dashboard 上一个按钮"暗杀"正在工作的 Agent，然后看它秽土转生。那是对"状态系统到底能恢复到多完整"最直接的压力测试。*