# Memory System 设计文档

## 1. 概述

Memory System 模块是 goagent 框架的核心组件，负责管理系统中的各类内存数据，包括会话内存、任务内存和蒸馏记忆，实现短期会话上下文管理、任务执行追踪以及长期经验提取与检索。

### 核心功能

- **会话内存管理**: 管理对话会话的上下文和历史消息
- **任务内存追踪**: 记录任务执行过程中的输入、输出、步骤和结果
- **记忆蒸馏**: 从对话历史中提取关键信息，生成可检索的经验记忆
- **向量检索**: 支持基于语义相似度的任务和记忆检索

## 2. 系统架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Memory System 架构                                │
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
│  │  │  │ - 文档块            │  │ - 蒸馏记忆                    │  │   │  │
│  │  │  │ - 向量索引          │  │ - 类型分类                    │  │   │  │
│  │  │  │ - 语义检索          │  │ - 重要性评分                  │  │   │  │
│  │  │  └────────────────────┘  └────────────────────────────────┘  │   │  │
│  │  └────────────────────────────────────────────────────────────┘   │  │
│  │                                                                     │  │
│  │  ┌────────────────────────────────────────────────────────────┐   │  │
│  │  │                  Embedding Service                           │   │  │
│  │  │  - OpenAI API                                               │   │  │
│  │  │  - 本地 Embedding 服务                                       │   │  │
│  │  └────────────────────────────────────────────────────────────┘   │  │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 3. 存储策略

### 3.1 SessionMemory

**存储位置**: 内存
**生命周期**: 会话期间 (默认 24h)
**用途**: 管理对话会话的上下文和历史消息

**数据结构**:
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

**关键操作**:
```go
// 创建会话
sessionID := memoryManager.CreateSession(ctx, userID)

// 添加消息
memoryManager.AddMessage(ctx, sessionID, "user", "Hello")

// 获取消息
messages := memoryManager.GetMessages(ctx, sessionID)

// 删除会话
memoryManager.DeleteSession(ctx, sessionID)
```

### 3.2 TaskMemory

**存储位置**: 内存
**生命周期**: 任务期间 (默认 1h)
**用途**: 记录任务执行过程，支持任务蒸馏

**数据结构**:
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

**关键操作**:
```go
// 创建任务
taskID := memoryManager.CreateTask(ctx, sessionID, userID, input)

// 更新输出
memoryManager.UpdateTaskOutput(ctx, taskID, output)

// 添加执行步骤
memoryManager.taskMemory.AddStep(ctx, taskID, StepRecord{
    Name:   "tool_call",
    Input:  "query database",
    Output: "results",
})

// 蒸馏任务
distilled := memoryManager.DistillTask(ctx, taskID)
```

### 3.3 蒸馏记忆存储

#### 方式一：本地哈希向量存储 (旧版)

**存储位置**: 内存 map
**向量生成**: 基于文本哈希的简单向量
**特点**: 快速、轻量，但语义检索能力弱

**实现代码**:
```go
// internal/memory/manager_impl.go:281
func (m *memoryManager) generateHashVector(text string) []float64 {
    vector := make([]float64, m.vectorDim)
    
    if len(text) == 0 {
        return vector
    }
    
    // 简单哈希算法
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

**存储结构**:
```go
type DistilledTaskData struct {
    TaskID    string
    Input     string
    Output    string
    Context   map[string]interface{}
    Vector    []float64  // 哈希向量
    CreatedAt time.Time
}
```

#### 方式二：PostgreSQL + pgvector (新版)

**存储位置**: PostgreSQL
**向量生成**: 真实 Embedding 模型
**特点**: 语义检索能力强，支持多租户隔离

**数据库表结构**:
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

-- 向量索引
CREATE INDEX idx_chunks_embedding ON knowledge_chunks_1024 
    USING IVFFlat(embedding vector_cosine_ops)
    WITH (lists = 100);

-- 租户隔离索引
CREATE INDEX idx_chunks_tenant ON knowledge_chunks_1024(tenant_id);
```

**蒸馏存储代码**:
```go
// examples/knowledge-base/main.go:609
func (kb *KnowledgeBase) distillMemory(ctx context.Context, tenantID string) {
    // 1. 获取对话历史
    messages, err := kb.memory.GetMessages(ctx, kb.sessionID)
    
    // 2. 构建对话摘要
    var summary strings.Builder
    summary.WriteString("Conversation Summary:\n\n")
    for _, msg := range messages {
        fmt.Fprintf(&summary, "%s: %s\n", msg.Role, msg.Content)
    }
    
    // 3. 生成 Embedding 向量
    embedding, err := kb.embedding.EmbedWithPrefix(ctx, summaryText, "memory:")
    
    // 4. 归一化向量
    embedding = postgres.NormalizeVector(embedding)
    
    // 5. 存储到知识库
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

## 4. 记忆蒸馏模块

### 4.1 模块概述

记忆蒸馏模块 (`internal/memory/distillation/`) 是新一代的记忆提取引擎，用于从对话历史中智能提取和分类关键信息。

### 4.2 架构设计

```
┌─────────────────────────────────────────────────────────────────────┐
│                      记忆蒸馏引擎架构                                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  输入: 对话历史 []Message                                            │
│          │                                                          │
│          ▼                                                          │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              1. ExperienceExtractor                          │   │
│  │  - 检测问题/解决方案对                                        │   │
│  │  - 支持跨轮对话提取 (Cross-turn Extraction)                   │   │
│  │  - 过滤噪音内容                                               │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │ []Experience                          │
│                             ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              2. MemoryClassifier                            │   │
│  │  - 分类记忆类型: fact/preference/solution/rule                │   │
│  │  - 基于内容特征自动分类                                       │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │ []Memory (带类型)                     │
│                             ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              3. ImportanceScorer                            │   │
│  │  - 计算记忆重要性评分                                        │   │
│  │  - 支持长度奖励                                              │   │
│  │  - 过滤低重要性记忆                                          │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │ []Memory (带评分)                     │
│                             ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              4. ConflictResolver                            │   │
│  │  - 检测相似记忆冲突                                          │   │
│  │  - 冲突解决策略: replace/keep both/merge                     │   │
│  │  - 基于向量相似度检测                                        │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │ []Memory (去重)                       │
│                             ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              5. Top-N Filter & Cap                          │   │
│  │  - 限制每次蒸馏的记忆数量                                    │   │
│  │  - 强制执行解决方案上限                                      │   │
│  └──────────────────────────┬──────────────────────────────────┘   │
│                             │                                      │
│                             ▼                                      │
│  输出: []Memory (已蒸馏、分类、评分的记忆)                          │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 4.3 核心组件

#### ExperienceExtractor

**文件位置**: `internal/memory/distillation/extractor.go`

**功能**: 从对话中提取问题-解决方案对

**关键代码**:
```go
type ExperienceExtractor struct {
    questionDetector *QuestionDetector
    noiseFilter      *NoiseFilter
    enableCrossTurn  bool
}

// 提取直接对话对
func (e *ExperienceExtractor) extractDirectExperience(user, assistant Message) *Experience {
    problem := strings.TrimSpace(user.Content)
    solution := strings.TrimSpace(assistant.Content)
    
    // 提取核心解决方案
    solution = e.extractCoreSolution(solution)
    
    return &Experience{
        Problem:    problem,
        Solution:   solution,
        Confidence: e.calculateConfidence(problem, solution),
        ExtractionMethod: ExtractionDirect,
    }
}

// 提取跨轮对话对 (用户 -> 助手(澄清) -> 用户(补充) -> 助手(答案))
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

**文件位置**: `internal/memory/distillation/classifier.go`

**功能**: 将提取的经验分类为四种记忆类型

**记忆类型**:
- **fact**: 事实性信息
- **preference**: 用户偏好
- **solution**: 解决方案/方法
- **rule**: 规则/模式

**分类逻辑**:
```go
func (c *MemoryClassifier) ClassifyMemory(exp *Experience) MemoryType {
    solution := strings.ToLower(exp.Solution)
    
    // 检测解决方案类型
    if c.isSolutionPattern(solution) {
        return MemorySolution
    }
    
    // 检测偏好类型
    if c.isPreferencePattern(exp.Problem, solution) {
        return MemoryPreference
    }
    
    // 检测规则类型
    if c.isRulePattern(solution) {
        return MemoryRule
    }
    
    // 默认为事实类型
    return MemoryFact
}
```

#### ImportanceScorer

**文件位置**: `internal/memory/distillation/scorer.go`

**功能**: 计算记忆的重要性评分

**评分因素**:
- 解决方案长度 (长度奖励)
- 动作动词存在性
- 具体性指标

**评分代码**:
```go
func (s *ImportanceScorer) ScoreMemory(memoryType MemoryType, problem, solution string) float64 {
    score := 0.5
    
    // 基础分数基于记忆类型
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
    
    // 长度奖励
    if s.enableLengthBonus && len(solution) > s.lengthThreshold {
        score += s.lengthBonus
    }
    
    // 动作动词奖励
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

**文件位置**: `internal/memory/distillation/resolver.go`

**功能**: 检测和解决记忆冲突

**冲突检测**:
```go
// DetectConflict 检测与现有记忆的冲突
// 参数:
//   - ctx: 操作上下文
//   - vector: 用于搜索相似记忆的嵌入向量
//   - tenantID: 租户ID，用于多租户隔离
// 返回:
//   - *Experience: 冲突的记忆，如果没有冲突则为nil
//   - error: 遇到的错误
func (r *ConflictResolver) DetectConflict(ctx context.Context, vector []float64, tenantID string) (*Experience, error) {
    if r.repo == nil {
        return nil, nil // 未配置仓库
    }

    if len(vector) == 0 {
        return nil, nil // 未提供向量
    }

    // 向量检索相似记忆
    similar, err := r.repo.SearchByVector(ctx, vector, tenantID, r.searchLimit)
    if err != nil {
        return nil, errors.Wrap(err, "failed to search for similar memories")
    }

    if len(similar) == 0 {
        return nil, nil
    }

    // 检查是否有超过冲突阈值的相似记忆
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

**冲突解决策略**:
```go
type ResolutionStrategy string

const (
    ReplaceOld ResolutionStrategy = "replace" // 用新记忆替换旧记忆
    KeepBoth   ResolutionStrategy = "version" // 保留两个版本
    Merge      ResolutionStrategy = "merge"   // 合并记忆 (未来实现)
)

func (r *ConflictResolver) ResolveConflict(newExp, oldExp *Experience) ResolutionStrategy {
    // 基于置信度选择策略
    if newExp.Confidence > oldExp.Confidence + 0.1 {
        return ReplaceOld
    }
    return KeepBoth
}
```

### 4.4 使用示例

#### 创建蒸馏引擎

```go
import (
    "goagent/internal/memory/distillation"
    "goagent/internal/storage/postgres/embedding"
)

// 1. 创建 Embedding 服务
embedder := embedding.NewEmbeddingClient(config.EmbeddingServiceURL, config.EmbeddingModel)

// 2. 创建 Experience Repository
repo := repositories.NewExperienceRepository(pool)

// 3. 创建蒸馏配置
distillConfig := distillation.DefaultDistillationConfig()
distillConfig.MinImportance = 0.6
distillConfig.MaxMemoriesPerDistillation = 3

// 4. 创建蒸馏引擎
distiller := distillation.NewDistiller(distillConfig, embedder, repo)
```

#### 执行蒸馏

```go
// 准备对话历史
messages := []distillation.Message{
    {Role: "user", Content: "如何安装 Go 语言？"},
    {Role: "assistant", Content: "可以通过以下步骤安装 Go：1. 访问 go.dev/dl..."},
    {Role: "user", Content: "Mac 上怎么安装？"},
    {Role: "assistant", Content: "在 Mac 上可以使用 Homebrew 安装：brew install go"},
}

// 执行蒸馏
memories, err := distiller.DistillConversation(
    ctx,
    "conversation_123",
    messages,
    "tenant_abc",
    "user_456",
)

// 处理蒸馏结果
for _, mem := range memories {
    fmt.Printf("类型: %s, 重要性: %.2f\n", mem.Type, mem.Importance)
    fmt.Printf("内容: %s\n", mem.Content)
    
    // 存储到数据库
    // repo.Create(ctx, mem)
}
```

### 4.5 配置参数

```go
type DistillationConfig struct {
    // 重要性过滤
    MinImportance              float64  // 最小重要性分数 (默认: 0.6)
    
    // 冲突检测
    ConflictThreshold          float64  // 冲突检测阈值 (默认: 0.85)
    ConflictSearchLimit        int      // 冲突搜索限制 (默认: 5)
    
    // 数量限制
    MaxMemoriesPerDistillation int      // 每次蒸馏最大记忆数 (默认: 3)
    MaxSolutionsPerTenant      int      // 每租户最大解决方案数 (默认: 5000)
    
    // 噪音过滤
    EnableCodeFilter           bool     // 启用代码块过滤
    EnableStacktraceFilter     bool     // 启用堆栈跟踪过滤
    EnableLogFilter            bool     // 启用日志过滤
    EnableMarkdownTableFilter  bool     // 启用 Markdown 表格过滤
    
    // 跨轮提取
    EnableCrossTurnExtraction  bool     // 启用跨轮对话提取
    
    // 长度奖励
    EnableLengthBonus          bool     // 启用长度奖励
    LengthThreshold            int      // 长度阈值 (默认: 60)
    LengthBonus                float64  // 长度奖励值 (默认: 0.1)
    
    // 性能优化
    TopNBeforeConflict         bool     // 冲突检测前先 Top-N 过滤
    PrecisionOverRecall        bool     // 优先精确度而非召回率
}
```

## 5. 相似任务检索

### 5.1 哈希向量检索 (旧版)

```go
// internal/memory/manager_impl.go:319
func (m *memoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
    // 1. 为查询生成哈希向量
    queryVector := m.generateHashVector(query)
    
    // 2. 计算余弦相似度
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
    
    // 3. 按得分排序
    sort.Slice(results, func(i, j int) bool {
        return results[i].Score > results[j].Score
    })
    
    // 4. 应用限制
    if len(results) > limit {
        results = results[:limit]
    }
    
    return results, nil
}
```

### 5.2 向量检索 (新版)

```go
// internal/memory/manager_impl.go:419
func (m *memoryManager) searchSimilarTasksNew(ctx context.Context, query string, limit int) ([]*models.Task, error) {
    // 1. 生成查询向量
    queryVector, err := m.embedder.EmbedWithPrefix(ctx, query, "query:")
    if err != nil {
        return nil, err
    }
    
    // 2. 向量检索
    experiences, err := m.expRepo.SearchByVector(ctx, queryVector, "default", limit)
    if err != nil {
        return nil, err
    }
    
    // 3. 转换为任务对象
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

## 6. 配置参数总结

| 参数 | 默认值 | 说明 |
|------|--------|------|
| session_ttl | 24h | 会话过期时间 |
| task_ttl | 1h | 任务过期时间 |
| max_sessions | 1000 | 最大会话数 |
| max_tasks | 10000 | 最大任务数 |
| max_history | 100 | 历史消息数量限制 |
| vector_dimension | 1024 | 向量维度 |
| distillation_threshold | 3 | 触发蒸馏的对话轮数 |
| enable_distillation | true | 启用记忆蒸馏 |

## 7. API 抽象

详见 `api/core/memory.go`，定义了 MemoryRepository 和 MemoryService 接口，支持：
- 会话管理
- 消息管理
- 任务蒸馏
- 相似任务检索
