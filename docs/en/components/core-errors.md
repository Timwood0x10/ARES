# Error Codes Design Document

## 1. Overview

The Error Codes module defines the unified error code system for the Style Agent framework, used for error handling and debugging across all system modules.

## 2. Error Code Specification

```
Format: XX-YYY-ZZZ
  - XX:   Module code (01-Agent, 02-Protocol, 03-Storage, 04-LLM, 05-Tools)
  - YYY:  Error type (001-099 System level, 100-199 Business level)
  - ZZZ:  Specific error number
```

## 3. Error Code Table

### 01-Agent Module

| Error Code | Name | Description | Retriable | Max Retries |
|------------|------|------------|-----------|--------------|
| 01-001 | AgentNotFound | Agent not registered | No | 0 |
| 01-002 | AgentTimeout | Agent execution timeout | Yes | 3 |
| 01-003 | AgentPanic | Agent internal panic | Yes | 2 |
| 01-004 | TaskQueueFull | Task queue full | Yes | 5 |
| 01-005 | DependencyCycle | Task dependency cycle | No | 0 |
| 01-006 | AgentNotReady | Agent not ready | Yes | 3 |

### 02-Protocol Module

| Error Code | Name | Description | Retriable | Max Retries |
|------------|------|------------|-----------|--------------|
| 02-001 | InvalidMessage | Invalid message format | No | 0 |
| 02-002 | MessageTimeout | Message send timeout | Yes | 3 |
| 02-003 | HeartbeatMissed | Heartbeat missed | Yes | 5 |
| 02-004 | MessageEncodingError | Message encoding error | No | 0 |
| 02-005 | QueueNotFound | Queue not found | No | 0 |

### 03-Storage Module

| Error Code | Name | Description | Retriable | Max Retries |
|------------|------|------------|-----------|--------------|
| 03-001 | DBConnectionFailed | Database connection failed | Yes | 3 |
| 03-002 | QueryFailed | Query failed | Yes | 2 |
| 03-003 | VectorSearchFailed | Vector search failed | Yes | 2 |
| 03-004 | TransactionFailed | Transaction failed | Yes | 2 |
| 03-005 | RecordNotFound | Record not found | No | 0 |

### 04-LLM Module

| Error Code | Name | Description | Retriable | Max Retries |
|------------|------|------------|-----------|--------------|
| 04-001 | LLMRequestFailed | LLM request failed | Yes | 3 |
| 04-002 | LLMTimeout | LLM response timeout | Yes | 2 |
| 04-003 | LLMQuotaExceeded | Quota exceeded | No | 0 |
| 04-004 | LLMInvalidResponse | Invalid LLM response format | Yes | 2 |
| 04-005 | LLMModelNotFound | Model not found | No | 0 |

### 05-Tools Module

| Error Code | Name | Description | Retriable | Max Retries |
|------------|------|------------|-----------|--------------|
| 05-001 | ToolNotFound | Tool not found | No | 0 |
| 05-002 | ToolExecutionFailed | Tool execution failed | Yes | 2 |
| 05-003 | ToolTimeout | Tool execution timeout | Yes | 2 |
| 05-004 | ToolValidationFailed | Parameter validation failed | No | 0 |

## 4. Error Structure Definition

```go
type ErrorCode struct {
    Code       string        `json:"code"`
    Message    string        `json:"message"`
    Module     string        `json:"module"`
    Retry      bool          `json:"retry"`
    RetryMax   int           `json:"retry_max"`
    Backoff    time.Duration `json:"backoff"`
}

type AppError struct {
    Code    ErrorCode
    Err     error
    Stack   string
    Context map[string]interface{}
    Timestamp time.Time
}

func (e *AppError) Error() string {
    return fmt.Sprintf("[%s] %s: %v", e.Code.Code, e.Code.Message, e.Err)
}
```

## 5. Error Handling Strategy

```go
// Error strategy
type ErrorStrategy struct {
    Backoff time.Duration // Backoff time
    DLQ     bool          // Whether to enter DLQ
    Alert   bool          // Whether to alert
}

var ErrorStrategyMap = map[string]ErrorStrategy{
    "01-002": { Backoff: 5*time.Second, DLQ: true, Alert: true},
    "01-003": { Backoff: 10*time.Second, DLQ: true, Alert: true},
    "01-004": { Backoff: 3*time.Second, DLQ: false, Alert: true},
    "02-003": { Backoff: 5*time.Second, DLQ: false, Alert: true},
    "04-001": { Backoff: 3*time.Second, DLQ: false, Alert: false},
}
```

## 6. Error Handling Flow

```
1. Error occurs
       │
       ▼
2. Get error code
       │
       ▼
3. Lookup error strategy
       │
       ├─────── Retriable ───────┐
       │                      │
       ▼                      ▼
4. Check retry count       5. Execute retry
       │                      │
       │◀─────────────────────┘
       │
       ▼
6. Retry success? ──No──▶ 7. Enter DLQ
       │Yes
       ▼
8. Return result
```

## 7. Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| default_backoff | 1s | Default backoff time |
| max_backoff | 30s | Max backoff time |
| dlq_size | 500 | Dead letter queue size |
| alert_threshold | 5 | Alert threshold (consecutive errors) |
