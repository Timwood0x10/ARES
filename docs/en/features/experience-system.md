# Experience System Design Document

## Overview

The Experience System is an intelligent component in the goagent framework that automatically learns from task executions and reuses that knowledge in future tasks. By distilling successful task results into reusable experiences, the system enables continuous learning and optimization of agents.

---

## Core Principles

> **Simplicity First** - Core code < 200 lines
> **Engineering Elegance** - Reuse existing infrastructure without adding complexity
> **Low Complexity** - Simple algorithms, avoid ML systems
> **High Reusability** - Seamless integration with existing pgvector, LLM, and Embedding services

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                   Experience System Architecture              │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   ┌─────────────────┐     ┌──────────────────────────────┐ │
│   │   Agent Task    │────►│   DistillationService        │ │
│   │   Execution     │     │   - ShouldDistill()          │ │
│   └─────────────────┘     │   - Distill()                │ │
│                           │   - DistillBatch()           │ │
│                           └──────────────┬───────────────┘ │
│                                          │                 │
│                                          ▼                 │
│   ┌───────────────────────────────────────────────────────┐ │
│   │            PostgreSQL (experiences_1024)             │ │
│   │  Problem | Solution | Constraints | Embedding       │ │
│   └───────────────────────────────────────────────────────┘ │
│                                          │                 │
│                                          ▼                 │
│   ┌───────────────────────────────────────────────────────┐ │
│   │         Retrieval Service with Enhancement            │ │
│   │  ┌────────────────────────────────────────────────┐ │ │
│   │  │  1. Vector Search (Top 20)                     │ │ │
│   │  │  2. RankingService.Rank()                      │ │ │
│   │  │     - Semantic Score + Usage + Recency          │ │ │
│   │  │  3. ConflictResolver.Resolve()                 │ │ │
│   │  │     - Simple Clustering O(K²)                   │ │ │
│   │  │  4. Top 5 Experiences                          │ │ │
│   │  └────────────────────────────────────────────────┘ │ │
│   └───────────────────────────────────────────────────────┘ │
│                                          │                 │
│                                          ▼                 │
│   ┌───────────────────────────────────────────────────────┐ │
│   │         Agent Prompt Injection                        │ │
│   └───────────────────────────────────────────────────────┘ │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## Core Components

### 1. Experience (Experience Model)

**Location**: `internal/storage/postgres/models/experience.go`

Represents a distilled task experience.

```go
type Experience struct {
    ID               string                 `json:"id"`
    TenantID         string                 `json:"tenant_id"`
    Type             string                 `json:"type"`          // "success" or "failure"
    Problem          string                 `json:"problem"`       // ⚠️ Abstract problem (core field)
    Solution         string                 `json:"solution"`      // ⚠️ Solution approach (core field)
    Constraints      string                 `json:"constraints"`   // ⚠️ Constraints (core field)
    Embedding        []float64              `json:"embedding"`     // ⚠️ Embedding based on Problem
    EmbeddingModel   string                 `json:"embedding_model"`
    EmbeddingVersion int                    `json:"embedding_version"`
    Score            float64                `json:"score"`         // Overall score
    Success          bool                   `json:"success"`       // Success status
    AgentID          string                 `json:"agent_id"`
    UsageCount       int                    `json:"usage_count"`   // ⚠️ Usage count (Reinforcement Signal)
    DecayAt          time.Time              `json:"decay_at"`
    CreatedAt        time.Time              `json:"created_at"`
}
```

**Key Design**:
- **Experience = Distilled Knowledge**: Not execution logs, but abstract problems and solutions
- **Embedding based on Problem**: When retrieving, User Query matches Problem semantically
- **UsageCount as reinforcement signal**: Only incremented when agent uses it successfully

### 2. DistillationService (Distillation Service)

**Location**: `api/experience/distillation_service.go`

Extracts reusable experiences from task results.

```go
type DistillationService struct {
    llmClient       *llm.Client
    embeddingClient *embedding.EmbeddingClient
    experienceRepo  repositories.ExperienceRepositoryInterface
    logger          *slog.Logger
}
```

**Core Methods**:

```go
// Check if task should be distilled
func (s *DistillationService) ShouldDistill(ctx context.Context, task *TaskResult) bool

// Distill single task
func (s *DistillationService) Distill(ctx context.Context, task *TaskResult) (*Experience, error)

// Batch distillation
func (s *DistillationService) DistillBatch(ctx context.Context, tasks []*TaskResult) ([]*Experience, error)
```

**Heuristic Logic**:

```go
ShouldDistill =
    task.success
    AND
    task_length > 10
    AND
    result_length > 20
```

**Key Improvements**:
- **0 LLM call**: No LLM for reusability judgment, reduces cost
- **0 DB query**: No database query in ShouldDistill, improves performance
- Simple and efficient: Follows "Simplicity First" principle

### 3. RankingService (Ranking Service)

**Location**: `api/experience/ranking_service.go`

Multi-signal ranking combining semantic similarity, usage frequency, and time decay.

```go
type RankingService struct {
    logger        *slog.Logger
    usageWeight   float64  // Default 0.05
    recencyWeight float64  // Default 0.05
    recencyDays   float64  // Default 30.0
}
```

**Ranking Formula**:

```
FinalScore = SemanticScore + UsageBoost + RecencyBoost

UsageBoost = min(log(1 + usage_count) * 0.05, 0.2)
RecencyBoost = exp(-age_days / 30) * 0.05
```

**Key Features**:
- **UsageBoost capped at 0.2**: Prevents old experiences from dominating
- **RecencyBoost exponential decay**: 30-day half-life
- **Semantic dominance**: SemanticScore remains the primary factor

### 4. ConflictResolver (Conflict Resolver)

**Location**: `api/experience/conflict_resolver.go`

Lazy conflict resolution to eliminate duplicate experiences.

```go
type ConflictResolver struct {
    logger                    *slog.Logger
    problemSimilarityThreshold float64  // Default 0.9
}
```

**Simple Clustering Algorithm O(K²)**:

```go
func DetectConflictGroups(ctx context.Context, experiences []*Experience) [][]*Experience {
    // For K=20, O(K²) = 400 comparisons, < 0.1ms
    for i, exp1 := range experiences {
        group := []*Experience{exp1}
        
        for j := i + 1; j < len(experiences); j++ {
            exp2 := experiences[j]
            
            // Calculate similarity using Problem Embedding
            similarity := cosineSimilarity(exp1.Embedding, exp2.Embedding)
            
            // Group if similarity > 0.9
            if similarity > 0.9 {
                group = append(group, exp2)
            }
        }
        
        groups = append(groups, group)
    }
    
    return groups
}
```

**Key Improvements**:
- **No ANN**: O(K²) is perfectly acceptable for K=20
- **Use Problem Embedding**: Clearer semantics
- **Select highest score per group**: Based on multi-signal ranking

---

## Data Flow

### Distillation Flow (Write)

```
Task Execution (AgentService)
    ↓
TaskResult (success = true)
    ↓
DistillationService.Distill()
    ↓
LLM Extraction (Problem + Solution + Constraints)
    ↓
Embedding Generation (based on Problem)
    ↓
ExperienceRepository.Create()
    ↓
PostgreSQL (experiences_1024 table)
```

### Retrieval Flow (Read)

```
User Query
    ↓
RetrievalService.Search()
    ↓
Embedding Generation
    ↓
ExperienceRepository.SearchByVector() [Top 20]
    ↓
RankingService.Rank()
    ↓
ConflictResolver.Resolve()
    ↓
Top 5 Experiences
    ↓
Inject to Agent Prompt
```

---

## Database Schema

```sql
CREATE TABLE experiences_1024 (
    id UUID PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    type VARCHAR(20) NOT NULL,           -- "success" or "failure"
    input TEXT NOT NULL,                  -- Stores problem (backward compatibility)
    output TEXT NOT NULL,                 -- Stores solution (backward compatibility)
    embedding VECTOR(1024) NOT NULL,
    embedding_model TEXT,
    embedding_version INT,
    score FLOAT DEFAULT 0.0,
    success BOOLEAN DEFAULT false,
    agent_id TEXT,
    metadata JSONB,                       -- Stores constraints and usage count
    decay_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_exp_tenant ON experiences_1024(tenant_id);
CREATE INDEX idx_exp_type ON experiences_1024(type);
CREATE INDEX idx_exp_agent ON experiences_1024(agent_id);
CREATE INDEX idx_exp_embedding ON experiences_1024 USING IVFFlat(embedding vector_cosine_ops);
CREATE INDEX idx_exp_decay ON experiences_1024(decay_at);
CREATE INDEX idx_exp_usage ON experiences_1024((metadata->>'usage_count'));
```

---

## Integration

### 1. Retrieval Service Integration

**Location**: `internal/storage/postgres/services/retrieval_service.go`

```go
type RetrievalService struct {
    // ... existing fields ...
    
    // Experience-specific services
    distillationService *experience.DistillationService
    rankingService      *experience.RankingService
    conflictResolver    *experience.ConflictResolver
}

// Set Experience services
func (s *RetrievalService) SetExperienceServices(
    distillationService *experience.DistillationService,
    rankingService *experience.RankingService,
    conflictResolver *experience.ConflictResolver,
) {
    s.distillationService = distillationService
    s.rankingService = rankingService
    s.conflictResolver = conflictResolver
}

// Enhanced search method
func (s *RetrievalService) searchExperienceWithRanking(
    ctx context.Context, 
    req *SearchRequest,
) []*SearchResult {
    // 1. Vector search Top 20
    experiences := s.expRepo.SearchByVector(..., 20)
    
    // 2. Multi-signal ranking
    ranked := s.rankingService.Rank(ctx, experiences, baseScores)
    
    // 3. Conflict resolution
    resolved := s.conflictResolver.Resolve(ctx, ranked)
    
    // 4. Return Top 5
    return s.convertAPIExperiencesToResults(resolved[:5])
}
```

### 2. Agent Service Integration

**Location**: `api/agent/service.go`

```go
type AgentService struct {
    // ... existing fields ...
    
    // Experience distillation
    experienceService *experience.DistillationService
}

// Auto-distill after task execution
func (s *AgentService) ExecuteTask(ctx context.Context, req *ExecuteTaskRequest) (*TaskResult, error) {
    // Execute task
    result, err := s.executeTaskInternal(ctx, req)
    if err != nil {
        return nil, err
    }
    
    // Async distillation (doesn't block main flow)
    go s.distillExperienceAsync(context.Background(), result)
    
    return result, nil
}

// Async distillation
func (s *AgentService) distillExperienceAsync(ctx context.Context, result *TaskResult) {
    // Increment usage if experience was used and task succeeded
    if result.Success && result.UsedExperienceID != "" {
        go s.experienceService.IncrementUsageCount(
            context.Background(),
            result.UsedExperienceID,
        )
    }
}
```

---

## Configuration

### Retrieval Plan Configuration

```go
type RetrievalPlan struct {
    // ... existing fields ...
    
    // Experience-specific configuration
    ExperienceRankingEnabled  bool  `json:"experience_ranking_enabled"`   // Enable ranking
    ExperienceConflictResolve bool  `json:"experience_conflict_resolve"` // Enable conflict resolution
    ExperienceTopK            int   `json:"experience_top_k"`            // Experience recall count (default 20)
}
```

### Ranking Weights Configuration

```go
type RankingWeights struct {
    UsageWeight   float64 `json:"usage_weight"`   // Usage frequency weight (default 0.05)
    RecencyWeight float64 `json:"recency_weight"` // Time decay weight (default 0.05)
    RecencyDays   int     `json:"recency_days"`   // Time decay days (default 30)
}
```

### Conflict Resolver Configuration

```go
// Set similarity threshold
conflictResolver.Configure(0.9) // 90% similarity threshold
```

---

## Usage Examples

### 1. Basic Distillation

```go
import "goagent/api/experience"

// Create task result
task := &experience.TaskResult{
    Task:      "Optimize PostgreSQL query performance",
    Context:   "Query is slow with 100k records",
    Result:    "Added composite index on user_id and created_at columns",
    Success:   true,
    AgentID:   "agent-1",
    TenantID:  "tenant-1",
}

// Distill experience
distilled, err := distillationService.Distill(ctx, task)
if err != nil {
    slog.Error("Failed to distill", "error", err)
} else {
    slog.Info("Experience distilled", "id", distilled.ID)
}
```

### 2. Batch Distillation

```go
tasks := []*experience.TaskResult{
    {Task: "Fix memory leak", Result: "Add context cancellation", Success: true},
    {Task: "Add rate limiting", Result: "Token bucket algorithm", Success: true},
}

experiences, err := distillationService.DistillBatch(ctx, tasks)
if err != nil {
    slog.Error("Failed to distill batch", "error", err)
} else {
    slog.Info("Batch distilled", "count", len(experiences))
}
```

### 3. Configure Ranking Weights

```go
weights := &experience.RankingWeights{
    UsageWeight:   0.1,  // Increase usage weight
    RecencyWeight: 0.05,
    RecencyDays:   30,
}

err := rankingService.Configure(weights)
if err != nil {
    slog.Error("Failed to configure ranking", "error", err)
}
```

### 4. Configure Conflict Resolution

```go
err := conflictResolver.Configure(0.85) // Lower threshold for more aggressive conflict detection
if err != nil {
    slog.Error("Failed to configure conflict resolver", "error", err)
}
```

---

## Performance Metrics

| Metric | Target | Description |
|--------|--------|-------------|
| Distillation Latency | < 2s | Single task distillation time |
| Retrieval Latency | < 100ms | Experience retrieval time |
| Ranking Latency | < 10ms | Ranking time (K=20) |
| Conflict Resolution | < 20ms | Conflict resolution time |
| Storage Overhead | < 50% | Experience storage (relative to Knowledge) |

---

## Best Practices

### 1. Distillation Strategy

- **Distill only successful tasks**: Failed tasks don't produce useful experiences
- **Set reasonable length thresholds**: Too short tasks aren't worth distilling
- **Monitor distillation quality**: Regularly check extracted Problem and Solution quality

### 2. Ranking Configuration

- **UsageBoost capped at 0.2**: Prevents old experiences from dominating
- **RecencyDays based on domain**: Use smaller values for fast-changing domains
- **Keep semantic dominance**: SemanticScore should remain the primary factor

### 3. Conflict Resolution

- **Similarity threshold 0.85-0.95**: Adjust based on data quality
- **Regular cleanup**: Delete low-quality or expired experiences
- **Monitor conflict rate**: High conflict rate may need extraction strategy adjustment

### 4. Retrieval Optimization

- **ExperienceTopK = 20**: Sufficient candidates without being too slow
- **Return Top 5**: Keep it concise, avoid information overload
- **Combine with Knowledge**: Use Experience and Knowledge together

---

## Monitoring Metrics

### Distillation Metrics

```go
experience_distillation_total          // Total distillation count
experience_distillation_success_rate   // Distillation success rate
experience_distillation_latency         // Distillation latency
```

### Retrieval Metrics

```go
experience_retrieval_total             // Total retrieval count
experience_retrieval_latency           // Retrieval latency
experience_ranking_latency             // Ranking latency
experience_conflict_resolved_total     // Conflict resolution count
```

### Storage Metrics

```go
experience_storage_total               // Total experience count
experience_usage_count_distribution    // Usage count distribution
experience_decay_rate                  // Experience decay rate
```

---

## Comparison with Memory Distillation

| Feature | Memory Distillation | Experience System |
|---------|-------------------|-------------------|
| **Data Source** | Conversation history, task context | Task execution results |
| **Extraction** | Simple packing, summary extraction | LLM extraction of Problem/Solution/Constraints |
| **Storage** | distilled_memories | experiences_1024 |
| **Vector Quality** | Simple hash or real embedding | Real embedding (based on Problem) |
| **Ranking** | Based on importance, time | Multi-signal ranking (semantic+usage+time) |
| **Conflict Handling** | None | Simple Clustering O(K²) |
| **Use Case** | Conversation context, user preferences | Task execution, problem solving |
| **Reinforcement Learning** | None | UsageCount as reinforcement signal |

---

## Troubleshooting

### Issue 1: No experiences being distilled

**Possible causes**:
- Tasks don't meet distillation criteria (failed, too short)
- LLM client misconfigured
- Embedding service unavailable

**Solutions**:
- Check `ShouldDistill` return value
- Verify LLM and Embedding configuration
- Review DistillationService logs

### Issue 2: Poor retrieval quality

**Possible causes**:
- Ranking weights unreasonable
- ExperienceTopK too small
- Poor embedding quality

**Solutions**:
- Adjust ranking weights
- Increase ExperienceTopK to 20
- Check Problem extraction quality

### Issue 3: Inaccurate conflict detection

**Possible causes**:
- Similarity threshold inappropriate
- Poor embedding quality
- Unclear problem descriptions

**Solutions**:
- Adjust similarity threshold (0.85-0.95)
- Check embedding quality
- Optimize Problem extraction prompt

---

## Future Extensions

### 1. Adaptive Ranking

- Automatically adjust ranking weights based on domain
- Dynamically optimize parameters based on feedback

### 2. Multi-modal Experiences

- Support images, code, and other multi-modal experiences
- Cross-modal retrieval and ranking

### 3. Knowledge Graph

- Build relationship graphs between experiences
- Support experience recommendation and completion

### 4. Transfer Learning

- Support cross-tenant experience migration
- Domain-adaptive experience distillation

---

## References

- [Experience System Development Plan](../plan/experience_system_development_plan.md)
- [Code Rules](../plan/code_rules.md)
- [Memory Distillation Documentation](./memory-distillation_en.md)
- [Storage API Documentation](./storage/api_en.md)

---

**Version**: 1.0  
**Last Updated**: 2026-03-24  
**Maintainer**: GoAgent Team