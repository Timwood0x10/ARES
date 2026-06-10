# 记忆蒸馏模块设计文档

## 概述

记忆蒸馏（Memory Distillation）模块是 goagent 框架中用于从对话历史和任务执行过程中提取关键信息，并将其存储为可检索格式的组件。该模块支持两种蒸馏实现方式，分别适用于不同的使用场景。

---

## 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                      记忆蒸馏模块架构                         │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   ┌─────────────────┐     ┌──────────────────────────────┐ │
│   │   TaskMemory    │────►│   DistillTask()              │ │
│   │   (任务上下文)   │     │   - 打包 Input/Output/Context │ │
│   └─────────────────┘     └──────────────┬───────────────┘ │
│                                           │                 │
│                                           ▼                 │
│   ┌───────────────────────────────────────────────────────┐ │
│   │            memoryManager.StoreDistilledTask()         │ │
│   │  - generateHashVector() 生成哈希向量                   │ │
│   │  - 存入本地 distilledTasks map                        │ │
│   └───────────────────────────────────────────────────────┘ │
│                                           │                 │
│   ┌───────────────────────────────────────────────────────┐ │
│   │            KnowledgeBase.distillMemory()              │ │
│   │  - 拼接对话历史为摘要                                  │ │
│   │  - EmbedWithPrefix() 生成真实向量                     │ │
│   │  - 存入 PostgreSQL + pgvector                         │ │
│   └───────────────────────────────────────────────────────┘ │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## 核心组件

### 1. TaskMemory

**文件位置**: `internal/memory/context/task.go`

负责管理单个任务的上下文信息，包括输入、输出、执行步骤和结果记录。

```go
type TaskData struct {
    TaskID     string
    SessionID  string
    UserID     string
    Input      string
    Output     string
    Context    map[string]interface{}
    Steps      []StepRecord      // 执行步骤记录
    Results    []ResultRecord    // 结果记录
    CreatedAt  time.Time
    AccessedAt time.Time
}
```

### 2. memoryManager

**文件位置**: `internal/memory/manager_impl.go`

核心内存管理器，协调会话记忆、任务记忆和本地向量存储。

```go
type memoryManager struct {
    sessionMemory  *memctx.SessionMemory
    taskMemory      *memctx.TaskMemory
    distilledTasks  map[string]*DistilledTaskData  // 本地蒸馏任务存储
    vectorDim       int                              // 向量维度
}
```

### 3. DistilledMemory

**文件位置**: `internal/storage/postgres/repositories/distilled_memory_repository.go`

用于 PostgreSQL 持久化存储的蒸馏记忆结构。

```go
type DistilledMemory struct {
    ID               string
    TenantID         string                 // 多租户隔离
    UserID           string
    SessionID        string
    Content          string                 // 蒸馏后的内容
    Embedding        []float64              // pgvector 向量
    EmbeddingModel   string
    MemoryType       string                 // "profile", "preference" 等
    Importance       float64                // 重要性评分
    Metadata         map[string]interface{}
    ExpiresAt        time.Time              // 过期时间
    CreatedAt        time.Time
}
```

---

## 蒸馏实现

### 方式一：TaskMemory 简单蒸馏

**触发时机**: 任务执行完成后由 Leader Agent 调用

**蒸馏逻辑**:

```go
// internal/memory/context/task.go:245
func (m *TaskMemory) Distill(ctx context.Context, taskID string) (*models.Task, error) {
    task, exists := m.tasks[taskID]
    if !exists {
        return nil, ErrTaskNotFound
    }

    distilled := &models.Task{
        TaskID: taskID,
        Payload: map[string]any{
            "input":   task.Input,
            "output":  task.Output,
            "context": task.Context,
        },
        CreatedAt: task.CreatedAt,
    }
    return distilled, nil
}
```

**存储逻辑**:

```go
// internal/memory/manager_impl.go:238
func (m *memoryManager) StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error {
    // 1. 提取输入字符串
    inputStr := distilled.Payload["input"].(string)

    // 2. 生成哈希向量
    vector := m.generateHashVector(inputStr)

    // 3. 存入本地 map
    data := &DistilledTaskData{
        TaskID:    taskID,
        Input:     inputStr,
        Output:    fmt.Sprintf("%v", distilled.Payload["output"]),
        Vector:    vector,
        CreatedAt: time.Now(),
    }
    m.distilledTasks[taskID] = data
    return nil
}
```

**向量生成算法**:

```go
// internal/memory/manager_impl.go:281
func (m *memoryManager) generateHashVector(text string) []float64 {
    vector := make([]float64, m.vectorDim)

    // 简单哈希：遍历字符累加
    hash := uint64(0)
    for i, c := range text {
        hash = hash*31 + uint64(c)
        if i >= len(text)-1 {
            break
        }
    }

    // 将哈希值分散到向量维度
    for i := range vector {
        vector[i] = float64((hash>>(i*5))%1000) / 1000.0
    }

    // L2 归一化
    norm := math.Sqrt(norm)
    for i := range vector {
        vector[i] /= norm
    }
    return vector
}
```

**特点**:
- 仅打包原始数据，不做信息压缩
- 使用简单哈希生成向量，语义检索能力弱
- 存储在内存中，支持进程内快速检索

---

### 方式二：KnowledgeBase 蒸馏

**触发时机**: 对话轮数达到阈值（`DistillationThreshold`）

**蒸馏逻辑**:

```go
// examples/knowledge-base/main.go:609
func (kb *KnowledgeBase) distillMemory(ctx context.Context, tenantID string) {
    // 1. 获取对话历史
    messages, err := kb.memory.GetMessages(ctx, kb.sessionID)

    // 2. 拼接对话摘要
    var summary strings.Builder
    summary.WriteString("Conversation Summary:\n\n")
    for _, msg := range messages {
        fmt.Fprintf(&summary, "%s: %s\n", msg.Role, msg.Content)
    }

    // 3. 生成嵌入向量（带前缀）
    embedding, err := kb.embedding.EmbedWithPrefix(ctx, summaryText, "memory:")

    // 4. 归一化向量
    embedding = postgres.NormalizeVector(embedding)

    // 5. 存储到知识库
    distilledChunk := &storage_models.KnowledgeChunk{
        TenantID:         tenantID,
        Content:          summaryText,
        Embedding:        embedding,
        SourceType:       "distilled",
        Source:           fmt.Sprintf("memory:%s", kb.sessionID),
    }
    kb.repo.Create(ctx, distilledChunk)
}
```

**特点**:
- 使用真实 embedding 模型生成向量
- 存储在 PostgreSQL + pgvector，支持语义相似度检索
- 标记 `SourceType: "distilled"` 区分来源

---

## 相似任务检索

```go
// internal/memory/manager_impl.go:319
func (m *memoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
    // 1. 为查询生成向量
    queryVector := m.generateHashVector(query)

    // 2. 计算余弦相似度
    for _, data := range m.distilledTasks {
        score := m.cosineSimilarity(queryVector, data.Vector)
        if score > 0.5 {  // 阈值过滤
            results = append(results, ...)
        }
    }

    // 3. 按得分排序
    sort(results, byScoreDesc)

    return results[:limit], nil
}
```

---

## 数据库表结构

```sql
CREATE TABLE distilled_memories (
    id UUID PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    user_id TEXT,
    session_id TEXT,
    content TEXT NOT NULL,
    embedding VECTOR(1024),
    embedding_model TEXT,
    memory_type VARCHAR(50),
    importance FLOAT,
    metadata JSONB,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- 索引
CREATE INDEX idx_distilled_tenant ON distilled_memories(tenant_id);
CREATE INDEX idx_distilled_user ON distilled_memories(tenant_id, user_id);
CREATE INDEX idx_distilled_embedding ON distilled_memories USING IVFFlat(embedding vector_cosine_ops);
```

---

## 配置项

```yaml
memory:
  task_distillation:
    enabled: true              # 启用任务蒸馏
    storage: "postgres"        # 存储位置: "memory" 或 "postgres"
    vector_store: true         # 使用 pgvector 存储向量
    prompt: "..."              # 自定义蒸馏提示词

  # KnowledgeBase 配置
  enable_distillation: true
  distillation_threshold: 3   # 触发蒸馏的对话轮数
```

---

## 文件清单

| 文件路径 | 作用 |
|---------|------|
| `internal/memory/context/task.go` | TaskMemory 任务上下文管理 |
| `internal/memory/manager_impl.go` | 内存管理器实现 + 蒸馏逻辑 |
| `internal/storage/postgres/repositories/distilled_memory_repository.go` | PostgreSQL 持久化存储 |
| `examples/knowledge-base/main.go` | KnowledgeBase 蒸馏示例 |
| `internal/tools/resources/builtin/memory/distilled_memory_tools.go` | 蒸馏记忆搜索工具 |

---

## 设计评价

### 优点

1. **多层次存储**: 支持内存 map 和 PostgreSQL 两种存储方式
2. **多租户隔离**: 所有操作基于 `tenant_id` 隔离
3. **TTL 管理**: 支持过期时间自动清理
4. **异步处理**: Leader Agent 用 goroutine 异步执行蒸馏

### 待改进

1. **蒸馏"含金量"低**: 当前只是打包数据，未做真正的信息提取/压缩
2. **两套实现割裂**: TaskMemory 和 KnowledgeBase 逻辑完全不同
3. **向量质量**: 简单哈希向量语义检索效果弱
4. **缺少重要性过滤**: 所有任务都蒸馏，容易产生噪音
