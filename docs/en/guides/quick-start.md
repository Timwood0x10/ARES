# Quick Start Guide

This guide helps you run your first go-agent example within 10 minutes.

## Prerequisites

### Required Components

- **Go 1.21+**
  ```bash
  go version  # Check version
  ```

- **PostgreSQL 15+ with pgvector**
  ```bash
  # Install PostgreSQL
  # macOS: brew install postgresql@15
  # Ubuntu: apt install postgresql-15

  # Install pgvector extension
  # Download: https://github.com/pgvector/pgvector/releases
  ```

- **Ollama (or other LLM service)**
  ```bash
  # Install Ollama
  # macOS: brew install ollama
  # Linux: curl -fsSL https://ollama.com/install.sh | sh

  # Pull model
  ollama pull llama3.2
  ```

### Optional Components

- **Docker** (for quick PostgreSQL setup)
- **Redis** (for distributed caching, optional)

## Installation Steps

### 1. Clone the Project

```bash
git clone https://github.com/yourusername/go-agent.git
cd go-agent
```

### 2. Install Dependencies

```bash
go mod download
```

### 3. Configure Database

#### Option 1: Use Local PostgreSQL

```bash
# Create database
createdb goagent

# Start PostgreSQL
pg_ctl start

# Install pgvector extension
psql -d goagent -c "CREATE EXTENSION vector;"
```

#### Option 2: Use Docker (Recommended)

```bash
# Start PostgreSQL + pgvector
docker run -d \
  --name goagent-db \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=goagent \
  -p 5433:5432 \
  pgvector/pgvector:pg15

# Wait for database startup
sleep 5

# Verify connection
docker exec -it goagent-db psql -U postgres -d goagent -c "SELECT version();"
```

### 4. Configure Example

Edit `examples/knowledge-base/config.yaml`:

**Code Location**: `examples/knowledge-base/config.yaml`

```yaml
database:
  host: localhost
  port: 5433  # Default is 5433 when using Docker
  user: postgres
  password: postgres
  database: goagent

embedding_service_url: http://localhost:11434
embedding_model: nomic-embed-text

llm:
  provider: openrouter
  api_key: your-api-key  # Configure your API key
  base_url: https://openrouter.ai/api/v1
  model: meta-llama/llama-3.1-8b-instruct

memory:
  enabled: true
  enable_distillation: true
  distillation_threshold: 3
```

### 5. Import Knowledge Base

```bash
cd examples/knowledge-base

# Import sample document
go run main.go --save README.md
```

**Code Location**: `examples/knowledge-base/main.go:325-350` (ImportDocuments function)

Expected output:
```
Importing document: README.md
Document split into 5 chunks
Successfully imported 5/5 chunks
Document imported successfully. Document ID: xxx
```

## Running Examples

### Knowledge Base Q&A

```bash
cd examples/knowledge-base
go run main.go --chat
```

**Code Location**: `examples/knowledge-base/main.go:370-400` (StartChat function)

Expected output:
```
Chat mode. Enter your questions (type 'exit' to quit):
LLM enabled - Using RAG (Retrieval + Generation) mode
Memory enabled - Conversation history and distillation supported
Session created: session_xxx

You: what is go-agent?
```

### Travel Planning

```bash
cd examples/travel
go run main.go
```

**Code Location**: `examples/travel/main.go:30-120` (main function)

## Verify Installation

### Check Database Connection

```bash
# Connect to database
psql -h localhost -p 5433 -U postgres -d goagent

# List tables
\dt

# You should see these tables:
# - knowledge_chunks_1024
# - distilled_memories
# - conversations
# - task_results
```

**Code Location**: `internal/storage/postgres/migrate.go:50-100` (Database migration)

### Check Vector Search

```bash
# In knowledge-base example
cd examples/knowledge-base
go run main.go --list
```

**Code Location**: `examples/knowledge-base/main.go:410-430` (ListDocuments function)

Expected output:
```
Documents:
- ID: xxx, Source: README.md, Chunks: 5
```

## Common Issues

### Q: go mod download fails?

**A**: Use Go proxy:
```bash
export GOPROXY=https://goproxy.cn,direct
go mod download
```

### Q: PostgreSQL connection fails?

**A**: Check the following:
1. Is PostgreSQL running?
2. Is the port correct (Docker default is 5433)?
3. Are username and password correct?
4. Is pgvector extension installed?

**Code Location**: `internal/storage/postgres/pool.go:35-50` (Connection pool initialization)

### Q: Ollama connection fails?

**A**: Check if Ollama is running:
```bash
# Check Ollama status
ollama list

# Test model
ollama run llama3.2 "hello"
```

**Code Location**: `internal/llm/client.go:80-100` (LLM client)

### Q: LLM call timeout?

**A**: Check timeout configuration in config, increase timeout:
```yaml
llm:
  timeout: 120  # Increase to 120 seconds
```

**Code Location**: `internal/llm/client.go:120-140` (Timeout configuration)

## Next Steps

- Read [Architecture Documentation](arch.md) to understand system design
- Read [Integration Guide](integration_guide_en.md) to learn how to integrate into existing projects
- Check [Example Code](../examples/) to learn more usage

## Get Help

- Check [FAQ](faq_en.md)
- Submit [Issue](https://github.com/yourusername/goagent/issues)

---

**Last Updated**: 2026-03-23  
**Version**: v1.0.0  
**Code Base**: Based on actual go-agent code analysis