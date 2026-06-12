# Sub Agent Design Document

## 1. Module Overview

Sub Agent (Worker Agent) is the Worker that executes specific fashion recommendation tasks. Each Sub Agent is responsible for a specific domain such as tops, bottoms, shoes, accessories, etc.

## 2. Core Responsibilities

| Responsibility | Description |
|---------------|-------------|
| **Receive Task** | Get TASK message from message queue |
| **Execute Recommendation** | Call Tools, LLM for recommendations |
| **Return Result** | Send RESULT message to Leader |
| **Progress Report** | Send PROGRESS message to report progress |
| **Heartbeat** | Send HEARTBEAT periodically |

## 3. Agent Types

| Agent Type | Description | Dependencies |
|------------|-------------|--------------|
| agent_top | Top recommendation | - |
| agent_bottom | Bottom recommendation | - |
| agent_shoes | Shoes recommendation | agent_top |
| agent_head | Head accessories | - |
| agent_accessory | Accessory recommendation | agent_top, agent_bottom |

## 4. Architecture Design

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

## 5. Task Execution Flow

```
1. Receive TASK message from Mailbox
2. Parse task parameters (UserProfile, Context)
3. Execute business logic:
   a. Call Tools to get data (weather, fashion search)
   b. Call Vector DB for similar recommendations
   c. Call LLM to generate recommendation reasons
4. Send RESULT to Leader
5. Wait for next task
```

## 6. Interface Definition

```go
type SubAgent interface {
    // Start starts the Agent
    Start(ctx context.Context) error
    
    // Stop stops the Agent
    Stop() error
    
    // HandleTask handles the task
    HandleTask(ctx context.Context, task *Task) (*TaskResult, error)
    
    // SendHeartbeat sends heartbeat
    SendHeartbeat()
    
    // GetAgentType gets agent type
    GetAgentType() AgentType
}
```

## 7. Message Handling

### TASK Message Handling

```go
func (a *SubAgent) handleTask(ctx context.Context, msg *AHPMessage) error {
    // 1. Parse task
    task := ParseTask(msg.Payload)
    
    // 2. Send ACK
    a.sendAck(msg.MessageID)
    
    // 3. Send PROGRESS
    a.sendProgress(0.3, "loading data...")
    
    // 4. Execute recommendation
    result, err := a.execute(ctx, task)
    if err != nil {
        return a.handleError(err)
    }
    
    // 5. Send RESULT
    a.sendResult(msg.TaskID, result)
    
    return nil
}
```

## 8. Error Handling

| Error Code | Description | Strategy |
|------------|-------------|----------|
| 01-002 | AgentTimeout | Retry 3 times, fail to DLQ |
| 01-003 | AgentPanic | Log and restart Agent |
| 04-001 | LLMRequestFailed | Retry 3 times |
| 04-002 | LLMTimeout | Retry 2 times |

## 9. Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| worker_pool_size | 10 | Worker pool size |
| task_timeout | 60s | Task execution timeout |
| heartbeat_interval | 10s | Heartbeat interval |
| max_retries | 3 | Max retry attempts |
| queue_size | 1000 | Message queue size |
