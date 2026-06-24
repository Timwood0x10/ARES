# Local Knowledge Base Example

This is a local knowledge base example based on the ares storage module. It demonstrates how to quickly build a fully functional document retrieval and Q&A system using the high-level APIs of the storage module.

## Tech Stack and Components

### Technologies Used
- **Language**: Go 1.26+
- **Database**: PostgreSQL 16 + pgvector extension
- **Embedding Service**: Ollama (qwen3-embedding:0.6b) or Custom Python Service
- **LLM**: Ollama (llama3.2:latest) for answer generation
- **Configuration Format**: YAML
- **Retrieval Algorithms**: 
  - Vector similarity search (pgvector)
  - BM25 full-text search
  - Hybrid retrieval (Vector + BM25)
  - Precision Mode (Exact Match → Keyword → Vector)
  - Smart RAG Detection
- **Cache**: Redis (optional)

### Core Components Used

| Component | Purpose | Code Location |
|-----------|---------|---------------|
| **KnowledgeBase API** | High-level knowledge base interface | `api/service/knowledge/service.go` |
| **KnowledgeRepository** | Knowledge base data access | `internal/storage/postgres/repositories/knowledge_repository.go` |
| **RetrievalService** | Intelligent retrieval service | `api/retrieval/service.go` |
| **EmbeddingClient** | Vector embedding client | `internal/storage/postgres/embedding/client.go` |
| **LLM Client** | LLM interaction client | `internal/llm/client.go` |
| **MemoryManager** | Memory management and distillation | `internal/memory/production_manager.go` |
| **TenantGuard** | Tenant isolation | `internal/storage/postgres/tenant_guard.go` |
| **RetrievalGuard** | Retrieval rate limiting | `internal/storage/postgres/retrieval_guard.go` |

### Key Feature Implementations

**Code Location References**:
- Document import and chunking: `examples/knowledge-base/main.go:150-200`
- Vector storage: `internal/storage/postgres/repositories/knowledge_repository.go:80-120`
- Hybrid retrieval: `api/retrieval/service.go:100-150`
- RAG detection: `examples/knowledge-base/main.go:250-300`
- Memory distillation: `internal/memory/distillation/service.go:50-100`
- Intent detection: `examples/knowledge-base/main.go:350-400`

## Features

### Core Features
- 📄 **Document Import**: Import text documents with automatic chunking, vectorization, and storage
- 🔍 **Intelligent Retrieval**: Hybrid search combining vector retrieval and BM25 full-text search
- 💬 **Interactive Q&A**: Command-line interactive knowledge Q&A
- 📊 **Document Management**: List and delete imported documents
- 🏢 **Multi-Tenant Isolation**: Support for multiple independent tenant spaces
- ⚡ **High Performance**: Efficient vector retrieval based on pgvector

### Advanced Features
- 🎯 **Precision Mode**: Automatic detection and handling of precise queries (short queries, special symbols like `=+-*/:`)
- 🤖 **Complete RAG Pipeline**: Retrieval → Generation → Verification with local LLM (Ollama)
- 🧠 **Memory System**: Conversation history tracking with session management
- 💾 **Memory Distillation**: Automatic extraction and storage of conversation knowledge after reaching threshold
- 🔬 **Fact Checking**: Correct user misconceptions with factual information from knowledge base
- 🎨 **Smart RAG Detection**: Automatically determine if RAG is needed for each query
- 🏠 **Local LLM Integration**: Full local setup with Ollama (llama3.2:latest) for privacy and speed
- 📝 **Knowledge Correction**: Detect correction requests (Chinese: "纠正", "改正", "修正") and search for relevant content to update
- 👤 **Self-Introduction Detection**: Identify user introductions (Chinese: "我是XXX", "我叫XXX") and store user profile
- 💭 **Cross-Session Memory**: Retrieve user preferences and profile from distilled memories in new conversations

## System Requirements

### Required Components

1. **PostgreSQL 16 + pgvector extension**
   ```bash
   # Start PostgreSQL with Docker
   docker run -d \
     --name postgres-pgvector \
     -p 5433:5432 \
     -e POSTGRES_PASSWORD=postgres \
     -e POSTGRES_DB=goagent \
     pgvector/pgvector:pg16
   ```

2. **Ollama service** (for both embedding and LLM)
   ```bash
   # Install Ollama
   curl -fsSL https://ollama.com/install.sh | sh

   # Pull embedding model
   ollama pull qwen3-embedding:0.6b

   # Pull LLM model for answer generation
   ollama pull llama3.2:latest

   # Start Ollama service
   ollama serve
   ```

3. **Embedding service** (optional, can use Ollama directly)
   ```bash
   cd services/embedding
   
   ./start.sh
   ```

### Verify Installation

```bash
# Check PostgreSQL
docker exec -it postgres-pgvector psql -U postgres -d goagent -c "SELECT * FROM pg_extension WHERE extname='vector';"

# Check Ollama
curl http://localhost:11434/api/tags
```

## Quick Start

### Prerequisites

1. **PostgreSQL + pgvector running**
   ```bash
   docker run -d \
     --name postgres-pgvector \
     -p 5433:5432 \
     -e POSTGRES_PASSWORD=postgres \
     -e POSTGRES_DB=goagent \
     pgvector/pgvector:pg16
   ```

2. **Ollama running with required models**
   ```bash
   # Start Ollama
   ollama serve
   
   # Pull models (in another terminal)
   ollama pull qwen3-embedding:0.6b  # For embedding
   ollama pull llama3.2:latest        # For answer generation
   ```

3. **Embedding service running** (optional, can use Ollama directly)
   ```bash
   cd services/embedding
   PORT=8000 python3.14 app.py
   ```

### One-Click Startup

```bash
# 1. Start embedding service
cd services/embedding
./start.sh

# 2. Import document
cd ../../examples/knowledge-base
go run main.go --save ../../plan/code_rules.md

# 3. Start interactive Q&A
go run main.go --chat
```

### Detailed Setup

#### 1. Configure Database

Ensure PostgreSQL is running and properly configured:

```bash
# Check database connection
docker exec -it postgres-pgvector psql -U postgres -d goagent -c "SELECT version();"
```

### 2. Configure Application

Edit `config.yaml` file to confirm database, embedding service, and LLM configuration:

```yaml
database:
  host: localhost
  port: 5433
  user: postgres
  password: postgres
  database: goagent

embedding_service_url: http://localhost:8000
embedding_model: qwen3-embedding:0.6b

# LLM Configuration for answer generation
llm:
  provider: ollama
  base_url: http://localhost:11434
  model: llama3.2:latest
  timeout: 120
  max_tokens: 2048

# Memory System Configuration
memory:
  enabled: true
  max_history: 10
  max_sessions: 100
  enable_distillation: true
  distillation_threshold: 3

# Knowledge Base Configuration
knowledge:
  chunk_size: 200
  chunk_overlap: 50
  top_k: 10
  min_score: 0.4
```

### 3. Import Documents

```bash
# Import a document
go run main.go --save README.md

# Import other documents
go run main.go --save ../../docs/storage/api.md
go run main.go --save ../../README.md
```

### 4. Start Q&A

```bash
# Start interactive Q&A
go run main.go --chat
```

Then enter your questions:

#### Example 1: General Conversation (No RAG)
```
You: Hello
Assistant: Hello! How can I help you today?

You: My name is Xiao Ming
Assistant: Hello Xiao Ming! Nice to meet you.
```

#### Example 2: Knowledge Retrieval with RAG
```
You: What is RAG?

Found 3 relevant results:

[1] Score: 0.892
Content: RAG (Retrieval-Augmented Generation) is an AI system architecture combining retrieval and generation...
Source: README.md

Assistant: RAG (Retrieval-Augmented Generation) is an AI system architecture that combines 
information retrieval with text generation. It works by first retrieving relevant documents from a 
knowledge base, then using those documents as context to generate more accurate and factual 
responses. [Document 1]
```

#### Example 3: Precision Mode (Exact Match)
```
You: chunk_size = 200

Precision mode triggered
Retrieving exact match...

Assistant: For your question about chunk_size, according to the knowledge base, the 
recommended chunk_size for optimal performance and semantic integrity is between 50-100 characters. 
This helps maintain context while ensuring precise matching. [Document 1]
```

#### Example 4: Fact Checking
```
You: Should Go language frequently use panic to handle errors?

Assistant: According to the provided knowledge base context, using panic to handle errors in 
Go is prohibited. panic() is only allowed for fatal startup failures, not for business logic. 
Therefore, Go requires using error interfaces to handle business logic errors instead of using 
panic. [Document 1]
```

#### Example 5: Memory Distillation
```
You: Hello
Assistant: Hello! How can I help you?

You: My name is Xiao Ming
Assistant: Hello Xiao Ming! Nice to meet you.

You: Do you remember my name?
🎯 [Memory Distillation] Conversation rounds reached threshold (3/3), triggering memory distillation...
🔄 [Memory Distillation] Starting distillation for session...
✅ [Memory Distillation] Distillation completed!

Assistant: Yes, I remember! Your name is Xiao Ming. [Based on distilled memory]
```

#### Example 6: Knowledge Correction
```
You: What is go-agent?

Assistant: GoAgent is a high-performance vector storage and retrieval system based on PostgreSQL + pgvector...

You: Correct it, go-agent is a universal AI agent development framework implemented in Go
🔧 [Knowledge Correction] Detected correction request

Assistant: I detected you want to correct knowledge. Found relevant results. Correction request recorded, please continue.
```

#### Example 7: Self-Introduction Detection
```
You: I am Xiao Ming, I like programming, I am good at rust and golang, I don't like python
👤 [Self-Introduction Detection] user_id=Xiao Ming

Assistant: Hello Xiao Ming! I have recorded your information.
```

#### Example 8: Cross-Session Memory Retrieval
```
# First conversation:
You: I am Xiao Ming, I am good at rust and golang
# ... distillation triggered ...

# New conversation:
You: I am Xiao Ming, what is my tech stack?
👤 [Self-Introduction Detection] user_id=Xiao Ming
💭 [Memory Retrieval] Loading user profile from distilled memories

Assistant: Based on your history, your tech stack includes Rust and Golang.
```

### 5. Manage Documents

```bash
# List all documents
go run main.go --list

# Output example:
# Documents:
#   - ID: 1234567890abcdef, Source: README.md, Chunks: 12
#   - ID: abcdef1234567890, Source: api.md, Chunks: 45

# Delete specified document
go run main.go --delete 1234567890abcdef
```

## Usage Guide

### Command Line Options

```bash
go run main.go [options]

Options:
  --save <path>     Import document to knowledge base
  --chat            Start interactive Q&A mode
  --list            List all imported documents
  --delete <id>     Delete specified document
  --tenant <id>     Specify tenant ID (default: default)
  --config <path>   Config file path (default: config.yaml)
```

### Configuration Options

#### Database Configuration
```yaml
database:
  host: localhost
  port: 5433
  user: postgres
  password: postgres
  database: goagent
```

#### Embedding Configuration
```yaml
embedding_service_url: http://localhost:8000
embedding_model: qwen3-embedding:0.6b
```

#### LLM Configuration
```yaml
llm:
  provider: ollama              # LLM provider (ollama, openrouter)
  base_url: http://localhost:11434
  model: llama3.2:latest       # LLM model for answer generation
  timeout: 120                  # LLM generation timeout (seconds)
  max_tokens: 2048              # Maximum tokens in LLM response
```

#### Memory System Configuration
```yaml
memory:
  enabled: true                  # Enable memory system
  max_history: 10               # Maximum conversation turns to keep
  max_sessions: 100              # Maximum sessions to store
  enable_distillation: true     # Enable automatic distillation
  distillation_threshold: 3     # Messages before triggering distillation
```

**Memory System Features:**
- Track conversation history for context
- Auto-distill after reaching threshold
- Store distilled memories in knowledge base
- Enable conversation continuity across sessions
- Detect user self-introductions and store profile
- Retrieve user preferences from distilled memories

**Intent Detection Features:**
- Knowledge correction: Detect correction keywords (Chinese: "纠正", "改正", "修正", "不对", "不是")
- Self-introduction: Detect introduction patterns (Chinese: "我是XXX", "我叫XXX")
- Load user profile from distilled memories in new conversations

#### Knowledge Base Configuration
```yaml
knowledge:
  chunk_size: 200              # Document chunk size (characters)
  chunk_overlap: 50            # Chunk overlap size (characters)
  top_k: 10                     # Number of retrieval results
  min_score: 0.4                # Minimum similarity threshold
```

### Multi-Tenant Usage

```bash
# Create independent knowledge base spaces for different users/projects
go run main.go --save user1_doc.pdf --tenant user1
go run main.go --save user2_doc.pdf --tenant user2

# Each tenant can only see their own documents
go run main.go --list --tenant user1
go run main.go --chat --tenant user2
```

### Fact Checking

The system can automatically detect and correct user misconceptions:

```bash
# Start chat mode
go run main.go --chat

# Example:
You: Should Go language frequently use panic to handle errors?

# System will:
# 1. Detect the incorrect assumption
# 2. Retrieve factual information from knowledge base
# 3. Generate corrected answer with facts

Assistant: According to the provided knowledge base context, using panic to handle 
errors in Go is prohibited. panic() is only allowed for fatal startup failures, not for 
business logic. Therefore, Go requires using error interfaces to handle business logic errors 
instead of using panic.
```

### Batch Import

```bash
# Batch import multiple documents
for file in docs/*.md; do
  go run main.go --save "$file" --tenant default
done
```

### Check Distilled Memories

```bash
# Run Go-based distillation checker
go run cmd/check_distillation/main.go

# Or build and run
go build -o check_distillation cmd/check_distillation/main.go
./check_distillation
```

## How It Works

### Import Flow

```
Document Read → Intelligent Chunking → Generate Embedding Vectors → Store in PostgreSQL + pgvector
```

1. **Document Read**: Read document content
2. **Intelligent Chunking**: Split into chunks based on configured size and overlap
3. **Generate Embedding**: Generate 1024-dimensional vectors for each chunk using embedding service
4. **Vector Storage**: Store in PostgreSQL pgvector table

### Retrieval Flow (Complete RAG Pipeline)

```
User Question → RAG Detection → Retrieval (Precision/Recall Mode) → LLM Generation → Fact Checking → Answer
```

1. **RAG Detection**: Use LLM to determine if the question needs knowledge base search
   - Needs RAG: Technical questions, documentation queries, fact-based questions
   - No RAG: General conversation, greetings, personal information

2. **Precision Mode** (for short queries or special symbols):
   - Exact Match → Keyword Search → Vector Search (early return)
   - No multi-query, no score dilution

3. **Recall Mode** (for complex queries):
   - Multi-query generation (original + rewrites)
   - Hybrid retrieval (vector + keyword)
   - Result ranking and reranking

4. **LLM Generation**: Use local LLM to generate natural language answers based on retrieved context
   - Include conversation history for context
   - Fact checking to correct user misconceptions

5. **Memory Management**:
   - Track conversation history
   - Auto-distill after reaching threshold (default: 3 rounds)
   - Store distilled memories in knowledge base for future retrieval

### Memory Distillation Flow

```
Conversation History → Threshold Check → Extract Key Information → Generate Embedding → Store in Knowledge Base
```

1. **Conversation Tracking**: Store each message in session memory
2. **Threshold Check**: Monitor message count (configurable, default: 3)
3. **Distillation Trigger**: When threshold reached, extract conversation summary
4. **Vector Generation**: Generate embedding for distilled memory
5. **Knowledge Storage**: Store in knowledge base for future retrieval

### Intent Detection Flow

```
User Input → Intent Analysis → Route to Handler → Execute Action → Return Response
```

1. **Intent Analysis**: Detect user intent types
   - Knowledge correction: Detect correction keywords (Chinese: "纠正", "改正", "修正", "不对", "不是")
   - Self-introduction: Detect introduction patterns (Chinese: "我是XXX", "我叫XXX")
   - Regular question: Default handling

2. **Route to Handler**:
   - Correction: Search knowledge base → Record correction request
   - Self-introduction: Extract user ID → Load profile from distilled memories
   - Regular: Execute standard RAG pipeline

3. **Execute Action**: Perform appropriate action based on intent
4. **Return Response**: Provide appropriate response to user

## Advanced Usage

### Precision Mode Examples

Precision mode automatically triggers for short queries or queries containing special symbols:

```bash
# Start chat mode
go run main.go --chat

# Precision mode examples:
You: chunk_size = 200
# → Uses Exact Match → Keyword → Vector pipeline

You: a = x
# → Uses Exact Match → Keyword → Vector pipeline

You: timeout > 0
# → Uses Exact Match → Keyword → Vector pipeline

You: What are Go coding standards?
# → Uses Recall mode with RAG
```

### Memory Distillation

The system automatically distills conversation history after reaching the threshold:

```bash
# Start chat mode
go run main.go --chat

# Example conversation:
You: Hello
Assistant: Hello! How can I help you?

You: My name is Xiao Ming
Assistant: Hello Xiao Ming! Nice to meet you.

You: Do you remember my name?
# → Triggers memory distillation (3rd message)
# → Stores conversation summary in knowledge base
# → Can be retrieved in future conversations

# Check distilled memories:
go run cmd/check_distillation/main.go
```

### Configuration Tuning

#### Knowledge Base Parameters

```yaml
knowledge:
  chunk_size: 500          # Smaller chunks improve precision
  chunk_overlap: 50        # Maintain context continuity
  top_k: 5                 # Return more candidate results
  min_score: 0.6           # Raise similarity threshold
```

#### Memory System Parameters

```yaml
memory:
  enabled: true              # Enable memory system
  max_history: 10           # Maximum conversation turns to keep
  enable_distillation: true  # Enable automatic distillation
  distillation_threshold: 3  # Messages before triggering distillation
```

**Parameter Description:**

- `chunk_size`: Document chunk size
  - Small values (200-300): More precise, but may lose context
  - Medium values (500-700): Balance precision and context (recommended)
  - Large values (1000+): More context, but lower precision

- `chunk_overlap`: Chunk overlap size
  - Usually set to 10-20% of chunk_size
  - Helps maintain semantic continuity

- `top_k`: Number of retrieval results
  - 3-5: Precise answers
  - 5-10: Comprehensive answers (recommended)
  - 10+: Broad exploration

- `min_score`: Minimum similarity threshold
  - 0.7-0.8: High relevance, fewer results
  - 0.6-0.7: Balance relevance and result count (recommended)
  - 0.5-0.6: More results, may include irrelevant content

- `distillation_threshold`: Messages before distillation
  - Lower values (2-3): More frequent distillation, less context per batch
  - Higher values (5-10): Less frequent distillation, more context per batch
  - Recommended: 3 for active conversations

## Architecture

### Core Components

```
KnowledgeBase (High-level API)
    ├── Pool (Database connection pool)
    ├── KnowledgeRepository (Knowledge base data access)
    ├── RetrievalService (Intelligent retrieval - SimpleRetrievalService)
    │   ├── Precision Mode (Exact Match → Keyword → Vector)
    │   └── Recall Mode (Multi-query + Hybrid retrieval)
    ├── EmbeddingClient (Embedding service)
    ├── LLMClient (Local LLM for answer generation)
    ├── MemoryManager (Conversation history and distillation)
    ├── TenantGuard (Tenant isolation)
    └── RetrievalGuard (Rate limiting circuit breaker)
```

### Data Flow

**Document Import:**
```
Document → Chunking → Embedding Vector → PostgreSQL + pgvector
```

**Knowledge Q&A (Complete RAG):**
```
Question → RAG Detection → Precision/Recall Mode → Retrieval → LLM Generation → Fact Checking → Answer
```

**Memory Management:**
```
Messages → Session Memory → Threshold Check → Distillation → Knowledge Base Storage
```

## Performance Optimization

### 1. Database Optimization

```sql
-- Create indexes
CREATE INDEX idx_knowledge_tenant_id ON knowledge_chunks_1024(tenant_id);
CREATE INDEX idx_knowledge_document_id ON knowledge_chunks_1024(document_id);
CREATE INDEX idx_knowledge_embedding_status ON knowledge_chunks_1024(embedding_status);
```

### 2. Cache Configuration

```yaml
embedding_service_url: http://localhost:11434
embedding_model: nomic-embed-text
```

### 3. Batch Processing

When importing a large number of documents, process in batches:

```bash
# Import 10 documents per batch
find docs/ -name "*.md" | head -n 10 | xargs -I {} go run main.go --save "{}"
```

## Troubleshooting

### Issue 1: Database Connection Failed

```
Error: create database pool: connection refused
```

**Solution:**
- Check if PostgreSQL is running: `docker ps | grep postgres`
- Check if port is correct: `netstat -an | grep 5433`
- Check database configuration in config file

### Issue 2: Embedding Service Unavailable

```
Error: Failed to embed chunk: connection refused
```

**Solution:**
- Check if Ollama is running: `ps aux | grep ollama`
- Check if model is downloaded: `ollama list`
- Restart Ollama service: `ollama serve`

### Issue 3: Import Timeout

```
Import timeout (5 minutes exceeded)
```

**Solution:**
- Check embedding service response speed
- Reduce document size or increase chunk count
- Check network connection
- Check which chunk timed out (logs show details)

### Issue 4: Search Timeout

```
Search timeout. Please try again.
```

**Solution:**
- Check database connection status
- Check if embedding service is normal
- Reduce search result count (lower top_k value)
- Check for high concurrent requests

### Issue 5: Program Freezes

**Symptoms**: Program unresponsive, cannot exit

**Prevention Measures:**
- All operations have timeout protection (import 5 minutes, search 30 seconds)
- Each chunk has independent timeout (60 seconds)
- Use Ctrl+C to interrupt program
- Non-blocking input (bufio.Scanner)

**Solution:**
- Press Ctrl+C to interrupt program
- Check if Ollama service is stuck: `curl http://localhost:11434/api/tags`
- Check if database is stuck: `docker exec -it postgres-pgvector psql -U postgres -d goagent -c "SELECT 1;"`
- Restart related services

### Issue 6: Poor Retrieval Results

**Solution:**
- Adjust `chunk_size` and `chunk_overlap`
- Lower `min_score` threshold
- Increase `top_k` value
- Try different embedding models

### Issue 8: pgvector Not Installed

```
Error: type "vector" does not exist
```

**Solution:**
```bash
# Install pgvector extension in PostgreSQL
docker exec -it postgres-pgvector psql -U postgres -d goagent -c "CREATE EXTENSION vector;"
```

### Issue 9: Memory Distillation Not Triggering

**Symptoms**: Conversation continues without distillation despite reaching threshold

**Solution:**
- Check memory configuration: `memory.enable_distillation: true`
- Check threshold value: `memory.distillation_threshold: 3`
- Check logs for distillation trigger: look for `🎯 [Memory Distillation]`
- Verify conversation message count matches threshold

### Issue 10: LLM Generation Failed

**Symptoms**: Error message "LLM generation failed, falling back to raw results"

**Solution:**
- Check Ollama LLM model is available: `ollama list`
- Verify LLM model name in config: `llm.model: llama3.2:latest`
- Check Ollama service is running: `curl http://localhost:11434/api/tags`
- Increase LLM timeout: `llm.timeout: 120`

### Issue 11: Fact Checking Not Working

**Symptoms**: System agrees with user's incorrect statements

**Solution:**
- Ensure LLM prompt includes fact-checking instructions
- Check retrieved documents contain correct information
- Verify RAG is triggered for the question (check logs)
- Try rephrasing the question to trigger RAG

### Issue 12: Precision Mode Not Triggering

**Symptoms**: Complex queries using vector search instead of exact match

**Solution:**
- Precision mode triggers for: `len(query) <= 10` OR contains `=+-*/:`
- Check logs for: `Using precision mode`
- For longer queries, system correctly uses Recall mode
- Try shorter queries or include special symbols

## Extension Features

### 1. Support More Document Formats

Integrate PDF, Word and other document parsing libraries:

```go
import "github.com/unidoc/unipdf/v3/extractor"

func loadPDF(path string) (string, error) {
    // PDF parsing logic
}
```

### 2. Add Document Metadata

Add more metadata to documents:

```go
type DocumentMetadata struct {
    Title       string    `json:"title"`
    Author      string    `json:"author"`
    CreatedAt   time.Time `json:"created_at"`
    Tags        []string  `json:"tags"`
    Category    string    `json:"category"`
}
```

### 3. Implement Document Version Management

Support version control and updates for documents:

```go
func (kb *KnowledgeBase) UpdateDocument(ctx context.Context, tenantID, docID string) error {
    // Delete old version
    kb.DeleteDocument(ctx, tenantID, docID)
    // Import new version
    kb.ImportDocuments(ctx, tenantID, docPath)
}
```

## Tech Stack

- **Language**: Go 1.26+
- **Database**: PostgreSQL 16 + pgvector
- **Embedding Service**: Ollama (qwen3-embedding:0.6b) or Custom Python Service
- **LLM**: Ollama (llama3.2:latest) for answer generation
- **Configuration**: YAML
- **Retrieval**: 
  - Vector similarity (pgvector)
  - BM25 full-text search
  - Precision Mode (Exact Match → Keyword → Vector)
  - Smart RAG Detection
- **Memory System**: Session-based conversation history with automatic distillation
- **Fact Checking**: Automatic detection and correction of user misconceptions

## References

- [Storage API Documentation](../../docs/storage/api.md)
- [Retrieval Strategy Guide](../../docs/retrieval-strategy.md)
- [Memory System Documentation](../../docs/memory/)
- [pgvector Documentation](https://github.com/pgvector/pgvector)
- [Ollama Documentation](https://github.com/ollama/ollama)
- [RAG Best Practices](https://docs.anthropic.com/claude/docs/retrieval-augmented-generation)
- [LLM Query Rewriting](../../docs/llm/llm_query_rewrite.md)

## License

MIT License

## Contributing

Issues and Pull Requests are welcome!