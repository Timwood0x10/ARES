# 存储系统设计文档

## 1. 概述

存储模块是基于 PostgreSQL 16 + pgvector 构建的生产级 AI Memory & Retrieval 系统，为 Agent Framework 提供多租户支持、异步嵌入流程和企业级安全功能。

### 架构级别
- **L3 - Infra Agent (生产就绪)**
- **核心功能**: 可插拔 VectorStore 后端、多租户隔离、向量搜索、混合检索、密钥管理

### 核心设计原则
1. **可插拔 VectorStore**: 单一接口 (`storage.VectorStore`) 抽象所有向量后端 -- 无需修改业务逻辑即可切换 PostgreSQL、Qdrant、Milvus 或内存实现
2. **向量维度隔离**: 按维度分表（避免混合向量空间）
3. **去重**: 基于 hash 的实时去重 + 异步嵌入去重
4. **优雅降级**: 所有关键路径都有完整的降级机制
5. **多租户**: RLS（行级安全）+ Tenant Guard 双层保护

## 2. 可插拔 VectorStore 接口

向量存储层由 `internal/storage/vector.go` 中的单一接口定义。每个向量后端 -- PostgreSQL + pgvector、Qdrant、Milvus、SQLite-vec 或内存实现 -- 都实现此契约。

### 2.1 接口定义

```go
// internal/storage/vector.go

// VectorStore defines the interface for vector similarity search.
// Implement this interface to add a new vector backend.
type VectorStore interface {
    // Search performs vector similarity search and returns results ordered by distance.
    Search(ctx context.Context, table string, embedding []float64, limit int) ([]*SearchResult, error)

    // AddEmbedding stores a vector with associated metadata.
    AddEmbedding(ctx context.Context, table, id string, embedding []float64, metadata map[string]any) error

    // CreateCollection creates a vector collection/table if it doesn't exist.
    CreateCollection(ctx context.Context, name string, dimension int) error
}

// SearchResult represents a single vector search result.
type SearchResult struct {
    ID       string         `json:"id"`
    Score    float64        `json:"score"`
    Metadata map[string]any `json:"metadata,omitempty"`
}
```

### 2.2 内置实现

| 后端 | 包路径 | 适用场景 |
|------|--------|---------|
| PostgreSQL + pgvector | `internal/storage/postgres` | 生产环境，完整 SQL 支持，IVFFlat 索引 |
| 纯内存 | `internal/storage/memory` | 开发、测试、原型验证 |

**PostgreSQL 实现** (`VectorSearcher`): 使用 pgvector 的 `<=>` 余弦距离运算符和 IVFFlat 索引。验证表名以防止 SQL 注入，强制可配置的搜索限制，创建 `VECTOR(N)` 列和 JSONB 元数据。

**内存实现**: 线程安全（使用 `sync.RWMutex`），暴力余弦相似度搜索，零外部依赖。适合单元测试和本地开发。

### 2.3 Repository 集成

`Repository` 结构体持有类型为 `storage.VectorStore` 的 `Vector` 字段，使后端可在构造时或运行时替换：

```go
// internal/storage/postgres/repository.go
type Repository struct {
    Session   *SessionRepository
    Recommend *RecommendRepository
    Profile   *ProfileRepository
    Vector    storage.VectorStore  // <-- 可插拔
    // ...
}
```

两行代码替换向量后端：

```go
// 生产环境：PostgreSQL + pgvector（默认）
repo := postgres.NewRepository(pool)

// 开发/测试：内存实现
repo.Vector = memory.NewVectorStore()

// 自定义后端：任何实现 storage.VectorStore 的类型
repo.Vector = myqdrant.New("localhost", 6333)
```

### 2.4 添加自定义后端

参见[自定义向量存储指南](../../zh/development/custom-vector-store.md)，包含 Qdrant、Milvus、Elasticsearch 和 SQLite-vec 的完整示例。

## 3. 架构组件

### 3.1 数据库架构

#### 核心表

| 表名 | 用途 | 向量维度 |
|------|------|----------|
| `knowledge_chunks_1024` | RAG 知识库 | 1024D |
| `experiences_1024` | Agent 经验 | 1024D |
| `tools` | 工具语义搜索 | 可选 |
| `conversations` | 对话历史 | 无 |
| `task_results_1024` | 任务执行结果 | 1024D |
| `secrets` | 加密敏感数据 | 无 |
| `models_config` | 模型版本跟踪 | 无 |

#### 关键特性
- **多租户**: 所有表都包含 `tenant_id` 字段
- **向量索引**: IVFFlat 索引用于向量相似度搜索
- **全文搜索**: TSV 索引，使用预计算的 tsvector 列
- **Hash 去重**: `content_hash` 上的 UNIQUE 索引用于实时去重
- **行级安全**: RLS 策略用于租户隔离
- **异步嵌入**: 基于队列的嵌入流程，带重试机制

### 3.2 系统架构

```
┌─────────────────────────────────────────────────────────┐
│                    应用层                                │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │ MemoryManager│  │  Agent 逻辑  │  │  API 层      │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         │         服务层                      │         │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────▼───────┐ │
│  │RetrievalService│ │MemoryPolicy │ │TenantGuard   │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         │        仓储层                      │         │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────▼───────┐ │
│  │KnowledgeRepo │ │ExperienceRepo│ │SecretRepo    │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         │         适配层                      │         │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────▼───────┐ │
│  │SecretAdapter │ │EmbeddingCache│ │QueryCache    │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         │         数据层                      │         │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────▼───────┐ │
│  │PostgreSQL 16 │ │pgvector 0.5.0 │ │EmbeddingService││
│  └──────────────┘  └──────────────┘  └──────────────┘ │
└─────────────────────────────────────────────────────────┘
```

## 4. 核心组件

### 4.1 仓储层

#### KnowledgeRepository
```go
type KnowledgeRepository interface {
    Create(ctx context.Context, chunk *KnowledgeChunk) error
    CreateBatch(ctx context.Context, chunks []*KnowledgeChunk) error
    Search(ctx context.Context, req *SearchRequest) ([]*SearchResult, error)
    GetByID(ctx context.Context, id string) (*KnowledgeChunk, error)
    Update(ctx context.Context, chunk *KnowledgeChunk) error
    Delete(ctx context.Context, id string) error
}
```

#### ExperienceRepository
```go
type ExperienceRepository interface {
    Create(ctx context.Context, exp *Experience) error
    Search(ctx context.Context, req *SearchRequest) ([]*SearchResult, error)
    GetByID(ctx context.Context, id string) (*Experience, error)
    ListByType(ctx context.Context, expType string, limit int) ([]*Experience, error)
    UpdateScore(ctx context.Context, id string, score float64) error
}
```

#### SecretRepository
```go
type SecretRepository interface {
    Set(ctx context.Context, key, value, tenantID string) error
    Get(ctx context.Context, key, tenantID string) (string, error)
    Delete(ctx context.Context, key, tenantID string) error
    Import(ctx context.Context, tenantID string, data []byte, format string) (int64, error)
    Export(ctx context.Context, tenantID string) ([]byte, error)
    RotateKey(ctx context.Context, newKey []byte) (int64, error)
}
```

### 4.2 服务层

#### RetrievalService
实现混合检索流程，支持多种策略：

**核心功能：**
- 跨多源的并行向量搜索（知识、经验、工具）
- 向量搜索失败时的 BM25 降级
- 查询重写（可选，基于 LLM）
- 时间衰减评分
- 结果融合，使用 RRF（倒数排名融合）

**性能目标：**
- 平均延迟：200-500ms
- 向量搜索：2s 超时
- 并发限制：3 个并行搜索

#### MemoryPolicy
实现智能数据过滤：

**功能：**
- ShouldStore：判断数据是否值得存储
- GetTTL：获取数据生存时间
- ShouldDecay：判断数据是否应该衰减

**策略示例：**
- 低分失败经验（< 0.7）被丢弃
- 系统角色的对话不存储
- 经验的 TTL：成功经验 30 天，失败经验 7 天

### 4.3 适配层

#### SecretAdapter
密钥导入/导出的格式转换（JSON、YAML、CSV）。基于内容分析自动检测输入格式。

#### EmbeddingCache
嵌入的多级缓存：本地 LRU（内存）-> Redis（分布式）-> 嵌入服务（远程）。缓存键使用 Unicode 归一化（NFKC）、大小写折叠和空白字符归一化。

## 5. 异步嵌入流程

### 5.1 流程设计

```
写入数据（无嵌入）
    ↓
标记 embedding_status = 'pending'
    ↓
写入 embedding_queue 表
    ↓
Embedding Worker 轮询任务
    ↓
计算嵌入
    ↓
更新数据（status='completed', embedding 值）
    ↓
失败时重试（最多 3 次，指数退避）
    ↓
最终失败 → status='failed' + 死信队列
```

### 5.2 队列表结构

```sql
CREATE TABLE embedding_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id TEXT NOT NULL,
    table_name TEXT NOT NULL,
    content TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    embedding_model TEXT DEFAULT 'e5-large',
    embedding_version INT DEFAULT 1,
    dedupe_key TEXT UNIQUE,  -- 幂等性保证
    retry_count INT DEFAULT 0,
    status TEXT DEFAULT 'pending',
    queued_at TIMESTAMP DEFAULT NOW(),
    processing_at TIMESTAMP,
    completed_at TIMESTAMP,
    error_message TEXT
);
```

### 5.3 并发控制

**FOR UPDATE SKIP LOCKED：**
```sql
SELECT id, task_id, table_name, content, tenant_id,
       embedding_model, embedding_version, retry_count
FROM embedding_queue
WHERE status = 'pending'
ORDER BY queued_at ASC
FOR UPDATE SKIP LOCKED
LIMIT $1
```

### 5.4 Reconciler（巡检器）

**目的：** 查找并重新入队缺失的嵌入任务。

```sql
SELECT id, tenant_id, content, embedding_model, embedding_version
FROM knowledge_chunks_1024
WHERE embedding_status = 'pending'
  AND embedding_queued_at < NOW() - $1
  AND embedding_processed_at IS NULL
LIMIT 1000
```

## 6. 安全特性

### 6.1 多租户隔离

**双层保护：**
1. **RLS（行级安全）**：数据库级别的逻辑隔离
2. **Tenant Guard**：应用级别的物理隔离

```go
// Tenant Guard
func (g *TenantGuard) SetTenantContext(ctx context.Context, tenantID string) error {
    _, err := g.db.ExecContext(ctx, "SET app.tenant_id = $1", tenantID)
    return err
}

// RLS 策略
CREATE POLICY tenant_isolation ON knowledge_chunks_1024
FOR ALL USING (tenant_id = current_setting('app.tenant_id')::TEXT);
```

### 6.2 密钥管理

- **加密**: AES-256-GCM，支持密钥轮换和每个密钥的版本控制
- **导入/导出**: 多格式支持（JSON/YAML/CSV），仅导出元数据
- **密钥轮换**: 基于事务的原子重新加密

### 6.3 输入验证

- SQL 注入防护（参数化查询）
- XSS 防护（输出编码）
- 输入长度限制

## 7. 性能与弹性

- **写入缓冲区**: 批处理将 DB QPS 降低约 80%，嵌入调用降低约 50%
- **查询缓存**: 使用 tenant + query + filters 的 SHA-256 哈希作为缓存键，延迟降低约 70%
- **时间衰减**: `final_score = base_score * time_decay` 防止旧数据主导结果
- **熔断器**: 5 次连续失败 -> 打开（2s 超时），10s 后半开
- **限流**: 令牌桶 + 滑动窗口 + 信号量
- **超时保护**: DB 2s，嵌入 5s，向量搜索 2s，整体请求 10s

## 8. 监控

- **日志**: `slog` 结构化日志，包含 `tenant_id` 和 `trace_id`
- **关键指标**: 嵌入队列长度、缓存命中率、检索延迟、错误率
- **追踪**: `RetrievalTrace` 结构体记录查询重写、结果数、执行时间

## 9. 配置

```yaml
database:
  host: localhost
  port: 5432
  max_open_conns: 25
  max_idle_conns: 10
embedding:
  model: intfloat/e5-large
  dimension: 1024
  timeout: 5s
  cache_ttl: 24h
retrieval:
  vector_search_timeout: 2s
  max_results: 20
  enable_hybrid_search: true
```

## 10. 部署

**前置条件**: PostgreSQL 16+ 安装 pgvector 扩展（0.5.0+），Python 嵌入服务，Redis（可选）。

```bash
go run cmd/migrate/main.go   # 数据库迁移
go run cmd/server/main.go     # 启动应用
```

**健康检查**: `/health`、`/health/db`、`/health/embedding`

## 11. 故障排除

**高嵌入队列长度**: 嵌入服务缓慢或宕机 -- 检查健康状态，扩展 Worker。

**检索质量差**: 向量维度不正确或嵌入过时 -- 验证模型，必要时重新嵌入。

**跨租户数据访问**: Tenant Guard 配置不正确 -- 验证所有操作中都设置了租户上下文。

**数据库调优**: 增加向量操作的 `work_mem`，调整 `effective_cache_size`，使用连接池。
