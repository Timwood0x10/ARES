# GoAgentX Architecture Deep Dive (2): Agent Harmony Protocol — The Communication Bedrock for Multi-Agent Systems

## Introduction

In the previous article, we explored the overall architecture of GoAgentX. This article focuses on one of its core components — the **Agent Harmony Protocol (AHP)**, the infrastructure that powers all inter-agent communication in GoAgentX.

AHP is an **in-process, in-memory messaging protocol** designed specifically for multiple Agent instances running within the same Go process. Unlike distributed scenarios, AHP doesn't traverse the network — it relies on Go channels and a shared `HeartbeatMonitor` instance.

## Why AHP is Needed

In GoAgentX's architecture, there are two roles: Leader Agent and Sub Agent. The Leader handles task decomposition and scheduling, while the Sub handles execution. The communication between them must satisfy:

- **Asynchronous messaging**: The Leader doesn't block waiting for Sub execution to complete
- **Progress feedback**: Sub can push execution progress proactively
- **Heartbeat detection**: The Leader needs to know whether a Sub is alive
- **Fault tolerance**: Failed message delivery needs a fallback mechanism

GoAgentX chose an **in-process protocol** over a distributed message queue for these reasons:

1. **Ultra-low latency**: Channel operations vs network RTT — orders of magnitude faster
2. **Simple semantics**: No serialization overhead, network jitter, or partition tolerance issues
3. **Evolution-friendly**: AHP serves as an internal abstraction layer; the underlying transport can be swapped to gRPC/RabbitMQ for distributed deployments without affecting upper-layer business logic

## Global Architecture

```
  Leader Agent                    Sub Agent
       |                              |
       |  SendMessage(Enqueue)        |
       |----------------------------->|
       |                              |
       |  ReceiveMessage(Dequeue)     |
       |<-----------------------------|
       |                              |
       |   HeartbeatMonitor           |
       |   (shared in-process)        |
       |       |                      |
       |       +-- RecordHeartbeat ---|
       |       |                      |
       |       +-- CheckTimeouts ---->|
       |       +-- TimeoutCallback -->|
       |                              |
       |   Dead Letter Queue          |
       |   (failed messages)          |
       +------------------------------+
```

| Component | Responsibility | Highlights |
|-----------|---------------|------------|
| `Protocol` | Unified facade, composes all sub-components | Facade pattern, one-stop interface |
| `MessageQueue` | Per-agent message queue | Buffered channel + backup buffer + atomic.Bool |
| `HeartbeatMonitor` | Heartbeat detection + timeout callbacks | Shared instance, no external dependencies |
| `DLQ` | Dead letter storage and retry | Custom handlers + auto retry |
| `QueueRegistry` | Manages all agent queues | Lazy initialization + double-checked locking |
| `Codec` | Message serialization | JSON implementation, extensible via CodecRegistry |

## Message Model

### Five Message Types

```go
const (
    AHPMethodTask      AHPMethod = "TASK"      // Task assignment
    AHPMethodResult    AHPMethod = "RESULT"     // Task result
    AHPMethodProgress  AHPMethod = "PROGRESS"   // Progress update
    AHPMethodACK       AHPMethod = "ACK"        // Acknowledgment
    AHPMethodHeartbeat AHPMethod = "HEARTBEAT"  // Liveness signal
)
```

### Message Structure

```go
type AHPMessage struct {
    MessageID   string         `json:"message_id"`
    Method      AHPMethod      `json:"method"`
    AgentID     string         `json:"agent_id"`
    TargetAgent string         `json:"target_agent"`
    TaskID      string         `json:"task_id"`
    SessionID   string         `json:"session_id"`
    Payload     map[string]any `json:"payload"`
    Timestamp   time.Time      `json:"timestamp"`
}
```

### MessageID Generation

The MessageID is a three-part composite:

```go
func generateMessageID() string {
    id := atomic.AddUint64(&messageIDCounter, 1)
    randSuffix := getRandomSuffix()
    return fmt.Sprintf("%s.%d.%s",
        time.Now().Format("20060102150405.000000"), id, randSuffix)
}
```

- **Timestamp prefix**: Human-readable, traceable
- **Atomic counter**: Monotonic sequence within the same nanosecond
- **Random suffix**: Collision-free across multiple processes

### Constructor Functions

```go
NewMessage(method, agentID, targetAgent, taskID, sessionID)
NewTaskMessage(agentID, targetAgent, taskID, sessionID, payload)
NewResultMessage(agentID, targetAgent, taskID, sessionID, result)
NewProgressMessage(agentID, targetAgent, taskID, sessionID, progress)
NewACKMessage(agentID, targetAgent, taskID, sessionID)
NewHeartbeatMessage(agentID)
```

`GetResult()` handles a tricky problem: after JSON deserialization, `TaskResult` loses its type and becomes `map[string]any`. The method's `reconstructTaskResult` function uses reflection and field mapping to rebuild the original struct — a classic solution to JSON polymorphism serialization.

## MessageQueue

### Core Implementation

```go
type MessageQueue struct {
    messages     chan *AHPMessage
    agentID      string
    opts         *QueueOptions
    backupBuffer []*AHPMessage
    backupMu     sync.Mutex
    closed       atomic.Bool
    closeOnce    sync.Once
}
```

### Enqueue: Non-Blocking Write

```go
func (q *MessageQueue) Enqueue(ctx context.Context, msg *AHPMessage) (retErr error) {
    if q.closed.Load() { return errors.ErrQueueClosed }
    defer func() {
        if r := recover(); r != nil { retErr = errors.ErrQueueClosed }
    }()
    select {
    case q.messages <- msg:
        return nil
    default:
        return errors.ErrQueueFull
    }
}
```

Key design choices:
1. **Non-blocking**: Returns `ErrQueueFull` immediately when the channel is full
2. **atomic.Bool**: Lock-free closed-state check
3. **defer recover**: Gracefully handles `send on closed channel` panics
4. **ctx parameter unused**: The `default` branch never selects `ctx.Done()`

### Dequeue: Blocking Read

```go
func (q *MessageQueue) Dequeue(ctx context.Context) (*AHPMessage, error) {
    q.backupMu.Lock()
    if len(q.backupBuffer) > 0 {
        msg := q.backupBuffer[0]
        q.backupBuffer = q.backupBuffer[1:]
        q.backupMu.Unlock()
        return msg, nil
    }
    q.backupMu.Unlock()
    select {
    case msg, ok := <-q.messages:
        if !ok { return nil, errors.ErrQueueClosed }
        return msg, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}
```

### Peek and Backup Buffer

`Peek()` inspects the head of the queue without removing the message. The challenge: once you take a message from a channel, you can't put it back if the channel is full. The solution is a `backupBuffer` — `Dequeue` checks the backup buffer first, ensuring no message is lost.

### QueueRegistry

Uses **Double-Checked Locking** for performance:

```go
func (r *QueueRegistry) GetOrCreate(agentID string) *MessageQueue {
    r.mu.RLock()
    q, ok := r.queues[agentID]
    r.mu.RUnlock()
    if ok { return q }
    r.mu.Lock()
    defer r.mu.Unlock()
    if q, ok := r.queues[agentID]; ok { return q }
    q = NewMessageQueue(agentID, r.defaultOpts)
    r.queues[agentID] = q
    return q
}
```

## HeartbeatMonitor

### Core Flow

- Agents send heartbeats at a fixed interval (default 5s)
- HeartbeatMonitor records the last heartbeat time
- If the timeout is exceeded (default 30s) and `MissedCount >= MaxMissed` (default 3), the agent is marked offline

### Timeout Detection Algorithm

```go
func (m *HeartbeatMonitor) CheckTimeouts() []string {
    timedOut := m.checkAndMarkOffline()  // Under write lock
    for _, agentID := range timedOut {
        m.notifyCallbacks(agentID)        // Outside the lock
    }
    return timedOut
}
```

Critical edge cases:
1. **Gradual timeout**: 3 missed heartbeats before declaring offline — avoids false kills from transient network delays
2. **No duplicate callbacks**: Already-offline agents won't trigger callbacks again
3. **Callbacks outside the lock**: `notifyCallbacks` copies the callback slice under a read lock, releases it, then invokes callbacks. This prevents deadlocks.

### Two HeartbeatSenders

1. **`ahp.HeartbeatSender`**: Sends `AHPMethodHeartbeat` messages into `MessageQueue` — **in-band** heartbeat
2. **`heartbeatSender`** (in `internal/agents/sub/`): Calls `HeartbeatMonitor.RecordHeartbeat` directly — **out-of-band** heartbeat

Currently, Sub Agents use the second approach, which is more efficient in monolithic deployments.

## Dead Letter Queue

When `Enqueue` fails, `Protocol.SendMessage` routes the failed message to DLQ:

```go
func classifyEnqueueError(err error) string {
    switch {
    case errors.Is(err, apperrors.ErrQueueClosed):  return "queue_closed"
    case errors.Is(err, apperrors.ErrQueueFull):    return "queue_full"
    case errors.Is(err, context.Canceled):          return "context_canceled"
    case errors.Is(err, context.DeadlineExceeded):  return "context_deadline"
    default:                                        return "unknown"
    }
}
```

`DLQProcessor` supports custom handlers per error type and automatic retry:

- `MaxRetries = 0`: Unlimited retries
- `MaxRetries > 0`: Exhausted after the configured count
- No exponential backoff — a potential improvement area

## Protocol Facade

```go
type Protocol struct {
    registry  *QueueRegistry
    dlq       *DLQ
    codec     Codec
    heartbeat *HeartbeatMonitor
    config    *ProtocolConfig
}
```

| Method | Purpose |
|--------|---------|
| `SendMessage(ctx, msg)` | Send message, auto-route to DLQ on failure |
| `ReceiveMessage(ctx, agentID)` | Receive message, blocking |
| `SendTask/SendResult` | Convenience wrappers |
| `RecordHeartbeat(agentID)` | Record heartbeat |
| `CheckTimeouts()` | Check for timed-out agents |
| `Stats()` | Snapshot of runtime state |
| `Close()` | Shutdown all resources |

## Agent Integration

### Messenger Interface

```go
type Messenger interface {
    SendMessage(ctx context.Context, msg *ahp.AHPMessage) error
    ReceiveMessage(ctx context.Context) (*ahp.AHPMessage, error)
}
```

Both `leaderAgent` and `subAgent` implement this, with `MessageQueue` and `HeartbeatMonitor` injected via constructors.

### Dispatcher Task Distribution

`taskDispatcher` supports both **local execution** and **distributed dispatch**:

```go
if executor, ok := d.executorFuncs[task.Type]; ok {
    return executor(ctx, task, agentAddr, sessionID)
}
if d.messageSender == nil { /* return error */ }
msg := ahp.NewTaskMessage(...)
d.messageSender.Send(ctx, agentAddr, msg)
return d.waitForResult(ctx, task.TaskID)
```

This design allows seamless switching between monolithic and distributed deployment.

## Design Patterns Summary

| Pattern | Location | Description |
|---------|----------|-------------|
| **Facade** | `Protocol` | Unified interface over all components |
| **Registry** | `QueueRegistry`, `CodecRegistry` | Named instance management, lazy init |
| **Strategy** | `Codec` interface | Pluggable serialization |
| **Observer** | `TimeoutCallback` | Heartbeat timeout callbacks |
| **Dead Letter Queue** | `DLQ` + `DLQProcessor` | Failed message storage and retry |
| **Double-Checked Locking** | `GetOrCreate` | Performance + correctness |
| **Panic Recovery** | `Enqueue` | `defer recover()` for concurrent close |
| **Lock-Free Read** | `atomic.Bool` | Lock-free closed flag check |

## Key Design Decisions

### Why Non-Blocking Enqueue?

- Agents run in a multi-threaded environment — blocking can cause cascading waits
- DLQ provides better fault-tolerance semantics — failed messages can be retried
- The caller has full control: immediate retry, deferred retry, or discard

### TOCTOU Prevention

`SendMessage` intentionally **does not check `IsFull` before enqueuing**. A check-before-send approach would create a TOCTOU race: the queue could go from non-full to full between the check and the send. Direct execution with error handling is more robust.

### Codec Extensibility

Although AHP is currently in-process only, the `Codec` interface was designed for:
1. **Cross-process communication**: protobuf/msgpack for smaller payloads
2. **Persistence**: Binary formats for DLQ message storage

## Limitations and Future Directions

1. **In-process only**: No cross-process communication. Replacing MessageQueue with a network transport layer is needed for distributed evolution
2. **No multicast/broadcast**: Messages are point-to-point. Distributing tasks to multiple Subs requires individual sends
3. **No exponential backoff**: DLQ retries use a fixed interval, potentially causing retry storms under sustained failures
4. **Static routing**: No content-based routing or topic subscriptions

## Summary

The Agent Harmony Protocol is the communication bedrock of GoAgentX's multi-agent system. High-performance message passing through Go channels, reliable fault tolerance via DLQ, and liveness detection through HeartbeatMonitor form a complete, production-ready communication infrastructure. The extensibility hooks — Codec interface, DLQ handler registration, and MessageSender abstraction — pave the way for GoAgentX's evolution from monolithic to distributed architecture.

Next: **Memory Distillation** — how GoAgentX learns to forget and refine experiences from conversation history.