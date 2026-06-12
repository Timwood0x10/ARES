# Error Codes 设计文档

## 1. 概述

Error Codes 模块定义了 Style Agent 框架的统一错误码体系，用于系统各模块的错误处理和定位。

## 2. 错误码规范

```
格式: XX-YYY-ZZZ
  - XX:   模块代码 (01-Agent, 02-Protocol, 03-Storage, 04-LLM, 05-Tools)
  - YYY:  错误类型 (001-099 系统级, 100-199 业务级)
  - ZZZ:  具体错误序号
```

## 3. 错误码表

### 01-Agent 模块

| 错误码 | 名称 | 说明 | 可重试 | 最大重试 |
|--------|------|------|--------|----------|
| 01-001 | AgentNotFound | Agent 未注册 | 否 | 0 |
| 01-002 | AgentTimeout | Agent 执行超时 | 是 | 3 |
| 01-003 | AgentPanic | Agent 内部 panic | 是 | 2 |
| 01-004 | TaskQueueFull | 任务队列满 | 是 | 5 |
| 01-005 | DependencyCycle | 任务依赖循环 | 否 | 0 |
| 01-006 | AgentNotReady | Agent 未就绪 | 是 | 3 |

### 02-Protocol 模块

| 错误码 | 名称 | 说明 | 可重试 | 最大重试 |
|--------|------|------|--------|----------|
| 02-001 | InvalidMessage | 消息格式错误 | 否 | 0 |
| 02-002 | MessageTimeout | 消息发送超时 | 是 | 3 |
| 02-003 | HeartbeatMissed | 心跳丢失 | 是 | 5 |
| 02-004 | MessageEncodingError | 消息编码错误 | 否 | 0 |
| 02-005 | QueueNotFound | 队列不存在 | 否 | 0 |

### 03-Storage 模块

| 错误码 | 名称 | 说明 | 可重试 | 最大重试 |
|--------|------|------|--------|----------|
| 03-001 | DBConnectionFailed | 数据库连接失败 | 是 | 3 |
| 03-002 | QueryFailed | 查询失败 | 是 | 2 |
| 03-003 | VectorSearchFailed | 向量搜索失败 | 是 | 2 |
| 03-004 | TransactionFailed | 事务失败 | 是 | 2 |
| 03-005 | RecordNotFound | 记录不存在 | 否 | 0 |

### 04-LLM 模块

| 错误码 | 名称 | 说明 | 可重试 | 最大重试 |
|--------|------|------|--------|----------|
| 04-001 | LLMRequestFailed | LLM 请求失败 | 是 | 3 |
| 04-002 | LLMTimeout | LLM 响应超时 | 是 | 2 |
| 04-003 | LLMQuotaExceeded | 配额超限 | 否 | 0 |
| 04-004 | LLMInvalidResponse | LLM 响应格式错误 | 是 | 2 |
| 04-005 | LLMModelNotFound | 模型不存在 | 否 | 0 |

### 05-Tools 模块

| 错误码 | 名称 | 说明 | 可重试 | 最大重试 |
|--------|------|------|--------|----------|
| 05-001 | ToolNotFound | 工具不存在 | 否 | 0 |
| 05-002 | ToolExecutionFailed | 工具执行失败 | 是 | 2 |
| 05-003 | ToolTimeout | 工具执行超时 | 是 | 2 |
| 05-004 | ToolValidationFailed | 参数校验失败 | 否 | 0 |

## 4. 错误结构定义

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

## 5. 错误处理策略

```go
// 错误策略
type ErrorStrategy struct {
    Backoff time.Duration // 退避时间
    DLQ     bool          // 是否进入死信队列
    Alert   bool          // 是否告警
}

var ErrorStrategyMap = map[string]ErrorStrategy{
    "01-002": { Backoff: 5*time.Second, DLQ: true, Alert: true},
    "01-003": { Backoff: 10*time.Second, DLQ: true, Alert: true},
    "01-004": { Backoff: 3*time.Second, DLQ: false, Alert: true},
    "02-003": { Backoff: 5*time.Second, DLQ: false, Alert: true},
    "04-001": { Backoff: 3*time.Second, DLQ: false, Alert: false},
}
```

## 6. 错误处理流程

```
1. 发生错误
       │
       ▼
2. 获取错误码
       │
       ▼
3. 查找错误策略
       │
       ├─────── 可重试 ───────┐
       │                      │
       ▼                      ▼
4. 检查重试次数        5. 执行重试
       │                      │
       │◀─────────────────────┘
       │
       ▼
6. 重试成功？ ──否──▶ 7. 进入 DLQ
       │是
       ▼
8. 返回结果
```

## 7. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| default_backoff | 1s | 默认退避时间 |
| max_backoff | 30s | 最大退避时间 |
| dlq_size | 500 | 死信队列大小 |
| alert_threshold | 5 | 告警阈值（连续错误次数） |
