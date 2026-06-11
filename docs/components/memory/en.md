# Memory System Design Document

## 1. Overview

The Memory System manages session memory, task memory, and distilled memories for the goagent framework.

**Core Features**: session context management, task execution tracking, memory distillation (experience extraction), vector-based semantic search.

## 2. Storage Strategy

### SessionMemory

**Storage**: In-Memory | **Lifecycle**: Session duration (default 24h)

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

**Storage**: In-Memory | **Lifecycle**: Task duration (default 1h)

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

### Distilled Memory: PostgreSQL + pgvector

**Storage**: PostgreSQL | **Vector**: Real Embedding model | **Features**: Semantic retrieval, multi-tenant isolation

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

## 3. Memory Distillation Engine

The distillation module (`internal/memory/distillation/`) extracts and classifies key information from conversation history through a 5-stage pipeline:

```
[]Message → ExperienceExtractor → MemoryClassifier → ImportanceScorer
    → ConflictResolver → Top-N Filter → []Memory
```

### ExperienceExtractor

Extracts problem-solution pairs. Supports direct pairs and cross-turn extraction (user -> assistant(clarify) -> user(add) -> assistant(answer)).

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

Classifies experiences into 4 types: **fact**, **preference**, **solution**, **rule**.

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

Scores memories based on type, length bonus, and action verb presence.

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
    // action verb bonus: +0.05 for "restart", "run", "execute", etc.
    return score
}
```

### ConflictResolver

Detects similar memories via vector cosine similarity. Strategies: **replace** (new overwrites old if confidence +0.1 higher), **version** (keep both).

```go
func (r *ConflictResolver) DetectConflict(ctx context.Context, vector []float64, tenantID string) (*Experience, error) {
    similar, err := r.repo.SearchByVector(ctx, vector, tenantID, r.searchLimit)
    // ... check cosine similarity > conflictThreshold (default 0.85)
}
```

### Usage

```go
embedder := embedding.NewEmbeddingClient(config.EmbeddingServiceURL, config.EmbeddingModel)
repo := repositories.NewExperienceRepository(pool)
distillConfig := distillation.DefaultDistillationConfig()
distillConfig.MinImportance = 0.6
distillConfig.MaxMemoriesPerDistillation = 3
distiller := distillation.NewDistiller(distillConfig, embedder, repo)

memories, err := distiller.DistillConversation(ctx, "conv_123", messages, "tenant_abc", "user_456")
```

## 4. Similar Task Retrieval

```go
// PostgreSQL + pgvector based retrieval
func (m *memoryManager) searchSimilarTasksNew(ctx context.Context, query string, limit int) ([]*models.Task, error) {
    queryVector, err := m.embedder.EmbedWithPrefix(ctx, query, "query:")
    experiences, err := m.expRepo.SearchByVector(ctx, queryVector, "default", limit)
    // convert to []*models.Task ...
}
```

## 5. Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| session_ttl | 24h | Session expiration time |
| task_ttl | 1h | Task expiration time |
| max_sessions | 1000 | Maximum number of sessions |
| max_tasks | 10000 | Maximum number of tasks |
| vector_dimension | 1024 | Vector dimension |
| distillation_threshold | 3 | Conversation rounds to trigger distillation |
| MinImportance | 0.6 | Minimum importance score for distillation |
| ConflictThreshold | 0.85 | Similarity threshold for conflict detection |
| MaxMemoriesPerDistillation | 3 | Max memories per distillation run |

## 6. Leader Session Recovery

### GetLatestSessionForLeader

Retrieves the most recent session ID for a leader from `leader_checkpoints`. Enables failover recovery -- a promoted leader resumes from the last checkpoint's session.

**Interface** (`internal/memory/manager.go`):
```go
GetLatestSessionForLeader(ctx context.Context, leaderID string) (string, error)
```

**Production implementation** (`internal/memory/production_manager.go`):
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

**In-memory implementation** always returns `("", nil)` since checkpoints are not persisted.

### Checkpoint Recovery Integration

```
Heartbeat timeout → handleFailover callback → doFailover
    → checkpoint.GetLatest(ctx, leaderID)
    → strategy.HandleFailover(...)
    → recovery.RecoverStaleTasks(ctx, cp.SessionID)
```

The supervisor passes the checkpoint `SessionID` to `TaskRecovery.RecoverStaleTasks`, which re-queues in-flight tasks from the dead leader.

## 7. Benchmark Data

Benchmarks in `internal/memory/distillation/benchmark_test.go`:

| Benchmark | Measures |
|-----------|---------|
| `BenchmarkScoreMemory` | ImportanceScorer.ScoreMemory, 4 pairs |
| `BenchmarkConflictDetection` | cosineSimilarity on 1024-dim vectors |
| `BenchmarkNoiseFilter` | IsNoise on 6 text samples |
| `BenchmarkMemoryClassification` | ClassifyMemory across 4 types |
| `BenchmarkExperienceExtraction` | ExtractExperiences on 50 messages |
| `BenchmarkTopNFilter` | TopNFilter, 100 experiences, N=10 |
| `BenchmarkMemoryOperations/Create` | Memory struct allocation |
| `BenchmarkStringOperations/Format` | FormatExperience |

```bash
go test ./internal/memory/distillation/ -bench=. -benchmem -count=3
```

## 8. API Abstraction

See `api/core/memory.go` for MemoryRepository and MemoryService interfaces, supporting session management, message management, task distillation, and similar task retrieval.
