# AHP Protocol 设计文档

## 1. 协议概述

AHP (Agent Heartbeat Protocol) 是 Style Agent 框架的自定义通信协议，用于 Leader Agent 与 Sub Agent 之间的消息传递。

## 2. 消息类型

| 消息类型 | 英文 | 方向 | 说明 |
|----------|------|------|------|
| TASK | Task | Leader → Sub | 派发任务 |
| RESULT | Result | Sub → Leader | 返回结果 |
| PROGRESS | Progress | Sub → Leader | 进度汇报 |
| ACK | Acknowledgment | Sub → Leader | 确认收到 |
| HEARTBEAT | Heartbeat | All → All | 心跳保活 |

## 3. 消息格式

```go
type AHPMessage struct {
    MessageID   string                 `json:"message_id"`   // 消息唯一ID
    Method      AHPMethod              `json:"method"`       // 消息类型
    AgentID     string                 `json:"agent_id"`     // 发送方 Agent ID
    TargetAgent string                `json:"target_agent"` // 接收方 Agent ID
    TaskID      string                 `json:"task_id"`      // 任务ID
    SessionID   string                 `json:"session_id"`   // 会话ID
    Payload     map[string]interface{} `json:"payload"`      // 消息内容
    Timestamp   time.Time              `json:"timestamp"`    // 时间戳
}
```

## 4. 消息流转

```
┌─────────────────────────────────────────────────────────────────┐
│                      消息生命周期                                 │
└─────────────────────────────────────────────────────────────────┘

   [发送]                    [队列]                     [接收/处理]
   
┌─────────┐              ┌─────────┐                ┌─────────┐
│ Leader  │  TASK       │  MQ     │                │ Sub     │
│         │ ─────────▶  │         │  ─────────▶    │  Agent  │
│         │              │         │                │         │
│         │ ◀─────────  │         │ ◀─────────     │         │
└─────────┘   RESULT    └─────────┘    ACK         └─────────┘
```

## 5. 队列设计

### 队列结构

```go
type MessageQueue struct {
    // 每个 Agent 独立的队列
    queues map[string]chan *AHPMessage
    
    // 全局广播队列
    broadcast chan *AHPMessage
    
    // 死信队列
    dlq chan *AHPMessage
    
    mu sync.RWMutex
}
```

### 队列操作

```go
// Send 发送消息
func (q *MessageQueue) Send(ctx context.Context, msg *AHPMessage) error

// Receive 接收消息
func (q *MessageQueue) Receive(ctx context.Context, agentID string) (*AHPMessage, error)

// Broadcast 广播消息
func (q *MessageQueue) Broadcast(ctx context.Context, msg *AHPMessage) error

// SendToDLQ 发送到死信队列
func (q *MessageQueue) SendToDLQ(ctx context.Context, msg *AHPMessage) error
```

## 6. 消息序列化

支持 JSON 和 Protobuf 两种序列化方式：

```go
type Serializer interface {
    Marshal(msg *AHPMessage) ([]byte, error)
    Unmarshal(data []byte) (*AHPMessage, error)
}

// JSON 序列化
type JSONSerializer struct{}

func (s *JSONSerializer) Marshal(msg *AHPMessage) ([]byte, error)
func (s *JSONSerializer) Unmarshal(data []byte) (*AHPMessage, error)

// Protobuf 序列化
type ProtobufSerializer struct{}

func (s *ProtobufSerializer) Marshal(msg *AHPMessage) ([]byte, error)
func (s *ProtobufSerializer) Unmarshal(data []byte) (*AHPMessage, error)
```

## 7. 超时与重试

| 场景 | 超时时间 | 重试策略 |
|------|----------|----------|
| 消息发送 | 5s | 指数退避 |
| 任务处理 | 60s | 3 次重试 |
| 心跳间隔 | 10s | 连续 3 次丢失判定离线 |
| ACK 确认 | 3s | 自动重发 |

## 8. 死信队列 (DLQ)

### 触发条件

- 消息处理失败且超过最大重试次数
- 目标 Agent 不存在
- 任务依赖循环检测

### DLQ 消息格式

```go
type DLQMessage struct {
    OriginalMsg  *AHPMessage `json:"original_msg"`
    ErrorCode    string      `json:"error_code"`
    ErrorMessage string      `json:"error_message"`
    RetryCount   int         `json:"retry_count"`
    Timestamp    time.Time   `json:"timestamp"`
}
```

## 9. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| queue_buffer_size | 100 | 队列缓冲区大小 |
| dlq_size | 500 | 死信队列大小 |
| message_timeout | 30s | 消息超时时间 |
| heartbeat_interval | 10s | 心跳间隔 |
| max_retries | 3 | 最大重试次数 |

## 10. Callback 机制 (v2)

`HeartbeatMonitor` 支持注册 callback，当 `CheckTimeouts` 判定某 agent 离线时自动触发。这将超时检测逻辑与下游恢复动作解耦。

### TimeoutCallback 类型

```go
// TimeoutCallback 在 agent 被 CheckTimeouts 标记为离线时调用。
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

### Callback 调用流程

`CheckTimeouts` 将工作分为两个阶段，避免在 callback 执行期间持有写锁：

```go
func (m *HeartbeatMonitor) CheckTimeouts() []string {
    // 阶段 1: 在写锁下标记 agent 离线
    timedOut := m.checkAndMarkOffline()

    // 阶段 2: 在锁外调用 callback（防止死锁）
    for _, agentID := range timedOut {
        m.notifyCallbacks(agentID)
    }
    return timedOut
}
```

`notifyCallbacks` 在读锁下复制 callback 切片后再逐个调用，因此 callback 可以安全地重新注册或检查 monitor 状态：

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

## 11. Supervisor 集成

`LeaderSupervisor` 使用 callback 机制在 leader 心跳超时时触发 failover。在 `Start` 期间注册 `handleFailover` 方法作为 callback：

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

`handleFailover` callback 通过 `errgroup` 异步启动 failover：

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

`doFailover` 中的 failover 流程：
1. 停止旧 leader agent
2. 获取该 leader 的最新 checkpoint
3. 调用 `FailoverStrategy`（最多 `MaxFailoverAttempts` 次）
4. 注册新 leader agent
5. 从 checkpoint session 恢复 stale tasks

### ColdRestartStrategy

默认的 `FailoverStrategy` 通过工厂函数创建全新的 leader 实例：

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

`HeartbeatSender` 运行一个后台 goroutine，定期将 `HEARTBEAT` 消息入队到 agent 的 `MessageQueue`。

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

### 使用方式

```go
sender := ahp.NewHeartbeatSender(agentID, 5*time.Second, queue)
if err := sender.Validate(); err != nil {
    log.Fatal(err)
}
sender.Start(ctx)
// ... agent 运行中 ...
sender.Stop() // 优雅关闭，等待 goroutine 退出
```

`Stop` 之后可以再次调用 `Start`，使 sender 可在重启周期间复用。`Stop` 使用 `sync.Once` 确保 cancel/wait 序列即使在并发调用下也只执行一次。
