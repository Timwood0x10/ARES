# Memory System Design Document

## 1. Overview

The Memory System module is a core component of the goagent framework, responsible for managing various types of memory data in the system, including session memory, task memory, and distilled memories. It implements short-term session context management, task execution tracking, and long-term experience extraction and retrieval.

### Core Features

- **Session Memory Management**: Manages conversation session context and message history
- **Task Memory Tracking**: Records task execution process including inputs, outputs, steps, and results
- **Memory Distillation**: Extracts key information from conversation history for future retrieval
- **Vector Search**: Supports semantic similarity-based task and memory retrieval

## 2. System Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Memory System Architecture                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                         Application Layer                            │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────────┐  │   │
│  │  │  Leader Agent│  │  Sub Agent   │  │  Knowledge Base App      │  │   │
│  │  └──────┬───────┘  └──────┬───────┘  └───────────┬──────────────┘  │   │
│  └─────────┼──────────────────┼──────────────────────┼─────────────────┘   │
│            │                  │                      │                        │
│  ┌─────────▼──────────────────▼──────────────────────▼─────────────────┐   │
│  │                       MemoryManager                                │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────────┐  │   │
│  │  │ SessionMemory│  │  TaskMemory  │  │   Distillation Engine    │  │   │
│  │  │  (In-Memory) │  │  (In-Memory) │  │  ┌────────────────────┐  │  │   │
│  │  └──────────────┘  └──────────────┘  │  │ ExperienceExtractor │  │  │   │
│  │                                          │  MemoryClassifier   │  │  │   │
│  │  ┌──────────────────────────────────┐  │  ImportanceScorer   │  │  │   │
│  │  │    Local Distilled Tasks         │  │  ConflictResolver   │  │  │   │
│  │  │    (Hash-based Vector)           │  │  NoiseFilter        │  │  │   │
│  │  └──────────────────────────────────┘  └────────────────────┘  │  │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│            │                              │                                │
│  ┌─────────▼──────────────────────────────▼──────────────────────────┐  │
│  │                      Storage Layer                                 │  │
│  │                                                                     │  │
│  │  ┌────────────────────────────────────────────────────────────┐   │  │
│  │  │                 PostgreSQL + pgvector                        │   │  │
│  │  │  ┌────────────────────┐  ┌────────────────────────────────┐  │   │  │
│  │  │  │ knowledge_chunks_1024│ │ experiences (New Engine)      │  │   │  │
│  │  │  │ - Document chunks    │  │ - Distilled memories         │  │   │  │
│  │  │  │ - Vector index       │  │ - Type classification        │  │   │  │
│  │  │  │ - Semantic search     │  │ - Importance scoring         │  │   │  │
│  │  │  └────────────────────┘  └────────────────────────────────┘  │   │  │
│  │  └────────────────────────────────────────────────────────────┘   │  │
│  │                                                                     │  │
│  │  ┌────────────────────────────────────────────────────────────┐   │  │
│  │  │                  Embedding Service                           │   │  │
│  │  │  - OpenAI API                                               │   │  │
│  │  │  - Local Embedding service                                  │   │  │
│  │  └────────────────────────────────────────────────────────────┘   │  │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 3. Storage Strategy

### 3.1 SessionMemory

**Storage Location**: In-Memory
**Lifecycle**: Session duration (default 24h)
**Purpose**: Manage conversation session context and message history

**Data Structure**:
```go
// internal/memory/context/session.go
type SessionMemory struct {
    sessions map[string]*SessionData
    mu       sync.RWMutex
    maxSize  int
    ttl      time.Duration
}

type SessionData struct {
    SessionID   string
    UserID      string
    Messages    []Message
    CreatedAt   time.Time
    AccessedAt  time.Time
}
```

**Key Operations**:
```go
// Create session
sessionID := memoryManager.CreateSession(ctx, userID)

// Add message
memoryManager.AddMessage(ctx, sessionID, "user", "Hello")

// Get messages
messages := memoryManager.GetMessages(ctx, sessionID)

// Delete session
memoryManager.DeleteSession(ctx, sessionID)
```

### 3.2 TaskMemory

**Storage Location**: In-Memory
**Lifecycle**: Task duration (default 1h)
**Purpose**: Record task execution process, support task distillation

**Data Structure**:
```go
// internal/memory/context/task.go
type TaskMemory struct {
    tasks   map[string]*TaskData
    mu      sync.RWMutex
    maxSize int
    ttl     time.Duration
}

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

**Key Operations**:
```go
// Create task
taskID := memoryManager.CreateTask(ctx, sessionID, userID, input)

// Update output
memoryManager.UpdateTaskOutput(ctx, taskID, output)

// Add execution step
memoryManager.taskMemory.AddStep(ctx, taskID, StepRecord{
    Name:   "tool_call",
    Input:  "query database",
    Output: "results",
})

// Distill task
distilled := memoryManager.DistillTask(ctx, taskID)
```

### 3.3 Distilled Memory Storage

#### Method 1: Local Hash Vector Storage (Legacy)

**Storage Location**: In-Memory map
**Vector Generation**: Simple hash-based vector
**Features**: Fast, lightweight, but weak semantic retrieval capability

**Implementation Code**:
```go
// internal/memory/manager_impl.go:281
func (m *memoryManager) generateHashVector(text string) []float64 {
    vector := make([]float64, m.vectorDim)
    
    if len(text) == 0 {
        return vector
    }
    
    // Simple hash algorithm
    hash := uint64(0)
    for i, c := range text {
        hash = hash*31 + uint64(c)
        if i >= len(text)-1 {
            break
        }
    }
    
    // Spread hash across vector dimensions
    for i := range vector {
        vector[i] = float64((hash>>(i*5))%1000) / 1000.0
    }
    
    // L2 normalization
    norm := 0.0
    for _, v := range vector {
        norm += v * v
    }
    norm = math.Sqrt(norm)
    
    if norm > 0 {
        for i := range vector {
            vector[i] /= norm
        }
    }
    
    return vector
}
```

**Storage Structure**:
```go
type DistilledTaskData struct {
    TaskID    string
    Input     string
    Output    string
    Context   map[string]interface{}
    Vector    []float64  // Hash vector
    CreatedAt time.Time
}
```

#### Method 2: PostgreSQL + pgvector (New)

**Storage Location**: PostgreSQL
**Vector Generation**: Real Embedding model
**Features**: Strong semantic retrieval capability, multi-tenant isolation

**Database Schema**:
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

-- Vector index
CREATE INDEX idx_chunks_embedding ON knowledge_chunks_1024 
    USING IVFFlat(embedding vector_cosine_ops)
    WITH (lists = 100);

-- Tenant isolation index
CREATE INDEX idx_chunks_tenant ON knowledge_chunks_1024(tenant_id);
```

**Distillation Storage Code**:
```go
// examples/knowledge-base/main.go:609
func (kb *KnowledgeBase) distillMemory(ctx context.Context, tenantID string) {
    // 1. Get conversation history
    messages, err := kb.memory.GetMessages(ctx, kb.sessionID)
    
    // 2. Build conversation summary
    var summary strings.Builder
    summary.WriteString("Conversation Summary:\n\n")
    for _, msg := range messages {
        fmt.Fprintf(&summary, "%s: %s\n", msg.Role, msg.Content)
    }
    
    // 3. Generate embedding vector
    embedding, err := kb.embedding.EmbedWithPrefix(ctx, summaryText, "memory:")
    
    // 4. Normalize vector
    embedding = postgres.NormalizeVector(embedding)
    
    // 5. Store in knowledge base
    distilledChunk := &storage_models.KnowledgeChunk{
        TenantID:         tenantID,
        Content:          summaryText,
        Embedding:        embedding,
        EmbeddingModel:   kb.config.EmbeddingModel,
        SourceType:       "distilled",
        Source:           fmt.Sprintf("memory:%s", kb.sessionID),
    }
    kb.repo.Create(ctx, distilledChunk)
}
```

## 4. Memory Distillation Module

### 4.1 Module Overview

The memory distillation module (`internal/memory/distillation/`) is the next-generation memory extraction engine for intelligently extracting and classifying key information from conversation history.

### 4.2 Architecture Design

```
┌─────────────────────────────────────────────────────────────────────┐
│                    Memory Distillation Engine Architecture            │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Input: Conversation History []Message                               │
│          │                                                          │
│          ▼                                                          │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              1. ExperienceExtractor                          │   │
│  │  - Detect problem/solution pairs                             │   │
│  │  - Support cross-turn extraction                             │   │
│  │  - Filter noise content                                      │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │ []Experience                          │
│                             ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              2. MemoryClassifier                            │   │
│  │  - Classify memory types: fact/preference/solution/rule       │   │
│  │  - Auto-classify based on content features                   │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │ []Memory (with type)                 │
│                             ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              3. ImportanceScorer                            │   │
│  │  - Calculate memory importance score                         │   │
│  │  - Support length bonus                                      │   │
│  │  - Filter low importance memories                            │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │ []Memory (with score)                 │
│                             ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              4. ConflictResolver                            │   │
│  │  - Detect similar memory conflicts                           │   │
│  │  - Resolution strategy: replace/keep both/merge              │   │
│  │  - Vector similarity based detection                         │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │ []Memory (deduplicated)              │
│                             ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              5. Top-N Filter & Cap                          │   │
│  │  - Limit memories per distillation                           │   │
│  │  - Enforce solution cap                                      │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │                                      │
│                             ▼                                      │
│  Output: []Memory (distilled, classified, scored memories)          │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 4.3 Core Components

#### ExperienceExtractor

**File Location**: `internal/memory/distillation/extractor.go`

**Function**: Extract problem-solution pairs from conversations

**Key Code**:
```go
type ExperienceExtractor struct {
    questionDetector *QuestionDetector
    noiseFilter      *NoiseFilter
    enableCrossTurn  bool
}

// Extract direct conversation pair
func (e *ExperienceExtractor) extractDirectExperience(user, assistant Message) *Experience {
    problem := strings.TrimSpace(user.Content)
    solution := strings.TrimSpace(assistant.Content)
    
    // Extract core solution
    solution = e.extractCoreSolution(solution)
    
    return &Experience{
        Problem:    problem,
        Solution:   solution,
        Confidence: e.calculateConfidence(problem, solution),
        ExtractionMethod: ExtractionDirect,
    }
}

// Extract cross-turn conversation pair (user -> assistant(clarification) -> user(more) -> assistant(answer))
func (e *ExperienceExtractor) extractCrossTurnExperience(user, a1, m2, a2 Message) *Experience {
    problem := user.Content
    if m2.Role == "user" {
        problem += " " + m2.Content
    }
    
    solution := strings.TrimSpace(a2.Content)
    solution = e.extractCoreSolution(solution)
    
    return &Experience{
        Problem:    strings.TrimSpace(problem),
        Solution:   solution,
        Confidence: e.calculateConfidence(problem, solution),
        ExtractionMethod: ExtractionCrossTurn,
    }
}
```

#### MemoryClassifier

**File Location**: `internal/memory/distillation/classifier.go`

**Function**: Classify extracted experiences into four memory types

**Memory Types**:
- **fact**: Factual information
- **preference**: User preferences
- **solution**: Solutions/methods
- **rule**: Rules/patterns

**Classification Logic**:
```go
func (c *MemoryClassifier) ClassifyMemory(exp *Experience) MemoryType {
    solution := strings.ToLower(exp.Solution)
    
    // Detect solution type
    if c.isSolutionPattern(solution) {
        return MemorySolution
    }
    
    // Detect preference type
    if c.isPreferencePattern(exp.Problem, solution) {
        return MemoryPreference
    }
    
    // Detect rule type
    if c.isRulePattern(solution) {
        return MemoryRule
    }
    
    // Default to fact type
    return MemoryFact
}
```

#### ImportanceScorer

**File Location**: `internal/memory/distillation/scorer.go`

**Function**: Calculate memory importance scores

**Scoring Factors**:
- Solution length (length bonus)
- Presence of action verbs
- Specificity indicators

**Scoring Code**:
```go
func (s *ImportanceScorer) ScoreMemory(memoryType MemoryType, problem, solution string) float64 {
    score := 0.5
    
    // Base score based on memory type
    switch memoryType {
    case MemorySolution:
        score = 0.7
    case MemoryPreference:
        score = 0.6
    case MemoryRule:
        score = 0.65
    case MemoryFact:
        score = 0.5
    }
    
    // Length bonus
    if s.enableLengthBonus && len(solution) > s.lengthThreshold {
        score += s.lengthBonus
    }
    
    // Action verb bonus
    actionVerbs := []string{"restart", "run", "execute", "install", "configure"}
    lower := strings.ToLower(solution)
    for _, verb := range actionVerbs {
        if strings.Contains(lower, verb) {
            score += 0.05
            break
        }
    }
    
    return score
}
```

#### ConflictResolver

**File Location**: `internal/memory/distillation/resolver.go`

**Function**: Detect and resolve memory conflicts

**Conflict Detection**:
```go
// DetectConflict detects conflicts with existing memories.
// Args:
//   - ctx: operation context
//   - vector: embedding vector to search for similar memories
//   - tenantID: tenant ID for multi-tenancy
// Returns:
//   - *Experience: conflicting memory, or nil if no conflict
//   - error: any error encountered
func (r *ConflictResolver) DetectConflict(ctx context.Context, vector []float64, tenantID string) (*Experience, error) {
    if r.repo == nil {
        return nil, nil // No repository configured
    }

    if len(vector) == 0 {
        return nil, nil // No vector provided
    }

    // Vector search for similar memories
    similar, err := r.repo.SearchByVector(ctx, vector, tenantID, r.searchLimit)
    if err != nil {
        return nil, errors.Wrap(err, "failed to search for similar memories")
    }

    if len(similar) == 0 {
        return nil, nil
    }

    // Check for high similarity memories
    for i := range similar {
        if len(similar[i].Vector) == 0 {
            continue
        }
        similarity := r.cosineSimilarity(vector, similar[i].Vector)
        if similarity > r.conflictThreshold {
            return &similar[i], nil
        }
    }

    return nil, nil
}
```

**Conflict Resolution Strategies**:
```go
type ResolutionStrategy string

const (
    ReplaceOld ResolutionStrategy = "replace" // Replace old memory with new
    KeepBoth   ResolutionStrategy = "version" // Keep both versions
    Merge      ResolutionStrategy = "merge"   // Merge memories (future)
)

func (r *ConflictResolver) ResolveConflict(newExp, oldExp *Experience) ResolutionStrategy {
    // Choose strategy based on confidence
    if newExp.Confidence > oldExp.Confidence + 0.1 {
        return ReplaceOld
    }
    return KeepBoth
}
```

### 4.4 Usage Example

#### Creating Distillation Engine

```go
import (
    "goagent/internal/memory/distillation"
    "goagent/internal/storage/postgres/embedding"
)

// 1. Create Embedding service
embedder := embedding.NewEmbeddingClient(config.EmbeddingServiceURL, config.EmbeddingModel)

// 2. Create Experience Repository
repo := repositories.NewExperienceRepository(pool)

// 3. Create distillation configuration
distillConfig := distillation.DefaultDistillationConfig()
distillConfig.MinImportance = 0.6
distillConfig.MaxMemoriesPerDistillation = 3

// 4. Create distillation engine
distiller := distillation.NewDistiller(distillConfig, embedder, repo)
```

#### Executing Distillation

```go
// Prepare conversation history
messages := []distillation.Message{
    {Role: "user", Content: "How to install Go?"},
    {Role: "assistant", Content: "You can install Go by following these steps: 1. Visit go.dev/dl..."},
    {Role: "user", Content: "How to install on Mac?"},
    {Role: "assistant", Content: "On Mac, you can use Homebrew: brew install go"},
}

// Execute distillation
memories, err := distiller.DistillConversation(
    ctx,
    "conversation_123",
    messages,
    "tenant_abc",
    "user_456",
)

// Process distillation results
for _, mem := range memories {
    fmt.Printf("Type: %s, Importance: %.2f\n", mem.Type, mem.Importance)
    fmt.Printf("Content: %s\n", mem.Content)
    
    // Store to database
    // repo.Create(ctx, mem)
}
```

### 4.5 Configuration Parameters

```go
type DistillationConfig struct {
    // Importance filtering
    MinImportance              float64  // Minimum importance score (default: 0.6)
    
    // Conflict detection
    ConflictThreshold          float64  // Conflict detection threshold (default: 0.85)
    ConflictSearchLimit        int      // Conflict search limit (default: 5)
    
    // Quantity limits
    MaxMemoriesPerDistillation int      // Max memories per distillation (default: 3)
    MaxSolutionsPerTenant      int      // Max solutions per tenant (default: 5000)
    
    // Noise filtering
    EnableCodeFilter           bool     // Enable code block filtering
    EnableStacktraceFilter     bool     // Enable stacktrace filtering
    EnableLogFilter            bool     // Enable log filtering
    EnableMarkdownTableFilter  bool     // Enable markdown table filtering
    
    // Cross-turn extraction
    EnableCrossTurnExtraction  bool     // Enable cross-turn conversation extraction
    
    // Length bonus
    EnableLengthBonus          bool     // Enable length bonus
    LengthThreshold            int      // Length threshold (default: 60)
    LengthBonus                float64  // Length bonus value (default: 0.1)
    
    // Performance optimization
    TopNBeforeConflict         bool     // Top-N filter before conflict detection
    PrecisionOverRecall        bool     // Prioritize precision over recall
}
```

## 5. Similar Task Retrieval

### 5.1 Hash Vector Retrieval (Legacy)

```go
// internal/memory/manager_impl.go:319
func (m *memoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
    // 1. Generate hash vector for query
    queryVector := m.generateHashVector(query)
    
    // 2. Calculate cosine similarity
    var results []*models.Task
    for _, data := range m.distilledTasks {
        score := m.cosineSimilarity(queryVector, data.Vector)
        if score > 0.5 {
            results = append(results, &models.Task{
                TaskID: data.TaskID,
                Payload: map[string]any{
                    "input":   data.Input,
                    "output":  data.Output,
                    "context": data.Context,
                },
            })
        }
    }
    
    // 3. Sort by score
    sort.Slice(results, func(i, j int) bool {
        return results[i].Score > results[j].Score
    })
    
    // 4. Apply limit
    if len(results) > limit {
        results = results[:limit]
    }
    
    return results, nil
}
```

### 5.2 Vector Retrieval (New)

```go
// internal/memory/manager_impl.go:419
func (m *memoryManager) searchSimilarTasksNew(ctx context.Context, query string, limit int) ([]*models.Task, error) {
    // 1. Generate query vector
    queryVector, err := m.embedder.EmbedWithPrefix(ctx, query, "query:")
    if err != nil {
        return nil, err
    }
    
    // 2. Vector search
    experiences, err := m.expRepo.SearchByVector(ctx, queryVector, "default", limit)
    if err != nil {
        return nil, err
    }
    
    // 3. Convert to task objects
    tasks := make([]*models.Task, 0, len(experiences))
    for _, exp := range experiences {
        task := &models.Task{
            TaskID: exp.ID,
            Payload: map[string]any{
                "input":   exp.Input,
                "output":  exp.Output,
                "context": exp.Metadata,
            },
        }
        tasks = append(tasks, task)
    }
    
    return tasks, nil
}
```

## 6. Configuration Parameters Summary

| Parameter | Default | Description |
|-----------|---------|-------------|
| session_ttl | 24h | Session expiration time |
| task_ttl | 1h | Task expiration time |
| max_sessions | 1000 | Maximum number of sessions |
| max_tasks | 10000 | Maximum number of tasks |
| max_history | 100 | History message limit |
| vector_dimension | 1024 | Vector dimension |
| distillation_threshold | 3 | Conversation rounds to trigger distillation |
| enable_distillation | true | Enable memory distillation |

## 7. API Abstraction

See `api/core/memory.go` for MemoryRepository and MemoryService interfaces, supporting:
- Session management
- Message management
- Task distillation
- Similar task retrieval
