# Data Models 设计文档

## 1. 概述

Data Models 模块定义了 Style Agent 框架的核心数据结构，包括用户信息、任务、消息、推荐结果等。

## 2. 核心数据结构

### 2.1 UserProfile

用户画像，描述用户的基本信息和偏好。

```go
type UserProfile struct {
    UserID      string            `json:"user_id"`       // 用户ID
    Name        string            `json:"name"`          // 姓名
    Gender      Gender            `json:"gender"`        // 性别
    Age         int               `json:"age"`           // 年龄
    Occupation  string            `json:"occupation"`    // 职业
    Style       []StyleTag       `json:"style"`         // 风格标签
    Budget      *PriceRange      `json:"budget"`        // 预算范围
    Colors      []string         `json:"colors"`         // 偏好颜色
    Occasions   []Occasion       `json:"occasions"`     // 使用场景
    BodyType    string           `json:"body_type"`     // 体型
    Preferences map[string]interface{} `json:"preferences"` // 其他偏好
    CreatedAt   time.Time        `json:"created_at"`
    UpdatedAt   time.Time        `json:"updated_at"`
}

type Gender string

const (
    GenderMale   Gender = "male"
    GenderFemale Gender = "female"
    GenderOther  Gender = "other"
)

type StyleTag string

const (
    StyleCasual   StyleTag = "casual"
    StyleFormal   StyleTag = "formal"
    StyleStreet   StyleTag = "street"
    StyleSporty   StyleTag = "sporty"
    StyleMinimalist StyleTag = "minimalist"
)
```

### 2.2 Task

任务结构，表示一个待处理的推荐任务。

```go
type Task struct {
    TaskID       string            `json:"task_id"`        // 任务ID
    TaskType     AgentType         `json:"task_type"`      // 任务类型
    AgentType    AgentType         `json:"agent_type"`     // 执行 Agent 类型
    UserProfile  *UserProfile      `json:"user_profile"`   // 用户画像
    Context      *TaskContext      `json:"context"`        // 任务上下文（依赖结果）
    Payload      map[string]interface{} `json:"payload"` // 额外参数
    Priority     int               `json:"priority"`       // 优先级
    Deadline     time.Time         `json:"deadline"`       // 截止时间
    CreatedAt    time.Time         `json:"created_at"`
}

type TaskContext struct {
    // 依赖的任务ID
    Dependencies []string `json:"dependencies"`
    // 依赖任务的结果
    DepResults   map[string]*TaskResult `json:"dep_results"`
    // 协调上下文
    Coordination map[string]interface{} `json:"coordination"`
}
```

### 2.3 TaskResult

任务执行结果。

```go
type TaskResult struct {
    TaskID     string                 `json:"task_id"`
    AgentType  AgentType              `json:"agent_type"`
    Success    bool                   `json:"success"`
    Items      []*RecommendItem       `json:"items"`        // 推荐项
    Reason     string                 `json:"reason"`       // 推荐理由
    Metadata   map[string]interface{} `json:"metadata"`     // 元数据
    Error      *AppError              `json:"error"`        // 错误信息
    Duration   time.Duration          `json:"duration"`     // 执行时长
    CreatedAt  time.Time              `json:"created_at"`
}

type RecommendItem struct {
    ItemID     string                 `json:"item_id"`
    Name       string                 `json:"name"`
    Category   string                 `json:"category"`
    ImageURL   string                 `json:"image_url"`
    Price      float64                `json:"price"`
    Brand      string                 `json:"brand"`
    Tags       []string              `json:"tags"`
    Score      float64               `json:"score"`
    Metadata   map[string]interface{} `json:"metadata"`
}
```

### 2.4 RecommendResult

最终推荐结果。

```go
type RecommendResult struct {
    SessionID    string            `json:"session_id"`
    UserID      string            `json:"user_id"`
    Items       []*RecommendItem  `json:"items"`       // 完整搭配
    TotalPrice  float64           `json:"total_price"` // 总价
    Score       float64           `json:"score"`       // 搭配评分
    Summary     string            `json:"summary"`     // 推荐总结
    StyleTags   []StyleTag        `json:"style_tags"`
    Occasions   []Occasion        `json:"occasions"`
    Feedback    *UserFeedback     `json:"feedback"`    // 用户反馈
    CreatedAt   time.Time         `json:"created_at"`
}

type UserFeedback struct {
    Liked    bool   `json:"liked"`
    Comment  string `json:"comment"`
    Rating   int    `json:"rating"` // 1-5
}
```

### 2.5 Session

会话信息。

```go
type Session struct {
    SessionID  string         `json:"session_id"`
    UserID     string         `json:"user_id"`
    Status     SessionStatus  `json:"status"`
    Tasks      []*Task        `json:"tasks"`       // 任务列表
    Results    []*TaskResult  `json:"results"`     // 结果列表
    History    []*RecommendResult `json:"history"` // 历史推荐
    Context    map[string]interface{} `json:"context"` // 会话上下文
    CreatedAt  time.Time     `json:"created_at"`
    UpdatedAt  time.Time     `json:"updated_at"`
    ExpiredAt  time.Time     `json:"expired_at"`
}

type SessionStatus string

const (
    SessionStatusPending   SessionStatus = "pending"
    SessionStatusProcessing SessionStatus = "processing"
    SessionStatusCompleted SessionStatus = "completed"
    SessionStatusFailed    SessionStatus = "failed"
    SessionStatusExpired   SessionStatus = "expired"
)
```

## 3. 数据关系图

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  Session    │────▶│    Task     │────▶│ TaskResult  │
└─────────────┘     └─────────────┘     └─────────────┘
       │                   │                   │
       │                   ▼                   ▼
       │            ┌─────────────┐     ┌─────────────┐
       │            │UserProfile  │     │RecommendItem│
       │            └─────────────┘     └─────────────┘
       │
       ▼
┌─────────────────────────────────────────────────────┐
│                   RecommendResult                   │
└─────────────────────────────────────────────────────┘
```

## 4. 配置参数

| 参数 | 说明 |
|------|------|
| session_ttl | 会话过期时间，默认 24h |
| max_tasks_per_session | 单会话最大任务数，默认 50 |
| max_history_per_session | 单会话最大历史记录数，默认 100 |
