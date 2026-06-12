# Repositories Module API Documentation

## Overview

The Repositories module provides the Data Access Layer (DAL) for interacting with the PostgreSQL database. This module implements various Repository interfaces, offering CRUD operations and advanced query capabilities.

## Core Features

- **Database Abstraction**: Uses `DBTX` interface to support database connections and transactions
- **Vector Search**: Implements semantic search using pgvector extension
- **Full-Text Search**: Supports keyword search with BM25 ranking
- **Tenant Isolation**: All operations support multi-tenant isolation
- **Error Handling**: Unified error handling and return values
- **Transaction Support**: Supports atomic operations and batch processing

## Available Repositories

### 1. ConversationRepository

Conversation history data access layer, providing storage and retrieval of session messages.

#### Main Methods

| Method | Description |
|--------|-------------|
| `Create(ctx, conv)` | Create a new conversation message |
| `GetByID(ctx, id)` | Get conversation message by ID |
| `GetBySession(ctx, sessionID, tenantID, limit)` | Get all messages for a specific session |
| `DeleteBySession(ctx, sessionID, tenantID)` | Delete all messages for a specific session |
| `Delete(ctx, id)` | Delete a single conversation message |
| `GetByUser(ctx, userID, tenantID, limit)` | Get recent messages for a user |
| `GetByAgent(ctx, agentID, tenantID, limit)` | Get recent messages for an agent |
| `CleanupExpired(ctx)` | Clean up expired conversation messages |
| `UpdateExpiresAt(ctx, sessionID, tenantID, expiresAt)` | Update session expiration time |
| `CountBySession(ctx, sessionID, tenantID)` | Count messages in a session |
| `GetRecentSessions(ctx, tenantID, limit)` | Get list of recent sessions |

#### Usage Example

```go
repo := repositories.NewConversationRepository(db)

// Create conversation message
conv := &storage_models.Conversation{
    SessionID: "session-1",
    TenantID:  "tenant-1",
    UserID:    "user-1",
    AgentID:   "agent-1",
    Role:      "user",
    Content:   "Hello, how can I help you?",
    CreatedAt: time.Now(),
}
err := repo.Create(ctx, conv)

// Get session messages
messages, err := repo.GetBySession(ctx, "session-1", "tenant-1", 100)
```

### 2. TaskResultRepository

Task result data access layer, providing storage and retrieval of task execution results.

#### Main Methods

| Method | Description |
|--------|-------------|
| `Create(ctx, result)` | Create a new task result |
| `GetByID(ctx, id)` | Get task result by ID |
| `GetBySession(ctx, sessionID, tenantID, limit)` | Get task results for a specific session |
| `GetByAgent(ctx, agentID, tenantID, limit)` | Get task results for a specific agent |
| `GetByStatus(ctx, status, tenantID, limit)` | Get task results by status |
| `Update(ctx, result)` | Update task result |
| `Delete(ctx, id)` | Delete task result |
| `DeleteBySession(ctx, sessionID, tenantID)` | Delete all task results for a session |
| `SearchByVector(ctx, embedding, tenantID, limit)` | Vector similarity search |
| `SearchByKeyword(ctx, query, tenantID, limit)` | Keyword search |
| `GetStatsByAgent(ctx, agentID, tenantID)` | Get statistics for an agent |
| `GetStatsByTenant(ctx, tenantID)` | Get statistics for a tenant |

#### Usage Example

```go
repo := repositories.NewTaskResultRepository(db)

// Create task result
result := &storage_models.TaskResult{
    SessionID: "session-1",
    TenantID:  "tenant-1",
    TaskType:  "chat",
    AgentID:   "agent-1",
    Input:     map[string]interface{}{"query": "test"},
    Status:    "completed",
    CreatedAt: time.Now(),
}
err := repo.Create(ctx, result)
```

### 3. ToolRepository

Tool definition data access layer, providing storage and retrieval of tools with semantic search support.

#### Main Methods

| Method | Description |
|--------|-------------|
| `Create(ctx, tool)` | Create a new tool |
| `GetByID(ctx, id)` | Get tool by ID |
| `GetByName(ctx, name, tenantID)` | Get tool by name |
| `Update(ctx, tool)` | Update tool |
| `Delete(ctx, id)` | Delete tool |
| `SearchByVector(ctx, embedding, tenantID, limit)` | Vector similarity search |
| `SearchByKeyword(ctx, query, tenantID, limit)` | Keyword search |
| `ListAll(ctx, tenantID, limit)` | List all tools |
| `ListByAgentType(ctx, agentType, tenantID, limit)` | List tools by agent type |
| `ListByTags(ctx, tags, tenantID, limit)` | List tools by tags |
| `UpdateUsage(ctx, id, success)` | Update tool usage statistics |
| `UpdateEmbedding(ctx, id, embedding, model, version)` | Update tool embedding |

#### Usage Example

```go
repo := repositories.NewToolRepository(db)

// Create tool
tool := &storage_models.Tool{
    TenantID:         "tenant-1",
    Name:             "web_search",
    Description:      "Search the web for information",
    Embedding:        createTestEmbedding(),
    EmbeddingModel:   "e5-large",
    EmbeddingVersion: 1,
    Tags:             []string{"search", "web"},
    CreatedAt:        time.Now(),
}
err := repo.Create(ctx, tool)

// Vector search
similarTools, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-1", 10)
```

### 4. KnowledgeRepository

Knowledge base data access layer, providing storage and retrieval of knowledge chunks for RAG (Retrieval Augmented Generation).

#### Main Methods

| Method | Description |
|--------|-------------|
| `Create(ctx, chunk)` | Create a new knowledge chunk |
| `CreateBatch(ctx, chunks)` | Batch create knowledge chunks (with transaction) |
| `GetByID(ctx, id)` | Get knowledge chunk by ID |
| `Update(ctx, chunk)` | Update knowledge chunk |
| `Delete(ctx, id)` | Delete knowledge chunk |
| `SearchByVector(ctx, embedding, tenantID, limit)` | Vector similarity search |
| `SearchByKeyword(ctx, query, tenantID, limit)` | Keyword search (BM25) |
| `ListByDocument(ctx, documentID, tenantID)` | List all chunks for a document |
| `UpdateEmbedding(ctx, id, embedding, model, version)` | Update knowledge chunk embedding |
| `UpdateEmbeddingStatus(ctx, id, status, errorMsg)` | Update embedding processing status |
| `CleanupExpired(ctx, olderThan)` | Clean up expired knowledge chunks |

#### Usage Example

```go
repo := repositories.NewKnowledgeRepository(db, dbPool)

// Create knowledge chunk
chunk := &storage_models.KnowledgeChunk{
    TenantID:         "tenant-1",
    Content:          "This is a knowledge chunk about AI",
    Embedding:        createTestEmbedding(),
    EmbeddingModel:   "e5-large",
    EmbeddingVersion: 1,
    EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
    SourceType:       "document",
    DocumentID:       "doc-123",
    ContentHash:      "hash-abc",
    CreatedAt:        time.Now(),
}
err := repo.Create(ctx, chunk)

// Vector search
similarChunks, err := repo.SearchByVector(ctx, queryEmbedding, "tenant-1", 5)
```

### 5. ExperienceRepository

Experience repository data access layer, storing and managing agent execution experiences.

#### Main Methods

| Method | Description |
|--------|-------------|
| `Create(ctx, experience)` | Create a new experience record |
| `GetByID(ctx, id)` | Get experience record by ID |
| `Update(ctx, experience)` | Update experience record |
| `Delete(ctx, id)` | Delete experience record |
| `ListByAgent(ctx, agentID, tenantID, limit)` | List experiences for an agent |
| `ListByTaskType(ctx, taskType, tenantID, limit)` | List experiences by task type |
| `SearchByVector(ctx, embedding, tenantID, limit)` | Vector similarity search |
| `GetSuccessRate(ctx, agentID, tenantID)` | Get success rate statistics |
| `UpdateEmbedding(ctx, id, embedding, model, version)` | Update experience embedding |

#### Usage Example

```go
repo := repositories.NewExperienceRepository(db)

// Create experience record
experience := &storage_models.Experience{
    TenantID:         "tenant-1",
    AgentID:          "agent-1",
    TaskType:         "chat",
    TaskInput:        map[string]interface{}{"query": "test"},
    TaskOutput:       map[string]interface{}{"response": "test response"},
    Success:          true,
    Embedding:        createTestEmbedding(),
    EmbeddingModel:   "e5-large",
    EmbeddingVersion: 1,
    CreatedAt:        time.Now(),
}
err := repo.Create(ctx, experience)
```

### 6. SecretRepository

Secret management data access layer, providing encrypted storage for sensitive data.

#### Main Methods

| Method | Description |
|--------|-------------|
| `Set(ctx, key, value, tenantID)` | Store secret (encrypted) |
| `Get(ctx, key, tenantID)` | Get secret (decrypted) |
| `Delete(ctx, key, tenantID)` | Delete secret |
| `List(ctx, tenantID)` | List all secrets (without values) |
| `SetWithExpiration(ctx, key, value, tenantID, expiresAt)` | Store secret with expiration |
| `UpdateMetadata(ctx, key, tenantID, metadata)` | Update secret metadata |
| `CleanupExpired(ctx)` | Clean up expired secrets |
| `RotateKey(ctx, newKey)` | Rotate encryption key |
| `Export(ctx, tenantID)` | Export secrets (backup) |
| `Import(ctx, tenantID, data, format)` | Import secrets (restore) |
| `GetKeyVersion(ctx, key, tenantID)` | Get secret key version |

#### Usage Example

```go
encryptionKey := make([]byte, 32) // 32 bytes for AES-256-GCM
repo := repositories.NewSecretRepository(db, encryptionKey)

// Store secret
err := repo.Set(ctx, "api_key", "sk-1234567890", "tenant-1")

// Get secret
value, err := repo.Get(ctx, "api_key", "tenant-1")

// Store secret with expiration
expiresAt := time.Now().Add(30 * 24 * time.Hour)
err = repo.SetWithExpiration(ctx, "temp_key", "temp-value", "tenant-1", expiresAt)
```

## Error Handling

All Repository methods return standard error types:

- `errors.ErrInvalidArgument`: Invalid argument
- `errors.ErrRecordNotFound`: Record not found
- `errors.ErrNoTransaction`: Transaction required but not available
- `errors.ErrSecretExpired`: Secret has expired

## Test Coverage

Current test coverage: 75.0%

Test coverage includes:
- Normal path tests
- Edge case tests
- Error path tests
- Concurrent operation tests
- Tenant isolation tests

## Performance Considerations

- Uses prepared statements to prevent SQL injection
- Supports batch operations to reduce database round trips
- Uses indexes to optimize query performance
- Supports connection pooling and transactions
- Vector search optimized with pgvector

## Security

- All database operations support context cancellation
- Secrets encrypted with AES-256-GCM
- Supports tenant isolation
- Input validation and parameterized queries
- Regular cleanup of expired data

## Future Plans

- [ ] Add caching layer support
- [ ] Implement read-write separation
- [ ] Support more vector search algorithms
- [ ] Add performance monitoring and logging
- [ ] Support data migration and version management