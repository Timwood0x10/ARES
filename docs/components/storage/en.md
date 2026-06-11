# Storage System Design Document

## 1. Overview

The Storage module is a production-grade AI Memory & Retrieval System providing multi-tenant support, asynchronous embedding pipeline, and enterprise-grade security for the Agent Framework.

### Architecture Level
- **L3 - Infra Agent (Production Ready)**
- **Core Features**: Pluggable vector backend, multi-tenant isolation, vector search, hybrid retrieval, secret management

### Key Design Principles
1. **Pluggable VectorStore**: A single interface (`storage.VectorStore`) abstracts all vector backends -- swap PostgreSQL, Qdrant, Milvus, or in-memory without touching business logic
2. **Vector Dimension Segmentation**: Separate tables for each vector dimension (avoids mixing vector spaces)
3. **Deduplication**: Hash-based deduplication + async embedding deduplication
4. **Graceful Degradation**: Complete fallback mechanisms for all critical paths
5. **Multi-Tenancy**: RLS (Row Level Security) + Tenant Guard dual-layer protection

## 2. Pluggable VectorStore Interface

The vector storage layer is defined by a single interface in `internal/storage/vector.go`. Every vector backend -- PostgreSQL + pgvector, Qdrant, Milvus, SQLite-vec, or in-memory -- implements this contract.

### 2.1 Interface Definition

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

### 2.2 Built-in Implementations

| Backend | Package | Use Case |
|---------|---------|----------|
| PostgreSQL + pgvector | `internal/storage/postgres` | Production, full SQL features, IVFFlat index |
| In-memory | `internal/storage/memory` | Development, testing, prototyping |

**PostgreSQL implementation** (`VectorSearcher`): Uses pgvector's `<=>` cosine distance operator with IVFFlat indexing. Validates table names to prevent SQL injection, enforces configurable search limits, and creates `VECTOR(N)` columns with JSONB metadata.

**In-memory implementation**: Thread-safe (uses `sync.RWMutex`), brute-force cosine similarity search, zero external dependencies. Ideal for unit tests and local development.

### 2.3 Repository Integration

The `Repository` struct holds a `Vector` field typed as `storage.VectorStore`, making the backend swappable at construction time or at runtime:

```go
// internal/storage/postgres/repository.go
type Repository struct {
    Session   *SessionRepository
    Recommend *RecommendRepository
    Profile   *ProfileRepository
    Vector    storage.VectorStore  // <-- pluggable
    // ...
}
```

Swap the vector backend in two lines:

```go
// Production: PostgreSQL + pgvector (default)
repo := postgres.NewRepository(pool)

// Development/testing: in-memory
repo.Vector = memory.NewVectorStore()

// Custom backend: any implementation of storage.VectorStore
repo.Vector = myqdrant.New("localhost", 6333)
```

### 2.4 Adding a Custom Backend

See [Custom Vector Store Guide](../../en/development/custom-vector-store.md) for a complete walkthrough with examples for Qdrant, Milvus, Elasticsearch, and SQLite-vec.

## 3. Architecture Components

### 3.1 Database Schema

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

### 3.2 System Architecture

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

## 4. Core Components

### 4.1 Repository Layer

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

### 4.2 Service Layer

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

### 4.3 Adapter Layer

#### SecretAdapter
Format conversion for secret import/export (JSON, YAML, CSV). Auto-detects input format based on content analysis.

#### EmbeddingCache
Multi-level caching for embeddings: local LRU (in-memory) -> Redis (distributed) -> embedding service (remote). Cache keys use Unicode normalization (NFKC), case folding, and whitespace normalization.

## 5. Asynchronous Embedding Pipeline

### 5.1 Pipeline Design

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

### 5.2 Queue Table Structure

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

### 5.3 Concurrency Control

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

### 5.4 Reconciler

**Purpose:** Find and re-queue missing embedding tasks.

```sql
SELECT id, tenant_id, content, embedding_model, embedding_version
FROM knowledge_chunks_1024
WHERE embedding_status = 'pending'
  AND embedding_queued_at < NOW() - $1
  AND embedding_processed_at IS NULL
LIMIT 1000
```

## 6. Security Features

### 6.1 Multi-Tenant Isolation

**Dual-Layer Protection:**
1. **RLS (Row Level Security)**: Database-level logical isolation
2. **Tenant Guard**: Application-level physical isolation

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

### 6.2 Secret Management

- **Encryption**: AES-256-GCM with key rotation and per-secret key versioning
- **Import/Export**: Multi-format support (JSON/YAML/CSV), metadata-only export
- **Key Rotation**: Atomic transaction-based re-encryption

### 6.3 Input Validation

- SQL injection prevention (parameterized queries)
- XSS prevention (output encoding)
- Input length limits

## 7. Performance & Resilience

- **Write Buffer**: Batching reduces DB QPS by ~80% and embedding calls by ~50%
- **Query Cache**: SHA-256 hash of tenant + query + filters as key, reduces latency by ~70%
- **Time Decay**: `final_score = base_score * time_decay` prevents old data from dominating
- **Circuit Breaker**: 5 consecutive failures -> open (2s timeout), half-open after 10s
- **Rate Limiting**: Token bucket + sliding window + semaphore
- **Timeouts**: DB 2s, embedding 5s, vector search 2s, overall request 10s

## 8. Monitoring

- **Logging**: `slog` structured logging with `tenant_id` and `trace_id`
- **Key Metrics**: Embedding queue length, cache hit rates, retrieval latency, error rates
- **Tracing**: `RetrievalTrace` struct captures query rewriting, result counts, execution time

## 9. Configuration

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

## 10. Deployment

**Prerequisites**: PostgreSQL 16+ with pgvector (0.5.0+), Python embedding service, Redis (optional).

```bash
go run cmd/migrate/main.go   # Database migration
go run cmd/server/main.go     # Start application
```

**Health checks**: `/health`, `/health/db`, `/health/embedding`

## 11. Troubleshooting

**High embedding queue length**: Embedding service slow or down -- check health, scale workers.

**Poor retrieval quality**: Incorrect vector dimensions or outdated embeddings -- verify model, re-embed.

**Cross-tenant data access**: Tenant Guard misconfigured -- verify tenant context in all operations.

**Database tuning**: Increase `work_mem` for vector operations, adjust `effective_cache_size`, use connection pooling.
