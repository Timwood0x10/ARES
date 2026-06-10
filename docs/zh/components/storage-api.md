# Storage 模块 API 文档

**更新日期**: 2026-03-24

## 概述

Storage模块是GoAgent的核心数据持久化层，基于PostgreSQL 15+ with pgvector实现，提供高性能的向量存储、检索和多租户隔离能力。

### 核心能力

- **向量存储与检索**: 基于pgvector的高性能向量相似度搜索
- **连接池管理**: "获取-使用-释放"模式的连接池
- **智能缓存**: 嵌入向量缓存、结果缓存
- **限流熔断**: 保护系统稳定性
- **事务支持**: 完整的数据库事务支持

## 架构设计

### 分层架构

```
┌─────────────────────────────────────────────────────────┐
│                   应用层 (Application)                    │
│  知识库应用 | 代理经验存储 | 工具管理 | 任务结果存储      │
└─────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────┐
│                 数据访问层 (Repositories)                 │
│  KnowledgeRepository | SessionRepository | ...          │
└─────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────┐
│                   核心层 (Core)                          │
│  Pool | TenantGuard | RetrievalGuard | Security         │
└─────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────┐
│              PostgreSQL 15+ + pgvector                   │
│  knowledge_chunks_1024 | sessions | distilled_memories   │
└─────────────────────────────────────────────────────────┘
```

### 核心组件

#### 1. Pool (连接池)

数据库连接池，实现"获取-使用-释放"模式。

**代码位置**: `internal/storage/postgres/pool.go:1-50`

**接口:**
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

// 创建连接池
func NewPool(cfg *Config) (*Pool, error)

// 获取连接
func (p *Pool) Get(ctx context.Context) (*sql.Conn, error)

// 释放连接
func (p *Pool) Release(conn *sql.Conn)

// 使用连接（推荐模式）
func (p *Pool) WithConnection(ctx context.Context, fn func(*sql.Conn) error) error

// 关闭连接池
func (p *Pool) Close() error

// 获取统计信息
func (p *Pool) Stats() *PoolStats

// 检查健康状态
func (p *Pool) IsHealthy() bool

// Ping数据库
func (p *Pool) Ping(ctx context.Context) error

// 执行查询
func (p *Pool) Exec(ctx context.Context, query string, args ...any) (sql.Result, error)

// 查询多行
func (p *Pool) Query(ctx context.Context, query string, args ...any) (*ManagedRows, error)

// 查询单行
func (p *Pool) QueryRow(ctx context.Context, query string, args ...any) *sql.Row

// 开始事务
func (p *Pool) Begin(ctx context.Context) (*sql.Tx, error)
```

**配置:**
```go
type Config struct {
    Host            string
    Port            int
    User            string
    Password        string
    Database        string
    MaxOpenConns    int           // 最大打开连接数 (默认25)
    MaxIdleConns    int           // 最大空闲连接数 (默认10)
    ConnMaxLifetime time.Duration // 连接最大生命周期 (默认5分钟)
    ConnMaxIdleTime time.Duration // 连接最大空闲时间 (默认1分钟)
    QueryTimeout    time.Duration // 查询超时 (默认30秒)
    SSLMode         string        // SSL模式 (disable, require, verify-ca, verify-full)
}

// DSN 生成连接字符串
func (c *Config) DSN() string

// 验证配置
func (c *Config) Validate() error
```

**统计信息:**
```go
type PoolStats struct {
    OpenConnections  int           // 当前打开的连接数
    InUseConnections int           // 正在使用的连接数
    IdleConnections  int           // 空闲连接数
    WaitCount        int64         // 等待连接次数
    WaitDuration     time.Duration // 等待连接总时长
    MaxOpenConns     int           // 最大打开连接数
}
```

## 功能模块

### 1. 知识库 (Knowledge Repository)

管理文档知识的存储、检索。

**代码位置**: `internal/storage/postgres/repositories/knowledge_repository.go`

**核心功能:**
- 文档分块存储
- 向量相似度检索
- 批量导入
- 时间衰减

**接口:**
```go
type KnowledgeRepository struct {
    db     DBTX
    dbPool *sql.DB
}

// 创建知识块
func (r *KnowledgeRepository) Create(ctx context.Context, chunk *KnowledgeChunk) error

// 批量创建
func (r *KnowledgeRepository) CreateBatch(ctx context.Context, chunks []*KnowledgeChunk) error

// 根据ID查询
func (r *KnowledgeRepository) GetByID(ctx context.Context, id string) (*KnowledgeChunk, error)

// 向量检索
func (r *KnowledgeRepository) SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*KnowledgeChunk, error)

// 按文档列出所有块
func (r *KnowledgeRepository) ListByDocument(ctx context.Context, documentID, tenantID string) ([]*KnowledgeChunk, error)

// 更新嵌入向量
func (r *KnowledgeRepository) UpdateEmbedding(ctx context.Context, id string, embedding []float64, model string, version int) error

// 删除
func (r *KnowledgeRepository) Delete(ctx context.Context, id string) error
```

**数据模型:**
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

### 2. 会话存储 (Session Repository)

管理用户会话和消息历史。

**代码位置**: `internal/storage/postgres/repositories/session_repository.go`

**接口:**
```go
type SessionRepository struct {
    db DBTX
}

// 创建会话
func (r *SessionRepository) Create(ctx context.Context, session *Session) error

// 获取会话
func (r *SessionRepository) Get(ctx context.Context, id string) (*Session, error)

// 删除会话
func (r *SessionRepository) Delete(ctx context.Context, id string) error

// 添加消息
func (r *SessionRepository) AddMessage(ctx context.Context, message *Message) error

// 获取消息
func (r *SessionRepository) GetMessages(ctx context.Context, sessionID string, limit int) ([]*Message, error)
```

**数据模型:**
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

## 使用指南

### 基础使用

#### 1. 初始化连接池

**代码位置**: `internal/storage/postgres/pool.go:30-50`

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

#### 2. 使用连接池（推荐模式）

**代码位置**: `internal/storage/postgres/pool.go:70-90`

```go
// 使用 WithConnection 模式
err := pool.WithConnection(ctx, func(conn *sql.Conn) error {
    var result int
    return conn.QueryRowContext(ctx, "SELECT 1").Scan(&result)
})

if err != nil {
    slog.Error("Query failed", "error", err)
}
```

#### 3. 查询数据

**代码位置**: `internal/storage/postgres/pool.go:100-120`

```go
// 查询单行
row := pool.QueryRow(ctx, "SELECT id, content FROM knowledge_chunks WHERE id = $1", chunkID)

var chunk KnowledgeChunk
err := row.Scan(&chunk.ID, &chunk.Content)
if err != nil {
    slog.Error("Failed to scan row", "error", err)
}
```

#### 4. 执行查询

**代码位置**: `internal/storage/postgres/pool.go:130-150`

```go
// 执行插入
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

#### 5. 使用事务

**代码位置**: `internal/storage/postgres/pool.go:160-180`

```go
// 开始事务
tx, err := pool.Begin(ctx)
if err != nil {
    return err
}

// 执行多个操作
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

// 提交事务
if err := tx.Commit(); err != nil {
    return err
}
```

### 高级使用

#### 1. 连接池监控

**代码位置**: `internal/storage/postgres/pool.go:190-210`

```go
// 获取连接池统计信息
stats := pool.Stats()

slog.Info("Pool stats",
    "open_connections", stats.OpenConnections,
    "in_use", stats.InUseConnections,
    "idle", stats.IdleConnections,
    "wait_count", stats.WaitCount,
    "wait_duration", stats.WaitDuration,
)

// 检查健康状态
if !pool.IsHealthy() {
    slog.Warn("Pool is not healthy")
}
```

#### 2. 错误处理

**代码位置**: `internal/storage/postgres/pool.go:220-240`

```go
// 处理连接错误
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

#### 3. 批量操作

**代码位置**: `internal/storage/postgres/repositories/knowledge_repository.go:100-120`

```go
// 批量创建知识块
chunks := []*KnowledgeChunk{
    {Content: "内容1", ...},
    {Content: "内容2", ...},
    {Content: "内容3", ...},
}

err := kbRepo.CreateBatch(ctx, chunks)
if err != nil {
    slog.Error("Failed to create batch", "error", err)
}
```

## 最佳实践

### 1. 连接池配置

**代码位置**: `internal/storage/postgres/pool.go:50-60`

```go
config := &postgres.Config{
    MaxOpenConns:    25,  // 根据并发需求调整
    MaxIdleConns:    10,  // 保持一定空闲连接
    ConnMaxLifetime: 5 * time.Minute,  // 定期刷新连接
    QueryTimeout:    30 * time.Second,  // 设置合理超时
}
```

### 2. 使用 WithConnection 模式

**代码位置**: `internal/storage/postgres/pool.go:70-90`

```go
// 推荐模式：自动处理连接的获取和释放
err := pool.WithConnection(ctx, func(conn *sql.Conn) error {
    // 使用连接执行操作
    return doSomething(conn)
})
```

### 3. 错误处理

**代码位置**: `internal/storage/postgres/pool.go:220-240`

```go
// 检查错误类型
if errors.Is(err, context.DeadlineExceeded) {
    // 超时错误
    return ErrTimeout
}

if errors.Is(err, sql.ErrConnDone) {
    // 连接已关闭
    return ErrConnectionClosed
}
```

### 4. 上下文管理

**代码位置**: `internal/storage/postgres/pool.go:130-150`

```go
// 设置超时
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

// 执行查询
result, err := pool.Exec(ctx, query, args...)
```

## 性能优化

### 1. 连接池优化

- **MaxOpenConns**: 根据并发需求设置（默认25）
- **MaxIdleConns**: 保持一定空闲连接（默认10）
- **ConnMaxLifetime**: 定期刷新连接（默认5分钟）
- **QueryTimeout**: 设置合理超时（默认30秒）

### 2. 批量操作

- 使用批量插入接口
- 批量生成嵌入向量
- 批量更新操作

### 3. 索引优化

- 确保tenant_id有索引
- 确保embedding_status有索引
- 确保content_hash有索引

## 错误处理

### 错误类型

**代码位置**: `internal/core/errors/common.go`

```go
// 核心错误
var (
    ErrInvalidArgument   = errors.New("invalid argument")
    ErrRecordNotFound    = errors.New("record not found")
    ErrDuplicateKey      = errors.New("duplicate key")
    ErrNoTransaction     = errors.New("no transaction")
    ErrDBConnectionFailed = errors.New("database connection failed")
)
```

### 错误处理示例

**代码位置**: `internal/storage/postgres/pool.go:220-240`

```go
// 检查错误类型
if errors.Is(err, context.DeadlineExceeded) {
    // 超时错误
    return ErrTimeout
}

if errors.Is(err, sql.ErrConnDone) {
    // 连接已关闭
    return ErrConnectionClosed
}
```

## 监控与日志

### 1. 连接池监控

**代码位置**: `internal/storage/postgres/pool.go:190-210`

```go
// 定期记录连接池统计信息
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

### 2. 查询性能监控

```go
// 记录查询时间
start := time.Now()
result, err := pool.Query(ctx, query, args...)
duration := time.Since(start)

slog.Info("Query executed",
    "duration", duration,
    "query", query,
)
```

## 参考资料

- [PostgreSQL文档](https://www.postgresql.org/docs/15/)
- [pgvector文档](https://github.com/pgvector/pgvector)
- [Go database/sql](https://pkg.go.dev/database/sql)

---

**版本**: 1.0  
**最后更新**: 2026-03-24  
**维护者**: GoAgent Team