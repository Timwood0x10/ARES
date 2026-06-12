# Leader Agent Design Document

## 1. Module Overview

Leader Agent is the **coordination center** of the Style Agent framework. It is responsible for receiving user input, parsing requirements, planning tasks, coordinating multiple Sub Agents, and ultimately aggregating results to return to the user.

## 2. Core Responsibilities

| Responsibility | Description |
|---------------|-------------|
| **User Input Parsing** | Parse user text to extract UserProfile (gender, age, style preferences, etc.) |
| **Task Planning** | Decide which Sub Agents are needed based on Profile |
| **Task Dispatch** | Phase 1: Parallel dispatch to all Sub Agents |
| **Dependency Coordination** | Phase 2: Handle dependent tasks (e.g., top results → shoes) |
| **Result Aggregation** | Collect all Sub Agent results, generate final recommendation |
| **State Management** | Maintain session state, handle timeouts and errors |

## 3. Architecture Design

```
┌─────────────────────────────────────────────────────────────────┐
│                        Leader Agent                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐        │
│  │ParseProfile │    │TaskPlanner │    │Aggregator  │        │
│  │   (LLM)     │    │   (LLM)    │    │   (LLM)    │        │
│  └──────┬──────┘    └──────┬──────┘    └──────┬──────┘        │
│         │                  │                  │                │
│         └──────────────────┼──────────────────┘                │
│                            │                                    │
│                    dispatch tasks                               │
└───────────────────────────┬────────────────────────────────────┘
                            │
         ┌──────────────────┼──────────────────┐
         ▼                  ▼                  ▼
   ┌──────────┐       ┌──────────┐       ┌──────────┐
   │agent_top │       │agent_btm│       │agent_hd │
   └──────────┘       └──────────┘       └──────────┘
```

## 4. Task Flow

### Phase 1: Parallel Task Dispatch

```
1. Receive user input "Xiao Ming, male, 22, student"
2. ParseProfile: LLM parses to UserProfile
3. TaskPlanner: LLM decides agents needed: agent_top, agent_bottom, agent_shoes
4. Send TASK messages to each Sub Agent queue in parallel
```

### Phase 2: Dependency-Aware Tasks

```
1. Wait for Phase 1 results
2. If dependencies exist (e.g., top → shoes), dispatch with result as context
3. Coordinate Context transmission
```

### Phase 3: Result Aggregation

```
1. Collect RESULT messages from all Sub Agents
2. Aggregator: LLM integrates into final recommendation
3. Save to database
4. Return to user
```

## 5. Interface Definition

```go
type LeaderAgent interface {
    // HandleInput processes user input
    HandleInput(ctx context.Context, input string) (*RecommendResult, error)
    
    // DispatchTasks dispatches tasks in parallel
    DispatchTasks(ctx context.Context, profile *UserProfile, tasks []Task) error
    
    // CollectResults collects results
    CollectResults(ctx context.Context, taskIDs []string) ([]*TaskResult, error)
    
    // Aggregate aggregates results
    Aggregate(ctx context.Context, results []*TaskResult) (*RecommendResult, error)
}
```

## 6. Message Protocol

| Message Type | Direction | Description |
|--------------|-----------|-------------|
| TASK | Leader → Sub Agent | Task dispatch |
| RESULT | Sub Agent → Leader | Return result |
| PROGRESS | Sub Agent → Leader | Progress report |
| ACK | Sub Agent → Leader | Acknowledgment |

## 7. Error Handling

| Error Code | Description | Strategy |
|------------|-------------|----------|
| 01-001 | AgentNotFound | Return error to user |
| 01-002 | AgentTimeout | Retry 3 times |
| 01-004 | TaskQueueFull | Reject new task, return 429 |

## 8. Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| phase1_timeout | 30s | Phase 1 timeout |
| phase2_timeout | 20s | Phase 2 timeout |
| max_retries | 3 | Max retry attempts |
| task_queue_limit | 1000 | Task queue limit |
