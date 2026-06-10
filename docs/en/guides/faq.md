# Frequently Asked Questions (FAQ)

This document collects common issues and solutions when using go-agent.

## Installation Issues

### Q1: go mod download fails?

**Symptoms**:
```
go: downloading goagent/api v0.0.0
go: module goagent/api: Get "https://proxy.golang.org/goagent/api/@v/list": dial tcp: lookup proxy.golang.org
```

**Solution**:
```bash
# Use Go China proxy
export GOPROXY=https://goproxy.cn,direct
go mod download
```

**Code Location**: `go.mod` (Dependency management)

---

### Q2: PostgreSQL connection fails?

**Symptoms**:
```
failed to connect to database: connection refused
```

**Solution**:

1. Check if PostgreSQL is running:
```bash
# macOS/Linux
pg_ctl status

# Docker
docker ps | grep postgres
```

2. Check port configuration:
```bash
# Check PostgreSQL port
netstat -an | grep 5432

# Docker default is 5433
netstat -an | grep 5433
```

3. Check configuration file:
**Code Location**: `examples/knowledge-base/config.yaml:5-10`
```yaml
database:
  host: localhost
  port: 5433  # Make sure port is correct
  user: postgres
  password: postgres
  database: goagent
```

4. Check pgvector extension:
```bash
psql -d goagent -c "SELECT extname FROM pg_extension WHERE extname='vector';"
# Should return: vector
```

**Code Location**: `internal/storage/postgres/pool.go:35-50` (Connection pool initialization)

---

### Q3: pgvector extension installation fails?

**Symptoms**:
```
ERROR: could not open extension control file: "vector": No such file or directory
```

**Solution**:

1. Install pgvector extension:
```bash
# Download corresponding version
wget https://github.com/pgvector/pgvector/archive/refs/tags/v0.5.0.tar.gz
tar -xzf v0.5.0.tar.gz
cd pgvector-0.5.0

# Compile and install
make
make install
```

2. Enable extension:
```bash
psql -d goagent -c "CREATE EXTENSION vector;"
```

**Code Location**: `internal/storage/postgres/migrate.go:50-100` (Database migration)

---

## Configuration Issues

### Q4: How to configure database connection?

**Solution**:

Edit configuration file `examples/knowledge-base/config.yaml`:

**Code Location**: `examples/knowledge-base/config.yaml:5-10`
```yaml
database:
  host: localhost        # Database host
  port: 5433            # Database port
  user: postgres        # Username
  password: postgres    # Password
  database: goagent     # Database name
```

**Code Location**: `internal/storage/postgres/pool.go:35-50` (Connection pool initialization)

---

### Q5: How to configure LLM provider?

**Solution**:

The following LLM providers are supported:

1. **OpenRouter** (default):
**Code Location**: `examples/knowledge-base/config.yaml:15-20`
```yaml
llm:
  provider: openrouter
  api_key: your-api-key
  base_url: https://openrouter.ai/api/v1
  model: meta-llama/llama-3.1-8b-instruct
```

2. **Ollama** (local):
```yaml
llm:
  provider: ollama
  base_url: http://localhost:11434
  model: llama3.2
```

**Code Location**: `internal/llm/client.go:80-100` (LLM client)

---

### Q6: How to configure memory distillation?

**Solution**:

Edit configuration file `examples/knowledge-base/config.yaml`:

**Code Location**: `examples/knowledge-base/config.yaml:25-30`
```yaml
memory:
  enabled: true
  enable_distillation: true
  distillation_threshold: 3  # Trigger distillation every 3 rounds
```

**Code Location**: `examples/knowledge-base/main.go:750-760` (Distillation trigger logic)

---

## Runtime Issues

### Q7: Agent startup fails?

**Symptoms**:
```
Failed to create knowledge base: create database pool: failed to ping database
```

**Solution**:

1. Check database connection (see Q2)
2. Check if database is created:
```bash
psql -l | grep goagent
```
3. Check if tables are migrated:
```bash
psql -d goagent -c "\dt"
# Should see: knowledge_chunks_1024, distilled_memories, etc.
```

**Code Location**: `internal/storage/postgres/pool.go:35-50` (Connection pool initialization)

---

### Q8: LLM call timeout?

**Symptoms**:
```
LLM generation failed: context deadline exceeded
```

**Solution**:

1. Increase timeout:
**Code Location**: `examples/knowledge-base/config.yaml:18`
```yaml
llm:
  timeout: 120  # Increase to 120 seconds
```

2. Check if LLM service is available:
```bash
# Test Ollama
curl http://localhost:11434/api/generate

# Test OpenRouter
curl -H "Authorization: Bearer your-api-key" \
  https://openrouter.ai/api/v1/models
```

**Code Location**: `internal/llm/client.go:120-140` (Timeout configuration)

---

### Q9: Vector search returns empty results?

**Symptoms**:
```
Search returned 0 results
```

**Solution**:

1. Confirm knowledge base is imported:
```bash
cd examples/knowledge-base
go run main.go --list
```

2. Check vector generation:
**Code Location**: `internal/storage/postgres/embedding/client.go:50-70`
```bash
# Check embedding service
curl http://localhost:11434/api/embeddings
```

3. Check pgvector configuration:
```bash
psql -d goagent -c "SELECT extversion FROM pg_extension WHERE extname='vector';"
```

**Code Location**: `internal/storage/postgres/repositories/knowledge_repository.go:100-120` (Vector search)

---

### Q10: Memory distillation not working?

**Symptoms**:
```
Memory distillation skipped
```

**Solution**:

1. Check configuration:
**Code Location**: `examples/knowledge-base/config.yaml:25-30`
```yaml
memory:
  enable_distillation: true
  distillation_threshold: 3
```

2. Check conversation rounds:
- Distillation triggers every N rounds (default 3 rounds)
- At least 3 rounds of conversation are needed

3. Check logs:
```bash
# Check distillation logs
grep "Memory Distillation" run.log
```

**Code Location**: `examples/knowledge-base/main.go:750-760` (Distillation trigger logic)

---

## Performance Issues

### Q11: Database connection pool exhausted?

**Symptoms**:
```
failed to get connection: connection pool exhausted
```

**Solution**:

1. Adjust connection pool configuration:
**Code Location**: `internal/storage/postgres/pool.go:50-60`
```yaml
database:
  max_open_conns: 25    # Increase max open connections
  max_idle_conns: 10    # Increase max idle connections
```

2. Use connection pool pattern:
**Code Location**: `internal/storage/postgres/pool.go:70-90`
```go
// Use WithConnection pattern
pool.WithConnection(ctx, func(conn *sql.Conn) error {
    // Use connection
    return nil
})
```

**Code Location**: `internal/storage/postgres/pool.go:70-90` (Connection pool management)

---

### Q12: Vector search slow?

**Symptoms**:
```
Vector search query took 5s
```

**Solution**:

1. Create vector index:
```sql
CREATE INDEX ON knowledge_chunks_1024 USING ivfflat (embedding vector_cosine_ops);
```

2. Adjust search parameters:
**Code Location**: `examples/knowledge-base/config.yaml:35-40`
```yaml
knowledge:
  top_k: 10          # Reduce number of results
  min_score: 0.6     # Increase minimum similarity threshold
```

**Code Location**: `internal/storage/postgres/repositories/knowledge_repository.go:100-120` (Vector search)

---

## Error Handling

### Q13: How to view detailed error logs?

**Solution**:

1. Enable debug logging:
**Code Location**: `examples/knowledge-base/main.go:20-30`
```go
slog.SetLogLoggerLevel(slog.LevelDebug)
```

2. Check log file:
```bash
# View run log
cat run.log

# Real-time view
tail -f run.log
```

**Code Location**: `examples/knowledge-base/main.go:20-30` (Log configuration)

---

### Q14: How to handle task failures?

**Solution**:

1. Check task status:
**Code Location**: `internal/agents/leader/agent.go:200-220`
```go
result, err := agent.Process(ctx, input)
if err != nil {
    slog.Error("Task failed", "error", err)
    // Handle error
}
```

2. Use dead letter queue (DLQ):
**Code Location**: `internal/protocol/ahp/dlq.go:30-50`
```go
dlq := protocol.GetDLQ()
dlq.Add(msg, err, reason)
```

**Code Location**: `internal/protocol/ahp/dlq.go:30-50` (Dead letter queue)

---

## Other Issues

### Q15: How to upgrade go-agent?

**Solution**:

```bash
# Pull latest code
git pull origin main

# Update dependencies
go mod tidy

# Rebuild
go build ./...
```

---

### Q16: How to contribute?

**Solution**:

1. Fork the project
2. Create a branch
3. Submit a PR
4. Wait for review

---

## Get More Help

- Read [Architecture Documentation](arch_en.md)
- Read [Quick Start Guide](quick_start_en.md)
- Submit [Issue](https://github.com/yourusername/goagent/issues)

---

**Last Updated**: 2026-03-23  
**Version**: v1.0.0  
**Code Base**: Based on actual go-agent code analysis