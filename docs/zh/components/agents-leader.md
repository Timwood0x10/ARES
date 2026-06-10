# Leader Agent 设计文档

## 1. 模块概述

Leader Agent 是整个 Style Agent 框架的**协调中枢**，负责接收用户输入、解析需求、规划任务、协调多个 Sub Agent 工作，并最终聚合结果返回给用户。

## 2. 核心职责

| 职责 | 说明 |
|------|------|
| **用户输入解析** | 解析用户文本，提取 UserProfile（性别、年龄、风格偏好等） |
| **任务规划** | 根据 Profile 决策需要哪些 Sub Agent 参与 |
| **任务派发** | Phase 1 并行派发任务到各个 Sub Agent |
| **依赖协调** | Phase 2 处理有依赖关系的任务（如 top 结果给 shoes） |
| **结果聚合** | 收集所有 Sub Agent 结果，生成最终推荐 |
| **状态管理** | 维护会话状态，处理超时和错误 |

## 3. 架构设计

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

## 4. 任务流程

### Phase 1: 并行任务派发

```
1. 接收用户输入 "Xiao Ming, male, 22, student"
2. ParseProfile: LLM 解析为 UserProfile
3. TaskPlanner: LLM 决策需要 agent_top, agent_bottom, agent_shoes
4. 并行发送 TASK 消息到各 Sub Agent 队列
```

### Phase 2: 依赖感知任务

```
1. 等待 Phase 1 结果返回
2. 如果存在依赖（如 top → shoes），将结果作为上下文派发
3. 协调 Context 传递
```

### Phase 3: 结果聚合

```
1. 收集所有 Sub Agent 的 RESULT 消息
2. Aggregator: LLM 整合为最终推荐
3. 保存到数据库
4. 返回给用户
```

## 5. 接口定义

```go
type LeaderAgent interface {
    // HandleInput 处理用户输入
    HandleInput(ctx context.Context, input string) (*RecommendResult, error)
    
    // DispatchTasks 并行派发任务
    DispatchTasks(ctx context.Context, profile *UserProfile, tasks []Task) error
    
    // CollectResults 收集结果
    CollectResults(ctx context.Context, taskIDs []string) ([]*TaskResult, error)
    
    // Aggregate 聚合结果
    Aggregate(ctx context.Context, results []*TaskResult) (*RecommendResult, error)
}
```

## 6. 消息协议

| 消息类型 | 方向 | 说明 |
|----------|------|------|
| TASK | Leader → Sub Agent | 派发任务 |
| RESULT | Sub Agent → Leader | 返回结果 |
| PROGRESS | Sub Agent → Leader | 进度汇报 |
| ACK | Sub Agent → Leader | 确认收到 |

## 7. 错误处理

| 错误码 | 说明 | 处理策略 |
|--------|------|----------|
| 01-001 | AgentNotFound | 返回错误给用户 |
| 01-002 | AgentTimeout | 重试 3 次 |
| 01-004 | TaskQueueFull | 拒绝新任务，返回 429 |

## 8. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| phase1_timeout | 30s | Phase 1 超时时间 |
| phase2_timeout | 20s | Phase 2 超时时间 |
| max_retries | 3 | 最大重试次数 |
| task_queue_limit | 1000 | 任务队列上限 |
