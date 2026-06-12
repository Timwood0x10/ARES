# Sub Agent 设计文档

## 1. 模块概述

Sub Agent（Worker Agent）是执行具体穿搭推荐任务的 Worker，每个 Sub Agent 负责特定领域的推荐，如上衣、下装、鞋子、配饰等。

## 2. 核心职责

| 职责 | 说明 |
|------|------|
| **接收任务** | 从消息队列获取 TASK 消息 |
| **执行推荐** | 调用 Tools、LLM 进行推荐 |
| **返回结果** | 发送 RESULT 消息给 Leader |
| **进度汇报** | 发送 PROGRESS 消息报告进度 |
| **心跳保活** | 定期发送 HEARTBEAT |

## 3. Agent 类型

| Agent 类型 | 说明 | 依赖 |
|------------|------|------|
| agent_top | 上衣推荐 | - |
| agent_bottom | 下装推荐 | - |
| agent_shoes | 鞋子推荐 | agent_top |
| agent_head | 头部配饰 | - |
| agent_accessory | 配饰推荐 | agent_top, agent_bottom |

## 4. 架构设计

```
┌─────────────────────────────────────────────────────────────────┐
│                        Sub Agent                                │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐        │
│  │  Mailbox    │───▶│ HandleTask  │───▶│   Execute   │        │
│  │  (channel)  │    │             │    │             │        │
│  └─────────────┘    └─────────────┘    └──────┬──────┘        │
│         ▲                                        │               │
│         │                                        ▼               │
│         │                              ┌─────────────────┐       │
│         │                              │ Tools / LLM /   │       │
│         │                              │ Vector Search   │       │
│         │                              └─────────────────┘       │
│         │                                        │               │
│         └────────────────────────────────────────┘               │
│                          RESULT / PROGRESS                       │
└─────────────────────────────────────────────────────────────────┘
```

## 5. 任务执行流程

```
1. 从 Mailbox 接收 TASK 消息
2. 解析任务参数（UserProfile, Context）
3. 执行业务逻辑：
   a. 调用 Tools 获取数据（天气、时尚搜索）
   b. 调用 Vector DB 进行相似推荐
   c. 调用 LLM 生成推荐理由
4. 发送 RESULT 给 Leader
5. 等待下一个任务
```

## 6. 接口定义

```go
type SubAgent interface {
    // Start 启动 Agent
    Start(ctx context.Context) error
    
    // Stop 停止 Agent
    Stop() error
    
    // HandleTask 处理任务
    HandleTask(ctx context.Context, task *Task) (*TaskResult, error)
    
    // SendHeartbeat 发送心跳
    SendHeartbeat()
    
    // GetAgentType 获取 Agent 类型
    GetAgentType() AgentType
}
```

## 7. 消息处理

### TASK 消息处理

```go
func (a *SubAgent) handleTask(ctx context.Context, msg *AHPMessage) error {
    // 1. 解析任务
    task := ParseTask(msg.Payload)
    
    // 2. 发送 ACK
    a.sendAck(msg.MessageID)
    
    // 3. 发送 PROGRESS
    a.sendProgress(0.3, "loading data...")
    
    // 4. 执行推荐
    result, err := a.execute(ctx, task)
    if err != nil {
        return a.handleError(err)
    }
    
    // 5. 发送 RESULT
    a.sendResult(msg.TaskID, result)
    
    return nil
}
```

## 8. 错误处理

| 错误码 | 说明 | 处理策略 |
|--------|------|----------|
| 01-002 | AgentTimeout | 重试 3 次，失败进入 DLQ |
| 01-003 | AgentPanic | 记录日志，重启 Agent |
| 04-001 | LLMRequestFailed | 重试 3 次 |
| 04-002 | LLMTimeout | 重试 2 次 |

## 9. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| worker_pool_size | 10 | Worker 池大小 |
| task_timeout | 60s | 任务执行超时 |
| heartbeat_interval | 10s | 心跳间隔 |
| max_retries | 3 | 最大重试次数 |
| queue_size | 1000 | 消息队列大小 |
