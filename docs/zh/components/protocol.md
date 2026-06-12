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
