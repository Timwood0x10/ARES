# Storage Module API Documentation

**Last Updated**: 2026-03-24

## Overview

The Storage module is the core data persistence layer of GoAgent, implemented based on PostgreSQL 15+ with pgvector, providing high-performance vector storage, retrieval, and multi-tenant isolation capabilities.

### Core Capabilities

- **Vector Storage & Retrieval**: High-performance vector similarity search based on pgvector
- **Connection Pool Management**: "Get-Use-Release" pattern connection pool
- **Intelligent Caching**: Embedding vector caching, result caching
- **Rate Limiting & Circuit Breaking**: System stability protection
- **Transaction Support**: Complete database transaction support

## Architecture Design

### Layered Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   Application Layer                       │
│  Knowledge Base | Agent Experience | Tool Management    │
└─────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────┐
│                 Data Access Layer (Repositories)         │
│  KnowledgeRepository | SessionRepository | ...          │
└─────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────┐
│                   Core Layer                              │
│  Pool | TenantGuard | RetrievalGuard | Security         │
└─────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────┐
│              PostgreSQL 15+ + pgvector                   │
│  knowledge_chunks_1024 | sessions | distilled_memories   │
└─────────────────────────────────────────────────────────┘
```

### Core Components

#### 1. Pool (Connection Pool)

Database connection pool implementing the "Get-Use-Release" pattern.

**Code Location**: `internal/storage/postgres/pool.go:1-50`

**Interface:**
```go
type Pool struct {
    cfg          *Config
    db           *sql.DB
    mu           sync.RWMutex
    openCount    int
    idleCount    int
    waitCount    int
    waitDuration time.Duration
}

// Create connection pool
func NewPool(cfg *Config) (*Pool, error)

// Get connection
func (p *Pool) Get(ctx context.Context) (*sql.Conn, error)

// Release connection
func (p *Pool) Release(conn *sql.Conn)

// Use connection (recommended pattern)
func (p *Pool) WithConnection(ctx context.Context, fn func(*sql.Conn) error) error

// Close connection pool
func (p *Pool) Close() error

// Get statistics
func (p *Pool) Stats() *PoolStats

// Check health status
func (p *Pool) IsHealthy() bool

// Ping database
func (p *Pool) Ping(ctx context.Context) error

// Execute query
func (p *Pool) Exec(ctx context.Context, query string, args ...any) (sql.Result, error)

// Query multiple rows
func (p *Pool) Query(ctx context.Context, query string, args ...any) (*ManagedRows, error)

// Query single row
func (p *Pool) QueryRow(ctx context.Context, query string, args ...any) *sql.Row

// Begin transaction
func (p *Pool) Begin(ctx context.Context) (*sql.Tx, error)
```

**Configuration:**
```go
type Config struct {
    Host            string
    Port            int
    User            string
    Password        string
    Database        string
    MaxOpenConns    int           // Maximum open connections (default 25)
    MaxIdleConns    int           // Maximum idle connections (default 10)
    ConnMaxLifetime time.Duration // Connection max lifetime (default 5 minutes)
    ConnMaxIdleTime time.Duration // Connection max idle time (default 1 minute)
    QueryTimeout    time.Duration // Query timeout (default 30 seconds)
    SSLMode         string        // SSL mode (disable, require, verify-ca, verify-full)
}

// DSN generates connection string
func (c *Config) DSN() string

// Validate configuration
func (c *Config) Validate() error
```

**Statistics:**
```go
type PoolStats struct {
    OpenConnections  int           // Current open connections
    InUseConnections int           // Connections in use
    IdleConnections  int           // Idle connections
    WaitCount        int64         // Wait count
    WaitDuration     time.Duration // Total wait duration
    MaxOpenConns     int           // Maximum open connections
}
```

## Functional Modules

### 1. Knowledge Repository

Manages document knowledge storage and retrieval.

**Code Location**: `internal/storage/postgres/repositories/knowledge_repository.go`

**Core Features:**
- Document chunk storage
- Vector similarity retrieval
- Batch import
- Time decay

**Interface:**
```go
type KnowledgeRepository struct {
    db     DBTX
    dbPool *sql.DB
}

// Create knowledge chunk
func (r *KnowledgeRepository) Create(ctx context.Context, chunk *KnowledgeChunk) error

// Batch create
func (r *KnowledgeRepository) CreateBatch(ctx context.Context, chunks []*KnowledgeChunk) error

// Get by ID
func (r *KnowledgeRepository) GetByID(ctx context.Context, id string) (*KnowledgeChunk, error)

// Vector search
func (r *KnowledgeRepository) SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*KnowledgeChunk, error)

// List all chunks by document
func (r *KnowledgeRepository) ListByDocument(ctx context.Context, documentID, tenantID string) ([]*KnowledgeChunk, error)

// Update embedding vector
func (r *KnowledgeRepository) UpdateEmbedding(ctx context.Context, id string, embedding []float64, model string, version int) error

// Delete
func (r *KnowledgeRepository) Delete(ctx context.Context, id string) error
```

**Data Model:**
```go
type KnowledgeChunk struct {
    ID               string                 `json:"id"`
    TenantID         string                 `json:"tenant_id"`
    Content          string                 `json:"content"`
    Embedding        []float64              `json:"embedding"`
    EmbeddingModel   string                 `json:"embedding_model"`
    EmbeddingVersion int                    `json:"embedding_version"`
    EmbeddingStatus  string                 `json:"embedding_status"`
    SourceType       string                 `json:"source_type"`
    Source           string                 `json:"source"`
    Metadata         map[string]interface{} `json:"metadata"`
    DocumentID       string                 `json:"document_id"`
    ChunkIndex       int                    `json:"chunk_index"`
    ContentHash      string                 `json:"content_hash"`
    AccessCount      int                    `json:"access_count"`
    CreatedAt        time.Time              `json:"created_at"`
    UpdatedAt        time.Time              `json:"updated_at"`
}
```

### 2. Session Repository

Manages user sessions and message history.

**Code Location**: `internal/storage/postgres/repositories/session_repository.go`

**Interface:**
```go
type SessionRepository struct {
    db DBTX
}

// Create session
func (r *SessionRepository) Create(ctx context.Context, session *Session) error

// Get session
func (r *SessionRepository) Get(ctx context.Context, id string) (*Session, error)

// Delete session
func (r *SessionRepository) Delete(ctx context.Context, id string) error

// Add message
func (r *SessionRepository) AddMessage(ctx context.Context, message *Message) error

// Get messages
func (r *SessionRepository) GetMessages(ctx context.Context, sessionID string, limit int) ([]*Message, error)
```

**Data Model:**
```go
type Session struct {
    ID        string                 `json:"id"`
    UserID    string                 `json:"user_id"`
    TenantID  string                 `json:"tenant_id"`
    Status    string                 `json:"status"`
    CreatedAt time.Time              `json:"created_at"`
    UpdatedAt time.Time              `json:"updated_at"`
    ExpiresAt *time.Time             `json:"expires_at,omitempty"`
    Metadata  map[string]interface{} `json:"metadata"`
}

type Message struct {
    ID        string                 `json:"id"`
    SessionID string                 `json:"session_id"`
    Role      string                 `json:"role"`
    Content   string                 `json:"content"`
    Time      time.Time              `json:"time"`
    Metadata  map[string]interface{} `json:"metadata"`
}
```

## Usage Guide

### Basic Usage

#### 1. Initialize Connection Pool

**Code Location**: `internal/storage/postgres/pool.go:30-50`

```go
import "goagent/internal/storage/postgres"

config := &postgres.Config{
    Host:            "localhost",
    Port:            5433,
    User:            "postgres",
    Password:        "postgres",
    Database:        "goagent",
    MaxOpenConns:    25,
    MaxIdleConns:    10,
    ConnMaxLifetime: 5 * time.Minute,
    QueryTimeout:    30 * time.Second,
}

pool, err := postgres.NewPool(config)
if err != nil {
    slog.Error("Failed to create pool", "error", err)
    return
}
defer pool.Close()
```

#### 2. Use Connection Pool (Recommended Pattern)

**Code Location**: `internal/storage/postgres/pool.go:70-90`

```go
// Use WithConnection pattern
err := pool.WithConnection(ctx, func(conn *sql.Conn) error {
    var result int
    return conn.QueryRowContext(ctx, "SELECT 1").Scan(&result)
})

if err != nil {
    slog.Error("Query failed", "error", err)
}
```

#### 3. Query Data

**Code Location**: `internal/storage/postgres/pool.go:100-120`

```go
// Query single row
row := pool.QueryRow(ctx, "SELECT id, content FROM knowledge_chunks WHERE id = $1", chunkID)

var chunk KnowledgeChunk
err := row.Scan(&chunk.ID, &chunk.Content)
if err != nil {
    slog.Error("Failed to scan row", "error", err)
}
```

#### 4. Execute Query

**Code Location**: `internal/storage/postgres/pool.go:130-150`

```go
// Execute insert
result, err := pool.Exec(ctx, `
    INSERT INTO knowledge_chunks (id, tenant_id, content)
    VALUES ($1, $2, $3)
`, chunkID, tenantID, content)

if err != nil {
    slog.Error("Failed to insert", "error", err)
}

rowsAffected, _ := result.RowsAffected()
slog.Info("Inserted rows", "count", rowsAffected)
```

#### 5. Use Transactions

**Code Location**: `internal/storage/postgres/pool.go:160-180`

```go
// Begin transaction
tx, err := pool.Begin(ctx)
if err != nil {
    return err
}

// Execute multiple operations
_, err = tx.ExecContext(ctx, "INSERT INTO sessions ...")
if err != nil {
    tx.Rollback()
    return err
}

_, err = tx.ExecContext(ctx, "INSERT INTO messages ...")
if err != nil {
    tx.Rollback()
    return err
}

// Commit transaction
if err := tx.Commit(); err != nil {
    return err
}
```

### Advanced Usage

#### 1. Connection Pool Monitoring

**Code Location**: `internal/storage/postgres/pool.go:190-210`

```go
// Get connection pool statistics
stats := pool.Stats()

slog.Info("Pool stats",
    "open_connections", stats.OpenConnections,
    "in_use", stats.InUseConnections,
    "idle", stats.IdleConnections,
    "wait_count", stats.WaitCount,
    "wait_duration", stats.WaitDuration,
)

// Check health status
if !pool.IsHealthy() {
    slog.Warn("Pool is not healthy")
}
```

#### 2. Error Handling

**Code Location**: `internal/storage/postgres/pool.go:220-240`

```go
// Handle connection errors
conn, err := pool.Get(ctx)
if err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        slog.Error("Connection timeout")
        return ErrTimeout
    }
    return fmt.Errorf("failed to get connection: %w", err)
}
defer pool.Release(conn)
```

#### 3. Batch Operations

**Code Location**: `internal/storage/postgres/repositories/knowledge_repository.go:100-120`

```go
// Batch create knowledge chunks
chunks := []*KnowledgeChunk{
    {Content: "Content 1", ...},
    {Content: "Content 2", ...},
    {Content: "Content 3", ...},
}

err := kbRepo.CreateBatch(ctx, chunks)
if err != nil {
    slog.Error("Failed to create batch", "error", err)
}
```

## Best Practices

### 1. Connection Pool Configuration

**Code Location**: `internal/storage/postgres/pool.go:50-60`

```go
config := &postgres.Config{
    MaxOpenConns:    25,  // Adjust based on concurrency needs
    MaxIdleConns:    10,  // Maintain some idle connections
    ConnMaxLifetime: 5 * time.Minute,  // Periodically refresh connections
    QueryTimeout:    30 * time.Second,  // Set reasonable timeout
}
```

### 2. Use WithConnection Pattern

**Code Location**: `internal/storage/postgres/pool.go:70-90`

```go
// Recommended pattern: automatically handle connection acquisition and release
err := pool.WithConnection(ctx, func(conn *sql.Conn) error {
    // Use connection to perform operations
    return doSomething(conn)
})
```

### 3. Error Handling

**Code Location**: `internal/storage/postgres/pool.go:220-240`

```go
// Check error types
if errors.Is(err, context.DeadlineExceeded) {
    // Timeout error
    return ErrTimeout
}

if errors.Is(err, sql.ErrConnDone) {
    // Connection closed
    return ErrConnectionClosed
}
```

### 4. Context Management

**Code Location**: `internal/storage/postgres/pool.go:130-150`

```go
// Set timeout
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

// Execute query
result, err := pool.Exec(ctx, query, args...)
```

## Performance Optimization

### 1. Connection Pool Optimization

- **MaxOpenConns**: Adjust based on concurrency needs (default 25)
- **MaxIdleConns**: Maintain some idle connections (default 10)
- **ConnMaxLifetime**: Periodically refresh connections (default 5 minutes)
- **QueryTimeout**: Set reasonable timeout (default 30 seconds)

### 2. Batch Operations

- Use batch insert interfaces
- Batch generate embedding vectors
- Batch update operations

### 3. Index Optimization

- Ensure tenant_id has index
- Ensure embedding_status has index
- Ensure content_hash has index

## Error Handling

### Error Types

**Code Location**: `internal/core/errors/common.go`

```go
// Core errors
var (
    ErrInvalidArgument    = errors.New("invalid argument")
    ErrRecordNotFound     = errors.New("record not found")
    ErrDuplicateKey       = errors.New("duplicate key")
    ErrNoTransaction      = errors.New("no transaction")
    ErrDBConnectionFailed = errors.New("database connection failed")
)
```

### Error Handling Example

**Code Location**: `internal/storage/postgres/pool.go:220-240`

```go
// Check error types
if errors.Is(err, context.DeadlineExceeded) {
    // Timeout error
    return ErrTimeout
}

if errors.Is(err, sql.ErrConnDone) {
    // Connection closed
    return ErrConnectionClosed
}
```

## Monitoring & Logging

### 1. Connection Pool Monitoring

**Code Location**: `internal/storage/postgres/pool.go:190-210`

```go
// Periodically log connection pool statistics
ticker := time.NewTicker(30 * time.Second)
defer ticker.Stop()

for range ticker.C {
    stats := pool.Stats()
    slog.Info("Pool stats",
        "open_connections", stats.OpenConnections,
        "in_use", stats.InUseConnections,
        "idle", stats.IdleConnections,
        "wait_count", stats.WaitCount,
        "wait_duration", stats.WaitDuration,
    )
}
```

### 2. Query Performance Monitoring

```go
// Log query time
start := time.Now()
result, err := pool.Query(ctx, query, args...)
duration := time.Since(start)

slog.Info("Query executed",
    "duration", duration,
    "query", query,
)
```

## References

- [PostgreSQL Documentation](https://www.postgresql.org/docs/15/)
- [pgvector Documentation](https://github.com/pgvector/pgvector)
- [Go database/sql](https://pkg.go.dev/database/sql)

---

**Version**: 1.0  
**Last Updated**: 2026-03-24  
**Maintainer**: GoAgent Team