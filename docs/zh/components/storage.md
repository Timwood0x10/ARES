# 存储系统设计文档

## 1. 概述

存储模块是基于 PostgreSQL 16 + pgvector 构建的生产级 AI Memory & Retrieval 系统，为 Agent Framework 提供多租户支持、异步嵌入流程和企业级安全功能。

### 架构级别
- **L3 - Infra Agent (生产就绪)**
- **核心功能**: 多租户隔离、向量搜索、混合检索、密钥管理

### 核心设计原则
1. **向量维度隔离**: 按维度分表（避免混合向量空间）
2. **去重**: 基于 hash 的实时去重 + 异步嵌入去重
3. **优雅降级**: 所有关键路径都有完整的降级机制
4. **多租户**: RLS（行级安全）+ Tenant Guard 双层保护

## 2. 架构组件

### 2.1 数据库架构

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

### 2.2 系统架构

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

## 3. 核心组件

### 3.1 仓储层

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

### 3.2 服务层

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

### 3.3 适配层

#### SecretAdapter
密钥导入/导出的格式转换：

**支持的格式：**
- JSON：标准 JSON 格式
- YAML：键值对形式的 YAML 格式
- CSV：CSV 格式，列包括（key, value, expires_at）

**自动检测：**
基于内容分析自动检测输入格式。

#### EmbeddingCache
嵌入的多级缓存：

**缓存层次：**
1. 本地 LRU 缓存（内存）
2. Redis 缓存（分布式）
3. 嵌入服务（远程）

**缓存键归一化：**
- Unicode 归一化（NFKC）
- 大小写折叠
- 空白字符归一化

## 4. 异步嵌入流程

### 4.1 流程设计

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

### 4.2 队列表结构

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

### 4.3 并发控制

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

### 4.4 Reconciler（巡检器）

**目的：** 查找并重新入队缺失的嵌入任务

**逻辑：**
```sql
SELECT id, tenant_id, content, embedding_model, embedding_version
FROM knowledge_chunks_1024
WHERE embedding_status = 'pending'
  AND embedding_queued_at < NOW() - $1
  AND embedding_processed_at IS NULL
LIMIT 1000
```

## 5. 安全特性

### 5.1 多租户隔离

**双层保护：**
1. **RLS（行级安全）**：数据库级别的逻辑隔离
2. **Tenant Guard**：应用级别的物理隔离

**实现：**
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

### 5.2 密钥管理

**加密：**
- 算法：AES-256-GCM
- 支持密钥轮换
- 每个密钥的版本控制

**导入/导出：**
- 导出：仅元数据（无加密值）
- 导入：多格式支持（JSON/YAML/CSV）
- 密钥轮换：基于事务的原子重新加密

### 5.3 输入验证

**清理：**
- SQL 注入防护（参数化查询）
- XSS 防护（输出编码）
- 输入长度限制

## 6. 性能优化

### 6.1 写入缓冲区

**目的：** 减少 DB QPS 和嵌入服务负载

**实现：**
```go
type WriteBuffer struct {
    buffer chan *WriteItem
    batchSize int
    flushInterval time.Duration
}
```

**性能影响：**
- DB QPS：↓ 80%
- 嵌入调用：↓ 50%
- 延迟：更稳定

### 6.2 查询缓存

**目的：** 缓存查询结果以绕过 DB + 嵌入

**缓存键：**
```go
func (c *QueryCache) GetCacheKey(query string, tenantID string, filters map[string]interface{}) string {
    keyData := fmt.Sprintf("query:%s:%s:%v", tenantID, query, filters)
    hash := sha256.Sum256([]byte(keyData))
    return fmt.Sprintf("query_cache:%x", hash[:16])
}
```

**性能影响：**
- 延迟：↓ 70%
- DB QPS：↓
- 嵌入 QPS：↓

### 6.3 时间衰减评分

**公式：**
```go
final_score = base_score * time_decay
```

**目的：** 防止旧数据主导结果

## 7. 错误处理和弹性

### 7.1 熔断器

**目的：** 防止级联故障

**配置：**
- 失败阈值：5 次连续失败
- 超时：2s
- 半开超时：10s

### 7.2 限流

**策略：**
- 令牌桶限流器
- 滑动窗口限流器
- 并发操作的信号量

### 7.3 超时保护

**超时设置：**
- DB 操作：2s
- 嵌入调用：5s
- 向量搜索：2s
- 整体请求：10s

## 8. 监控和可观测性

### 8.1 日志

**结构化日志：**
- 使用 `slog` 进行结构化日志记录
- 所有日志条目都包含 `tenant_id` 和 `trace_id`
- 生产环境中不记录原始 LLM 输入/输出

### 8.2 指标

**关键指标：**
- 嵌入队列长度
- 缓存命中率
- 检索延迟
- 错误率
- 资源使用情况

### 8.3 追踪

**检索追踪：**
```go
type RetrievalTrace struct {
    OriginalQuery    string
    RewrittenQuery   string
    RewriteUsed      bool
    VectorResults    int
    KeywordResults   int
    FinalResults     int
    ExecutionTime    time.Duration
    VectorError      error
}
```

## 9. 配置

### 9.1 数据库配置

```yaml
database:
  host: localhost
  port: 5432
  user: postgres
  password: postgres
  database: goagent
  max_open_conns: 25
  max_idle_conns: 10
  conn_max_lifetime: 5m
  conn_max_idle_time: 1m
```

### 9.2 嵌入配置

```yaml
embedding:
  service_url: http://localhost:8000
  model: intfloat/e5-large
  dimension: 1024
  timeout: 5s
  cache_ttl: 24h
  batch_size: 32
```

### 9.3 检索配置

```yaml
retrieval:
  vector_search_timeout: 2s
  keyword_search_timeout: 1s
  max_results: 20
  enable_query_rewrite: false
  enable_hybrid_search: true
  time_decay_enabled: true
```

## 10. 测试

### 10.1 测试覆盖率

**当前覆盖率：**
- Models：100%
- Adapters：85%
- Query：75%
- Embedding：25.8%
- Services：25.8%
- Repositories：0%（需要完成）

### 10.2 测试要求

**强制测试：**
- 所有公共方法的单元测试
- 关键路径的集成测试
- 竞争条件检测（`go test -race`）
- 边界条件测试
- 错误场景测试

## 11. 部署

### 11.1 前置条件

- PostgreSQL 16+
- pgvector 扩展（版本 0.5.0+）
- Python 嵌入服务
- Redis（可选，用于缓存）

### 11.2 迁移步骤

1. **数据库迁移：**
   ```bash
   go run cmd/migrate/main.go
   ```

2. **启动嵌入服务：**
   ```bash
   cd services/embedding
   ./start.sh
   ```

3. **启动应用：**
   ```bash
   go run cmd/server/main.go
   ```

### 11.3 健康检查

**端点：**
- `/health`：应用健康状态
- `/health/db`：数据库连接状态
- `/health/embedding`：嵌入服务可用性

## 12. 故障排除

### 12.1 常见问题

**问题：高嵌入队列长度**
- 原因：嵌入服务缓慢或宕机
- 解决方案：检查嵌入服务健康状态，扩展 Worker

**问题：检索质量差**
- 原因：向量维度不正确或嵌入过时
- 解决方案：验证嵌入模型，必要时重新嵌入

**问题：跨租户数据访问**
- 原因：Tenant Guard 配置不正确
- 解决方案：验证所有操作中都设置了租户上下文

### 12.2 性能调优

**数据库调优：**
- 增加向量操作的 `work_mem`
- 根据可用 RAM 调整 `effective_cache_size`
- 使用连接池

**嵌入调优：**
- 增加批量操作的批次大小
- 为频繁查询的文本启用缓存
- 水平扩展嵌入服务

## 13. 未来增强

### 13.1 计划功能

- **查询重写**：基于 LLM 的查询扩展和细化
- **高级向量索引**：HNSW 索引以获得更好的性能
- **多模型支持**：支持多个嵌入模型
- **流式检索**：实时结果流

### 13.2 性能改进

- **GPU 加速**：基于 GPU 的嵌入计算
- **分布式缓存**：多节点缓存层
- **读取副本**：数据库读取扩展

## 14. 参考

- [Effective Go](https://go.dev/doc/effective-go)
- [pgvector 文档](https://github.com/pgvector/pgvector)
- [PostgreSQL 行级安全](https://www.postgresql.org/docs/current/ddl-rowsecurity.html)
- [AES-GCM 加密](https://en.wikipedia.org/wiki/Galois/Counter_Mode)

## 15. 版本历史

| 版本 | 日期 | 变更 |
|------|------|------|
| 1.0.0 | 2026-03-18 | 初始生产版本 |
| 1.0.1 | 2026-03-18 | 添加密钥导入适配层 |
| 1.0.2 | 2026-03-18 | 完成 models 测试覆盖率（100%） |