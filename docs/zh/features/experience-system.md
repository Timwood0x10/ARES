# Experience System 设计文档

## 概述

Experience System（经验系统）是 goagent 框架中用于从任务执行中自动学习和复用经验的智能组件。该系统通过蒸馏成功的任务结果，提取可复用的知识，并在后续任务中智能检索和应用这些经验，实现 Agent 的持续学习和优化。

---

## 核心理念

> **大道至简** - 核心代码 < 200 行
> **工程优雅** - 复用现有基础设施，不增加复杂度
> **低复杂度** - 极简算法，避免 ML 系统
> **高复用** - 无缝集成现有 pgvector、LLM、Embedding 服务

---

## 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                      Experience System 架构                   │
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

## 核心组件

### 1. Experience（经验模型）

**文件位置**: `internal/storage/postgres/models/experience.go`

代表一个蒸馏后的任务经验。

```go
type Experience struct {
    ID               string                 `json:"id"`
    TenantID         string                 `json:"tenant_id"`
    Type             string                 `json:"type"`          // "success" or "failure"
    Problem          string                 `json:"problem"`       // ⚠️ 问题抽象（核心字段）
    Solution         string                 `json:"solution"`      // ⚠️ 解决方案（核心字段）
    Constraints      string                 `json:"constraints"`   // ⚠️ 约束条件（核心字段）
    Embedding        []float64              `json:"embedding"`     // ⚠️ 基于 Problem 的 embedding
    EmbeddingModel   string                 `json:"embedding_model"`
    EmbeddingVersion int                    `json:"embedding_version"`
    Score            float64                `json:"score"`         // 综合评分
    Success          bool                   `json:"success"`       // 是否成功
    AgentID          string                 `json:"agent_id"`
    UsageCount       int                    `json:"usage_count"`   // ⚠️ 使用次数（Reinforcement Signal）
    DecayAt          time.Time              `json:"decay_at"`
    CreatedAt        time.Time              `json:"created_at"`
}
```

**关键设计**:
- **Experience = Distilled Knowledge**：不是 execution logs，而是抽象的问题和解决方案
- **Embedding 基于 Problem**：检索时使用 User Query，与 Problem 语义更匹配
- **UsageCount 作为强化信号**：只有在 agent 使用成功后才增加

### 2. DistillationService（蒸馏服务）

**文件位置**: `api/experience/distillation_service.go`

从任务结果中提取可复用经验。

```go
type DistillationService struct {
    llmClient       *llm.Client
    embeddingClient *embedding.EmbeddingClient
    experienceRepo  repositories.ExperienceRepositoryInterface
    logger          *slog.Logger
}
```

**核心方法**:

```go
// 判断任务是否应该被蒸馏
func (s *DistillationService) ShouldDistill(ctx context.Context, task *TaskResult) bool

// 蒸馏单个任务
func (s *DistillationService) Distill(ctx context.Context, task *TaskResult) (*Experience, error)

// 批量蒸馏
func (s *DistillationService) DistillBatch(ctx context.Context, tasks []*TaskResult) ([]*Experience, error)
```

**Heuristic 判断逻辑**:

```go
ShouldDistill =
    task.success
    AND
    task_length > 10
    AND
    result_length > 20
```

**关键改进**:
- **0 LLM call**：不使用 LLM 判断可复用性，降低成本
- **0 DB query**：不在 ShouldDistill 中做数据库查询，提升性能
- 简洁高效：符合"大道至简"原则

### 3. RankingService（排序服务）

**文件位置**: `api/experience/ranking_service.go`

多信号排序，综合语义相似度、使用频率和时间衰减。

```go
type RankingService struct {
    logger        *slog.Logger
    usageWeight   float64  // 默认 0.05
    recencyWeight float64  // 默认 0.05
    recencyDays   float64  // 默认 30.0
}
```

**排序公式**:

```
FinalScore = SemanticScore + UsageBoost + RecencyBoost

UsageBoost = min(log(1 + usage_count) * 0.05, 0.2)
RecencyBoost = exp(-age_days / 30) * 0.05
```

**关键特性**:
- **UsageBoost 上限 0.2**：防止老经验垄断 ranking
- **RecencyBoost 指数衰减**：30 天半衰期
- **保持语义主导**：SemanticScore 仍然是主要因素

### 4. ConflictResolver（冲突解决器）

**文件位置**: `api/experience/conflict_resolver.go`

Lazy conflict resolution，消除重复经验。

```go
type ConflictResolver struct {
    logger                    *slog.Logger
    problemSimilarityThreshold float64  // 默认 0.9
}
```

**Simple Clustering 算法 O(K²)**:

```go
func DetectConflictGroups(ctx context.Context, experiences []*Experience) [][]*Experience {
    // 对于 K=20，O(K²) = 400 次比较，< 0.1ms
    for i, exp1 := range experiences {
        group := []*Experience{exp1}
        
        for j := i + 1; j < len(experiences); j++ {
            exp2 := experiences[j]
            
            // 使用 Problem Embedding 计算相似度
            similarity := cosineSimilarity(exp1.Embedding, exp2.Embedding)
            
            // 相似度 > 0.9 归为同一组
            if similarity > 0.9 {
                group = append(group, exp2)
            }
        }
        
        groups = append(groups, group)
    }
    
    return groups
}
```

**关键改进**:
- **不使用 ANN**：O(K²) 在 K=20 时完全可接受
- **使用 Problem Embedding**：语义更清晰
- **每组选择最高分**：基于多信号排序

---

## 数据流

### 蒸馏流程（写入）

```
Task Execution (AgentService)
    ↓
TaskResult (success = true)
    ↓
DistillationService.Distill()
    ↓
LLM Extraction (Problem + Solution + Constraints)
    ↓
Embedding Generation (基于 Problem)
    ↓
ExperienceRepository.Create()
    ↓
PostgreSQL (experiences_1024 table)
```

### 检索流程（读取）

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

## 数据库表结构

```sql
CREATE TABLE experiences_1024 (
    id UUID PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    type VARCHAR(20) NOT NULL,           -- "success" or "failure"
    input TEXT NOT NULL,                  -- 存储问题（向后兼容）
    output TEXT NOT NULL,                 -- 存储解决方案（向后兼容）
    embedding VECTOR(1024) NOT NULL,
    embedding_model TEXT,
    embedding_version INT,
    score FLOAT DEFAULT 0.0,
    success BOOLEAN DEFAULT false,
    agent_id TEXT,
    metadata JSONB,                       -- 存储约束和使用次数
    decay_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- 索引
CREATE INDEX idx_exp_tenant ON experiences_1024(tenant_id);
CREATE INDEX idx_exp_type ON experiences_1024(type);
CREATE INDEX idx_exp_agent ON experiences_1024(agent_id);
CREATE INDEX idx_exp_embedding ON experiences_1024 USING IVFFlat(embedding vector_cosine_ops);
CREATE INDEX idx_exp_decay ON experiences_1024(decay_at);
CREATE INDEX idx_exp_usage ON experiences_1024((metadata->>'usage_count'));
```

---

## 集成方式

### 1. Retrieval Service 集成

**文件位置**: `internal/storage/postgres/services/retrieval_service.go`

```go
type RetrievalService struct {
    // ... 现有字段 ...
    
    // Experience 特定服务
    distillationService *experience.DistillationService
    rankingService      *experience.RankingService
    conflictResolver    *experience.ConflictResolver
}

// 设置 Experience 服务
func (s *RetrievalService) SetExperienceServices(
    distillationService *experience.DistillationService,
    rankingService *experience.RankingService,
    conflictResolver *experience.ConflictResolver,
) {
    s.distillationService = distillationService
    s.rankingService = rankingService
    s.conflictResolver = conflictResolver
}

// 扩展的搜索方法
func (s *RetrievalService) searchExperienceWithRanking(
    ctx context.Context, 
    req *SearchRequest,
) []*SearchResult {
    // 1. 向量检索 Top 20
    experiences := s.expRepo.SearchByVector(..., 20)
    
    // 2. 多信号排序
    ranked := s.rankingService.Rank(ctx, experiences, baseScores)
    
    // 3. 冲突解决
    resolved := s.conflictResolver.Resolve(ctx, ranked)
    
    // 4. 返回 Top 5
    return s.convertAPIExperiencesToResults(resolved[:5])
}
```

### 2. Agent Service 集成

**文件位置**: `api/agent/service.go`

```go
type AgentService struct {
    // ... 现有字段 ...
    
    // Experience 蒸馏
    experienceService *experience.DistillationService
}

// 任务执行完成后自动蒸馏
func (s *AgentService) ExecuteTask(ctx context.Context, req *ExecuteTaskRequest) (*TaskResult, error) {
    // 执行任务
    result, err := s.executeTaskInternal(ctx, req)
    if err != nil {
        return nil, err
    }
    
    // 异步蒸馏经验（不阻塞主流程）
    go s.distillExperienceAsync(context.Background(), result)
    
    return result, nil
}

// 异步蒸馏
func (s *AgentService) distillExperienceAsync(ctx context.Context, result *TaskResult) {
    // 如果使用了经验且任务成功，增加 usage
    if result.Success && result.UsedExperienceID != "" {
        go s.experienceService.IncrementUsageCount(
            context.Background(),
            result.UsedExperienceID,
        )
    }
}
```

---

## 配置选项

### Retrieval Plan 配置

```go
type RetrievalPlan struct {
    // ... 现有字段 ...
    
    // Experience 特定配置
    ExperienceRankingEnabled  bool  `json:"experience_ranking_enabled"`   // 启用排序
    ExperienceConflictResolve bool  `json:"experience_conflict_resolve"` // 启用冲突解决
    ExperienceTopK            int   `json:"experience_top_k"`            // 经验召回数量 (默认 20)
}
```

### Ranking Weights 配置

```go
type RankingWeights struct {
    UsageWeight   float64 `json:"usage_weight"`   // 使用频率权重 (默认 0.05)
    RecencyWeight float64 `json:"recency_weight"` // 时间衰减权重 (默认 0.05)
    RecencyDays   int     `json:"recency_days"`   // 时间衰减天数 (默认 30)
}
```

### Conflict Resolver 配置

```go
// 设置相似度阈值
conflictResolver.Configure(0.9) // 90% 相似度阈值
```

---

## 使用示例

### 1. 基础蒸馏

```go
import "goagent/api/experience"

// 创建任务结果
task := &experience.TaskResult{
    Task:      "Optimize PostgreSQL query performance",
    Context:   "Query is slow with 100k records",
    Result:    "Added composite index on user_id and created_at columns",
    Success:   true,
    AgentID:   "agent-1",
    TenantID:  "tenant-1",
}

// 蒸馏经验
distilled, err := distillationService.Distill(ctx, task)
if err != nil {
    slog.Error("Failed to distill", "error", err)
} else {
    slog.Info("Experience distilled", "id", distilled.ID)
}
```

### 2. 批量蒸馏

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

### 3. 配置排序权重

```go
weights := &experience.RankingWeights{
    UsageWeight:   0.1,  // 增加使用频率权重
    RecencyWeight: 0.05,
    RecencyDays:   30,
}

err := rankingService.Configure(weights)
if err != nil {
    slog.Error("Failed to configure ranking", "error", err)
}
```

### 4. 配置冲突解决

```go
err := conflictResolver.Configure(0.85) // 降低阈值，更激进的冲突检测
if err != nil {
    slog.Error("Failed to configure conflict resolver", "error", err)
}
```

---

## 性能指标

| 指标 | 目标值 | 说明 |
|------|--------|------|
| Distillation Latency | < 2s | 单个任务蒸馏时间 |
| Retrieval Latency | < 100ms | Experience 检索时间 |
| Ranking Latency | < 10ms | 排序时间（K=20） |
| Conflict Resolution | < 20ms | 冲突解决时间 |
| Storage Overhead | < 50% | Experience 存储（相对 Knowledge） |

---

## 最佳实践

### 1. 蒸馏策略

- **只蒸馏成功任务**：失败任务不会产生有用的经验
- **设置合理的长度阈值**：太短的任务不值得蒸馏
- **监控蒸馏质量**：定期检查提取的 Problem 和 Solution 质量

### 2. 排序配置

- **UsageBoost 上限 0.2**：防止老经验垄断
- **RecencyDays 根据领域调整**：快速变化的领域使用较小的值
- **保持语义主导**：SemanticScore 应该是主要因素

### 3. 冲突解决

- **相似度阈值 0.85-0.95**：根据数据质量调整
- **定期清理**：删除低质量或过期的经验
- **监控冲突率**：过高的冲突率可能需要调整提取策略

### 4. 检索优化

- **ExperienceTopK = 20**：足够的候选，不会太慢
- **最终返回 Top 5**：保持简洁，避免信息过载
- **结合 Knowledge**：Experience 和 Knowledge 混合使用

---

## 监控指标

### 蒸馏指标

```go
experience_distillation_total          // 总蒸馏次数
experience_distillation_success_rate   // 蒸馏成功率
experience_distillation_latency         // 蒸馏延迟
```

### 检索指标

```go
experience_retrieval_total             // 总检索次数
experience_retrieval_latency           // 检索延迟
experience_ranking_latency             // 排序延迟
experience_conflict_resolved_total     // 冲突解决次数
```

### 存储指标

```go
experience_storage_total               // 总经验数量
experience_usage_count_distribution    // 使用次数分布
experience_decay_rate                  // 经验衰减率
```

---

## 与 Memory Distillation 的区别

| 特性 | Memory Distillation | Experience System |
|------|-------------------|-------------------|
| **数据源** | 对话历史、任务上下文 | 任务执行结果 |
| **提取方式** | 简单打包、摘要提取 | LLM 提取 Problem/Solution/Constraints |
| **存储位置** | distilled_memories | experiences_1024 |
| **向量质量** | 简单哈希或真实 embedding | 真实 embedding（基于 Problem） |
| **排序策略** | 基于重要性、时间 | 多信号排序（语义+使用+时间） |
| **冲突处理** | 无 | Simple Clustering O(K²) |
| **使用场景** | 对话上下文、用户偏好 | 任务执行、问题解决 |
| **强化学习** | 无 | UsageCount 作为强化信号 |

---

## 故障排查

### 问题 1：没有经验被蒸馏

**可能原因**：
- 任务不符合蒸馏条件（失败、太短）
- LLM 客户端配置错误
- Embedding 服务不可用

**解决方案**：
- 检查 `ShouldDistill` 返回值
- 验证 LLM 和 Embedding 配置
- 查看 DistillationService 日志

### 问题 2：检索结果质量差

**可能原因**：
- 排序权重不合理
- ExperienceTopK 太小
- Embedding 质量差

**解决方案**：
- 调整排序权重
- 增加 ExperienceTopK 到 20
- 检查 Problem 提取质量

### 问题 3：冲突检测不准确

**可能原因**：
- 相似度阈值不合适
- Embedding 质量差
- Problem 描述不清晰

**解决方案**：
- 调整相似度阈值（0.85-0.95）
- 检查 Embedding 质量
- 优化 Problem 提取 Prompt

---

## 未来扩展

### 1. 自适应排序

- 根据领域自动调整排序权重
- 基于反馈动态优化参数

### 2. 多模态经验

- 支持图像、代码等多模态经验
- 跨模态检索和排序

### 3. 知识图谱

- 构建经验之间的关系图谱
- 支持经验推荐和补全

### 4. 迁移学习

- 支持跨租户经验迁移
- 领域自适应的经验蒸馏

---

## 参考资料

- [Experience System 开发计划](../plan/experience_system_development_plan.md)
- [Code Rules](../plan/code_rules.md)
- [Memory Distillation 文档](./memory-distillation.md)
- [Storage API 文档](./storage/api.md)

---

**版本**: 1.0  
**最后更新**: 2026-03-24  
**维护者**: GoAgent Team