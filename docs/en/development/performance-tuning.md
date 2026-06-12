# Performance Tuning Guide

**Last Updated**: 2026-03-23

## Introduction

This document describes how to optimize the performance of the GoAgent framework, including database connection pooling, concurrency control, caching strategies, and more.

## Database Connection Pool Optimization

### Connection Pool Configuration

PostgreSQL connection pool configuration directly impacts application performance:

```yaml
storage:
  postgres:
    host: "localhost"
    port: 5433
    user: "postgres"
    password: "postgres"
    database: "goagent"
    
    # Connection pool configuration
    pool:
      max_open_conns: 25        # Maximum open connections
      max_idle_conns: 10        # Maximum idle connections
      conn_max_lifetime: 300   # Connection max lifetime (seconds)
      conn_max_idle_time: 60    # Connection max idle time (seconds)
```

**Code Location**: `internal/storage/postgres/pool.go:70-100`

### Recommended Configurations

**Small Application** (Single deployment):
```yaml
max_open_conns: 10
max_idle_conns: 5
conn_max_lifetime: 300
conn_max_idle_time: 60
```

**Medium Application** (High concurrency):
```yaml
max_open_conns: 50
max_idle_conns: 20
conn_max_lifetime: 600
conn_max_idle_time: 300
```

**Large Application** (Distributed):
```yaml
max_open_conns: 100
max_idle_conns: 40
conn_max_lifetime: 1800
conn_max_idle_time: 900
```

### Connection Leak Detection

Monitor connection pool usage:

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
        
        // Warn about connection leaks
        if stats.InUse > stats.MaxOpenConnections*80/100 {
            log.Warn("High connection usage detected")
        }
    }
}
```

**Code Location**: `internal/storage/postgres/pool.go:120-150`

## Concurrency Control Optimization

### Agent Parallel Configuration

Control the number of parallel Agents to balance performance and resource usage:

```yaml
agents:
  leader:
    id: "leader-agent"
    max_parallel_tasks: 4  # Maximum parallel tasks
    
    # Performance optimization configuration
    performance:
      worker_pool_size: 8    # Worker pool size
      queue_size: 100        # Task queue size
      timeout: 30             # Single task timeout
```

**Code Location**: `internal/agents/leader/agent.go:120-150`

### Concurrency Mode Selection

#### Mode 1: Conservative Mode (Stability First)

```yaml
agents:
  leader:
    max_parallel_tasks: 2    # Limit parallelism
    timeout: 60               # Increase timeout
    enable_cache: true        # Enable caching
```

#### Mode 2: Balanced Mode (Recommended)

```yaml
agents:
  leader:
    max_parallel_tasks: 4    # Moderate parallelism
    timeout: 30               # Standard timeout
    enable_cache: true        # Enable caching
```

#### Mode 3: Aggressive Mode (Performance First)

```yaml
agents:
  leader:
    max_parallel_tasks: 8    # High parallelism
    timeout: 15               # Short timeout
    enable_cache: true        # Enable caching
    retry_policy:
      max_attempts: 2         # Reduce retries
```

**Code Location**: `internal/agents/leader/agent.go:80-120`

### Worker Pool Optimization

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
        // Task submitted
    default:
        // Queue full, wait
        p.tasks <- task
    }
}
```

**Code Location**: `internal/agents/leader/worker_pool.go:50-100`

## Caching Strategies

### LLM Response Caching

Enable LLM response caching to reduce duplicate requests:

```yaml
llm:
  provider: "ollama"
  model: "llama3.2"
  
  # Cache configuration
  cache:
    enabled: true
    type: "memory"           # memory / redis
    ttl: 3600                # Cache time (seconds)
    max_size: 1000           # Maximum cache entries
```

**Code Location**: `internal/llm/client.go:200-250`

### Tool Result Caching

Cache tool execution results:

```yaml
tools:
  cache:
    enabled: true
    ttl: 1800                # 30 minutes
    cache_keys:
      - "calculator.*"
      - "datetime.*"
```

**Code Location**: `internal/tools/resources/core/cache.go:80-120`

### Embedding Caching

Embedding results use Redis cache:

```yaml
embedding:
  service_url: "http://localhost:8000"
  
  # Cache configuration
  cache:
    enabled: true
    type: "redis"
    redis_url: "redis://localhost:6379"
    ttl: 86400               # 24 hours
```

**Code Location**: `services/embedding/app.py:100-150`

## Vector Retrieval Optimization

### pgvector Index Optimization

Create appropriate vector indexes:

```sql
-- Create ivfflat index
CREATE INDEX knowledge_chunks_1024_embedding_idx 
ON knowledge_chunks_1028 
USING ivfflat (embedding vector_cosine_ops) 
WITH (lists = 100);

-- Create HNSW index (faster queries)
CREATE INDEX knowledge_chunks_1024_embedding_hnsw 
ON knowledge_chunks_1028 
USING hnsw (embedding vector_cosine_ops);
```

**Code Location**: `internal/storage/postgres/migrations/001_create_indexes.sql:20-50`

### Retrieval Parameter Tuning

```yaml
knowledge:
  # Retrieval configuration
  retrieval:
    top_k: 10                # Number of results to return
    min_score: 0.6           # Minimum similarity
    ef_search: 100           # HNSW parameter (performance)
    ef_construction: 100     # HNSW parameter (precision)
```

**Code Location**: `internal/storage/postgres/repositories/knowledge_repository.go:100-150`

### Batch Retrieval Optimization

Use batch retrieval to reduce network overhead:

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

**Code Location**: `internal/storage/postgres/repositories/knowledge_repository.go:200-250`

## Memory Optimization

### Memory Limit Configuration

```yaml
memory:
  # Session memory limits
  session:
    max_history: 50         # Maximum history records
    max_message_size: 10240 # Max message size per entry
    
  # Distillation memory optimization
  distillation:
    batch_size: 10          # Batch processing size
    max_concurrent: 5       # Max concurrent distillation tasks
```

**Code Location**: `internal/memory/context/session.go:50-100`

### Object Pool Optimization

Use object pools to reduce GC pressure:

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

**Code Location**: `internal/protocol/ahp/message.go:50-80`

## Monitoring Metrics

### Key Performance Metrics

#### 1. Agent Response Time

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

**Code Location**: `internal/agents/base/metrics.go:30-60`

#### 2. Database Query Time

```go
func (r *Repository) QueryWithMetrics(ctx context.Context, query string) (*Result, error) {
    start := time.Now()
    defer func() {
        duration := time.Since(start)
        metrics.Record("db.query.duration", duration)
        
        // Slow query warning
        if duration > 1*time.Second {
            log.Warn("Slow query detected", "duration", duration)
        }
    }()
    
    return r.Query(ctx, query)
}
```

**Code Location**: `internal/storage/postgres/repositories/base.go:80-120`

#### 3. Cache Hit Rate

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

**Code Location**: `internal/cache/metrics.go:50-100`

### Performance Monitoring Dashboard

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

// Print performance report
func (m *PerformanceMonitor) PrintReport() {
    report := m.Report()
    
    fmt.Println("=== Performance Report ===")
    fmt.Printf("Avg Response Time: %.2f ms\n", report["avg_response_time"])
    fmt.Printf("Cache Hit Rate: %.2f%%\n", report["cache_hit_rate"])
    fmt.Printf("DB Query Time: %.2f ms\n", report["avg_db_query_time"])
    fmt.Printf("Concurrent Tasks: %.0f\n", report["concurrent_tasks"])
}
```

**Code Location**: `internal/observability/monitor.go:80-120`

## Common Performance Issues

### Issue 1: Slow Response

**Symptom**: Agent response time is too long

**Diagnosis**:
```go
func DiagnoseSlowResponse() {
    // Check 1: Database connection pool
    stats := db.Stats()
    if stats.InUse == stats.MaxOpenConnections {
        log.Warn("Database connection pool exhausted")
    }
    
    // Check 2: Concurrent task count
    activeTasks := agent.GetActiveTaskCount()
    if activeTasks > agent.config.MaxParallelTasks {
        log.Warn("Exceeding parallel task limit")
    }
    
    // Check 3: Cache hit rate
    if cache.HitRate() < 50 {
        log.Warn("Low cache hit rate, consider tuning")
    }
}
```

**Solutions**:
- Increase `max_open_conns`
- Optimize SQL queries
- Increase cache size

**Code Location**: `internal/observability/diagnostics.go:50-100`

### Issue 2: High Memory Usage

**Symptom**: Process memory usage keeps growing

**Diagnosis**:
```go
func CheckMemoryLeak() {
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    
    log.Info("Memory Stats",
        "alloc", m.Alloc/1024/1024,           // Allocated memory (MB)
        "sys", m.Sys/1024/1024,             // System memory (MB)
        "num_gc", m.NumGC,                // GC count
        "next_gc", m.NextGC,              // Next GC time
    )
    
    // Check goroutine leak
    numGoroutines := runtime.NumGoroutine()
    if numGoroutines > 1000 {
        log.Warn("High goroutine count detected")
    }
}
```

**Solutions**:
- Enable object pooling
- Limit concurrency
- Check for goroutine leaks

**Code Location**: `internal/observability/diagnostics.go:150-200`

### Issue 3: Database Connection Exhaustion

**Symptom**: Unable to get database connection

**Diagnosis**:
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
    
    // Warn about long wait times
    if stats.WaitDuration > 5*time.Second {
        log.Warn("Long wait time for database connection")
    }
}
```

**Solutions**:
- Increase `max_open_conns`
- Optimize long-running queries
- Use connection pool timeout

**Code Location**: `internal/storage/postgres/diagnostics.go:50-100`

## Performance Benchmarks

### Benchmark Scenarios

#### Scenario 1: Single Agent Query

```
Configuration: max_parallel_tasks=1
Average Response Time: 2.5s
Throughput: 24 queries/min
```

#### Scenario 2: Parallel Agent Query

```
Configuration: max_parallel_tasks=4
Average Response Time: 1.8s
Throughput: 133 queries/min
```

#### Scenario 3: Cached Query

```
Configuration: enable_cache=true, ttl=3600
Average Response Time (first): 2.5s
Average Response Time (cache hit): 50ms
Cache Hit Rate: 85%
```

### Performance Comparison Table

| Configuration | Avg Response Time | Throughput | Memory Usage |
|----------------|-------------------|-----------|-------------|
| Conservative Mode | 3.2s | 18/min | 200MB |
| Balanced Mode | 1.8s | 133/min | 350MB |
| Aggressive Mode | 1.2s | 200/min | 500MB |

**Code Location**: `benchmark/agent_benchmark.go:100-150`

## Optimization Recommendations

### Short-term Optimizations (Immediate)

1. **Enable Caching**
   ```yaml
   llm:
     cache:
       enabled: true
       ttl: 3600
   ```

2. **Adjust Connection Pool**
   ```yaml
   max_open_conns: 25
   max_idle_conns: 10
   ```

3. **Enable Vector Index**
   ```sql
   CREATE INDEX ... USING ivfflat
   ```

**Code Location**: 
- `internal/llm/client.go:200-250`
- `internal/storage/postgres/pool.go:70-100`
- `internal/storage/postgres/migrations/001_create_indexes.sql:20-50`

### Medium-term Optimizations (Planning Required)

1. **Implement Read-Write Separation**
2. **Add Redis Cluster**
3. **Optimize Embedding Model**
4. **Implement Result Pagination**

### Long-term Optimizations (Architecture Changes)

1. **Distributed Deployment**
2. **Implement Service Mesh**
3. **Introduce Message Queue**
4. **Optimize Model Inference**

## References

- [Integration Guide](integration_guide_en.md)
- [Testing Guide](testing_guide_en.md)
- [Architecture Documentation](arch.md)
- [Storage API](storage/api_en.md)

---

**Version**: 1.0  
**Last Updated**: 202-2026-03-23  
**Maintainer**: GoAgent Team