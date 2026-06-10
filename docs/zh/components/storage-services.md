# Services 模块 API 文档

## 概述

Services 模块提供了业务逻辑层，封装了复杂的数据访问操作和检索逻辑。该模块实现了多源检索、查询重写、时间衰减等高级功能。

## 核心服务

### RetrievalService（检索服务）

智能检索服务，支持跨多个数据源的混合搜索。

#### 主要功能

- **混合搜索**：结合向量搜索和关键词搜索（BM25）
- **多源检索**：支持知识库、经验库、工具、任务结果等多个数据源
- **查询重写**：自动优化查询以提高检索效果
- **时间衰减**：基于时间的评分衰减，优先返回最新内容
- **结果融合**：智能合并和排序来自不同源的结果
- **租户隔离**：所有操作支持多租户隔离

#### 核心数据结构

##### SearchRequest

搜索请求配置：

```go
type SearchRequest struct {
    Query       string          // 搜索查询文本
    TenantID    string          // 租户ID，用于隔离
    TopK        int             // 返回结果数量
    MinScore    float64         // 最小相似度分数
    Plan        *RetrievalPlan  // 检索策略
    EnableTrace bool            // 启用跟踪日志
    Trace       *RetrievalTrace // 跟踪信息
}
```

##### RetrievalPlan

检索策略配置：

```go
type RetrievalPlan struct {
    // 数据源配置
    SearchKnowledge   bool    // 搜索知识库
    SearchExperience  bool    // 搜索经验库
    SearchTools       bool    // 搜索工具
    SearchTaskResults bool    // 搜索任务结果

    // 权重配置
    KnowledgeWeight   float64 // 知识库结果权重（默认 0.4）
    ExperienceWeight  float64 // 经验库结果权重（默认 0.3）
    ToolsWeight       float64 // 工具结果权重（默认 0.2）
    TaskResultsWeight float64 // 任务结果权重（默认 0.1）

    // 功能配置
    EnableQueryRewrite  bool // 启用查询重写
    EnableKeywordSearch bool // 启用关键词/BM25搜索
    EnableTimeDecay     bool // 启用基于时间的评分衰减

    TopK int // 每个源的最大结果数
}
```

##### SearchResult

搜索结果：

```go
type SearchResult struct {
    ID        string                 // 结果ID
    Content   string                 // 结果内容
    Score     float64                // 相似度分数
    Source    string                 // 来源（knowledge, experience, tool, task_result）
    Type      string                 // 结果类型，用于过滤
    Metadata  map[string]interface{} // 附加元数据
    CreatedAt time.Time              // 创建时间
}
```

##### RetrievalTrace

检索跟踪信息：

```go
type RetrievalTrace struct {
    OriginalQuery   string        // 原始查询
    RewrittenQuery  string        // 重写后的查询
    RewriteUsed     bool          // 是否使用了查询重写
    VectorResults   int           // 向量搜索结果数
    KeywordResults  int           // 关键词搜索结果数
    FinalResults    int           // 最终结果数
    ExecutionTime   time.Duration // 执行时间
    VectorError     error         // 向量搜索错误
    SearchBreakdown map[string]int // 每个源的结果数量
}
```

#### 主要方法

| 方法 | 描述 |
|------|------|
| `NewRetrievalService(...)` | 创建新的检索服务实例 |
| `Search(ctx, req)` | 执行智能检索 |
| `validateRequest(req)` | 验证搜索请求 |
| `getEmbedding(ctx, query)` | 获取查询的向量嵌入 |
| `shouldRewriteQuery(query)` | 判断是否应该重写查询 |
| `isQueryInCache(query)` | 检查查询是否在缓存中 |
| `queryRewrite(ctx, query)` | 执行查询重写 |
| `parallelVectorSearch(...)` | 并行向量搜索 |
| `searchKnowledgeVector(...)` | 在知识库中执行向量搜索 |
| `searchExperienceVector(...)` | 在经验库中执行向量搜索 |
| `searchToolsVector(...)` | 在工具库中执行向量搜索 |
| `bm25Search(...)` | 执行 BM25 关键词搜索 |
| `bm25SearchKnowledge(...)` | 在知识库中执行 BM25 搜索 |
| `bm25SearchExperience(...)` | 在经验库中执行 BM25 搜索 |
| `bm25SearchTools(...)` | 在工具库中执行 BM25 搜索 |
| `mergeAndRank(...)` | 合并和排序结果 |
| `calculateTimeDecay(createdAt)` | 计算时间衰减因子 |
| `filterByScore(results, minScore)` | 根据分数过滤结果 |
| `countResultsBySource(results)` | 统计每个源的结果数量 |

#### 使用示例

##### 基础搜索

```go
service := services.NewRetrievalService(
    pool,
    embeddingClient,
    tenantGuard,
    retrievalGuard,
    kbRepo,
)

// 创建搜索请求
req := &services.SearchRequest{
    Query:    "如何使用 Go 进行并发编程",
    TenantID: "tenant-1",
    TopK:     10,
    Plan:     services.DefaultRetrievalPlan(),
}

// 执行搜索
results, err := service.Search(ctx, req)
if err != nil {
    // 处理错误
}

// 处理结果
for _, result := range results {
    fmt.Printf("Score: %.2f, Content: %s\n", result.Score, result.Content)
}
```

##### 自定义检索策略

```go
// 创建自定义检索计划
plan := &services.RetrievalPlan{
    SearchKnowledge:   true,
    SearchExperience:  true,
    SearchTools:       true,
    SearchTaskResults: false,

    KnowledgeWeight:   0.5,  // 增加知识库权重
    ExperienceWeight:  0.3,
    ToolsWeight:       0.2,
    TaskResultsWeight: 0.0,

    EnableQueryRewrite:  true,  // 启用查询重写
    EnableKeywordSearch: true,  // 启用关键词搜索
    EnableTimeDecay:     true,  // 启用时间衰减

    TopK: 15,  // 每个源返回15个结果
}

req := &services.SearchRequest{
    Query:    "机器学习最佳实践",
    TenantID: "tenant-1",
    TopK:     20,
    Plan:     plan,
    MinScore: 0.7,  // 最小相似度分数
}

results, err := service.Search(ctx, req)
```

##### 启用跟踪

```go
req := &services.SearchRequest{
    Query:       "API 安全性设计",
    TenantID:    "tenant-1",
    TopK:        10,
    Plan:        services.DefaultRetrievalPlan(),
    EnableTrace: true,  // 启用跟踪
}

results, err := service.Search(ctx, req)

// 访问跟踪信息
if req.Trace != nil {
    fmt.Printf("原始查询: %s\n", req.Trace.OriginalQuery)
    fmt.Printf("重写查询: %s\n", req.Trace.RewrittenQuery)
    fmt.Printf("执行时间: %v\n", req.Trace.ExecutionTime)
    fmt.Printf("结果分布: %v\n", req.Trace.SearchBreakdown)
}
```

#### 默认配置

```go
// 默认检索计划
plan := services.DefaultRetrievalPlan()

// 默认配置：
// - SearchKnowledge: true
// - SearchExperience: true
// - SearchTools: true
// - SearchTaskResults: false
// - KnowledgeWeight: 0.4
// - ExperienceWeight: 0.3
// - ToolsWeight: 0.2
// - TaskResultsWeight: 0.1
// - EnableQueryRewrite: false
// - EnableKeywordSearch: true
// - EnableTimeDecay: true
// - TopK: 10
```

#### 检索算法

##### 1. 混合搜索

RetrievalService 结合了两种搜索方法：

- **向量搜索**：使用嵌入向量进行语义相似度匹配
- **关键词搜索**：使用 BM25 算法进行关键词匹配

结果通过加权平均和重排序来融合。

##### 2. 时间衰减

为了优先返回最新内容，RetrievalService 实现了时间衰减：

```go
// 时间衰减公式
decayFactor = 0.9 ^ (daysOld / 30)

// 最小衰减因子为 0.1
finalScore = baseScore * decayFactor
```

##### 3. 查询重写

查询重写功能可以优化查询以提高检索效果：

- 识别同义词
- 扩展查询
- 纠正拼写错误
- 添加相关术语

#### 错误处理

服务返回以下错误类型：

- `errors.ErrInvalidArgument`：无效的搜索请求
- `errors.ErrEmbeddingFailed`：嵌入生成失败
- `errors.ErrTenantIsolation`：租户隔离验证失败

#### 性能优化

- **并行搜索**：多个数据源的搜索并行执行
- **结果缓存**：嵌入向量缓存以减少计算
- **批量处理**：支持批量检索操作
- **限流保护**：防止过度请求

#### 测试覆盖

当前测试覆盖率：52.4%

已测试的功能：
- 默认检索计划配置
- 时间衰减计算
- 分数过滤
- 结果合并和排序
- 搜索请求验证
- 查询重写判断逻辑
- 结果来源统计
- 辅助函数（字符串处理、日志截断等）- 100% 覆盖
- 检索服务构造器
- 向量搜索（知识库）
- BM25 搜索（知识库）
- 查询重写功能
- 查询缓存检查
- 经验库搜索（返回空结果）- 100% 覆盖
- 工具库搜索（返回空结果）- 100% 覆盖
- 结果融合（包含不同来源和时间衰减）
- 边界情况处理（空结果、未知来源等）
- Unicode 字符处理
- 时间衰减边界情况（未来时间、零时间）
- 结果去重和分数合并
- 权重配置验证
- 查询长度和特殊字符处理
- 大数据集处理（50+ 结果）
- 所有数据源类型测试
- 精确阈值匹配
- 特定时间点的时间衰减
- 各种分数范围（零分、高分、负分）
- 验证边界情况（TopK=1、超大 TopK）
- 特殊字符和数字处理
- 大小写不敏感匹配

## 最佳实践

### 1. 选择合适的检索策略

根据使用场景选择合适的检索计划：

```go
// 学术研究场景：侧重知识库和经验库
plan := &services.RetrievalPlan{
    SearchKnowledge:   true,
    SearchExperience:  true,
    SearchTools:       false,
    SearchTaskResults: false,
    KnowledgeWeight:   0.6,
    ExperienceWeight:  0.4,
}
```

### 2. 合理设置 TopK

```go
// 推荐 TopK 值：
// - 快速响应：5-10
// - 一般搜索：10-20
// - 深度搜索：20-50
```

### 3. 启用跟踪调试

```go
// 开发和调试时启用跟踪
if os.Getenv("ENV") == "development" {
    req.EnableTrace = true
}
```

### 4. 处理错误

```go
results, err := service.Search(ctx, req)
if err != nil {
    if errors.Is(err, errors.ErrInvalidArgument) {
        // 处理无效参数
    } else if errors.Is(err, errors.ErrEmbeddingFailed) {
        // 处理嵌入失败
    } else {
        // 处理其他错误
    }
}
```
