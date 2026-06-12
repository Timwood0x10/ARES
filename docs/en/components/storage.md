# Storage System Design Document

## 1. Overview

The Storage module is a production-grade AI Memory & Retrieval System built on PostgreSQL 16 + pgvector, providing multi-tenant support, asynchronous embedding pipeline, and enterprise-grade security for the Agent Framework.

### Architecture Level
- **L3 - Infra Agent (Production Ready)**
- **Core Features**: Multi-tenant isolation, vector search, hybrid retrieval, secret management

### Key Design Principles
1. **Vector Dimension Segmentation**: Separate tables for each vector dimension (avoids mixing vector spaces)
2. **Deduplication**: Hash-based deduplication + async embedding deduplication
3. **Graceful Degradation**: Complete fallback mechanisms for all critical paths
4. **Multi-Tenancy**: RLS (Row Level Security) + Tenant Guard dual-layer protection

## 2. Architecture Components

### 2.1 Database Schema

#### Core Tables

| Table | Purpose | Vector Dimension |
|-------|---------|------------------|
| `knowledge_chunks_1024` | RAG knowledge base | 1024D |
| `experiences_1024` | Agent experiences | 1024D |
| `tools` | Tool semantic search | Optional |
| `conversations` | Conversation history | None |
| `task_results_1024` | Task execution results | 1024D |
| `secrets` | Encrypted sensitive data | None |
| `models_config` | Model version tracking | None |

#### Key Features
- **Multi-tenant**: All tables include `tenant_id` field
- **Vector Index**: IVFFlat index for vector similarity search
- **Full-text Search**: TSV index with pre-computed tsvector column
- **Hash Deduplication**: UNIQUE index on `content_hash` for real-time deduplication
- **Row Level Security**: RLS policies for tenant isolation
- **Asynchronous Embedding**: Queue-based embedding pipeline with retry mechanism

### 2.2 System Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Application Layer                     │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │ MemoryManager│  │  Agent Logic │  │  API Layer   │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         │         Service Layer              │         │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────▼───────┐ │
│  │RetrievalService│ │MemoryPolicy │ │TenantGuard   │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         │        Repository Layer             │         │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────▼───────┐ │
│  │KnowledgeRepo │ │ExperienceRepo│ │SecretRepo    │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         │          Adapter Layer              │         │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────▼───────┐ │
│  │SecretAdapter │ │EmbeddingCache│ │QueryCache    │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         │         Data Layer                  │         │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────▼───────┐ │
│  │PostgreSQL 16 │ │pgvector 0.5.0 │ │EmbeddingService││
│  └──────────────┘  └──────────────┘  └──────────────┘ │
└─────────────────────────────────────────────────────────┘
```

## 3. Core Components

### 3.1 Repository Layer

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

### 3.2 Service Layer

#### RetrievalService
Implements hybrid retrieval pipeline with multiple strategies:

**Core Features:**
- Parallel vector search across multiple sources (knowledge, experience, tools)
- BM25 fallback when vector search fails
- Query Rewrite (optional, LLM-based)
- Time decay scoring
- Result fusion with RRF (Reciprocal Rank Fusion)

**Performance Targets:**
- Average latency: 200-500ms
- Vector search: 2s timeout
- Concurrency limit: 3 parallel searches

#### MemoryPolicy
Implements intelligent data filtering:

**Features:**
- ShouldStore: Determine if data is worth storing
- GetTTL: Get data time-to-live
- ShouldDecay: Determine if data should decay

**Policy Examples:**
- Failed experiences with low score (< 0.7) are discarded
- Conversations with system role are not stored
- Experiences have 30-day TTL (success) or 7-day TTL (failure)

### 3.3 Adapter Layer

#### SecretAdapter
Format conversion for secret import/export:

**Supported Formats:**
- JSON: Standard JSON format
- YAML: YAML format with key-value pairs
- CSV: CSV format with columns (key, value, expires_at)

**Auto-Detection:**
Automatically detects input format based on content analysis.

#### EmbeddingCache
Multi-level caching for embeddings:

**Cache Hierarchy:**
1. Local LRU cache (in-memory)
2. Redis cache (distributed)
3. Embedding service (remote)

**Cache Key Normalization:**
- Unicode normalization (NFKC)
- Case folding
- Whitespace normalization

## 4. Asynchronous Embedding Pipeline

### 4.1 Pipeline Design

```
Write Data (without embedding)
    ↓
Mark embedding_status = 'pending'
    ↓
Write to embedding_queue table
    ↓
Embedding Worker polls tasks
    ↓
Calculate embedding
    ↓
Update data (status='completed', embedding value)
    ↓
Retry on failure (max 3 retries, exponential backoff)
    ↓
Final failure → status='failed' + dead letter queue
```

### 4.2 Queue Table Structure

```sql
CREATE TABLE embedding_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id TEXT NOT NULL,
    table_name TEXT NOT NULL,
    content TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    embedding_model TEXT DEFAULT 'e5-large',
    embedding_version INT DEFAULT 1,
    dedupe_key TEXT UNIQUE,  -- Idempotency guarantee
    retry_count INT DEFAULT 0,
    status TEXT DEFAULT 'pending',
    queued_at TIMESTAMP DEFAULT NOW(),
    processing_at TIMESTAMP,
    completed_at TIMESTAMP,
    error_message TEXT
);
```

### 4.3 Concurrency Control

**FOR UPDATE SKIP LOCKED:**
```sql
SELECT id, task_id, table_name, content, tenant_id, 
       embedding_model, embedding_version, retry_count
FROM embedding_queue
WHERE status = 'pending'
ORDER BY queued_at ASC
FOR UPDATE SKIP LOCKED
LIMIT $1
```

### 4.4 Reconciler

**Purpose:** Find and re-queue missing embedding tasks

**Logic:**
```sql
SELECT id, tenant_id, content, embedding_model, embedding_version
FROM knowledge_chunks_1024
WHERE embedding_status = 'pending'
  AND embedding_queued_at < NOW() - $1
  AND embedding_processed_at IS NULL
LIMIT 1000
```

## 5. Security Features

### 5.1 Multi-Tenant Isolation

**Dual-Layer Protection:**
1. **RLS (Row Level Security)**: Database-level logical isolation
2. **Tenant Guard**: Application-level physical isolation

**Implementation:**
```go
// Tenant Guard
func (g *TenantGuard) SetTenantContext(ctx context.Context, tenantID string) error {
    _, err := g.db.ExecContext(ctx, "SET app.tenant_id = $1", tenantID)
    return err
}

// RLS Policy
CREATE POLICY tenant_isolation ON knowledge_chunks_1024
FOR ALL USING (tenant_id = current_setting('app.tenant_id')::TEXT);
```

### 5.2 Secret Management

**Encryption:**
- Algorithm: AES-256-GCM
- Key rotation support
- Per-secret key versioning

**Import/Export:**
- Export: Metadata only (no encrypted values)
- Import: Multi-format support (JSON/YAML/CSV)
- Key rotation: Atomic transaction-based re-encryption

### 5.3 Input Validation

**Sanitization:**
- SQL injection prevention (parameterized queries)
- XSS prevention (output encoding)
- Input length limits

## 6. Performance Optimization

### 6.1 Write Buffer

**Purpose:** Reduce DB QPS and embedding service load

**Implementation:**
```go
type WriteBuffer struct {
    buffer chan *WriteItem
    batchSize int
    flushInterval time.Duration
}
```

**Performance Impact:**
- DB QPS: ↓ 80%
- Embedding calls: ↓ 50%
- Latency: More stable

### 6.2 Query Cache

**Purpose:** Cache query results to bypass DB + embedding

**Cache Key:**
```go
func (c *QueryCache) GetCacheKey(query string, tenantID string, filters map[string]interface{}) string {
    keyData := fmt.Sprintf("query:%s:%s:%v", tenantID, query, filters)
    hash := sha256.Sum256([]byte(keyData))
    return fmt.Sprintf("query_cache:%x", hash[:16])
}
```

**Performance Impact:**
- Latency: ↓ 70%
- DB QPS: ↓
- Embedding QPS: ↓

### 6.3 Time Decay Scoring

**Formula:**
```go
final_score = base_score * time_decay
```

**Purpose:** Prevent old data from dominating results

## 7. Error Handling & Resilience

### 7.1 Circuit Breaker

**Purpose:** Prevent cascading failures

**Configuration:**
- Failure threshold: 5 consecutive failures
- Timeout: 2s
- Half-open timeout: 10s

### 7.2 Rate Limiting

**Strategies:**
- Token bucket rate limiter
- Sliding window rate limiter
- Semaphore for concurrent operations

### 7.3 Timeout Protection

**Timeouts:**
- DB operations: 2s
- Embedding calls: 5s
- Vector search: 2s
- Overall request: 10s

## 8. Monitoring & Observability

### 8.1 Logging

**Structured Logging:**
- Use `slog` for structured logging
- Include `tenant_id` and `trace_id` in all log entries
- Do not log raw LLM inputs/outputs in production

### 8.2 Metrics

**Key Metrics:**
- Embedding queue length
- Cache hit rates
- Retrieval latency
- Error rates
- Resource usage

### 8.3 Tracing

**Retrieval Trace:**
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

## 9. Configuration

### 9.1 Database Configuration

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

### 9.2 Embedding Configuration

```yaml
embedding:
  service_url: http://localhost:8000
  model: intfloat/e5-large
  dimension: 1024
  timeout: 5s
  cache_ttl: 24h
  batch_size: 32
```

### 9.3 Retrieval Configuration

```yaml
retrieval:
  vector_search_timeout: 2s
  keyword_search_timeout: 1s
  max_results: 20
  enable_query_rewrite: false
  enable_hybrid_search: true
  time_decay_enabled: true
```

## 10. Testing

### 10.1 Test Coverage

**Current Coverage:**
- Models: 100%
- Adapters: 85%
- Query: 75%
- Embedding: 25.8%
- Services: 25.8%
- Repositories: 0% (needs completion)

### 10.2 Test Requirements

**Mandatory Tests:**
- Unit tests for all public methods
- Integration tests for critical paths
- Race condition detection (`go test -race`)
- Boundary condition tests
- Error scenario tests


## 11. Deployment

### 11.1 Prerequisites

- PostgreSQL 16+
- pgvector extension (version 0.5.0+)
- Python embedding service
- Redis (optional, for caching)

### 11.2 Migration Steps

1. **Database Migration:**
   ```bash
   go run cmd/migrate/main.go
   ```

2. **Start Embedding Service:**
   ```bash
   cd services/embedding
   ./start.sh
   ```

3. **Start Application:**
   ```bash
   go run cmd/server/main.go
   ```

### 11.3 Health Checks

**Endpoints:**
- `/health`: Application health
- `/health/db`: Database connectivity
- `/health/embedding`: Embedding service availability

## 12. Troubleshooting

### 12.1 Common Issues

**Issue: High embedding queue length**
- Cause: Embedding service slow or down
- Solution: Check embedding service health, scale workers

**Issue: Poor retrieval quality**
- Cause: Incorrect vector dimensions or outdated embeddings
- Solution: Verify embedding model, re-embed if needed

**Issue: Cross-tenant data access**
- Cause: Tenant Guard not properly configured
- Solution: Verify tenant context is set in all operations

### 12.2 Performance Tuning

**Database Tuning:**
- Increase `work_mem` for vector operations
- Adjust `effective_cache_size` based on available RAM
- Use connection pooling

**Embedding Tuning:**
- Increase batch size for bulk operations
- Enable caching for frequently queried texts
- Scale embedding service horizontally

## 13. Future Enhancements

### 13.1 Planned Features

- **Query Rewrite**: LLM-based query expansion and refinement
- **Advanced Vector Indexing**: HNSW index for better performance
- **Multi-Model Support**: Support multiple embedding models
- **Streaming Retrieval**: Real-time result streaming

### 13.2 Performance Improvements

- **GPU Acceleration**: GPU-based embedding computation
- **Distributed Caching**: Multi-node caching layer
- **Read Replicas**: Database read scaling

## 14. References

- [Effective Go](https://go.dev/doc/effective_go)
- [pgvector Documentation](https://github.com/pgvector/pgvector)
- [PostgreSQL Row Level Security](https://www.postgresql.org/docs/current/ddl-rowsecurity.html)
- [AES-GCM Encryption](https://en.wikipedia.org/wiki/Galois/Counter_Mode)

## 15. Version History

| Version | Date | Changes |
|---------|------|---------|
| 1.0.0 | 2026-03-18 | Initial production release |
| 1.0.1 | 2026-03-18 | Added secret import adapter layer |
| 1.0.2 | 2026-03-18 | Completed models test coverage (100%) |