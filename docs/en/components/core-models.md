# Data Models Design Document

## 1. Overview

The Data Models module defines the core data structures of the Style Agent framework, including user information, tasks, messages, recommendation results, etc.

## 2. Core Data Structures

### 2.1 UserProfile

User profile describing basic information and preferences.

```go
type UserProfile struct {
    UserID      string            `json:"user_id"`       // User ID
    Name        string            `json:"name"`          // Name
    Gender      Gender            `json:"gender"`        // Gender
    Age         int               `json:"age"`           // Age
    Occupation  string            `json:"occupation"`    // Occupation
    Style       []StyleTag       `json:"style"`         // Style tags
    Budget      *PriceRange      `json:"budget"`        // Budget range
    Colors      []string         `json:"colors"`         // Preferred colors
    Occasions   []Occasion       `json:"occasions"`     // Usage scenarios
    BodyType    string           `json:"body_type"`     // Body type
    Preferences map[string]interface{} `json:"preferences"` // Other preferences
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

Task structure representing a pending recommendation task.

```go
type Task struct {
    TaskID       string            `json:"task_id"`        // Task ID
    TaskType     AgentType         `json:"task_type"`      // Task type
    AgentType    AgentType         `json:"agent_type"`     // Executing Agent type
    UserProfile  *UserProfile      `json:"user_profile"`   // User profile
    Context      *TaskContext      `json:"context"`        // Task context (dependencies)
    Payload      map[string]interface{} `json:"payload"` // Additional parameters
    Priority     int               `json:"priority"`       // Priority
    Deadline     time.Time         `json:"deadline"`       // Deadline
    CreatedAt    time.Time         `json:"created_at"`
}

type TaskContext struct {
    // Dependent task IDs
    Dependencies []string `json:"dependencies"`
    // Dependent task results
    DepResults   map[string]*TaskResult `json:"dep_results"`
    // Coordination context
    Coordination map[string]interface{} `json:"coordination"`
}
```

### 2.3 TaskResult

Task execution result.

```go
type TaskResult struct {
    TaskID     string                 `json:"task_id"`
    AgentType  AgentType              `json:"agent_type"`
    Success    bool                   `json:"success"`
    Items      []*RecommendItem       `json:"items"`        // Recommended items
    Reason     string                 `json:"reason"`       // Recommendation reason
    Metadata   map[string]interface{} `json:"metadata"`     // Metadata
    Error      *AppError              `json:"error"`        // Error information
    Duration   time.Duration          `json:"duration"`     // Execution duration
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

Final recommendation result.

```go
type RecommendResult struct {
    SessionID    string            `json:"session_id"`
    UserID      string            `json:"user_id"`
    Items       []*RecommendItem  `json:"items"`       // Complete outfit
    TotalPrice  float64           `json:"total_price"` // Total price
    Score       float64           `json:"score"`       // Outfit score
    Summary     string             `json:"summary"`    // Recommendation summary
    StyleTags   []StyleTag        `json:"style_tags"`
    Occasions   []Occasion        `json:"occasions"`
    Feedback    *UserFeedback     `json:"feedback"`    // User feedback
    CreatedAt   time.Time         `json:"created_at"`
}

type UserFeedback struct {
    Liked    bool   `json:"liked"`
    Comment  string `json:"comment"`
    Rating   int    `json:"rating"` // 1-5
}
```

### 2.5 Session

Session information.

```go
type Session struct {
    SessionID  string         `json:"session_id"`
    UserID     string         `json:"user_id"`
    Status     SessionStatus  `json:"status"`
    Tasks      []*Task        `json:"tasks"`       // Task list
    Results    []*TaskResult  `json:"results"`     // Result list
    History    []*RecommendResult `json:"history"` // Historical recommendations
    Context    map[string]interface{} `json:"context"` // Session context
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

## 3. Data Relationship Diagram

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

## 4. Configuration Parameters

| Parameter | Description |
|-----------|-------------|
| session_ttl | Session expiration time, default 24h |
| max_tasks_per_session | Max tasks per session, default 50 |
| max_history_per_session | Max history per session, default 100 |
