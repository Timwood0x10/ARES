# AHP Protocol Design Document

## 1. Protocol Overview

AHP (Agent Heartbeat Protocol) is the custom communication protocol for the Style Agent framework, used for message passing between Leader Agent and Sub Agents.

## 2. Message Types

| Message Type | Direction | Description |
|--------------|-----------|-------------|
| TASK | Leader → Sub | Task dispatch |
| RESULT | Sub → Leader | Return result |
| PROGRESS | Sub → Leader | Progress report |
| ACK | Sub → Leader | Acknowledgment |
| HEARTBEAT | All → All | Heartbeat keep-alive |

## 3. Message Format

```go
type AHPMessage struct {
    MessageID   string                 `json:"message_id"`   // Unique message ID
    Method      AHPMethod              `json:"method"`       // Message type
    AgentID     string                 `json:"agent_id"`     // Sender Agent ID
    TargetAgent string                 `json:"target_agent"` // Receiver Agent ID
    TaskID      string                 `json:"task_id"`      // Task ID
    SessionID   string                 `json:"session_id"`   // Session ID
    Payload     map[string]interface{} `json:"payload"`      // Message content
    Timestamp   time.Time              `json:"timestamp"`    // Timestamp
}
```

## 4. Message Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Message Lifecycle                             │
└─────────────────────────────────────────────────────────────────┘

   [Send]                    [Queue]                   [Receive/Process]
   
┌─────────┐              ┌─────────┐                ┌─────────┐
│ Leader  │  TASK       │  MQ     │                │ Sub     │
│         │ ─────────▶  │         │  ─────────▶    │  Agent  │
│         │              │         │                │         │
│         │ ◀─────────  │         │ ◀─────────     │         │
└─────────┘   RESULT    └─────────┘    ACK         └─────────┘
```

## 5. Queue Design

### Queue Structure

```go
type MessageQueue struct {
    // Independent queue for each Agent
    queues map[string]chan *AHPMessage
    
    // Global broadcast queue
    broadcast chan *AHPMessage
    
    // Dead letter queue
    dlq chan *AHPMessage
    
    mu sync.RWMutex
}
```

### Queue Operations

```go
// Send sends a message
func (q *MessageQueue) Send(ctx context.Context, msg *AHPMessage) error

// Receive receives a message
func (q *MessageQueue) Receive(ctx context.Context, agentID string) (*AHPMessage, error)

// Broadcast broadcasts a message
func (q *MessageQueue) Broadcast(ctx context.Context, msg *AHPMessage) error

// SendToDLQ sends to dead letter queue
func (q *MessageQueue) SendToDLQ(ctx context.Context, msg *AHPMessage) error
```

## 6. Message Serialization

Supports both JSON and Protobuf serialization:

```go
type Serializer interface {
    Marshal(msg *AHPMessage) ([]byte, error)
    Unmarshal(data []byte) (*AHPMessage, error)
}

// JSON Serializer
type JSONSerializer struct{}

func (s *JSONSerializer) Marshal(msg *AHPMessage) ([]byte, error)
func (s *JSONSerializer) Unmarshal(data []byte) (*AHPMessage, error)

// Protobuf Serializer
type ProtobufSerializer struct{}

func (s *ProtobufSerializer) Marshal(msg *AHPMessage) ([]byte, error)
func (s *ProtobufSerializer) Unmarshal(data []byte) (*AHPMessage, error)
```

## 7. Timeout and Retry

| Scenario | Timeout | Retry Strategy |
|----------|---------|----------------|
| Message Send | 5s | Exponential backoff |
| Task Processing | 60s | 3 retries |
| Heartbeat | 10s | 3 consecutive misses = offline |
| ACK Confirm | 3s | Auto resend |

## 8. Dead Letter Queue (DLQ)

### Trigger Conditions

- Message processing failed and exceeded max retries
- Target Agent does not exist
- Task dependency cycle detected

### DLQ Message Format

```go
type DLQMessage struct {
    OriginalMsg  *AHPMessage `json:"original_msg"`
    ErrorCode    string      `json:"error_code"`
    ErrorMessage string      `json:"error_message"`
    RetryCount   int         `json:"retry_count"`
    Timestamp    time.Time   `json:"timestamp"`
}
```

## 9. Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| queue_buffer_size | 100 | Queue buffer size |
| dlq_size | 500 | Dead letter queue size |
| message_timeout | 30s | Message timeout |
| heartbeat_interval | 10s | Heartbeat interval |
| max_retries | 3 | Max retry attempts |

## 10. Callback Mechanism (v2)

The `HeartbeatMonitor` supports registering callbacks that fire when an agent is marked offline by `CheckTimeouts`. This decouples the timeout detection logic from downstream recovery actions.

### TimeoutCallback Type

```go
// TimeoutCallback is invoked when an agent is marked offline by CheckTimeouts.
type TimeoutCallback func(agentID string)
```

### RegisterCallback

```go
func (m *HeartbeatMonitor) RegisterCallback(fn TimeoutCallback) {
    if fn == nil {
        return
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    m.callbacks = append(m.callbacks, fn)
}
```

### Callback Invocation Flow

`CheckTimeouts` splits the work into two phases to avoid holding the write lock during callback execution:

```go
func (m *HeartbeatMonitor) CheckTimeouts() []string {
    // Phase 1: mark agents offline under write lock
    timedOut := m.checkAndMarkOffline()

    // Phase 2: invoke callbacks outside the lock (prevents deadlocks)
    for _, agentID := range timedOut {
        m.notifyCallbacks(agentID)
    }
    return timedOut
}
```

`notifyCallbacks` copies the callback slice under a read lock before calling each function, so callbacks may safely re-register or inspect monitor state:

```go
func (m *HeartbeatMonitor) notifyCallbacks(agentID string) {
    m.mu.RLock()
    callbacks := make([]TimeoutCallback, len(m.callbacks))
    copy(callbacks, m.callbacks)
    m.mu.RUnlock()

    for _, fn := range callbacks {
        fn(agentID)
    }
}
```

## 11. Supervisor Integration

`LeaderSupervisor` uses the callback mechanism to trigger failover when a leader's heartbeat times out. During `Start`, it registers its `handleFailover` method as a callback:

```go
// LeaderSupervisor.Start
s.heartbeatMon.RegisterCallback(s.handleFailover)

s.g.Go(func() error {
    ticker := time.NewTicker(s.config.CheckInterval)
    defer ticker.Stop()
    for {
        select {
        case <-s.gctx.Done():
            return nil
        case <-ticker.C:
            s.heartbeatMon.CheckTimeouts()
        }
    }
})
```

The `handleFailover` callback launches an asynchronous failover via `errgroup`:

```go
func (s *LeaderSupervisor) handleFailover(leaderID string) {
    s.mu.RLock()
    stopped := s.stopped
    g := s.g
    gctx := s.gctx
    s.mu.RUnlock()

    if stopped || g == nil {
        return
    }
    g.Go(func() error {
        s.doFailover(gctx, leaderID)
        return nil
    })
}
```

The failover sequence in `doFailover`:
1. Stop the old leader agent
2. Retrieve the latest checkpoint for the leader
3. Invoke the `FailoverStrategy` (up to `MaxFailoverAttempts` times)
4. Register the new leader agent
5. Recover stale tasks from the checkpoint session

### ColdRestartStrategy

The default `FailoverStrategy` creates a fresh leader instance via a factory function:

```go
type ColdRestartStrategy struct {
    factory     func(ctx context.Context, config interface{}) (base.Agent, error)
    agentConfig interface{}
}

func (s *ColdRestartStrategy) HandleFailover(
    ctx context.Context,
    leaderID string,
    checkpoint *LeaderCheckpoint,
) (base.Agent, error) {
    config := s.agentConfig
    if config == nil && checkpoint != nil && len(checkpoint.Metadata) > 0 {
        config = checkpoint.Metadata
    }

    agent, err := s.factory(ctx, config)
    if err != nil {
        return nil, errors.Wrap(err, "cold restart strategy: create agent")
    }
    if err := agent.Start(ctx); err != nil {
        return nil, errors.Wrap(err, "cold restart strategy: start agent")
    }
    return agent, nil
}
```

## 12. HeartbeatSender

`HeartbeatSender` runs a background goroutine that periodically enqueues `HEARTBEAT` messages into the agent's `MessageQueue`.

```go
type HeartbeatSender struct {
    agentID  string
    interval time.Duration
    queue    *MessageQueue
    ctx      context.Context
    cancel   context.CancelFunc
    wg       sync.WaitGroup
    stopOnce sync.Once
    started  bool
    stopped  bool
    mu       sync.Mutex
}
```

### Usage

```go
sender := ahp.NewHeartbeatSender(agentID, 5*time.Second, queue)
if err := sender.Validate(); err != nil {
    log.Fatal(err)
}
sender.Start(ctx)
// ... agent runs ...
sender.Stop() // graceful shutdown, waits for goroutine
```

`Start` can be called again after `Stop`, making the sender reusable across restart cycles. `Stop` uses `sync.Once` to ensure the cancel/wait sequence runs exactly once even under concurrent calls.
