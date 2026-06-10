# Memory Distillation Module Design

## Overview

The Memory Distillation module is a component in the goagent framework for extracting key information from conversation history and task execution processes, storing it in a retrievable format. The module supports two distillation implementation methods, suitable for different use cases.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                  Memory Distillation Module Architecture       │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   ┌─────────────────┐     ┌──────────────────────────────┐ │
│   │   TaskMemory    │────►│   DistillTask()              │ │
│   │  (Task Context) │     │   - Pack Input/Output/Context │ │
│   └─────────────────┘     └──────────────┬───────────────┘ │
│                                           │                 │
│                                           ▼                 │
│   ┌───────────────────────────────────────────────────────┐ │
│   │            memoryManager.StoreDistilledTask()         │ │
│   │  - generateHashVector() Generate hash vector          │ │
│   │  - Store in local distilledTasks map                 │ │
│   └───────────────────────────────────────────────────────┘ │
│                                           │                 │
│   ┌───────────────────────────────────────────────────────┐ │
│   │            KnowledgeBase.distillMemory()              │ │
│   │  - Concatenate conversation history as summary       │ │
│   │  - EmbedWithPrefix() Generate real vector            │ │
│   │  - Store in PostgreSQL + pgvector                     │ │
│   └───────────────────────────────────────────────────────┘ │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## Core Components

### 1. TaskMemory

**File Location**: `internal/memory/context/task.go`

Responsible for managing context information for individual tasks, including input, output, execution steps, and result records.

```go
type TaskData struct {
    TaskID     string
    SessionID  string
    UserID     string
    Input      string
    Output     string
    Context    map[string]interface{}
    Steps      []StepRecord      // Execution step records
    Results    []ResultRecord    // Result records
    CreatedAt  time.Time
    AccessedAt time.Time
}
```

### 2. memoryManager

**File Location**: `internal/memory/manager_impl.go`

Core memory manager, coordinating session memory, task memory, and local vector storage.

```go
type memoryManager struct {
    sessionMemory  *memctx.SessionMemory
    taskMemory      *memctx.TaskMemory
    distilledTasks  map[string]*DistilledTaskData  // Local distilled task storage
    vectorDim       int                              // Vector dimension
}
```

### 3. DistilledMemory

**File Location**: `internal/storage/postgres/repositories/distilled_memory_repository.go`

Distilled memory structure for PostgreSQL persistent storage.

```go
type DistilledMemory struct {
    ID               string
    TenantID         string                 // Multi-tenant isolation
    UserID           string
    SessionID        string
    Content          string                 // Distilled content
    Embedding        []float64              // pgvector vector
    EmbeddingModel   string
    MemoryType       string                 // "profile", "preference", etc.
    Importance       float64                // Importance score
    Metadata         map[string]interface{}
    ExpiresAt        time.Time              // Expiration time
    CreatedAt        time.Time
}
```

---

## Distillation Implementation

### Method 1: TaskMemory Simple Distillation

**Trigger Timing**: Called by Leader Agent after task execution completion

**Distillation Logic**:

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

**Storage Logic**:

```go
// internal/memory/manager_impl.go:238
func (m *memoryManager) StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error {
    // 1. Extract input string
    inputStr := distilled.Payload["input"].(string)

    // 2. Generate hash vector
    vector := m.generateHashVector(inputStr)

    // 3. Store in local map
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

**Vector Generation Algorithm**:

```go
// internal/memory/manager_impl.go:281
func (m *memoryManager) generateHashVector(text string) []float64 {
    vector := make([]float64, m.vectorDim)

    // Simple hash: iterate through characters and accumulate
    hash := uint64(0)
    for i, c := range text {
        hash = hash*31 + uint64(c)
        if i >= len(text)-1 {
            break
        }
    }

    // Disperse hash values to vector dimensions
    for i := range vector {
        vector[i] = float64((hash>>(i*5))%1000) / 1000.0
    }

    // L2 normalization
    norm := math.Sqrt(norm)
    for i := range vector {
        vector[i] /= norm
    }
    return vector
}
```

**Features**:
- Only packs raw data, no information compression
- Uses simple hash to generate vectors, weak semantic retrieval capability
- Stored in memory, supports fast in-process retrieval

---

### Method 2: KnowledgeBase Distillation

**Trigger Timing**: Conversation rounds reach threshold (`DistillationThreshold`)

**Distillation Logic**:

```go
// examples/knowledge-base/main.go:609
func (kb *KnowledgeBase) distillMemory(ctx context.Context, tenantID string) {
    // 1. Get conversation history
    messages, err := kb.memory.GetMessages(ctx, kb.sessionID)

    // 2. Concatenate conversation summary
    var summary strings.Builder
    summary.WriteString("Conversation Summary:\n\n")
    for _, msg := range messages {
        fmt.Fprintf(&summary, "%s: %s\n", msg.Role, msg.Content)
    }

    // 3. Generate embedding vector (with prefix)
    embedding, err := kb.embedding.EmbedWithPrefix(ctx, summaryText, "memory:")

    // 4. Normalize vector
    embedding = postgres.NormalizeVector(embedding)

    // 5. Store in knowledge base
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

**Features**:
- Uses real embedding model to generate vectors
- Stored in PostgreSQL + pgvector, supports semantic similarity retrieval
- Marked `SourceType: "distilled"` to distinguish source

---

## Similar Task Retrieval

```go
// internal/memory/manager_impl.go:319
func (m *memoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
    // 1. Generate vector for query
    queryVector := m.generateHashVector(query)

    // 2. Calculate cosine similarity
    for _, data := range m.distilledTasks {
        score := m.cosineSimilarity(queryVector, data.Vector)
        if score > 0.5 {  // Threshold filtering
            results = append(results, ...)
        }
    }

    // 3. Sort by score
    sort(results, byScoreDesc)

    return results[:limit], nil
}
```

---

## Database Table Structure

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

-- Indexes
CREATE INDEX idx_distilled_tenant ON distilled_memories(tenant_id);
CREATE INDEX idx_distilled_user ON distilled_memories(tenant_id, user_id);
CREATE INDEX idx_distilled_embedding ON distilled_memories USING IVFFlat(embedding vector_cosine_ops);
```

---

## Configuration

```yaml
memory:
  task_distillation:
    enabled: true              # Enable task distillation
    storage: "postgres"        # Storage location: "memory" or "postgres"
    vector_store: true         # Use pgvector for vector storage
    prompt: "..."              # Custom distillation prompt

  # KnowledgeBase configuration
  enable_distillation: true
  distillation_threshold: 3   # Conversation rounds to trigger distillation
```

---

## File List

| File Path | Purpose |
|-----------|---------|
| `internal/memory/context/task.go` | TaskMemory task context management |
| `internal/memory/manager_impl.go` | Memory manager implementation + distillation logic |
| `internal/storage/postgres/repositories/distilled_memory_repository.go` | PostgreSQL persistent storage |
| `examples/knowledge-base/main.go` | KnowledgeBase distillation example |
| `internal/tools/resources/builtin/memory/distilled_memory_tools.go` | Distilled memory search tools |

---

## Design Evaluation

### Advantages

1. **Multi-level storage**: Supports both memory map and PostgreSQL storage methods
2. **Multi-tenant isolation**: All operations based on `tenant_id` isolation
3. **TTL management**: Supports automatic cleanup based on expiration time
4. **Async processing**: Leader Agent uses goroutine for async distillation

### Areas for Improvement

1. **Low distillation quality**: Currently just packs data, no real information extraction/compression
2. **Fragmented implementations**: TaskMemory and KnowledgeBase logic completely different
3. **Vector quality**: Simple hash vectors have weak semantic retrieval effect
4. **Missing importance filtering**: All tasks are distilled, easy to generate noise

---

**Version**: 1.0  
**Last Updated**: 2026-03-24  
**Maintainer**: GoAgent Team