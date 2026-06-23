# Memory System 设计文档

## 1. 概述

Memory System 模块是 ares 框架的核心组件，负责管理会话内存、任务内存和蒸馏记忆，实现短期会话上下文管理、任务执行追踪以及长期经验提取与检索。

**核心功能**：会话上下文管理、任务执行追踪、记忆蒸馏（经验提取）、基于向量的语义检索。

## 2. 存储策略

### SessionMemory

**存储位置**：内存 | **生命周期**：会话期间（默认 24h）

```go
// internal/memory/context/session.go
type SessionMemory struct {
    sessions map[string]*SessionData
    mu       sync.RWMutex
    maxSize  int
    ttl      time.Duration
}

type SessionData struct {
    SessionID  string
    UserID     string
    Messages   []Message
    CreatedAt  time.Time
    AccessedAt time.Time
}
```

```go
sessionID := memoryManager.CreateSession(ctx, userID)
memoryManager.AddMessage(ctx, sessionID, "user", "Hello")
messages := memoryManager.GetMessages(ctx, sessionID)
memoryManager.DeleteSession(ctx, sessionID)
```

### TaskMemory

**存储位置**：内存 | **生命周期**：任务期间（默认 1h）

```go
// internal/memory/context/task.go
type TaskData struct {
    TaskID     string
    SessionID  string
    UserID     string
    Input      string
    Output     string
    Context    map[string]interface{}
    Steps      []StepRecord
    Results    []ResultRecord
    CreatedAt  time.Time
    AccessedAt time.Time
}
```

```go
taskID := memoryManager.CreateTask(ctx, sessionID, userID, input)
memoryManager.UpdateTaskOutput(ctx, taskID, output)
distilled := memoryManager.DistillTask(ctx, taskID)
```

### 蒸馏记忆存储：PostgreSQL + pgvector

**存储位置**：PostgreSQL | **向量**：真实 Embedding 模型 | **特点**：语义检索、多租户隔离

```sql
CREATE TABLE knowledge_chunks_1024 (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    content TEXT NOT NULL,
    embedding VECTOR(1024),
    embedding_model TEXT,
    embedding_version INT,
    embedding_status TEXT,
    source_type TEXT,
    source TEXT,
    document_id TEXT,
    chunk_index INT,
    content_hash TEXT,
    access_count INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_chunks_embedding ON knowledge_chunks_1024
    USING IVFFlat(embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX idx_chunks_tenant ON knowledge_chunks_1024(tenant_id);
```

## 3. 记忆蒸馏引擎

蒸馏模块 (`internal/memory/distillation/`) 通过 5 阶段 pipeline 从对话历史中提取和分类关键信息：

```
[]Message → ExperienceExtractor → MemoryClassifier → ImportanceScorer
    → ConflictResolver → Top-N Filter → []Memory
```

### ExperienceExtractor

提取问题-解决方案对。支持直接对话对和跨轮提取（用户 → 助手(澄清) → 用户(补充) → 助手(答案)）。

```go
// internal/memory/distillation/extractor.go
func (e *ExperienceExtractor) extractDirectExperience(user, assistant Message) *Experience {
    problem := strings.TrimSpace(user.Content)
    solution := e.extractCoreSolution(strings.TrimSpace(assistant.Content))
    return &Experience{
        Problem:    problem,
        Solution:   solution,
        Confidence: e.calculateConfidence(problem, solution),
        ExtractionMethod: ExtractionDirect,
    }
}
```

### MemoryClassifier

将经验分为 4 种类型：**fact**、**preference**、**solution**、**rule**。

```go
func (c *MemoryClassifier) ClassifyMemory(exp *Experience) MemoryType {
    solution := strings.ToLower(exp.Solution)
    if c.isSolutionPattern(solution)   { return MemorySolution }
    if c.isPreferencePattern(...)       { return MemoryPreference }
    if c.isRulePattern(solution)        { return MemoryRule }
    return MemoryFact
}
```

### ImportanceScorer

基于类型、长度奖励和动作动词存在性评分。

```go
func (s *ImportanceScorer) ScoreMemory(memoryType MemoryType, problem, solution string) float64 {
    score := 0.5
    switch memoryType {
    case MemorySolution:  score = 0.7
    case MemoryPreference: score = 0.6
    case MemoryRule:      score = 0.65
    case MemoryFact:      score = 0.5
    }
    if s.enableLengthBonus && len(solution) > s.lengthThreshold {
        score += s.lengthBonus
    }
    // 动作动词奖励：+0.05（restart, run, execute 等）
    return score
}
```

### ConflictResolver

通过向量余弦相似度检测相似记忆。策略：**replace**（新记忆置信度高 0.1 时覆盖旧记忆）、**version**（保留两个版本）。

```go
func (r *ConflictResolver) DetectConflict(ctx context.Context, vector []float64, tenantID string) (*Experience, error) {
    similar, err := r.repo.SearchByVector(ctx, vector, tenantID, r.searchLimit)
    // ... 检查 cosine similarity > conflictThreshold（默认 0.85）
}
```

### 使用方式

```go
embedder := embedding.NewEmbeddingClient(config.EmbeddingServiceURL, config.EmbeddingModel)
repo := repositories.NewExperienceRepository(pool)
distillConfig := distillation.DefaultDistillationConfig()
distillConfig.MinImportance = 0.6
distillConfig.MaxMemoriesPerDistillation = 3
distiller := distillation.NewDistiller(distillConfig, embedder, repo)

memories, err := distiller.DistillConversation(ctx, "conv_123", messages, "tenant_abc", "user_456")
```

## 4. 相似任务检索

```go
// 基于 PostgreSQL + pgvector 的检索
func (m *memoryManager) searchSimilarTasksNew(ctx context.Context, query string, limit int) ([]*models.Task, error) {
    queryVector, err := m.embedder.EmbedWithPrefix(ctx, query, "query:")
    experiences, err := m.expRepo.SearchByVector(ctx, queryVector, "default", limit)
    // 转换为 []*models.Task ...
}
```

## 5. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| session_ttl | 24h | 会话过期时间 |
| task_ttl | 1h | 任务过期时间 |
| max_sessions | 1000 | 最大会话数 |
| max_tasks | 10000 | 最大任务数 |
| vector_dimension | 1024 | 向量维度 |
| distillation_threshold | 3 | 触发蒸馏的对话轮数 |
| MinImportance | 0.6 | 最小重要性分数 |
| ConflictThreshold | 0.85 | 冲突检测相似度阈值 |
| MaxMemoriesPerDistillation | 3 | 每次蒸馏最大记忆数 |

## 6. Leader Session 恢复

### GetLatestSessionForLeader

从 `leader_checkpoints` 表中检索 leader 最近的 session ID。用于 failover 恢复——新 leader 被提升后可从最近的 checkpoint session 恢复。

**接口定义** (`internal/memory/manager.go`)：
```go
GetLatestSessionForLeader(ctx context.Context, leaderID string) (string, error)
```

**Production 实现** (`internal/memory/production_manager.go`)：
```go
func (m *ProductionMemoryManager) GetLatestSessionForLeader(ctx context.Context, leaderID string) (string, error) {
    if leaderID == "" {
        return "", nil
    }
    tenantID := m.getCurrentTenantID()
    if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
        return "", errors.Wrap(err, "get latest session for leader: set tenant context")
    }
    query := `SELECT session_id FROM leader_checkpoints WHERE leader_id = $1 ORDER BY updated_at DESC LIMIT 1`
    row := m.dbPool.QueryRow(ctx, query, leaderID)
    var sessionID string
    if err := row.Scan(&sessionID); err != nil {
        if err == sql.ErrNoRows { return "", nil }
        return "", errors.Wrap(err, "get latest session for leader")
    }
    return sessionID, nil
}
```

**内存实现**始终返回 `("", nil)`，因为 checkpoint 不持久化。

### Checkpoint 恢复集成

```
心跳超时 → handleFailover callback → doFailover
    → checkpoint.GetLatest(ctx, leaderID)
    → strategy.HandleFailover(...)
    → recovery.RecoverStaleTasks(ctx, cp.SessionID)
```

Supervisor 将 checkpoint 的 `SessionID` 传递给 `TaskRecovery.RecoverStaleTasks`，后者将旧 leader 崩溃时正在执行的 tasks 重新入队。

## 7. Benchmark 数据

蒸馏模块 benchmark（`internal/memory/distillation/benchmark_test.go`）：

| Benchmark | 测量内容 |
|-----------|---------|
| `BenchmarkScoreMemory` | ImportanceScorer.ScoreMemory，4 组 |
| `BenchmarkConflictDetection` | cosineSimilarity，1024 维向量 |
| `BenchmarkNoiseFilter` | IsNoise，6 个样本 |
| `BenchmarkMemoryClassification` | ClassifyMemory，4 种类型 |
| `BenchmarkExperienceExtraction` | ExtractExperiences，50 条消息 |
| `BenchmarkTopNFilter` | TopNFilter，100 条，N=10 |
| `BenchmarkMemoryOperations/Create` | Memory 结构体分配 |
| `BenchmarkStringOperations/Format` | FormatExperience |

```bash
go test ./internal/memory/distillation/ -bench=. -benchmem -count=3
```

## 8. API 抽象

详见 `api/core/memory.go`，定义了 MemoryRepository 和 MemoryService 接口，支持会话管理、消息管理、任务蒸馏和相似任务检索。
