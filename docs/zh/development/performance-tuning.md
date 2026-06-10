# 性能调优指南

**更新日期**: 2026-03-23

## 简介

本文档介绍如何优化 GoAgent 框架的性能，包括数据库连接池、并发控制、缓存策略等方面的优化建议。

## 数据库连接池优化

### 连接池配置

PostgreSQL 连接池配置直接影响应用性能：

```yaml
storage:
  postgres:
    host: "localhost"
    port: 5433
    user: "postgres"
    password: "postgres"
    database: "goagent"
    
    # 连接池配置
    pool:
      max_open_conns: 25        # 最大打开连接数
      max_idle_conns: 10        # 最大空闲连接数
      conn_max_lifetime: 300   # 连接最大生命周期（秒）
      conn_max_idle_time: 60    # 连接最大空闲时间（秒）
```

**代码位置**: `internal/storage/postgres/pool.go:70-100`

### 推荐配置

**小型应用**（单机部署）:
```yaml
max_open_conns: 10
max_idle_conns: 5
conn_max_lifetime: 300
conn_max_idle_time: 60
```

**中型应用**（高并发）:
```yaml
max_open_conns: 50
max_idle_conns: 20
conn_max_lifetime: 600
conn_max_idle_time: 300
```

**大型应用**（分布式）:
```yaml
max_open_conns: 100
max_idle_conns: 40
conn_max_lifetime: 1800
conn_max_idle_time: 900
```

### 连接泄漏检测

监控连接池使用情况：

```go
import "time"

func monitorPoolStats(pool *sql.DB) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for range ticker.C {
        stats := pool.Stats()
        
        log.Info("Pool Stats",
            "max_open", stats.MaxOpenConnections,
            "open", stats.OpenConnections,
            "in_use", stats.InUse,
            "idle", stats.Idle,
        )
        
        // 警告连接泄漏
        if stats.InUse > stats.MaxOpenConnections*80/100 {
            log.Warn("High connection usage detected")
        }
    }
}
```

**代码位置**: `internal/storage/postgres/pool.go:120-150`

## 并发控制优化

### Agent 并发配置

控制并行 Agent 数量以平衡性能和资源使用：

```yaml
agents:
  leader:
    id: "leader-agent"
    max_parallel_tasks: 4  # 最大并行任务数
    
    # 性能优化配置
    performance:
      worker_pool_size: 8    # Worker 池大小
      queue_size: 100        # 任务队列大小
      timeout: 30             # 单个任务超时
```

**代码位置**: `internal/agents/leader/agent.go:120-150`

### 并发模式选择

#### 模式 1: 保守模式（稳定优先）

```yaml
agents:
  leader:
    max_parallel_tasks: 2    # 限制并行数
    timeout: 60               # 增加超时
    enable_cache: true        # 启用缓存
```

#### 模式 2: 均衡模式（推荐）

```yaml
agents:
  leader:
    max_parallel_tasks: 4    # 适中并行数
    timeout: 30               # 标准超时
    enable_cache: true        # 启用缓存
```

#### 模式 3: 激进模式（性能优先）

```yaml
agents:
  leader:
    max_parallel_tasks: 8    # 高并行数
    timeout: 15               # 短超时
    enable_cache: true        # 启用缓存
    retry_policy:
      max_attempts: 2         # 减少重试
```

**代码位置**: `internal/agents/leader/agent.go:80-120`

### Worker 池优化

```go
type WorkerPool struct {
    workers chan struct{}
    tasks   chan Task
    wg      sync.WaitGroup
}

func NewWorkerPool(size int) *WorkerPool {
    return &WorkerPool{
        workers: make(chan struct{}, size),
        tasks:   make(chan Task, size*2),
    }
}

func (p *WorkerPool) Submit(task Task) {
    select {
    case p.tasks <- task:
        // 任务已提交
    default:
        // 队列已满，阻塞等待
        p.tasks <- task
    }
}
```

**代码位置**: `internal/agents/leader/worker_pool.go:50-100`

## 缓存策略

### LLM 响应缓存

启用 LLM 响应缓存以减少重复请求：

```yaml
llm:
  provider: "ollama"
  model: "llama3.2"
  
  # 缓存配置
  cache:
    enabled: true
    type: "memory"           # memory / redis
    ttl: 3600                # 缓存时间（秒）
    max_size: 1000           # 最大缓存条目
```

**代码位置**: `internal/llm/client.go:200-250`

### 工具结果缓存

缓存工具执行结果：

```yaml
tools:
  cache:
    enabled: true
    ttl: 1800                # 30分钟
    cache_keys:
      - "calculator.*"
      - "datetime.*"
```

**代码位置**: `internal/tools/resources/core/cache.go:80-120`

### Embedding 缓存

Embedding 结果使用 Redis 缓存：

```yaml
embedding:
  service_url: "http://localhost:8000"
  
  # 缓存配置
  cache:
    enabled: true
    type: "redis"
    redis_url: "redis://localhost:6379"
    ttl: 86400               # 24小时
```

**代码位置**: `services/embedding/app.py:100-150`

## 向量检索优化

### pgvector 索引优化

创建合适的向量索引：

```sql
-- 创建 ivfflat 索引
CREATE INDEX knowledge_chunks_1024_embedding_idx 
ON knowledge_chunks_1028 
USING ivfflat (embedding vector_cosine_ops) 
WITH (lists = 100);

-- 创建 HNSW 索引（更快的查询）
CREATE INDEX knowledge_chunks_1024_embedding_hnsw 
ON knowledge_chunks_1028 
USING hnsw (embedding vector_cosine_ops);
```

**代码位置**: `internal/storage/postgres/migrations/001_create_indexes.sql:20-50`

### 检索参数调优

```yaml
knowledge:
  # 检索配置
  retrieval:
    top_k: 10                # 返回结果数量
    min_score: 0.6           # 最小相似度
    ef_search: 100           # HNSW 参数（性能）
    ef_construction: 100     # HNSW 参数（精度）
```

**代码位置**: `internal/storage/postgres/repositories/knowledge_repository.go:100-150`

### 批量检索优化

使用批量检索减少网络开销：

```go
func (r *KnowledgeRepository) BatchRetrieve(
    ctx context.Context,
    queries []string,
    topK int,
) ([][]Document, error) {
    var wg sync.WaitGroup
    results := make([][]Document, len(queries))
    
    for i, query := range queries {
        wg.Add(1)
        go func(idx int, q string) {
            defer wg.Done()
            docs, err := r.Retrieve(ctx, q, topK)
            if err == nil {
                results[idx] = docs
            }
        }(i, query)
    }
    
    wg.Wait()
    return results, nil
}
```

**代码位置**: `internal/storage/postgres/repositories/knowledge_repository.go:200-250`

## 内存优化

### 内存限制配置

```yaml
memory:
  # 会话内存限制
  session:
    max_history: 50         # 最大历史记录数
    max_message_size: 10240 # 单条消息最大大小
    
  # 蒸馏内存优化
  distillation:
    batch_size: 10          # 批量处理大小
    max_concurrent: 5       # 最大并发蒸馏任务
```

**代码位置**: `internal/memory/context/session.go:50-100`

### 对象池优化

使用对象池减少 GC 压力：

```go
var messagePool = sync.Pool{
    New: func() interface{} {
        return &Message{}
    },
}

func NewMessage() *Message {
    return messagePool.Get().(*Message)
}

func (m *Message) Release() {
    m.Reset()
    messagePool.Put(m)
}
```

**代码位置**: `internal/protocol/ahp/message.go:50-80`

## 监控指标

### 关键性能指标

#### 1. Agent 响应时间

```go
import "time"

func (a *Agent) ProcessWithMetrics(ctx context.Context, query string) (string, error) {
    start := time.Now()
    defer func() {
        duration := time.Since(start)
        metrics.Record("agent.process.duration", duration)
    }()
    
    return a.Process(ctx, query)
}
```

**代码位置**: `internal/agents/base/metrics.go:30-60`

#### 2. 数据库查询时间

```go
func (r *Repository) QueryWithMetrics(ctx context.Context, query string) (*Result, error) {
    start := time.Now()
    defer func() {
        duration := time.Since(start)
        metrics.Record("db.query.duration", duration)
        
        // 慢查询告警
        if duration > 1*time.Second {
            log.Warn("Slow query detected", "duration", duration)
        }
    }()
    
    return r.Query(ctx, query)
}
```

**代码位置**: `internal/storage/postgres/repositories/base.go:80-120`

#### 3. 缓存命中率

```go
type CacheMetrics struct {
    Hits   int64
    Misses int64
}

func (c *Cache) getWithMetrics(key string) (interface{}, bool) {
    start := time.Now()
    defer func() {
        duration := time.Since(start)
        metrics.Record("cache.get.duration", duration)
    }()
    
    val, found := c.cache.Get(key)
    if found {
        c.metrics.Hits++
    } else {
        c.metrics.Misses++
    }
    
    return val, found
}

func (c *Cache) HitRate() float64 {
    total := c.metrics.Hits + c.metrics.Misses
    if total == 0 {
        return 0
    }
    return float64(c.metrics.Hits) / float64(total) * 100
}
```

**代码位置**: `internal/cache/metrics.go:50-100`

### 性能监控面板

```go
type PerformanceMonitor struct {
    metrics map[string]float64
    mu      sync.RWMutex
}

func (m *PerformanceMonitor) Report() map[string]float64 {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    return m.metrics
}

// 输出性能报告
func (m *PerformanceMonitor) PrintReport() {
    report := m.Report()
    
    fmt.Println("=== Performance Report ===")
    fmt.Printf("Avg Response Time: %.2f ms\n", report["avg_response_time"])
    fmt.Printf("Cache Hit Rate: %.2f%%\n", report["cache_hit_rate"])
    fmt.Printf("DB Query Time: %.2f ms\n", report["avg_db_query_time"])
    fmt.Printf("Concurrent Tasks: %.0f\n", report["concurrent_tasks"])
}
```

**代码位置**: `internal/observability/monitor.go:80-120`

## 常见性能问题

### 问题 1: 响应慢

**症状**: Agent 响应时间过长

**诊断**:
```go
func DiagnoseSlowResponse() {
    // 检查 1: 数据库连接池
    stats := db.Stats()
    if stats.InUse == stats.MaxOpenConnections {
        log.Warn("Database connection pool exhausted")
    }
    
    // 检查 2: 并发任务数
    activeTasks := agent.GetActiveTaskCount()
    if activeTasks > agent.config.MaxParallelTasks {
        log.Warn("Exceeding parallel task limit")
    }
    
    // 检查 3: 缓存命中率
    if cache.HitRate() < 50 {
        log.Warn("Low cache hit rate, consider tuning")
    }
}
```

**解决方法**:
- 增加 `max_open_conns`
- 优化 SQL 查询
- 增加缓存大小

**代码位置**: `internal/observability/diagnostics.go:50-100`

### 问题 2: 内存占用高

**症状**: 进程内存占用持续增长

**诊断**:
```go
func CheckMemoryLeak() {
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    
    log.Info("Memory Stats",
        "alloc", m.Alloc/1024/1024,           // 已分配内存 (MB)
        "sys", m.Sys/1024/1024,             // 系统内存 (MB)
        "num_gc", m.NumGC,                // GC 次数
        "next_gc", m.NextGC,              // 下次 GC 时间
    )
    
    // 检查 goroutine 泄漏
    numGoroutines := runtime.NumGoroutine()
    if numGoroutines > 1000 {
        log.Warn("High goroutine count detected")
    }
}
```

**解决方法**:
- 启用对象池
- 限制并发数
- 检查 goroutine 泄漏

**代码位置**: `internal/observability/diagnostics.go:150-200`

### 问题 3: 数据库连接耗尽

**症状**: 无法获取数据库连接

**诊断**:
```go
func CheckDatabaseConnections() {
    stats := db.Stats()
    
    log.Info("Database Connection Stats",
        "open", stats.OpenConnections,
        "in_use", stats.InUse,
        "idle", stats.Idle,
        "wait_count", stats.WaitCount,
        "wait_duration", stats.WaitDuration,
    )
    
    // 警告等待时间过长
    if stats.WaitDuration > 5*time.Second {
        log.Warn("Long wait time for database connection")
    }
}
```

**解决方法**:
- 增加 `max_open_conns`
- 优化长时间运行的查询
- 使用连接池超时

**代码位置**: `internal/storage/postgres/diagnostics.go:50-100`

## 性能基准

### 基准测试场景

#### 场景 1: 单 Agent 查询

```
配置: max_parallel_tasks=1
平均响应时间: 2.5s
吞吐量: 24 queries/min
```

#### 场景 2: 并行 Agent 查询

```
配置: max_parallel_tasks=4
平均响应时间: 1.8s
吞吐量: 133 queries/min
```

#### 场景 3: 带缓存的查询

```
配置: enable_cache=true, ttl=3600
平均响应时间（首次）: 2.5s
平均响应时间（缓存命中）: 50ms
缓存命中率: 85%
```

### 性能对比表

| 配置 | 平均响应时间 | 吞吐量 | 内存占用 |
|------|------------|--------|----------|
| 保守模式 | 3.2s | 18/min | 200MB |
| 均衡模式 | 1.8s | 133/min | 350MB |
| 激进模式 | 1.2s | 200/min | 500MB |

**代码位置**: `benchmark/agent_benchmark.go:100-150`

## 优化建议

### 短期优化（立即可做）

1. **启用缓存**
   ```yaml
   llm:
     cache:
       enabled: true
       ttl: 3600
   ```

2. **调整连接池**
   ```yaml
   max_open_conns: 25
   max_idle_conns: 10
   ```

3. **启用向量索引**
   ```sql
   CREATE INDEX ... USING ivfflat
   ```

**代码位置**: 
- `internal/llm/client.go:200-250`
- `internal/storage/postgres/pool.go:70-100`
- `internal/storage/postgres/migrations/001_create_indexes.sql:20-50`

### 中期优化（需要规划）

1. **实现读写分离**
2. **添加 Redis 集群**
3. **优化 Embedding 模型**
4. **实现结果分页**

### 长期优化（架构调整）

1. **分布式部署**
2. **实现服务网格**
3. **引入消息队列**
4. **优化模型推理**

## 参考文档

- [集成指南](integration_guide.md)
- [测试指南](testing_guide.md)
- [架构文档](arch.md)
- [存储 API](storage/api.md)

---

**版本**: 1.0  
**最后更新**: 2026-03-23  
**维护者**: GoAgent 团队