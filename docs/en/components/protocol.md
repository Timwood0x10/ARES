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
