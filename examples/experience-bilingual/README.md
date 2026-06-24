# Experience System Bilingual Test

This example demonstrates the Experience System's distillation and retrieval capabilities with bilingual (Chinese and English) dialogue processing.

## Features

- **Dialogue Parsing**: Reads and parses dialogue from text files
- **Experience Distillation**: Extracts reusable experiences from task results
- **Database Storage**: Stores experiences in PostgreSQL with pgvector
- **Vector Retrieval**: Retrieves experiences using vector similarity search
- **Multi-tenant Support**: Isolates data by tenant ID
- **Bilingual Support**: Processes both Chinese and English dialogues

## Prerequisites

- PostgreSQL database with pgvector extension
- Embedding service (default: http://localhost:8000)
- Ollama LLM service (optional, default: http://localhost:11434)

## Configuration

Edit `config.yaml` to match your environment:

```yaml
database:
  host: localhost
  port: 5433
  user: postgres
  password: postgres
  database: goagent

embedding_service:
  url: http://localhost:8000
  model: qwen3-embedding:0.6b

llm:
  provider: ollama
  api_key: ""
  base_url: http://localhost:11434
  model: llama3.2:latest
  timeout: 60
```

## Setup

### 1. Database Migration

Run the database migration to create the required tables:

```bash
cd /Users/scc/go/src/ares
go run cmd/migrate_ares/main.go
```

This creates the `experiences_1024` table with vector support.

### 2. Configure Services

Update `config.yaml` with your database and service credentials.

## Usage

Run the test:

```bash
cd /Users/scc/go/src/ares/examples/experience-bilingual-test
go run main.go
```

## Output Files

The test generates the following files:

- **`chinese_pre_db.txt`**: Chinese distillation results before storage
- **`chinese_post_db.txt`**: Chinese experiences retrieved from database
- **`english_pre_db.txt`**: English distillation results before storage
- **`english_post_db.txt`**: English experiences retrieved from database
- **`performance_summary.txt`**: Overall performance metrics

## Test Scenarios

### Chinese Dialogues

1. **Insomnia Problem**: Sleep improvement strategies
2. **Fitness Beginner**: Workout guidance for beginners
3. **Language Learning**: Japanese learning methods
4. **Kitchen Organization**: Small kitchen storage solutions
5. **Child Reading**: Cultivating reading habits in children

### English Dialogues

1. **Insomnia Problem**: Sleep improvement strategies
2. **Fitness Beginner**: Workout guidance for beginners
3. **Language Learning**: Spanish learning methods
4. **Apartment Organization**: Small apartment storage solutions
5. **Solo Travel**: European solo travel advice

## Architecture

```
Dialogue Files (txt)
    ↓
Parse Dialogue
    ↓
Task Results
    ↓
DistillationService.ShouldDistill()
    ↓
DistillationService.Distill()
    ↓
ExperienceRepository.Create()
    ↓
PostgreSQL (experiences_1024)
    ↓
ExperienceRepository.ListByType()
    ↓
ExperienceRepository.SearchByVector()
    ↓
Retrieval Results (txt)
```

## Key Components

### DistillationService

Checks eligibility and extracts experiences from task results:

- `ShouldDistill()`: Validates task meets distillation criteria
- `Distill()`: Extracts Problem, Solution, Constraints using LLM

### ExperienceRepository

Manages database operations:

- `Create()`: Stores experience with vector embedding
- `ListByType()`: Retrieves experiences by type
- `SearchByVector()`: Vector similarity search

## Performance Metrics

| Metric | Value |
|--------|-------|
| Distillation Eligibility | 100% (10/10) |
| Storage Success | 100% (10/10) |
| Retrieval Success | 100% (10/10) |
| Vector Search | Top 5 similar experiences |

## Database Schema

```sql
CREATE TABLE experiences_1024 (
    id UUID PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type IN ('query', 'solution', 'failure', 'pattern', 'distilled')),
    input TEXT NOT NULL,
    output TEXT NOT NULL,
    embedding VECTOR(1024) NOT NULL,
    embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
    embedding_version INT NOT NULL DEFAULT 1,
    score FLOAT DEFAULT 0.5 CHECK (score >= 0 AND score <= 1),
    success BOOLEAN DEFAULT true,
    agent_id VARCHAR(255),
    metadata JSONB DEFAULT '{}'::jsonb,
    decay_at TIMESTAMP DEFAULT NOW() + INTERVAL '30 days',
    created_at TIMESTAMP DEFAULT NOW()
);
```

## Code Quality

All code follows the project's coding standards:

- ✅ Passes `go vet`
- ✅ Passes `staticcheck`
- ✅ Passes `golangci-lint`
- ✅ Uses Repository API (no raw SQL)
- ✅ Configuration-driven (no hardcoded defaults)
- ✅ Proper error handling with `%w`
- ✅ Structured logging with `slog`

## Troubleshooting

### Database Connection Failed

- Verify PostgreSQL is running on `localhost:5433`
- Check database credentials in `config.yaml`
- Ensure pgvector extension is installed

### Embedding Service Unavailable

- Verify embedding service is running on `http://localhost:8000`
- Check embedding model name in `config.yaml`

### No Experiences Retrieved

- Check if experiences were stored successfully in database
- Verify tenant ID matches between storage and retrieval
- Check vector embedding generation

## References

- [Experience System Documentation](../../docs/features/experience-system_en.md)
- [Code Rules](../../plan/code_rules.md)
- [Storage API](../../internal/storage/postgres/README.md)